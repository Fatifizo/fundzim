// Package idempotency implements the Idempotency-Key protocol (docs/api/api-design.md §7,
// docs/stage-4/interface-contracts.md §3.3) on app.idempotency_keys.
//
// PostgreSQL is the only store (never Valkey: idempotency records are authoritative, ARCHITECTURE §8.2).
// The unique constraint uq_idempotency_keys_scope_key decides which concurrent request owns a key; the
// guard trigger keeps scope/key/request_hash immutable and freezes COMPLETED rows. An IN_PROGRESS row
// carries a lease (locked_until); the exact lease value doubles as a fencing token, so a request whose
// lease was taken over after expiry can no longer complete or release the record.
package idempotency

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Defaults.
const (
	DefaultTTL   = 24 * time.Hour  // api-design §7.2: at least 24 hours
	DefaultLease = 2 * time.Minute // must exceed the request timeout (HTTP_REQUEST_TIMEOUT)
)

// Store persists idempotency records.
type Store struct {
	pool  *pgxpool.Pool
	clk   clock.Clock
	ttl   time.Duration
	lease time.Duration
}

// NewStore returns a store. ttl <= 0 means DefaultTTL; clk nil means clock.System.
func NewStore(pool *pgxpool.Pool, clk clock.Clock, ttl time.Duration) *Store {
	if clk == nil {
		clk = clock.System
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{pool: pool, clk: clk, ttl: ttl, lease: DefaultLease}
}

// SetLease changes how long an IN_PROGRESS record blocks other requests with the same key before it
// may be taken over. Call it before serving traffic; d must exceed the request timeout.
func (s *Store) SetLease(d time.Duration) {
	if d > 0 {
		s.lease = d
	}
}

// DeleteExpired purges records past expires_at in bounded batches (worker maintenance job).
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	const batch = 5000
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM app.idempotency_keys WHERE id IN (
			SELECT id FROM app.idempotency_keys WHERE expires_at <= $1 ORDER BY expires_at LIMIT $2)`,
			s.clk.Now(), batch)
		if err != nil {
			return total, fmt.Errorf("idempotency: delete expired: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			return total, nil
		}
	}
}

type outcome int

const (
	acquired   outcome = iota // caller owns the key and must complete or release it
	replay                    // COMPLETED with the same fingerprint
	inProgress                // another request holds a live lease
	reused                    // same key, different fingerprint
)

type beginResult struct {
	outcome outcome
	id      string
	lease   time.Time // acquired: the fencing token
	status  int       // replay
	body    []byte    // replay (nil when the response had no body)
	retry   time.Duration
}

func (s *Store) now() time.Time { return s.clk.Now().UTC().Truncate(time.Microsecond) }

// begin claims (scope, key) or reports what the existing record means for this request.
func (s *Store) begin(ctx context.Context, scope, key string, hash []byte) (beginResult, error) {
	for attempt := 0; attempt < 5; attempt++ {
		now := s.now()
		lease := now.Add(s.lease)
		id := ids.New()
		tag, err := s.pool.Exec(ctx, `INSERT INTO app.idempotency_keys
			(id, scope, key, request_hash, status, locked_until, created_at, updated_at, expires_at)
			VALUES ($1, $2, $3, $4, 'IN_PROGRESS', $5, $6, $6, $7)
			ON CONFLICT ON CONSTRAINT uq_idempotency_keys_scope_key DO NOTHING`,
			id, scope, key, hash, lease, now, now.Add(s.ttl))
		if err != nil {
			return beginResult{}, fmt.Errorf("idempotency: insert: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return beginResult{outcome: acquired, id: id, lease: lease}, nil
		}

		var (
			exID, status string
			exHash, body []byte
			lockedUntil  *time.Time
			code         *int16
			expiresAt    time.Time
		)
		err = s.pool.QueryRow(ctx, `SELECT id, request_hash, status, locked_until, response_status_code,
			response_body, expires_at FROM app.idempotency_keys WHERE scope = $1 AND key = $2`, scope, key).
			Scan(&exID, &exHash, &status, &lockedUntil, &code, &body, &expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // released or purged between the insert and the read: try again
		}
		if err != nil {
			return beginResult{}, fmt.Errorf("idempotency: read: %w", err)
		}
		if !expiresAt.After(now) {
			// Retention has passed but the purge job has not run: the old record no longer counts.
			if _, err := s.pool.Exec(ctx, `DELETE FROM app.idempotency_keys WHERE id = $1 AND expires_at <= $2`, exID, now); err != nil {
				return beginResult{}, fmt.Errorf("idempotency: delete expired record: %w", err)
			}
			continue
		}
		if !bytes.Equal(exHash, hash) {
			return beginResult{outcome: reused}, nil
		}
		if status == "COMPLETED" {
			if code == nil {
				return beginResult{}, errors.New("idempotency: completed record without status")
			}
			return beginResult{outcome: replay, status: int(*code), body: body}, nil
		}
		if lockedUntil != nil && lockedUntil.After(now) {
			return beginResult{outcome: inProgress, retry: lockedUntil.Sub(now)}, nil
		}
		// Lease expired (the owner crashed or overran): take over with a compare-and-swap on the
		// observed lease, so exactly one contender wins and the previous owner is fenced off.
		tag, err = s.pool.Exec(ctx, `UPDATE app.idempotency_keys SET locked_until = $3
			WHERE id = $1 AND status = 'IN_PROGRESS' AND locked_until IS NOT DISTINCT FROM $2`, exID, lockedUntil, lease)
		if err != nil {
			return beginResult{}, fmt.Errorf("idempotency: take over: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return beginResult{outcome: acquired, id: exID, lease: lease}, nil
		}
	}
	return beginResult{}, errors.New("idempotency: could not settle key state")
}

// errLeaseLost means the record was taken over (or removed) while this request ran.
var errLeaseLost = errors.New("idempotency: lease lost")

// complete stores the final response and freezes the record.
func (s *Store) complete(ctx context.Context, id string, lease time.Time, status int, body []byte) error {
	now := s.now()
	var b any
	if body != nil {
		b = string(body)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE app.idempotency_keys
		SET status = 'COMPLETED', locked_until = NULL, response_status_code = $3, response_body = $4::jsonb,
		    completed_at = $5, expires_at = greatest(expires_at, $6)
		WHERE id = $1 AND status = 'IN_PROGRESS' AND locked_until = $2`,
		id, lease, int16(status), b, now, now.Add(s.ttl))
	if err != nil {
		return fmt.Errorf("idempotency: complete: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	return nil
}

// release deletes an IN_PROGRESS record so the client may retry with the same key.
func (s *Store) release(ctx context.Context, id string, lease time.Time) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM app.idempotency_keys WHERE id = $1 AND status = 'IN_PROGRESS' AND locked_until = $2`, id, lease)
	if err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	return nil
}
