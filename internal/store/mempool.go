package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// MempoolTx is one unconfirmed transaction. The fee comes from the rows the
// snapshot writes, so no caller supplies it.
type MempoolTx struct {
	Txid      chain.Hash
	Index     int
	SizeBytes int
	Raw       []byte
	// FirstSeen is when this index first held the transaction, in unix seconds.
	FirstSeen int64
}

// Mempool is the whole unconfirmed set. A pass replaces it as one value, so a
// transaction that leaves the node leaves the index with it.
type Mempool struct {
	Txs     []MempoolTx
	Creates []Output
	Spends  []Spend
}

// ReplaceMempool swaps the unconfirmed set for a new one, in a single
// transaction. A reader therefore never sees half a snapshot.
//
// A transaction that both snapshots hold keeps its original first_seen, so the
// arrival order a caller reads stays true across passes.
func (s *Store) ReplaceMempool(ctx context.Context, pool Mempool) error {
	now := time.Now().Unix()
	return s.inTx(ctx, func(tx pgx.Tx) error {
		seen := make(map[string]int64)
		rows, err := tx.Query(ctx, `SELECT txid, first_seen FROM mempool_txs`)
		if err != nil {
			return fmt.Errorf("read mempool first seen: %w", err)
		}
		for rows.Next() {
			var txid []byte
			var at int64
			if err := rows.Scan(&txid, &at); err != nil {
				rows.Close()
				return fmt.Errorf("scan mempool first seen: %w", err)
			}
			seen[string(txid)] = at
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read mempool first seen: %w", err)
		}

		for _, table := range []string{"mempool_spends", "mempool_outputs", "mempool_txs"} {
			if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil {
				return fmt.Errorf("clear %s: %w", table, err)
			}
		}

		for _, mtx := range pool.Txs {
			at, ok := seen[string(mtx.Txid[:])]
			if !ok {
				at = now
			}
			txid := mtx.Txid
			_, err := tx.Exec(ctx, `
				INSERT INTO mempool_txs (txid, tx_index, size_bytes, fee_sats, raw, first_seen)
				VALUES ($1, $2, $3, 0, $4, $5)`,
				txid[:], int32(mtx.Index), int32(mtx.SizeBytes), mtx.Raw, at)
			if err != nil {
				return fmt.Errorf("insert mempool tx %s: %w", mtx.Txid, err)
			}
		}

		for _, out := range pool.Creates {
			key := out.OutPoint.Key()
			scripthash := out.Address.ScriptHash()
			_, err := tx.Exec(ctx, `
				INSERT INTO mempool_outputs
					(outpoint, kind, source_id, vout, address, scripthash, value_sats, content, content_type)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (outpoint) DO NOTHING`,
				key[:], int16(out.OutPoint.Kind), out.OutPoint.Source[:], int32(out.OutPoint.Vout),
				out.Address[:], scripthash[:], out.ValueSats, []byte(out.Content), out.ContentType)
			if err != nil {
				return fmt.Errorf("insert mempool output: %w", err)
			}
		}

		for _, spend := range pool.Spends {
			key := spend.OutPoint.Key()
			source := spend.Source
			_, err := tx.Exec(ctx, `
				INSERT INTO mempool_spends (outpoint, source, kind, vin)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (outpoint) DO NOTHING`,
				key[:], source[:], int16(spend.Kind), int32(spend.Vin))
			if err != nil {
				return fmt.Errorf("insert mempool spend: %w", err)
			}
		}
		return setMempoolFees(ctx, tx)
	})
}

// setMempoolFees computes each fee from the rows the snapshot just wrote. An
// input takes its value from a confirmed output or from another transaction of
// the same snapshot, so both joins count.
//
// A fee floors at zero, because a client reads it as an amount. It falls below
// zero only when the index holds no output for an input.
func setMempoolFees(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
		UPDATE mempool_txs t SET fee_sats = GREATEST(
			COALESCE((SELECT SUM(o.value_sats) FROM mempool_spends s
			          JOIN outputs o ON o.outpoint = s.outpoint
			          WHERE s.source = t.txid AND s.kind = 0), 0)
			+ COALESCE((SELECT SUM(m.value_sats) FROM mempool_spends s
			            JOIN mempool_outputs m ON m.outpoint = s.outpoint
			            WHERE s.source = t.txid AND s.kind = 0), 0)
			- COALESCE((SELECT SUM(value_sats) FROM mempool_outputs
			            WHERE source_id = t.txid AND kind = 0), 0),
			0)`)
	if err != nil {
		return fmt.Errorf("compute mempool fees: %w", err)
	}
	return nil
}

// MempoolStats counts what the unconfirmed set funds and spends for one address
// or scripthash. It fills the mempool_stats an esplora caller reads.
//
// A spend counts whether the output it takes is confirmed or unconfirmed, so a
// wallet sees its own outgoing payment at once.
func (s *Store) MempoolStats(ctx context.Context, column string, key []byte) (TxoStats, error) {
	if err := checkKeyColumn(column); err != nil {
		return TxoStats{}, err
	}
	query := fmt.Sprintf(`
		WITH funded AS (
			SELECT outpoint, source_id, value_sats
			FROM mempool_outputs WHERE %s = $1
		),
		spent AS (
			SELECT s.source, o.value_sats
			FROM mempool_spends s
			JOIN outputs o ON o.outpoint = s.outpoint
			WHERE o.%s = $1
			UNION ALL
			SELECT s.source, m.value_sats
			FROM mempool_spends s
			JOIN mempool_outputs m ON m.outpoint = s.outpoint
			WHERE m.%s = $1
		)
		SELECT
			(SELECT COUNT(*) FROM funded),
			(SELECT COALESCE(SUM(value_sats), 0) FROM funded),
			(SELECT COUNT(*) FROM spent),
			(SELECT COALESCE(SUM(value_sats), 0) FROM spent),
			(SELECT COUNT(*) FROM (
				SELECT source_id AS id FROM funded
				UNION
				SELECT source FROM spent
			) touched)`, column, column, column)

	var out TxoStats
	err := s.pool.QueryRow(ctx, query, key).Scan(
		&out.FundedCount, &out.FundedSum, &out.SpentCount, &out.SpentSum, &out.TxCount)
	if err != nil {
		return TxoStats{}, fmt.Errorf("read %s mempool stats: %w", column, err)
	}
	return out, nil
}

// MempoolSpentOutpoints names the confirmed outputs the unconfirmed set spends.
// A UTXO read drops these, so a spend leaves the balance at once rather than at
// the next block.
func (s *Store) MempoolSpentOutpoints(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT outpoint FROM mempool_spends`)
	if err != nil {
		return nil, fmt.Errorf("read mempool spends: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var outpoint []byte
		if err := rows.Scan(&outpoint); err != nil {
			return nil, fmt.Errorf("scan mempool spend: %w", err)
		}
		out[string(outpoint)] = true
	}
	return out, rows.Err()
}

// MempoolUTXOs lists the unconfirmed outputs of one address or scripthash that
// nothing else in the mempool spends.
func (s *Store) MempoolUTXOs(ctx context.Context, column string, key []byte) ([]UTXO, error) {
	if err := checkKeyColumn(column); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		SELECT m.kind, m.source_id, m.vout, m.value_sats, m.content_type, m.content
		FROM mempool_outputs m
		LEFT JOIN mempool_spends s ON s.outpoint = m.outpoint
		WHERE m.%s = $1 AND s.outpoint IS NULL
		ORDER BY m.vout`, column)

	rows, err := s.pool.Query(ctx, query, key)
	if err != nil {
		return nil, fmt.Errorf("read %s mempool utxos: %w", column, err)
	}
	defer rows.Close()

	var out []UTXO
	for rows.Next() {
		var (
			u      UTXO
			kind   int16
			source []byte
			vout   uint32
		)
		if err := rows.Scan(&kind, &source, &vout, &u.ValueSats, &u.ContentType, &u.Content); err != nil {
			return nil, fmt.Errorf("scan mempool utxo: %w", err)
		}
		u.OutPoint.Kind = chain.OutPointKind(kind)
		copy(u.OutPoint.Source[:], source)
		u.OutPoint.Vout = vout
		u.Unconfirmed = true
		out = append(out, u)
	}
	return out, rows.Err()
}

// MempoolTxids names every unconfirmed transaction, newest first.
func (s *Store) MempoolTxids(ctx context.Context) ([]chain.Hash, error) {
	rows, err := s.pool.Query(ctx, `SELECT txid FROM mempool_txs ORDER BY first_seen DESC`)
	if err != nil {
		return nil, fmt.Errorf("read mempool txids: %w", err)
	}
	defer rows.Close()

	var out []chain.Hash
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan mempool txid: %w", err)
		}
		var h chain.Hash
		copy(h[:], raw)
		out = append(out, h)
	}
	return out, rows.Err()
}

// MempoolSummary counts the unconfirmed set and what it pays.
func (s *Store) MempoolSummary(ctx context.Context) (count int, vsize int64, fees int64, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0), COALESCE(SUM(fee_sats), 0)
		FROM mempool_txs`).Scan(&count, &vsize, &fees)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read mempool summary: %w", err)
	}
	return count, vsize, fees, nil
}

// MempoolTxidsFor names the unconfirmed transactions that touch one address or
// scripthash, newest first.
func (s *Store) MempoolTxidsFor(ctx context.Context, column string, key []byte) ([]chain.Hash, error) {
	if err := checkKeyColumn(column); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		SELECT t.txid FROM mempool_txs t
		WHERE t.txid IN (
			SELECT source_id FROM mempool_outputs WHERE %s = $1
			UNION
			SELECT s.source FROM mempool_spends s
			JOIN outputs o ON o.outpoint = s.outpoint WHERE o.%s = $1
			UNION
			SELECT s.source FROM mempool_spends s
			JOIN mempool_outputs m ON m.outpoint = s.outpoint WHERE m.%s = $1
		)
		ORDER BY t.first_seen DESC`, column, column, column)

	rows, err := s.pool.Query(ctx, query, key)
	if err != nil {
		return nil, fmt.Errorf("read %s mempool txids: %w", column, err)
	}
	defer rows.Close()

	var out []chain.Hash
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan mempool txid: %w", err)
		}
		var h chain.Hash
		copy(h[:], raw)
		out = append(out, h)
	}
	return out, rows.Err()
}

// MempoolTxRow reads one unconfirmed transaction with the coins on both sides.
// It answers ErrNotFound when the snapshot holds no such transaction.
//
// An input takes its coin from a confirmed output or from another transaction
// of the same snapshot, so both joins run.
func (s *Store) MempoolTxRow(ctx context.Context, txid chain.Hash) (TxRow, error) {
	var (
		out   TxRow
		index int32
		size  int32
	)
	err := s.pool.QueryRow(ctx,
		`SELECT tx_index, size_bytes, fee_sats, raw FROM mempool_txs WHERE txid = $1`,
		txid[:]).Scan(&index, &size, &out.FeeSats, &out.Raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return TxRow{}, ErrNotFound
	}
	if err != nil {
		return TxRow{}, fmt.Errorf("read mempool transaction %s: %w", txid, err)
	}
	out.Txid = txid
	out.Index = int(index)
	out.SizeBytes = int(size)
	out.Unconfirmed = true

	out.Vin, err = s.mempoolCoins(ctx, `
		SELECT s.vin, o.kind, o.source_id, o.vout, o.address, o.value_sats,
		       o.content, o.content_type
		FROM mempool_spends s JOIN outputs o ON o.outpoint = s.outpoint
		WHERE s.source = $1 AND s.kind = 0
		UNION ALL
		SELECT s.vin, m.kind, m.source_id, m.vout, m.address, m.value_sats,
		       m.content, m.content_type
		FROM mempool_spends s JOIN mempool_outputs m ON m.outpoint = s.outpoint
		WHERE s.source = $1 AND s.kind = 0
		ORDER BY vin`, txid[:])
	if err != nil {
		return TxRow{}, fmt.Errorf("read inputs of %s: %w", txid, err)
	}

	out.Vout, err = s.mempoolCoins(ctx, `
		SELECT vout, kind, source_id, vout, address, value_sats, content, content_type
		FROM mempool_outputs WHERE source_id = $1 AND kind = 0
		ORDER BY vout`, txid[:])
	if err != nil {
		return TxRow{}, fmt.Errorf("read outputs of %s: %w", txid, err)
	}
	return out, nil
}

// mempoolCoins reads coins a mempool query names. The first column orders the
// rows and nothing else reads it.
func (s *Store) mempoolCoins(ctx context.Context, query string, args ...any) ([]Coin, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Coin
	for rows.Next() {
		var (
			c       Coin
			order   int32
			kind    int16
			source  []byte
			vout    int32
			address []byte
		)
		if err := rows.Scan(&order, &kind, &source, &vout, &address,
			&c.ValueSats, &c.Content, &c.ContentType); err != nil {
			return nil, fmt.Errorf("read mempool coin row: %w", err)
		}
		c.OutPoint.Kind = chain.OutPointKind(kind)
		c.OutPoint.Vout = uint32(vout)
		copy(c.OutPoint.Source[:], source)
		copy(c.Address[:], address)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read mempool coin rows: %w", err)
	}
	return out, nil
}

// MempoolEntry is one row of the recent list: what a transaction pays, what it
// costs, and how big it is.
type MempoolEntry struct {
	Txid      chain.Hash
	FeeSats   int64
	SizeBytes int
	ValueSats int64
}

// MempoolRecent lists the newest unconfirmed transactions, newest first.
func (s *Store) MempoolRecent(ctx context.Context, limit int) ([]MempoolEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.txid, t.fee_sats, t.size_bytes,
		       COALESCE((SELECT SUM(value_sats) FROM mempool_outputs
		                 WHERE source_id = t.txid AND kind = 0), 0)
		FROM mempool_txs t
		ORDER BY t.first_seen DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read recent mempool transactions: %w", err)
	}
	defer rows.Close()

	var out []MempoolEntry
	for rows.Next() {
		var (
			entry MempoolEntry
			raw   []byte
			size  int32
		)
		if err := rows.Scan(&raw, &entry.FeeSats, &size, &entry.ValueSats); err != nil {
			return nil, fmt.Errorf("scan recent mempool transaction: %w", err)
		}
		copy(entry.Txid[:], raw)
		entry.SizeBytes = int(size)
		out = append(out, entry)
	}
	return out, rows.Err()
}
