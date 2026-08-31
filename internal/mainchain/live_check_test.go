//go:build livecheck

package mainchain

import (
	"context"
	"os"
	"testing"
)

// Reads a real enforcer, to make sure the JSON shapes match what it sends.
//
//	ENFORCER_URL=http://127.0.0.1:15051 go test -tags livecheck ./internal/mainchain/ -v
func TestLiveEnforcer(t *testing.T) {
	url := os.Getenv("ENFORCER_URL")
	if url == "" {
		t.Skip("set ENFORCER_URL to read a live enforcer")
	}
	e := New(url)
	ctx := context.Background()

	chains, err := e.Sidechains(ctx)
	if err != nil {
		t.Fatalf("sidechains: %v", err)
	}
	if len(chains) == 0 {
		t.Fatal("the enforcer knows no sidechains")
	}
	for _, c := range chains {
		ctip, ok, err := e.Ctip(ctx, c.Slot)
		if err != nil {
			t.Fatalf("ctip for slot %d: %v", c.Slot, err)
		}
		t.Logf("slot %3d %-12s votes=%d act=%d ctip=%v vout=%d %d sats",
			c.Slot, c.Title, c.VoteCount, c.ActivationHeight, ok, ctip.Vout, ctip.ValueSats)
		if c.Title == "" {
			t.Errorf("slot %d has no title", c.Slot)
		}
	}
}
