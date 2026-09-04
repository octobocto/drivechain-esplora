package api

import (
	"encoding/json"
	"net/http"

	"github.com/octobocto/drivechain-esplora/internal/store"
)

// Activity is one thing that happened on the chain: a transaction the body
// carried, or a deposit the mainchain sent.
type Activity struct {
	// Kind reads "transfer", "withdrawal" or "deposit". A deposit never
	// appears in a block body, so a feed of transactions alone misses it.
	Kind string `json:"kind"`
	// ID is the txid. A deposit carries its mainchain txid.
	ID     string `json:"id"`
	Value  int64  `json:"value"`
	Fee    int64  `json:"fee"`
	Size   int    `json:"size"`
	Status Status `json:"status"`
}

// WithdrawalState answers /drivechain/withdrawals.
type WithdrawalState struct {
	// Bundle is the node's own pending bundle JSON, unchanged. It is null when
	// the chain proposes none.
	Bundle json.RawMessage `json:"bundle"`
	// LastFailedHeight is the sidechain height of the last bundle the
	// mainchain rejected.
	LastFailedHeight *uint32 `json:"last_failed_height"`
}

func newActivity(row store.ActivityRow) Activity {
	return Activity{
		Kind:   row.Kind,
		ID:     row.ID.String(),
		Value:  row.ValueSats,
		Fee:    row.FeeSats,
		Size:   row.SizeBytes,
		Status: newStatus(row.Height, row.BlockHash, row.BlockTime),
	}
}

// recentActivity answers /txs/recent. The unconfirmed set comes first, because
// a reader wants what happened last.
func (s *Server) recentActivity(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	pending, err := st.MempoolRecent(r.Context(), activityListSize)
	if s.failed(w, r, err) {
		return
	}
	out := make([]Activity, 0, activityListSize)
	for _, row := range pending {
		out = append(out, Activity{
			Kind:   store.KindTransfer,
			ID:     row.Txid.String(),
			Value:  row.ValueSats,
			Fee:    row.FeeSats,
			Size:   row.SizeBytes,
			Status: pendingStatus,
		})
	}

	confirmed, err := st.Activity(r.Context(), activityListSize-len(out))
	if s.failed(w, r, err) {
		return
	}
	for _, row := range confirmed {
		out = append(out, newActivity(row))
	}
	writeJSON(w, out)
}

// blockActivity answers /block/{hash}/activity: what the block carried, with
// the deposits it applied.
func (s *Server) blockActivity(w http.ResponseWriter, r *http.Request) {
	st, row, ok := s.blockByHash(w, r)
	if !ok {
		return
	}
	rows, err := st.ActivityInBlock(r.Context(), row.Hash)
	if s.failed(w, r, err) {
		return
	}
	out := make([]Activity, 0, len(rows))
	for _, row := range rows {
		out = append(out, newActivity(row))
	}
	writeJSON(w, out)
}

// drivechainWithdrawals answers /drivechain/withdrawals. A light client has no
// node, so the index reads the bundle for it.
func (s *Server) drivechainWithdrawals(w http.ResponseWriter, r *http.Request) {
	if s.withdrawals == nil {
		writeError(w, http.StatusServiceUnavailable, "this index reads no node")
		return
	}
	bundle, err := s.withdrawals.Pending(r.Context())
	if err != nil {
		s.log.Warn("read the pending withdrawal bundle", "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	height, err := s.withdrawals.LastFailedHeight(r.Context())
	if err != nil {
		s.log.Warn("read the last failed bundle height", "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, WithdrawalState{Bundle: bundle, LastFailedHeight: height})
}
