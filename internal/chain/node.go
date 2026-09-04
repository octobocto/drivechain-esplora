package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Caller runs one JSON-RPC method against a node.
type Caller interface {
	Call(ctx context.Context, method string, params any, out any) error
	URL() string
}

// ErrEmptyChain says the node holds no blocks yet.
var ErrEmptyChain = errors.New("node has no blocks")

// Node wraps a Caller with the methods this index reads.
type Node struct {
	rpc Caller
}

// NewNode wraps a Caller.
func NewNode(rpc Caller) *Node { return &Node{rpc: rpc} }

// URL is the node address.
func (n *Node) URL() string { return n.rpc.URL() }

// BestBlockHash returns the current tip, or nil when the chain is empty.
func (n *Node) BestBlockHash(ctx context.Context) (*Hash, error) {
	var hash *Hash
	if err := n.rpc.Call(ctx, "get_best_sidechain_block_hash", nil, &hash); err != nil {
		return nil, err
	}
	return hash, nil
}

// TipHeight returns the height of the tip. The node reports a block *count*,
// and genesis sits at height 0, so the tip is one below the count.
func (n *Node) TipHeight(ctx context.Context) (uint32, error) {
	var count uint32
	if err := n.rpc.Call(ctx, "getblockcount", nil, &count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, ErrEmptyChain
	}
	return count - 1, nil
}

// BlockHashAt returns the hash of the block at one height in the active chain,
// or nil above the tip.
func (n *Node) BlockHashAt(ctx context.Context, height uint32) (*Hash, error) {
	var hash *Hash
	if err := n.rpc.Call(ctx, "get_block_hash", []any{height}, &hash); err != nil {
		return nil, err
	}
	return hash, nil
}

// GetBlock returns one block, or nil when the node holds no such header.
//
// The node answers with an error, not with nil, when it holds the header but
// not yet the body. A caller retries rather than treating that as the end of
// the chain.
func (n *Node) GetBlock(ctx context.Context, hash Hash) (*Block, error) {
	var block *Block
	if err := n.rpc.Call(ctx, "get_block", []any{hash}, &block); err != nil {
		return nil, fmt.Errorf("get block %s: %w", hash, err)
	}
	return block, nil
}

// GetBlockIndex returns what the block body does not carry: the txids, the
// mainchain deposits, and the outputs a withdrawal bundle removed.
func (n *Node) GetBlockIndex(ctx context.Context, hash Hash) (BlockIndex, error) {
	var index BlockIndex
	if err := n.rpc.Call(ctx, "get_block_index", []any{hash}, &index); err != nil {
		return BlockIndex{}, fmt.Errorf("get index for block %s: %w", hash, err)
	}
	return index, nil
}

// SubmitTransaction broadcasts an authorized transaction and returns its txid.
func (n *Node) SubmitTransaction(ctx context.Context, tx any) (Hash, error) {
	var txid Hash
	if err := n.rpc.Call(ctx, "submit_transaction", []any{tx}, &txid); err != nil {
		return Hash{}, err
	}
	return txid, nil
}

// BlockTemplate returns the block the node would mine next. Its body holds the
// transactions the node accepted and no block carries yet.
func (n *Node) BlockTemplate(ctx context.Context) (*BlockTemplate, error) {
	var template *BlockTemplate
	if err := n.rpc.Call(ctx, "get_block_template", nil, &template); err != nil {
		return nil, fmt.Errorf("get block template: %w", err)
	}
	return template, nil
}

// PendingWithdrawalBundle reads the bundle the node proposes to the mainchain,
// as the node writes it. A chain with no bundle answers nil.
//
// The index holds no bundle model. It carries the node's own JSON, so a client
// reads one shape whether it runs a node or not.
func (n *Node) PendingWithdrawalBundle(ctx context.Context) (json.RawMessage, error) {
	var bundle json.RawMessage
	if err := n.rpc.Call(ctx, "pending_withdrawal_bundle", nil, &bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

// LatestFailedWithdrawalBundleHeight is the sidechain height of the last
// bundle the mainchain rejected. A chain that never failed one answers nil.
func (n *Node) LatestFailedWithdrawalBundleHeight(ctx context.Context) (*uint32, error) {
	var height *uint32
	if err := n.rpc.Call(ctx, "latest_failed_withdrawal_bundle_height", nil, &height); err != nil {
		return nil, err
	}
	return height, nil
}
