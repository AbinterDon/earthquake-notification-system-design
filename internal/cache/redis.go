// Package cache provides Redis-backed location index and supersession cache.
package cache

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewClient creates a Redis client with startup retry logic.
func NewClient(addr string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx := context.Background()

	for attempt := 1; attempt <= 10; attempt++ {
		if err := rdb.Ping(ctx).Err(); err == nil {
			log.Printf("cache: connected to redis at %s (attempt %d)", addr, attempt)
			return rdb, nil
		}
		wait := time.Duration(attempt) * time.Second
		log.Printf("cache: redis not ready (attempt %d), retrying in %s…", attempt, wait)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("cache: could not connect to redis at %s", addr)
}
