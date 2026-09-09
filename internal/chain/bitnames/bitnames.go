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

// A block body carries OutputContent; a block index deposit carries
// FilledOutputContent. The two spell the same coin differently: a filled name
// holds its id, and a filled withdrawal is called BitcoinWithdrawal. One
// decoder reads both, because the index feeds both to it.
type withdrawal struct {
	Value       uint64 `json:"value"`
	MainFee     uint64 `json:"main_fee"`
	ValueSats   uint64 `json:"value_sats"`
	MainFeeSats uint64 `json:"main_fee_sats"`
	MainAddress string `json:"main_address"`
}

// DecodeContent reads one output payload. A withdrawal removes both its payout
// and its mainchain fee from the sidechain, because the enforcer pays both out
// of the treasury. A name holds a name, not a coin.
func (Decoder) DecodeContent(raw json.RawMessage) (chain.Content, error) {
	tag, payload, err := chain.Variant(raw)
	if err != nil {
		return chain.Content{}, fmt.Errorf("decode bitnames output content: %w", err)
	}
	switch tag {
	case "BitcoinSats":
		return value(payload, "value")
	case "Withdrawal", "BitcoinWithdrawal":
		return withdrawalValue(payload)
	case "BitName":
		return chain.Content{Type: "bitname"}, nil
	case "BitNameReservation":
		return chain.Content{Type: "bitname_reservation"}, nil
	default:
		return chain.Content{}, fmt.Errorf("bitnames output content %q names no known variant", tag)
	}
}

func value(payload json.RawMessage, kind string) (chain.Content, error) {
	var sats uint64
	if err := json.Unmarshal(payload, &sats); err != nil {
		return chain.Content{}, fmt.Errorf("decode bitnames %s: %w", kind, err)
	}
	out, err := toSats(sats)
	if err != nil {
		return chain.Content{}, err
	}
	return chain.Content{ValueSats: out, Type: kind}, nil
}

func withdrawalValue(payload json.RawMessage) (chain.Content, error) {
	var w withdrawal
	if err := json.Unmarshal(payload, &w); err != nil {
		return chain.Content{}, fmt.Errorf("decode bitnames withdrawal: %w", err)
	}
	amount := max(w.Value, w.ValueSats)
	fee := max(w.MainFee, w.MainFeeSats)
	total := amount + fee
	if total < amount {
		return chain.Content{}, fmt.Errorf(
			"withdrawal of %d sats plus fee %d sats overflows", amount, fee)
	}
	sats, err := toSats(total)
	if err != nil {
		return chain.Content{}, err
	}
	return chain.Content{ValueSats: sats, Type: "withdrawal"}, nil
}

func toSats(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("value %d sats does not fit an int64", v)
	}
	return int64(v), nil
}

var _ chain.Decoder = Decoder{}
