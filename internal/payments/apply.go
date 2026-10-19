package payments

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// payRow is a payment_intents row.
type payRow struct {
	ID, DonationIntentID, CampaignID, Currency, Method, ProviderID, AccountID, CapabilityID, Status, IdemKey, Correlation string
	Amount                                                                                                                int64
	Simulated, NeedsRecon                                                                                                 bool
	ExecEnv                                                                                                               string
	AttemptSeq, PollAttempts, Version                                                                                     int
	ProviderPaymentRef, UnknownReason, FailureCode, LastSource                                                            *string
	UnknownSince, NextPollAt, PollHorizonAt, ExpiresAt                                                                    *time.Time
	CreatedAt, UpdatedAt                                                                                                  time.Time
}

const paySelect = `SELECT id, donation_intent_id, campaign_id, currency, method, provider_id, provider_account_id, capability_id, status,
	idempotency_key::text, correlation_id, amount_minor, is_simulated, needs_reconciliation, attempt_seq, poll_attempts, version,
	provider_payment_ref, unknown_reason, failure_code, last_authoritative_source, unknown_since, next_poll_at, poll_horizon_at, expires_at,
	created_at, updated_at, execution_environment FROM app.payment_intents`

func scanPay(row pgx.Row) (payRow, error) {
	var p payRow
	err := row.Scan(&p.ID, &p.DonationIntentID, &p.CampaignID, &p.Currency, &p.Method, &p.ProviderID, &p.AccountID, &p.CapabilityID,
		&p.Status, &p.IdemKey, &p.Correlation, &p.Amount, &p.Simulated, &p.NeedsRecon, &p.AttemptSeq, &p.PollAttempts, &p.Version,
		&p.ProviderPaymentRef, &p.UnknownReason, &p.FailureCode, &p.LastSource, &p.UnknownSince, &p.NextPollAt, &p.PollHorizonAt,
		&p.ExpiresAt, &p.CreatedAt, &p.UpdatedAt, &p.ExecEnv)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrPaymentNotFound
	}
	return p, err
}

// lockPay loads a payment FOR UPDATE: every decision about one payment is serialised on its row
// (payment-schema §6: webhook vs poll vs sweeper).
func lockPay(ctx context.Context, tx pgx.Tx, id string) (payRow, error) {
	if !ids.Valid(id) {
		return payRow{}, ErrPaymentNotFound
	}
	return scanPay(tx.QueryRow(ctx, paySelect+` WHERE id = $1 FOR UPDATE`, id))
}

// Obs is one observation to apply to a payment.
type Obs struct {
	Target          string // canonical target status
	Source          string
	RawStatus       string // provider status verbatim ("" for INTERNAL/STAFF)
	Mapped          bool
	MappingVersion  string
	ProviderRef     string
	ReportedAmount  *int64
	ReportedCcy     string
	ProviderEventID string
	InboxID         string
	AttemptID       string
	OccurredAt      *time.Time
	UnknownReason   string
	FailureCode     string
	PayerToken      []byte
	PayerMasked     string
	ActorType       string
	ActorID         string
	Reason          string
	Correlation     string
}

// Outcome is what apply did.
type Outcome struct {
	Applied      bool
	NotApplied   string
	From, To     string
	Unrecognised bool // unmapped provider status: the inbox row must be UNRECOGNISED (alert)
	Parked       bool // prerequisite not reached: the inbox row must be PARKED until SUCCEEDED
}

func fromProvider(src string) bool {
	return src == SrcSync || src == SrcWebhook || src == SrcPoll || src == SrcRecon
}

// apply applies an observation to a LOCKED payment in tx (payment-state-machine §3.1). It never returns an error
// for a provider anomaly: anomalies are recorded (event applied=false, reconciliation reference), not applied.
func (s *Service) apply(ctx context.Context, tx pgx.Tx, p *payRow, o Obs) (Outcome, error) {
	if o.ActorType == "" {
		o.ActorType = "SYSTEM"
		if fromProvider(o.Source) {
			o.ActorType = "PROVIDER"
		}
	}
	if o.Correlation == "" {
		o.Correlation = p.Correlation
	}
	prov := fromProvider(o.Source)
	if prov && o.RawStatus != "" {
		if err := s.recordTransaction(ctx, tx, p, o); err != nil {
			return Outcome{}, err
		}
		if err := s.recordEvidence(ctx, tx, p, o); err != nil {
			return Outcome{}, err
		}
	}
	// 1. unmapped provider status: never guessed (ADR-024)
	if prov && !o.Mapped {
		if err := s.notApplied(ctx, tx, p, o, psp.StatusUnknown, NotAnomaly); err != nil {
			return Outcome{}, err
		}
		if err := s.openRecon(ctx, tx, p, reconUnmapped, o); err != nil {
			return Outcome{}, err
		}
		s.Logger.Warn("payments: unmapped provider status (ALERT)", "payment_id", p.ID, "raw_status", o.RawStatus)
		return Outcome{NotApplied: NotAnomaly, From: p.Status, To: psp.StatusUnknown, Unrecognised: true}, nil
	}
	// 2. a reported amount or currency that differs is an anomaly, never applied as money-affecting
	if prov && o.ReportedAmount != nil && (o.Target == psp.StatusSucceeded || o.Target == psp.StatusAuthorised) &&
		(*o.ReportedAmount != p.Amount || o.ReportedCcy != p.Currency) {
		reason := reconAmountMismatch
		if o.ReportedCcy != p.Currency {
			reason = reconCurrencyMismatch
		}
		if err := s.notApplied(ctx, tx, p, o, o.Target, NotAnomaly); err != nil {
			return Outcome{}, err
		}
		if err := s.openRecon(ctx, tx, p, reason, o); err != nil {
			return Outcome{}, err
		}
		s.Logger.Warn("payments: provider reported a different amount or currency (ALERT)", "payment_id", p.ID)
		return Outcome{NotApplied: NotAnomaly, From: p.Status, To: o.Target}, nil
	}
	// 3. the same provider event already applied
	if o.ProviderEventID != "" {
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payment_events WHERE payment_intent_id = $1 AND provider_event_id = $2
			AND applied)`, p.ID, o.ProviderEventID).Scan(&dup); err != nil {
			return Outcome{}, err
		}
		if dup {
			if err := s.notApplied(ctx, tx, p, o, o.Target, NotDuplicate); err != nil {
				return Outcome{}, err
			}
			return Outcome{NotApplied: NotDuplicate, From: p.Status, To: o.Target}, nil
		}
	}
	// 4. a synchronous/observed state that implies an intermediate step from CREATED (CREATED → PENDING → X)
	if _, ok := EdgeCode(p.Status, o.Target); !ok && p.Status == psp.StatusCreated && o.Target != psp.StatusPending {
		if _, ok := EdgeCode(psp.StatusPending, o.Target); ok && sourceAllowed(p.Status, psp.StatusPending, o.Source) {
			step := o
			step.Target, step.ProviderEventID = psp.StatusPending, ""
			if _, err := s.transition(ctx, tx, p, step); err != nil {
				return Outcome{}, err
			}
		}
	}
	_, isEdge := EdgeCode(p.Status, o.Target)
	switch {
	case p.Status == o.Target:
		if err := s.notApplied(ctx, tx, p, o, o.Target, NotLowerRank); err != nil {
			return Outcome{}, err
		}
		return Outcome{NotApplied: NotLowerRank, From: p.Status, To: o.Target}, nil
	case isEdge && (Rank[o.Target] > Rank[p.Status] || exception(p.Status, o.Target, o.Source)) && sourceAllowed(p.Status, o.Target, o.Source) &&
		(o.Target != psp.StatusAuthorised || p.Method == psp.MethodCard):
		return s.transition(ctx, tx, p, o)
	case needsSuccessFirst(o.Target) && Rank[p.Status] < Rank[psp.StatusSucceeded] && o.InboxID != "":
		if err := s.notApplied(ctx, tx, p, o, o.Target, NotParked); err != nil {
			return Outcome{}, err
		}
		return Outcome{NotApplied: NotParked, From: p.Status, To: o.Target, Parked: true}, nil
	case Rank[o.Target] <= Rank[p.Status]:
		if err := s.notApplied(ctx, tx, p, o, o.Target, NotLowerRank); err != nil {
			return Outcome{}, err
		}
		return Outcome{NotApplied: NotLowerRank, From: p.Status, To: o.Target}, nil
	}
	// a higher-rank non-edge (e.g. SUCCEEDED after REFUNDED is lower; FAILED after SUCCEEDED is lower-rank and
	// recorded above; this branch is a forbidden jump such as CREATED → REFUNDED from a poll)
	if err := s.notApplied(ctx, tx, p, o, o.Target, NotAnomaly); err != nil {
		return Outcome{}, err
	}
	if prov {
		if err := s.openRecon(ctx, tx, p, reconAnomaly, o); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{NotApplied: NotAnomaly, From: p.Status, To: o.Target}, nil
}

// transition performs one allowed edge with its event, side effects and outbox events.
func (s *Service) transition(ctx context.Context, tx pgx.Tx, p *payRow, o Obs) (Outcome, error) {
	from, to := p.Status, o.Target
	code, _ := EdgeCode(from, to)
	now := s.now()
	if err := s.insertEvent(ctx, tx, eventRow{subject: "PAYMENT_INTENT", donationID: p.DonationIntentID, paymentID: p.ID, from: from,
		to: to, applied: true, code: code, obs: o}); err != nil {
		return Outcome{}, err
	}
	set := struct {
		unknownSince  *time.Time
		unknownReason *string
		nextPoll      *time.Time
		horizon       *time.Time
		failure       *string
	}{p.UnknownSince, p.UnknownReason, p.NextPollAt, p.PollHorizonAt, p.FailureCode}
	switch {
	case to == psp.StatusUnknown:
		t, r := now, o.UnknownReason
		h := now.Add(UnknownHorizon)
		n := now.Add(3 * time.Second)
		set.unknownSince, set.unknownReason, set.horizon, set.nextPoll = &t, &r, &h, &n
		s.m.unknown.WithLabelValues(providerLabel(p), r).Inc()
	case terminal(to):
		set.nextPoll = nil
		if to == psp.StatusFailed && o.FailureCode != "" {
			f := o.FailureCode
			set.failure = &f
		}
	default: // PENDING, REQUIRES_ACTION, AUTHORISED: safety-net polling behind the webhooks
		n := now.Add(pollDelay(0))
		set.nextPoll = &n
	}
	src := o.Source
	if src == SrcDonor {
		src = ""
	}
	if _, err := tx.Exec(ctx, `UPDATE app.payment_intents SET status = $2, status_rank = app.payment_status_rank($2),
		last_authoritative_source = coalesce(nullif($3, ''), last_authoritative_source), unknown_since = $4, unknown_reason = $5,
		next_poll_at = $6, poll_horizon_at = $7, failure_code = $8, poll_attempts = CASE WHEN $2 = 'UNKNOWN' THEN 0 ELSE poll_attempts END,
		version = version + 1 WHERE id = $1`, p.ID, to, src, set.unknownSince, set.unknownReason, set.nextPoll, set.horizon, set.failure); err != nil {
		return Outcome{}, err
	}
	p.Status, p.UnknownSince, p.UnknownReason, p.NextPollAt, p.PollHorizonAt, p.FailureCode = to, set.unknownSince, set.unknownReason,
		set.nextPoll, set.horizon, set.failure
	p.Version++
	payload := map[string]any{"payment_id": p.ID, "donation_intent_id": p.DonationIntentID, "from": from, "to": to, "source": o.Source,
		"simulated": p.Simulated, "execution_environment": p.ExecEnv, "test_payment": p.ExecEnv != psp.ExecProduction}
	if to == psp.StatusFailed || to == psp.StatusUnknown {
		// risk signals (Stage 13) and the donor's notification (Stage 15, ADR-047): ids and the amount only
		var donor, campaign string
		if err := tx.QueryRow(ctx, `SELECT donor_user_id::text, campaign_id::text FROM app.donation_intents WHERE id = $1`, p.DonationIntentID).
			Scan(&donor, &campaign); err != nil {
			return Outcome{}, err
		}
		payload["donor_user_id"], payload["campaign_id"] = donor, campaign
		payload["amount_minor"], payload["currency"] = strconv.FormatInt(p.Amount, 10), p.Currency
	}
	if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "payment", AggregateID: p.ID, EventType: "payments.payment_status_changed",
		Payload:       payload,
		CorrelationID: o.Correlation, OccurredAt: now}); err != nil {
		return Outcome{}, err
	}
	if from == psp.StatusUnknown && fromProvider(o.Source) {
		if err := audit.Record(ctx, tx, audit.Event{Action: "payments.payment.status_reconciled", ActorType: "system", TargetType: "payment",
			TargetID: p.ID, Metadata: map[string]any{"audit_event": "PAYMENT_STATUS_RECONCILED", "from": from, "to": to, "source": o.Source,
				"execution_environment": p.ExecEnv, "raw_status": o.RawStatus}}); err != nil {
			return Outcome{}, err
		}
	}
	if to == psp.StatusSucceeded && from != psp.StatusDisputed {
		if err := s.onSucceeded(ctx, tx, p, from, o); err != nil {
			return Outcome{}, err
		}
	}
	if terminal(to) {
		if _, err := tx.Exec(ctx, `UPDATE app.payment_reconciliation_references SET status = 'RESOLVED_BY_PROVIDER_STATUS', resolved_at = $2,
			resolution_note = $3, version = version + 1 WHERE payment_intent_id = $1 AND status <> 'RESOLVED_BY_PROVIDER_STATUS'
			AND reason IN ('UNKNOWN_HORIZON_EXCEEDED','NOT_FOUND_AFTER_HORIZON')`, p.ID, now,
			"Authoritative provider status "+to+" ("+o.Source+"); settlement not reconciled (Stage 17)."); err != nil {
			return Outcome{}, err
		}
	}
	if s.PSP != nil {
		if _, err := s.PSP.ReleaseParked(ctx, tx, p.ID, to); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{Applied: true, From: from, To: to}, nil
}

// onSucceeded: posting request (Stage 10 seam), outbox, late success, donation intent PAID, duplicate success.
func (s *Service) onSucceeded(ctx context.Context, tx pgx.Tx, p *payRow, from string, o Obs) error {
	key := "payment:" + p.ID + ":capture"
	if s.Ledger != nil && p.ExecEnv == psp.ExecProduction { // test payments are never posted (Stage 9)
		if err := s.Ledger.Post(ctx, tx, PostingRequest{Key: key, Rule: "DONATION_CAPTURED", PaymentID: p.ID, CampaignID: p.CampaignID,
			Currency: p.Currency, AmountMinor: p.Amount}); err != nil {
			return err
		}
	}
	now := s.now()
	var donor string // the donor's confirmation (Stage 15): only this event, emitted for authoritative provider evidence, may produce one
	if err := tx.QueryRow(ctx, `SELECT donor_user_id::text FROM app.donation_intents WHERE id = $1`, p.DonationIntentID).Scan(&donor); err != nil {
		return err
	}
	if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "payment", AggregateID: p.ID, EventType: "payments.payment_succeeded",
		Payload: map[string]any{"payment_id": p.ID, "donation_intent_id": p.DonationIntentID, "campaign_id": p.CampaignID,
			"donor_user_id": donor, "amount_minor": strconv.FormatInt(p.Amount, 10), "currency": p.Currency,
			"posting_key": key, "simulated": p.Simulated, "execution_environment": p.ExecEnv,
			// Stage 10 posts only PRODUCTION payments; simulator and provider-sandbox successes are test payments
			"test_payment": p.ExecEnv != psp.ExecProduction}, CorrelationID: o.Correlation, OccurredAt: now}); err != nil {
		return err
	}
	if from == psp.StatusFailed || from == psp.StatusExpired || from == psp.StatusCancelled {
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "payment", AggregateID: p.ID, EventType: "payments.late_success",
			Payload: map[string]any{"payment_id": p.ID, "from": from, "simulated": p.Simulated}, CorrelationID: o.Correlation,
			OccurredAt: now}); err != nil {
			return err
		}
		if err := s.openRecon(ctx, tx, p, reconLateSuccess, o); err != nil {
			return err
		}
	}
	// another payment of the same donation already succeeded: a duplicate payment (refund rule), never hidden
	var others int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.payment_intents WHERE donation_intent_id = $1 AND id <> $2
		AND status_rank >= app.payment_status_rank('SUCCEEDED')`, p.DonationIntentID, p.ID).Scan(&others); err != nil {
		return err
	}
	if others > 0 {
		if err := s.openRecon(ctx, tx, p, reconDuplicateSuccess, o); err != nil {
			return err
		}
	}
	return s.markDonationPaid(ctx, tx, p.DonationIntentID, o.Correlation)
}

func (s *Service) markDonationPaid(ctx context.Context, tx pgx.Tx, donationID, corr string) error {
	var st string
	if err := tx.QueryRow(ctx, `SELECT status FROM app.donation_intents WHERE id = $1 FOR UPDATE`, donationID).Scan(&st); err != nil {
		return err
	}
	if st == DonationPaid {
		return nil
	}
	if err := s.insertEvent(ctx, tx, eventRow{subject: "DONATION_INTENT", donationID: donationID, from: st, to: DonationPaid, applied: true,
		obs: Obs{Source: SrcInternal, ActorType: "SYSTEM", Reason: "a payment succeeded", Correlation: corr}}); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE app.donation_intents SET status = 'PAID', version = version + 1 WHERE id = $1`, donationID)
	return err
}

// recordTransaction upserts the provider transaction (raw status verbatim) and the provider reference.
func (s *Service) recordTransaction(ctx context.Context, tx pgx.Tx, p *payRow, o Obs) error {
	ref := o.ProviderRef
	if ref == "" && p.ProviderPaymentRef != nil {
		ref = *p.ProviderPaymentRef
	}
	if ref == "" {
		return nil
	}
	if p.ProviderPaymentRef == nil {
		if _, err := tx.Exec(ctx, `UPDATE app.payment_intents SET provider_payment_ref = $2 WHERE id = $1 AND provider_payment_ref IS NULL`,
			p.ID, ref); err != nil {
			return err
		}
		p.ProviderPaymentRef = &ref
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app.payment_provider_references (id, provider_id, ref_kind, reference, payment_intent_id, source)
		VALUES ($1, $2, 'PAYMENT', $3, $4, $5) ON CONFLICT (provider_id, ref_kind, reference) DO NOTHING`,
		ids.New(), p.ProviderID, ref, p.ID, o.Source); err != nil {
		return err
	}
	mapped := o.Target
	if !o.Mapped {
		mapped = psp.StatusUnknown
	}
	var fp []byte
	if len(o.PayerToken) > 0 && s.Keyed != nil {
		fp = s.Keyed.Sum("payments.payer_instrument", string(o.PayerToken))
	}
	var masked *string
	if o.PayerMasked != "" {
		masked = &o.PayerMasked
	}
	var amt *int64
	var ccy *string
	if o.ReportedAmount != nil && o.ReportedCcy != "" {
		amt, ccy = o.ReportedAmount, &o.ReportedCcy
	}
	raw := o.RawStatus
	if len(raw) > 200 {
		raw = raw[:200]
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.payment_transactions (id, payment_intent_id, provider_id, provider_transaction_ref, raw_status,
		raw_status_at, mapped_status, mapping_version, last_source, reported_amount_minor, reported_currency, payer_instrument_fingerprint,
		payer_instrument_masked) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (payment_intent_id) DO UPDATE SET raw_status = EXCLUDED.raw_status, raw_status_at = EXCLUDED.raw_status_at,
		mapped_status = EXCLUDED.mapped_status, mapping_version = EXCLUDED.mapping_version, last_source = EXCLUDED.last_source,
		reported_amount_minor = coalesce(EXCLUDED.reported_amount_minor, app.payment_transactions.reported_amount_minor),
		reported_currency = coalesce(EXCLUDED.reported_currency, app.payment_transactions.reported_currency),
		payer_instrument_fingerprint = coalesce(EXCLUDED.payer_instrument_fingerprint, app.payment_transactions.payer_instrument_fingerprint),
		payer_instrument_masked = coalesce(EXCLUDED.payer_instrument_masked, app.payment_transactions.payer_instrument_masked),
		version = app.payment_transactions.version + 1`,
		ids.New(), p.ID, p.ProviderID, ref, raw, o.OccurredAt, mapped, nonEmpty(o.MappingVersion, "none"), o.Source, amt, ccy, fp, masked)
	return err
}

// eventRow is one payment_events insert.
type eventRow struct {
	subject, donationID, paymentID, refundID, from, to, code, notApplied string
	applied                                                              bool
	obs                                                                  Obs
	evidenceID                                                           string
}

func (s *Service) insertEvent(ctx context.Context, tx pgx.Tx, e eventRow) error {
	o := e.obs
	actorType := o.ActorType
	if actorType == "" {
		actorType = "SYSTEM"
	}
	raw := o.RawStatus
	if len(raw) > 200 {
		raw = raw[:200]
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.payment_events (id, subject_type, donation_intent_id, payment_intent_id, refund_request_id,
		from_status, to_status, applied, not_applied_reason, transition_code, source, provider_event_id, inbox_event_id, payment_attempt_id,
		raw_status, actor_type, actor_id, reason, evidence_record_id, correlation_id, occurred_at)
		VALUES ($1, $2, $3, nullif($4, '')::uuid, nullif($5, '')::uuid, nullif($6, ''), $7, $8, nullif($9, ''), nullif($10, ''), $11,
		nullif($12, ''), nullif($13, '')::uuid, nullif($14, '')::uuid, nullif($15, ''), $16, nullif($17, '')::uuid, nullif($18, ''),
		nullif($19, '')::uuid, nullif($20, ''), $21)`,
		ids.New(), e.subject, e.donationID, e.paymentID, e.refundID, e.from, e.to, e.applied, e.notApplied, e.code, o.Source,
		o.ProviderEventID, o.InboxID, o.AttemptID, raw, actorType, o.ActorID, o.Reason, e.evidenceID, o.Correlation, o.OccurredAt)
	return err
}

// notApplied records an observation that changed nothing (never lost).
func (s *Service) notApplied(ctx context.Context, tx pgx.Tx, p *payRow, o Obs, to, reason string) error {
	s.m.notApplied.WithLabelValues(reason).Inc()
	return s.insertEvent(ctx, tx, eventRow{subject: "PAYMENT_INTENT", donationID: p.DonationIntentID, paymentID: p.ID, from: p.Status,
		to: to, applied: false, notApplied: reason, obs: o})
}

func pollDelay(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 0; i < attempt && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func providerLabel(p *payRow) string {
	if p.Simulated {
		return "sandbox"
	}
	return "real"
}

func i64s(n int64) string { return strconv.FormatInt(n, 10) }
