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
	// MainHeight is the height of that mainchain block. It is empty on an
	// index that reads no enforcer.
	MainHeight *uint32
	BlockTime  *int64
	TxCount    int
	// FeeSats is what every transaction in the block paid together.
	FeeSats int64
	// ValueSats is what the block's transactions paid out together.
	ValueSats int64
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
	// Unconfirmed marks a row the mempool holds. No block carries it, so its
	// height and its block are empty.
	Unconfirmed bool
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

const blockColumns = `b.height, b.hash, b.prev_hash, b.merkle_root, b.main_hash,
	b.main_height, b.block_time, b.tx_count,
	COALESCE((SELECT SUM(t.fee_sats) FROM txs t WHERE t.height = b.height), 0),
	COALESCE((SELECT SUM(o.value_sats) FROM outputs o
	          JOIN txs t ON t.txid = o.source_id
	          WHERE t.height = b.height AND o.kind = 0), 0)`

// BlockByHash reads one block header.
func (s *Store) BlockByHash(ctx context.Context, hash chain.Hash) (BlockRow, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+blockColumns+` FROM blocks b WHERE b.hash = $1`, hash[:])
	return scanBlock(row)
}

// BlockAtHeight reads the block header at one height.
func (s *Store) BlockAtHeight(ctx context.Context, height uint32) (BlockRow, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+blockColumns+` FROM blocks b WHERE b.height = $1`, int32(height))
	return scanBlock(row)
}

// Blocks lists up to limit block headers at or below startHeight, newest first.
func (s *Store) Blocks(ctx context.Context, startHeight uint32, limit int) ([]BlockRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+blockColumns+` FROM blocks b WHERE b.height <= $1
		 ORDER BY b.height DESC LIMIT $2`, int32(startHeight), limit)
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
		out        BlockRow
		height     int32
		hash       []byte
		prev       []byte
		merkle     []byte
		main       []byte
		mainHeight *int32
		count      int32
	)
	err := row.Scan(&height, &hash, &prev, &merkle, &main, &mainHeight,
		&out.BlockTime, &count, &out.FeeSats, &out.ValueSats)
	if errors.Is(err, pgx.ErrNoRows) {
		return BlockRow{}, ErrNotFound
	}
	if err != nil {
		return BlockRow{}, fmt.Errorf("read block row: %w", err)
	}
	out.Height = uint32(height)
	out.TxCount = int(count)
	if mainHeight != nil {
		h := uint32(*mainHeight)
		out.MainHeight = &h
	}
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
	if errors.Is(err, pgx.ErrNoRows) {
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

// BlocksMissingMainHeight lists blocks whose mainchain height is unresolved,
// newest first. A block indexed before the column existed reads this way.
func (s *Store) BlocksMissingMainHeight(ctx context.Context, limit int) ([]BlockRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+blockColumns+` FROM blocks b WHERE b.main_height IS NULL
		 ORDER BY b.height DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read blocks with no mainchain height: %w", err)
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

// SetMainHeight records the height of the mainchain block a header names.
func (s *Store) SetMainHeight(ctx context.Context, height, mainHeight uint32) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE blocks SET main_height = $2 WHERE height = $1`,
		int32(height), int32(mainHeight))
	if err != nil {
		return fmt.Errorf("set the mainchain height of block %d: %w", height, err)
	}
	return nil
}
