package bitassets

import "testing"

// A block body carries OutputContent; a block index deposit carries
// FilledOutputContent. One decoder reads both, so both spellings appear here.
// A body writes BitAsset as an amount; a filled output writes an id and an
// amount. An earlier decoder typed it as a number alone, so a deposit that
// held an asset failed the whole block.
func TestDecodeContent(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantSats int64
		wantType string
	}{
		{"body bitcoin", `{"BitcoinSats":21000}`, 21000, "value"},
		{"body zero bitcoin", `{"BitcoinSats":0}`, 0, "value"},
		{"body bitasset is an amount", `{"BitAsset":500}`, 0, "bitasset"},
		{"filled bitasset is an id and an amount", `{"BitAsset":["4f2a",500]}`, 0, "bitasset"},
		{"body control", `"BitAssetControl"`, 0, "bitasset_control"},
		{"filled control holds its id", `{"BitAssetControl":"4f2a"}`, 0, "bitasset_control"},
		{"body reservation", `"BitAssetReservation"`, 0, "bitasset_reservation"},
		{"filled reservation holds its hash", `{"BitAssetReservation":"9c1d"}`, 0, "bitasset_reservation"},
		{"body auction receipt", `"DutchAuctionReceipt"`, 0, "dutch_auction_receipt"},
		{"filled auction receipt holds its id", `{"DutchAuctionReceipt":"1b7e"}`, 0, "dutch_auction_receipt"},
		{
			"body withdrawal adds the mainchain fee",
			`{"Withdrawal":{"value_sats":1000,"main_fee_sats":250,"main_address":"tb1q"}}`,
			1250, "withdrawal",
		},
		{
			"filled withdrawal carries its own name",
			`{"BitcoinWithdrawal":{"value_sats":1000,"main_fee_sats":250,"main_address":"tb1q"}}`,
			1250, "withdrawal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decoder{}.DecodeContent([]byte(tc.raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.ValueSats != tc.wantSats {
				t.Errorf("ValueSats = %d, want %d", got.ValueSats, tc.wantSats)
			}
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
		})
	}
}

func TestDecodeContentRejectsUnknown(t *testing.T) {
	for _, raw := range []string{`{"Sideways":1}`, `{}`, `"nonsense"`, `{"a":1,"b":2}`} {
		if _, err := (Decoder{}).DecodeContent([]byte(raw)); err == nil {
			t.Errorf("want an error for %s, got none", raw)
		}
	}
}

func TestDecodeContentRejectsOverflow(t *testing.T) {
	const raw = `{"Withdrawal":{"value_sats":18446744073709551615,"main_fee_sats":1,"main_address":"tb1q"}}`
	if _, err := (Decoder{}).DecodeContent([]byte(raw)); err == nil {
		t.Fatal("want an overflow error, got none")
	}
}

// The node writes a withdrawal as value and main_fee. One serializer renames
// them, so both spellings must read the same amount.
func TestDecodeWithdrawalTakesEitherSpelling(t *testing.T) {
	cases := map[string]string{
		"as the node writes it":   `{"Withdrawal":{"value":1000,"main_fee":250,"main_address":"tb1q"}}`,
		"with the renamed fields": `{"Withdrawal":{"value_sats":1000,"main_fee_sats":250,"main_address":"tb1q"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Decoder{}.DecodeContent([]byte(raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.ValueSats != 1250 {
				t.Errorf("ValueSats = %d, want 1250", got.ValueSats)
			}
		})
	}
}
