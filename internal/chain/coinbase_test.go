package chain

import (
	"encoding/json"
	"testing"
)

func mustHash(t *testing.T, text string) Hash {
	t.Helper()
	hash, err := ParseHash(text)
	if err != nil {
		t.Fatalf("parse hash %q: %v", text, err)
	}
	return hash
}

func mustBitcoinHash(t *testing.T, text string) BitcoinHash {
	t.Helper()
	var hash BitcoinHash
	if err := json.Unmarshal([]byte(`"`+text+`"`), &hash); err != nil {
		t.Fatalf("parse bitcoin hash %q: %v", text, err)
	}
	return hash
}

// The expected txids come from thunder_types::Coinbase::compute_txid at
// thunder-rust 0.18.1.
func TestCoinbaseTxidMatchesTheNode(t *testing.T) {
	parent := mustHash(t, "8b52259afff825b7ccbbb35bb00de2a8cad00a7ced10ca6c58db14a21da1ec74")
	header := Header{
		MerkleRoot:   mustHash(t, "812c97082cde521a6128a777b45d9221259e8e3e33d6f84b7b496ef43ca6bdbb"),
		PrevSideHash: &parent,
		PrevMainHash: mustBitcoinHash(t, "0000000000000000ca123028c7f364b1991861777bcd6f529da94e5f588e3c24"),
	}

	if got, want := CoinbaseTxid(header), mustHash(t, "942e334bc65263c10177119ea2aaff967d11ced3a7716f26c9f9aa288388deca"); got != want {
		t.Errorf("txid with a parent = %s, want %s", got, want)
	}

	header.PrevSideHash = nil
	if got, want := CoinbaseTxid(header), mustHash(t, "b285caa0115415ab856a425829c130233c629c436c1592a7546a5bad510a4dc6"); got != want {
		t.Errorf("genesis txid = %s, want %s", got, want)
	}
}

func TestLiveBetanetBlocksCarryACoinbaseObject(t *testing.T) {
	for _, name := range []string{"thunder_betanet_block.json", "bitnames_betanet_block.json"} {
		t.Run(name, func(t *testing.T) {
			block := readBlockFile(t, name)
			if !block.Body.Coinbase.KeyedByTxid {
				t.Fatal("coinbase is not keyed by txid")
			}
			if len(block.Body.Coinbase.Memo) != 0 || len(block.Body.Coinbase.Outputs) != 0 {
				t.Errorf("coinbase = %+v, want an empty memo and no outputs", block.Body.Coinbase)
			}
			if got, want := block.CoinbaseSource(), CoinbaseTxid(block.Header); got != want {
				t.Errorf("coinbase source = %s, want the txid %s", got, want)
			}
		})
	}
}

func TestCoinbaseReadsBothMemoForms(t *testing.T) {
	for name, memo := range map[string]string{"hex": `"0a0b"`, "numbers": `[10,11]`} {
		t.Run(name, func(t *testing.T) {
			wire := `{"memo":` + memo + `,"outputs":[{"address":"pEbmSWqJdBuPadRGm8tDY4USQK","content":{"Value":7}}]}`
			var coinbase Coinbase
			if err := json.Unmarshal([]byte(wire), &coinbase); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := coinbase.Memo.String(); got != "0a0b" {
				t.Errorf("memo = %s, want 0a0b", got)
			}
			if len(coinbase.Outputs) != 1 || !coinbase.KeyedByTxid {
				t.Errorf("coinbase = %+v, want one output keyed by txid", coinbase)
			}
		})
	}
}

func TestAnOlderCoinbaseKeysOnTheMerkleRoot(t *testing.T) {
	block := readBlockFile(t, "thunder_block.json")
	if block.Body.Coinbase.KeyedByTxid {
		t.Fatal("a bare coinbase list is keyed by txid")
	}
	if len(block.Body.Coinbase.Outputs) != 1 {
		t.Fatalf("got %d coinbase outputs, want 1", len(block.Body.Coinbase.Outputs))
	}
	if got := block.CoinbaseSource(); got != block.Header.MerkleRoot {
		t.Errorf("coinbase source = %s, want the merkle root %s", got, block.Header.MerkleRoot)
	}
}

func TestCoinbaseRoundTripsBothForms(t *testing.T) {
	for _, wire := range []string{
		`[]`,
		`{"memo":"0a","outputs":[]}`,
	} {
		var coinbase Coinbase
		if err := json.Unmarshal([]byte(wire), &coinbase); err != nil {
			t.Fatalf("decode %s: %v", wire, err)
		}
		out, err := json.Marshal(coinbase)
		if err != nil {
			t.Fatalf("encode %s: %v", wire, err)
		}
		if string(out) != wire {
			t.Errorf("round trip = %s, want %s", out, wire)
		}
	}
}

func TestACoinbaseOutPointReadsAnOlderMerkleRoot(t *testing.T) {
	var out OutPoint
	if err := json.Unmarshal([]byte(`{"Coinbase":{"merkle_root":"`+testHashHex+`","vout":2}}`), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := (OutPoint{Kind: KindCoinbase, Source: mustHash(t, testHashHex), Vout: 2}); out != want {
		t.Errorf("outpoint = %+v, want %+v", out, want)
	}
}

func TestACoinbaseOutPointNeedsASource(t *testing.T) {
	var out OutPoint
	if err := json.Unmarshal([]byte(`{"Coinbase":{"vout":2}}`), &out); err == nil {
		t.Fatal("want an error for a coinbase outpoint with no source, got none")
	}
}

// Truthcoin keys a coinbase output on the same txid as thunder. The first
// transaction of betanet block 2 spends the genesis coinbase by that txid.
func TestTruthcoinSpendsTheGenesisCoinbaseByTxid(t *testing.T) {
	genesis := Header{
		MerkleRoot:   mustHash(t, "984824aaa2883a8365cfa40df4b07b0f2caf2148256367a5e6147e331c575b1b"),
		PrevMainHash: mustBitcoinHash(t, "0000000000000000cc7325c7048d6e0cb6eb87c35e8032917cddf70dd992b832"),
	}
	block := readBlockFile(t, "truthcoin_betanet_block.json")
	spend := block.Body.Transactions[0].Inputs[0].OutPoint
	want := OutPoint{Kind: KindCoinbase, Source: CoinbaseTxid(genesis), Vout: 0}
	if spend != want {
		t.Errorf("first input = %s, want %s", spend, want)
	}
}
