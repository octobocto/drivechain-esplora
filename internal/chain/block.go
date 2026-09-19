package chain

import (
	"encoding/json"
	"fmt"

	"lukechampine.com/blake3"
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

// Input names the output a transaction spends, with its utreexo leaf hash. A
// chain with no utreexo sends the outpoint alone, and leaves LeafHash empty.
type Input struct {
	OutPoint OutPoint
	LeafHash Bytes
}

func (i *Input) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '[' {
		return decodePair(data, "input", &i.OutPoint, &i.LeafHash)
	}
	i.LeafHash = nil
	if err := json.Unmarshal(data, &i.OutPoint); err != nil {
		return fmt.Errorf("decode input outpoint: %w", err)
	}
	return nil
}

func (i Input) MarshalJSON() ([]byte, error) {
	if len(i.LeafHash) == 0 {
		return json.Marshal(i.OutPoint)
	}
	return json.Marshal([]any{i.OutPoint, i.LeafHash})
}

// Authorization is the ed25519 signature over one input.
type Authorization struct {
	VerifyingKey ByteString `json:"verifying_key"`
	Signature    ByteString `json:"signature"`
}

// Transaction is a sidechain transaction. It has no version, no locktime, and
// no per-input sequence. Signatures live in the body, not here.
type Transaction struct {
	Inputs  []Input  `json:"inputs"`
	Outputs []Output `json:"outputs"`
}

// Coinbase holds the outputs a block body creates. A node that sends a memo
// sends an object and keys each output on the coinbase txid. An older node
// sends a bare list and keys each output on the header merkle root.
type Coinbase struct {
	Memo    Bytes
	Outputs []Output
	// KeyedByTxid is true when the node sent the object form.
	KeyedByTxid bool
}

type coinbaseObject struct {
	Memo    Bytes    `json:"memo"`
	Outputs []Output `json:"outputs"`
}

func (c *Coinbase) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '[' {
		*c = Coinbase{}
		if err := json.Unmarshal(data, &c.Outputs); err != nil {
			return fmt.Errorf("decode coinbase outputs: %w", err)
		}
		return nil
	}
	var object coinbaseObject
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("decode coinbase: %w", err)
	}
	*c = Coinbase{Memo: object.Memo, Outputs: object.Outputs, KeyedByTxid: true}
	return nil
}

func (c Coinbase) MarshalJSON() ([]byte, error) {
	outputs := c.Outputs
	if outputs == nil {
		outputs = []Output{}
	}
	if !c.KeyedByTxid {
		return json.Marshal(outputs)
	}
	return json.Marshal(coinbaseObject{Memo: c.Memo, Outputs: outputs})
}

// Body holds a block's coins. Coinbase outputs belong to the body itself, not
// to any transaction.
type Body struct {
	Coinbase       Coinbase        `json:"coinbase"`
	Transactions   []Transaction   `json:"transactions"`
	Authorizations []Authorization `json:"authorizations"`
}

// Block is what get_block returns.
type Block struct {
	Header Header `json:"header"`
	Body   Body   `json:"body"`
}

// CoinbaseSource returns the hash each coinbase outpoint of this block names.
func (b *Block) CoinbaseSource() Hash {
	if b.Body.Coinbase.KeyedByTxid {
		return CoinbaseTxid(b.Header)
	}
	return b.Header.MerkleRoot
}

// CoinbaseTxid is the blake3 digest over the borsh encoding of the merkle
// root, the previous mainchain hash, and the optional previous sidechain hash.
func CoinbaseTxid(header Header) Hash {
	encoding := make([]byte, 0, 32+32+1+32)
	encoding = append(encoding, header.MerkleRoot[:]...)
	encoding = append(encoding, header.PrevMainHash[:]...)
	if header.PrevSideHash == nil {
		encoding = append(encoding, 0)
	} else {
		encoding = append(encoding, 1)
		encoding = append(encoding, header.PrevSideHash[:]...)
	}
	return Hash(blake3.Sum256(encoding))
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
}

// TxInfo names one transaction. A body carries neither field: a txid is a
// blake3 digest over the borsh encoding, and a size is that encoding's length.
// The node computes all three, so no chain-specific encoder runs here.
type TxInfo struct {
	Txid Hash   `json:"txid"`
	Size uint64 `json:"size"`
	// Raw is the borsh encoding. It is what /tx/{txid}/hex serves.
	Raw Bytes `json:"raw"`
}

// MempoolTx is one entry of list_mempool. It carries the whole transaction
// beside the identity the node computed for it.
type MempoolTx struct {
	TxInfo
	Tx Transaction `json:"tx"`
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

// Deposit is one output a mainchain deposit created. The node names its
// fields, because a tuple of ref schemas does not compose in the OpenAPI
// document.
type Deposit struct {
	OutPoint OutPoint `json:"outpoint"`
	Output   Output   `json:"output"`
}

// BundleSpend is one output a withdrawal bundle removed, with the bundle that
// took it.
type BundleSpend struct {
	OutPoint OutPoint    `json:"outpoint"`
	M6id     BitcoinHash `json:"m6id"`
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
