// Package truthcoin reads the output payloads of the truthcoin sidechain.
package truthcoin

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// Decoder reads a truthcoin OutputContent.
type Decoder struct{}

// Name identifies the chain in the registry.
func (Decoder) Name() string { return "truthcoin" }

type withdrawal struct {
	Value       uint64 `json:"value"`
	MainFee     uint64 `json:"main_fee"`
	ValueSats   uint64 `json:"value_sats"`
	MainFeeSats uint64 `json:"main_fee_sats"`
	MainAddress string `json:"main_address"`
}

type marketFunds struct {
	Amount *uint64 `json:"amount"`
	IsFee  bool    `json:"is_fee"`
}

// DecodeContent reads one output payload. Each value is what the node's
// GetValue returns: a withdrawal holds its payout plus its mainchain fee, and
// market funds hold the sats in the market treasury or the author fee pot.
func (Decoder) DecodeContent(raw json.RawMessage) (chain.Content, error) {
	tag, payload, err := chain.Variant(raw)
	if err != nil {
		return chain.Content{}, fmt.Errorf("decode truthcoin output content: %w", err)
	}
	switch tag {
	case "Value":
		var sats uint64
		if err := json.Unmarshal(payload, &sats); err != nil {
			return chain.Content{}, fmt.Errorf("decode truthcoin value: %w", err)
		}
		out, err := toSats(sats)
		if err != nil {
			return chain.Content{}, err
		}
		return chain.Content{ValueSats: out, Type: "value"}, nil
	case "Withdrawal":
		return withdrawalValue(payload)
	case "MarketFunds":
		return marketFundsValue(payload)
	default:
		return chain.Content{}, fmt.Errorf("truthcoin output content %q names no known variant", tag)
	}
}

func withdrawalValue(payload json.RawMessage) (chain.Content, error) {
	var w withdrawal
	if err := json.Unmarshal(payload, &w); err != nil {
		return chain.Content{}, fmt.Errorf("decode truthcoin withdrawal: %w", err)
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

func marketFundsValue(payload json.RawMessage) (chain.Content, error) {
	var m marketFunds
	if err := json.Unmarshal(payload, &m); err != nil {
		return chain.Content{}, fmt.Errorf("decode truthcoin market funds: %w", err)
	}
	if m.Amount == nil {
		return chain.Content{}, fmt.Errorf("truthcoin market funds %s name no amount", payload)
	}
	sats, err := toSats(*m.Amount)
	if err != nil {
		return chain.Content{}, err
	}
	if m.IsFee {
		return chain.Content{ValueSats: sats, Type: "market_author_fee"}, nil
	}
	return chain.Content{ValueSats: sats, Type: "market_treasury"}, nil
}

func toSats(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("value %d sats does not fit an int64", v)
	}
	return int64(v), nil
}

var _ chain.Decoder = Decoder{}
