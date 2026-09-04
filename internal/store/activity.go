package store

import (
	"context"
	"fmt"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// Activity kinds. A deposit never appears in a block body, so a feed that
// reads only the transactions misses it.
const (
	KindTransfer   = "transfer"
	KindWithdrawal = "withdrawal"
	KindDeposit    = "deposit"
)

// ActivityRow is one thing that happened on the chain.
type ActivityRow struct {
	Kind      string
	ID        chain.Hash
	Height    uint32
	BlockHash chain.Hash
	BlockTime *int64
	FeeSats   int64
	SizeBytes int
	ValueSats int64
}

// activityQuery reads transactions and deposits as one feed. A transaction
// that creates a withdrawal output reads as a withdrawal, and a deposit keeps
// its mainchain txid.
const activityQuery = `
WITH feed AS (
	SELECT
		CASE WHEN EXISTS (
			SELECT 1 FROM outputs o
			WHERE o.source_id = t.txid AND o.kind = 0
			  AND o.content_type = 'withdrawal'
		) THEN 'withdrawal' ELSE 'transfer' END AS kind,
		t.txid AS id, t.height AS height, t.tx_index AS ordinal,
		t.fee_sats AS fee_sats, t.size_bytes AS size_bytes,
		COALESCE((SELECT SUM(o.value_sats) FROM outputs o
		          WHERE o.source_id = t.txid AND o.kind = 0), 0) AS value_sats
	FROM txs t
	UNION ALL
	SELECT 'deposit', o.source_id, o.height, -1, 0, 0, SUM(o.value_sats)
	FROM outputs o WHERE o.kind = 2 GROUP BY o.source_id, o.height
)
SELECT f.kind, f.id, f.height, f.fee_sats, f.size_bytes, f.value_sats,
       b.hash, b.block_time
FROM feed f JOIN blocks b ON b.height = f.height
`

// Activity lists the newest transactions and deposits together, newest first.
func (s *Store) Activity(ctx context.Context, limit int) ([]ActivityRow, error) {
	rows, err := s.pool.Query(ctx,
		activityQuery+` ORDER BY f.height DESC, f.ordinal DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read the activity feed: %w", err)
	}
	return scanActivity(rows)
}

// ActivityInBlock lists what one block carried, in body order. A deposit the
// block applied sorts after the transactions.
func (s *Store) ActivityInBlock(ctx context.Context, hash chain.Hash) ([]ActivityRow, error) {
	rows, err := s.pool.Query(ctx,
		activityQuery+` WHERE b.hash = $1 ORDER BY f.ordinal DESC`, hash[:])
	if err != nil {
		return nil, fmt.Errorf("read the block activity: %w", err)
	}
	return scanActivity(rows)
}

func scanActivity(rows rowSet) ([]ActivityRow, error) {
	defer rows.Close()

	var out []ActivityRow
	for rows.Next() {
		var (
			row    ActivityRow
			id     []byte
			hash   []byte
			height int32
			size   int32
		)
		if err := rows.Scan(&row.Kind, &id, &height, &row.FeeSats, &size,
			&row.ValueSats, &hash, &row.BlockTime); err != nil {
			return nil, fmt.Errorf("scan an activity row: %w", err)
		}
		row.Height = uint32(height)
		row.SizeBytes = int(size)
		copy(row.ID[:], id)
		copy(row.BlockHash[:], hash)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the activity rows: %w", err)
	}
	return out, nil
}

// rowSet is the part of pgx.Rows this file reads.
type rowSet interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}
