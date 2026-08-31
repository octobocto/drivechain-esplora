// Package api serves the Esplora REST API over the index.
package api

import (
	"encoding/hex"
	"encoding/json"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// ScriptPubKeyType is what a vout reports in place of a bitcoin script type.
// These chains have no script; an address is a 20-byte ed25519 address.
const ScriptPubKeyType = "sidechain_address"

// Status is where a transaction sits in the chain.
type Status struct {
	Confirmed   bool    `json:"confirmed"`
	BlockHeight *uint32 `json:"block_height"`
	BlockHash   *string `json:"block_hash"`
	BlockTime   *int64  `json:"block_time"`
}

// TxoStats counts what an address funded and spent.
type TxoStats struct {
	FundedTxoCount int   `json:"funded_txo_count"`
	FundedTxoSum   int64 `json:"funded_txo_sum"`
	SpentTxoCount  int   `json:"spent_txo_count"`
	SpentTxoSum    int64 `json:"spent_txo_sum"`
	TxCount        int   `json:"tx_count"`
}

// AddressInfo answers /address/{a} and /scripthash/{h}.
type AddressInfo struct {
	Address    string   `json:"address,omitempty"`
	ScriptHash string   `json:"scripthash,omitempty"`
	ChainStats TxoStats `json:"chain_stats"`
	// MempoolStats stays at zero. These nodes serve no mempool view, and a
	// client reads the field on every balance.
	MempoolStats TxoStats `json:"mempool_stats"`
}

// Vout is one output.
type Vout struct {
	ScriptPubKey        string `json:"scriptpubkey"`
	ScriptPubKeyAsm     string `json:"scriptpubkey_asm"`
	ScriptPubKeyType    string `json:"scriptpubkey_type"`
	ScriptPubKeyAddress string `json:"scriptpubkey_address"`
	Value               int64  `json:"value"`
	// OutpointKind names how the output came into being: regular, coinbase,
	// or deposit. Bitcoin has no such distinction.
	OutpointKind string `json:"outpoint_kind"`
	// Content is the chain-specific payload, such as a withdrawal.
	Content json.RawMessage `json:"content,omitempty"`
}

// Vin is one input.
type Vin struct {
	Txid       string   `json:"txid"`
	Vout       uint32   `json:"vout"`
	Prevout    *Vout    `json:"prevout"`
	ScriptSig  string   `json:"scriptsig"`
	Witness    []string `json:"witness"`
	IsCoinbase bool     `json:"is_coinbase"`
	Sequence   uint32   `json:"sequence"`
}

// Tx answers /tx/{txid}.
type Tx struct {
	Txid string `json:"txid"`
	// Version, Locktime and each Sequence stay at zero. These chains have no
	// such fields, and a client parser still reads them.
	Version  int    `json:"version"`
	Locktime uint32 `json:"locktime"`
	Size     int    `json:"size"`
	// Weight is Size times four, so a client that divides by four reads the
	// size back.
	Weight int    `json:"weight"`
	Fee    int64  `json:"fee"`
	Vin    []Vin  `json:"vin"`
	Vout   []Vout `json:"vout"`
	Status Status `json:"status"`
}

// UTXO answers /address/{a}/utxo.
type UTXO struct {
	Txid   string `json:"txid"`
	Vout   uint32 `json:"vout"`
	Value  int64  `json:"value"`
	Status Status `json:"status"`
	// OutpointKind names how the output came into being. A deposit txid is a
	// mainchain txid, not a sidechain one.
	OutpointKind string `json:"outpoint_kind"`
	// HeightExact is false for a deposit the node could not attribute to a
	// block. Such a row carries the height where the index first saw it.
	HeightExact bool `json:"height_exact"`
}

// Outspend answers /tx/{txid}/outspend/{vout}.
type Outspend struct {
	Spent  bool    `json:"spent"`
	Txid   *string `json:"txid"`
	Vin    *uint32 `json:"vin"`
	Status *Status `json:"status"`
	// SpentBy reads "transaction" or "withdrawal_bundle". A bundle spends an
	// output with no transaction at all, and Txid then names the bundle.
	SpentBy string `json:"spent_by,omitempty"`
}

// Block answers /block/{hash}.
type Block struct {
	ID           string  `json:"id"`
	Height       uint32  `json:"height"`
	Version      int     `json:"version"`
	Timestamp    *int64  `json:"timestamp"`
	TxCount      int     `json:"tx_count"`
	Size         int     `json:"size"`
	Weight       int     `json:"weight"`
	MerkleRoot   string  `json:"merkle_root"`
	PreviousHash *string `json:"previousblockhash"`
	// MainchainHash is the mainchain block this header points at. A sidechain
	// header carries no timestamp of its own.
	MainchainHash string `json:"mainchain_blockhash"`
}

// BlockStatus answers /block/{hash}/status.
type BlockStatus struct {
	InBestChain bool    `json:"in_best_chain"`
	Height      *uint32 `json:"height"`
	NextBest    *string `json:"next_best"`
}

// newStatus builds the confirmation block a client reads on every row.
func newStatus(height uint32, hash chain.Hash, blockTime *int64) Status {
	h := height
	s := hash.String()
	return Status{Confirmed: true, BlockHeight: &h, BlockHash: &s, BlockTime: blockTime}
}

// sourceString renders an outpoint source. A deposit names a mainchain txid, so
// it keeps mainchain byte order. Everything else keeps the chain's own order.
func sourceString(o chain.OutPoint) string {
	if o.Kind == chain.KindDeposit {
		return chain.BitcoinHash(o.Source).String()
	}
	return o.Source.String()
}

func newVout(c store.Coin) Vout {
	return Vout{
		ScriptPubKey:        hex.EncodeToString(c.Address[:]),
		ScriptPubKeyAsm:     "",
		ScriptPubKeyType:    ScriptPubKeyType,
		ScriptPubKeyAddress: c.Address.String(),
		Value:               c.ValueSats,
		OutpointKind:        c.OutPoint.Kind.String(),
		Content:             c.Content,
	}
}

func newTx(row store.TxRow) Tx {
	out := Tx{
		Txid:   row.Txid.String(),
		Size:   row.SizeBytes,
		Weight: row.SizeBytes * 4,
		Fee:    row.FeeSats,
		Vin:    make([]Vin, 0, len(row.Vin)),
		Vout:   make([]Vout, 0, len(row.Vout)),
		Status: newStatus(row.Height, row.Block.Hash, row.Block.BlockTime),
	}
	for _, coin := range row.Vin {
		prevout := newVout(coin)
		out.Vin = append(out.Vin, Vin{
			Txid: sourceString(coin.OutPoint),
			Vout: coin.OutPoint.Vout,
			// A client dereferences prevout with no nil check, so it is
			// always present.
			Prevout:    &prevout,
			ScriptSig:  "",
			Witness:    []string{},
			IsCoinbase: coin.OutPoint.Kind == chain.KindCoinbase,
		})
	}
	for _, coin := range row.Vout {
		out.Vout = append(out.Vout, newVout(coin))
	}
	return out
}

func newUTXO(u store.UTXO) UTXO {
	return UTXO{
		Txid:         sourceString(u.OutPoint),
		Vout:         u.OutPoint.Vout,
		Value:        u.ValueSats,
		Status:       newStatus(u.Height, u.BlockHash, u.BlockTime),
		OutpointKind: u.OutPoint.Kind.String(),
		HeightExact:  u.HeightExact,
	}
}

func newBlock(b store.BlockRow) Block {
	out := Block{
		ID:            b.Hash.String(),
		Height:        b.Height,
		Timestamp:     b.BlockTime,
		TxCount:       b.TxCount,
		MerkleRoot:    b.MerkleRoot.String(),
		MainchainHash: b.MainHash.String(),
	}
	if b.PrevHash != nil {
		prev := b.PrevHash.String()
		out.PreviousHash = &prev
	}
	return out
}
