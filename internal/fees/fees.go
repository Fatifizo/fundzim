// Package fees is the Stage 12 fee module (ADR-044): versioned fee policies with maker-checker, provider fee
// evidence, the pure calculation engine (calc.go), quotes, assessments, adjustments, fee accounting through the
// ledger, reconciliation and revenue reporting. Every fee is a TEST fee: production fee charging and revenue
// recognition are BLOCKED (ProductionFeesEnabled = false; ck_*_test_only_stage12).
//
// fees imports platform, audit and ledger only (ADR-044 §2). Campaign facts, provider routing and linked staff
// identities reach it through the small interfaces below, wired in internal/app.
package fees

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
)

// ProductionFeesEnabled gates every production fee path. It is a constant on purpose: charging real fees or
// recognising real revenue needs a fee model (PD-01), tax review (LR-016, LR-045), an accounting review, legal
// sign-off, an ADR and owner acceptance — not a setting.
const ProductionFeesEnabled = false

// EnvTest is the only fee environment in Stage 12.
const EnvTest = "TEST"

// DefaultQuoteTTL is the quote validity when FEES_QUOTE_TTL is not set.
const DefaultQuoteTTL = 15 * time.Minute

// CampaignScope is what fees needs to know about a campaign.
type CampaignScope struct {
	ID           string
	CategoryCode string
	OwnerUserID  string
	// OwnerOrganisationID is set for organisation campaigns.
	OwnerOrganisationID string
	// AcceptsDonations: the campaign is in a state where a donation intent could be created.
	AcceptsDonations bool
}

// Campaigns gives fees campaign facts and owner access (implemented over campaigns.Service in internal/app).
type Campaigns interface {
	FeeScope(ctx context.Context, campaignID string) (CampaignScope, error) // ErrCampaignNotFound if absent
	// CanManage reports whether userID owns or manages the campaign (organiser disclosure).
	CanManage(ctx context.Context, userID, campaignID string) (bool, error)
}

// Routing resolves the provider a payment method would route to (implemented over psp.Service).
type Routing interface {
	RouteMethod(ctx context.Context, method, currency string, amountMinor int64) (providerID string, err error)
	// AvailableMethods lists the methods that would route now for a currency (sandbox or real per process).
	AvailableMethods(ctx context.Context, currency string) ([]string, error)
	// ProviderIsSandbox reports whether a registered provider is the sandbox simulator (ErrNotFound-like error if absent).
	ProviderIsSandbox(ctx context.Context, providerID string) (bool, error)
}

// Identities resolves an actor's linked accounts (implemented over kyc.Service.ActorIdentities).
type Identities interface {
	ActorIdentities(ctx context.Context, actorID string) ([]string, error)
}

// Service is the fee module.
type Service struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Logger     *slog.Logger
	Ledger     *ledger.Service // nil: fee journals and ledger reconciliation unavailable
	Campaigns  Campaigns
	Routing    Routing
	Identities Identities
	// QuoteTTL is FEES_QUOTE_TTL (DefaultQuoteTTL when zero).
	QuoteTTL time.Duration
	// UsableCurrencies returns the currencies that may carry money (money.UsableCurrencies over the app pool).
	UsableCurrencies func(ctx context.Context) ([]string, error)
	// ReconGrace overrides DefaultReconGrace (tests).
	ReconGrace time.Duration
	// ReconWindow overrides DefaultReconWindow, the recency pass (tests).
	ReconWindow int
	// ReconSweep bounds the coverage sweep (zero values: DefaultReconSweep; tests use their own Scope).
	ReconSweep reconsweep.Config
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) quoteTTL() time.Duration {
	if s.QuoteTTL > 0 {
		return s.QuoteTTL
	}
	return DefaultQuoteTTL
}

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Errors (stable codes).
var (
	ErrNotFound          = errs.New(errs.NotFound, "FEE_NOT_FOUND", "No such fee record.")
	ErrCampaignNotFound  = errs.New(errs.NotFound, "CAMPAIGN_NOT_FOUND", "Campaign not found.")
	ErrCategoryDisabled  = errs.New(errs.Unprocessable, "FEE_CATEGORY_NOT_ENABLED", "This fee category cannot be enabled: FundZim never converts currencies, and other fees have no posting rule.")
	ErrPolicyState       = errs.New(errs.Conflict, "FEE_POLICY_STATE", "The fee policy version is not in a state that allows this action.")
	ErrPolicyConflict    = errs.New(errs.Conflict, "FEE_POLICY_CONFLICT", "Another version of this policy is already active from the same start, or a policy with this scope already exists.")
	ErrPolicyAmbiguous   = errs.New(errs.Conflict, "FEE_POLICY_AMBIGUOUS", "More than one fee policy matches with the same precedence.")
	ErrSelfApproval      = errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "You cannot decide a fee change or adjustment you made, or one made by an account linked to yours.")
	ErrStepUp            = errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
	ErrRetroactive       = errs.New(errs.Unprocessable, "FEE_POLICY_RETROACTIVE", "A fee policy version cannot take effect before it is submitted.")
	ErrCurrency          = errs.New(errs.Unprocessable, "CURRENCY_NOT_SUPPORTED", "This currency cannot be used.")
	ErrQuoteExpired      = errs.New(errs.Conflict, "FEE_QUOTE_EXPIRED", "The fee quote has expired. Request a new quote.")
	ErrQuoteStale        = errs.New(errs.Conflict, "FEE_QUOTE_STALE", "The fee quote no longer matches the request or the fee policies in force. Request a new quote.")
	ErrQuoteUsed         = errs.New(errs.Conflict, "FEE_QUOTE_ALREADY_USED", "The fee quote was already used.")
	ErrFeeExceedsAmount  = errs.New(errs.Unprocessable, "FEE_EXCEEDS_AMOUNT", "The fees borne by the campaign would not leave a positive amount.")
	ErrCampaignClosed    = errs.New(errs.Unprocessable, "CAMPAIGN_NOT_ACCEPTING_DONATIONS", "This campaign is not accepting donations.")
	ErrMethodUnavailable = errs.New(errs.Unprocessable, "PAYMENT_METHOD_UNAVAILABLE", "This payment method is not available.")
	ErrAdjustmentState   = errs.New(errs.Conflict, "FEE_ADJUSTMENT_STATE", "The fee adjustment is not pending approval.")
	ErrAdjustmentBounds  = errs.New(errs.Unprocessable, "FEE_ADJUSTMENT_EXCEEDS_ASSESSMENT", "The adjustment would take the assessed fee below zero.")
	ErrUnknownAmount     = errs.New(errs.Unprocessable, "FEE_AMOUNT_UNKNOWN", "This fee's amount is UNKNOWN and cannot be adjusted.")
	ErrSyntheticDisabled = errs.New(errs.Forbidden, "FEE_FIXTURES_DISABLED", "TEST_ONLY fee fixtures are disabled here (TEST ledger and LEDGER_SYNTHETIC_FIXTURES_ENABLED only).")
	ErrLedgerUnavailable = errs.New(errs.Unavailable, "FEE_LEDGER_UNAVAILABLE", "The ledger is not available to this process.")
	ErrProductionFees    = errs.New(errs.Unavailable, "FEE_PRODUCTION_DISABLED", "Production fee charging and revenue recognition are disabled.")
	ErrReconState        = errs.New(errs.Conflict, "FEE_RECONCILIATION_STATE", "The reconciliation item is already resolved.")
)

// calcError maps pure-calculation errors to API errors.
func calcError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrCalcExceeds):
		return ErrFeeExceedsAmount
	case errors.Is(err, ErrCalcAmount):
		return validation("amount_minor", "INVALID_VALUE")
	case errors.Is(err, ErrCalcOverflow):
		return validation("amount_minor", "OUT_OF_RANGE")
	case errors.Is(err, ErrCalcTerms):
		return validation("terms", "INVALID_VALUE")
	}
	return err
}

func validation(field, code string) error {
	e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
	e.Details = []errs.Detail{{Field: field, Code: code}}
	return e
}

// mapDB turns database refusals of fee invariants into stable errors (the Go checks normally fire first).
func mapDB(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch {
	case pg.Code == "23505" && (strings.Contains(pg.ConstraintName, "active_start") || strings.Contains(pg.ConstraintName, "uq_fee_policies_scope") ||
		strings.Contains(pg.ConstraintName, "one_draft") || strings.Contains(pg.ConstraintName, "uq_fee_provider_schedules_active")):
		return ErrPolicyConflict
	case pg.Code == "23514" && strings.Contains(pg.Message, "maker-checker"):
		return ErrSelfApproval
	case pg.Code == "23514" && strings.Contains(pg.Message, "below zero"):
		return ErrAdjustmentBounds
	case pg.Code == "23514" && strings.Contains(pg.ConstraintName, "category_enabled"):
		return ErrCategoryDisabled
	case pg.Code == "23514" && strings.Contains(pg.Message, "illegal fee_"):
		return ErrPolicyState
	case pg.Code == "23503" && strings.Contains(pg.ConstraintName, "campaign"):
		return validation("campaign_id", "NOT_FOUND")
	case pg.Code == "23503" && strings.Contains(pg.ConstraintName, "provider"):
		return validation("provider_id", "NOT_FOUND")
	}
	return err
}

// Actor of a fee event.
type Actor struct {
	Type string // USER | STAFF | SYSTEM
	ID   string
}

func staff(id string) Actor { return Actor{Type: "STAFF", ID: id} }

var systemActor = Actor{Type: "SYSTEM", ID: "fees"}

// event appends to the fee history (app.fee_audit_events) in tx.
func event(ctx context.Context, tx pgx.Tx, aggType, aggID, evType string, a Actor, detail map[string]any, at time.Time) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO app.fee_audit_events (id, aggregate_type, aggregate_id, event_type, actor_type, actor_id, detail,
		correlation_id, occurred_at) VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, ''), $9)`,
		ids.New(), aggType, aggID, evType, a.Type, a.ID, b, correlation(ctx), at)
	return err
}

type corrKey struct{}

// WithCorrelation stores a correlation id for fee events.
func WithCorrelation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, corrKey{}, id)
}

func correlation(ctx context.Context) string {
	v, _ := ctx.Value(corrKey{}).(string)
	return v
}

// Event is one fee history entry (staff views).
type Event struct {
	ID            string          `json:"id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	ActorType     string          `json:"actor_type"`
	ActorID       string          `json:"actor_id"`
	Detail        json.RawMessage `json:"detail"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

// Events returns the history of one aggregate.
func (s *Service) Events(ctx context.Context, aggType, aggID string) ([]Event, error) {
	if !ids.Valid(aggID) {
		return nil, ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, aggregate_type, aggregate_id, event_type, actor_type, actor_id, detail, occurred_at
		FROM app.fee_audit_events WHERE aggregate_type = $1 AND aggregate_id = $2 ORDER BY occurred_at, id`, aggType, aggID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		err := r.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.ActorType, &e.ActorID, &e.Detail, &e.OccurredAt)
		return e, err
	})
}

// linked reports whether actor is (or is linked to) any of others.
func (s *Service) linked(ctx context.Context, actor string, others ...string) (bool, error) {
	ids := []string{actor}
	if s.Identities != nil {
		var err error
		if ids, err = s.Identities.ActorIdentities(ctx, actor); err != nil {
			return false, err
		}
	}
	for _, id := range ids {
		for _, o := range others {
			if o != "" && id == o {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) currencyUsable(ctx context.Context, ccy string) error {
	if len(ccy) != 3 || strings.ToUpper(ccy) != ccy {
		return validation("currency", "INVALID_FORMAT")
	}
	if s.UsableCurrencies == nil {
		return nil
	}
	list, err := s.UsableCurrencies(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c == ccy {
			return nil
		}
	}
	return ErrCurrency
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// lockCurrency serialises quoting against policy changes per currency (transaction advisory lock): quotes and intent
// binding take it SHARED, every policy-version and provider-schedule lifecycle change takes it EXCLUSIVE. A quote is
// therefore either committed before a change (and invalidated by it) or computed after it — an ACTIVE quote can never
// rest on a version that is no longer in force.
func lockCurrency(ctx context.Context, tx pgx.Tx, currency string, exclusive bool) error {
	q := `SELECT pg_advisory_xact_lock_shared(hashtextextended('fees:currency:' || $1, 0))`
	if exclusive {
		q = `SELECT pg_advisory_xact_lock(hashtextextended('fees:currency:' || $1, 0))`
	}
	_, err := tx.Exec(ctx, q, currency)
	return err
}
