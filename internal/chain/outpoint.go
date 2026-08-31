package chain

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// OutPointKind tags which of the three ways an output came into being. The
// values are the borsh enum discriminants, so they also index the key bytes.
type OutPointKind uint8

const (
	// KindRegular is an output of a sidechain transaction.
	KindRegular OutPointKind = 0
	// KindCoinbase is an output of a block body, keyed by the merkle root.
	KindCoinbase OutPointKind = 1
	// KindDeposit is an output created by a mainchain deposit.
	KindDeposit OutPointKind = 2
)

func (k OutPointKind) String() string {
	switch k {
	case KindRegular:
		return "regular"
	case KindCoinbase:
		return "coinbase"
	case KindDeposit:
		return "deposit"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// OutPointKeySize is the width of an OutPointKey: one discriminant byte, a
// 32-byte hash, and a little-endian u32.
const OutPointKeySize = 37

// OutPointKey is the canonical borsh encoding of an OutPoint. The node uses it
// as its own database key, and so does this index.
type OutPointKey [OutPointKeySize]byte

// OutPoint names one output. Source is a sidechain txid for a regular output, a
// block merkle root for a coinbase output, and a mainchain txid for a deposit.
type OutPoint struct {
	Kind   OutPointKind
	Source Hash
	Vout   uint32
}

// Key returns the 37-byte form the node keys its own tables on.
func (o OutPoint) Key() OutPointKey {
	var key OutPointKey
	key[0] = byte(o.Kind)
	copy(key[1:33], o.Source[:])
	binary.LittleEndian.PutUint32(key[33:], o.Vout)
	return key
}

// OutPointFromKey reverses Key.
func OutPointFromKey(key OutPointKey) (OutPoint, error) {
	kind := OutPointKind(key[0])
	if kind > KindDeposit {
		return OutPoint{}, fmt.Errorf("outpoint key has unknown kind %d", key[0])
	}
	out := OutPoint{Kind: kind, Vout: binary.LittleEndian.Uint32(key[33:])}
	copy(out.Source[:], key[1:33])
	return out, nil
}

func (o OutPoint) String() string {
	return fmt.Sprintf("%s %s %d", o.Kind, o.sourceString(), o.Vout)
}

// sourceString renders a deposit source in mainchain byte order, and every
// other source in sidechain byte order.
func (o OutPoint) sourceString() string {
	if o.Kind == KindDeposit {
		return BitcoinHash(o.Source).String()
	}
	return o.Source.String()
}

type outPointWire struct {
	Regular *struct {
		Txid Hash   `json:"txid"`
		Vout uint32 `json:"vout"`
	} `json:"Regular,omitempty"`
	Coinbase *struct {
		MerkleRoot Hash   `json:"merkle_root"`
		Vout       uint32 `json:"vout"`
	} `json:"Coinbase,omitempty"`
	Deposit *struct {
		Txid BitcoinHash `json:"txid"`
		Vout uint32      `json:"vout"`
	} `json:"Deposit,omitempty"`
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
		*o = OutPoint{Kind: KindCoinbase, Source: wire.Coinbase.MerkleRoot, Vout: wire.Coinbase.Vout}
	case wire.Deposit != nil:
		*o = OutPoint{Kind: KindDeposit, Source: Hash(wire.Deposit.Txid), Vout: wire.Deposit.Vout}
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
		wire.Coinbase = &struct {
			MerkleRoot Hash   `json:"merkle_root"`
			Vout       uint32 `json:"vout"`
		}{MerkleRoot: o.Source, Vout: o.Vout}
	case KindDeposit:
		wire.Deposit = &struct {
			Txid BitcoinHash `json:"txid"`
			Vout uint32      `json:"vout"`
		}{Txid: BitcoinHash(o.Source), Vout: o.Vout}
	default:
		return nil, fmt.Errorf("cannot encode outpoint of kind %d", uint8(o.Kind))
	}
	return json.Marshal(wire)
}

// InPointKind tags how an output left the UTXO set.
type InPointKind uint8

const (
	// SpendRegular is a spend by a sidechain transaction input.
	SpendRegular InPointKind = 0
	// SpendWithdrawal is a spend by a withdrawal bundle, which has no
	// sidechain transaction at all.
	SpendWithdrawal InPointKind = 1
)

func (k InPointKind) String() string {
	switch k {
	case SpendRegular:
		return "regular"
	case SpendWithdrawal:
		return "withdrawal"
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
