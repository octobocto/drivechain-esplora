// Package bitnames reads the output payloads of the bitnames sidechain.
package bitnames

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// Decoder reads a bitnames OutputContent.
type Decoder struct{}

// Name identifies the chain in the registry.
func (Decoder) Name() string { return "bitnames" }

// A bitname and a reservation carry no bitcoin, so serde writes each one as a
// bare string rather than an object.
const (
	tagBitName     = "BitName"
	tagReservation = "BitNameReservation"
)

type content struct {
	BitcoinSats *uint64 `json:"BitcoinSats,omitempty"`
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
// of the treasury. A bitname and a reservation hold a name, not a coin, so
// each one carries no value.
func (Decoder) DecodeContent(raw json.RawMessage) (chain.Content, error) {
	if tag, ok := unitVariant(raw); ok {
		switch tag {
		case tagBitName:
			return chain.Content{Type: "bitname"}, nil
		case tagReservation:
			return chain.Content{Type: "bitname_reservation"}, nil
		default:
			return chain.Content{}, fmt.Errorf("bitnames output content %q names no known variant", tag)
		}
	}

	var c content
	if err := json.Unmarshal(raw, &c); err != nil {
		return chain.Content{}, fmt.Errorf("decode bitnames output content: %w", err)
	}
	switch {
	case c.BitcoinSats != nil:
		sats, err := toSats(*c.BitcoinSats)
		if err != nil {
			return chain.Content{}, err
		}
		return chain.Content{ValueSats: sats, Type: "value"}, nil
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
		return chain.Content{}, fmt.Errorf("bitnames output content %s names no known variant", raw)
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
