// Package thunder reads the output payloads of the thunder sidechain.
package thunder

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// Decoder reads a thunder OutputContent.
type Decoder struct{}

// Name identifies the chain in the registry.
func (Decoder) Name() string { return "thunder" }

type content struct {
	Value *uint64 `json:"Value,omitempty"`
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
// of the treasury.
func (Decoder) DecodeContent(raw json.RawMessage) (chain.Content, error) {
	var c content
	if err := json.Unmarshal(raw, &c); err != nil {
		return chain.Content{}, fmt.Errorf("decode thunder output content: %w", err)
	}
	switch {
	case c.Value != nil:
		sats, err := toSats(*c.Value)
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
		return chain.Content{}, fmt.Errorf("thunder output content %s names no known variant", raw)
	}
}

func toSats(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("value %d sats does not fit an int64", v)
	}
	return int64(v), nil
}

var _ chain.Decoder = Decoder{}
