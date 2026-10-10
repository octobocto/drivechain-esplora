package index

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/chain/thunder"
	"github.com/octobocto/drivechain-esplora/internal/chain/truthcoin"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

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
	raw, err := json.Marshal(map[string]uint64{"Value": sats})
	if err != nil {
		panic(err)
	}
	return raw
}

// A block creates coins three ways and spends them two ways. Prepare must place
// every one of them, or a balance is wrong.
func TestPrepareCoversEveryPath(t *testing.T) {
	merkle := hash(0xaa)
	blockHash := hash(0xbb)
	txid := hash(0xcc)
	prevOut := chain.OutPoint{Kind: chain.KindRegular, Source: hash(0xdd), Vout: 0}
	depositOut := chain.OutPoint{Kind: chain.KindDeposit, Source: hash(0xee), Vout: 1}
	bundleOut := chain.OutPoint{Kind: chain.KindRegular, Source: hash(0xff), Vout: 2}
	m6id := chain.BitcoinHash(hash(0x11))

	block := &chain.Block{
		Header: chain.Header{MerkleRoot: merkle, PrevMainHash: chain.BitcoinHash(hash(0x22))},
		Body: chain.Body{
			Coinbase: chain.Coinbase{Outputs: []chain.Output{{Address: addr(1), Content: value(500)}}},
			Transactions: []chain.Transaction{{
				Inputs:  []chain.Input{{OutPoint: prevOut}},
				Outputs: []chain.Output{{Address: addr(2), Content: value(900)}},
			}},
		},
	}
	blockIndex := chain.BlockIndex{
		Txs:          []chain.TxInfo{{Txid: txid, Size: 180}},
		Deposits:     []chain.Deposit{{OutPoint: depositOut, Output: chain.Output{Address: addr(3), Content: value(7000)}}},
		BundleSpends: []chain.BundleSpend{{OutPoint: bundleOut, M6id: m6id}},
	}

	got, err := Prepare(12, blockHash, block, blockIndex, thunder.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if got.Height != 12 || got.Hash != blockHash || got.MerkleRoot != merkle {
		t.Errorf("header fields = %+v", got)
	}

	if len(got.Creates) != 3 {
		t.Fatalf("creates %d outputs, want 3", len(got.Creates))
	}
	// A bare coinbase list keys on the header merkle root.
	coinbase := got.Creates[0]
	want := chain.OutPoint{Kind: chain.KindCoinbase, Source: merkle, Vout: 0}
	if coinbase.OutPoint != want {
		t.Errorf("coinbase outpoint = %+v, want %+v", coinbase.OutPoint, want)
	}
	if coinbase.ValueSats != 500 {
		t.Errorf("coinbase value = %d, want 500", coinbase.ValueSats)
	}

	txOut := got.Creates[1]
	want = chain.OutPoint{Kind: chain.KindRegular, Source: txid, Vout: 0}
	if txOut.OutPoint != want {
		t.Errorf("transaction outpoint = %+v, want %+v", txOut.OutPoint, want)
	}

	deposit := got.Creates[2]
	if deposit.OutPoint != depositOut {
		t.Errorf("deposit outpoint = %+v, want %+v", deposit.OutPoint, depositOut)
	}
	if deposit.ValueSats != 7000 {
		t.Errorf("deposit value = %d, want 7000", deposit.ValueSats)
	}

	if len(got.Spends) != 2 {
		t.Fatalf("records %d spends, want 2", len(got.Spends))
	}
	if got.Spends[0].OutPoint != prevOut || got.Spends[0].Source != txid ||
		got.Spends[0].Kind != chain.SpendRegular {
		t.Errorf("transaction spend = %+v", got.Spends[0])
	}
	// A bundle spend has no transaction. The m6id names it instead.
	if got.Spends[1].OutPoint != bundleOut || got.Spends[1].Source != chain.Hash(m6id) ||
		got.Spends[1].Kind != chain.SpendWithdrawal {
		t.Errorf("bundle spend = %+v", got.Spends[1])
	}

	if len(got.Txs) != 1 || got.Txs[0].Txid != txid || got.Txs[0].SizeBytes != 180 {
		t.Errorf("transactions = %+v", got.Txs)
	}
}

// A coinbase object keys each output on the coinbase txid, which is what a
// transaction that spends it names.
func TestPrepareKeysACoinbaseObjectOnItsTxid(t *testing.T) {
	parent := hash(0x33)
	block := &chain.Block{
		Header: chain.Header{MerkleRoot: hash(0xaa), PrevSideHash: &parent, PrevMainHash: chain.BitcoinHash(hash(0x22))},
		Body: chain.Body{Coinbase: chain.Coinbase{
			Outputs:     []chain.Output{{Address: addr(1), Content: value(500)}, {Address: addr(2), Content: value(600)}},
			KeyedByTxid: true,
		}},
	}

	got, err := Prepare(5, hash(0xbb), block, chain.BlockIndex{}, thunder.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(got.Creates) != 2 {
		t.Fatalf("creates %d outputs, want 2", len(got.Creates))
	}
	txid := chain.CoinbaseTxid(block.Header)
	for vout, create := range got.Creates {
		want := chain.OutPoint{Kind: chain.KindCoinbase, Source: txid, Vout: uint32(vout)}
		if create.OutPoint != want {
			t.Errorf("coinbase output %d = %+v, want %+v", vout, create.OutPoint, want)
		}
	}
}

// A withdrawal output removes both its payout and its mainchain fee.
func TestPrepareCountsTheWithdrawalFee(t *testing.T) {
	raw := json.RawMessage(
		`{"Withdrawal":{"value_sats":1000,"main_fee_sats":250,"main_address":"tb1q"}}`)
	block := &chain.Block{
		Body: chain.Body{Coinbase: chain.Coinbase{Outputs: []chain.Output{{Address: addr(1), Content: raw}}}},
	}

	got, err := Prepare(1, hash(1), block, chain.BlockIndex{}, thunder.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got.Creates[0].ValueSats != 1250 {
		t.Errorf("withdrawal value = %d, want 1250", got.Creates[0].ValueSats)
	}
	if got.Creates[0].ContentType != "withdrawal" {
		t.Errorf("content type = %q, want %q", got.Creates[0].ContentType, "withdrawal")
	}
}

// Genesis has no parent. The walk stops there rather than reading past it.
func TestPrepareKeepsGenesisWithoutAParent(t *testing.T) {
	block := &chain.Block{}
	got, err := Prepare(0, hash(1), block, chain.BlockIndex{}, thunder.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got.PrevHash != nil {
		t.Errorf("PrevHash = %v, want nil at genesis", got.PrevHash)
	}
}

// A mismatch between the body and its index would silently attribute an output
// to the wrong transaction.
func TestPrepareRejectsAMismatchedIndex(t *testing.T) {
	block := &chain.Block{
		Body: chain.Body{Transactions: []chain.Transaction{{}, {}}},
	}
	blockIndex := chain.BlockIndex{Txs: []chain.TxInfo{{Txid: hash(1)}}}
	if _, err := Prepare(1, hash(2), block, blockIndex, thunder.Decoder{}, nil); err == nil {
		t.Fatal("want an error for a short index, got none")
	}
}

func TestPrepareRejectsANonDepositInTheDepositList(t *testing.T) {
	blockIndex := chain.BlockIndex{
		Deposits: []chain.Deposit{{
			OutPoint: chain.OutPoint{Kind: chain.KindRegular, Source: hash(1)},
			Output:   chain.Output{Address: addr(1), Content: value(1)},
		}},
	}
	if _, err := Prepare(1, hash(2), &chain.Block{}, blockIndex, thunder.Decoder{}, nil); err == nil {
		t.Fatal("want an error for a regular outpoint in the deposit list, got none")
	}
}

// Truthcoin market code creates and removes outputs with no transaction, and a
// trade that failed its price limit stays in the body with no effect. The
// fixture holds the shape the node's get_block_index answers.
func TestPrepareAppliesTruthcoinMarketEntries(t *testing.T) {
	raw, err := os.ReadFile("../chain/testdata/truthcoin_market_block_index.json")
	if err != nil {
		t.Fatalf("read block index: %v", err)
	}
	var answer struct {
		Result chain.BlockIndex `json:"result"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode block index: %v", err)
	}
	blockIndex := answer.Result

	trade := chain.Transaction{
		Inputs:  []chain.Input{{OutPoint: chain.OutPoint{Kind: chain.KindRegular, Source: hash(0x51), Vout: 0}}},
		Outputs: []chain.Output{{Address: addr(5), Content: value(800)}},
	}
	skipped := chain.Transaction{
		Inputs:  []chain.Input{{OutPoint: chain.OutPoint{Kind: chain.KindRegular, Source: hash(0x52), Vout: 0}}},
		Outputs: []chain.Output{{Address: addr(6), Content: value(900)}},
	}
	block := &chain.Block{
		Header: chain.Header{MerkleRoot: hash(0xaa), PrevMainHash: chain.BitcoinHash(hash(0x22))},
		Body:   chain.Body{Transactions: []chain.Transaction{trade, skipped}},
	}
	blockHash := hash(0xbb)

	got, err := Prepare(5, blockHash, block, blockIndex, truthcoin.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if len(got.Txs) != 2 {
		t.Fatalf("writes %d transactions, want both", len(got.Txs))
	}

	type create struct {
		kind        chain.OutPointKind
		sats        int64
		contentType string
	}
	wantCreates := []create{
		{chain.KindRegular, 800, "value"},
		{chain.KindPayout, 3000, "value"},
		{chain.KindMarketFunds, 996970, "market_treasury"},
		{chain.KindMarketFunds, 30, "market_author_fee"},
	}
	if len(got.Creates) != len(wantCreates) {
		t.Fatalf("creates %d outputs, want %d: %+v", len(got.Creates), len(wantCreates), got.Creates)
	}
	for i, want := range wantCreates {
		c := got.Creates[i]
		if c.OutPoint.Kind != want.kind || c.ValueSats != want.sats || c.ContentType != want.contentType {
			t.Errorf("create %d = %s %d sats %q, want %s %d sats %q",
				i, c.OutPoint, c.ValueSats, c.ContentType, want.kind, want.sats, want.contentType)
		}
	}
	if got.Creates[1].OutPoint != blockIndex.MarketCreates[0].OutPoint {
		t.Errorf("payout outpoint = %s, want %s", got.Creates[1].OutPoint, blockIndex.MarketCreates[0].OutPoint)
	}

	wantSpends := []store.Spend{
		{OutPoint: trade.Inputs[0].OutPoint, Source: blockIndex.Txs[0].Txid, Kind: chain.SpendRegular, Vin: 0},
		{OutPoint: blockIndex.MarketDeletes[0].OutPoint, Source: blockHash, Kind: chain.SpendMarket, Vin: 0},
		{OutPoint: blockIndex.MarketDeletes[1].OutPoint, Source: blockHash, Kind: chain.SpendMarket, Vin: 1},
	}
	if len(got.Spends) != len(wantSpends) {
		t.Fatalf("records %d spends, want %d: %+v", len(got.Spends), len(wantSpends), got.Spends)
	}
	for i, want := range wantSpends {
		if got.Spends[i] != want {
			t.Errorf("spend %d = %+v, want %+v", i, got.Spends[i], want)
		}
	}
	// Settlement removes the treasury this block created, so the index needs
	// both rows in one write.
	if got.Spends[2].OutPoint != got.Creates[2].OutPoint {
		t.Errorf("the second delete names %s, want the treasury the block created, %s",
			got.Spends[2].OutPoint, got.Creates[2].OutPoint)
	}
}
