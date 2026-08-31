package chain

import (
	"encoding/json"
	"testing"
)

const testHashHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// An input is a two-element pair, and its leaf hash carries no hex adapter, so
// a node may render it as an array of numbers.
func TestInputAcceptsBothLeafHashForms(t *testing.T) {
	numbers := "[" + repeatByte(32) + "]"

	cases := map[string]string{
		"array of numbers": `[{"Regular":{"txid":"` + testHashHex + `","vout":1}},` + numbers + `]`,
		"hex string":       `[{"Regular":{"txid":"` + testHashHex + `","vout":1}},"` + testHashHex + `"]`,
	}

	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			var in Input
			if err := json.Unmarshal([]byte(wire), &in); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if in.OutPoint.Kind != KindRegular || in.OutPoint.Vout != 1 {
				t.Errorf("outpoint = %+v, want a regular outpoint at vout 1", in.OutPoint)
			}
			if len(in.LeafHash) != 32 {
				t.Errorf("leaf hash is %d bytes, want 32", len(in.LeafHash))
			}
		})
	}
}

func TestInputRejectsWrongPairLength(t *testing.T) {
	var in Input
	if err := json.Unmarshal([]byte(`[{"Regular":{"txid":"`+testHashHex+`","vout":0}}]`), &in); err == nil {
		t.Fatal("want an error for a one-element pair, got none")
	}
}

// The body holds one flat signature list, one per input, in transaction order.
// A wrong split attributes a signature to the wrong sender.
func TestAuthorizationsFor(t *testing.T) {
	body := Body{
		Transactions: []Transaction{
			{Inputs: make([]Input, 2)},
			{Inputs: make([]Input, 1)},
			{Inputs: make([]Input, 3)},
		},
		Authorizations: make([]Authorization, 6),
	}
	for i := range body.Authorizations {
		body.Authorizations[i].Signature = Bytes{byte(i)}
	}

	cases := []struct {
		txIndex int
		want    []byte
	}{
		{txIndex: 0, want: []byte{0, 1}},
		{txIndex: 1, want: []byte{2}},
		{txIndex: 2, want: []byte{3, 4, 5}},
	}

	for _, tc := range cases {
		got, err := body.AuthorizationsFor(tc.txIndex)
		if err != nil {
			t.Fatalf("transaction %d: %v", tc.txIndex, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("transaction %d has %d signatures, want %d", tc.txIndex, len(got), len(tc.want))
		}
		for i, want := range tc.want {
			if got[i].Signature[0] != want {
				t.Errorf("transaction %d signature %d = %d, want %d",
					tc.txIndex, i, got[i].Signature[0], want)
			}
		}
	}
}

func TestAuthorizationsForRejectsShortList(t *testing.T) {
	body := Body{
		Transactions:   []Transaction{{Inputs: make([]Input, 2)}},
		Authorizations: make([]Authorization, 1),
	}
	if _, err := body.AuthorizationsFor(0); err == nil {
		t.Fatal("want an error when the body has too few signatures, got none")
	}
	if _, err := body.AuthorizationsFor(1); err == nil {
		t.Fatal("want an error for a transaction index outside the body, got none")
	}
}

// Genesis is the block whose PrevSideHash is null. The walk stops there.
func TestHeaderGenesisHasNoParent(t *testing.T) {
	const wire = `{"merkle_root":"` + testHashHex + `","prev_side_hash":null,` +
		`"prev_main_hash":"` + testHashHex + `","roots":[]}`
	var h Header
	if err := json.Unmarshal([]byte(wire), &h); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h.PrevSideHash != nil {
		t.Errorf("PrevSideHash = %v, want nil at genesis", h.PrevSideHash)
	}
}

func repeatByte(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		out += "1"
	}
	return out
}

// The encoder must produce the pair the node sends, so a round trip holds.
func TestInputRoundTrip(t *testing.T) {
	source, err := ParseHash(testHashHex)
	if err != nil {
		t.Fatalf("parse hash: %v", err)
	}
	want := Input{
		OutPoint: OutPoint{Kind: KindRegular, Source: source, Vout: 4},
		LeafHash: Bytes{1, 2, 3},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if raw[0] != '[' {
		t.Errorf("encoded as %s, want a two element pair", raw)
	}

	var got Input
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OutPoint != want.OutPoint || string(got.LeafHash) != string(want.LeafHash) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// The node serializes a Rust tuple, so a deposit and a bundle spend arrive as
// two element pairs, not as objects. A real node caught this; a hand written
// fixture did not.
func TestBlockIndexDecodesTuplePairs(t *testing.T) {
	const wire = `{
		"txs": [{"txid":"` + testHashHex + `","size":180,"raw":"0102"}],
		"deposits": [
			[{"Deposit":"` + testHashHex + `:0"},
			 {"address":"pEbmSWqJdBuPadRGm8tDY4USQK","content":{"Value":500000000}}]
		],
		"bundle_spends": [
			[{"Regular":{"txid":"` + testHashHex + `","vout":2}}, "` + testHashHex + `"]
		]
	}`

	var index BlockIndex
	if err := json.Unmarshal([]byte(wire), &index); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(index.Txs) != 1 || index.Txs[0].Size != 180 {
		t.Errorf("txs = %+v", index.Txs)
	}
	if len(index.Deposits) != 1 {
		t.Fatalf("got %d deposits, want 1", len(index.Deposits))
	}
	if index.Deposits[0].OutPoint.Kind != KindDeposit {
		t.Errorf("deposit outpoint kind = %s, want deposit", index.Deposits[0].OutPoint.Kind)
	}
	if len(index.BundleSpends) != 1 {
		t.Fatalf("got %d bundle spends, want 1", len(index.BundleSpends))
	}
	if index.BundleSpends[0].OutPoint.Vout != 2 {
		t.Errorf("bundle spend vout = %d, want 2", index.BundleSpends[0].OutPoint.Vout)
	}

	// The encoder must produce the same pair form, or the test node lies.
	raw, err := json.Marshal(index.Deposits[0])
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if raw[0] != '[' {
		t.Errorf("encoded a deposit as %s, want a pair", raw)
	}
}

func TestPairRejectsAWrongLength(t *testing.T) {
	var d Deposit
	if err := json.Unmarshal([]byte(`[{"Regular":{"txid":"`+testHashHex+`","vout":0}}]`), &d); err == nil {
		t.Fatal("want an error for a one element pair, got none")
	}
}
