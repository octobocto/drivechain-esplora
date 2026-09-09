// Package bitassets reads the output payloads of the bitassets sidechain.
package bitassets

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// Decoder reads a bitassets OutputContent.
type Decoder struct{}

// Name identifies the chain in the registry.
func (Decoder) Name() string { return "bitassets" }

// A control output, a reservation and an auction receipt carry no field, so
// serde writes each one as a bare string.
var unitTypes = map[string]string{
	"BitAssetControl":     "bitasset_control",
	"BitAssetReservation": "bitasset_reservation",
	"DutchAuctionReceipt": "dutch_auction_receipt",
}

type content struct {
	BitcoinSats *uint64 `json:"BitcoinSats,omitempty"`
	// A bitasset amount counts units of that asset, not satoshis.
	BitAsset *uint64 `json:"BitAsset,omitempty"`
	// A withdrawal names its amounts as value and main_fee. One serializer
	// renames them to value_sats and main_fee_sats, so both spellings decode.
	Withdrawal *struct {
		Value       uint64 `json:"value"`
		MainFee     uint64 `json:"main_fee"`
		ValueSats   uint64 `json:"value_sats"`
		MainFeeSats uint64 `json:"main_fee_sats"`
		MainAddress string `json:"main_address"`
	} `json:"Withdrawal,omitempty"`
}

// DecodeContent reads one output payload. A withdrawal removes both its payout
// and its mainchain fee from the sidechain, because the enforcer pays both out
// of the treasury. Only a bitcoin output and a withdrawal hold satoshis.
func (Decoder) DecodeContent(raw json.RawMessage) (chain.Content, error) {
	if tag, ok := unitVariant(raw); ok {
		if name, ok := unitTypes[tag]; ok {
			return chain.Content{Type: name}, nil
		}
		return chain.Content{}, fmt.Errorf("bitassets output content %q names no known variant", tag)
	}

	var c content
	if err := json.Unmarshal(raw, &c); err != nil {
		return chain.Content{}, fmt.Errorf("decode bitassets output content: %w", err)
	}
	switch {
	case c.BitcoinSats != nil:
		sats, err := toSats(*c.BitcoinSats)
		if err != nil {
			return chain.Content{}, err
		}
		return chain.Content{ValueSats: sats, Type: "value"}, nil
	case c.BitAsset != nil:
		return chain.Content{Type: "bitasset"}, nil
	case c.Withdrawal != nil:
		value := max(c.Withdrawal.Value, c.Withdrawal.ValueSats)
		mainFee := max(c.Withdrawal.MainFee, c.Withdrawal.MainFeeSats)
		total := value + mainFee
		if total < value {
			return chain.Content{}, fmt.Errorf(
				"withdrawal of %d sats plus fee %d sats overflows",
				value, mainFee)
		}
		sats, err := toSats(total)
		if err != nil {
			return chain.Content{}, err
		}
		return chain.Content{ValueSats: sats, Type: "withdrawal"}, nil
	default:
		return chain.Content{}, fmt.Errorf("bitassets output content %s names no known variant", raw)
	}
}

// unitVariant reads the bare string serde writes for a variant with no fields.
func unitVariant(raw json.RawMessage) (string, bool) {
	var tag string
	if err := json.Unmarshal(raw, &tag); err != nil {
		return "", false
	}
	return tag, true
}

func toSats(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("value %d sats does not fit an int64", v)
	}
	return int64(v), nil
}

var _ chain.Decoder = Decoder{}
