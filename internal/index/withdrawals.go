package index

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/service"
)

// Withdrawals reads the node's withdrawal bundle state. A light client runs no
// node of its own, so the index answers for it.
type Withdrawals struct {
	nodes *service.Service[*chain.Node]
}

// NewWithdrawals builds a reader over the node service.
func NewWithdrawals(nodes *service.Service[*chain.Node]) *Withdrawals {
	return &Withdrawals{nodes: nodes}
}

// Pending is the bundle the node proposes to the mainchain, as the node writes
// it. A chain with no bundle answers nil.
func (w *Withdrawals) Pending(ctx context.Context) (json.RawMessage, error) {
	node, err := w.nodes.Get(ctx)
	if err != nil {
		return nil, err
	}
	bundle, err := node.PendingWithdrawalBundle(ctx)
	if err != nil {
		w.nodes.Drop()
		return nil, fmt.Errorf("read the pending withdrawal bundle: %w", err)
	}
	return bundle, nil
}

// LastFailedHeight is the sidechain height of the last bundle the mainchain
// rejected. A chain that never failed one answers nil.
func (w *Withdrawals) LastFailedHeight(ctx context.Context) (*uint32, error) {
	node, err := w.nodes.Get(ctx)
	if err != nil {
		return nil, err
	}
	height, err := node.LatestFailedWithdrawalBundleHeight(ctx)
	if err != nil {
		w.nodes.Drop()
		return nil, fmt.Errorf("read the last failed bundle height: %w", err)
	}
	return height, nil
}
