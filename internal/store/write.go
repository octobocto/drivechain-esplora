package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// Output is one coin the index records.
type Output struct {
	OutPoint    chain.OutPoint
	Address     chain.Address
	ValueSats   int64
	Content     json.RawMessage
	ContentType string
	// HeightExact is false for a deposit the node could not attribute to a
	// block. Such a row carries the height where the index first saw it.
	HeightExact bool
}

// Spend records how one output left the UTXO set.
type Spend struct {
	OutPoint chain.OutPoint
	// Source is the spending txid, or the bundle m6id for a peg-out.
	Source chain.Hash
	Kind   chain.InPointKind
	// Vin is the input position. A bundle spend has no transaction, so it
	// carries the position within the bundle instead.
	Vin uint32
}

// Tx is one transaction row.
type Tx struct {
	Txid      chain.Hash
	Index     int
	SizeBytes int
	FeeSats   int64
	Raw       []byte
}

// Block is everything one block writes. The index package builds it; this
// package only stores it.
type Block struct {
	Height     uint32
	Hash       chain.Hash
	PrevHash   *chain.Hash
	MerkleRoot chain.Hash
	MainHash   chain.BitcoinHash
	// MainHeight is the height of that mainchain block. An index that reads no
	// enforcer leaves it empty.
	MainHeight *uint32
	BlockTime  *int64
	Txs        []Tx
	Creates    []Output
	Spends     []Spend
}

// ApplyResult reports what one block wrote.
type ApplyResult struct {
	// UnknownSpends counts spends whose output the index does not hold. A node
	// without the deposit index leaves some deposits unseen, and a spend of one
	// lands here rather than corrupting a balance.
	UnknownSpends int
}

// Apply writes one block in a single transaction. A crash never leaves a half
// written block.
func (s *Store) Apply(ctx context.Context, block Block) (ApplyResult, error) {
	var result ApplyResult
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := insertBlock(ctx, tx, block); err != nil {
			return err
		}
		if err := insertTxs(ctx, tx, block); err != nil {
			return err
		}
		if err := insertOutputs(ctx, tx, block); err != nil {
			return err
		}
		applied, err := applySpends(ctx, tx, block)
		if err != nil {
			return err
		}
		result.UnknownSpends = len(block.Spends) - applied
		if err := setFees(ctx, tx, block.Height); err != nil {
			return err
		}
		if err := clearMempool(ctx, tx, block); err != nil {
			return err
		}
		return setTip(ctx, tx, block.Height, block.Hash)
	})
	if err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

func insertBlock(ctx context.Context, tx pgx.Tx, b Block) error {
	var prev []byte
	if b.PrevHash != nil {
		prev = b.PrevHash[:]
	}
	var mainHeight *int32
	if b.MainHeight != nil {
		h := int32(*b.MainHeight)
		mainHeight = &h
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO blocks (height, hash, prev_hash, merkle_root, main_hash, main_height, block_time, tx_count)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		int32(b.Height), b.Hash[:], prev, b.MerkleRoot[:], b.MainHash[:],
		mainHeight, b.BlockTime, int32(len(b.Txs)))
	if err != nil {
		return fmt.Errorf("insert block %d (%s): %w", b.Height, b.Hash, err)
	}
	return nil
}

func insertTxs(ctx context.Context, tx pgx.Tx, b Block) error {
	if len(b.Txs) == 0 {
		return nil
	}
	rows := make([][]any, len(b.Txs))
	for i, t := range b.Txs {
		rows[i] = []any{t.Txid[:], int32(b.Height), int32(t.Index), int32(t.SizeBytes), t.FeeSats, t.Raw}
	}
	_, err := tx.CopyFrom(ctx,
		pgx.Identifier{"txs"},
		[]string{"txid", "height", "tx_index", "size_bytes", "fee_sats", "raw"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("insert transactions for block %d: %w", b.Height, err)
	}
	return nil
}

func insertOutputs(ctx context.Context, tx pgx.Tx, b Block) error {
	if len(b.Creates) == 0 {
		return nil
	}
	rows := make([][]any, len(b.Creates))
	for i, o := range b.Creates {
		key := o.OutPoint.Key()
		scripthash := o.Address.ScriptHash()
		rows[i] = []any{
			key[:], int16(o.OutPoint.Kind), o.OutPoint.Source[:], int32(o.OutPoint.Vout),
			b.Hash[:], o.Address[:], scripthash[:], o.ValueSats,
			[]byte(o.Content), o.ContentType, int32(b.Height), o.HeightExact,
		}
	}
	_, err := tx.CopyFrom(ctx,
		pgx.Identifier{"outputs"},
		[]string{
			"outpoint", "kind", "source_id", "vout",
			"block_hash", "address", "scripthash", "value_sats",
			"content", "content_type", "height", "height_exact",
		},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("insert outputs for block %d: %w", b.Height, err)
	}
	return nil
}

// applySpends marks every spent output in one statement, and returns how many
// rows it actually matched.
func applySpends(ctx context.Context, tx pgx.Tx, b Block) (int, error) {
	if len(b.Spends) == 0 {
		return 0, nil
	}
	keys := make([][]byte, len(b.Spends))
	sources := make([][]byte, len(b.Spends))
	kinds := make([]int16, len(b.Spends))
	vins := make([]int32, len(b.Spends))
	for i, sp := range b.Spends {
		key := sp.OutPoint.Key()
		keys[i] = key[:]
		source := sp.Source
		sources[i] = source[:]
		kinds[i] = int16(sp.Kind)
		vins[i] = int32(sp.Vin)
	}

	tag, err := tx.Exec(ctx,
		`UPDATE outputs AS o
		 SET spent_source = s.source, spent_kind = s.kind, spent_vin = s.vin,
		     spent_height = $5
		 FROM unnest($1::bytea[], $2::bytea[], $3::smallint[], $4::integer[])
		      AS s(outpoint, source, kind, vin)
		 WHERE o.outpoint = s.outpoint`,
		keys, sources, kinds, vins, int32(b.Height))
	if err != nil {
		return 0, fmt.Errorf("mark spends for block %d: %w", b.Height, err)
	}
	return int(tag.RowsAffected()), nil
}

// setFees computes each fee from the rows this block just wrote. Both sides of
// the subtraction are indexed lookups.
//
// A fee floors at zero, because a client reads it as an amount. It can only go
// below zero when a prevout is missing, and Apply already reports that count.
func setFees(ctx context.Context, tx pgx.Tx, height uint32) error {
	_, err := tx.Exec(ctx,
		`UPDATE txs t SET fee_sats = GREATEST(
			COALESCE((SELECT SUM(value_sats) FROM outputs
			          WHERE spent_source = t.txid AND spent_kind = 0
			            AND spent_height = $1), 0)
			- COALESCE((SELECT SUM(value_sats) FROM outputs
			            WHERE source_id = t.txid AND kind = 0 AND height = $1), 0),
			0)
		 WHERE t.height = $1`, int32(height))
	if err != nil {
		return fmt.Errorf("compute fees for block %d: %w", height, err)
	}
	return nil
}

// clearMempool drops the snapshot rows this block confirms. A coin that sits in
// both tables counts twice, so the block that carries it takes it out of the
// unconfirmed set at the same time.
func clearMempool(ctx context.Context, tx pgx.Tx, b Block) error {
	txids := make([][]byte, 0, len(b.Txs))
	for _, t := range b.Txs {
		txid := t.Txid
		txids = append(txids, txid[:])
	}
	creates := make([][]byte, 0, len(b.Creates))
	for _, o := range b.Creates {
		key := o.OutPoint.Key()
		creates = append(creates, key[:])
	}
	spends := make([][]byte, 0, len(b.Spends))
	for _, sp := range b.Spends {
		key := sp.OutPoint.Key()
		spends = append(spends, key[:])
	}

	for _, clear := range []struct {
		query string
		keys  [][]byte
	}{
		{`DELETE FROM mempool_txs WHERE txid = ANY($1::bytea[])`, txids},
		{`DELETE FROM mempool_outputs WHERE outpoint = ANY($1::bytea[])`, creates},
		{`DELETE FROM mempool_spends WHERE outpoint = ANY($1::bytea[])`, spends},
	} {
		if len(clear.keys) == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, clear.query, clear.keys); err != nil {
			return fmt.Errorf("clear the mempool for block %d: %w", b.Height, err)
		}
	}
	return nil
}

func setTip(ctx context.Context, tx pgx.Tx, height uint32, hash chain.Hash) error {
	_, err := tx.Exec(ctx,
		`UPDATE sync_state SET tip_height = $1, tip_hash = $2 WHERE id = 1`,
		int32(height), hash[:])
	if err != nil {
		return fmt.Errorf("record tip %d: %w", height, err)
	}
	return nil
}

// Rollback undoes every block above height, in one transaction. A wrong
// rollback shows a user coins that do not exist, so the three statements always
// run together.
func (s *Store) Rollback(ctx context.Context, height uint32) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE outputs SET spent_source = NULL, spent_kind = NULL,
			        spent_vin = NULL, spent_height = NULL
			 WHERE spent_height > $1`, int32(height)); err != nil {
			return fmt.Errorf("unspend outputs above height %d: %w", height, err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM outputs WHERE height > $1`, int32(height)); err != nil {
			return fmt.Errorf("delete outputs above height %d: %w", height, err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM blocks WHERE height > $1`, int32(height)); err != nil {
			return fmt.Errorf("delete blocks above height %d: %w", height, err)
		}

		var hash []byte
		err := tx.QueryRow(ctx,
			`SELECT hash FROM blocks WHERE height = $1`, int32(height)).Scan(&hash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err = tx.Exec(ctx,
				`UPDATE sync_state SET tip_height = NULL, tip_hash = NULL WHERE id = 1`)
		case err != nil:
			return fmt.Errorf("read tip after rollback to %d: %w", height, err)
		default:
			_, err = tx.Exec(ctx,
				`UPDATE sync_state SET tip_height = $1, tip_hash = $2 WHERE id = 1`,
				int32(height), hash)
		}
		if err != nil {
			return fmt.Errorf("record tip after rollback to %d: %w", height, err)
		}
		return nil
	})
}

// Tip returns the height and hash of the highest indexed block. The bool is
// false when the index holds no blocks.
func (s *Store) Tip(ctx context.Context) (uint32, chain.Hash, bool, error) {
	var height *int32
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT tip_height, tip_hash FROM sync_state WHERE id = 1`).Scan(&height, &raw)
	if err != nil {
		return 0, chain.Hash{}, false, fmt.Errorf("read tip: %w", err)
	}
	if height == nil || len(raw) != len(chain.Hash{}) {
		return 0, chain.Hash{}, false, nil
	}
	var hash chain.Hash
	copy(hash[:], raw)
	return uint32(*height), hash, true, nil
}

// HashAt returns the indexed block hash at one height, or false when the index
// does not reach that far.
func (s *Store) HashAt(ctx context.Context, height uint32) (chain.Hash, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT hash FROM blocks WHERE height = $1`, int32(height)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return chain.Hash{}, false, nil
	}
	if err != nil {
		return chain.Hash{}, false, fmt.Errorf("read block hash at height %d: %w", height, err)
	}
	var hash chain.Hash
	copy(hash[:], raw)
	return hash, true, nil
}

// Init records which chain and network this database holds. It refuses a
// database that already holds a different chain, because two chains in one
// database would mix balances.
func (s *Store) Init(ctx context.Context, chainName string, network chain.Network) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var haveChain, haveNetwork string
		err := tx.QueryRow(ctx,
			`SELECT chain, network FROM sync_state WHERE id = 1`).Scan(&haveChain, &haveNetwork)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx,
				`INSERT INTO sync_state (id, chain, network) VALUES (1, $1, $2)`,
				chainName, string(network))
			if err != nil {
				return fmt.Errorf("record chain %s on %s: %w", chainName, network, err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("read chain state: %w", err)
		}
		if haveChain != chainName || haveNetwork != string(network) {
			return fmt.Errorf(
				"database already holds %s on %s, refusing to index %s on %s",
				haveChain, haveNetwork, chainName, network)
		}
		return nil
	})
}
