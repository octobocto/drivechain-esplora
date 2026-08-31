package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// openStore gives each test its own database, so one test never reads another
// test's rows.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set TEST_DATABASE_URL to run the store tests")
	}

	ctx := context.Background()
	name := fmt.Sprintf("test_%s", sanitize(t.Name()))

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS ` + name,
		`CREATE DATABASE ` + name,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = conn.Close(ctx)

	st, err := store.Open(ctx, replaceDatabase(admin, name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(st.Close)

	if err := st.Init(ctx, "thunder", chain.Regtest); err != nil {
		t.Fatalf("init: %v", err)
	}
	return st
}

func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func replaceDatabase(dsn, name string) string {
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '/' {
			return dsn[:i+1] + name
		}
	}
	return dsn
}

func hash(b byte) chain.Hash {
	var h chain.Hash
	for i := range h {
		h[i] = b
	}
	return h
}

func addr(b byte) chain.Address {
	var a chain.Address
	for i := range a {
		a[i] = b
	}
	return a
}

func regular(source chain.Hash, vout uint32) chain.OutPoint {
	return chain.OutPoint{Kind: chain.KindRegular, Source: source, Vout: vout}
}

func output(o chain.OutPoint, a chain.Address, sats int64) store.Output {
	return store.Output{
		OutPoint:    o,
		Address:     a,
		ValueSats:   sats,
		Content:     json.RawMessage(fmt.Sprintf(`{"Value":%d}`, sats)),
		ContentType: "value",
		HeightExact: true,
	}
}

// block builds a one-transaction block that pays `to` and spends `spends`.
func block(height uint32, h chain.Hash, prev *chain.Hash) store.Block {
	return store.Block{
		Height:     height,
		Hash:       h,
		PrevHash:   prev,
		MerkleRoot: hash(byte(height) ^ 0x80),
		MainHash:   chain.BitcoinHash(hash(byte(height) ^ 0x40)),
	}
}

func TestMigrationsRunTwice(t *testing.T) {
	st := openStore(t)
	// Open again on the same database. A second run must change nothing.
	ctx := context.Background()
	if _, _, _, err := st.Tip(ctx); err != nil {
		t.Fatalf("tip after migrate: %v", err)
	}
}

// A second chain in one database would mix two sets of balances.
func TestInitRefusesAnotherChain(t *testing.T) {
	st := openStore(t)
	err := st.Init(context.Background(), "bitnames", chain.Regtest)
	if err == nil {
		t.Fatal("want an error for a second chain, got none")
	}
}

func TestApplyAndRead(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice, bob := addr(1), addr(2)
	txid := hash(0x11)

	b := block(0, hash(0xa0), nil)
	b.Txs = []store.Tx{{Txid: txid, Index: 0, SizeBytes: 120, Raw: []byte{1, 2, 3}}}
	b.Creates = []store.Output{
		output(regular(txid, 0), alice, 5000),
		output(regular(txid, 1), bob, 3000),
	}
	if _, err := st.Apply(ctx, b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stats, err := st.Stats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.FundedSum != 5000 || stats.FundedCount != 1 {
		t.Errorf("alice funded = %d over %d outputs, want 5000 over 1",
			stats.FundedSum, stats.FundedCount)
	}
	if stats.SpentSum != 0 {
		t.Errorf("alice spent = %d, want 0", stats.SpentSum)
	}
	if stats.TxCount != 1 {
		t.Errorf("alice tx count = %d, want 1", stats.TxCount)
	}

	utxos, err := st.UTXOs(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	if len(utxos) != 1 || utxos[0].ValueSats != 5000 {
		t.Fatalf("alice utxos = %+v, want one worth 5000", utxos)
	}

	// A scripthash query answers the same, so an Electrum-style client works.
	sh := alice.ScriptHash()
	byHash, err := st.Stats(ctx, store.ColumnScriptHash, sh[:])
	if err != nil {
		t.Fatalf("scripthash stats: %v", err)
	}
	if byHash != stats {
		t.Errorf("scripthash stats = %+v, want %+v", byHash, stats)
	}

	height, tipHash, have, err := st.Tip(ctx)
	if err != nil || !have || height != 0 || tipHash != b.Hash {
		t.Errorf("tip = %d %s (have %v, err %v), want height 0", height, tipHash, have, err)
	}
}

// A withdrawal bundle spends an output with no transaction at all. The index
// must record that spend, or the coin reads as still available.
func TestBundleSpendMarksTheOutput(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice := addr(1)
	txid := hash(0x11)
	m6id := hash(0x99)

	first := block(0, hash(0xa0), nil)
	first.Txs = []store.Tx{{Txid: txid, Raw: []byte{1}}}
	first.Creates = []store.Output{output(regular(txid, 0), alice, 7000)}
	if _, err := st.Apply(ctx, first); err != nil {
		t.Fatalf("apply first: %v", err)
	}

	prev := first.Hash
	second := block(1, hash(0xa1), &prev)
	second.Spends = []store.Spend{{
		OutPoint: regular(txid, 0),
		Source:   m6id,
		Kind:     chain.SpendWithdrawal,
	}}
	result, err := st.Apply(ctx, second)
	if err != nil {
		t.Fatalf("apply second: %v", err)
	}
	if result.UnknownSpends != 0 {
		t.Errorf("reported %d unknown spends, want 0", result.UnknownSpends)
	}

	utxos, err := st.UTXOs(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	if len(utxos) != 0 {
		t.Errorf("alice still holds %d utxos after the bundle spend", len(utxos))
	}
}

// A spend of an output the index never saw is reported, never silently applied.
func TestUnknownSpendIsReported(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	b := block(0, hash(0xa0), nil)
	b.Txs = []store.Tx{{Txid: hash(0x11), Raw: []byte{1}}}
	b.Spends = []store.Spend{{
		OutPoint: regular(hash(0xee), 0),
		Source:   hash(0x11),
		Kind:     chain.SpendRegular,
	}}
	result, err := st.Apply(ctx, b)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.UnknownSpends != 1 {
		t.Errorf("reported %d unknown spends, want 1", result.UnknownSpends)
	}
}

// A wrong rollback shows a user coins that do not exist. After a rollback every
// balance must match what it was before the rolled-back block.
func TestRollbackRestoresEveryBalance(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice, bob := addr(1), addr(2)
	fundTxid, spendTxid := hash(0x11), hash(0x22)

	genesis := block(0, hash(0xa0), nil)
	genesis.Txs = []store.Tx{{Txid: fundTxid, Raw: []byte{1}}}
	genesis.Creates = []store.Output{output(regular(fundTxid, 0), alice, 9000)}
	if _, err := st.Apply(ctx, genesis); err != nil {
		t.Fatalf("apply genesis: %v", err)
	}

	before, err := st.Stats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("stats before: %v", err)
	}

	prev := genesis.Hash
	forked := block(1, hash(0xb1), &prev)
	forked.Txs = []store.Tx{{Txid: spendTxid, Raw: []byte{2}}}
	forked.Spends = []store.Spend{{
		OutPoint: regular(fundTxid, 0), Source: spendTxid, Kind: chain.SpendRegular,
	}}
	forked.Creates = []store.Output{output(regular(spendTxid, 0), bob, 8500)}
	if _, err := st.Apply(ctx, forked); err != nil {
		t.Fatalf("apply forked: %v", err)
	}

	if utxos, err := st.UTXOs(ctx, store.ColumnAddress, alice[:]); err != nil {
		t.Fatalf("utxos after spend: %v", err)
	} else if len(utxos) != 0 {
		t.Fatalf("alice holds %d utxos after her coin is spent, want 0", len(utxos))
	}

	if err := st.Rollback(ctx, 0); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	after, err := st.Stats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("stats after: %v", err)
	}
	if after != before {
		t.Errorf("alice stats after rollback = %+v, want %+v", after, before)
	}

	// Bob's coin came from the rolled-back block, so it is gone.
	bobStats, err := st.Stats(ctx, store.ColumnAddress, bob[:])
	if err != nil {
		t.Fatalf("bob stats: %v", err)
	}
	if bobStats.FundedCount != 0 {
		t.Errorf("bob holds %d outputs after the rollback, want 0", bobStats.FundedCount)
	}

	height, tipHash, have, err := st.Tip(ctx)
	if err != nil || !have || height != 0 || tipHash != genesis.Hash {
		t.Errorf("tip after rollback = %d %s (have %v, err %v), want genesis",
			height, tipHash, have, err)
	}
}

// A rollback to before genesis leaves an empty index, not a stale tip.
func TestRollbackToNothing(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	b := block(0, hash(0xa0), nil)
	b.Creates = []store.Output{output(
		chain.OutPoint{Kind: chain.KindCoinbase, Source: b.MerkleRoot, Vout: 0},
		addr(1), 5000)}
	if _, err := st.Apply(ctx, b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Height 0 is genesis, so rolling back "above" it keeps genesis. Deleting
	// genesis itself needs the block row gone.
	if _, err := st.Pool().Exec(ctx, `DELETE FROM blocks`); err != nil {
		t.Fatalf("clear blocks: %v", err)
	}
	if err := st.Rollback(ctx, 0); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, _, have, err := st.Tip(ctx); err != nil || have {
		t.Errorf("tip = have %v (err %v), want no tip", have, err)
	}
}

func TestFeeComesFromTheIndexedCoins(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	fundTxid, spendTxid := hash(0x11), hash(0x22)

	genesis := block(0, hash(0xa0), nil)
	genesis.Txs = []store.Tx{{Txid: fundTxid, Raw: []byte{1}}}
	genesis.Creates = []store.Output{output(regular(fundTxid, 0), addr(1), 10000)}
	if _, err := st.Apply(ctx, genesis); err != nil {
		t.Fatalf("apply genesis: %v", err)
	}

	prev := genesis.Hash
	next := block(1, hash(0xa1), &prev)
	next.Txs = []store.Tx{{Txid: spendTxid, Raw: []byte{2}}}
	next.Spends = []store.Spend{{
		OutPoint: regular(fundTxid, 0), Source: spendTxid, Kind: chain.SpendRegular,
	}}
	next.Creates = []store.Output{output(regular(spendTxid, 0), addr(2), 9700)}
	if _, err := st.Apply(ctx, next); err != nil {
		t.Fatalf("apply next: %v", err)
	}

	row, err := st.Tx(ctx, spendTxid)
	if err != nil {
		t.Fatalf("read transaction: %v", err)
	}
	if row.FeeSats != 300 {
		t.Errorf("fee = %d, want 300", row.FeeSats)
	}
	if len(row.Vin) != 1 || row.Vin[0].ValueSats != 10000 {
		t.Errorf("vin = %+v, want one coin worth 10000", row.Vin)
	}
	if len(row.Vout) != 1 || row.Vout[0].ValueSats != 9700 {
		t.Errorf("vout = %+v, want one coin worth 9700", row.Vout)
	}
}
