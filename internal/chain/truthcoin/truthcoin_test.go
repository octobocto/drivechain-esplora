package truthcoin

import (
	"encoding/json"
	"os"
	"testing"
)

// Each value mirrors the node's GetValue. A withdrawal holds its payout plus
// its mainchain fee, and market funds hold the sats the market locks.
func TestDecodeContent(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantSats int64
		wantType string
	}{
		{"value", `{"Value":21000}`, 21000, "value"},
		{"zero value", `{"Value":0}`, 0, "value"},
		{
			"withdrawal adds the mainchain fee",
			`{"Withdrawal":{"value_sats":1000,"main_fee_sats":250,"main_address":"tb1qexample"}}`,
			1250, "withdrawal",
		},
		{
			"market treasury",
			`{"MarketFunds":{"market_id":[97,94,213,19,245,102],"amount":1000000,"is_fee":false}}`,
			1000000, "market_treasury",
		},
		{
			"market author fee",
			`{"MarketFunds":{"market_id":[97,94,213,19,245,102],"amount":4200,"is_fee":true}}`,
			4200, "market_author_fee",
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
	for _, raw := range []string{
		`{"Sideways":1}`,
		`{}`,
		`"nonsense"`,
		`{"Votecoin":5}`,
		`{"MarketFunds":{"market_id":[1,2,3,4,5,6],"is_fee":false}}`,
	} {
		if _, err := (Decoder{}).DecodeContent([]byte(raw)); err == nil {
			t.Errorf("want an error for %s, got none", raw)
		}
	}
}

func TestDecodeContentRejectsOverflow(t *testing.T) {
	for _, raw := range []string{
		`{"Withdrawal":{"value_sats":18446744073709551615,"main_fee_sats":1,"main_address":"tb1q"}}`,
		`{"MarketFunds":{"market_id":[1,2,3,4,5,6],"amount":9223372036854775808,"is_fee":false}}`,
	} {
		if _, err := (Decoder{}).DecodeContent([]byte(raw)); err == nil {
			t.Errorf("want an overflow error for %s, got none", raw)
		}
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

// Block 2 of the betanet chain creates four markets. Each market creation
// locks 1000000 sats in a treasury output beside a change output.
func TestDecodeEveryOutputOfALiveBlock(t *testing.T) {
	raw, err := os.ReadFile("../testdata/truthcoin_betanet_block.json")
	if err != nil {
		t.Fatalf("read block: %v", err)
	}
	var answer struct {
		Result struct {
			Body struct {
				Transactions []struct {
					Outputs []struct {
						Content json.RawMessage `json:"content"`
					} `json:"outputs"`
				} `json:"transactions"`
			} `json:"body"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode block: %v", err)
	}

	txs := answer.Result.Body.Transactions
	if len(txs) != 4 {
		t.Fatalf("block carries %d transactions, want 4", len(txs))
	}
	for i, tx := range txs {
		if len(tx.Outputs) != 2 {
			t.Fatalf("transaction %d has %d outputs, want 2", i, len(tx.Outputs))
		}
		treasury, err := Decoder{}.DecodeContent(tx.Outputs[0].Content)
		if err != nil {
			t.Fatalf("transaction %d treasury: %v", i, err)
		}
		if treasury.Type != "market_treasury" || treasury.ValueSats != 1000000 {
			t.Errorf("transaction %d treasury = %+v, want 1000000 sats of market_treasury", i, treasury)
		}
		change, err := Decoder{}.DecodeContent(tx.Outputs[1].Content)
		if err != nil {
			t.Fatalf("transaction %d change: %v", i, err)
		}
		if change.Type != "value" || change.ValueSats <= 0 {
			t.Errorf("transaction %d change = %+v, want a positive value", i, change)
		}
	}
}
