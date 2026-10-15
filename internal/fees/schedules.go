package fees

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Provider fee evidence (ADR-044 §8). No provider is selected (ADR-040), so no provider fee is known: a schedule is
// SYNTHETIC (sandbox providers only, for TEST) or UNVERIFIED (a documented claim). VERIFIED is refused in Stage 12.
// A schedule is used only once a second person activates it.

// Provider fee types.
const (
	FeeTypeTransaction = "TRANSACTION"
	FeeTypeSettlement  = "SETTLEMENT"
	FeeTypeRefund      = "REFUND"
	FeeTypeChargeback  = "CHARGEBACK"
	FeeTypePayout      = "PAYOUT"
)

// Confirmation statuses.
const (
	ConfSynthetic  = "SYNTHETIC"
	ConfUnverified = "UNVERIFIED"
	ConfVerified   = "VERIFIED"
)

// ErrVerifiedRefused: no provider evidence exists to verify against.
var ErrVerifiedRefused = errs.New(errs.Unprocessable, "PROVIDER_FEE_VERIFICATION_UNAVAILABLE",
	"Provider fees cannot be marked VERIFIED: no payment provider is selected and no contractual evidence exists (ADR-040).")

// ErrSyntheticRealProvider: synthetic schedules are for the sandbox simulator only.
var ErrSyntheticRealProvider = errs.New(errs.Unprocessable, "PROVIDER_FEE_SYNTHETIC_REAL_PROVIDER",
	"SYNTHETIC provider fees may only be recorded for the sandbox simulator.")

// NewSchedule records provider fee evidence.
type NewSchedule struct {
	ProviderID         string     `json:"provider_id"`
	ContractReference  string     `json:"contract_reference"`
	ScheduleVersion    string     `json:"schedule_version"`
	Currency           string     `json:"currency"`
	PaymentMethod      *string    `json:"payment_method"`
	FeeType            string     `json:"fee_type"`
	Model              string     `json:"model"`
	RateBps            *int64     `json:"rate_bps"`
	FixedMinor         *string    `json:"fixed_minor"`
	MinFeeMinor        *string    `json:"min_fee_minor"`
	MaxFeeMinor        *string    `json:"max_fee_minor"`
	Tiers              []TierDoc  `json:"tiers"`
	RoundingMode       *string    `json:"rounding_mode"` // null: the provider's rounding is UNKNOWN
	EffectiveFrom      time.Time  `json:"effective_from"`
	EffectiveTo        *time.Time `json:"effective_to"`
	EvidenceSource     string     `json:"evidence_source"`
	EvidenceReference  *string    `json:"evidence_reference"`
	ConfirmationStatus string     `json:"confirmation_status"`
}

// Schedule is a provider fee schedule.
type Schedule struct {
	ID                 string     `json:"id"`
	ProviderID         string     `json:"provider_id"`
	ProviderIsSandbox  bool       `json:"provider_is_sandbox"`
	ContractReference  string     `json:"contract_reference"`
	ScheduleVersion    string     `json:"schedule_version"`
	Currency           string     `json:"currency"`
	PaymentMethod      *string    `json:"payment_method"`
	FeeType            string     `json:"fee_type"`
	Terms              TermsDoc   `json:"terms"`
	RoundingKnown      bool       `json:"rounding_known"`
	EffectiveFrom      time.Time  `json:"effective_from"`
	EffectiveTo        *time.Time `json:"effective_to"`
	EvidenceSource     string     `json:"evidence_source"`
	EvidenceReference  *string    `json:"evidence_reference"`
	ConfirmationStatus string     `json:"confirmation_status"`
	Status             string     `json:"status"`
	FeeEnvironment     string     `json:"fee_environment"`
	CreatedBy          string     `json:"created_by"`
	ApprovedBy         *string    `json:"approved_by"`
	ApprovedAt         *time.Time `json:"approved_at"`
	RetiredBy          *string    `json:"retired_by"`
	RetiredAt          *time.Time `json:"retired_at"`
	CreatedAt          time.Time  `json:"created_at"`
}

const scheduleCols = `id, provider_id, provider_is_sandbox, contract_reference, schedule_version, currency, payment_method, fee_type, model,
	rate_bps, fixed_minor, min_fee_minor, max_fee_minor, tiers, rounding_mode, effective_from, effective_to, evidence_source, evidence_reference,
	confirmation_status, status, fee_environment, created_by, approved_by, approved_at, retired_by, retired_at, created_at`

func scanSchedule(r pgx.Row) (Schedule, error) {
	var sc Schedule
	var t rawTerms
	var rounding *string
	err := r.Scan(&sc.ID, &sc.ProviderID, &sc.ProviderIsSandbox, &sc.ContractReference, &sc.ScheduleVersion, &sc.Currency, &sc.PaymentMethod,
		&sc.FeeType, &t.model, &t.rate, &t.fixed, &t.minF, &t.maxF, &t.tiers, &rounding, &sc.EffectiveFrom, &sc.EffectiveTo, &sc.EvidenceSource,
		&sc.EvidenceReference, &sc.ConfirmationStatus, &sc.Status, &sc.FeeEnvironment, &sc.CreatedBy, &sc.ApprovedBy, &sc.ApprovedAt,
		&sc.RetiredBy, &sc.RetiredAt, &sc.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sc, ErrNotFound
	}
	if err != nil {
		return sc, err
	}
	if rounding != nil {
		t.rounding, sc.RoundingKnown = *rounding, true
	}
	terms := Terms{Model: t.model, RateBps: t.rate, Fixed: t.fixed, Min: t.minF, Max: t.maxF, Rounding: t.rounding}
	if len(t.tiers) > 0 {
		var docs []TierDoc
		if err := json.Unmarshal(t.tiers, &docs); err != nil {
			return sc, err
		}
		if terms.Tiers, err = TiersFromDocs(docs); err != nil {
			return sc, err
		}
	}
	sc.Terms = terms.Doc()
	return sc, nil
}

func (sc Schedule) input() (*ScheduleInput, error) {
	t, err := sc.Terms.Terms()
	if err != nil {
		return nil, err
	}
	return &ScheduleInput{ScheduleID: sc.ID, Terms: t, ConfirmationStatus: sc.ConfirmationStatus}, nil
}

// CreateSchedule records provider fee evidence (fee.config.request) as PENDING_APPROVAL.
func (s *Service) CreateSchedule(ctx context.Context, staffID string, in NewSchedule) (Schedule, error) {
	if in.ConfirmationStatus == ConfVerified {
		return Schedule{}, ErrVerifiedRefused
	}
	in.PaymentMethod, in.EvidenceReference = trimP(in.PaymentMethod), trimP(in.EvidenceReference)
	var det []errs.Detail
	add := func(f, c string) { det = append(det, errs.Detail{Field: f, Code: c}) }
	if !ids.Valid(in.ProviderID) {
		add("provider_id", "INVALID_FORMAT")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.ContractReference)); n < 3 || n > 200 {
		add("contract_reference", "INVALID_LENGTH")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.ScheduleVersion)); n < 1 || n > 64 {
		add("schedule_version", "INVALID_LENGTH")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.EvidenceSource)); n < 3 || n > 200 {
		add("evidence_source", "INVALID_LENGTH")
	}
	if in.EvidenceReference != nil && utf8.RuneCountInString(*in.EvidenceReference) > 500 {
		add("evidence_reference", "INVALID_LENGTH")
	}
	if in.PaymentMethod != nil && !methodRE.MatchString(*in.PaymentMethod) {
		add("payment_method", "INVALID_FORMAT")
	}
	switch in.FeeType {
	case FeeTypeTransaction, FeeTypeSettlement, FeeTypeRefund, FeeTypeChargeback, FeeTypePayout:
	default:
		add("fee_type", "INVALID_VALUE") // FX margins: FundZim never converts currencies
	}
	switch in.ConfirmationStatus {
	case ConfSynthetic, ConfUnverified:
	default:
		add("confirmation_status", "INVALID_VALUE")
	}
	rounding := HalfUp // validation only: the stored rounding stays NULL (UNKNOWN) unless given
	if in.RoundingMode != nil {
		rounding = *in.RoundingMode
	}
	vin := VersionInput{Model: in.Model, RateBps: in.RateBps, FixedMinor: in.FixedMinor, MinFeeMinor: in.MinFeeMinor, MaxFeeMinor: in.MaxFeeMinor,
		Tiers: in.Tiers, RoundingMode: rounding}
	t, tdet := vin.terms()
	det = append(det, tdet...)
	if in.Model == ModelZero || in.Model == ModelPassThrough {
		add("model", "INVALID_VALUE")
	}
	if in.EffectiveFrom.IsZero() {
		add("effective_from", "REQUIRED")
	}
	if in.EffectiveTo != nil && !in.EffectiveTo.After(in.EffectiveFrom) {
		add("effective_to", "BEFORE_START")
	}
	if len(det) > 0 {
		return Schedule{}, detailsError(det)
	}
	if err := s.currencyUsable(ctx, in.Currency); err != nil {
		return Schedule{}, err
	}
	sandbox, err := s.Routing.ProviderIsSandbox(ctx, in.ProviderID)
	if err != nil {
		return Schedule{}, err
	}
	if in.ConfirmationStatus == ConfSynthetic && !sandbox {
		return Schedule{}, ErrSyntheticRealProvider
	}
	tiers, err := tiersJSON(t)
	if err != nil {
		return Schedule{}, err
	}
	id := ids.New()
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app.fee_provider_schedules (id, provider_id, provider_is_sandbox, contract_reference,
			schedule_version, currency, payment_method, fee_type, model, rate_bps, fixed_minor, min_fee_minor, max_fee_minor, tiers, rounding_mode,
			effective_from, effective_to, evidence_source, evidence_reference, confirmation_status, status, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, 'PENDING_APPROVAL', $21)`,
			id, in.ProviderID, sandbox, strings.TrimSpace(in.ContractReference), strings.TrimSpace(in.ScheduleVersion), in.Currency, in.PaymentMethod,
			in.FeeType, t.Model, t.RateBps, t.Fixed, t.Min, t.Max, tiers, in.RoundingMode, in.EffectiveFrom.UTC(), toUTC(in.EffectiveTo),
			strings.TrimSpace(in.EvidenceSource), in.EvidenceReference, in.ConfirmationStatus, staffID); err != nil {
			return mapDB(err)
		}
		if err := event(ctx, tx, "fee_provider_schedule", id, "SCHEDULE_RECORDED", staff(staffID), map[string]any{
			"confirmation_status": in.ConfirmationStatus, "fee_type": in.FeeType}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.provider_schedule.recorded", TargetType: "fee_provider_schedule", TargetID: id,
			Metadata: map[string]any{"confirmation_status": in.ConfirmationStatus}})
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.Schedule(ctx, id)
}

// Schedule returns one provider fee schedule.
func (s *Service) Schedule(ctx context.Context, id string) (Schedule, error) {
	if !ids.Valid(id) {
		return Schedule{}, ErrNotFound
	}
	return scanSchedule(s.Pool.QueryRow(ctx, `SELECT `+scheduleCols+` FROM app.fee_provider_schedules WHERE id = $1`, id))
}

// Schedules lists provider fee schedules (newest first).
func (s *Service) Schedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+scheduleCols+` FROM app.fee_provider_schedules ORDER BY created_at DESC, id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ApproveSchedule activates provider fee evidence (fee.config.approve; never its recorder or anyone linked).
func (s *Service) ApproveSchedule(ctx context.Context, staffID string, stepUpAt time.Time, id string) (Schedule, error) {
	return s.scheduleDecision(ctx, staffID, stepUpAt, id, "", true)
}

// RetireSchedule retires provider fee evidence (PENDING_APPROVAL or ACTIVE → RETIRED).
func (s *Service) RetireSchedule(ctx context.Context, staffID string, stepUpAt time.Time, id, why string) (Schedule, error) {
	return s.scheduleDecision(ctx, staffID, stepUpAt, id, why, false)
}

func (s *Service) scheduleDecision(ctx context.Context, staffID string, stepUpAt time.Time, id, why string, approve bool) (Schedule, error) {
	if stepUpAt.IsZero() {
		return Schedule{}, ErrStepUp
	}
	var r string
	if !approve {
		var err error
		if r, err = reason(why); err != nil {
			return Schedule{}, err
		}
	}
	now := s.now()
	cur, err := s.Schedule(ctx, id)
	if err != nil {
		return Schedule{}, err
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockCurrency(ctx, tx, cur.Currency, true); err != nil {
			return err
		}
		sc, err := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleCols+` FROM app.fee_provider_schedules WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if approve {
			if sc.Status != "PENDING_APPROVAL" {
				return ErrPolicyState
			}
			if self, err := s.linked(ctx, staffID, sc.CreatedBy); err != nil {
				return err
			} else if self {
				return ErrSelfApproval
			}
			_, err = tx.Exec(ctx, `UPDATE app.fee_provider_schedules SET status = 'ACTIVE', approved_by = $2, approved_at = $3, version = version + 1
				WHERE id = $1`, id, staffID, now)
		} else {
			if sc.Status == "RETIRED" {
				return ErrPolicyState
			}
			_, err = tx.Exec(ctx, `UPDATE app.fee_provider_schedules SET status = 'RETIRED', retired_by = $2, retired_at = $3, version = version + 1
				WHERE id = $1`, id, staffID, now)
		}
		if err != nil {
			return mapDB(err)
		}
		n, err := invalidateQuotes(ctx, tx, sc.Currency, "provider fee schedule "+id+" changed", now)
		if err != nil {
			return err
		}
		ev := map[bool]string{true: "SCHEDULE_ACTIVATED", false: "SCHEDULE_RETIRED"}[approve]
		if err := event(ctx, tx, "fee_provider_schedule", id, ev, staff(staffID), map[string]any{"reason": r, "quotes_invalidated": n}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.provider_schedule." + strings.ToLower(strings.TrimPrefix(ev, "SCHEDULE_")),
			TargetType: "fee_provider_schedule", TargetID: id, Reason: r})
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.Schedule(ctx, id)
}

// resolveSchedule selects the ACTIVE schedule in force for a provider, fee type, currency and method: a
// method-specific schedule beats a provider-wide one, then the latest effective start. nil: no evidence.
func (s *Service) resolveSchedule(ctx context.Context, q querier, providerID, feeType, currency, method string, at time.Time) (*Schedule, error) {
	if providerID == "" {
		return nil, nil
	}
	if q == nil {
		q = s.Pool
	}
	sc, err := scanSchedule(q.QueryRow(ctx, `SELECT `+scheduleCols+` FROM app.fee_provider_schedules
		WHERE status = 'ACTIVE' AND provider_id::text = $1 AND fee_type = $2 AND currency = $3 AND (payment_method IS NULL OR payment_method = $4)
		  AND effective_from <= $5 AND (effective_to IS NULL OR effective_to > $5)
		ORDER BY (payment_method IS NOT NULL) DESC, effective_from DESC, id LIMIT 1`, providerID, feeType, currency, method, at.UTC()))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sc, nil
}
