package index

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/chain/bitnames"
	"github.com/octobocto/drivechain-esplora/internal/chain/thunder"
	"github.com/octobocto/drivechain-esplora/internal/chain/truthcoin"
)

// readRealBlock reads a get_block answer a live node gave. The chain package
// keeps the files, because it owns the decoders they exercise.
func readRealBlock(t *testing.T, name string) *chain.Block {
	t.Helper()
	raw, err := os.ReadFile("../chain/testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var answer struct {
		Result chain.Block `json:"result"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &answer.Result
}

// namesFor builds the index a node would answer beside the block, one entry
// per transaction, so Prepare reads a whole block.
func namesFor(block *chain.Block) chain.BlockIndex {
	var index chain.BlockIndex
	for i := range block.Body.Transactions {
		index.Txs = append(index.Txs, chain.TxInfo{
			Txid: hash(byte(i + 1)), Size: 180, Raw: chain.Bytes{byte(i)},
		})
	}
	return index
}

// A bitnames output names its content with a bare string, and thunder names it
// with an object. Prepare must read a live block of either chain.
func TestPrepareReadsARealBitnamesBlock(t *testing.T) {
	block := readRealBlock(t, "bitnames_block.json")

	got, err := Prepare(35, hash(0xb1), block, namesFor(block), bitnames.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	wantTypes := []string{"bitname_reservation", "bitname", "bitname_reservation", "bitname"}
	if len(got.Creates) != len(wantTypes) {
		t.Fatalf("creates %d outputs, want %d", len(got.Creates), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got.Creates[i].ContentType != want {
			t.Errorf("output %d content type = %q, want %q", i, got.Creates[i].ContentType, want)
		}
		if got.Creates[i].ValueSats != 0 {
			t.Errorf("output %d holds %d sats, want none", i, got.Creates[i].ValueSats)
		}
	}
	// The store writes the payload as the node sent it, so a bare string stays
	// a bare string in the content column.
	if string(got.Creates[0].Content) != `"BitNameReservation"` {
		t.Errorf("content = %s, want the bare string the node sent", got.Creates[0].Content)
	}
	if len(got.Spends) != 2 {
		t.Errorf("records %d spends, want 2", len(got.Spends))
	}
}

func TestPrepareReadsARealThunderBlock(t *testing.T) {
	block := readRealBlock(t, "thunder_block.json")

	got, err := Prepare(47, hash(0xc1), block, namesFor(block), thunder.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if len(got.Creates) != 3 {
		t.Fatalf("creates %d outputs, want 3", len(got.Creates))
	}
	for i, create := range got.Creates {
		if create.ContentType != "value" {
			t.Errorf("output %d content type = %q, want %q", i, create.ContentType, "value")
		}
	}
	if got.Creates[0].ValueSats != 1000 {
		t.Errorf("coinbase value = %d, want 1000", got.Creates[0].ValueSats)
	}
	if len(got.Spends) != 1 {
		t.Errorf("records %d spends, want 1", len(got.Spends))
	}
}

// Betanet block 2 creates four markets. The index the node answered names the
// real txids, so every output keys on the outpoint the node holds.
func TestPrepareReadsARealTruthcoinBlock(t *testing.T) {
	block := readRealBlock(t, "truthcoin_betanet_block.json")
	raw, err := os.ReadFile("../chain/testdata/truthcoin_betanet_block_index.json")
	if err != nil {
		t.Fatalf("read block index: %v", err)
	}
	var answer struct {
		Result chain.BlockIndex `json:"result"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode block index: %v", err)
	}

	got, err := Prepare(2, hash(0xd2), block, answer.Result, truthcoin.Decoder{}, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if len(got.Txs) != 4 {
		t.Fatalf("writes %d transactions, want 4", len(got.Txs))
	}
	if len(got.Creates) != 9 {
		t.Fatalf("creates %d outputs, want 9", len(got.Creates))
	}
	// The coinbase claims the four fees of 4504 sats.
	coinbase := got.Creates[0]
	if coinbase.OutPoint.Kind != chain.KindCoinbase || coinbase.ValueSats != 4*4504 {
		t.Errorf("coinbase = %s with %d sats, want a coinbase of %d sats",
			coinbase.OutPoint, coinbase.ValueSats, 4*4504)
	}
	for i, create := range got.Creates[1:] {
		wantType := "value"
		if i%2 == 0 {
			wantType = "market_treasury"
			if create.ValueSats != 1000000 {
				t.Errorf("output %d holds %d sats, want 1000000", i, create.ValueSats)
			}
		}
		if create.ContentType != wantType {
			t.Errorf("output %d content type = %q, want %q", i, create.ContentType, wantType)
		}
		want := chain.OutPoint{Kind: chain.KindRegular, Source: got.Txs[i/2].Txid, Vout: uint32(i % 2)}
		if create.OutPoint != want {
			t.Errorf("output %d outpoint = %s, want %s", i, create.OutPoint, want)
		}
	}
	if len(got.Spends) != 5 {
		t.Fatalf("records %d spends, want 5", len(got.Spends))
	}
	// Each market creation after the first spends the change of the one before.
	for i := 1; i < 4; i++ {
		want := chain.OutPoint{Kind: chain.KindRegular, Source: got.Txs[i-1].Txid, Vout: 1}
		if got.Spends[i+1].OutPoint != want {
			t.Errorf("transaction %d spends %s, want %s", i, got.Spends[i+1].OutPoint, want)
		}
	}
}
