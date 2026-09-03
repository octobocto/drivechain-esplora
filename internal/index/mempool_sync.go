package index

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/service"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// MempoolSyncer keeps the unconfirmed set level with the node.
//
// The node names the transactions it would mine next, which is its mempool. A
// pass replaces the whole snapshot, so a transaction the node dropped leaves
// the index with it.
type MempoolSyncer struct {
	nodes   *service.Service[*chain.Node]
	stores  *service.Service[*store.Store]
	decoder chain.Decoder
	log     *slog.Logger

	// Interval is how long the syncer waits between passes.
	Interval time.Duration
}

// NewMempoolSyncer builds a poller. Give it its own node service, so a slow
// block pass never delays an unconfirmed payment.
func NewMempoolSyncer(
	nodes *service.Service[*chain.Node],
	stores *service.Service[*store.Store],
	decoder chain.Decoder,
	log *slog.Logger,
) *MempoolSyncer {
	return &MempoolSyncer{
		nodes:    nodes,
		stores:   stores,
		decoder:  decoder,
		log:      log,
		Interval: PollInterval,
	}
}

// Run polls until the context ends. It never returns because a dependency is
// down; it waits and tries again.
func (s *MempoolSyncer) Run(ctx context.Context) error {
	for {
		err := s.Once(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, service.ErrUnavailable):
			s.log.Debug("waiting for a dependency", "error", err)
		case err != nil:
			s.log.Warn("mempool pass failed, retrying", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Interval):
		}
	}
}

// Once reads the node one time and replaces the snapshot.
//
// A failed read leaves the last snapshot alone. The node holds the answer, and
// an empty index would read as a payment that went away.
func (s *MempoolSyncer) Once(ctx context.Context) error {
	node, err := s.nodes.Get(ctx)
	if err != nil {
		return err
	}
	st, err := s.stores.Get(ctx)
	if err != nil {
		return err
	}

	template, err := node.BlockTemplate(ctx)
	if err != nil {
		s.nodes.Drop()
		return fmt.Errorf("read the block template: %w", err)
	}
	if template == nil {
		return errors.New("the node answered with no block template")
	}

	pool, err := PrepareMempool(template.Block.Body.Transactions, s.decoder)
	if err != nil {
		return err
	}
	return st.ReplaceMempool(ctx, pool)
}
