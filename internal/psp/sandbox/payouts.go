package sandbox

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// The simulator's payout side (Stage 11, docs/stage-11/payout-simulator.md). Like the payment side it never
// contacts real payout infrastructure: its provider-side state is app.sandbox_provider_payouts, it stores only a
// masked synthetic destination, and its webhooks are signed with test secrets and sent through FundZim's own
// verification path. Scenario facts are the simulator's contract, never statements about a real rail.

// PayoutMappingVersion is the version of the payout status mapping table.
const PayoutMappingVersion = "sandbox-payout-map-v1"

var payoutMapping = map[string]string{
	"processing": psp.PayoutProcessing,
	"completed":  psp.PayoutCompleted,
	"failed":     psp.PayoutFailed,
	"rejected":   psp.PayoutFailed,
	"returned":   psp.PayoutReturned,
}

// payoutStatusRank orders simulated provider states so a replayed earlier status does not move the provider back.
var payoutStatusRank = map[string]int{"processing": 1, "failed": 3, "rejected": 3, "completed": 4, "returned": 5}

// Payout scenario names (brief §26).
const (
	PoSuccess             = "SUCCESS"
	PoPending             = "PENDING"
	PoFailure             = "FAILURE"
	PoTimeout             = "TIMEOUT"
	PoUnknown             = "UNKNOWN"
	PoDuplicateCallback   = "DUPLICATE_CALLBACK"
	PoOutOfOrderCallback  = "OUT_OF_ORDER_CALLBACK"
	PoInvalidSignature    = "INVALID_SIGNATURE"
	PoDelayedSuccess      = "DELAYED_SUCCESS"
	PoProviderUnavailable = "PROVIDER_UNAVAILABLE"
	PoPartialResponse     = "PARTIAL_PROVIDER_RESPONSE"
	PoFailureAsync        = "FAILURE_AFTER_PROCESSING"
	PoReturned            = "RETURNED_AFTER_COMPLETION"
)

// PayoutScenario is a deterministic simulator payout behaviour.
type PayoutScenario struct {
	Name     string
	Webhooks []Hook
}

// payoutScenarios by the amount's last two minor-unit digits. WORKER_RESTART is not a provider behaviour: it is
// exercised by crashing between the IN_FLIGHT attempt and its result (tests and docs).
var payoutScenarios = map[int64]PayoutScenario{
	0:  {Name: PoSuccess, Webhooks: []Hook{{Status: "completed", After: time.Second}}},
	1:  {Name: PoPending},
	2:  {Name: PoFailure},
	3:  {Name: PoTimeout, Webhooks: []Hook{{Status: "completed", After: 3 * time.Second}}},
	4:  {Name: PoUnknown},
	5:  {Name: PoDuplicateCallback, Webhooks: []Hook{{Status: "completed", After: time.Second, Copies: 3}}},
	6:  {Name: PoOutOfOrderCallback, Webhooks: []Hook{{Status: "completed", After: time.Second}, {Status: "processing", After: 3 * time.Second}}},
	7:  {Name: PoInvalidSignature, Webhooks: []Hook{{Status: "completed", After: time.Second, SignWith: "wrong"}, {Status: "completed", After: time.Second, SignWith: "none"}}},
	8:  {Name: PoDelayedSuccess, Webhooks: []Hook{{Status: "completed", After: 12 * time.Second}}},
	9:  {Name: PoProviderUnavailable},
	10: {Name: PoPartialResponse, Webhooks: []Hook{{Status: "completed", After: 2 * time.Second}}},
	11: {Name: PoFailureAsync, Webhooks: []Hook{{Status: "failed", After: time.Second}}},
	12: {Name: PoReturned, Webhooks: []Hook{{Status: "completed", After: time.Second}, {Status: "returned", After: 4 * time.Second}}},
}

// PayoutScenarioFor selects the scenario: an explicit metadata name (non-production only), else the amount's last
// two minor-unit digits, else SUCCESS.
func PayoutScenarioFor(amountMinor int64, explicit string) PayoutScenario {
	if explicit != "" {
		for _, sc := range payoutScenarios {
			if sc.Name == explicit {
				return sc
			}
		}
	}
	if sc, ok := payoutScenarios[amountMinor%100]; ok {
		return sc
	}
	return payoutScenarios[0]
}

// PayoutScenarios returns the scenario table keyed by amount suffix (documentation and tests).
func PayoutScenarios() map[int64]PayoutScenario {
	out := make(map[int64]PayoutScenario, len(payoutScenarios))
	for k, v := range payoutScenarios {
		out[k] = v
	}
	return out
}

// MapPayoutStatus implements psp.PayoutProvider.
func (a *Adapter) MapPayoutStatus(raw string) (string, bool, string) {
	if c, ok := payoutMapping[raw]; ok {
		return c, true, PayoutMappingVersion
	}
	return psp.PayoutUnknown, false, PayoutMappingVersion
}

// PayoutLimits implements psp.PayoutProvider: the simulator contract's payout limits (USD 1.00 – 10 000.00 to the
// three simulated rails). Simulator facts only.
func (a *Adapter) PayoutLimits(rail, currency string) (psp.Limits, bool) {
	if currency != "USD" || (rail != "ECOCASH" && rail != "ONEMONEY" && rail != "BANK_TRANSFER") {
		return psp.Limits{}, false
	}
	return psp.Limits{MinMinor: 100, MaxMinor: 1_000_000}, true
}

func (a *Adapter) observePayout(raw string) psp.Observation {
	c, mapped, v := a.MapPayoutStatus(raw)
	return psp.Observation{Class: psp.ClassAccepted, RawStatus: raw, Canonical: c, Mapped: mapped, MappingVersion: v, HTTPStatus: 200}
}

type payoutState struct {
	ID, Ref, Our, Rail, Currency, Scenario, Raw string
	Amount                                      int64
}

var errNoPayout = errors.New("sandbox: no simulated payout")

func (a *Adapter) loadPayout(ctx context.Context, our string) (payoutState, error) {
	var s payoutState
	err := a.Pool.QueryRow(ctx, `SELECT id, provider_payout_ref, our_reference::text, rail, currency, scenario, raw_status, amount_minor
		FROM app.sandbox_provider_payouts WHERE provider_id = $1 AND our_reference = $2`, a.ProviderID, our).
		Scan(&s.ID, &s.Ref, &s.Our, &s.Rail, &s.Currency, &s.Scenario, &s.Raw, &s.Amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return payoutState{}, errNoPayout
	}
	return s, err
}

// CreatePayout implements psp.PayoutProvider. The same reference always returns the existing simulated payout:
// the simulator is idempotent on our reference (it never disburses twice).
func (a *Adapter) CreatePayout(ctx context.Context, m psp.CallMeta, r psp.CreatePayoutRequest) psp.Observation {
	start := a.now()
	finish := func(o psp.Observation) psp.Observation { o.Latency = a.now().Sub(start); return o }
	if !ids.Valid(r.PayoutID) || r.AmountMinor <= 0 {
		return finish(psp.Observation{Class: psp.ClassInvalidRequest, HTTPStatus: 400})
	}
	if ex, err := a.loadPayout(ctx, r.PayoutID); err == nil {
		o := a.observePayout(ex.Raw)
		o.ProviderRef = ex.Ref
		return finish(o)
	}
	sc := PayoutScenarioFor(r.AmountMinor, r.Metadata["sandbox_scenario"])
	switch sc.Name {
	case PoProviderUnavailable:
		// connection refused before the request was written: nothing reached the simulated provider
		return finish(psp.Observation{Class: psp.ClassRetryableBeforeSend})
	case PoUnknown:
		// the call timed out and the simulated provider never created the payout: only a status query can tell
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout})
	}
	ref := randomRef("sbx_po_")
	initial := "processing"
	if sc.Name == PoFailure {
		initial = "rejected"
	}
	if err := a.recordPayout(ctx, ref, r, sc, initial); err != nil {
		a.Logger.Error("sandbox: record simulated payout failed", "err", err)
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownMalformed})
	}
	switch sc.Name {
	case PoFailure:
		o := a.observePayout("rejected")
		o.Class, o.ProviderRef, o.FailureCode, o.HTTPStatus = psp.ClassDefiniteFailure, ref, "DESTINATION_REJECTED", 422
		return finish(o)
	case PoTimeout:
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout})
	case PoPartialResponse:
		return finish(psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownMalformed, HTTPStatus: 200})
	}
	o := a.observePayout(initial)
	o.ProviderRef = ref
	return finish(o)
}

func (a *Adapter) recordPayout(ctx context.Context, ref string, r psp.CreatePayoutRequest, sc PayoutScenario, status string) error {
	return db.WithTx(ctx, a.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO app.sandbox_provider_payouts (id, provider_id, provider_payout_ref, our_reference, rail,
			destination_masked, currency, amount_minor, scenario, raw_status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			id, a.ProviderID, ref, r.PayoutID, r.Rail, r.DestinationMasked, r.Currency, r.AmountMinor, sc.Name, status); err != nil {
			return err
		}
		now := a.now()
		for _, h := range sc.Webhooks {
			evt := randomRef("sbx_evt_")
			for i := 0; i < max(1, h.Copies); i++ {
				if err := schedulePayoutTx(ctx, tx, id, evt, h, r.AmountMinor, r.Currency, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func schedulePayoutTx(ctx context.Context, tx pgx.Tx, sandboxPayoutID, eventID string, h Hook, amount int64, ccy string, now time.Time) error {
	sign := h.SignWith
	if sign == "" {
		sign = "current"
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.sandbox_scheduled_webhooks (id, sandbox_payout_id, provider_event_id, event_type, raw_status,
		amount_minor, currency, sign_with, timestamp_offset_seconds, due_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		ids.New(), sandboxPayoutID, eventID, "payout."+h.Status, h.Status, amount, ccy, sign, h.TimestampOffset, now.Add(h.After))
	return err
}

// GetPayout implements psp.PayoutProvider (status query by OUR reference). "Not found" is final only after the
// simulator's documented NotFoundFinality window since our reference was first sent.
func (a *Adapter) GetPayout(ctx context.Context, m psp.CallMeta, payoutID string) psp.Observation {
	start := a.now()
	if !ids.Valid(payoutID) {
		return psp.Observation{Class: psp.ClassInvalidRequest}
	}
	s, err := a.loadPayout(ctx, payoutID)
	if errors.Is(err, errNoPayout) {
		final := !m.ReferenceCreatedAt.IsZero() && a.now().Sub(m.ReferenceCreatedAt) > NotFoundFinality
		return psp.Observation{Class: psp.ClassNotFound, HTTPStatus: 404, NotFoundFinal: final, Latency: a.now().Sub(start)}
	}
	if err != nil {
		return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownHTTP5xx, HTTPStatus: 500, Latency: a.now().Sub(start)}
	}
	o := a.observePayout(s.Raw)
	o.ProviderRef, o.ReportedAmount, o.ReportedCcy = s.Ref, &s.Amount, s.Currency
	if s.Raw == "failed" || s.Raw == "rejected" {
		o.FailureCode = "DESTINATION_REJECTED"
	}
	o.Latency = a.now().Sub(start)
	return o
}

// CancelPayout implements psp.PayoutProvider: the simulator does not support payout cancellation (the
// capability declares supports_cancel only for collections). Explicitly NOT_SUPPORTED.
func (a *Adapter) CancelPayout(ctx context.Context, m psp.CallMeta, payoutID string) psp.Observation {
	return psp.Observation{Class: psp.ClassNotSupported}
}

// PayoutState is the simulator-side view of a payout for the development control API.
type PayoutState struct {
	ProviderPayoutRef string `json:"provider_payout_ref"`
	OurReference      string `json:"our_reference"`
	Scenario          string `json:"scenario"`
	RawStatus         string `json:"raw_status"`
	AmountMinor       string `json:"amount_minor"`
	Currency          string `json:"currency"`
	Rail              string `json:"rail"`
	Simulated         bool   `json:"simulated"`
}

// ErrNoSimulatedPayout means the simulator has no record for the payout.
var ErrNoSimulatedPayout = errNoPayout

// InspectPayout returns the simulator-side state of a payout.
func (a *Adapter) InspectPayout(ctx context.Context, payoutID string) (PayoutState, error) {
	s, err := a.loadPayout(ctx, payoutID)
	if err != nil {
		return PayoutState{}, err
	}
	return PayoutState{ProviderPayoutRef: s.Ref, OurReference: s.Our, Scenario: s.Scenario, RawStatus: s.Raw,
		AmountMinor: strconv.FormatInt(s.Amount, 10), Currency: s.Currency, Rail: s.Rail, Simulated: true}, nil
}

// SchedulePayout schedules a webhook for an existing simulated payout (development control API).
func (a *Adapter) SchedulePayout(ctx context.Context, payoutID string, h Hook) error {
	s, err := a.loadPayout(ctx, payoutID)
	if err != nil {
		return err
	}
	return db.WithTx(ctx, a.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		evt := randomRef("sbx_evt_")
		for i := 0; i < max(1, h.Copies); i++ {
			if err := schedulePayoutTx(ctx, tx, s.ID, evt, h, s.Amount, s.Currency, a.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

var _ psp.PayoutProvider = (*Adapter)(nil)
