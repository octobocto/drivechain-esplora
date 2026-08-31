package index

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/service"
)

// Broadcaster hands a signed transaction to the node.
type Broadcaster struct {
	nodes *service.Service[*chain.Node]
}

// NewBroadcaster builds a broadcaster over the node service.
func NewBroadcaster(nodes *service.Service[*chain.Node]) *Broadcaster {
	return &Broadcaster{nodes: nodes}
}

// Broadcast submits one authorized transaction and returns its txid. The
// transaction arrives as the JSON a node reads, and travels on unchanged.
func (b *Broadcaster) Broadcast(ctx context.Context, tx json.RawMessage) (chain.Hash, error) {
	node, err := b.nodes.Get(ctx)
	if err != nil {
		return chain.Hash{}, err
	}
	txid, err := node.SubmitTransaction(ctx, tx)
	if err != nil {
		b.nodes.Drop()
		return chain.Hash{}, fmt.Errorf("submit transaction: %w", err)
	}
	return txid, nil
}
