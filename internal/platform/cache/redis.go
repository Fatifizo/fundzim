// Package cache wraps the optional Redis client. Redis is never authoritative: no balances, payment or
// payout state, ledger data, idempotency records or financial jobs (ARCHITECTURE §8.2). The platform must
// work without it.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is a minimal, context-aware cache API.
type Client struct{ rdb *redis.Client }

// Open parses url and returns a client with bounded timeouts. It does not require Redis to be up.
func Open(url string) (*Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, errors.New("cache: invalid redis URL")
	}
	opt.DialTimeout = 2 * time.Second
	opt.ReadTimeout = time.Second
	opt.WriteTimeout = time.Second
	opt.PoolTimeout = 2 * time.Second
	opt.MaxRetries = 1
	return &Client{rdb: redis.NewClient(opt)}, nil
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// Get returns a cached value; ok is false on a miss.
func (c *Client) Get(ctx context.Context, key string) (val string, ok bool, err error) {
	v, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// Set stores a value with a mandatory TTL (nothing is cached forever).
func (c *Client) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("cache: ttl must be positive")
	}
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

// Close releases connections.
func (c *Client) Close() error { return c.rdb.Close() }
