package bitassets

import "testing"

// Only a bitcoin output and a withdrawal hold satoshis. A bitasset amount
// counts units of that asset, and a control output, a reservation and an
// auction receipt hold no coin at all.
func TestDecodeContent(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantSats int64
		wantType string
	}{
		{"bitcoin", `{"BitcoinSats":21000}`, 21000, "value"},
		{"zero bitcoin", `{"BitcoinSats":0}`, 0, "value"},
		{"a bitasset amount is not satoshis", `{"BitAsset":500}`, 0, "bitasset"},
		{
			"withdrawal adds the mainchain fee",
			`{"Withdrawal":{"value_sats":1000,"main_fee_sats":250,"main_address":"tb1qexample"}}`,
			1250, "withdrawal",
		},
		{"control", `"BitAssetControl"`, 0, "bitasset_control"},
		{"reservation", `"BitAssetReservation"`, 0, "bitasset_reservation"},
		{"auction receipt", `"DutchAuctionReceipt"`, 0, "dutch_auction_receipt"},
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
	for _, raw := range []string{`{"Sideways":1}`, `{}`, `"nonsense"`} {
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
