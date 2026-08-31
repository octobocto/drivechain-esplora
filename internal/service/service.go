// Package service wraps a connection to something outside this process. A
// service that is not up at start is not a failure. The wrapper keeps trying,
// and it tells a caller whether the connection stands right now.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrUnavailable says the service does not accept connections right now.
var ErrUnavailable = errors.New("service is unavailable")

// Connector opens one connection.
type Connector[T any] func(ctx context.Context) (T, error)

// Closer releases a connection the wrapper is about to replace.
type Closer[T any] func(T)

// Service holds a connection and reopens it after a failure.
type Service[T any] struct {
	name      string
	connector Connector[T]
	closer    Closer[T]
	log       *slog.Logger

	mu     sync.RWMutex
	client T
	have   bool

	connected atomic.Bool

	// Each subscriber owns one channel. A shared channel lets two consumers
	// race for one buffered value, and the loser then waits forever.
	subsMu sync.Mutex
	subs   map[chan bool]struct{}

	// RetryInterval is how long the reconnect loop waits between tries.
	RetryInterval time.Duration
}

// New wraps a connector. Pass a nil closer when a connection needs no release.
func New[T any](name string, log *slog.Logger, connector Connector[T], closer Closer[T]) *Service[T] {
	return &Service[T]{
		name:          name,
		connector:     connector,
		closer:        closer,
		log:           log,
		subs:          make(map[chan bool]struct{}),
		RetryInterval: time.Second,
	}
}

// Get returns the connection, and opens one when none stands.
func (s *Service[T]) Get(ctx context.Context) (T, error) {
	if s.connected.Load() {
		s.mu.RLock()
		client := s.client
		s.mu.RUnlock()
		return client, nil
	}
	return s.Connect(ctx)
}

// Connect opens one connection and records the result.
func (s *Service[T]) Connect(ctx context.Context) (T, error) {
	var zero T
	if s.connector == nil {
		s.setConnected(false)
		return zero, fmt.Errorf("%s has no connector: %w", s.name, ErrUnavailable)
	}

	client, err := s.connector(ctx)
	if err != nil {
		s.setConnected(false)
		return zero, fmt.Errorf("%s: %w: %w", s.name, ErrUnavailable, err)
	}

	s.mu.Lock()
	old, had := s.client, s.have
	s.client = client
	s.have = true
	s.mu.Unlock()

	if had && s.closer != nil {
		s.closer(old)
	}
	s.setConnected(true)
	return client, nil
}

// IsConnected reports whether the connection stands right now.
func (s *Service[T]) IsConnected() bool { return s.connected.Load() }

// Name identifies the service in a log line or a health report.
func (s *Service[T]) Name() string { return s.name }

// Run reconnects until the context ends. It returns only when the context ends,
// so a caller runs it in its own goroutine.
func (s *Service[T]) Run(ctx context.Context) {
	ticker := time.NewTicker(s.RetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.close()
			return
		case <-ticker.C:
			if s.connected.Load() {
				continue
			}
			if _, err := s.Connect(ctx); err != nil {
				s.log.Debug("cannot connect", "service", s.name, "error", err)
			}
		}
	}
}

// Drop marks the connection as down, so the next pass reopens it. A caller
// calls this when a request fails against a connection it already held.
func (s *Service[T]) Drop() { s.setConnected(false) }

func (s *Service[T]) close() {
	s.mu.Lock()
	client, had := s.client, s.have
	s.have = false
	s.mu.Unlock()
	if had && s.closer != nil {
		s.closer(client)
	}
}

func (s *Service[T]) setConnected(val bool) {
	if s.connected.Swap(val) == val {
		return
	}
	s.log.Info("connection state changed", "service", s.name, "connected", val)

	// A wedged subscriber misses one value and reads the next. It never
	// blocks this call.
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- val:
		default:
		}
	}
}

// ConnectedChan subscribes to connection state changes. The channel carries the
// current state at once, and one value per later change. It closes when ctx
// ends, so pass a context scoped to the reader.
func (s *Service[T]) ConnectedChan(ctx context.Context) <-chan bool {
	ch := make(chan bool, 1)
	ch <- s.connected.Load()

	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()

	go func() {
		<-ctx.Done()
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}()

	return ch
}
