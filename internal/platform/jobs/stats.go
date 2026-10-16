package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats is the job queue state for operational health (Stage 14), measured from River's tables.
type Stats struct {
	Available        int64      `json:"available"`
	Running          int64      `json:"running"`
	Retryable        int64      `json:"retryable"`
	DiscardedLast24h int64      `json:"discarded_last_24h"` // failed for good: operator attention
	LastCompletedAt  *time.Time `json:"last_completed_at"`  // worker liveness evidence (periodic jobs complete every second or minute)
	LeaderElectedAt  *time.Time `json:"leader_elected_at"`
	LeaderExpiresAt  *time.Time `json:"leader_expires_at"`
}

// ReadStats reads the River job queue state.
func ReadStats(ctx context.Context, pool *pgxpool.Pool) (Stats, error) {
	var s Stats
	err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE state = 'available'),
		count(*) FILTER (WHERE state = 'running'),
		count(*) FILTER (WHERE state = 'retryable'),
		count(*) FILTER (WHERE state = 'discarded' AND finalized_at > now() - interval '24 hours'),
		max(finalized_at) FILTER (WHERE state = 'completed'),
		(SELECT max(elected_at) FROM `+Schema+`.river_leader),
		(SELECT max(expires_at) FROM `+Schema+`.river_leader)
		FROM `+Schema+`.river_job`).Scan(&s.Available, &s.Running, &s.Retryable, &s.DiscardedLast24h, &s.LastCompletedAt, &s.LeaderElectedAt, &s.LeaderExpiresAt)
	return s, err
}
