package store_test

import (
	"context"
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// mempoolTx builds one unconfirmed transaction row.
func mempoolTx(txid chain.Hash, index, size int) store.MempoolTx {
	return store.MempoolTx{Txid: txid, Index: index, SizeBytes: size, Raw: []byte{byte(index)}}
}

// A pass replaces the whole snapshot, so a transaction the node dropped leaves
// the index with it.
func TestReplaceMempoolSwapsTheWholeSet(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice := addr(1)
	first, second := hash(0x11), hash(0x22)

	if err := st.ReplaceMempool(ctx, store.Mempool{
		Txs:     []store.MempoolTx{mempoolTx(first, 0, 100)},
		Creates: []store.Output{output(regular(first, 0), alice, 4000)},
	}); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	stats, err := st.MempoolStats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.FundedSum != 4000 || stats.FundedCount != 1 || stats.TxCount != 1 {
		t.Errorf("stats = %+v, want one output worth 4000", stats)
	}

	// The node drops the first transaction and holds another one.
	if err := st.ReplaceMempool(ctx, store.Mempool{
		Txs:     []store.MempoolTx{mempoolTx(second, 0, 120)},
		Creates: []store.Output{output(regular(second, 0), alice, 900)},
	}); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	txids, err := st.MempoolTxids(ctx)
	if err != nil {
		t.Fatalf("txids: %v", err)
	}
	if len(txids) != 1 || txids[0] != second {
		t.Fatalf("txids = %v, want the second transaction alone", txids)
	}

	utxos, err := st.MempoolUTXOs(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	if len(utxos) != 1 || utxos[0].ValueSats != 900 || !utxos[0].Unconfirmed {
		t.Errorf("utxos = %+v, want one unconfirmed coin worth 900", utxos)
	}
}

// A transaction both passes hold keeps the time the index first saw it, so the
// arrival order a caller reads stays true.
func TestReplaceMempoolKeepsTheFirstSeenTime(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	txid := hash(0x11)
	pool := store.Mempool{Txs: []store.MempoolTx{mempoolTx(txid, 0, 100)}}
	if err := st.ReplaceMempool(ctx, pool); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	var first int64
	err := st.Pool().QueryRow(ctx,
		`SELECT first_seen FROM mempool_txs WHERE txid = $1`, txid[:]).Scan(&first)
	if err != nil {
		t.Fatalf("read first_seen: %v", err)
	}
	// Move the row back, so a kept time is visible.
	if _, err := st.Pool().Exec(ctx,
		`UPDATE mempool_txs SET first_seen = $1`, first-60); err != nil {
		t.Fatalf("age the row: %v", err)
	}

	if err := st.ReplaceMempool(ctx, pool); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	var second int64
	err = st.Pool().QueryRow(ctx,
		`SELECT first_seen FROM mempool_txs WHERE txid = $1`, txid[:]).Scan(&second)
	if err != nil {
		t.Fatalf("read first_seen again: %v", err)
	}
	if second != first-60 {
		t.Errorf("first_seen = %d, want the older %d", second, first-60)
	}
}

// A wallet that spends a confirmed coin must lose it from its balance at once,
// not at the next block.
func TestMempoolSpendOfAConfirmedOutput(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice, bob := addr(1), addr(2)
	fundTxid, spendTxid := hash(0x11), hash(0x22)

	b := block(0, hash(0xa0), nil)
	b.Txs = []store.Tx{{Txid: fundTxid, Raw: []byte{1}}}
	b.Creates = []store.Output{output(regular(fundTxid, 0), alice, 10000)}
	if _, err := st.Apply(ctx, b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if err := st.ReplaceMempool(ctx, store.Mempool{
		Txs: []store.MempoolTx{mempoolTx(spendTxid, 0, 100)},
		Spends: []store.Spend{{
			OutPoint: regular(fundTxid, 0), Source: spendTxid, Kind: chain.SpendRegular,
		}},
		Creates: []store.Output{output(regular(spendTxid, 0), bob, 9700)},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	stats, err := st.MempoolStats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("alice stats: %v", err)
	}
	if stats.SpentSum != 10000 || stats.SpentCount != 1 || stats.TxCount != 1 {
		t.Errorf("alice mempool stats = %+v, want one spend of 10000", stats)
	}

	spent, err := st.MempoolSpentOutpoints(ctx)
	if err != nil {
		t.Fatalf("spent outpoints: %v", err)
	}
	key := regular(fundTxid, 0).Key()
	if !spent[string(key[:])] {
		t.Error("the spent outpoint is missing, so a reader still counts the coin")
	}

	// The fee comes from the coins the snapshot names, on both sides.
	row, err := st.MempoolTxRow(ctx, spendTxid)
	if err != nil {
		t.Fatalf("read the transaction: %v", err)
	}
	if row.FeeSats != 300 {
		t.Errorf("fee = %d, want 300", row.FeeSats)
	}
	if !row.Unconfirmed {
		t.Error("the row reads as confirmed")
	}
	if len(row.Vin) != 1 || row.Vin[0].ValueSats != 10000 {
		t.Errorf("vin = %+v, want the confirmed coin", row.Vin)
	}
	if len(row.Vout) != 1 || row.Vout[0].ValueSats != 9700 {
		t.Errorf("vout = %+v, want one coin worth 9700", row.Vout)
	}

	txids, err := st.MempoolTxidsFor(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("alice txids: %v", err)
	}
	if len(txids) != 1 || txids[0] != spendTxid {
		t.Errorf("alice txids = %v, want the spend", txids)
	}
}

// A wallet that spends its own unconfirmed change makes one mempool
// transaction spend another. Counting the middle coin would report the money
// twice.
func TestMempoolChainOfTwoTransactions(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice, bob := addr(1), addr(2)
	fundTxid, first, second := hash(0x11), hash(0x22), hash(0x33)

	b := block(0, hash(0xa0), nil)
	b.Txs = []store.Tx{{Txid: fundTxid, Raw: []byte{1}}}
	b.Creates = []store.Output{output(regular(fundTxid, 0), alice, 10000)}
	if _, err := st.Apply(ctx, b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The first transaction pays alice her own change. The second spends that
	// change and pays bob.
	if err := st.ReplaceMempool(ctx, store.Mempool{
		Txs: []store.MempoolTx{mempoolTx(first, 0, 100), mempoolTx(second, 1, 100)},
		Spends: []store.Spend{
			{OutPoint: regular(fundTxid, 0), Source: first, Kind: chain.SpendRegular},
			{OutPoint: regular(first, 0), Source: second, Kind: chain.SpendRegular},
		},
		Creates: []store.Output{
			output(regular(first, 0), alice, 9000),
			output(regular(second, 0), bob, 8000),
		},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// Alice funded 9000 and spent 10000 and 9000. Her balance change is what
	// the two sums leave.
	stats, err := st.MempoolStats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("alice stats: %v", err)
	}
	if stats.FundedSum != 9000 || stats.SpentSum != 19000 {
		t.Errorf("alice stats = %+v, want 9000 funded and 19000 spent", stats)
	}
	if stats.TxCount != 2 {
		t.Errorf("alice touched %d transactions, want 2", stats.TxCount)
	}

	// The middle coin belongs to no balance: the second transaction takes it.
	utxos, err := st.MempoolUTXOs(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("alice utxos: %v", err)
	}
	if len(utxos) != 0 {
		t.Errorf("alice holds %d unconfirmed coins, want 0", len(utxos))
	}

	bobUTXOs, err := st.MempoolUTXOs(ctx, store.ColumnAddress, bob[:])
	if err != nil {
		t.Fatalf("bob utxos: %v", err)
	}
	if len(bobUTXOs) != 1 || bobUTXOs[0].ValueSats != 8000 {
		t.Errorf("bob utxos = %+v, want one coin worth 8000", bobUTXOs)
	}

	// The second transaction spends an unconfirmed coin, so its fee comes from
	// the snapshot itself.
	row, err := st.MempoolTxRow(ctx, second)
	if err != nil {
		t.Fatalf("read the second transaction: %v", err)
	}
	if row.FeeSats != 1000 {
		t.Errorf("fee = %d, want 1000", row.FeeSats)
	}

	count, vsize, fees, err := st.MempoolSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if count != 2 || vsize != 200 || fees != 2000 {
		t.Errorf("summary = %d transactions, %d bytes, %d sats; want 2, 200, 2000",
			count, vsize, fees)
	}

	recent, err := st.MempoolRecent(ctx, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("recent holds %d rows, want 2", len(recent))
	}
}

// A block that carries a transaction takes it out of the snapshot at the same
// time. A coin in both tables counts twice.
func TestApplyClearsWhatItConfirms(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	alice := addr(1)
	txid := hash(0x11)

	pool := store.Mempool{
		Txs:     []store.MempoolTx{mempoolTx(txid, 0, 100)},
		Creates: []store.Output{output(regular(txid, 0), alice, 4000)},
	}
	if err := st.ReplaceMempool(ctx, pool); err != nil {
		t.Fatalf("replace: %v", err)
	}

	b := block(0, hash(0xa0), nil)
	b.Txs = []store.Tx{{Txid: txid, Raw: []byte{1}}}
	b.Creates = []store.Output{output(regular(txid, 0), alice, 4000)}
	if _, err := st.Apply(ctx, b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stats, err := st.MempoolStats(ctx, store.ColumnAddress, alice[:])
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.FundedCount != 0 {
		t.Errorf("the snapshot still funds %d outputs the chain holds", stats.FundedCount)
	}
	if _, err := st.MempoolTxRow(ctx, txid); err == nil {
		t.Error("the snapshot still holds a transaction the block carries")
	}
}
