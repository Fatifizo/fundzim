package ledger

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Account status changes and periods errors.
var (
	ErrAccountState = errs.New(errs.Conflict, "LEDGER_ACCOUNT_STATE", "The account is not in a state that allows this change.")
	ErrNonZeroClose = errs.New(errs.Conflict, "LEDGER_ACCOUNT_BALANCE_NOT_ZERO", "An account can be closed only with a zero balance.")
	ErrReason       = errs.New(errs.Unprocessable, errs.CodeValidationFailed, "A reason of at least 10 characters is required.")
	ErrPeriodState  = errs.New(errs.Conflict, "LEDGER_PERIOD_STATE", "The period is not in a state that allows this action.")
	ErrPeriodOpenTo = errs.New(errs.Conflict, "LEDGER_PERIOD_NOT_ENDED", "A period can be closed only after it has ended.")
	ErrSelfApproval = errs.New(errs.Forbidden, "LEDGER_SELF_APPROVAL_FORBIDDEN", "The approver must be a different person from the requester.")
)

func cleanReason(r string) (string, error) {
	r = strings.TrimSpace(r)
	if n := len([]rune(r)); n < 10 || n > 2000 {
		return "", ErrReason
	}
	return r, nil
}

// SetAccountStatus freezes (ACTIVE→FROZEN), unfreezes (FROZEN→ACTIVE) or closes (→CLOSED, zero balance only) an
// account. The row lock serialises with postings (the entry trigger reads the account FOR SHARE).
func (s *Service) SetAccountStatus(ctx context.Context, staffID, accountID, to, reason string) error {
	reason, err := cleanReason(reason)
	if err != nil {
		return err
	}
	if !uuidRE.MatchString(accountID) {
		return ErrNotFound
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var from, code, ccy string
		err := tx.QueryRow(ctx, `SELECT status, code, currency FROM ledger.ledger_accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&from, &code, &ccy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		ok := (from == "ACTIVE" && (to == "FROZEN" || to == "CLOSED")) || (from == "FROZEN" && (to == "ACTIVE" || to == "CLOSED"))
		if !ok {
			return ErrAccountState
		}
		if to == "CLOSED" {
			var bal int64
			if err := tx.QueryRow(ctx, `SELECT balance_minor FROM ledger.v_balances_current WHERE account_id = $1`, accountID).Scan(&bal); err != nil {
				return err
			}
			if bal != 0 {
				return ErrNonZeroClose
			}
		}
		// clock_timestamp(): the moment after the row lock was taken (every earlier posting has committed)
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_accounts SET status = $2, status_reason = $3, status_changed_by = $4,
			status_changed_at = clock_timestamp() WHERE id = $1`, accountID, to, reason, staffID); err != nil {
			return mapDBError(err)
		}
		action := map[string]string{"FROZEN": "ledger.account.frozen", "ACTIVE": "ledger.account.unfrozen", "CLOSED": "ledger.account.closed"}[to]
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: action, ActorType: "staff", ActorID: staffID, TargetType: "ledger_account",
			TargetID: accountID, Reason: reason, Metadata: map[string]any{"code": code, "currency": ccy, "from": from, "to": to}}); err != nil {
			return err
		}
		ev := EvAccountStatusChanged
		if to == "FROZEN" {
			ev = EvAccountFrozen
		}
		_, err = outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "ledger_account", AggregateID: accountID, EventType: ev,
			Payload: map[string]any{"account_id": accountID, "from": from, "to": to}, OccurredAt: s.now()})
		return err
	})
}

// ---- periods ---------------------------------------------------------------------------------------------------

// Period is an accounting period.
type Period struct {
	ID         string     `json:"id"`
	Code       string     `json:"code"`
	StartsAt   time.Time  `json:"starts_at"`
	EndsAt     time.Time  `json:"ends_at"`
	Status     string     `json:"status"`
	CloseBy    *string    `json:"close_requested_by"`
	CloseAt    *time.Time `json:"close_requested_at"`
	ClosedBy   *string    `json:"closed_by"`
	ClosedAt   *time.Time `json:"closed_at"`
	EvidenceID *string    `json:"close_evidence_record_id"`
}

// EnsurePeriod creates the monthly period containing t if missing (idempotent; system action).
func (s *Service) EnsurePeriod(ctx context.Context, t time.Time) error {
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		id := ids.New()
		tag, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_periods (id, code, starts_at, ends_at, status) VALUES ($1, $2, $3, $4, 'OPEN')
			ON CONFLICT DO NOTHING`, id, start.Format("2006-01"), start, end)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.period.opened", ActorType: "system", TargetType: "ledger_period",
			TargetID: id, Metadata: map[string]any{"code": start.Format("2006-01")}})
		return err
	})
}

// EnsureCurrentPeriods keeps the current and next month open for postings.
func (s *Service) EnsureCurrentPeriods(ctx context.Context) error {
	now := s.now()
	if err := s.EnsurePeriod(ctx, now); err != nil {
		return err
	}
	return s.EnsurePeriod(ctx, now.AddDate(0, 1, 0))
}

// RequestPeriodClose is the maker step: OPEN → CLOSING, only once the period has ended.
func (s *Service) RequestPeriodClose(ctx context.Context, staffID, periodID, reason string) error {
	reason, err := cleanReason(reason)
	if err != nil {
		return err
	}
	return s.periodStep(ctx, periodID, func(ctx context.Context, tx pgx.Tx, p Period) error {
		if p.Status != "OPEN" {
			return ErrPeriodState
		}
		if p.EndsAt.After(s.now()) {
			return ErrPeriodOpenTo
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_periods SET status = 'CLOSING', close_requested_by = $2, close_requested_at = $3
			WHERE id = $1`, p.ID, staffID, s.now()); err != nil {
			return err
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.period.close_requested", ActorType: "staff", ActorID: staffID,
			TargetType: "ledger_period", TargetID: p.ID, Reason: reason, Metadata: map[string]any{"code": p.Code}})
		return err
	})
}

// AbortPeriodClose returns CLOSING → OPEN (either person).
func (s *Service) AbortPeriodClose(ctx context.Context, staffID, periodID, reason string) error {
	reason, err := cleanReason(reason)
	if err != nil {
		return err
	}
	return s.periodStep(ctx, periodID, func(ctx context.Context, tx pgx.Tx, p Period) error {
		if p.Status != "CLOSING" {
			return ErrPeriodState
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_periods SET status = 'OPEN' WHERE id = $1`, p.ID); err != nil {
			return err
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.period.close_aborted", ActorType: "staff", ActorID: staffID,
			TargetType: "ledger_period", TargetID: p.ID, Reason: reason, Metadata: map[string]any{"code": p.Code}})
		return err
	})
}

// ApprovePeriodClose is the checker step: CLOSING → CLOSED by a different person, after a trial balance of the
// period balances in every currency; the trial balance snapshot is recorded as evidence.
func (s *Service) ApprovePeriodClose(ctx context.Context, staffID, periodID, reason string) error {
	reason, err := cleanReason(reason)
	if err != nil {
		return err
	}
	return s.periodStep(ctx, periodID, func(ctx context.Context, tx pgx.Tx, p Period) error {
		if p.Status != "CLOSING" {
			return ErrPeriodState
		}
		if p.CloseBy != nil && *p.CloseBy == staffID {
			return ErrSelfApproval
		}
		tb, err := trialBalance(ctx, tx, "", p.ID)
		if err != nil {
			return err
		}
		for _, c := range tb.Currencies {
			if c.Difference != "0" {
				return errs.New(errs.Conflict, "LEDGER_TRIAL_BALANCE_NOT_ZERO", "The period's trial balance does not balance; it cannot close.")
			}
		}
		auditID, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.period.closed", ActorType: "staff", ActorID: staffID,
			TargetType: "ledger_period", TargetID: p.ID, Reason: reason, Metadata: map[string]any{"code": p.Code}})
		if err != nil {
			return err
		}
		evID, err := s.recordSnapshot(ctx, tx, "ledger_period", p.ID, staffID, auditID, map[string]any{"period": p.Code, "trial_balance": tb.summary()})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_periods SET status = 'CLOSED', closed_by = $2, closed_at = $3,
			close_evidence_record_id = $4 WHERE id = $1`, p.ID, staffID, s.now(), evID); err != nil {
			return mapDBError(err)
		}
		_, err = outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "ledger_period", AggregateID: p.ID, EventType: EvPeriodClosed,
			Payload: map[string]any{"period_id": p.ID, "code": p.Code}, OccurredAt: s.now()})
		return err
	})
}

func (s *Service) periodStep(ctx context.Context, periodID string, f func(context.Context, pgx.Tx, Period) error) error {
	if !uuidRE.MatchString(periodID) {
		return ErrNotFound
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, err := scanPeriod(tx.QueryRow(ctx, periodSelect+` WHERE id = $1 FOR UPDATE`, periodID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return f(ctx, tx, p)
	})
}

const periodSelect = `SELECT id, code, starts_at, ends_at, status, close_requested_by::text, close_requested_at, closed_by::text, closed_at,
	close_evidence_record_id::text FROM ledger.ledger_periods`

func scanPeriod(r pgx.Row) (Period, error) {
	var p Period
	err := r.Scan(&p.ID, &p.Code, &p.StartsAt, &p.EndsAt, &p.Status, &p.CloseBy, &p.CloseAt, &p.ClosedBy, &p.ClosedAt, &p.EvidenceID)
	return p, err
}

// Periods lists periods, newest first.
func (s *Service) Periods(ctx context.Context) ([]Period, error) {
	rows, err := s.Pool.Query(ctx, periodSelect+` ORDER BY starts_at DESC LIMIT 60`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Period, error) { return scanPeriod(r) })
}

// recordSnapshot stores a SYSTEM_SNAPSHOT evidence record (content hash of the canonical JSON; ids and totals only).
func (s *Service) recordSnapshot(ctx context.Context, tx pgx.Tx, subjectType, subjectID, staffID, auditEventID string,
	content map[string]any) (string, error) {
	sum := sha256JSON(content)
	byType, byID := "system", ""
	if staffID != "" {
		byType, byID = "staff", staffID
	}
	return audit.RecordEvidenceGateway(ctx, tx, audit.Evidence{Type: "SYSTEM_SNAPSHOT", SubjectType: subjectType, SubjectID: subjectID,
		RelatedRefs: content, CollectedByType: byType, CollectedByID: byID, Source: "system_snapshot", SHA256: sum[:],
		Classification: "C2", RetentionClass: "FINANCIAL", AuditEventID: auditEventID, CollectedAt: s.now()})
}
