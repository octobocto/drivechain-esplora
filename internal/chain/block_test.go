package chain

import (
	"encoding/json"
	"os"
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

// A chain with no utreexo, such as bitnames, sends the outpoint alone. An
// earlier decoder read the pair form only, so every such block failed.
func TestInputReadsAnOutPointWithNoLeafHash(t *testing.T) {
	const wire = `{"Regular":{"txid":"` + testHashHex + `","vout":3}}`

	var in Input
	if err := json.Unmarshal([]byte(wire), &in); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if in.OutPoint.Kind != KindRegular || in.OutPoint.Vout != 3 {
		t.Errorf("outpoint = %+v, want a regular outpoint at vout 3", in.OutPoint)
	}
	if in.OutPoint.Source.String() != testHashHex {
		t.Errorf("source = %s, want %s", in.OutPoint.Source, testHashHex)
	}
	if len(in.LeafHash) != 0 {
		t.Errorf("leaf hash is %d bytes, want none", len(in.LeafHash))
	}
}

// An encode must give back the shape the node sent, so a chain with no utreexo
// never reads a leaf hash it does not have.
func TestInputRoundTripsBothShapes(t *testing.T) {
	cases := map[string]string{
		"pair":          `[{"Regular":{"txid":"` + testHashHex + `","vout":1}},"` + testHashHex + `"]`,
		"bare outpoint": `{"Regular":{"txid":"` + testHashHex + `","vout":1}}`,
	}

	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			var in Input
			if err := json.Unmarshal([]byte(wire), &in); err != nil {
				t.Fatalf("decode: %v", err)
			}
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if string(raw) != wire {
				t.Errorf("round trip = %s, want %s", raw, wire)
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

// A whole bitnames block: the body carries a transaction, and its input names
// the outpoint alone. get_block failed on such a block and stalled the index.
func TestBlockReadsABodyWithNoLeafHash(t *testing.T) {
	const wire = `{
		"header": {"merkle_root":"` + testHashHex + `","prev_side_hash":"` + testHashHex + `",
			"prev_main_hash":"` + testHashHex + `"},
		"body": {
			"coinbase": [],
			"transactions": [{
				"inputs": [{"Regular":{"txid":"` + testHashHex + `","vout":0}}],
				"outputs": [{"address":"pEbmSWqJdBuPadRGm8tDY4USQK","content":{"Value":500000000}}]
			}],
			"authorizations": [{"verifying_key":"0102","signature":"0304"}]
		}
	}`

	var block Block
	if err := json.Unmarshal([]byte(wire), &block); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(block.Body.Transactions) != 1 {
		t.Fatalf("got %d transactions, want 1", len(block.Body.Transactions))
	}
	inputs := block.Body.Transactions[0].Inputs
	if len(inputs) != 1 {
		t.Fatalf("got %d inputs, want 1", len(inputs))
	}
	if inputs[0].OutPoint.Kind != KindRegular || inputs[0].OutPoint.Vout != 0 {
		t.Errorf("outpoint = %+v, want a regular outpoint at vout 0", inputs[0].OutPoint)
	}
	if len(inputs[0].LeafHash) != 0 {
		t.Errorf("leaf hash is %d bytes, want none", len(inputs[0].LeafHash))
	}
}

// readBlockFile reads a whole get_block answer that a live node gave.
func readBlockFile(t *testing.T, name string) Block {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var answer struct {
		Result Block `json:"result"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return answer.Result
}

// Two live nodes, two renderings of one field. bitnames writes a key as bech32
// and a signature as prefixed hex; thunder writes both as arrays of numbers.
func TestRealBlocksCarryEveryAuthorizationForm(t *testing.T) {
	bitnames := readBlockFile(t, "bitnames_block.json")
	if len(bitnames.Body.Authorizations) != 2 {
		t.Fatalf("the bitnames block holds %d authorizations, want 2",
			len(bitnames.Body.Authorizations))
	}
	first := bitnames.Body.Authorizations[0]
	const key = "bn-svk179g6xez5ud4xvhjlgugc4zu8tlt0ap2lm7qh7gh5mzr43zqua9kq4d90yn"
	if first.VerifyingKey.Text != key {
		t.Errorf("bitnames key = %q, want %q", first.VerifyingKey.Text, key)
	}
	if first.VerifyingKey.Bytes != nil {
		t.Errorf("bitnames key holds %s as bytes, want the text alone",
			first.VerifyingKey.Bytes)
	}
	if len(first.Signature.Bytes) != 64 {
		t.Errorf("bitnames signature is %d bytes, want 64", len(first.Signature.Bytes))
	}

	thunder := readBlockFile(t, "thunder_block.json")
	if len(thunder.Body.Authorizations) != 1 {
		t.Fatalf("the thunder block holds %d authorizations, want 1",
			len(thunder.Body.Authorizations))
	}
	only := thunder.Body.Authorizations[0]
	if len(only.VerifyingKey.Bytes) != 32 || len(only.Signature.Bytes) != 64 {
		t.Errorf("thunder authorization = %+v, want a 32 byte key and a 64 byte signature", only)
	}
	if only.VerifyingKey.Text != "" {
		t.Errorf("thunder key holds text %q, want none", only.VerifyingKey.Text)
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
		body.Authorizations[i].Signature = ByteString{Bytes: Bytes{byte(i)}}
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
			if got[i].Signature.Bytes[0] != want {
				t.Errorf("transaction %d signature %d = %d, want %d",
					tc.txIndex, i, got[i].Signature.Bytes[0], want)
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
func TestBlockIndexDecodesNamedFields(t *testing.T) {
	const wire = `{
		"txs": [{"txid":"` + testHashHex + `","size":180,"raw":"0102"}],
		"deposits": [
			{"outpoint":{"Deposit":"` + testHashHex + `:0"},
			 "output":{"address":"pEbmSWqJdBuPadRGm8tDY4USQK","content":{"Value":500000000}}}
		],
		"bundle_spends": [
			{"outpoint":{"Regular":{"txid":"` + testHashHex + `","vout":2}},
			 "m6id":"` + testHashHex + `"}
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
}

// A deposit names its fields. An earlier decoder read a two element array, so
// the first block that carried a deposit failed and stalled the whole index.
func TestADepositIsAnObjectNotAPair(t *testing.T) {
	const pair = `[{"Deposit":"` + testHashHex + `:0"},{"address":"pEbmSWqJdBuPadRGm8tDY4USQK","content":{"Value":1}}]`
	var d Deposit
	if err := json.Unmarshal([]byte(pair), &d); err == nil {
		t.Error("a pair decoded as a deposit, so the object form is not enforced")
	}

	raw, err := json.Marshal(Deposit{})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if raw[0] != '{' {
		t.Errorf("encoded a deposit as %s, want an object", raw)
	}
}

func TestPairRejectsAWrongLength(t *testing.T) {
	var d Deposit
	if err := json.Unmarshal([]byte(`[{"Regular":{"txid":"`+testHashHex+`","vout":0}}]`), &d); err == nil {
		t.Fatal("want an error for a one element pair, got none")
	}
}
