package audit

// Chain verification (Stage 18, ADR-050, F-S18-08). The database computes every hash (audit.verify_chain_range): Go only
// splits the work, keeps MAC-protected checkpoints and logs each run in audit.chain_verifications. A checkpoint's MAC is
// computed under a key the database does not hold, so a database-level rewrite of history cannot also forge the
// checkpoint that a later incremental run starts from.

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Chains are the two hash-chained audit tables.
var Chains = []string{"audit.audit_events", "audit.security_audit_events"}

// Verification modes and outcomes (audit.chain_verifications).
const (
	ModeFull        = "FULL"
	ModeIncremental = "INCREMENTAL"

	OutcomeIntact            = "INTACT"
	OutcomeProblemsFound     = "PROBLEMS_FOUND"
	OutcomeCheckpointInvalid = "CHECKPOINT_INVALID"

	checkpointDomain = "audit.chain_checkpoint/v1"
)

// MACKey computes the checkpoint MAC (crypto.Keyed satisfies it; the domain separates it from every other use).
type MACKey interface {
	KeyID() string
	Sum(domain string, parts ...string) []byte
}

// Verifier verifies audit chains. Pool must be the worker pool (EXECUTE on the verification routines).
type Verifier struct {
	Pool    *pgxpool.Pool
	Key     MACKey
	Now     func() time.Time
	Workers int   // parallel ranges for a FULL run (default 4)
	MinSpan int64 // rows below which a FULL run uses one range (default 50 000)
}

// Problem is one finding of a verification.
type Problem struct {
	Seq     int64  `json:"seq"`
	Problem string `json:"problem"`
}

// Result is one verification run as recorded in audit.chain_verifications.
type Result struct {
	ID           string        `json:"id"`
	Chain        string        `json:"chain"`
	Mode         string        `json:"mode"`
	FromSeq      int64         `json:"from_seq"`
	ToSeq        int64         `json:"to_seq"`
	CheckpointID string        `json:"checkpoint_id,omitempty"`
	Outcome      string        `json:"outcome"`
	Problems     []Problem     `json:"problems"`
	Workers      int           `json:"workers"`
	Duration     time.Duration `json:"-"`
	DurationMS   int64         `json:"duration_ms"`
}

// Intact reports whether the run found nothing.
func (r Result) Intact() bool { return r.Outcome == OutcomeIntact }

type checkpoint struct {
	id   string
	seq  int64
	hash []byte
	mac  []byte
	kid  string
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now().UTC()
	}
	return time.Now().UTC()
}

func (v *Verifier) mac(chain string, seq int64, hash []byte) []byte {
	return v.Key.Sum(checkpointDomain, chain, strconv.FormatInt(seq, 10), fmt.Sprintf("%x", hash))
}

func validChain(chain string) error {
	for _, c := range Chains {
		if c == chain {
			return nil
		}
	}
	return fmt.Errorf("audit: %q is not an audit chain", chain)
}

func (v *Verifier) head(ctx context.Context, chain string) (int64, []byte, error) {
	var seq int64
	var hash []byte
	err := v.Pool.QueryRow(ctx, `SELECT seq, hash FROM audit.chain_head($1::regclass)`, chain).Scan(&seq, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, nil
	}
	return seq, hash, err
}

func (v *Verifier) verifyRange(ctx context.Context, chain string, from, to int64) ([]Problem, error) {
	rows, err := v.Pool.Query(ctx, `SELECT seq, problem FROM audit.verify_chain_range($1::regclass, $2, $3)`, chain, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Problem
	for rows.Next() {
		var p Problem
		if err := rows.Scan(&p.Seq, &p.Problem); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// verifySpan verifies from..to in up to `workers` contiguous ranges on separate connections. Each range also checks its
// first row's link to the stored hash of the row before it, so the union is exactly one sequential pass.
func (v *Verifier) verifySpan(ctx context.Context, chain string, from, to int64, workers int) ([]Problem, int, error) {
	span := to - from + 1
	minSpan := v.MinSpan
	if minSpan <= 0 {
		minSpan = 50_000
	}
	if workers < 1 || span < minSpan {
		workers = 1
	}
	if int64(workers) > span {
		workers = int(span)
	}
	size := (span + int64(workers) - 1) / int64(workers)
	type part struct {
		problems []Problem
		err      error
	}
	parts := make([]part, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		lo := from + int64(i)*size
		hi := min(lo+size-1, to)
		if lo > hi {
			continue
		}
		wg.Add(1)
		go func(i int, lo, hi int64) {
			defer wg.Done()
			parts[i].problems, parts[i].err = v.verifyRange(ctx, chain, lo, hi)
		}(i, lo, hi)
	}
	wg.Wait()
	var all []Problem
	for _, p := range parts {
		if p.err != nil {
			return nil, workers, p.err
		}
		all = append(all, p.problems...)
	}
	return all, workers, nil
}

// VerifyFull recomputes the whole chain (in parallel ranges), records the run and, when intact, a new checkpoint.
func (v *Verifier) VerifyFull(ctx context.Context, chain string) (Result, error) {
	if err := validChain(chain); err != nil {
		return Result{}, err
	}
	start := v.now()
	headSeq, headHash, err := v.head(ctx, chain)
	if err != nil {
		return Result{}, err
	}
	res := Result{ID: ids.New(), Chain: chain, Mode: ModeFull, FromSeq: 1, ToSeq: headSeq, Workers: 1}
	if headSeq > 0 {
		w := v.Workers
		if w == 0 {
			w = 4
		}
		res.Problems, res.Workers, err = v.verifySpan(ctx, chain, 1, headSeq, w)
		if err != nil {
			return Result{}, err
		}
	}
	return v.finish(ctx, res, start, headHash)
}

// VerifyIncremental verifies from the latest checkpoint written under the current key to the head; checkpoints under any
// other (rotated, or foreign) key vouch for nothing and are ignored, and without one it runs a FULL verification. A
// checkpoint whose MAC fails under the current key, or whose row no longer carries the checkpointed hash, is reported as
// CHECKPOINT_INVALID (never skipped, never replaced by an older one).
func (v *Verifier) VerifyIncremental(ctx context.Context, chain string) (Result, error) {
	if err := validChain(chain); err != nil {
		return Result{}, err
	}
	var cp checkpoint
	err := v.Pool.QueryRow(ctx, `SELECT id::text, seq, hash, mac, mac_key_id FROM audit.chain_checkpoints
		WHERE chain = $1 AND mac_key_id = $2 ORDER BY seq DESC LIMIT 1`, chain, v.Key.KeyID()).Scan(&cp.id, &cp.seq, &cp.hash, &cp.mac, &cp.kid)
	if errors.Is(err, pgx.ErrNoRows) {
		return v.VerifyFull(ctx, chain)
	}
	if err != nil {
		return Result{}, err
	}
	start := v.now()
	headSeq, headHash, err := v.head(ctx, chain)
	if err != nil {
		return Result{}, err
	}
	res := Result{ID: ids.New(), Chain: chain, Mode: ModeIncremental, FromSeq: cp.seq, ToSeq: headSeq, CheckpointID: cp.id, Workers: 1}
	if !hmac.Equal(cp.mac, v.mac(chain, cp.seq, cp.hash)) {
		res.Outcome = OutcomeCheckpointInvalid
		res.Problems = []Problem{{Seq: cp.seq, Problem: "checkpoint MAC does not verify"}}
		return v.record(ctx, res, start, nil)
	}
	if headSeq < cp.seq {
		res.ToSeq = cp.seq
		res.Outcome = OutcomeCheckpointInvalid
		res.Problems = []Problem{{Seq: cp.seq, Problem: "chain head is below the checkpoint (rows removed)"}}
		return v.record(ctx, res, start, nil)
	}
	var stored []byte
	if err := v.Pool.QueryRow(ctx, fmt.Sprintf(`SELECT hash FROM %s WHERE seq = $1`, chain), cp.seq).Scan(&stored); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	if !hmac.Equal(stored, cp.hash) {
		res.Outcome = OutcomeCheckpointInvalid
		res.Problems = []Problem{{Seq: cp.seq, Problem: "row at the checkpoint no longer has the checkpointed hash (history rewritten)"}}
		return v.record(ctx, res, start, nil)
	}
	// the checkpoint row itself is re-verified (its hash and its link to the row before it), then everything after it
	res.Problems, res.Workers, err = v.verifySpan(ctx, chain, cp.seq, headSeq, 1)
	if err != nil {
		return Result{}, err
	}
	return v.finish(ctx, res, start, headHash)
}

func (v *Verifier) finish(ctx context.Context, res Result, start time.Time, headHash []byte) (Result, error) {
	if len(res.Problems) == 0 {
		res.Outcome = OutcomeIntact
	} else {
		res.Outcome = OutcomeProblemsFound
	}
	var cp *checkpoint
	if res.Outcome == OutcomeIntact && res.ToSeq > 0 {
		cp = &checkpoint{id: ids.New(), seq: res.ToSeq, hash: headHash, kid: v.Key.KeyID()}
		cp.mac = v.mac(res.Chain, cp.seq, cp.hash)
	}
	return v.record(ctx, res, start, cp)
}

func (v *Verifier) record(ctx context.Context, res Result, start time.Time, cp *checkpoint) (Result, error) {
	end := v.now()
	res.Duration = end.Sub(start)
	res.DurationMS = res.Duration.Milliseconds()
	if res.Problems == nil {
		res.Problems = []Problem{}
	}
	first := ""
	if len(res.Problems) > 0 {
		first = fmt.Sprintf("seq %d: %s", res.Problems[0].Seq, res.Problems[0].Problem)
	}
	var cpRef *string
	if res.CheckpointID != "" {
		cpRef = &res.CheckpointID
	}
	err := pgx.BeginFunc(ctx, v.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO audit.chain_verifications (id, chain, mode, from_seq, to_seq, checkpoint_id, problems,
			first_problem, outcome, workers, duration_ms, started_at, finished_at) VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,''),$9,$10,$11,$12,$13)`,
			res.ID, res.Chain, res.Mode, res.FromSeq, res.ToSeq, cpRef, len(res.Problems), first, res.Outcome, res.Workers,
			res.Duration.Milliseconds(), start, end); err != nil {
			return err
		}
		if cp == nil {
			return nil
		}
		// a concurrent run may already have checkpointed this head: the first one stands
		_, err := tx.Exec(ctx, `INSERT INTO audit.chain_checkpoints (id, chain, seq, hash, mac, mac_key_id, verification_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (chain, mac_key_id, seq) DO NOTHING`, cp.id, res.Chain, cp.seq, cp.hash, cp.mac, cp.kid, res.ID)
		return err
	})
	return res, err
}

// LatestVerification is the most recent run of a chain (ok false: never verified).
func LatestVerification(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, chain string) (outcome string, finished time.Time, ok bool, err error) {
	err = q.QueryRow(ctx, `SELECT outcome, finished_at FROM audit.chain_verifications WHERE chain = $1 ORDER BY finished_at DESC LIMIT 1`,
		chain).Scan(&outcome, &finished)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	return outcome, finished, err == nil, err
}

// OpsSnapshot reports each chain's latest verification (Stage 18): UNAVAILABLE when it found problems or a checkpoint
// did not verify, DEGRADED when the latest run is older than staleAfter, UNKNOWN when the chain was never verified.
func OpsSnapshot(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, now time.Time, staleAfter time.Duration) (opsview.Snapshot, error) {
	snap := opsview.Snapshot{Module: "audit", Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	for _, chain := range Chains {
		var outcome string
		var finished time.Time
		var toSeq int64
		err := q.QueryRow(ctx, `SELECT outcome, finished_at, to_seq FROM audit.chain_verifications WHERE chain = $1
			ORDER BY finished_at DESC LIMIT 1`, chain).Scan(&outcome, &finished, &toSeq)
		m := opsview.Metric{Key: "audit.chain." + chain[len("audit."):], Label: "Audit chain verification: " + chain, Module: "audit",
			Nature: opsview.Real, Permission: "security.alert.view", Source: "audit.chain_verifications (latest run)"}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			m.Status, m.Nature = opsview.StatusUnknown, opsview.Unknown
		case err != nil:
			return snap, err
		case outcome != OutcomeIntact:
			m.Status, m.Value = opsview.Unavailable, opsview.Int64(toSeq)
		case now.Sub(finished) > staleAfter:
			m.Status, m.Value = opsview.Degraded, opsview.Int64(toSeq)
		default:
			m.Status, m.Value = opsview.Healthy, opsview.Int64(toSeq)
		}
		snap.Metrics = append(snap.Metrics, m)
	}
	return snap, nil
}
