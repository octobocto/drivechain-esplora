package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// ErrNotFound says the index holds no such row.
var ErrNotFound = errors.New("not found")

// Block is one indexed block header.
type BlockRow struct {
	Height     uint32
	Hash       chain.Hash
	PrevHash   *chain.Hash
	MerkleRoot chain.Hash
	MainHash   chain.BitcoinHash
	BlockTime  *int64
	TxCount    int
}

// TxRow is one indexed transaction, with the coins on both sides.
type TxRow struct {
	Txid      chain.Hash
	Height    uint32
	Index     int
	SizeBytes int
	FeeSats   int64
	Raw       []byte
	Block     BlockRow
	Vin       []Coin
	Vout      []Coin
}

// Coin is one output, seen either as a transaction input or as its output.
type Coin struct {
	OutPoint    chain.OutPoint
	Address     chain.Address
	ValueSats   int64
	Content     json.RawMessage
	ContentType string
	Height      uint32
	// SpentSource is the txid or m6id that spent this coin, if anything did.
	SpentSource *chain.Hash
	SpentKind   *chain.InPointKind
	SpentHeight *uint32
}

const blockColumns = `height, hash, prev_hash, merkle_root, main_hash, block_time, tx_count`

// BlockByHash reads one block header.
func (s *Store) BlockByHash(ctx context.Context, hash chain.Hash) (BlockRow, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+blockColumns+` FROM blocks WHERE hash = $1`, hash[:])
	return scanBlock(row)
}

// BlockAtHeight reads the block header at one height.
func (s *Store) BlockAtHeight(ctx context.Context, height uint32) (BlockRow, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+blockColumns+` FROM blocks WHERE height = $1`, int32(height))
	return scanBlock(row)
}

// Blocks lists up to limit block headers at or below startHeight, newest first.
func (s *Store) Blocks(ctx context.Context, startHeight uint32, limit int) ([]BlockRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+blockColumns+` FROM blocks WHERE height <= $1
		 ORDER BY height DESC LIMIT $2`, int32(startHeight), limit)
	if err != nil {
		return nil, fmt.Errorf("read blocks: %w", err)
	}
	defer rows.Close()

	var out []BlockRow
	for rows.Next() {
		block, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, block)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read block rows: %w", err)
	}
	return out, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanBlock(row scannable) (BlockRow, error) {
	var (
		out    BlockRow
		height int32
		hash   []byte
		prev   []byte
		merkle []byte
		main   []byte
		count  int32
	)
	err := row.Scan(&height, &hash, &prev, &merkle, &main, &out.BlockTime, &count)
	if err == pgx.ErrNoRows {
		return BlockRow{}, ErrNotFound
	}
	if err != nil {
		return BlockRow{}, fmt.Errorf("read block row: %w", err)
	}
	out.Height = uint32(height)
	out.TxCount = int(count)
	copy(out.Hash[:], hash)
	copy(out.MerkleRoot[:], merkle)
	copy(out.MainHash[:], main)
	if len(prev) == len(chain.Hash{}) {
		var h chain.Hash
		copy(h[:], prev)
		out.PrevHash = &h
	}
	return out, nil
}

// BlockTxids lists the transactions of one block, in body order.
func (s *Store) BlockTxids(ctx context.Context, hash chain.Hash) ([]chain.Hash, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT t.txid FROM txs t
		 JOIN blocks b ON b.height = t.height
		 WHERE b.hash = $1 ORDER BY t.tx_index`, hash[:])
	if err != nil {
		return nil, fmt.Errorf("read block txids: %w", err)
	}
	defer rows.Close()

	var out []chain.Hash
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("read txid row: %w", err)
		}
		var txid chain.Hash
		copy(txid[:], raw)
		out = append(out, txid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read txid rows: %w", err)
	}
	return out, nil
}

// Tx reads one transaction with the coins on both sides.
func (s *Store) Tx(ctx context.Context, txid chain.Hash) (TxRow, error) {
	var (
		out    TxRow
		height int32
		index  int32
		size   int32
	)
	err := s.pool.QueryRow(ctx,
		`SELECT t.height, t.tx_index, t.size_bytes, t.fee_sats, t.raw
		 FROM txs t WHERE t.txid = $1`, txid[:]).
		Scan(&height, &index, &size, &out.FeeSats, &out.Raw)
	if err == pgx.ErrNoRows {
		return TxRow{}, ErrNotFound
	}
	if err != nil {
		return TxRow{}, fmt.Errorf("read transaction %s: %w", txid, err)
	}
	out.Txid = txid
	out.Height = uint32(height)
	out.Index = int(index)
	out.SizeBytes = int(size)

	block, err := s.BlockAtHeight(ctx, out.Height)
	if err != nil {
		return TxRow{}, fmt.Errorf("read block for transaction %s: %w", txid, err)
	}
	out.Block = block

	// An input is an output this transaction spent, ordered by input position.
	out.Vin, err = s.coins(ctx,
		`WHERE spent_source = $1 AND spent_kind = 0 ORDER BY spent_vin`, txid[:])
	if err != nil {
		return TxRow{}, fmt.Errorf("read inputs of %s: %w", txid, err)
	}
	out.Vout, err = s.coins(ctx,
		`WHERE source_id = $1 AND kind = 0 ORDER BY vout`, txid[:])
	if err != nil {
		return TxRow{}, fmt.Errorf("read outputs of %s: %w", txid, err)
	}
	return out, nil
}

// Outspends lists what spent each output of one transaction, in vout order.
func (s *Store) Outspends(ctx context.Context, txid chain.Hash) ([]Coin, error) {
	return s.coins(ctx, `WHERE source_id = $1 AND kind = 0 ORDER BY vout`, txid[:])
}

const coinColumns = `kind, source_id, vout, address, value_sats, content, content_type,
	height, spent_source, spent_kind, spent_height`

func (s *Store) coins(ctx context.Context, where string, args ...any) ([]Coin, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+coinColumns+` FROM outputs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Coin
	for rows.Next() {
		var (
			c           Coin
			kind        int16
			source      []byte
			vout        int32
			address     []byte
			height      int32
			spentSource []byte
			spentKind   *int16
			spentHeight *int32
		)
		if err := rows.Scan(&kind, &source, &vout, &address, &c.ValueSats,
			&c.Content, &c.ContentType, &height,
			&spentSource, &spentKind, &spentHeight); err != nil {
			return nil, fmt.Errorf("read coin row: %w", err)
		}
		c.OutPoint.Kind = chain.OutPointKind(kind)
		c.OutPoint.Vout = uint32(vout)
		copy(c.OutPoint.Source[:], source)
		copy(c.Address[:], address)
		c.Height = uint32(height)
		if len(spentSource) == len(chain.Hash{}) {
			var h chain.Hash
			copy(h[:], spentSource)
			c.SpentSource = &h
		}
		if spentKind != nil {
			k := chain.InPointKind(*spentKind)
			c.SpentKind = &k
		}
		if spentHeight != nil {
			h := uint32(*spentHeight)
			c.SpentHeight = &h
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read coin rows: %w", err)
	}
	return out, nil
}
