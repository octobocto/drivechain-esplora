package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/octobocto/drivechain-esplora/internal/api"
	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/chain/thunder"
	"github.com/octobocto/drivechain-esplora/internal/e2e"
	"github.com/octobocto/drivechain-esplora/internal/index"
	"github.com/octobocto/drivechain-esplora/internal/rpc"
	"github.com/octobocto/drivechain-esplora/internal/service"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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

func value(sats uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"Value":%d}`, sats))
}

type harness struct {
	node    *e2e.FakeNode
	store   *store.Store
	syncer  *index.Syncer
	mempool *index.MempoolSyncer
	api     *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set TEST_DATABASE_URL to run the end to end tests")
	}

	ctx := context.Background()
	name := "e2e_" + sanitize(t.Name())

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	for _, stmt := range []string{`DROP DATABASE IF EXISTS ` + name, `CREATE DATABASE ` + name} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = conn.Close(ctx)

	dsn := replaceDatabase(admin, name)
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Init(ctx, "thunder", chain.Regtest); err != nil {
		t.Fatalf("init store: %v", err)
	}

	node := e2e.NewFakeNode()
	t.Cleanup(node.Close)

	nodes := service.New("node", quiet(), func(context.Context) (*chain.Node, error) {
		return chain.NewNode(rpc.New(node.URL())), nil
	}, nil)
	stores := service.New("database", quiet(), func(context.Context) (*store.Store, error) {
		return st, nil
	}, nil)

	server := httptest.NewServer(
		api.NewServer(stores, index.NewBroadcaster(nodes), nil, index.NewWithdrawals(nodes), quiet()).Handler())
	t.Cleanup(server.Close)

	return &harness{
		node:    node,
		store:   st,
		syncer:  index.NewSyncer(nodes, stores, thunder.Decoder{}, nil, quiet()),
		mempool: index.NewMempoolSyncer(nodes, stores, thunder.Decoder{}, quiet()),
		api:     server,
	}
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

func (h *harness) sync(t *testing.T) {
	t.Helper()
	if err := h.syncer.Once(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func (h *harness) syncMempool(t *testing.T) {
	t.Helper()
	if err := h.mempool.Once(context.Background()); err != nil {
		t.Fatalf("mempool sync: %v", err)
	}
}

func (h *harness) get(t *testing.T, path string, out any) int {
	t.Helper()
	resp, err := http.Get(h.api.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("GET %s decode %s: %v", path, body, err)
		}
	}
	return resp.StatusCode
}

func (h *harness) text(t *testing.T, path string) (string, int) {
	t.Helper()
	resp, err := http.Get(h.api.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode
}

// The whole pipeline: a node serves blocks, the syncer indexes them, and the
// API answers an Esplora client from Postgres.
func TestChainReachesTheAPI(t *testing.T) {
	h := newHarness(t)

	alice, bob, depositor := addr(1), addr(2), addr(3)
	genesisHash, secondHash := hash(0xa0), hash(0xa1)
	merkle0, merkle1 := hash(0x80), hash(0x81)
	txid := hash(0x11)
	depositTxid := hash(0xdd)

	// Genesis pays alice through the block coinbase, which keys on the merkle
	// root rather than on any txid.
	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: merkle0, PrevMainHash: chain.BitcoinHash(hash(0x40))},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(50000)}}},
	}, chain.BlockIndex{})

	// Block one spends alice's coin to bob, and takes a mainchain deposit.
	prev := genesisHash
	h.node.AddBlock(secondHash, &chain.Block{
		Header: chain.Header{
			MerkleRoot: merkle1, PrevSideHash: &prev,
			PrevMainHash: chain.BitcoinHash(hash(0x41)),
		},
		Body: chain.Body{
			Transactions: []chain.Transaction{{
				Inputs: []chain.Input{{OutPoint: chain.OutPoint{
					Kind: chain.KindCoinbase, Source: merkle0, Vout: 0,
				}}},
				Outputs: []chain.Output{{Address: bob, Content: value(49000)}},
			}},
		},
	}, chain.BlockIndex{
		Txs: []chain.TxInfo{{Txid: txid, Size: 200, Raw: chain.Bytes{1, 2, 3}}},
		Deposits: []chain.Deposit{{
			OutPoint: chain.OutPoint{Kind: chain.KindDeposit, Source: depositTxid, Vout: 0},
			Output:   chain.Output{Address: depositor, Content: value(123456)},
		}},
	})

	h.sync(t)

	if got, status := h.text(t, "/blocks/tip/height"); status != 200 || got != "1" {
		t.Errorf("tip height = %q (status %d), want \"1\"", got, status)
	}

	// Alice's coin is spent, so her balance is zero but her history holds it.
	var aliceInfo api.AddressInfo
	if status := h.get(t, "/address/"+alice.String(), &aliceInfo); status != 200 {
		t.Fatalf("address info status = %d", status)
	}
	if aliceInfo.ChainStats.FundedTxoSum != 50000 {
		t.Errorf("alice funded = %d, want 50000", aliceInfo.ChainStats.FundedTxoSum)
	}
	if aliceInfo.ChainStats.SpentTxoSum != 50000 {
		t.Errorf("alice spent = %d, want 50000", aliceInfo.ChainStats.SpentTxoSum)
	}

	var aliceUTXOs []api.UTXO
	h.get(t, "/address/"+alice.String()+"/utxo", &aliceUTXOs)
	if len(aliceUTXOs) != 0 {
		t.Errorf("alice holds %d utxos after her coin is spent", len(aliceUTXOs))
	}

	var bobUTXOs []api.UTXO
	h.get(t, "/address/"+bob.String()+"/utxo", &bobUTXOs)
	if len(bobUTXOs) != 1 || bobUTXOs[0].Value != 49000 {
		t.Fatalf("bob utxos = %+v, want one worth 49000", bobUTXOs)
	}
	if bobUTXOs[0].Txid != txid.String() {
		t.Errorf("bob utxo txid = %s, want %s", bobUTXOs[0].Txid, txid)
	}

	// A deposit is a first class coin, and its txid is a mainchain txid, so it
	// reads in mainchain byte order.
	var depositUTXOs []api.UTXO
	if status := h.get(t, "/address/"+depositor.String()+"/utxo", &depositUTXOs); status != 200 {
		t.Fatalf("deposit utxo status = %d", status)
	}
	if len(depositUTXOs) != 1 || depositUTXOs[0].Value != 123456 {
		t.Fatalf("deposit utxos = %+v, want one worth 123456", depositUTXOs)
	}
	if depositUTXOs[0].OutpointKind != "deposit" {
		t.Errorf("deposit kind = %q, want \"deposit\"", depositUTXOs[0].OutpointKind)
	}
	wantMainTxid := chain.BitcoinHash(depositTxid).String()
	if depositUTXOs[0].Txid != wantMainTxid {
		t.Errorf("deposit txid = %s, want the mainchain order %s",
			depositUTXOs[0].Txid, wantMainTxid)
	}

	// The deposit lookup by mainchain txid answers too.
	var byMainTxid []api.UTXO
	if status := h.get(t, "/deposit/"+wantMainTxid, &byMainTxid); status != 200 {
		t.Fatalf("deposit lookup status = %d", status)
	}
	if len(byMainTxid) != 1 {
		t.Errorf("deposit lookup returned %d rows, want 1", len(byMainTxid))
	}

	// A transaction carries its prevout, which a client dereferences with no
	// nil check.
	var tx api.Tx
	if status := h.get(t, "/tx/"+txid.String(), &tx); status != 200 {
		t.Fatalf("tx status = %d", status)
	}
	if len(tx.Vin) != 1 || tx.Vin[0].Prevout == nil {
		t.Fatalf("tx vin = %+v, want one input with a prevout", tx.Vin)
	}
	if tx.Vin[0].Prevout.Value != 50000 {
		t.Errorf("prevout value = %d, want 50000", tx.Vin[0].Prevout.Value)
	}
	if !tx.Vin[0].IsCoinbase {
		t.Error("a coinbase input does not read as a coinbase")
	}
	if tx.Fee != 1000 {
		t.Errorf("fee = %d, want 1000", tx.Fee)
	}
	if tx.Weight != tx.Size*4 {
		t.Errorf("weight %d is not four times size %d", tx.Weight, tx.Size)
	}
	if tx.Vout[0].ScriptPubKeyType != api.ScriptPubKeyType {
		t.Errorf("scriptpubkey type = %q", tx.Vout[0].ScriptPubKeyType)
	}
	if tx.Vout[0].ScriptPubKeyAddress != bob.String() {
		t.Errorf("scriptpubkey address = %q, want %q",
			tx.Vout[0].ScriptPubKeyAddress, bob)
	}

	// /fee-estimates must answer, because a client calls it before every send.
	var fees map[string]float64
	if status := h.get(t, "/fee-estimates", &fees); status != 200 {
		t.Fatalf("fee estimates status = %d", status)
	}
	if fees["1"] != api.FeeRateSatPerVByte {
		t.Errorf("fee for one block = %v, want %v", fees["1"], api.FeeRateSatPerVByte)
	}
}

// A reorg must return every balance to what it was before the dropped block.
func TestReorgRewindsTheAPI(t *testing.T) {
	h := newHarness(t)

	alice, bob := addr(1), addr(2)
	genesisHash := hash(0xa0)
	merkle0 := hash(0x80)

	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: merkle0, PrevMainHash: chain.BitcoinHash(hash(0x40))},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(50000)}}},
	}, chain.BlockIndex{})
	h.sync(t)

	var before api.AddressInfo
	h.get(t, "/address/"+alice.String(), &before)

	// A block spends alice's coin to bob.
	prev := genesisHash
	forked := hash(0xb1)
	h.node.AddBlock(forked, &chain.Block{
		Header: chain.Header{
			MerkleRoot: hash(0x81), PrevSideHash: &prev,
			PrevMainHash: chain.BitcoinHash(hash(0x41)),
		},
		Body: chain.Body{Transactions: []chain.Transaction{{
			Inputs: []chain.Input{{OutPoint: chain.OutPoint{
				Kind: chain.KindCoinbase, Source: merkle0, Vout: 0,
			}}},
			Outputs: []chain.Output{{Address: bob, Content: value(49000)}},
		}}},
	}, chain.BlockIndex{Txs: []chain.TxInfo{{Txid: hash(0x11), Size: 200, Raw: chain.Bytes{1}}}})
	h.sync(t)

	var spent api.AddressInfo
	h.get(t, "/address/"+alice.String(), &spent)
	if spent.ChainStats.SpentTxoSum != 50000 {
		t.Fatalf("alice spent = %d before the reorg, want 50000",
			spent.ChainStats.SpentTxoSum)
	}

	// The node drops that block and builds a different one.
	h.node.Reorg(0)
	replacement := hash(0xc1)
	h.node.AddBlock(replacement, &chain.Block{
		Header: chain.Header{
			MerkleRoot: hash(0x82), PrevSideHash: &prev,
			PrevMainHash: chain.BitcoinHash(hash(0x42)),
		},
		Body: chain.Body{Coinbase: []chain.Output{{Address: bob, Content: value(7000)}}},
	}, chain.BlockIndex{})
	h.sync(t)

	var after api.AddressInfo
	h.get(t, "/address/"+alice.String(), &after)
	if after.ChainStats != before.ChainStats {
		t.Errorf("alice after the reorg = %+v, want %+v",
			after.ChainStats, before.ChainStats)
	}

	var aliceUTXOs []api.UTXO
	h.get(t, "/address/"+alice.String()+"/utxo", &aliceUTXOs)
	if len(aliceUTXOs) != 1 || aliceUTXOs[0].Value != 50000 {
		t.Errorf("alice utxos after the reorg = %+v, want her coin back", aliceUTXOs)
	}

	if got, _ := h.text(t, "/blocks/tip/hash"); got != replacement.String() {
		t.Errorf("tip = %s, want the replacement block %s", got, replacement)
	}
}

// The API answers 503 while the database is down, and never crashes.
func TestAPISurvivesADownDatabase(t *testing.T) {
	stores := service.New("database", quiet(), func(context.Context) (*store.Store, error) {
		return nil, fmt.Errorf("connection refused")
	}, nil)
	server := httptest.NewServer(api.NewServer(stores, nil, nil, nil, quiet()).Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/blocks/tip/height")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// A light wallet has no node, so it broadcasts through the index. The
// transaction must reach the node exactly as the wallet signed it.
func TestBroadcastReachesTheNode(t *testing.T) {
	h := newHarness(t)

	const signed = `{"transaction":{"inputs":[],"outputs":[]},"authorizations":[]}`
	resp, err := http.Post(h.api.URL+"/tx", "application/json", strings.NewReader(signed))
	if err != nil {
		t.Fatalf("post the transaction: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tx answered %d: %s", resp.StatusCode, body)
	}
	if len(strings.TrimSpace(string(body))) != 64 {
		t.Errorf("POST /tx answered %q, want a txid", body)
	}

	got := h.node.Submitted()
	if len(got) != 1 {
		t.Fatalf("the node got %d transactions, want 1", len(got))
	}
	var want, have any
	_ = json.Unmarshal([]byte(signed), &want)
	_ = json.Unmarshal(got[0], &have)
	if !reflect.DeepEqual(want, have) {
		t.Errorf("the node got %s, want %s", got[0], signed)
	}
}

// A body that is not JSON cannot be a transaction, and the caller must read
// that rather than a node error.
func TestBroadcastRefusesANonJSONBody(t *testing.T) {
	h := newHarness(t)

	resp, err := http.Post(h.api.URL+"/tx", "text/plain", strings.NewReader("deadbeef"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /tx answered %d, want 400", resp.StatusCode)
	}
	if len(h.node.Submitted()) != 0 {
		t.Error("a bad body reached the node")
	}
}

// A payment must reach a wallet before a block carries it. The node holds the
// transaction, the poller reads it, and every address route shows it at once.
func TestUnconfirmedPaymentReachesTheAPI(t *testing.T) {
	h := newHarness(t)

	alice, bob := addr(1), addr(2)
	genesisHash, merkle := hash(0xa0), hash(0x80)
	coin := chain.OutPoint{Kind: chain.KindCoinbase, Source: merkle, Vout: 0}

	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: merkle, PrevMainHash: chain.BitcoinHash(hash(0x40))},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(50000)}}},
	}, chain.BlockIndex{})
	h.sync(t)

	// Alice pays bob 40000 and keeps 9000. The 1000 that is left is the fee.
	tx := chain.Transaction{
		Inputs: []chain.Input{{OutPoint: coin, LeafHash: make(chain.Bytes, 32)}},
		Outputs: []chain.Output{
			{Address: bob, Content: value(40000)},
			{Address: alice, Content: value(9000)},
		},
	}
	info, err := thunder.Decoder{}.IdentifyTx(tx)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	txid := info.Txid.String()

	h.node.SetMempool([]chain.Transaction{tx})
	h.syncMempool(t)

	// Bob sees the payment, and it counts as unconfirmed.
	var bobInfo api.AddressInfo
	if status := h.get(t, "/address/"+bob.String(), &bobInfo); status != 200 {
		t.Fatalf("bob address status = %d", status)
	}
	if bobInfo.MempoolStats.FundedTxoSum != 40000 || bobInfo.MempoolStats.TxCount != 1 {
		t.Errorf("bob mempool stats = %+v, want 40000 over one transaction",
			bobInfo.MempoolStats)
	}
	if bobInfo.ChainStats.FundedTxoSum != 0 {
		t.Errorf("bob chain stats = %+v, want nothing mined", bobInfo.ChainStats)
	}

	var bobUTXOs []api.UTXO
	h.get(t, "/address/"+bob.String()+"/utxo", &bobUTXOs)
	if len(bobUTXOs) != 1 || bobUTXOs[0].Value != 40000 {
		t.Fatalf("bob utxos = %+v, want one worth 40000", bobUTXOs)
	}
	if bobUTXOs[0].Status.Confirmed {
		t.Error("bob's new coin reads as confirmed")
	}
	if bobUTXOs[0].Txid != txid {
		t.Errorf("bob utxo txid = %s, want %s", bobUTXOs[0].Txid, txid)
	}

	// Alice loses the coin she spent at once, and holds her change instead.
	var aliceUTXOs []api.UTXO
	h.get(t, "/address/"+alice.String()+"/utxo", &aliceUTXOs)
	if len(aliceUTXOs) != 1 || aliceUTXOs[0].Value != 9000 {
		t.Fatalf("alice utxos = %+v, want her 9000 change alone", aliceUTXOs)
	}
	if aliceUTXOs[0].Status.Confirmed {
		t.Error("alice's change reads as confirmed")
	}

	var aliceInfo api.AddressInfo
	h.get(t, "/address/"+alice.String(), &aliceInfo)
	if aliceInfo.MempoolStats.SpentTxoSum != 50000 {
		t.Errorf("alice mempool spent = %d, want 50000", aliceInfo.MempoolStats.SpentTxoSum)
	}

	// The history carries the unconfirmed row first, the way an Esplora client
	// reads it.
	var history []api.Tx
	h.get(t, "/address/"+bob.String()+"/txs", &history)
	if len(history) != 1 || history[0].Txid != txid {
		t.Fatalf("bob history = %+v, want the unconfirmed payment", history)
	}
	if history[0].Status.Confirmed {
		t.Error("the history row reads as confirmed")
	}
	if history[0].Fee != 1000 {
		t.Errorf("fee = %d, want 1000", history[0].Fee)
	}
	if len(history[0].Vin) != 1 || history[0].Vin[0].Prevout.Value != 50000 {
		t.Errorf("vin = %+v, want the coin alice spent", history[0].Vin)
	}

	var pending []api.Tx
	h.get(t, "/address/"+bob.String()+"/txs/mempool", &pending)
	if len(pending) != 1 || pending[0].Txid != txid {
		t.Errorf("bob mempool history = %+v, want the payment", pending)
	}

	// A transaction read finds it, and reports that no block carries it.
	var one api.Tx
	if status := h.get(t, "/tx/"+txid, &one); status != 200 {
		t.Fatalf("tx status = %d", status)
	}
	if one.Status.Confirmed || one.Size != int(info.Size) {
		t.Errorf("tx = %+v, want an unconfirmed row of size %d", one, info.Size)
	}
	var txStatus api.Status
	h.get(t, "/tx/"+txid+"/status", &txStatus)
	if txStatus.Confirmed {
		t.Error("the status route reads as confirmed")
	}
	if raw, status := h.text(t, "/tx/"+txid+"/hex"); status != 200 || raw == "" {
		t.Errorf("hex = %q (status %d), want the encoding", raw, status)
	}

	// The mempool routes count the same transaction.
	var summary struct {
		Count    int   `json:"count"`
		Vsize    int64 `json:"vsize"`
		TotalFee int64 `json:"total_fee"`
	}
	h.get(t, "/mempool", &summary)
	if summary.Count != 1 || summary.TotalFee != 1000 {
		t.Errorf("summary = %+v, want one transaction paying 1000", summary)
	}
	var txids []string
	h.get(t, "/mempool/txids", &txids)
	if len(txids) != 1 || txids[0] != txid {
		t.Errorf("mempool txids = %v, want the payment", txids)
	}
	var recent []api.RecentTx
	h.get(t, "/mempool/recent", &recent)
	if len(recent) != 1 || recent[0].Value != 49000 {
		t.Errorf("recent = %+v, want one row paying out 49000", recent)
	}
}

// A block that carries the payment must not add it a second time. The balance
// stays the same and only the status changes.
func TestABlockTakesOverTheUnconfirmedPayment(t *testing.T) {
	h := newHarness(t)

	alice, bob := addr(1), addr(2)
	genesisHash, merkle0 := hash(0xa0), hash(0x80)
	coin := chain.OutPoint{Kind: chain.KindCoinbase, Source: merkle0, Vout: 0}

	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: merkle0, PrevMainHash: chain.BitcoinHash(hash(0x40))},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(50000)}}},
	}, chain.BlockIndex{})
	h.sync(t)

	tx := chain.Transaction{
		Inputs:  []chain.Input{{OutPoint: coin, LeafHash: make(chain.Bytes, 32)}},
		Outputs: []chain.Output{{Address: bob, Content: value(49000)}},
	}
	info, err := thunder.Decoder{}.IdentifyTx(tx)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}

	h.node.SetMempool([]chain.Transaction{tx})
	h.syncMempool(t)

	// The same transaction now arrives in a block, before the poller runs
	// again. Both tables would hold the coin, and bob would read it twice.
	prev := genesisHash
	h.node.AddBlock(hash(0xa1), &chain.Block{
		Header: chain.Header{
			MerkleRoot: hash(0x81), PrevSideHash: &prev,
			PrevMainHash: chain.BitcoinHash(hash(0x41)),
		},
		Body: chain.Body{Transactions: []chain.Transaction{tx}},
	}, chain.BlockIndex{
		Txs: []chain.TxInfo{{Txid: info.Txid, Size: info.Size, Raw: info.Raw}},
	})
	h.sync(t)

	var utxos []api.UTXO
	h.get(t, "/address/"+bob.String()+"/utxo", &utxos)
	if len(utxos) != 1 || utxos[0].Value != 49000 {
		t.Fatalf("bob utxos = %+v, want the one coin", utxos)
	}
	if !utxos[0].Status.Confirmed {
		t.Error("the coin still reads as unconfirmed after its block")
	}

	var info2 api.AddressInfo
	h.get(t, "/address/"+bob.String(), &info2)
	if info2.ChainStats.FundedTxoSum != 49000 {
		t.Errorf("bob chain stats = %+v, want 49000", info2.ChainStats)
	}
	if info2.MempoolStats.FundedTxoCount != 0 {
		t.Errorf("bob mempool stats = %+v, want nothing left", info2.MempoolStats)
	}

	var history []api.Tx
	h.get(t, "/address/"+bob.String()+"/txs", &history)
	if len(history) != 1 {
		t.Fatalf("bob history holds %d rows, want the payment one time", len(history))
	}

	// The poller runs again and finds the node holds nothing.
	h.node.SetMempool(nil)
	h.syncMempool(t)
	var summary struct {
		Count int `json:"count"`
	}
	h.get(t, "/mempool", &summary)
	if summary.Count != 0 {
		t.Errorf("the mempool still holds %d transactions", summary.Count)
	}
}
