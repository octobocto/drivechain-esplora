package thunder

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// The txid hashes these exact bytes. A change here changes every txid this
// index reports, with no other sign.
func TestEncodeTransactionBytes(t *testing.T) {
	var source chain.Hash
	for i := range source {
		source[i] = byte(i + 1)
	}
	var address chain.Address
	for i := range address {
		address[i] = 0x11
	}

	tx := chain.Transaction{
		Inputs: []chain.Input{{
			OutPoint: chain.OutPoint{Kind: chain.KindRegular, Source: source, Vout: 1},
			LeafHash: make(chain.Bytes, 32),
		}},
		Outputs: []chain.Output{{
			Address: address,
			Content: json.RawMessage(`{"Value":21000}`),
		}},
	}
	for i := range tx.Inputs[0].LeafHash {
		tx.Inputs[0].LeafHash[i] = 0xAA
	}

	got, err := encodeTx(tx)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := "01000000" + // one input
		"00" + // regular
		"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" +
		"01000000" + // vout 1
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"01000000" + // one output
		"1111111111111111111111111111111111111111" + // address
		"00" + // Value
		"0852000000000000" // 21000 sats, little endian

	if hex.EncodeToString(got) != want {
		t.Errorf("encoding =\n  %s\nwant\n  %s", hex.EncodeToString(got), want)
	}
}

// A withdrawal writes both amounts and the script its mainchain address pays.
func TestEncodeWithdrawalBytes(t *testing.T) {
	tx := chain.Transaction{Outputs: []chain.Output{{
		Content: json.RawMessage(`{"Withdrawal":{"value":1000,"main_fee":250,
			"main_address":"1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"}}`),
	}}}

	got, err := encodeTx(tx)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := "00000000" + // no inputs
		"01000000" + // one output
		"0000000000000000000000000000000000000000" + // the zero address
		"01" + // Withdrawal
		"e803000000000000" + // 1000 sats
		"fa00000000000000" + // 250 sats
		"19000000" + "76a91477bff20c60e522dfaa3350c39b030a5d004e839a88ac"

	if hex.EncodeToString(got) != want {
		t.Errorf("encoding =\n  %s\nwant\n  %s", hex.EncodeToString(got), want)
	}
}

// The script comes from the address itself, so one program reads the same on
// every mainchain.
func TestWithdrawalAddressReadsOnEveryNetwork(t *testing.T) {
	const program = "0014751e76e8199196d454941c45d1b3a323f1433bd6"
	for _, address := range []string{
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
		"tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx",
		"bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080",
	} {
		script, err := mainScriptPubKey(address)
		if err != nil {
			t.Errorf("%s: %v", address, err)
			continue
		}
		if hex.EncodeToString(script) != program {
			t.Errorf("%s pays %s, want %s", address, hex.EncodeToString(script), program)
		}
	}
}

func TestWithdrawalRejectsAnUnknownAddress(t *testing.T) {
	tx := chain.Transaction{Outputs: []chain.Output{{
		Content: json.RawMessage(`{"Withdrawal":{"value":1,"main_fee":1,"main_address":"nope"}}`),
	}}}
	if _, err := encodeTx(tx); err == nil {
		t.Fatal("want an error for an address no network reads, got none")
	}
}

// An empty transaction still writes both lengths.
func TestEncodeEmptyTransaction(t *testing.T) {
	got, err := encodeTx(chain.Transaction{})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if hex.EncodeToString(got) != "0000000000000000" {
		t.Errorf("encoding = %s, want two zero lengths", hex.EncodeToString(got))
	}
}

func TestEncodeRejectsAShortLeafHash(t *testing.T) {
	tx := chain.Transaction{Inputs: []chain.Input{{LeafHash: chain.Bytes{1, 2, 3}}}}
	if _, err := encodeTx(tx); err == nil {
		t.Fatal("want an error for a short leaf hash, got none")
	}
}

func TestEncodeRejectsEmptyContent(t *testing.T) {
	tx := chain.Transaction{Outputs: []chain.Output{{Content: json.RawMessage(`{}`)}}}
	if _, err := encodeTx(tx); err == nil {
		t.Fatal("want an error for an output with no content, got none")
	}
}

// A live node made this transfer and named it. The index computes the same
// txid from the transaction alone, because a block template carries no txid.
func TestIdentifyMatchesTheNode(t *testing.T) {
	const (
		txid = "fb456612c98a2925af2548c6677684fd2a6fb238caf963f3508efb03a9d0baad"
		// The node divides the encoding by eight and calls that the size.
		encoded = 204
		size    = encoded / 8
	)

	raw, err := os.ReadFile("testdata/transfer.json")
	if err != nil {
		t.Fatalf("read the transaction: %v", err)
	}
	var tx chain.Transaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		t.Fatalf("decode the transaction: %v", err)
	}

	info, err := Decoder{}.IdentifyTx(tx)
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if info.Txid.String() != txid {
		t.Errorf("txid = %s, want %s", info.Txid, txid)
	}
	if info.Size != size {
		t.Errorf("size = %d, want %d", info.Size, size)
	}
	if len(info.Raw) != encoded {
		t.Errorf("encoding is %d bytes, want %d", len(info.Raw), encoded)
	}
}
