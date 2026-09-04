package api

import (
	"context"
	"encoding/json"
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

func drivechainServer(mc Mainchain) http.Handler {
	return NewServer(nil, nil, mc, nil, slog.New(slog.DiscardHandler)).Handler()
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
	drivechainServer(stub()).ServeHTTP(rec,
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
	drivechainServer(stub()).ServeHTTP(rec,
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
	drivechainServer(stub()).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/sidechain/7", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("answered %d, want 404", rec.Code)
	}
}

// A deployment with no enforcer still serves its own chain, and says plainly
// that it reads no mainchain.
func TestSidechainsWithoutAnEnforcer(t *testing.T) {
	rec := httptest.NewRecorder()
	drivechainServer(nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/drivechain/sidechains", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("answered %d, want 503", rec.Code)
	}
}
