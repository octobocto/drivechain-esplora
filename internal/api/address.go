package api

import (
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// depositListSize is how many deposit rows one page carries.
const depositListSize = 50

// addressKey reads the path key. An address route takes base58; a scripthash
// route takes 32 hex bytes.
func (s *Server) addressKey(w http.ResponseWriter, r *http.Request) (*store.Store, string, []byte, bool) {
	st, ok := s.store(w, r)
	if !ok {
		return nil, "", nil, false
	}
	raw := r.PathValue("key")

	if isScriptHashRoute(r) {
		key, err := hex.DecodeString(raw)
		if err != nil || len(key) != 32 {
			writeError(w, http.StatusBadRequest, "a scripthash is 32 hex bytes")
			return nil, "", nil, false
		}
		return st, store.ColumnScriptHash, key, true
	}

	address, err := chain.ParseAddress(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, "", nil, false
	}
	return st, store.ColumnAddress, address[:], true
}

func isScriptHashRoute(r *http.Request) bool {
	const prefix = "/scripthash/"
	return len(r.URL.Path) >= len(prefix) && r.URL.Path[:len(prefix)] == prefix
}

func (s *Server) addressInfo(w http.ResponseWriter, r *http.Request) {
	st, column, key, ok := s.addressKey(w, r)
	if !ok {
		return
	}
	stats, err := st.Stats(r.Context(), column, key)
	if s.failed(w, r, err) {
		return
	}
	pending, err := st.MempoolStats(r.Context(), column, key)
	if s.failed(w, r, err) {
		return
	}
	out := AddressInfo{
		ChainStats:   newTxoStats(stats),
		MempoolStats: newTxoStats(pending),
	}
	if column == store.ColumnScriptHash {
		out.ScriptHash = r.PathValue("key")
	} else {
		out.Address = r.PathValue("key")
	}
	writeJSON(w, out)
}

func (s *Server) addressUTXOs(w http.ResponseWriter, r *http.Request) {
	st, column, key, ok := s.addressKey(w, r)
	if !ok {
		return
	}
	rows, err := st.UTXOs(r.Context(), column, key)
	if s.failed(w, r, err) {
		return
	}
	spent, err := st.MempoolSpentOutpoints(r.Context())
	if s.failed(w, r, err) {
		return
	}
	pending, err := st.MempoolUTXOs(r.Context(), column, key)
	if s.failed(w, r, err) {
		return
	}

	// A coin the mempool spends leaves the balance at once, rather than at the
	// next block.
	out := make([]UTXO, 0, len(rows)+len(pending))
	for _, row := range rows {
		key := row.OutPoint.Key()
		if spent[string(key[:])] {
			continue
		}
		out = append(out, newUTXO(row))
	}
	for _, row := range pending {
		out = append(out, newUTXO(row))
	}
	writeJSON(w, out)
}

// addressMempoolTxs lists the unconfirmed transactions that touch one address.
func (s *Server) addressMempoolTxs(w http.ResponseWriter, r *http.Request) {
	st, column, key, ok := s.addressKey(w, r)
	if !ok {
		return
	}
	txids, err := st.MempoolTxidsFor(r.Context(), column, key)
	if s.failed(w, r, err) {
		return
	}
	out, err := s.mempoolTxs(r, st, txids)
	if s.failed(w, r, err) {
		return
	}
	writeJSON(w, out)
}

// mempoolTxs reads a list of unconfirmed transactions. A transaction a block
// took while this request ran drops out rather than failing the request.
func (s *Server) mempoolTxs(
	r *http.Request, st *store.Store, txids []chain.Hash,
) ([]Tx, error) {
	out := make([]Tx, 0, len(txids))
	for _, txid := range txids {
		row, err := st.MempoolTxRow(r.Context(), txid)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, newTx(row))
	}
	return out, nil
}

// addressTxs pages an address history, newest first. A client reads the last
// txid of a page and passes it back to get the next one.
func (s *Server) addressTxs(w http.ResponseWriter, r *http.Request) {
	st, column, key, ok := s.addressKey(w, r)
	if !ok {
		return
	}

	before, err := s.startHeight(r, st)
	if s.failed(w, r, err) {
		return
	}
	lastSeen := r.PathValue("last_seen")
	if lastSeen != "" {
		txid, err := chain.ParseHash(lastSeen)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		row, err := st.Tx(r.Context(), txid)
		if s.notFound(w, r, err) {
			return
		}
		// The cursor names the oldest row of the previous page, so the next
		// page starts one block below it.
		if row.Height == 0 {
			writeJSON(w, []Tx{})
			return
		}
		before = row.Height - 1
	}

	refs, err := st.History(r.Context(), column, key, before, PageSize)
	if s.failed(w, r, err) {
		return
	}

	// The first page carries the unconfirmed rows, newest first, the way an
	// Esplora client reads them. A later page carries mined rows alone.
	out := make([]Tx, 0, len(refs))
	if lastSeen == "" {
		txids, err := st.MempoolTxidsFor(r.Context(), column, key)
		if s.failed(w, r, err) {
			return
		}
		out, err = s.mempoolTxs(r, st, txids)
		if s.failed(w, r, err) {
			return
		}
	}
	for _, ref := range refs {
		row, err := st.Tx(r.Context(), ref.Txid)
		if errors.Is(err, store.ErrNotFound) {
			// A bundle spend names an m6id, which is not a transaction.
			continue
		}
		if s.failed(w, r, err) {
			return
		}
		out = append(out, newTx(row))
	}
	writeJSON(w, out)
}

func (s *Server) addressDeposits(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	address, err := chain.ParseAddress(r.PathValue("key"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := st.AddressDeposits(r.Context(), address, depositListSize)
	if s.failed(w, r, err) {
		return
	}
	writeJSON(w, toUTXOs(rows))
}

// deposit lists the sidechain outputs one mainchain transaction created.
func (s *Server) deposit(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	var mainTxid chain.BitcoinHash
	if err := mainTxid.UnmarshalJSON([]byte(`"` + r.PathValue("txid") + `"`)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := st.Deposits(r.Context(), mainTxid[:], ^uint32(0), depositListSize)
	if s.failed(w, r, err) {
		return
	}
	if len(rows) == 0 {
		writeError(w, http.StatusNotFound, "no deposit from that transaction")
		return
	}
	writeJSON(w, toUTXOs(rows))
}

func (s *Server) deposits(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	start, err := s.startHeight(r, st)
	if s.failed(w, r, err) {
		return
	}
	rows, err := st.Deposits(r.Context(), nil, start, depositListSize)
	if s.failed(w, r, err) {
		return
	}
	writeJSON(w, toUTXOs(rows))
}

func newTxoStats(s store.TxoStats) TxoStats {
	return TxoStats{
		FundedTxoCount: s.FundedCount,
		FundedTxoSum:   s.FundedSum,
		SpentTxoCount:  s.SpentCount,
		SpentTxoSum:    s.SpentSum,
		TxCount:        s.TxCount,
	}
}

func toUTXOs(rows []store.UTXO) []UTXO {
	out := make([]UTXO, 0, len(rows))
	for _, row := range rows {
		out = append(out, newUTXO(row))
	}
	return out
}
