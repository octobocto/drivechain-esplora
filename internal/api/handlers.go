package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/service"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

func (s *Server) tipHeight(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	height, _, have, err := st.Tip(r.Context())
	if s.failed(w, r, err) {
		return
	}
	if !have {
		writeError(w, http.StatusNotFound, "the index holds no blocks yet")
		return
	}
	writeText(w, strconv.FormatUint(uint64(height), 10))
}

func (s *Server) tipHash(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	_, hash, have, err := st.Tip(r.Context())
	if s.failed(w, r, err) {
		return
	}
	if !have {
		writeError(w, http.StatusNotFound, "the index holds no blocks yet")
		return
	}
	writeText(w, hash.String())
}

func (s *Server) blocks(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	start, err := s.startHeight(r, st)
	if s.failed(w, r, err) {
		return
	}
	rows, err := st.Blocks(r.Context(), start, blockListSize)
	if s.failed(w, r, err) {
		return
	}
	out := make([]Block, 0, len(rows))
	for _, row := range rows {
		out = append(out, newBlock(row))
	}
	writeJSON(w, out)
}

func (s *Server) blockHeight(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	height, err := parseUint32(r.PathValue("height"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := st.BlockAtHeight(r.Context(), height)
	if s.notFound(w, r, err) {
		return
	}
	writeText(w, row.Hash.String())
}

func (s *Server) block(w http.ResponseWriter, r *http.Request) {
	st, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	_ = st
	writeJSON(w, newBlock(row))
}

func (s *Server) blockHeader(w http.ResponseWriter, r *http.Request) {
	_, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	// A sidechain header has no consensus byte encoding a client can parse, so
	// this route carries the fields instead of a hex blob.
	writeJSON(w, newBlock(row))
}

func (s *Server) blockStatus(w http.ResponseWriter, r *http.Request) {
	st, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	status := BlockStatus{InBestChain: true, Height: &row.Height}
	next, err := st.BlockAtHeight(r.Context(), row.Height+1)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case s.failed(w, r, err):
		return
	default:
		hash := next.Hash.String()
		status.NextBest = &hash
	}
	writeJSON(w, status)
}

func (s *Server) blockTxids(w http.ResponseWriter, r *http.Request) {
	st, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	txids, err := st.BlockTxids(r.Context(), row.Hash)
	if s.failed(w, r, err) {
		return
	}
	writeJSON(w, hashStrings(txids))
}

func (s *Server) blockTxid(w http.ResponseWriter, r *http.Request) {
	st, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		writeError(w, http.StatusBadRequest, "index must be a whole number")
		return
	}
	txids, err := st.BlockTxids(r.Context(), row.Hash)
	if s.failed(w, r, err) {
		return
	}
	if index >= len(txids) {
		writeError(w, http.StatusNotFound, "the block has no transaction at that index")
		return
	}
	writeText(w, txids[index].String())
}

func (s *Server) blockTxs(w http.ResponseWriter, r *http.Request) {
	st, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	start := 0
	if raw := r.PathValue("start_index"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "start index must be a whole number")
			return
		}
		start = parsed
	}
	txids, err := st.BlockTxids(r.Context(), row.Hash)
	if s.failed(w, r, err) {
		return
	}
	if start > len(txids) {
		start = len(txids)
	}
	end := min(start+PageSize, len(txids))

	out := make([]Tx, 0, end-start)
	for _, txid := range txids[start:end] {
		tx, err := st.Tx(r.Context(), txid)
		if s.failed(w, r, err) {
			return
		}
		out = append(out, newTx(tx))
	}
	writeJSON(w, out)
}

func (s *Server) tx(w http.ResponseWriter, r *http.Request) {
	st, txid, ok := s.txid(w, r)
	if !ok {
		return
	}
	row, err := st.Tx(r.Context(), txid)
	if s.notFound(w, r, err) {
		return
	}
	writeJSON(w, newTx(row))
}

func (s *Server) txStatus(w http.ResponseWriter, r *http.Request) {
	st, txid, ok := s.txid(w, r)
	if !ok {
		return
	}
	row, err := st.Tx(r.Context(), txid)
	if s.notFound(w, r, err) {
		return
	}
	writeJSON(w, newStatus(row.Height, row.Block.Hash, row.Block.BlockTime))
}

func (s *Server) txHex(w http.ResponseWriter, r *http.Request) {
	st, txid, ok := s.txid(w, r)
	if !ok {
		return
	}
	row, err := st.Tx(r.Context(), txid)
	if s.notFound(w, r, err) {
		return
	}
	// These bytes are the borsh encoding the txid hashes over, not a bitcoin
	// serialization.
	writeText(w, hex.EncodeToString(row.Raw))
}

func (s *Server) outspends(w http.ResponseWriter, r *http.Request) {
	st, txid, ok := s.txid(w, r)
	if !ok {
		return
	}
	coins, err := st.Outspends(r.Context(), txid)
	if s.failed(w, r, err) {
		return
	}
	out := make([]Outspend, 0, len(coins))
	for _, coin := range coins {
		spend, err := s.newOutspend(r, st, coin)
		if s.failed(w, r, err) {
			return
		}
		out = append(out, spend)
	}
	writeJSON(w, out)
}

func (s *Server) outspend(w http.ResponseWriter, r *http.Request) {
	st, txid, ok := s.txid(w, r)
	if !ok {
		return
	}
	vout, err := parseUint32(r.PathValue("vout"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	coins, err := st.Outspends(r.Context(), txid)
	if s.failed(w, r, err) {
		return
	}
	for _, coin := range coins {
		if coin.OutPoint.Vout != vout {
			continue
		}
		spend, err := s.newOutspend(r, st, coin)
		if s.failed(w, r, err) {
			return
		}
		writeJSON(w, spend)
		return
	}
	writeError(w, http.StatusNotFound, "the transaction has no output at that index")
}

// newOutspend reports what took one coin. A withdrawal bundle spends with no
// transaction, so Txid then names the bundle rather than a transaction.
func (s *Server) newOutspend(r *http.Request, st *store.Store, coin store.Coin) (Outspend, error) {
	if coin.SpentSource == nil {
		return Outspend{Spent: false}, nil
	}
	out := Outspend{Spent: true, SpentBy: "transaction"}
	if coin.SpentKind != nil && *coin.SpentKind == chain.SpendWithdrawal {
		out.SpentBy = "withdrawal_bundle"
		id := chain.BitcoinHash(*coin.SpentSource).String()
		out.Txid = &id
	} else {
		id := coin.SpentSource.String()
		out.Txid = &id
	}
	if coin.SpentHeight == nil {
		return out, nil
	}
	block, err := st.BlockAtHeight(r.Context(), *coin.SpentHeight)
	if errors.Is(err, store.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return Outspend{}, err
	}
	status := newStatus(block.Height, block.Hash, block.BlockTime)
	out.Status = &status
	return out, nil
}

func (s *Server) broadcast(w http.ResponseWriter, r *http.Request) {
	if s.broadcaster == nil {
		writeError(w, http.StatusServiceUnavailable, "no node connection for a broadcast")
		return
	}
	raw, err := readHexBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	txid, err := s.broadcaster.Broadcast(r.Context(), raw)
	if err != nil {
		s.log.Warn("broadcast failed", "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeText(w, txid.String())
}

func (s *Server) feeEstimates(w http.ResponseWriter, r *http.Request) {
	out := make(map[string]float64, 25)
	for target := 1; target <= 25; target++ {
		out[strconv.Itoa(target)] = FeeRateSatPerVByte
	}
	writeJSON(w, out)
}

func (s *Server) mempool(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"count":         0,
		"vsize":         0,
		"total_fee":     0,
		"fee_histogram": []any{},
	})
}

func (s *Server) emptyList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, []any{})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	connected := s.stores.IsConnected()
	status := http.StatusOK
	if !connected {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]bool{"database": connected})
}

// startHeight reads a path height, and falls back to the tip.
func (s *Server) startHeight(r *http.Request, st *store.Store) (uint32, error) {
	if raw := r.PathValue("start_height"); raw != "" {
		return parseUint32(raw)
	}
	height, _, have, err := st.Tip(r.Context())
	if err != nil {
		return 0, err
	}
	if !have {
		return 0, nil
	}
	return height, nil
}

func (s *Server) blockByHash(w http.ResponseWriter, r *http.Request) (*store.Store, store.BlockRow, bool) {
	st, ok := s.store(w, r)
	if !ok {
		return nil, store.BlockRow{}, false
	}
	hash, err := chain.ParseHash(r.PathValue("hash"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, store.BlockRow{}, false
	}
	row, err := st.BlockByHash(r.Context(), hash)
	if s.notFound(w, r, err) {
		return nil, store.BlockRow{}, false
	}
	return st, row, true
}

func (s *Server) txid(w http.ResponseWriter, r *http.Request) (*store.Store, chain.Hash, bool) {
	st, ok := s.store(w, r)
	if !ok {
		return nil, chain.Hash{}, false
	}
	txid, err := chain.ParseHash(r.PathValue("txid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, chain.Hash{}, false
	}
	return st, txid, true
}

// store returns the database connection, or answers 503. The listener runs
// whether or not the database is up.
func (s *Server) store(w http.ResponseWriter, r *http.Request) (*store.Store, bool) {
	st, err := s.stores.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the index database is not reachable")
		return nil, false
	}
	return st, true
}

// failed answers 500 and reports true when err is not nil.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, service.ErrUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "the index database is not reachable")
		return true
	}
	s.log.Error("request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "the request failed")
	return true
}

// notFound answers 404 for a missing row, and 500 for anything else.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return true
	}
	return s.failed(w, r, err)
}

func hashStrings(hashes []chain.Hash) []string {
	out := make([]string, 0, len(hashes))
	for _, h := range hashes {
		out = append(out, h.String())
	}
	return out
}

func parseUint32(raw string) (uint32, error) {
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", raw)
	}
	return uint32(v), nil
}
