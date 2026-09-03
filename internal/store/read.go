package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// TxoStats counts what an address funded and spent.
type TxoStats struct {
	FundedCount int
	FundedSum   int64
	SpentCount  int
	SpentSum    int64
	TxCount     int
}

// UTXO is one unspent output.
type UTXO struct {
	OutPoint    chain.OutPoint
	ValueSats   int64
	Height      uint32
	BlockHash   chain.Hash
	BlockTime   *int64
	ContentType string
	Content     json.RawMessage
	HeightExact bool
	// Unconfirmed marks an output the mempool holds. No block carries it, so
	// its height and its block hash are empty.
	Unconfirmed bool
}

// TxRef names one transaction in an address history.
type TxRef struct {
	Txid   chain.Hash
	Height uint32
}

// Stats counts what one column value funded and spent. The column is either
// address or scripthash, and both carry the same index shape.
func (s *Store) Stats(ctx context.Context, column string, key []byte) (TxoStats, error) {
	if err := checkKeyColumn(column); err != nil {
		return TxoStats{}, err
	}
	query := fmt.Sprintf(`
		WITH mine AS (SELECT * FROM outputs WHERE %s = $1)
		SELECT
			COUNT(*),
			COALESCE(SUM(value_sats), 0),
			COUNT(*) FILTER (WHERE spent_source IS NOT NULL),
			COALESCE(SUM(value_sats) FILTER (WHERE spent_source IS NOT NULL), 0),
			(SELECT COUNT(*) FROM (
				SELECT source_id AS id FROM mine
				UNION
				SELECT spent_source FROM mine WHERE spent_source IS NOT NULL
			) touched)
		FROM mine`, column)

	var out TxoStats
	err := s.pool.QueryRow(ctx, query, key).Scan(
		&out.FundedCount, &out.FundedSum, &out.SpentCount, &out.SpentSum, &out.TxCount)
	if err != nil {
		return TxoStats{}, fmt.Errorf("read %s stats: %w", column, err)
	}
	return out, nil
}

// UTXOs lists the unspent outputs of one address or scripthash, newest first.
func (s *Store) UTXOs(ctx context.Context, column string, key []byte) ([]UTXO, error) {
	if err := checkKeyColumn(column); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		SELECT o.kind, o.source_id, o.vout, o.value_sats, o.height,
		       o.block_hash, b.block_time, o.content_type, o.content, o.height_exact
		FROM outputs o
		JOIN blocks b ON b.height = o.height
		WHERE o.%s = $1 AND o.spent_source IS NULL
		ORDER BY o.height DESC, o.vout`, column)

	rows, err := s.pool.Query(ctx, query, key)
	if err != nil {
		return nil, fmt.Errorf("read %s utxos: %w", column, err)
	}
	defer rows.Close()

	return scanUTXOs(rows)
}

// Deposits lists deposit outputs, newest first. A zero mainchainTxid lists every
// deposit below startHeight.
func (s *Store) Deposits(
	ctx context.Context, mainchainTxid []byte, startHeight uint32, limit int,
) ([]UTXO, error) {
	query := `
		SELECT o.kind, o.source_id, o.vout, o.value_sats, o.height,
		       o.block_hash, b.block_time, o.content_type, o.content, o.height_exact
		FROM outputs o
		JOIN blocks b ON b.height = o.height
		WHERE o.kind = 2
		  AND ($1::bytea IS NULL OR o.source_id = $1)
		  AND o.height <= $2
		ORDER BY o.height DESC, o.vout
		LIMIT $3`

	var key any
	if len(mainchainTxid) > 0 {
		key = mainchainTxid
	}
	rows, err := s.pool.Query(ctx, query, key, clampHeight(startHeight), limit)
	if err != nil {
		return nil, fmt.Errorf("read deposits: %w", err)
	}
	defer rows.Close()

	return scanUTXOs(rows)
}

// AddressDeposits lists the deposits that paid one address, newest first.
func (s *Store) AddressDeposits(ctx context.Context, address chain.Address, limit int) ([]UTXO, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.kind, o.source_id, o.vout, o.value_sats, o.height,
		       o.block_hash, b.block_time, o.content_type, o.content, o.height_exact
		FROM outputs o
		JOIN blocks b ON b.height = o.height
		WHERE o.address = $1 AND o.kind = 2
		ORDER BY o.height DESC, o.vout
		LIMIT $2`, address[:], limit)
	if err != nil {
		return nil, fmt.Errorf("read address deposits: %w", err)
	}
	defer rows.Close()

	return scanUTXOs(rows)
}

func scanUTXOs(rows pgx.Rows) ([]UTXO, error) {
	var out []UTXO
	for rows.Next() {
		var (
			u      UTXO
			kind   int16
			source []byte
			block  []byte
			vout   int32
			height int32
		)
		if err := rows.Scan(&kind, &source, &vout, &u.ValueSats, &height,
			&block, &u.BlockTime, &u.ContentType, &u.Content, &u.HeightExact); err != nil {
			return nil, fmt.Errorf("read output row: %w", err)
		}
		u.OutPoint.Kind = chain.OutPointKind(kind)
		u.OutPoint.Vout = uint32(vout)
		copy(u.OutPoint.Source[:], source)
		copy(u.BlockHash[:], block)
		u.Height = uint32(height)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read output rows: %w", err)
	}
	return out, nil
}

// History lists the transactions that touched one address or scripthash, newest
// first. Pass a height below the tip to page backwards.
func (s *Store) History(
	ctx context.Context, column string, key []byte, beforeHeight uint32, limit int,
) ([]TxRef, error) {
	if err := checkKeyColumn(column); err != nil {
		return nil, err
	}
	// A row reaches the history twice: once for the block that created it, and
	// once for the block that spent it.
	query := fmt.Sprintf(`
		WITH mine AS (SELECT * FROM outputs WHERE %s = $1)
		SELECT id, height FROM (
			SELECT source_id AS id, height FROM mine WHERE kind = 0
			UNION
			SELECT spent_source, spent_height FROM mine WHERE spent_source IS NOT NULL
		) touched
		WHERE height <= $2
		ORDER BY height DESC, id
		LIMIT $3`, column)

	rows, err := s.pool.Query(ctx, query, key, clampHeight(beforeHeight), limit)
	if err != nil {
		return nil, fmt.Errorf("read %s history: %w", column, err)
	}
	defer rows.Close()

	var out []TxRef
	for rows.Next() {
		var (
			id     []byte
			height int32
		)
		if err := rows.Scan(&id, &height); err != nil {
			return nil, fmt.Errorf("read history row: %w", err)
		}
		var ref TxRef
		copy(ref.Txid[:], id)
		ref.Height = uint32(height)
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read history rows: %w", err)
	}
	return out, nil
}

// clampHeight keeps a height inside the range the height column holds. A
// caller that means "no upper bound" passes the largest uint32, and a plain
// cast would turn that into -1 and match nothing.
func clampHeight(height uint32) int32 {
	if height > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(height)
}

// checkKeyColumn guards the one place this package builds SQL by hand. Only
// these two columns ever reach a query string.
func checkKeyColumn(column string) error {
	switch column {
	case ColumnAddress, ColumnScriptHash:
		return nil
	default:
		return fmt.Errorf("cannot key a query on column %q", column)
	}
}

// The columns an address-shaped query may key on.
const (
	ColumnAddress    = "address"
	ColumnScriptHash = "scripthash"
)
