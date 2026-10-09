// Package featureflags reads feature flags (app.feature_flags). It is read-only: flags change only through the
// governed change process (app.feature_flag_changes, maker-checker for POLICY and KILL_SWITCH flags), never
// from application code. An unknown flag is reported as disabled together with ErrUnknownFlag, so callers fail
// closed.
package featureflags

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// Known flag keys.
const (
	// IndividualForOthers allows individuals to raise funds for someone else (LR-046/047/048/068, PD-27).
	IndividualForOthers = "campaign.individual_for_others.enabled"
)

// ErrUnknownFlag means no flag with that key exists (treat as disabled).
var ErrUnknownFlag = errors.New("featureflags: unknown flag")

// Querier is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var keyRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// Enabled reports whether the flag is on. It returns (false, ErrUnknownFlag) for a missing or malformed key
// and (false, err) on a database error.
func Enabled(ctx context.Context, q Querier, key string) (bool, error) {
	if !keyRe.MatchString(key) {
		return false, ErrUnknownFlag
	}
	var on bool
	err := q.QueryRow(ctx, `SELECT enabled FROM app.feature_flags WHERE key = $1`, key).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	if err != nil {
		return false, err
	}
	return on, nil
}
