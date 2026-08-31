package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A dependency that is down at start is not a failure. The service reports it,
// keeps trying, and connects when the dependency arrives.
func TestServiceConnectsAfterTheDependencyArrives(t *testing.T) {
	var up atomic.Bool
	svc := New("node", quiet(), func(context.Context) (int, error) {
		if !up.Load() {
			return 0, errors.New("connection refused")
		}
		return 42, nil
	}, nil)
	svc.RetryInterval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := svc.Get(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first Get error = %v, want ErrUnavailable", err)
	}
	if svc.IsConnected() {
		t.Fatal("service reports connected while the dependency is down")
	}

	go svc.Run(ctx)
	up.Store(true)

	if !waitFor(func() bool { return svc.IsConnected() }) {
		t.Fatal("service never connected after the dependency arrived")
	}
	client, err := svc.Get(ctx)
	if err != nil {
		t.Fatalf("Get after connect: %v", err)
	}
	if client != 42 {
		t.Errorf("client = %d, want 42", client)
	}
}

// Drop marks a connection down, so a caller that saw a request fail makes the
// next pass reopen it.
func TestDropForcesAReconnect(t *testing.T) {
	var opens atomic.Int64
	svc := New("node", quiet(), func(context.Context) (int, error) {
		return int(opens.Add(1)), nil
	}, nil)

	ctx := context.Background()
	if _, err := svc.Get(ctx); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if _, err := svc.Get(ctx); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if opens.Load() != 1 {
		t.Errorf("opened %d connections, want 1 while it stands", opens.Load())
	}

	svc.Drop()
	if svc.IsConnected() {
		t.Fatal("service reports connected after Drop")
	}
	if _, err := svc.Get(ctx); err != nil {
		t.Fatalf("Get after Drop: %v", err)
	}
	if opens.Load() != 2 {
		t.Errorf("opened %d connections, want 2 after Drop", opens.Load())
	}
}

// A replaced connection gets released, or the service leaks one per retry.
func TestReconnectClosesTheOldConnection(t *testing.T) {
	var closed atomic.Int64
	var opens atomic.Int64
	svc := New("store", quiet(),
		func(context.Context) (int, error) { return int(opens.Add(1)), nil },
		func(int) { closed.Add(1) })

	ctx := context.Background()
	if _, err := svc.Connect(ctx); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if closed.Load() != 0 {
		t.Errorf("closed %d connections before any replacement", closed.Load())
	}
	if _, err := svc.Connect(ctx); err != nil {
		t.Fatalf("second connect: %v", err)
	}
	if closed.Load() != 1 {
		t.Errorf("closed %d connections, want 1", closed.Load())
	}
}

// Two readers must each get every state change. One shared channel would let
// them race for a single value, and the loser would wait forever.
func TestConnectedChanReachesEverySubscriber(t *testing.T) {
	svc := New("node", quiet(), func(context.Context) (int, error) { return 1, nil }, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := svc.ConnectedChan(ctx)
	second := svc.ConnectedChan(ctx)

	if got := <-first; got {
		t.Errorf("first subscriber starts at %v, want false", got)
	}
	if got := <-second; got {
		t.Errorf("second subscriber starts at %v, want false", got)
	}

	if _, err := svc.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	for name, ch := range map[string]<-chan bool{"first": first, "second": second} {
		select {
		case got := <-ch:
			if !got {
				t.Errorf("%s subscriber read %v, want true", name, got)
			}
		case <-time.After(time.Second):
			t.Errorf("%s subscriber read nothing", name)
		}
	}
}

func TestConnectedChanClosesWithItsContext(t *testing.T) {
	svc := New("node", quiet(), func(context.Context) (int, error) { return 1, nil }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	ch := svc.ConnectedChan(ctx)
	<-ch
	cancel()

	select {
	case _, open := <-ch:
		if open {
			t.Error("channel carried a value after its context ended")
		}
	case <-time.After(time.Second):
		t.Error("channel stayed open after its context ended")
	}
}

func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}
