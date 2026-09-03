package chain

import (
	"encoding/json"
	"fmt"
)

// Header is a sidechain block header. It carries no height and no timestamp.
// A nil PrevSideHash marks genesis.
type Header struct {
	MerkleRoot   Hash        `json:"merkle_root"`
	PrevSideHash *Hash       `json:"prev_side_hash"`
	PrevMainHash BitcoinHash `json:"prev_main_hash"`
}

// Output is one coin. Content is the chain-specific payload, which a per-chain
// Decoder reads and this package stores verbatim.
type Output struct {
	Address Address         `json:"address"`
	Content json.RawMessage `json:"content"`
}

// Input names the output a transaction spends, with its utreexo leaf hash.
type Input struct {
	OutPoint OutPoint
	LeafHash Bytes
}

func (i *Input) UnmarshalJSON(data []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(data, &pair); err != nil {
		return fmt.Errorf("decode input pair: %w", err)
	}
	if len(pair) != 2 {
		return fmt.Errorf("input pair has %d elements, want 2", len(pair))
	}
	if err := json.Unmarshal(pair[0], &i.OutPoint); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &i.LeafHash)
}

func (i Input) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{i.OutPoint, i.LeafHash})
}

// Authorization is the ed25519 signature over one input.
type Authorization struct {
	VerifyingKey Bytes `json:"verifying_key"`
	Signature    Bytes `json:"signature"`
}

// Transaction is a sidechain transaction. It has no version, no locktime, and
// no per-input sequence. Signatures live in the body, not here.
type Transaction struct {
	Inputs  []Input  `json:"inputs"`
	Outputs []Output `json:"outputs"`
}

// Body holds a block's coins. Coinbase outputs belong to the body itself, not
// to any transaction, and key on the header merkle root.
type Body struct {
	Coinbase       []Output        `json:"coinbase"`
	Transactions   []Transaction   `json:"transactions"`
	Authorizations []Authorization `json:"authorizations"`
}

// Block is what get_block returns.
type Block struct {
	Header Header `json:"header"`
	Body   Body   `json:"body"`
}

// BlockTemplate is what get_block_template returns: the block the node would
// mine next. Its body carries every transaction the node accepted and no block
// holds yet, so that body is the mempool.
type BlockTemplate struct {
	CriticalHash Hash  `json:"critical_hash"`
	Block        Block `json:"block"`
	FeesSats     int64 `json:"fees_sats"`
}

// AuthorizationsFor returns the signatures that cover one transaction. The body
// holds a flat list, one signature per input, in transaction order.
func (b *Body) AuthorizationsFor(txIndex int) ([]Authorization, error) {
	if txIndex < 0 || txIndex >= len(b.Transactions) {
		return nil, fmt.Errorf("transaction index %d is outside the body", txIndex)
	}
	start := 0
	for _, tx := range b.Transactions[:txIndex] {
		start += len(tx.Inputs)
	}
	end := start + len(b.Transactions[txIndex].Inputs)
	if end > len(b.Authorizations) {
		return nil, fmt.Errorf(
			"body has %d authorizations, but transaction %d ends at %d",
			len(b.Authorizations), txIndex, end)
	}
	return b.Authorizations[start:end], nil
}

// Content is what a per-chain Decoder makes of one output payload.
type Content struct {
	// ValueSats is what the output removes from the sidechain when spent. A
	// withdrawal removes both its payout and its mainchain fee.
	ValueSats int64
	// Type names the payload for the API, such as "value" or "withdrawal".
	Type string
}

// Decoder reads the chain-specific part of a transaction. Everything else in a
// rust sidechain block is identical across chains.
type Decoder interface {
	// Name is the chain name, as it appears in the chain registry.
	Name() string
	// DecodeContent reads one output payload.
	DecodeContent(raw json.RawMessage) (Content, error)
	// IdentifyTx names one transaction. A block index carries the txid, the
	// size and the encoding; a block template carries none of the three.
	IdentifyTx(tx Transaction) (TxInfo, error)
}

// TxInfo names one transaction. A body carries neither field: a txid is a
// blake3 digest over the borsh encoding, and a size is that encoding's length.
type TxInfo struct {
	Txid Hash   `json:"txid"`
	Size uint64 `json:"size"`
	// Raw is the borsh encoding. It is what /tx/{txid}/hex serves.
	Raw Bytes `json:"raw"`
}

// BlockIndex carries everything a block body does not. A transaction has no
// txid and no size, a deposit never appears in the body at all, and a
// withdrawal bundle spends outputs with no transaction. One call returns all
// three.
type BlockIndex struct {
	// Txs names each transaction in the body, in body order.
	Txs []TxInfo `json:"txs"`
	// Deposits are the outputs mainchain deposits created in this block.
	Deposits []Deposit `json:"deposits"`
	// BundleSpends are the outputs a withdrawal bundle removed in this block.
	BundleSpends []BundleSpend `json:"bundle_spends"`
}

// Deposit is one output a mainchain deposit created. The node sends a pair,
// not an object, because it serializes a Rust tuple.
type Deposit struct {
	OutPoint OutPoint
	Output   Output
}

func (d *Deposit) UnmarshalJSON(data []byte) error {
	return decodePair(data, "deposit", &d.OutPoint, &d.Output)
}

func (d Deposit) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{d.OutPoint, d.Output})
}

// BundleSpend is one output a withdrawal bundle removed. It is a pair for the
// same reason.
type BundleSpend struct {
	OutPoint OutPoint
	M6id     BitcoinHash
}

func (b *BundleSpend) UnmarshalJSON(data []byte) error {
	return decodePair(data, "bundle spend", &b.OutPoint, &b.M6id)
}

func (b BundleSpend) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{b.OutPoint, b.M6id})
}

// decodePair reads a two element JSON array into two values.
func decodePair(data []byte, what string, first, second any) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(data, &pair); err != nil {
		return fmt.Errorf("decode %s pair: %w", what, err)
	}
	if len(pair) != 2 {
		return fmt.Errorf("%s pair has %d elements, want 2", what, len(pair))
	}
	if err := json.Unmarshal(pair[0], first); err != nil {
		return fmt.Errorf("decode %s outpoint: %w", what, err)
	}
	if err := json.Unmarshal(pair[1], second); err != nil {
		return fmt.Errorf("decode %s value: %w", what, err)
	}
	return nil
}
