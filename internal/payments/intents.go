package payments

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/money"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Limit keys read from risk.limits (approved rows only; none is seeded and none is invented).
const (
	LimitMinKey = "payments.donation.min_amount"
	LimitMaxKey = "payments.donation.max_amount"
)

// DonationIntent is the donor view of a donation intent.
type DonationIntent struct {
	ID          string `json:"id"`
	CampaignID  string `json:"campaign_id"`
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	IsAnonymous bool   `json:"is_anonymous"`
	Simulated   bool   `json:"simulated"`
	// ExecutionEnvironment and TestPayment make a test payment impossible to mistake for a real donation.
	ExecutionEnvironment string           `json:"execution_environment"`
	TestPayment          bool             `json:"test_payment"`
	ExpiresAt            time.Time        `json:"expires_at"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
	Attempts             []PaymentAttempt `json:"attempts,omitempty"`
}

type donationRow struct {
	ID, CampaignID, DonorID, Currency, Status, Correlation, IdemKey string
	Amount                                                          int64
	Anonymous, Simulated                                            bool
	ExecEnv                                                         string
	ExpiresAt, CreatedAt, UpdatedAt                                 time.Time
}

const donationSelect = `SELECT id, campaign_id, donor_user_id, currency, status, correlation_id, idempotency_key::text, amount_minor,
	is_anonymous, is_simulated, expires_at, created_at, updated_at, execution_environment FROM app.donation_intents`

func scanDonation(r pgx.Row) (donationRow, error) {
	var d donationRow
	err := r.Scan(&d.ID, &d.CampaignID, &d.DonorID, &d.Currency, &d.Status, &d.Correlation, &d.IdemKey, &d.Amount, &d.Anonymous,
		&d.Simulated, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt, &d.ExecEnv)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrIntentNotFound
	}
	return d, err
}

func (d donationRow) view() DonationIntent {
	return DonationIntent{ID: d.ID, CampaignID: d.CampaignID, AmountMinor: strconv.FormatInt(d.Amount, 10), Currency: d.Currency,
		Status: d.Status, IsAnonymous: d.Anonymous, Simulated: d.Simulated, ExecutionEnvironment: d.ExecEnv,
		TestPayment: d.ExecEnv != psp.ExecProduction, ExpiresAt: d.ExpiresAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}

// CreateIntentInput is POST /donation-intents.
type CreateIntentInput struct {
	CampaignID  string `json:"campaign_id"`
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
	IsAnonymous bool   `json:"is_anonymous"`
}

var amountRE = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// ExecEnv is this process's payment execution environment (Stage 9); DISABLED without a provider layer.
func (s *Service) ExecEnv() string {
	if s.PSP == nil {
		return psp.ExecDisabled
	}
	return s.PSP.Cfg.ExecEnv
}

// Simulated reports whether new donation intents are simulations (LOCAL_SIMULATOR). PROVIDER_SANDBOX intents are
// not simulated but are still test payments.
func (s *Service) Simulated() bool { return s.ExecEnv() == psp.ExecLocalSimulator }

// CreateIntent validates everything that can be validated before any provider is involved (ADR-039 §3:
// "VALIDATED" is not a state — a failed check stores nothing), then stores the intent with its first event.
func (s *Service) CreateIntent(ctx context.Context, donorID, idemKey, corr string, in CreateIntentInput) (DonationIntent, error) {
	var det []errs.Detail
	if !ids.Valid(in.CampaignID) {
		det = append(det, errs.Detail{Field: "campaign_id", Code: "INVALID_FORMAT"})
	}
	if !amountRE.MatchString(in.AmountMinor) {
		det = append(det, errs.Detail{Field: "amount_minor", Code: "INVALID_FORMAT"})
	}
	if len(in.Currency) != 3 || strings.ToUpper(in.Currency) != in.Currency {
		det = append(det, errs.Detail{Field: "currency", Code: "INVALID_FORMAT"})
	}
	if len(det) > 0 {
		return DonationIntent{}, httpx.Validation(det...)
	}
	amount, _ := strconv.ParseInt(in.AmountMinor, 10, 64)
	if !ids.Valid(idemKey) {
		return DonationIntent{}, errs.New(errs.Invalid, "IDEMPOTENCY_KEY_REQUIRED", "An Idempotency-Key header is required for this request.")
	}
	// replay backstop when the idempotency record has expired
	if d, err := scanDonation(s.Pool.QueryRow(ctx, donationSelect+` WHERE donor_user_id = $1 AND idempotency_key = $2`, donorID, idemKey)); err == nil {
		if d.CampaignID != in.CampaignID || d.Amount != amount || d.Currency != in.Currency {
			return DonationIntent{}, errs.New(errs.Conflict, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was already used for a different request.")
		}
		return d.view(), nil
	} else if !errors.Is(err, ErrIntentNotFound) {
		return DonationIntent{}, err
	}
	if err := s.checkDonor(ctx, donorID); err != nil {
		return DonationIntent{}, err
	}
	target, err := s.checkCampaign(ctx, in.CampaignID)
	if err != nil {
		return DonationIntent{}, err
	}
	if err := s.checkCurrency(ctx, target, in.Currency); err != nil {
		return DonationIntent{}, err
	}
	if err := s.checkLimits(ctx, amount, in.Currency); err != nil {
		return DonationIntent{}, err
	}
	switch s.ExecEnv() {
	case psp.ExecLocalSimulator, psp.ExecProviderSandbox:
	case psp.ExecProduction:
		// unreachable in Stage 9 (startup refuses PRODUCTION; the DB refuses PRODUCTION intents) — kept fail closed
		if live, err := s.PSP.LiveExecution(ctx); err != nil {
			return DonationIntent{}, err
		} else if !live {
			return DonationIntent{}, psp.ErrPaymentsUnavailable
		}
	default:
		return DonationIntent{}, psp.ErrPaymentsUnavailable
	}
	// at least one method must route for this amount and currency, or the intent would be unpayable
	if opts, err := s.PSP.AvailableMethods(ctx, in.Currency, s.Simulated()); err != nil {
		return DonationIntent{}, err
	} else if len(opts) == 0 {
		return DonationIntent{}, psp.ErrMethodUnavailable
	}
	now := s.now()
	d := donationRow{ID: ids.New(), CampaignID: in.CampaignID, DonorID: donorID, Currency: in.Currency, Status: DonationOpen,
		Correlation: corrOr(corr), IdemKey: idemKey, Amount: amount, Anonymous: in.IsAnonymous, Simulated: s.Simulated(), ExecEnv: s.ExecEnv(),
		ExpiresAt: now.Add(DonationIntentTTL), CreatedAt: now, UpdatedAt: now}
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO app.donation_intents (id, campaign_id, donor_user_id, currency, amount_minor, is_anonymous,
			is_simulated, status, expires_at, correlation_id, idempotency_key, execution_environment)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'OPEN', $8, $9, $10, $11)
			ON CONFLICT (donor_user_id, idempotency_key) DO NOTHING`,
			d.ID, d.CampaignID, donorID, d.Currency, d.Amount, d.Anonymous, d.Simulated, d.ExpiresAt, d.Correlation, idemKey, d.ExecEnv)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errConcurrentReplay
		}
		if err := s.insertEvent(ctx, tx, eventRow{subject: "DONATION_INTENT", donationID: d.ID, to: DonationOpen, applied: true,
			obs: Obs{Source: SrcDonor, ActorType: "DONOR", ActorID: donorID, Correlation: d.Correlation}}); err != nil {
			return err
		}
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "donation_intent", AggregateID: d.ID,
			EventType: "payments.donation_intent_created", Payload: map[string]any{"donation_intent_id": d.ID, "campaign_id": d.CampaignID,
				"simulated": d.Simulated, "execution_environment": d.ExecEnv}, CorrelationID: d.Correlation, OccurredAt: now}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payments.donation_intent.created", ActorType: "user", ActorID: donorID,
			TargetType: "donation_intent", TargetID: d.ID, Metadata: map[string]any{"campaign_id": d.CampaignID, "simulated": d.Simulated,
				"execution_environment": d.ExecEnv}})
	})
	if errors.Is(err, errConcurrentReplay) {
		existing, err := scanDonation(s.Pool.QueryRow(ctx, donationSelect+` WHERE donor_user_id = $1 AND idempotency_key = $2`, donorID, idemKey))
		if err != nil {
			return DonationIntent{}, err
		}
		return existing.view(), nil
	}
	if err != nil {
		return DonationIntent{}, err
	}
	s.m.intents.WithLabelValues(strconv.FormatBool(d.Simulated)).Inc()
	return d.view(), nil
}

var errConcurrentReplay = errors.New("payments: concurrent request with the same idempotency key")

func corrOr(c string) string {
	if c == "" || len(c) > 128 {
		return ids.New()
	}
	return c
}

func (s *Service) checkDonor(ctx context.Context, donorID string) error {
	if s.Restrictions == nil {
		return errors.New("payments: donor restrictions reader not configured")
	}
	restricted, err := s.Restrictions.Restricted(ctx, donorID)
	if err != nil {
		return err
	}
	if restricted {
		return ErrDonationNotAvailable
	}
	return nil
}

func (s *Service) checkCampaign(ctx context.Context, campaignID string) (campaigns.DonationTarget, error) {
	t, r, err := s.Campaigns.EvaluateDonation(ctx, campaignID)
	if errors.Is(err, campaigns.ErrNotFound) {
		return t, ErrCampaignNotAccepting
	}
	if err != nil {
		return t, err
	}
	if !r.Allowed {
		return t, ErrCampaignNotAccepting
	}
	return t, nil
}

// checkCurrency: the currency must be usable (enabled AND verified minor units: ZWG is refused, LR-043) and the
// campaign's goal currency (no FX, ever).
func (s *Service) checkCurrency(ctx context.Context, t campaigns.DonationTarget, ccy string) error {
	usable, err := money.UsableCurrencies(ctx, s.Pool)
	if err != nil {
		return err
	}
	ok := false
	for _, c := range usable {
		if c == ccy {
			ok = true
		}
	}
	if !ok {
		return ErrCurrencyNotAvailable
	}
	if t.GoalCurrency != ccy {
		return ErrCurrencyNotAccepted
	}
	return nil
}

// checkLimits applies technical limits (code) and any approved REGULATORY / INTERNAL_RISK money limits
// (risk.limits). Provider limits are applied by routing. No threshold is invented: an absent limit is absent.
func (s *Service) checkLimits(ctx context.Context, amount int64, ccy string) error {
	if amount < TechMinAmountMinor || amount > TechMaxAmountMinor {
		return ErrAmountOutOfRange
	}
	if s.Limits == nil {
		return nil
	}
	now := s.now()
	mins, err := s.Limits.MoneyLimits(ctx, LimitMinKey, ccy, now)
	if err != nil {
		return err
	}
	for _, l := range mins {
		if l.LimitType != "PROVIDER" && amount < l.ValueMinor {
			return ErrAmountOutOfRange
		}
	}
	maxs, err := s.Limits.MoneyLimits(ctx, LimitMaxKey, ccy, now)
	if err != nil {
		return err
	}
	for _, l := range maxs {
		if l.LimitType != "PROVIDER" && amount > l.ValueMinor {
			return ErrAmountOutOfRange
		}
	}
	return nil
}

// GetIntent returns the donor's own donation intent with its attempts (404 for anyone else).
func (s *Service) GetIntent(ctx context.Context, donorID, id string) (DonationIntent, error) {
	d, err := s.ownIntent(ctx, s.Pool, donorID, id, false)
	if err != nil {
		return DonationIntent{}, err
	}
	v := d.view()
	atts, err := s.attemptsOf(ctx, d.ID)
	if err != nil {
		return DonationIntent{}, err
	}
	v.Attempts = atts
	return v, nil
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) ownIntent(ctx context.Context, q rowQuerier, donorID, id string, lock bool) (donationRow, error) {
	if !ids.Valid(id) {
		return donationRow{}, ErrIntentNotFound
	}
	sql := donationSelect + ` WHERE id = $1 AND donor_user_id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanDonation(q.QueryRow(ctx, sql, id, donorID))
}

// ListIntents lists the donor's donation intents, newest first.
func (s *Service) ListIntents(ctx context.Context, donorID, cursor string, limit int) ([]DonationIntent, *string, error) {
	before, beforeID, err := psp.ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = psp.PageLimit(limit)
	rows, err := s.Pool.Query(ctx, donationSelect+` WHERE donor_user_id = $1 AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $4`, donorID, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (donationRow, error) { return scanDonation(r) })
	if err != nil {
		return nil, nil, err
	}
	out := make([]DonationIntent, 0, len(list))
	for _, d := range list {
		out = append(out, d.view())
	}
	var next *string
	if len(list) > 0 {
		next = psp.NextCursor(len(list), limit, list[len(list)-1].CreatedAt, list[len(list)-1].ID)
	}
	return out, next, nil
}

// CancelIntent cancels an OPEN intent with no live attempt (donor).
func (s *Service) CancelIntent(ctx context.Context, donorID, id string) (DonationIntent, error) {
	var out DonationIntent
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.ownIntent(ctx, tx, donorID, id, true)
		if err != nil {
			return err
		}
		if d.Status != DonationOpen {
			return ErrIntentNotOpen
		}
		if err := s.blockingAttempt(ctx, tx, d.ID); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, eventRow{subject: "DONATION_INTENT", donationID: d.ID, from: DonationOpen, to: DonationCancelled,
			applied: true, obs: Obs{Source: SrcDonor, ActorType: "DONOR", ActorID: donorID, Correlation: d.Correlation}}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.donation_intents SET status = 'CANCELLED', version = version + 1 WHERE id = $1`, d.ID); err != nil {
			return err
		}
		d.Status = DonationCancelled
		out = d.view()
		return audit.Record(ctx, tx, audit.Event{Action: "payments.donation_intent.cancelled", ActorType: "user", ActorID: donorID,
			TargetType: "donation_intent", TargetID: d.ID})
	})
	return out, err
}

// blockingAttempt refuses a new attempt or a cancellation while a payment is unknown, live or succeeded.
func (s *Service) blockingAttempt(ctx context.Context, tx pgx.Tx, donationID string) error {
	rows, err := tx.Query(ctx, `SELECT status FROM app.payment_intents WHERE donation_intent_id = $1`, donationID)
	if err != nil {
		return err
	}
	sts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var unknown, live bool
	for _, st := range sts {
		switch {
		case paid(st):
			return ErrAlreadyPaid
		case st == psp.StatusUnknown:
			unknown = true
		case inProgress(st):
			live = true
		}
	}
	if unknown {
		return ErrOutcomeUnknown
	}
	if live {
		return ErrInProgress
	}
	return nil
}

// cleanReason validates a staff reason/note: 3–2000 characters (the refund reason_text CHECK), no control characters.
func cleanReason(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 3 || len([]rune(s)) > 2000 {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

func invalidReason(field string) error {
	return httpx.Validation(errs.Detail{Field: field, Code: "INVALID_LENGTH"})
}
