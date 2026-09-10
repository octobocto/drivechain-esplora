package mainchain

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
)

// m8Script builds the scriptPubKey a BMM bid carries at output zero.
func m8Script(t *testing.T, slot uint8, criticalHash, prevMainHash string) []byte {
	t.Helper()
	critical, err := hex.DecodeString(criticalHash)
	if err != nil || len(critical) != blockHashLen {
		t.Fatalf("the critical hash is not 32 bytes: %v", err)
	}
	prevMain, err := hex.DecodeString(prevMainHash)
	if err != nil || len(prevMain) != blockHashLen {
		t.Fatalf("the prev hash is not 32 bytes: %v", err)
	}
	reverseBytes(prevMain)

	message := append([]byte{}, m8Tag...)
	message = append(message, slot)
	message = append(message, critical...)
	message = append(message, prevMain...)
	return append([]byte{opReturn, byte(len(message))}, message...)
}

// rawTx serializes a transaction that carries script at output zero.
func rawTx(t *testing.T, script []byte) string {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, script))
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		t.Fatalf("serialize the transaction: %v", err)
	}
	return hex.EncodeToString(buf.Bytes())
}

// fakeCore is a bitcoind that holds a fixed mempool. It counts the scans, so a
// test can tell a cache hit from a read.
type fakeCore struct {
	mempool map[string]string
	fees    map[string]float64
	// gone names a transaction the mempool lists and no longer holds.
	gone  []string
	scans atomic.Int64
}

func (f *fakeCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := body.Decode(&raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		var batch []rpcRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := make([]string, 0, len(batch))
		for _, req := range batch {
			txid, _ := req.Params[0].(string)
			hexTx, ok := f.mempool[txid]
			if !ok {
				out = append(out, fmt.Sprintf(
					`{"id":%d,"result":null,"error":{"code":-5,"message":"no such transaction"}}`, req.ID))
				continue
			}
			out = append(out, fmt.Sprintf(`{"id":%d,"result":%q,"error":null}`, req.ID, hexTx))
		}
		_, _ = fmt.Fprintf(w, "[%s]", strings.Join(out, ","))
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Method != "getrawmempool" {
		http.Error(w, "unexpected method "+req.Method, http.StatusBadRequest)
		return
	}
	f.scans.Add(1)
	entries := make(map[string]any, len(f.mempool))
	for txid := range f.mempool {
		entries[txid] = map[string]any{"fees": map[string]any{"base": f.fees[txid]}}
	}
	for _, txid := range f.gone {
		entries[txid] = map[string]any{"fees": map[string]any{"base": f.fees[txid]}}
	}
	answer, err := json.Marshal(map[string]any{"id": req.ID, "result": entries})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(answer)
}

const (
	criticalA = "c0af959a1f08e2e4e33701e4dcf9ce614585ec00ee7bd64744aafef3c8707490"
	criticalB = "c2578c88d43122018a46b9ab9907fe106d7d4a82e901d86ba6bc54e007a540d7"
	prevMainA = "00000000000000004f4638f447e96122d211d429f66a3a46c5d53316cb6c8d36"
)

func coreWithBids(t *testing.T) (*Core, *fakeCore) {
	t.Helper()
	fake := &fakeCore{
		mempool: map[string]string{
			"aa": rawTx(t, m8Script(t, 9, criticalA, prevMainA)),
			"bb": rawTx(t, m8Script(t, 2, criticalB, prevMainA)),
			"cc": rawTx(t, []byte{0x76, 0xa9, 0x14}),
		},
		fees: map[string]float64{"aa": 0.00003, "bb": 0.00001, "cc": 0.001},
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	core, err := NewCore(server.URL, "")
	if err != nil {
		t.Fatalf("build the client: %v", err)
	}
	return core, fake
}

// The scan reads every M8 out of the mempool, and leaves every other
// transaction alone.
func TestBidsReadTheMempool(t *testing.T) {
	core, _ := coreWithBids(t)

	bids, err := core.Bids(context.Background())
	if err != nil {
		t.Fatalf("read the bids: %v", err)
	}
	if len(bids) != 2 {
		t.Fatalf("read %d bids, want 2: %+v", len(bids), bids)
	}
	// The richest bid comes first.
	if bids[0].Txid != "aa" || bids[0].Slot != 9 || bids[0].BidSats != 3000 {
		t.Errorf("the first bid is %+v", bids[0])
	}
	if bids[0].CriticalHash != criticalA || bids[0].PrevMainHash != prevMainA {
		t.Errorf("the first bid carries %+v", bids[0])
	}
	if bids[1].Txid != "bb" || bids[1].Slot != 2 || bids[1].BidSats != 1000 {
		t.Errorf("the second bid is %+v", bids[1])
	}
}

// One scan serves every caller inside the time to live, and a later caller
// gets a new scan.
func TestBidsServeFromTheCache(t *testing.T) {
	core, fake := coreWithBids(t)
	start := time.Now()
	core.now = func() time.Time { return start }

	for range 3 {
		if _, err := core.Bids(context.Background()); err != nil {
			t.Fatalf("read the bids: %v", err)
		}
	}
	if got := fake.scans.Load(); got != 1 {
		t.Fatalf("bitcoind answered %d scans, want 1", got)
	}

	core.now = func() time.Time { return start.Add(bidCacheTTL) }
	if _, err := core.Bids(context.Background()); err != nil {
		t.Fatalf("read the bids: %v", err)
	}
	if got := fake.scans.Load(); got != 2 {
		t.Fatalf("bitcoind answered %d scans after the time to live, want 2", got)
	}
}

// A transaction that leaves the mempool during the scan drops out of the
// answer, because the mempool moves while the scan runs.
func TestBidsSkipATransactionThatLeft(t *testing.T) {
	core, fake := coreWithBids(t)
	fake.gone = []string{"dd"}
	fake.fees["dd"] = 0.01

	bids, err := core.Bids(context.Background())
	if err != nil {
		t.Fatalf("read the bids: %v", err)
	}
	if len(bids) != 2 {
		t.Fatalf("read %d bids, want 2", len(bids))
	}
}
