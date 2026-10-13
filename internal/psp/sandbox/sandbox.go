// Package sandbox is the FundZim sandbox SIMULATOR (payment-provider-interfaces §11, ADR-039 §6). It never
// contacts real payment infrastructure: its "provider side" is the psp-owned tables app.sandbox_provider_payments
// and app.sandbox_scheduled_webhooks, and its webhooks are signed with test secrets and POSTed to FundZim's own
// ingestion endpoint, through the same verification path a real provider uses. It refuses to exist in production
// (configuration refuses PAYMENTS_SANDBOX_ENABLED there; the database refuses a PRODUCTION sandbox account).
//
// Every simulated payment, webhook and record is marked simulated (livemode=false, simulated=true in payloads;
// is_simulated / is_sandbox in the database).
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Code is the sandbox provider code.
const Code = "sandbox"

// MappingVersion is the version of the sandbox status mapping table.
const MappingVersion = "sandbox-map-v1"

// RedirectHost is the only host the simulator sends donors to (it does not resolve: there is no live checkout).
const RedirectHost = "sandbox.fundzim.invalid"

// NotFoundFinality is the sandbox contract's documented window after which "not found" is final (PCR-010
// analogue for the simulator).
const NotFoundFinality = 60 * time.Second

// mapping is the versioned raw → canonical table. Anything else is unmapped (UNKNOWN, alert).
var mapping = map[string]string{
	"pending":            psp.StatusPending,
	"action_required":    psp.StatusRequiresAction,
	"paid":               psp.StatusSucceeded,
	"failed":             psp.StatusFailed,
	"declined":           psp.StatusFailed,
	"expired":            psp.StatusExpired,
	"cancelled":          psp.StatusCancelled,
	"partially_refunded": psp.StatusPartiallyRefunded,
	"refunded":           psp.StatusRefunded,
}

// Adapter is the sandbox simulator adapter.
type Adapter struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Logger     *slog.Logger
	Secrets    psp.SecretResolver
	ProviderID string
	// WebhookURL is FundZim's own ingestion endpoint (SANDBOX_WEBHOOK_URL), used by the delivery worker.
	WebhookURL string
	HTTP       *http.Client
}

// New builds the adapter.
func New(pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger, secrets psp.SecretResolver, providerID, webhookURL string) *Adapter {
	if clk == nil {
		clk = clock.System
	}
	return &Adapter{Pool: pool, Clock: clk, Logger: logger, Secrets: secrets, ProviderID: providerID, WebhookURL: webhookURL,
		HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// Code implements psp.Adapter.
func (a *Adapter) Code() string { return Code }

// Contract implements psp.ContractDescriber: the simulator's behaviour is FundZim's own (provider-interface §6).
func (a *Adapter) Contract() psp.ContractInfo {
	return psp.ContractInfo{Basis: psp.ContractSimulated, Sources: []string{"docs/stage-8/provider-interface.md §6"}}
}

// RedirectHosts implements psp.Adapter.
func (a *Adapter) RedirectHosts() []string { return []string{RedirectHost} }

// Limits implements psp.Adapter: the sandbox contract's limits (USD 1.00 – 10 000.00). They are simulator facts,
// not statements about any real rail. An unknown method has no limits (fail closed; found by the Stage 9
// conformance suite).
func (a *Adapter) Limits(method, currency string) (psp.Limits, bool) {
	if currency != "USD" || !psp.ValidMethod(method) {
		return psp.Limits{}, false
	}
	return psp.Limits{MinMinor: 100, MaxMinor: 1_000_000}, true
}

// MapPaymentStatus implements psp.Adapter.
func (a *Adapter) MapPaymentStatus(raw string) (string, bool, string) {
	if c, ok := mapping[raw]; ok {
		return c, true, MappingVersion
	}
	return psp.StatusUnknown, false, MappingVersion
}

func (a *Adapter) now() time.Time { return a.Clock.Now().UTC() }

func randomRef(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func (a *Adapter) observe(raw string) psp.Observation {
	c, mapped, v := a.MapPaymentStatus(raw)
	return psp.Observation{Class: psp.ClassAccepted, RawStatus: raw, Canonical: c, Mapped: mapped, MappingVersion: v, HTTPStatus: 200}
}

// CreatePayment implements psp.PaymentProvider. Scenario facts decide what the simulated provider does; the
// classification follows payment-provider-interfaces §5.
func (a *Adapter) CreatePayment(ctx context.Context, m psp.CallMeta, r psp.CreatePaymentRequest) psp.Observation {
	start := a.now()
	sc := ScenarioFor(r.AmountMinor, r.Metadata["sandbox_scenario"])
	finish := func(o psp.Observation) psp.Observation { o.Latency = a.now().Sub(start); return o }
	// idempotent create: the same reference returns the existing simulated payment, never a second one
	if ex, err := a.load(ctx, r.PaymentID); err == nil {
		o := a.observe(ex.Raw)
		o.ProviderRef = ex.Ref
		return finish(o)
	}
	switch sc.Name {
	case ScProviderUnavailable:
		// connection refused before the request was written: nothing reached the provider
		return finish(psp.Observation{Class: psp.ClassRetryableBeforeSend, HTTPStatus: 0})
	case ScTimeoutNotCreated:
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout})
	case ScDecline:
		ref := randomRef("sbx_pay_")
		if err := a.record(ctx, ref, r, sc, "declined", nil); err != nil {
			return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownMalformed})
		}
		o := a.observe("declined")
		o.Class, o.ProviderRef, o.FailureCode, o.HTTPStatus = psp.ClassDefiniteFailure, ref, "DECLINED", 402
		return finish(o)
	}
	ref := randomRef("sbx_pay_")
	initial := "pending"
	if sc.Handset {
		initial = "action_required"
	}
	if r.Method == psp.MethodCard {
		initial = "action_required"
	}
	if err := a.record(ctx, ref, r, sc, initial, sc.Webhooks); err != nil {
		a.Logger.Error("sandbox: record simulated payment failed", "err", err)
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownMalformed})
	}
	switch sc.Name {
	case ScTimeoutThenSuccess, ScTimeoutThenFailure:
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout})
	case ScProvider5xx:
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownHTTP5xx, HTTPStatus: 502})
	}
	o := a.observe(initial)
	o.ProviderRef = ref
	switch {
	case r.Method == psp.MethodCard:
		exp := a.now().Add(15 * time.Minute)
		o.Next = psp.NextAction{Type: psp.NextRedirect, RedirectURL: "https://" + RedirectHost + "/checkout/" + ref, ExpiresAt: &exp}
	case sc.Handset:
		exp := a.now().Add(10 * time.Minute)
		o.Next = psp.NextAction{Type: psp.NextAwaitHandset, ExpiresAt: &exp}
	default:
		o.Next = psp.NextAction{Type: psp.NextNone}
	}
	return finish(o)
}

// record stores the simulated provider-side payment and schedules its webhooks, in one transaction.
func (a *Adapter) record(ctx context.Context, ref string, r psp.CreatePaymentRequest, sc Scenario, status string, hooks []Hook) error {
	amount := r.AmountMinor
	if sc.Name == ScAmountMismatch {
		amount++ // the simulated provider reports a different amount (anomaly: never applied)
	}
	return db.WithTx(ctx, a.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO app.sandbox_provider_payments (id, provider_id, provider_payment_ref, our_reference, method,
			currency, amount_minor, scenario, raw_status, return_url) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, ''))`,
			id, a.ProviderID, ref, r.PaymentID, r.Method, r.Currency, amount, sc.Name, status, r.ReturnURL); err != nil {
			return err
		}
		now := a.now()
		for _, h := range hooks {
			evt := randomRef("sbx_evt_")
			for i := 0; i < max(1, h.Copies); i++ {
				if err := scheduleTx(ctx, tx, id, evt, h, amount, r.Currency, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func scheduleTx(ctx context.Context, tx pgx.Tx, sandboxPaymentID, eventID string, h Hook, amount int64, ccy string, now time.Time) error {
	sign := h.SignWith
	if sign == "" {
		sign = "current"
	}
	if h.Amount > 0 {
		amount = h.Amount
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.sandbox_scheduled_webhooks (id, sandbox_payment_id, provider_event_id, event_type, raw_status,
		amount_minor, currency, sign_with, timestamp_offset_seconds, due_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		ids.New(), sandboxPaymentID, eventID, "payment."+h.Status, h.Status, amount, ccy, sign, h.TimestampOffset, now.Add(h.After))
	return err
}

// state is the simulated provider-side payment.
type state struct {
	ID, Ref, Our, Method, Currency, Scenario, Raw string
	Amount                                        int64
	CreatedAt, UpdatedAt                          time.Time
}

var errNoPayment = errors.New("sandbox: no simulated payment")

func (a *Adapter) load(ctx context.Context, our string) (state, error) {
	var s state
	err := a.Pool.QueryRow(ctx, `SELECT id, provider_payment_ref, our_reference::text, method, currency, scenario, raw_status, amount_minor,
		created_at, updated_at FROM app.sandbox_provider_payments WHERE provider_id = $1 AND our_reference = $2`, a.ProviderID, our).
		Scan(&s.ID, &s.Ref, &s.Our, &s.Method, &s.Currency, &s.Scenario, &s.Raw, &s.Amount, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return state{}, errNoPayment
	}
	return s, err
}

// GetPayment implements psp.PaymentProvider (status query by OUR reference).
func (a *Adapter) GetPayment(ctx context.Context, m psp.CallMeta, paymentID string) psp.Observation {
	start := a.now()
	if !ids.Valid(paymentID) {
		return psp.Observation{Class: psp.ClassInvalidRequest}
	}
	s, err := a.load(ctx, paymentID)
	if errors.Is(err, errNoPayment) {
		final := !m.ReferenceCreatedAt.IsZero() && a.now().Sub(m.ReferenceCreatedAt) > NotFoundFinality
		return psp.Observation{Class: psp.ClassNotFound, HTTPStatus: 404, NotFoundFinal: final, Latency: a.now().Sub(start)}
	}
	if err != nil {
		return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownHTTP5xx, HTTPStatus: 500, Latency: a.now().Sub(start)}
	}
	o := a.observe(s.Raw)
	o.ProviderRef, o.ReportedAmount, o.ReportedCcy, o.HTTPStatus = s.Ref, &s.Amount, s.Currency, 200
	o.PayerMasked = "****1234"
	if s.Raw == "declined" || s.Raw == "failed" {
		o.FailureCode = "DECLINED"
	}
	o.Latency = a.now().Sub(start)
	return o
}

// CancelPayment implements psp.PaymentProvider: a pending or action-required simulated payment is cancelled.
func (a *Adapter) CancelPayment(ctx context.Context, m psp.CallMeta, paymentID string) psp.Observation {
	s, err := a.load(ctx, paymentID)
	if errors.Is(err, errNoPayment) {
		return psp.Observation{Class: psp.ClassNotFound, HTTPStatus: 404}
	}
	if err != nil {
		return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownHTTP5xx}
	}
	if s.Raw == "pending" || s.Raw == "action_required" {
		if _, err := a.Pool.Exec(ctx, `UPDATE app.sandbox_provider_payments SET raw_status = 'cancelled' WHERE id = $1`, s.ID); err != nil {
			return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownHTTP5xx}
		}
		s.Raw = "cancelled"
	}
	o := a.observe(s.Raw)
	o.ProviderRef = s.Ref
	return o
}

// State is the simulator-side view for the development control API.
type State struct {
	ProviderPaymentRef string    `json:"provider_payment_ref"`
	OurReference       string    `json:"our_reference"`
	Scenario           string    `json:"scenario"`
	RawStatus          string    `json:"raw_status"`
	AmountMinor        string    `json:"amount_minor"`
	Currency           string    `json:"currency"`
	Method             string    `json:"method"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Simulated          bool      `json:"simulated"`
}

// ErrNoSimulatedPayment means the simulator has no record for the payment.
var ErrNoSimulatedPayment = errNoPayment

// Inspect returns the simulator-side state of a payment.
func (a *Adapter) Inspect(ctx context.Context, paymentID string) (State, error) {
	s, err := a.load(ctx, paymentID)
	if err != nil {
		return State{}, err
	}
	return State{ProviderPaymentRef: s.Ref, OurReference: s.Our, Scenario: s.Scenario, RawStatus: s.Raw,
		AmountMinor: strconv.FormatInt(s.Amount, 10), Currency: s.Currency, Method: s.Method, CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt, Simulated: true}, nil
}

// Schedule schedules a webhook for an existing simulated payment (development control API).
func (a *Adapter) Schedule(ctx context.Context, paymentID string, h Hook) error {
	s, err := a.load(ctx, paymentID)
	if err != nil {
		return err
	}
	return db.WithTx(ctx, a.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		evt := randomRef("sbx_evt_")
		for i := 0; i < max(1, h.Copies); i++ {
			if err := scheduleTx(ctx, tx, s.ID, evt, h, s.Amount, s.Currency, a.now()); err != nil {
				return err
			}
		}
		return nil
	})
}
