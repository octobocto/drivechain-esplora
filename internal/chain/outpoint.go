package chain

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// OutPointKind tags how an output came into being. Kinds 0 to 4 are the borsh
// enum discriminants of thunder 0.18 and of truthcoin. Thunder 0.19 gives
// discriminant 3 to its unsuccessful withdrawal outpoint, so this index gives
// that outpoint its own kind.
type OutPointKind uint8

const (
	// KindRegular is an output of a sidechain transaction.
	KindRegular OutPointKind = 0
	// KindCoinbase is an output of a block body.
	KindCoinbase OutPointKind = 1
	// KindDeposit is an output created by a mainchain deposit.
	KindDeposit OutPointKind = 2
	// KindMarketFunds is a truthcoin market treasury or author fee output.
	KindMarketFunds OutPointKind = 3
	// KindPayout is a truthcoin output that a market trade or settlement
	// created.
	KindPayout OutPointKind = 4
	// KindUnsuccessfulWithdrawal is a thunder output that a failed or dropped
	// withdrawal bundle created again.
	KindUnsuccessfulWithdrawal OutPointKind = 5
)

func (k OutPointKind) String() string {
	switch k {
	case KindRegular:
		return "regular"
	case KindCoinbase:
		return "coinbase"
	case KindDeposit:
		return "deposit"
	case KindMarketFunds:
		return "market_funds"
	case KindPayout:
		return "payout"
	case KindUnsuccessfulWithdrawal:
		return "unsuccessful_withdrawal"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// OutPointKeySize is the width of an OutPointKey: one kind byte, a 32-byte
// hash, and a little-endian u32.
const OutPointKeySize = 37

// UnsuccessfulWithdrawalKeySize is the width of the key of an unsuccessful
// withdrawal outpoint, which also holds a little-endian u32 height.
const UnsuccessfulWithdrawalKeySize = OutPointKeySize + 4

// OutPointKey is the key that this index stores an output on. For kinds 0 to 4
// it is the canonical borsh encoding of the outpoint.
type OutPointKey []byte

// OutPoint names one output. Source is a sidechain txid for a regular output, a
// coinbase txid or an older node's block merkle root for a coinbase output, a
// mainchain txid for a deposit, the payout hash for a payout, and the bundle
// m6id for an unsuccessful withdrawal. A market funds outpoint packs its fields
// into Source; see MarketFundsOutPoint.
type OutPoint struct {
	Kind   OutPointKind
	Source Hash
	Vout   uint32
	// Height is the sidechain height at which an unsuccessful withdrawal
	// output came into being. It is zero for every other kind.
	Height uint32
}

// Key returns the form this index stores the output on.
func (o OutPoint) Key() OutPointKey {
	size := OutPointKeySize
	if o.Kind == KindUnsuccessfulWithdrawal {
		size = UnsuccessfulWithdrawalKeySize
	}
	key := make(OutPointKey, size)
	key[0] = byte(o.Kind)
	copy(key[1:33], o.Source[:])
	if o.Kind == KindUnsuccessfulWithdrawal {
		binary.LittleEndian.PutUint32(key[33:37], o.Height)
		binary.LittleEndian.PutUint32(key[37:], o.Vout)
	} else {
		binary.LittleEndian.PutUint32(key[33:], o.Vout)
	}
	return key
}

// OutPointFromKey reverses Key.
func OutPointFromKey(key OutPointKey) (OutPoint, error) {
	if len(key) == 0 {
		return OutPoint{}, fmt.Errorf("outpoint key is empty")
	}
	kind := OutPointKind(key[0])
	want := OutPointKeySize
	switch {
	case kind == KindUnsuccessfulWithdrawal:
		want = UnsuccessfulWithdrawalKeySize
	case kind > KindPayout:
		return OutPoint{}, fmt.Errorf("outpoint key has unknown kind %d", key[0])
	}
	if len(key) != want {
		return OutPoint{}, fmt.Errorf("%s outpoint key is %d bytes, want %d", kind, len(key), want)
	}
	out := OutPoint{Kind: kind}
	copy(out.Source[:], key[1:33])
	if kind == KindUnsuccessfulWithdrawal {
		out.Height = binary.LittleEndian.Uint32(key[33:37])
		out.Vout = binary.LittleEndian.Uint32(key[37:])
	} else {
		out.Vout = binary.LittleEndian.Uint32(key[33:])
	}
	return out, nil
}

// MarketFundsOutPoint builds a truthcoin market funds outpoint. The node keys
// it on its borsh encoding padded with zeros, so Source holds the market id,
// the little-endian block height and the fee flag, and Vout stays zero.
func MarketFundsOutPoint(marketID [6]byte, blockHeight uint32, isFee bool) OutPoint {
	out := OutPoint{Kind: KindMarketFunds}
	copy(out.Source[:6], marketID[:])
	binary.LittleEndian.PutUint32(out.Source[6:10], blockHeight)
	if isFee {
		out.Source[10] = 1
	}
	return out
}

// MarketFunds reads the fields MarketFundsOutPoint packed.
func (o OutPoint) MarketFunds() (marketID [6]byte, blockHeight uint32, isFee bool) {
	copy(marketID[:], o.Source[:6])
	return marketID, binary.LittleEndian.Uint32(o.Source[6:10]), o.Source[10] == 1
}

func (o OutPoint) String() string {
	if o.Kind == KindMarketFunds {
		marketID, height, isFee := o.MarketFunds()
		return fmt.Sprintf("%s %x %d fee=%t", o.Kind, marketID, height, isFee)
	}
	if o.Kind == KindUnsuccessfulWithdrawal {
		return fmt.Sprintf("%s %s %d %d", o.Kind, o.sourceString(), o.Height, o.Vout)
	}
	return fmt.Sprintf("%s %s %d", o.Kind, o.sourceString(), o.Vout)
}

// sourceString renders a deposit source and an unsuccessful withdrawal m6id in
// mainchain byte order, and every other source in sidechain byte order.
func (o OutPoint) sourceString() string {
	if o.Kind == KindDeposit || o.Kind == KindUnsuccessfulWithdrawal {
		return BitcoinHash(o.Source).String()
	}
	return o.Source.String()
}

type outPointWire struct {
	Regular *struct {
		Txid Hash   `json:"txid"`
		Vout uint32 `json:"vout"`
	} `json:"Regular,omitempty"`
	Coinbase *coinbaseOutPointWire `json:"Coinbase,omitempty"`
	// A deposit names a mainchain outpoint, which rust-bitcoin renders as one
	// "txid:vout" string rather than as an object.
	Deposit                *string                             `json:"Deposit,omitempty"`
	MarketFunds            *marketFundsOutPointWire            `json:"MarketFunds,omitempty"`
	Payout                 *payoutOutPointWire                 `json:"Payout,omitempty"`
	UnsuccessfulWithdrawal *unsuccessfulWithdrawalOutPointWire `json:"UnsuccessfulWithdrawal,omitempty"`
}

type unsuccessfulWithdrawalOutPointWire struct {
	M6id   BitcoinHash `json:"m6id"`
	Height uint32      `json:"height"`
	Vout   uint32      `json:"vout"`
}

type payoutOutPointWire struct {
	Hash Hash   `json:"hash"`
	Vout uint32 `json:"vout"`
}

type marketFundsOutPointWire struct {
	MarketID    [6]byte `json:"market_id"`
	BlockHeight uint32  `json:"block_height"`
	IsFee       bool    `json:"is_fee"`
}

// coinbaseOutPointWire names a coinbase output by its txid. An older node
// names it by the block merkle root.
type coinbaseOutPointWire struct {
	Txid       *Hash  `json:"txid,omitempty"`
	MerkleRoot *Hash  `json:"merkle_root,omitempty"`
	Vout       uint32 `json:"vout"`
}

func (w *coinbaseOutPointWire) source() (Hash, error) {
	switch {
	case w.Txid != nil:
		return *w.Txid, nil
	case w.MerkleRoot != nil:
		return *w.MerkleRoot, nil
	default:
		return Hash{}, fmt.Errorf("coinbase outpoint names neither a txid nor a merkle root")
	}
}

func (o *OutPoint) UnmarshalJSON(data []byte) error {
	var wire outPointWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode outpoint: %w", err)
	}
	switch {
	case wire.Regular != nil:
		*o = OutPoint{Kind: KindRegular, Source: wire.Regular.Txid, Vout: wire.Regular.Vout}
	case wire.Coinbase != nil:
		source, err := wire.Coinbase.source()
		if err != nil {
			return err
		}
		*o = OutPoint{Kind: KindCoinbase, Source: source, Vout: wire.Coinbase.Vout}
	case wire.Deposit != nil:
		parsed, err := parseBitcoinOutPoint(*wire.Deposit)
		if err != nil {
			return err
		}
		*o = parsed
	case wire.MarketFunds != nil:
		*o = MarketFundsOutPoint(
			wire.MarketFunds.MarketID, wire.MarketFunds.BlockHeight, wire.MarketFunds.IsFee)
	case wire.Payout != nil:
		*o = OutPoint{Kind: KindPayout, Source: wire.Payout.Hash, Vout: wire.Payout.Vout}
	case wire.UnsuccessfulWithdrawal != nil:
		*o = OutPoint{
			Kind:   KindUnsuccessfulWithdrawal,
			Source: Hash(wire.UnsuccessfulWithdrawal.M6id),
			Vout:   wire.UnsuccessfulWithdrawal.Vout,
			Height: wire.UnsuccessfulWithdrawal.Height,
		}
	default:
		return fmt.Errorf("outpoint %s names no known variant", data)
	}
	return nil
}

func (o OutPoint) MarshalJSON() ([]byte, error) {
	var wire outPointWire
	switch o.Kind {
	case KindRegular:
		wire.Regular = &struct {
			Txid Hash   `json:"txid"`
			Vout uint32 `json:"vout"`
		}{Txid: o.Source, Vout: o.Vout}
	case KindCoinbase:
		wire.Coinbase = &coinbaseOutPointWire{Txid: &o.Source, Vout: o.Vout}
	case KindDeposit:
		text := fmt.Sprintf("%s:%d", BitcoinHash(o.Source), o.Vout)
		wire.Deposit = &text
	case KindMarketFunds:
		marketID, height, isFee := o.MarketFunds()
		wire.MarketFunds = &marketFundsOutPointWire{
			MarketID: marketID, BlockHeight: height, IsFee: isFee,
		}
	case KindPayout:
		wire.Payout = &payoutOutPointWire{Hash: o.Source, Vout: o.Vout}
	case KindUnsuccessfulWithdrawal:
		wire.UnsuccessfulWithdrawal = &unsuccessfulWithdrawalOutPointWire{
			M6id: BitcoinHash(o.Source), Height: o.Height, Vout: o.Vout,
		}
	default:
		return nil, fmt.Errorf("cannot encode outpoint of kind %d", uint8(o.Kind))
	}
	return json.Marshal(wire)
}

// parseBitcoinOutPoint reads the "txid:vout" form of a mainchain outpoint.
func parseBitcoinOutPoint(text string) (OutPoint, error) {
	txidHex, voutText, found := strings.Cut(text, ":")
	if !found {
		return OutPoint{}, fmt.Errorf("mainchain outpoint %q has no vout", text)
	}
	vout, err := strconv.ParseUint(voutText, 10, 32)
	if err != nil {
		return OutPoint{}, fmt.Errorf("mainchain outpoint %q has a bad vout: %w", text, err)
	}
	var txid BitcoinHash
	if err := json.Unmarshal([]byte(`"`+txidHex+`"`), &txid); err != nil {
		return OutPoint{}, fmt.Errorf("mainchain outpoint %q: %w", text, err)
	}
	return OutPoint{Kind: KindDeposit, Source: Hash(txid), Vout: uint32(vout)}, nil
}

// InPointKind tags how an output left the UTXO set.
type InPointKind uint8

const (
	// SpendRegular is a spend by a sidechain transaction input.
	SpendRegular InPointKind = 0
	// SpendWithdrawal is a spend by a withdrawal bundle, which has no
	// sidechain transaction at all.
	SpendWithdrawal InPointKind = 1
	// SpendMarket is a removal by truthcoin market code, which has no
	// transaction. The node has no inpoint for it, so this value is the
	// index's own.
	SpendMarket InPointKind = 2
)

func (k InPointKind) String() string {
	switch k {
	case SpendRegular:
		return "regular"
	case SpendWithdrawal:
		return "withdrawal"
	case SpendMarket:
		return "market"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// InPoint names what spent an output. Source is the spending txid for a regular
// spend, and the bundle m6id for a withdrawal spend.
type InPoint struct {
	Kind   InPointKind
	Source Hash
	Vin    uint32
}

type inPointWire struct {
	Regular *struct {
		Txid Hash   `json:"txid"`
		Vin  uint32 `json:"vin"`
	} `json:"Regular,omitempty"`
	Withdrawal *struct {
		M6id BitcoinHash `json:"m6id"`
	} `json:"Withdrawal,omitempty"`
}

func (i *InPoint) UnmarshalJSON(data []byte) error {
	var wire inPointWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode inpoint: %w", err)
	}
	switch {
	case wire.Regular != nil:
		*i = InPoint{Kind: SpendRegular, Source: wire.Regular.Txid, Vin: wire.Regular.Vin}
	case wire.Withdrawal != nil:
		*i = InPoint{Kind: SpendWithdrawal, Source: Hash(wire.Withdrawal.M6id)}
	default:
		return fmt.Errorf("inpoint %s names no known variant", data)
	}
	return nil
}
