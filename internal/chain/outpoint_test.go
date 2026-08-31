package chain

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

// The node keys its own tables on this 37-byte form, and so does the index. A
// change here silently corrupts every row, so the layout is pinned by hand.
func TestOutPointKeyLayout(t *testing.T) {
	source, err := ParseHash("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	if err != nil {
		t.Fatalf("parse source hash: %v", err)
	}

	cases := []struct {
		name string
		out  OutPoint
		want string
	}{
		{
			name: "regular",
			out:  OutPoint{Kind: KindRegular, Source: source, Vout: 1},
			want: "00" + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" + "01000000",
		},
		{
			name: "coinbase",
			out:  OutPoint{Kind: KindCoinbase, Source: source, Vout: 0},
			want: "01" + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" + "00000000",
		},
		{
			name: "deposit",
			out:  OutPoint{Kind: KindDeposit, Source: source, Vout: 258},
			want: "02" + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" + "02010000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.out.Key()
			if len(key) != OutPointKeySize {
				t.Fatalf("key is %d bytes, want %d", len(key), OutPointKeySize)
			}
			if got := hex.EncodeToString(key[:]); got != tc.want {
				t.Errorf("key = %s, want %s", got, tc.want)
			}
			back, err := OutPointFromKey(key)
			if err != nil {
				t.Fatalf("read key back: %v", err)
			}
			if back != tc.out {
				t.Errorf("round trip = %+v, want %+v", back, tc.out)
			}
		})
	}
}

func TestOutPointFromKeyRejectsUnknownKind(t *testing.T) {
	var key OutPointKey
	key[0] = 7
	if _, err := OutPointFromKey(key); err == nil {
		t.Fatal("want an error for kind 7, got none")
	}
}

// A sidechain hash renders as plain hex. A mainchain hash renders reversed,
// because rust-bitcoin does. Mixing the two shows a user the wrong txid.
func TestOutPointJSON(t *testing.T) {
	const sideHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	const mainHex = "201f1e1d1c1b1a191817161514131211100f0e0d0c0b0a090807060504030201"

	cases := []struct {
		name string
		wire string
		want OutPoint
	}{
		{
			name: "regular",
			wire: `{"Regular":{"txid":"` + sideHex + `","vout":3}}`,
			want: OutPoint{Kind: KindRegular, Vout: 3},
		},
		{
			name: "coinbase",
			wire: `{"Coinbase":{"merkle_root":"` + sideHex + `","vout":0}}`,
			want: OutPoint{Kind: KindCoinbase, Vout: 0},
		},
		{
			name: "deposit reverses the mainchain txid",
			wire: `{"Deposit":{"txid":"` + mainHex + `","vout":1}}`,
			want: OutPoint{Kind: KindDeposit, Vout: 1},
		},
	}

	source, err := ParseHash(sideHex)
	if err != nil {
		t.Fatalf("parse source hash: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			want.Source = source

			var got OutPoint
			if err := json.Unmarshal([]byte(tc.wire), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != want {
				t.Fatalf("decode = %+v, want %+v", got, want)
			}

			out, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if string(out) != tc.wire {
				t.Errorf("encode = %s, want %s", out, tc.wire)
			}
		})
	}
}

func TestOutPointRejectsUnknownVariant(t *testing.T) {
	var out OutPoint
	if err := json.Unmarshal([]byte(`{"Sideways":{"vout":0}}`), &out); err == nil {
		t.Fatal("want an error for an unknown variant, got none")
	}
}

func TestInPointJSON(t *testing.T) {
	const sideHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	const mainHex = "201f1e1d1c1b1a191817161514131211100f0e0d0c0b0a090807060504030201"

	source, err := ParseHash(sideHex)
	if err != nil {
		t.Fatalf("parse source hash: %v", err)
	}

	var regular InPoint
	if err := json.Unmarshal([]byte(`{"Regular":{"txid":"`+sideHex+`","vin":2}}`), &regular); err != nil {
		t.Fatalf("decode regular: %v", err)
	}
	if want := (InPoint{Kind: SpendRegular, Source: source, Vin: 2}); regular != want {
		t.Errorf("regular = %+v, want %+v", regular, want)
	}

	// A bundle spend has no sidechain transaction. Only the m6id names it.
	var bundle InPoint
	if err := json.Unmarshal([]byte(`{"Withdrawal":{"m6id":"`+mainHex+`"}}`), &bundle); err != nil {
		t.Fatalf("decode withdrawal: %v", err)
	}
	if want := (InPoint{Kind: SpendWithdrawal, Source: source}); bundle != want {
		t.Errorf("withdrawal = %+v, want %+v", bundle, want)
	}
}
