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

// TimeSource reads the mainchain block time a sidechain header points at. A
// sidechain header carries no timestamp of its own.
type TimeSource interface {
	BlockTime(ctx context.Context, mainHash chain.BitcoinHash) (int64, error)
}

// Syncer walks the chain forward and keeps the index level with the node.
//
// The node and the database each sit behind a service wrapper, so neither one
// has to be up when this process starts.
type Syncer struct {
	nodes   *service.Service[*chain.Node]
	stores  *service.Service[*store.Store]
	decoder chain.Decoder
	times   TimeSource
	log     *slog.Logger

	node  *chain.Node
	store *store.Store

	// Interval is how long the syncer waits after it reaches the tip.
	Interval time.Duration
}

// NewSyncer builds a syncer. A nil TimeSource leaves every block time empty.
func NewSyncer(
	nodes *service.Service[*chain.Node],
	stores *service.Service[*store.Store],
	decoder chain.Decoder,
	times TimeSource,
	log *slog.Logger,
) *Syncer {
	return &Syncer{
		nodes:    nodes,
		stores:   stores,
		decoder:  decoder,
		times:    times,
		log:      log,
		Interval: 2 * time.Second,
	}
}

// Run follows the chain until the context ends. It never returns because a
// dependency is down; it waits and tries again.
func (s *Syncer) Run(ctx context.Context) error {
	for {
		err := s.Once(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, service.ErrUnavailable):
			s.log.Debug("waiting for a dependency", "error", err)
		case err != nil:
			s.log.Error("sync pass failed, retrying", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Interval):
		}
	}
}

// Once walks from the index tip to the node tip one time.
func (s *Syncer) Once(ctx context.Context) error {
	var err error
	if s.node, err = s.nodes.Get(ctx); err != nil {
		return err
	}
	if s.store, err = s.stores.Get(ctx); err != nil {
		return err
	}

	nodeTip, err := s.node.TipHeight(ctx)
	if errors.Is(err, chain.ErrEmptyChain) {
		return nil
	}
	if err != nil {
		s.nodes.Drop()
		return fmt.Errorf("read node tip: %w", err)
	}

	next, err := s.resolveStart(ctx)
	if err != nil {
		return err
	}

	for height := next; height <= nodeTip; height++ {
		applied, err := s.applyHeight(ctx, height)
		if err != nil {
			return err
		}
		if !applied {
			// The node moved under us. The next pass picks it up.
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// resolveStart finds the next height to read. It rolls back first when the node
// reorganized under the index.
func (s *Syncer) resolveStart(ctx context.Context) (uint32, error) {
	tip, tipHash, ok, err := s.store.Tip(ctx)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}

	nodeHash, err := s.node.BlockHashAt(ctx, tip)
	if err != nil {
		return 0, fmt.Errorf("read node hash at height %d: %w", tip, err)
	}
	if nodeHash != nil && *nodeHash == tipHash {
		return tip + 1, nil
	}

	fork, err := s.findFork(ctx, tip)
	if err != nil {
		return 0, err
	}
	s.log.Warn("node reorganized, rolling back", "from", tip, "to", fork)
	if err := s.store.Rollback(ctx, fork); err != nil {
		return 0, err
	}
	return fork + 1, nil
}

// findFork walks down until the index and the node agree on a hash. It returns
// the highest height they share.
func (s *Syncer) findFork(ctx context.Context, from uint32) (uint32, error) {
	for height := from; height > 0; height-- {
		below := height - 1
		ours, ok, err := s.store.HashAt(ctx, below)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		theirs, err := s.node.BlockHashAt(ctx, below)
		if err != nil {
			return 0, fmt.Errorf("read node hash at height %d: %w", below, err)
		}
		if theirs != nil && *theirs == ours {
			return below, nil
		}
	}
	// The chains disagree at genesis, so nothing survives.
	return 0, s.store.Rollback(ctx, 0)
}

// applyHeight reads and writes one block. It reports false when the node no
// longer holds a block at that height.
func (s *Syncer) applyHeight(ctx context.Context, height uint32) (bool, error) {
	hash, err := s.node.BlockHashAt(ctx, height)
	if err != nil {
		return false, fmt.Errorf("read node hash at height %d: %w", height, err)
	}
	if hash == nil {
		return false, nil
	}

	block, err := s.node.GetBlock(ctx, *hash)
	if err != nil {
		return false, err
	}
	if block == nil {
		return false, fmt.Errorf("node has a hash at height %d but no header for %s", height, hash)
	}

	blockIndex, err := s.node.GetBlockIndex(ctx, *hash)
	if err != nil {
		return false, err
	}

	blockTime, err := s.blockTime(ctx, block.Header.PrevMainHash)
	if err != nil {
		return false, err
	}

	write, err := Prepare(height, *hash, block, blockIndex, s.decoder, blockTime)
	if err != nil {
		return false, err
	}

	result, err := s.store.Apply(ctx, write)
	if err != nil {
		return false, err
	}
	if result.UnknownSpends > 0 {
		s.log.Warn("block spends outputs the index does not hold",
			"height", height, "hash", hash, "count", result.UnknownSpends)
	}
	return true, nil
}

func (s *Syncer) blockTime(ctx context.Context, mainHash chain.BitcoinHash) (*int64, error) {
	if s.times == nil {
		return nil, nil
	}
	t, err := s.times.BlockTime(ctx, mainHash)
	if err != nil {
		return nil, fmt.Errorf("read mainchain time for %s: %w", mainHash, err)
	}
	return &t, nil
}
