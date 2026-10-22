package reconciliation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Matching rule sets (docs/stage-17/matching-engine.md §4). The tunable parameters of MATCHER_V1 are a maker-checker
// approved, hashed, immutable rule set; one is ACTIVE at a time; none is seeded (they encode settlement-timing
// assumptions nobody has confirmed), so matching fails closed until FINANCE approves one. A new version never rewrites an
// earlier decision: every match and exception records the version it was made under.

// RuleSetConfig is the closed, bounded key set (also enforced by ck_recon_rule_sets_config).
type RuleSetConfig struct {
	DateWindowDays        int  `json:"date_window_days"`
	SettlementHorizonDays int  `json:"settlement_horizon_days"`
	StaleAfterHours       int  `json:"stale_after_hours"`
	EscalateAfterHours    *int `json:"escalate_after_hours,omitempty"`
}

// Bounds of each key (engineering limits on what may be configured, not defaults).
var ruleBounds = map[string][2]int{"date_window_days": {0, 14}, "settlement_horizon_days": {1, 60}, "stale_after_hours": {1, 2160},
	"escalate_after_hours": {1, 2160}}

// Validate checks the bounds.
func (c RuleSetConfig) Validate() error {
	var det []errs.Detail
	check := func(k string, v int) {
		b := ruleBounds[k]
		if v < b[0] || v > b[1] {
			det = append(det, errs.Detail{Field: "config." + k, Code: "OUT_OF_RANGE"})
		}
	}
	check("date_window_days", c.DateWindowDays)
	check("settlement_horizon_days", c.SettlementHorizonDays)
	check("stale_after_hours", c.StaleAfterHours)
	if c.EscalateAfterHours != nil {
		check("escalate_after_hours", *c.EscalateAfterHours)
	}
	if len(det) > 0 {
		return httpx.Validation(det...)
	}
	return nil
}

// Hash is the SHA-256 of the canonical JSON (struct field order is fixed).
func (c RuleSetConfig) Hash() [32]byte {
	raw, _ := json.Marshal(c)
	return sha256.Sum256(raw)
}

// RuleSetRecord is a rule set as staff see it.
type RuleSetRecord struct {
	ID            string        `json:"id"`
	VersionNo     int           `json:"version_no"`
	Status        string        `json:"status"`
	Config        RuleSetConfig `json:"config"`
	ConfigSHA256  string        `json:"config_sha256"`
	ChangeReason  string        `json:"change_reason"`
	ProposedBy    string        `json:"proposed_by"`
	ProposedAt    time.Time     `json:"proposed_at"`
	DecidedBy     *string       `json:"decided_by"`
	DecidedAt     *time.Time    `json:"decided_at"`
	DecisionNote  *string       `json:"decision_note"`
	EffectiveFrom *time.Time    `json:"effective_from"`
	RetiredAt     *time.Time    `json:"retired_at"`
	Version       int           `json:"version"`
}

// RuleSetInput is POST /admin/reconciliation/rule-sets.
type RuleSetInput struct {
	Config       RuleSetConfig `json:"config"`
	ChangeReason string        `json:"change_reason"`
}

const ruleCols = `id::text, version_no, status, config, config_sha256, change_reason, proposed_by::text, proposed_at, decided_by::text,
	decided_at, decision_note, effective_from, retired_at, version`

func scanRuleSet(r pgx.Row) (RuleSetRecord, error) {
	var x RuleSetRecord
	var cfg, sum []byte
	err := r.Scan(&x.ID, &x.VersionNo, &x.Status, &cfg, &sum, &x.ChangeReason, &x.ProposedBy, &x.ProposedAt, &x.DecidedBy, &x.DecidedAt,
		&x.DecisionNote, &x.EffectiveFrom, &x.RetiredAt, &x.Version)
	if err != nil {
		return x, err
	}
	_ = json.Unmarshal(cfg, &x.Config)
	x.ConfigSHA256 = hex.EncodeToString(sum)
	return x, nil
}

// RuleSets lists every rule set, newest version first.
func (s *Service) RuleSets(ctx context.Context) ([]RuleSetRecord, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+ruleCols+` FROM recon.matching_rule_sets ORDER BY version_no DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RuleSetRecord, error) { return scanRuleSet(r) })
}

// ActiveRuleSet returns the active rule set (ok=false when none: matching fails closed).
func (s *Service) ActiveRuleSet(ctx context.Context) (RuleSet, bool, error) {
	x, err := scanRuleSet(s.Pool.QueryRow(ctx, `SELECT `+ruleCols+` FROM recon.matching_rule_sets WHERE status = 'ACTIVE'`))
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleSet{}, false, nil
	}
	if err != nil {
		return RuleSet{}, false, err
	}
	return RuleSet{ID: x.ID, Version: x.VersionNo, DateWindowDays: x.Config.DateWindowDays, SettlementHorizonDays: x.Config.SettlementHorizonDays,
		StaleAfterHours: x.Config.StaleAfterHours, EscalateAfterHours: x.Config.EscalateAfterHours}, true, nil
}

// ProposeRuleSet files a rule set version (maker).
func (s *Service) ProposeRuleSet(ctx context.Context, staffID string, in RuleSetInput) (RuleSetRecord, error) {
	if err := in.Config.Validate(); err != nil {
		return RuleSetRecord{}, err
	}
	reason, err := cleanNote(in.ChangeReason, 10, 1000)
	if err != nil {
		return RuleSetRecord{}, httpx.Validation(errs.Detail{Field: "change_reason", Code: "INVALID_LENGTH"})
	}
	cfg, _ := json.Marshal(in.Config)
	sum := in.Config.Hash()
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO recon.matching_rule_sets (id, version_no, status, config, config_sha256, change_reason, proposed_by, proposed_at)
			VALUES ($1, (SELECT coalesce(max(version_no), 0) + 1 FROM recon.matching_rule_sets), 'PENDING_APPROVAL', $2, $3, $4, $5, $6)`,
			id, cfg, sum[:], reason, staffID, s.now())
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return errs.New(errs.Conflict, "RULE_SET_PENDING", "Another rule set is waiting for approval. Decide or withdraw it first.")
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.rule_set.proposed", ActorType: "staff", ActorID: staffID,
			TargetType: "reconciliation_rule_set", TargetID: id, Reason: reason, Metadata: map[string]any{"config_sha256": hex.EncodeToString(sum[:])}})
	})
	if err != nil {
		return RuleSetRecord{}, err
	}
	return s.ruleSet(ctx, id)
}

func (s *Service) ruleSet(ctx context.Context, id string) (RuleSetRecord, error) {
	if !ids.Valid(id) {
		return RuleSetRecord{}, ErrNotFound
	}
	x, err := scanRuleSet(s.Pool.QueryRow(ctx, `SELECT `+ruleCols+` FROM recon.matching_rule_sets WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleSetRecord{}, ErrNotFound
	}
	return x, err
}

// DecideRuleSet: approve or reject (checker; never the proposer or a linked account; step-up by route), withdraw (the
// proposer), retire the active set (checker permission; matching then fails closed until another is approved).
func (s *Service) DecideRuleSet(ctx context.Context, staffID, id, action, note, expectedHash string) (RuleSetRecord, error) {
	n, err := cleanNote(note, 5, 1000)
	if err != nil {
		return RuleSetRecord{}, err
	}
	idents, err := s.identities(ctx, staffID)
	if err != nil {
		return RuleSetRecord{}, err
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status, proposer string
		var sum []byte
		if err := tx.QueryRow(ctx, `SELECT status, proposed_by::text, config_sha256 FROM recon.matching_rule_sets WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &proposer, &sum); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		now := s.now()
		switch action {
		case "approve", "reject":
			if status != "PENDING_APPROVAL" {
				return ErrState
			}
			if idents[proposer] {
				return ErrSelf
			}
			if action == "approve" {
				if expectedHash != "" && expectedHash != hex.EncodeToString(sum) {
					return ErrHashMismatch
				}
				if _, err := tx.Exec(ctx, `UPDATE recon.matching_rule_sets SET status = 'RETIRED', retired_by = $1, retired_at = $2, version = version + 1
					WHERE status = 'ACTIVE'`, staffID, now); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE recon.matching_rule_sets SET status = 'ACTIVE', decided_by = $2, decided_at = $3, decision_note = $4,
					effective_from = $3, version = version + 1 WHERE id = $1`, id, staffID, now, n); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `UPDATE recon.matching_rule_sets SET status = 'REJECTED', decided_by = $2, decided_at = $3, decision_note = $4,
				version = version + 1 WHERE id = $1`, id, staffID, now, n); err != nil {
				return err
			}
		case "withdraw":
			if status != "PENDING_APPROVAL" {
				return ErrState
			}
			if proposer != staffID {
				return errs.New(errs.Forbidden, "NOT_PROPOSER", "Only the proposer can withdraw a rule set.")
			}
			if _, err := tx.Exec(ctx, `UPDATE recon.matching_rule_sets SET status = 'WITHDRAWN', decision_note = $2, version = version + 1 WHERE id = $1`,
				id, n); err != nil {
				return err
			}
		case "retire":
			if status != "ACTIVE" {
				return ErrState
			}
			if _, err := tx.Exec(ctx, `UPDATE recon.matching_rule_sets SET status = 'RETIRED', retired_by = $2, retired_at = $3, version = version + 1
				WHERE id = $1`, id, staffID, now); err != nil {
				return err
			}
		default:
			return ErrNotFound
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.rule_set." + action, ActorType: "staff", ActorID: staffID,
			TargetType: "reconciliation_rule_set", TargetID: id, Reason: n})
	})
	if err != nil {
		return RuleSetRecord{}, err
	}
	return s.ruleSet(ctx, id)
}
