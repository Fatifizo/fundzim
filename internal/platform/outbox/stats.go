package outbox

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats is the outbox state for operational health (Stage 14): measured from app.outbox_events, never estimated.
type Stats struct {
	Pending          int64      `json:"pending"`            // not yet dispatched (and not dead-lettered)
	OldestPendingAt  *time.Time `json:"oldest_pending_at"`  // occurred_at of the oldest pending event
	DeadLettered     int64      `json:"dead_lettered"`      // relay gave up; operator action required
	DispatchedLast1h int64      `json:"dispatched_last_1h"` // relay throughput evidence
	LastDispatchedAt *time.Time `json:"last_dispatched_at"`
}

// ReadStats reads the outbox state.
func ReadStats(ctx context.Context, pool *pgxpool.Pool) (Stats, error) {
	var s Stats
	err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL),
		min(occurred_at) FILTER (WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL),
		count(*) FILTER (WHERE dead_lettered_at IS NOT NULL),
		count(*) FILTER (WHERE dispatched_at > now() - interval '1 hour'),
		max(dispatched_at)
		FROM app.outbox_events`).Scan(&s.Pending, &s.OldestPendingAt, &s.DeadLettered, &s.DispatchedLast1h, &s.LastDispatchedAt)
	return s, err
}
