package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/mainchain"
)

// stubMainchain stands in for an enforcer.
type stubMainchain struct {
	chains []mainchain.Sidechain
	ctips  map[uint32]mainchain.Ctip
	err    error
}

func (s *stubMainchain) Sidechains(context.Context) ([]mainchain.Sidechain, error) {
	return s.chains, s.err
}

func (s *stubMainchain) Ctip(_ context.Context, slot uint32) (mainchain.Ctip, bool, error) {
	ctip, ok := s.ctips[slot]
	return ctip, ok, nil
}

func drivechainServer(mc Mainchain, bids BidSource) http.Handler {
	return NewServer(nil, nil, mc, bids, slog.New(slog.DiscardHandler)).Handler()
}

func stub() *stubMainchain {
	return &stubMainchain{
		chains: []mainchain.Sidechain{
			{Slot: 9, Title: "Thunder", Description: "big blocks", VoteCount: 73, ActivationHeight: 987402},
			{Slot: 2, Title: "BitNames", Description: "names"},
		},
		ctips: map[uint32]mainchain.Ctip{
			9: {Txid: "be013dc3", Vout: 0, ValueSats: 10403007000},
		},
	}
}

// A wallet with no node reads which sidechains exist, and what each treasury
// holds, from here.
func TestSidechainsListsTheEscrow(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(stub(), nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/sidechains", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}
	var got []SidechainInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d sidechains, want 2", len(got))
	}
	if got[0].Slot != 9 || got[0].Title != "Thunder" {
		t.Errorf("first chain = %+v", got[0])
	}
	if got[0].Treasury == nil || got[0].Treasury.ValueSats != 10403007000 {
		t.Errorf("thunder treasury = %+v", got[0].Treasury)
	}
	// A slot the enforcer holds no treasury for reads as none, never as zero
	// sats, so a caller can tell the two apart.
	if got[1].Treasury != nil {
		t.Errorf("bitnames reports a treasury it does not have: %+v", got[1].Treasury)
	}
}

// The per-chain route answers one slot.
func TestSidechainAnswersOneSlot(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(stub(), nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/sidechain/9", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}
	var got SidechainInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	if got.Slot != 9 || got.Title != "Thunder" {
		t.Errorf("got %+v", got)
	}
}

// An empty slot must answer plainly, not with an empty object.
func TestSidechainRefusesAnEmptySlot(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(stub(), nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/sidechain/7", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("answered %d, want 404", rec.Code)
	}
}

// A deployment with no enforcer still serves its own chain, and says plainly
// that it reads no mainchain.
func TestSidechainsWithoutAnEnforcer(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(nil, nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/sidechains", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("answered %d, want 503", rec.Code)
	}
}

// stubBids stands in for a mainchain mempool.
type stubBids struct {
	bids []mainchain.Bid
	err  error
}

func (s *stubBids) Bids(context.Context) ([]mainchain.Bid, error) { return s.bids, s.err }

func bidStub() *stubBids {
	return &stubBids{bids: []mainchain.Bid{
		{Slot: 9, Txid: "aa", CriticalHash: "c0af", PrevMainHash: "0000", BidSats: 3000},
		{Slot: 2, Txid: "bb", CriticalHash: "f3b6", PrevMainHash: "0000", BidSats: 2000},
		{Slot: 9, Txid: "cc", CriticalHash: "6f8c", PrevMainHash: "0000", BidSats: 1000},
	}}
}

func readBids(t *testing.T, target string) []mainchain.Bid {
	t.Helper()
	rec := httptest.NewRecorder()
	drivechainServer(nil, bidStub()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}
	var got []mainchain.Bid
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	return got
}

// A light wallet reads its competitors here, and it asks for its own slot.
func TestBidsListTheMempool(t *testing.T) {
	got := readBids(t, "/drivechain/bids")
	if len(got) != 3 {
		t.Fatalf("read %d bids, want 3", len(got))
	}
	if got[0].Txid != "aa" || got[0].BidSats != 3000 {
		t.Errorf("the first bid is %+v", got[0])
	}
}

func TestBidsAnswerOneSlot(t *testing.T) {
	got := readBids(t, "/drivechain/bids?slot=9")
	if len(got) != 2 {
		t.Fatalf("read %d bids, want 2: %+v", len(got), got)
	}
	for _, bid := range got {
		if bid.Slot != 9 {
			t.Errorf("slot 9 answered with %+v", bid)
		}
	}
}

func TestBidsAnswerAnEmptySlot(t *testing.T) {
	got := readBids(t, "/drivechain/bids?slot=7")
	if len(got) != 0 {
		t.Fatalf("slot 7 answered %+v", got)
	}
}

func TestBidsRefuseASlotThatIsNotANumber(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(nil, bidStub()).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/bids?slot=thunder", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("answered %d, want 400", rec.Code)
	}
}

// An index with no bitcoind behind it says so, rather than answering an empty
// list a wallet would read as "no competitors".
func TestBidsWithNoSource(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(nil, nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/bids", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("answered %d, want 503", rec.Code)
	}
}

func TestBidsReportAFailedRead(t *testing.T) {
	rec := httptest.NewRecorder()
	failed := &stubBids{err: errors.New("bitcoind is down")}
	NewServer(nil, nil, nil, failed, slog.New(slog.DiscardHandler)).Handler().
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/drivechain/bids", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("answered %d, want 502", rec.Code)
	}
}
