package payments

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Overview is GET /admin/payments/overview: counts only, never sums of money (no totals or balances exist).
type Overview struct {
	AttemptsByStatus         map[string]int     `json:"attempts_by_status"`
	UnknownCount             int                `json:"unknown_count"`
	NeedsReconciliationCount int                `json:"needs_reconciliation_count"`
	InboxByStatus            map[string]int     `json:"inbox_by_status"`
	Rejections24h            int                `json:"rejections_24h"`
	OpenRefundRequests       int                `json:"open_refund_requests"`
	Providers                []OverviewProvider `json:"providers"`
	LiveExecution            psp.GateState      `json:"live_execution"`
	SandboxEnabled           bool               `json:"sandbox_enabled"`
}

// OverviewProvider is a provider line on the overview.
type OverviewProvider struct {
	psp.ProviderRef
	Status  string `json:"status"`
	Circuit string `json:"circuit"`
}

// AdminOverview builds the overview.
func (s *Service) AdminOverview(ctx context.Context) (Overview, error) {
	o := Overview{AttemptsByStatus: map[string]int{}, Providers: []OverviewProvider{}, LiveExecution: s.PSP.Gates(ctx),
		SandboxEnabled: s.PSP.Cfg.SandboxEnabled}
	rows, err := s.Pool.Query(ctx, `SELECT status, count(*) FROM app.payment_intents GROUP BY status`)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return o, err
		}
		o.AttemptsByStatus[st] = n
	}
	rows.Close()
	o.UnknownCount = o.AttemptsByStatus[psp.StatusUnknown]
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM app.payment_intents WHERE needs_reconciliation),
		(SELECT count(*) FROM app.refund_requests WHERE status IN ('REQUESTED','PENDING_APPROVAL','ON_HOLD'))`).
		Scan(&o.NeedsReconciliationCount, &o.OpenRefundRequests); err != nil {
		return o, err
	}
	if o.InboxByStatus, o.Rejections24h, err = s.PSP.InboxCounts(ctx); err != nil {
		return o, err
	}
	ps, err := s.PSP.Providers(ctx)
	if err != nil {
		return o, err
	}
	for _, p := range ps {
		o.Providers = append(o.Providers, OverviewProvider{ProviderRef: p.Ref(), Status: p.Status, Circuit: s.PSP.ProviderCircuit(ctx, p.ID)})
	}
	return o, nil
}

// AdminIntent is a donation intent as staff see it.
type AdminIntent struct {
	ID          string         `json:"id"`
	CampaignID  string         `json:"campaign_id"`
	DonorUserID string         `json:"donor_user_id"`
	AmountMinor string         `json:"amount_minor"`
	Currency    string         `json:"currency"`
	Status      string         `json:"status"`
	IsAnonymous bool           `json:"is_anonymous"`
	Simulated   bool           `json:"simulated"`
	ExpiresAt   time.Time      `json:"expires_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Attempts    []AdminAttempt `json:"attempts,omitempty"`
	Events      []Event        `json:"events,omitempty"`
}

func (d donationRow) admin() AdminIntent {
	return AdminIntent{ID: d.ID, CampaignID: d.CampaignID, DonorUserID: d.DonorID, AmountMinor: strconv.FormatInt(d.Amount, 10),
		Currency: d.Currency, Status: d.Status, IsAnonymous: d.Anonymous, Simulated: d.Simulated, ExpiresAt: d.ExpiresAt,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}

var intentStatuses = map[string]bool{DonationOpen: true, DonationPaid: true, DonationExpired: true, DonationCancelled: true}

// AdminIntents lists donation intents newest first.
func (s *Service) AdminIntents(ctx context.Context, status, campaignID, cursor string, limit int) ([]AdminIntent, *string, error) {
	if status != "" && !intentStatuses[status] {
		return nil, nil, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	if campaignID != "" && !ids.Valid(campaignID) {
		return nil, nil, httpx.Validation(errs.Detail{Field: "campaign_id", Code: "INVALID_FORMAT"})
	}
	before, beforeID, err := psp.ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = psp.PageLimit(limit)
	rows, err := s.Pool.Query(ctx, donationSelect+` WHERE ($1 = '' OR status = $1) AND ($2 = '' OR campaign_id = nullif($2, '')::uuid)
		AND ($3::timestamptz IS NULL OR (created_at, id) < ($3, $4::uuid)) ORDER BY created_at DESC, id DESC LIMIT $5`,
		status, campaignID, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (donationRow, error) { return scanDonation(r) })
	if err != nil {
		return nil, nil, err
	}
	out := make([]AdminIntent, 0, len(list))
	for _, d := range list {
		out = append(out, d.admin())
	}
	var next *string
	if len(list) > 0 {
		next = psp.NextCursor(len(list), limit, list[len(list)-1].CreatedAt, list[len(list)-1].ID)
	}
	return out, next, nil
}

// AdminIntentDetail returns one donation intent with attempts and events.
func (s *Service) AdminIntentDetail(ctx context.Context, id string) (AdminIntent, error) {
	if !ids.Valid(id) {
		return AdminIntent{}, ErrIntentNotFound
	}
	d, err := scanDonation(s.Pool.QueryRow(ctx, donationSelect+` WHERE id = $1`, id))
	if err != nil {
		return AdminIntent{}, err
	}
	out := d.admin()
	rows, err := s.Pool.Query(ctx, paySelect+` WHERE donation_intent_id = $1 ORDER BY attempt_seq`, id)
	if err != nil {
		return AdminIntent{}, err
	}
	pays, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (payRow, error) { return scanPay(r) })
	if err != nil {
		return AdminIntent{}, err
	}
	out.Attempts = []AdminAttempt{}
	for _, p := range pays {
		a, err := s.adminAttempt(ctx, p)
		if err != nil {
			return AdminIntent{}, err
		}
		out.Attempts = append(out.Attempts, a)
	}
	out.Events, err = s.events(ctx, `donation_intent_id = $1`, id)
	return out, err
}

// AdminAttempt is a payment as staff see it.
type AdminAttempt struct {
	ID                  string          `json:"id"`
	DonationIntentID    string          `json:"donation_intent_id"`
	CampaignID          string          `json:"campaign_id"`
	AttemptNumber       int             `json:"attempt_number"`
	Method              string          `json:"method"`
	MethodCategory      string          `json:"method_category"`
	Status              string          `json:"status"`
	AmountMinor         string          `json:"amount_minor"`
	Currency            string          `json:"currency"`
	Provider            psp.ProviderRef `json:"provider"`
	Simulated           bool            `json:"simulated"`
	NeedsReconciliation bool            `json:"needs_reconciliation"`
	UnknownSince        *time.Time      `json:"unknown_since"`
	UnknownReason       *string         `json:"unknown_reason"`
	FailureCode         *string         `json:"failure_code"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

func (s *Service) adminAttempt(ctx context.Context, p payRow) (AdminAttempt, error) {
	prov, err := s.PSP.ProviderByID(ctx, p.ProviderID)
	if err != nil {
		return AdminAttempt{}, err
	}
	return AdminAttempt{ID: p.ID, DonationIntentID: p.DonationIntentID, CampaignID: p.CampaignID, AttemptNumber: p.AttemptSeq,
		Method: p.Method, MethodCategory: psp.MethodCategory(p.Method), Status: p.Status, AmountMinor: strconv.FormatInt(p.Amount, 10),
		Currency: p.Currency, Provider: prov.Ref(), Simulated: p.Simulated, NeedsReconciliation: p.NeedsRecon, UnknownSince: p.UnknownSince,
		UnknownReason: p.UnknownReason, FailureCode: p.FailureCode, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}, nil
}

var payStatuses = map[string]bool{}

func init() {
	for st := range Rank {
		payStatuses[st] = true
	}
}

// AdminAttempts lists payments newest first (unknown queue: oldest first).
func (s *Service) AdminAttempts(ctx context.Context, status, method string, needsRecon bool, cursor string, limit int) ([]AdminAttempt, *string, error) {
	if status != "" && !payStatuses[status] {
		return nil, nil, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	if method != "" && !psp.ValidMethod(method) {
		return nil, nil, httpx.Validation(errs.Detail{Field: "method", Code: "INVALID_VALUE"})
	}
	before, beforeID, err := psp.ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = psp.PageLimit(limit)
	rows, err := s.Pool.Query(ctx, paySelect+` WHERE ($1 = '' OR status = $1) AND ($2 = '' OR method = $2) AND (NOT $3 OR needs_reconciliation)
		AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5::uuid)) ORDER BY created_at DESC, id DESC LIMIT $6`,
		status, method, needsRecon, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	return s.adminPage(ctx, rows, limit, func(p payRow) (time.Time, string) { return p.CreatedAt, p.ID })
}

// UnknownQueue lists UNKNOWN payments, oldest unknown first (keyset on unknown_since ascending).
func (s *Service) UnknownQueue(ctx context.Context, cursor string, limit int) ([]AdminAttempt, *string, error) {
	var after *time.Time
	afterID := "00000000-0000-0000-0000-000000000000"
	if cursor != "" {
		t, id, err := psp.ParseCursor(cursor)
		if err != nil {
			return nil, nil, err
		}
		after, afterID = t, id
	}
	limit = psp.PageLimit(limit)
	rows, err := s.Pool.Query(ctx, paySelect+` WHERE status = 'UNKNOWN' AND ($1::timestamptz IS NULL OR (unknown_since, id) > ($1, $2::uuid))
		ORDER BY unknown_since, id LIMIT $3`, after, afterID, limit)
	if err != nil {
		return nil, nil, err
	}
	return s.adminPage(ctx, rows, limit, func(p payRow) (time.Time, string) { return *p.UnknownSince, p.ID })
}

func (s *Service) adminPage(ctx context.Context, rows pgx.Rows, limit int, key func(payRow) (time.Time, string)) ([]AdminAttempt, *string, error) {
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (payRow, error) { return scanPay(r) })
	if err != nil {
		return nil, nil, err
	}
	out := make([]AdminAttempt, 0, len(list))
	for _, p := range list {
		a, err := s.adminAttempt(ctx, p)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, a)
	}
	var next *string
	if len(list) > 0 {
		t, id := key(list[len(list)-1])
		next = psp.NextCursor(len(list), limit, t, id)
	}
	return out, next, nil
}

// ProviderCall is a payment_attempts row.
type ProviderCall struct {
	ID                        string     `json:"id"`
	AttemptNo                 int        `json:"attempt_no"`
	Operation                 string     `json:"operation"`
	Classification            string     `json:"classification"`
	ErrorClass                *string    `json:"error_class"`
	UnknownReason             *string    `json:"unknown_reason"`
	HTTPStatus                *int       `json:"http_status"`
	ProviderStatusRaw         *string    `json:"provider_status_raw"`
	ProviderReferenceReturned *string    `json:"provider_reference_returned"`
	LatencyMS                 *int       `json:"latency_ms"`
	CorrelationID             string     `json:"correlation_id"`
	StartedAt                 time.Time  `json:"started_at"`
	DeadlineAt                time.Time  `json:"deadline_at"`
	CompletedAt               *time.Time `json:"completed_at"`
}

// Transaction is the provider transaction (raw + mapped).
type Transaction struct {
	ProviderTransactionRef string     `json:"provider_transaction_ref"`
	RawStatus              string     `json:"raw_status"`
	RawStatusAt            *time.Time `json:"raw_status_at"`
	MappedStatus           string     `json:"mapped_status"`
	MappingVersion         string     `json:"mapping_version"`
	LastSource             string     `json:"last_source"`
	ReportedAmountMinor    *string    `json:"reported_amount_minor"`
	ReportedCurrency       *string    `json:"reported_currency"`
	PayerInstrumentMasked  *string    `json:"payer_instrument_masked"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// Reference is a provider reference.
type Reference struct {
	RefKind    string    `json:"ref_kind"`
	Reference  string    `json:"reference"`
	Source     string    `json:"source"`
	RecordedAt time.Time `json:"recorded_at"`
}

// SessionView is a payment session (never its token).
type SessionView struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	RedirectURL *string    `json:"redirect_url"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ReturnedAt  *time.Time `json:"returned_at"`
}

// Event is a payment_events row.
type Event struct {
	ID               string     `json:"id"`
	SubjectType      string     `json:"subject_type"`
	FromStatus       *string    `json:"from_status"`
	ToStatus         string     `json:"to_status"`
	Applied          bool       `json:"applied"`
	NotAppliedReason *string    `json:"not_applied_reason"`
	TransitionCode   *string    `json:"transition_code"`
	Source           string     `json:"source"`
	ProviderEventID  *string    `json:"provider_event_id"`
	RawStatus        *string    `json:"raw_status"`
	ActorType        string     `json:"actor_type"`
	Reason           *string    `json:"reason"`
	CorrelationID    *string    `json:"correlation_id"`
	OccurredAt       *time.Time `json:"occurred_at"`
	RecordedAt       time.Time  `json:"recorded_at"`
}

// Recon is a reconciliation reference.
type Recon struct {
	ID                  string     `json:"id"`
	PaymentID           string     `json:"payment_id"`
	Reason              string     `json:"reason"`
	Status              string     `json:"status"`
	ExpectedAmountMinor string     `json:"expected_amount_minor"`
	ExpectedCurrency    string     `json:"expected_currency"`
	ReportedAmountMinor *string    `json:"reported_amount_minor"`
	ReportedCurrency    *string    `json:"reported_currency"`
	ProviderStatusRaw   *string    `json:"provider_status_raw"`
	ProviderReference   *string    `json:"provider_reference"`
	EvidenceAt          time.Time  `json:"evidence_at"`
	CreatedAt           time.Time  `json:"created_at"`
	ResolvedAt          *time.Time `json:"resolved_at"`
	ResolutionNote      *string    `json:"resolution_note"`
}

// AttemptDetail is GET /admin/payments/attempts/{payment_id}.
type AttemptDetail struct {
	AdminAttempt
	ProviderPaymentRef      *string         `json:"provider_payment_ref"`
	LastAuthoritativeSource *string         `json:"last_authoritative_source"`
	PollAttempts            int             `json:"poll_attempts"`
	NextPollAt              *time.Time      `json:"next_poll_at"`
	PollHorizonAt           *time.Time      `json:"poll_horizon_at"`
	ProviderCalls           []ProviderCall  `json:"provider_calls"`
	Transaction             *Transaction    `json:"transaction"`
	References              []Reference     `json:"references"`
	Session                 *SessionView    `json:"session"`
	Webhooks                []psp.InboxItem `json:"webhooks"`
	Events                  []Event         `json:"events"`
	Reconciliation          []Recon         `json:"reconciliation"`
	RefundRequests          []Refund        `json:"refund_requests"`
}

// AdminAttemptDetail builds the attempt detail.
func (s *Service) AdminAttemptDetail(ctx context.Context, id string) (AttemptDetail, error) {
	if !ids.Valid(id) {
		return AttemptDetail{}, ErrPaymentNotFound
	}
	p, err := scanPay(s.Pool.QueryRow(ctx, paySelect+` WHERE id = $1`, id))
	if err != nil {
		return AttemptDetail{}, err
	}
	a, err := s.adminAttempt(ctx, p)
	if err != nil {
		return AttemptDetail{}, err
	}
	d := AttemptDetail{AdminAttempt: a, ProviderPaymentRef: p.ProviderPaymentRef, LastAuthoritativeSource: p.LastSource,
		PollAttempts: p.PollAttempts, NextPollAt: p.NextPollAt, PollHorizonAt: p.PollHorizonAt}
	rows, err := s.Pool.Query(ctx, `SELECT id, attempt_no, operation, classification, error_class, unknown_reason, http_status, provider_status_raw,
		provider_reference_returned, latency_ms, correlation_id, started_at, deadline_at, completed_at FROM app.payment_attempts
		WHERE payment_intent_id = $1 ORDER BY attempt_no`, id)
	if err != nil {
		return d, err
	}
	if d.ProviderCalls, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProviderCall, error) {
		var c ProviderCall
		var http *int16
		err := r.Scan(&c.ID, &c.AttemptNo, &c.Operation, &c.Classification, &c.ErrorClass, &c.UnknownReason, &http, &c.ProviderStatusRaw,
			&c.ProviderReferenceReturned, &c.LatencyMS, &c.CorrelationID, &c.StartedAt, &c.DeadlineAt, &c.CompletedAt)
		if http != nil {
			h := int(*http)
			c.HTTPStatus = &h
		}
		return c, err
	}); err != nil {
		return d, err
	}
	var t Transaction
	var amt *int64
	err = s.Pool.QueryRow(ctx, `SELECT provider_transaction_ref, raw_status, raw_status_at, mapped_status, mapping_version, last_source,
		reported_amount_minor, reported_currency, payer_instrument_masked, updated_at FROM app.payment_transactions WHERE payment_intent_id = $1`, id).
		Scan(&t.ProviderTransactionRef, &t.RawStatus, &t.RawStatusAt, &t.MappedStatus, &t.MappingVersion, &t.LastSource, &amt,
			&t.ReportedCurrency, &t.PayerInstrumentMasked, &t.UpdatedAt)
	switch {
	case err == nil:
		if amt != nil {
			v := strconv.FormatInt(*amt, 10)
			t.ReportedAmountMinor = &v
		}
		d.Transaction = &t
	case !errors.Is(err, pgx.ErrNoRows):
		return d, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT ref_kind, reference, source, recorded_at FROM app.payment_provider_references
		WHERE payment_intent_id = $1 ORDER BY recorded_at`, id)
	if err != nil {
		return d, err
	}
	if d.References, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Reference]); err != nil {
		return d, err
	}
	var sv SessionView
	err = s.Pool.QueryRow(ctx, `SELECT id, kind, status, redirect_url, expires_at, returned_at FROM app.payment_sessions WHERE payment_intent_id = $1`, id).
		Scan(&sv.ID, &sv.Kind, &sv.Status, &sv.RedirectURL, &sv.ExpiresAt, &sv.ReturnedAt)
	switch {
	case err == nil:
		d.Session = &sv
	case !errors.Is(err, pgx.ErrNoRows):
		return d, err
	}
	if d.Webhooks, err = s.PSP.InboxForReference(ctx, id); err != nil {
		return d, err
	}
	if d.Events, err = s.events(ctx, `payment_intent_id = $1`, id); err != nil {
		return d, err
	}
	if d.Reconciliation, _, err = s.reconList(ctx, "", id, "", 100); err != nil {
		return d, err
	}
	rows, err = s.Pool.Query(ctx, refundSelect+` WHERE payment_intent_id = $1 ORDER BY requested_at`, id)
	if err != nil {
		return d, err
	}
	d.RefundRequests, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Refund, error) { return scanRefund(r) })
	return d, err
}

func (s *Service) events(ctx context.Context, where, id string) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, subject_type, from_status, to_status, applied, not_applied_reason, transition_code, source,
		provider_event_id, raw_status, actor_type, reason, correlation_id, occurred_at, recorded_at FROM app.payment_events WHERE `+where+`
		ORDER BY recorded_at, id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Event])
}

var reconStatuses = map[string]bool{"OPEN": true, "AWAITING_SETTLEMENT_DATA": true, "RESOLVED_BY_PROVIDER_STATUS": true}

// ReconList lists reconciliation references newest first.
func (s *Service) ReconList(ctx context.Context, status, cursor string, limit int) ([]Recon, *string, error) {
	return s.reconList(ctx, status, "", cursor, limit)
}

func (s *Service) reconList(ctx context.Context, status, paymentID, cursor string, limit int) ([]Recon, *string, error) {
	if status != "" && !reconStatuses[status] {
		return nil, nil, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	before, beforeID, err := psp.ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = psp.PageLimit(limit)
	rows, err := s.Pool.Query(ctx, `SELECT id, payment_intent_id, reason, status, expected_amount_minor, expected_currency, reported_amount_minor,
		reported_currency, provider_status_raw, provider_reference, evidence_at, created_at, resolved_at, resolution_note
		FROM app.payment_reconciliation_references WHERE ($1 = '' OR status = $1) AND ($2 = '' OR payment_intent_id = nullif($2, '')::uuid)
		AND ($3::timestamptz IS NULL OR (created_at, id) < ($3, $4::uuid)) ORDER BY created_at DESC, id DESC LIMIT $5`,
		status, paymentID, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Recon, error) {
		var x Recon
		var exp int64
		var rep *int64
		err := r.Scan(&x.ID, &x.PaymentID, &x.Reason, &x.Status, &exp, &x.ExpectedCurrency, &rep, &x.ReportedCurrency, &x.ProviderStatusRaw,
			&x.ProviderReference, &x.EvidenceAt, &x.CreatedAt, &x.ResolvedAt, &x.ResolutionNote)
		x.ExpectedAmountMinor = strconv.FormatInt(exp, 10)
		if rep != nil {
			v := strconv.FormatInt(*rep, 10)
			x.ReportedAmountMinor = &v
		}
		return x, err
	})
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(list) > 0 {
		next = psp.NextCursor(len(list), limit, list[len(list)-1].CreatedAt, list[len(list)-1].ID)
	}
	return list, next, nil
}
