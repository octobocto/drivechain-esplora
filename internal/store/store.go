// Package store holds the address index in Postgres.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:migrations
var migrations embed.FS

// Store reads and writes the index.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres and applies any missing migration.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases every connection.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the connection pool, for a health check or a test.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

const migrationsTable = `
CREATE TABLE IF NOT EXISTS migrations (
    name    TEXT        PRIMARY KEY,
    applied TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// migrate applies every embedded migration that is not yet recorded. It is safe
// to run on every start.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, migrationsTable); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		applied, err := s.migrationApplied(ctx, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := s.applyMigration(ctx, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrationApplied(ctx context.Context, name string) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM migrations WHERE name = $1)`, name).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("read migration state for %s: %w", name, err)
	}
	return found, nil
}

// applyMigration runs one migration and records it in the same transaction, so
// a crash never leaves a migration half applied.
func (s *Store) applyMigration(ctx context.Context, name, body string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("run migration %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migrations (name) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// inTx runs fn inside one transaction and commits only when it returns nil.
func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
