package index

import (
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/chain/thunder"
)

// PrepareMempool holds the same rule as Prepare, and it needs no block index:
// the chain names each transaction from its own encoding.
func TestPrepareMempoolPlacesEveryCoin(t *testing.T) {
	prevOut := chain.OutPoint{Kind: chain.KindRegular, Source: hash(0xdd), Vout: 3}
	txs := []chain.Transaction{{
		Inputs:  []chain.Input{{OutPoint: prevOut, LeafHash: make(chain.Bytes, 32)}},
		Outputs: []chain.Output{{Address: addr(1), Content: value(900)}},
	}}

	got, err := PrepareMempool(txs, thunder.Decoder{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if len(got.Txs) != 1 {
		t.Fatalf("holds %d transactions, want 1", len(got.Txs))
	}
	txid := got.Txs[0].Txid
	if txid == (chain.Hash{}) {
		t.Error("the transaction carries no txid")
	}
	if got.Txs[0].SizeBytes != len(got.Txs[0].Raw)/8 {
		t.Errorf("size = %d over a %d byte encoding",
			got.Txs[0].SizeBytes, len(got.Txs[0].Raw))
	}

	if len(got.Spends) != 1 || got.Spends[0].OutPoint != prevOut {
		t.Fatalf("spends = %+v, want the one input", got.Spends)
	}
	if got.Spends[0].Source != txid || got.Spends[0].Kind != chain.SpendRegular {
		t.Errorf("spend names %+v, want transaction %s", got.Spends[0], txid)
	}

	want := chain.OutPoint{Kind: chain.KindRegular, Source: txid, Vout: 0}
	if len(got.Creates) != 1 || got.Creates[0].OutPoint != want {
		t.Fatalf("creates = %+v, want one output of %s", got.Creates, txid)
	}
	if got.Creates[0].ValueSats != 900 || got.Creates[0].Address != addr(1) {
		t.Errorf("output = %+v, want 900 sats for the first address", got.Creates[0])
	}
}

// Two transactions in one snapshot keep their own coins, and the second one
// spends the first.
func TestPrepareMempoolChainsTwoTransactions(t *testing.T) {
	first := chain.Transaction{
		Inputs: []chain.Input{{
			OutPoint: chain.OutPoint{Kind: chain.KindRegular, Source: hash(0xdd)},
			LeafHash: make(chain.Bytes, 32),
		}},
		Outputs: []chain.Output{{Address: addr(1), Content: value(1000)}},
	}
	firstInfo, err := thunder.Decoder{}.IdentifyTx(first)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	second := chain.Transaction{
		Inputs: []chain.Input{{
			OutPoint: chain.OutPoint{Kind: chain.KindRegular, Source: firstInfo.Txid},
			LeafHash: make(chain.Bytes, 32),
		}},
		Outputs: []chain.Output{{Address: addr(2), Content: value(800)}},
	}

	got, err := PrepareMempool([]chain.Transaction{first, second}, thunder.Decoder{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(got.Txs) != 2 || got.Txs[0].Txid != firstInfo.Txid {
		t.Fatalf("transactions = %+v, want the two in body order", got.Txs)
	}
	if got.Txs[0].Index != 0 || got.Txs[1].Index != 1 {
		t.Errorf("indexes = %d and %d, want 0 and 1", got.Txs[0].Index, got.Txs[1].Index)
	}
	if got.Spends[1].OutPoint.Source != firstInfo.Txid {
		t.Errorf("the second transaction spends %+v, want the first one",
			got.Spends[1].OutPoint)
	}
}

func TestPrepareMempoolHoldsNothingForAnEmptyBody(t *testing.T) {
	got, err := PrepareMempool(nil, thunder.Decoder{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(got.Txs) != 0 || len(got.Creates) != 0 || len(got.Spends) != 0 {
		t.Errorf("empty body prepared %+v", got)
	}
}

func TestPrepareMempoolRejectsAnOutputItCannotRead(t *testing.T) {
	txs := []chain.Transaction{{
		Outputs: []chain.Output{{Address: addr(1), Content: []byte(`{"Nonsense":1}`)}},
	}}
	if _, err := PrepareMempool(txs, thunder.Decoder{}); err == nil {
		t.Fatal("want an error for an output no decoder reads, got none")
	}
}
