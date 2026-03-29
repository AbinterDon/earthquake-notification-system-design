// Package db provides PostgreSQL helpers for config and outbox repositories.
package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgxpool with exponential-backoff retry (useful for Docker
// startup ordering where Postgres may not be ready immediately).
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	var (
		pool *pgxpool.Pool
		err  error
	)
	for attempt := 1; attempt <= 10; attempt++ {
		pool, err = pgxpool.New(ctx, dsn)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				log.Printf("db: connected to postgres (attempt %d)", attempt)
				return pool, nil
			}
			pool.Close()
		}
		wait := time.Duration(attempt) * time.Second
		log.Printf("db: postgres not ready (attempt %d), retrying in %s…", attempt, wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, fmt.Errorf("db: could not connect to postgres after retries: %w", err)
}
