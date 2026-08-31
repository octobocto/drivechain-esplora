package index

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/service"
)

// Broadcaster hands a raw transaction to the node.
type Broadcaster struct {
	nodes *service.Service[*chain.Node]
}

// NewBroadcaster builds a broadcaster over the node service.
func NewBroadcaster(nodes *service.Service[*chain.Node]) *Broadcaster {
	return &Broadcaster{nodes: nodes}
}

// Broadcast submits one authorized transaction and returns its txid. The body
// is the borsh encoding of an authorized transaction, as hex.
func (b *Broadcaster) Broadcast(ctx context.Context, raw []byte) (chain.Hash, error) {
	node, err := b.nodes.Get(ctx)
	if err != nil {
		return chain.Hash{}, err
	}
	txid, err := node.SubmitTransaction(ctx, hex.EncodeToString(raw))
	if err != nil {
		b.nodes.Drop()
		return chain.Hash{}, fmt.Errorf("submit transaction: %w", err)
	}
	return txid, nil
}
