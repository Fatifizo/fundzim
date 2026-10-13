package payments

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// NextActionView is the donor's next step.
type NextActionView struct {
	Type             string     `json:"type"`
	SessionID        *string    `json:"session_id"`
	RedirectURL      *string    `json:"redirect_url"`
	InstructionsCode *string    `json:"instructions_code"`
	ExpiresAt        *time.Time `json:"expires_at"`
}

// PaymentAttempt is the donor view of a payment (a "payment attempt").
type PaymentAttempt struct {
	ID               string          `json:"id"`
	DonationIntentID string          `json:"donation_intent_id"`
	AttemptNumber    int             `json:"attempt_number"`
	Method           string          `json:"method"`
	MethodCategory   string          `json:"method_category"`
	Status           string          `json:"status"`
	Provider         psp.ProviderRef `json:"provider"`
	FailureCode      *string         `json:"failure_code"`
	Message          string          `json:"message"`
	NextAction       NextActionView  `json:"next_action"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// donorMessage is the donor-safe explanation of a status (never claims money was received unless SUCCEEDED).
func donorMessage(st string) string {
	switch st {
	case psp.StatusCreated, psp.StatusPending:
		return "Waiting for the payment provider to confirm this payment."
	case psp.StatusRequiresAction:
		return "Complete the payment with your payment provider (for example, approve it on your phone)."
	case psp.StatusAuthorised:
		return "The payment is authorised and waiting to be completed."
	case psp.StatusUnknown:
		return "We're confirming this payment with the provider. Please don't pay again."
	case psp.StatusSucceeded:
		return "The payment provider confirmed this payment."
	case psp.StatusFailed:
		return "This payment did not go through. No money was taken by this attempt; you can try again."
	case psp.StatusExpired:
		return "This payment expired before it was completed. You can try again."
	case psp.StatusCancelled:
		return "This payment was cancelled."
	case psp.StatusPartiallyRefunded, psp.StatusRefunded:
		return "This payment has been refunded in part or in full."
	}
	return "This payment is being reviewed."
}

func (s *Service) attemptView(ctx context.Context, q rowQuerier, p payRow) (PaymentAttempt, error) {
	prov, err := s.PSP.ProviderForDisplay(ctx, p.ProviderID)
	if err != nil {
		return PaymentAttempt{}, err
	}
	v := PaymentAttempt{ID: p.ID, DonationIntentID: p.DonationIntentID, AttemptNumber: p.AttemptSeq, Method: p.Method,
		MethodCategory: psp.MethodCategory(p.Method), Status: p.Status, Provider: prov.Ref(), FailureCode: p.FailureCode,
		Message: donorMessage(p.Status), NextAction: NextActionView{Type: psp.NextNone}, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	if p.Status != psp.StatusRequiresAction {
		return v, nil
	}
	var sid, kind, status string
	var redirect, instr *string
	var exp time.Time
	err = q.QueryRow(ctx, `SELECT id, kind, status, redirect_url, instructions_code, expires_at FROM app.payment_sessions
		WHERE payment_intent_id = $1`, p.ID).Scan(&sid, &kind, &status, &redirect, &instr, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return PaymentAttempt{}, err
	}
	if status != "OPEN" || !exp.After(s.now()) {
		return v, nil
	}
	types := map[string]string{"REDIRECT": psp.NextRedirect, "HANDSET_APPROVAL": psp.NextAwaitHandset, "INSTRUCTIONS": psp.NextShowInstructions}
	v.NextAction = NextActionView{Type: types[kind], SessionID: &sid, RedirectURL: redirect, InstructionsCode: instr, ExpiresAt: &exp}
	return v, nil
}

func (s *Service) attemptsOf(ctx context.Context, donationID string) ([]PaymentAttempt, error) {
	rows, err := s.Pool.Query(ctx, paySelect+` WHERE donation_intent_id = $1 ORDER BY attempt_seq`, donationID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (payRow, error) { return scanPay(r) })
	if err != nil {
		return nil, err
	}
	out := make([]PaymentAttempt, 0, len(list))
	for _, p := range list {
		v, err := s.attemptView(ctx, s.Pool, p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ListAttempts lists a donor's attempts of one donation intent.
func (s *Service) ListAttempts(ctx context.Context, donorID, donationID string) ([]PaymentAttempt, error) {
	d, err := s.ownIntent(ctx, s.Pool, donorID, donationID, false)
	if err != nil {
		return nil, err
	}
	return s.attemptsOf(ctx, d.ID)
}

// StatusView is GET /donation-intents/{id}/status.
type StatusView struct {
	DonationIntentStatus string          `json:"donation_intent_status"`
	Attempt              *PaymentAttempt `json:"attempt"`
	Message              string          `json:"message"`
}

// Status returns the poll-friendly status of a donor's donation intent (latest attempt).
func (s *Service) Status(ctx context.Context, donorID, donationID string) (StatusView, error) {
	d, err := s.ownIntent(ctx, s.Pool, donorID, donationID, false)
	if err != nil {
		return StatusView{}, err
	}
	out := StatusView{DonationIntentStatus: d.Status}
	p, err := scanPay(s.Pool.QueryRow(ctx, paySelect+` WHERE donation_intent_id = $1 ORDER BY attempt_seq DESC LIMIT 1`, d.ID))
	switch {
	case errors.Is(err, ErrPaymentNotFound):
		out.Message = "No payment has been started for this donation."
	case err != nil:
		return StatusView{}, err
	default:
		v, err := s.attemptView(ctx, s.Pool, p)
		if err != nil {
			return StatusView{}, err
		}
		out.Attempt, out.Message = &v, v.Message
	}
	return out, nil
}

// CreateAttemptInput is POST /donation-intents/{id}/attempts.
type CreateAttemptInput struct {
	Method string `json:"method"`
}

// CreateAttempt starts a payment (payment-provider-interfaces §6): tx1 commits the payment CREATED and the CREATE
// call IN_FLIGHT; the provider call runs outside any transaction with an explicit deadline; tx2 classifies the
// outcome and applies it. A provider outcome is never a 5xx: the response carries the payment's real status
// (UNKNOWN after a timeout — never FAILED).
func (s *Service) CreateAttempt(ctx context.Context, donorID, donationID, idemKey, corr string, in CreateAttemptInput) (PaymentAttempt, error) {
	if !psp.ValidMethod(in.Method) {
		return PaymentAttempt{}, httpx.Validation(errs.Detail{Field: "method", Code: "INVALID_VALUE"})
	}
	if !ids.Valid(idemKey) {
		return PaymentAttempt{}, errs.New(errs.Invalid, "IDEMPOTENCY_KEY_REQUIRED", "An Idempotency-Key header is required for this request.")
	}
	d, err := s.ownIntent(ctx, s.Pool, donorID, donationID, false)
	if err != nil {
		return PaymentAttempt{}, err
	}
	// replay backstop (idempotency record expired)
	if p, err := scanPay(s.Pool.QueryRow(ctx, paySelect+` WHERE donation_intent_id = $1 AND idempotency_key = $2`, d.ID, idemKey)); err == nil {
		if p.Method != in.Method {
			return PaymentAttempt{}, errs.New(errs.Conflict, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was already used for a different request.")
		}
		return s.attemptView(ctx, s.Pool, p)
	} else if !errors.Is(err, ErrPaymentNotFound) {
		return PaymentAttempt{}, err
	}
	if err := s.checkDonor(ctx, donorID); err != nil {
		return PaymentAttempt{}, err
	}
	target, err := s.checkCampaign(ctx, d.CampaignID)
	if err != nil {
		return PaymentAttempt{}, err
	}
	if err := s.checkCurrency(ctx, target, d.Currency); err != nil {
		return PaymentAttempt{}, err
	}
	if err := s.checkLimits(ctx, d.Amount, d.Currency); err != nil {
		return PaymentAttempt{}, err
	}
	route, err := s.PSP.Route(ctx, psp.RouteQuery{Method: in.Method, Currency: d.Currency, AmountMinor: d.Amount, Simulated: d.Simulated})
	if err != nil {
		return PaymentAttempt{}, err
	}
	corr = corrOr(corr)
	var p payRow
	var attemptID string
	now := s.now()
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.ownIntent(ctx, tx, donorID, donationID, true) // serialises attempts of one donation
		if err != nil {
			return err
		}
		switch {
		case cur.Status == DonationPaid:
			return ErrAlreadyPaid
		case cur.Status != DonationOpen || !cur.ExpiresAt.After(now):
			return ErrIntentNotOpen
		}
		if err := s.blockingAttempt(ctx, tx, cur.ID); err != nil {
			return err
		}
		var seq int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(attempt_seq), 0) + 1 FROM app.payment_intents WHERE donation_intent_id = $1`,
			cur.ID).Scan(&seq); err != nil {
			return err
		}
		p = payRow{ID: ids.New(), DonationIntentID: cur.ID, CampaignID: cur.CampaignID, Currency: cur.Currency, Method: in.Method,
			ProviderID: route.Provider.ID, AccountID: route.Account.ID, CapabilityID: route.Capability.ID, Status: psp.StatusCreated,
			IdemKey: idemKey, Correlation: corr, Amount: cur.Amount, Simulated: cur.Simulated, AttemptSeq: seq, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO app.payment_intents (id, donation_intent_id, campaign_id, currency, amount_minor, method,
			is_simulated, provider_id, provider_account_id, capability_id, custody_model, capability_routable, routing_reason, attempt_seq,
			status, status_rank, idempotency_key, correlation_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			'CREATED', 0, $15, $16)`, p.ID, p.DonationIntentID, p.CampaignID, p.Currency, p.Amount, p.Method, p.Simulated, p.ProviderID,
			p.AccountID, p.CapabilityID, route.Capability.CustodyModel, route.Capability.Routable, route.Reason, seq, idemKey, corr); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, eventRow{subject: "PAYMENT_INTENT", donationID: p.DonationIntentID, paymentID: p.ID, to: psp.StatusCreated,
			applied: true, code: "P1", obs: Obs{Source: SrcDonor, ActorType: "DONOR", ActorID: donorID, Correlation: corr}}); err != nil {
			return err
		}
		attemptID, err = s.insertCall(ctx, tx, p, "CREATE", now, now.Add(CreateDeadline))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payments.attempt.created", ActorType: "user", ActorID: donorID,
			TargetType: "payment", TargetID: p.ID, Metadata: map[string]any{"donation_intent_id": p.DonationIntentID, "method": p.Method,
				"provider": route.Provider.Code, "simulated": p.Simulated}})
	})
	if err != nil {
		return PaymentAttempt{}, err
	}
	// the session id and its single-use return token exist before the call so the return URL can carry them;
	// only the token's hash is stored, and only if the provider asks the donor to act
	sessionID := ids.New()
	tok, err := crypto.NewToken()
	if err != nil {
		return PaymentAttempt{}, err
	}
	returnURL := strings.TrimRight(s.PublicSiteURL, "/") + "/donations/return?session=" + sessionID + "&token=" + url.QueryEscape(string(tok))
	start := s.Clock.Now()
	callCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), now.Add(CreateDeadline))
	obs := route.Adapter.CreatePayment(callCtx, psp.CallMeta{CorrelationID: corr, AttemptID: attemptID, Deadline: now.Add(CreateDeadline),
		Account: route.Account, ReferenceCreatedAt: now}, psp.CreatePaymentRequest{PaymentID: p.ID, AmountMinor: p.Amount,
		Currency: p.Currency, Method: p.Method, Description: "FundZim donation", ReturnURL: returnURL})
	cancel()
	s.PSP.RecordCall(ctx, route.Provider.ID, route.Provider.Code, psp.OpCreatePayment, p.Method, p.Currency, obs)
	// tx2 must run even if the client went away: the outcome is recorded either way
	ctx2 := context.WithoutCancel(ctx)
	var view PaymentAttempt
	err = db.WithTx(ctx2, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := lockPay(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if err := s.completeCall(ctx, tx, attemptID, obs); err != nil {
			return err
		}
		if err := s.applyCreate(ctx, tx, &cur, route, obs, attemptID, donorID, sessionID, string(tok)); err != nil {
			return err
		}
		view, err = s.attemptView(ctx, tx, cur)
		return err
	})
	s.PSP.Metrics.Duration.WithLabelValues("create").Observe(s.Clock.Now().Sub(start).Seconds())
	if err != nil {
		// the call happened but its outcome could not be recorded: the in-flight sweeper will mark the payment
		// UNKNOWN (CRASH_DURING_CREATE) and the status poll resolves it. Report that state, never a failure.
		s.Logger.Error("payments: record create outcome failed (sweeper will resolve)", "payment_id", p.ID, "err", err)
		return PaymentAttempt{ID: p.ID, DonationIntentID: p.DonationIntentID, AttemptNumber: p.AttemptSeq, Method: p.Method,
			MethodCategory: psp.MethodCategory(p.Method), Status: psp.StatusUnknown, Provider: route.Provider.Ref(),
			Message: donorMessage(psp.StatusUnknown), NextAction: NextActionView{Type: psp.NextNone}, CreatedAt: p.CreatedAt, UpdatedAt: s.now()}, nil
	}
	s.m.attempts.WithLabelValues(p.Method, view.Status).Inc()
	return view, nil
}

// applyCreate applies a CreatePayment observation (classification → transition, payment-provider-interfaces §5).
func (s *Service) applyCreate(ctx context.Context, tx pgx.Tx, p *payRow, route psp.Route, obs psp.Observation, attemptID, donorID,
	sessionID, token string) error {
	base := Obs{AttemptID: attemptID, Correlation: p.Correlation, ProviderRef: obs.ProviderRef, RawStatus: obs.RawStatus,
		Mapped: obs.Mapped, MappingVersion: obs.MappingVersion, ReportedAmount: obs.ReportedAmount, ReportedCcy: obs.ReportedCcy,
		PayerToken: obs.PayerToken, PayerMasked: obs.PayerMasked, OccurredAt: obs.OccurredAt}
	switch obs.Class {
	case psp.ClassAccepted:
		o := base
		o.Source, o.Target = SrcSync, obs.Canonical
		out, err := s.apply(ctx, tx, p, o)
		if err != nil {
			return err
		}
		if out.Applied && p.Status == psp.StatusRequiresAction && obs.Next.Type != psp.NextNone && obs.Next.Type != "" {
			return s.openSession(ctx, tx, p, route, obs.Next, donorID, sessionID, token)
		}
		return nil
	case psp.ClassDefiniteFailure:
		o := base
		o.Source, o.Target, o.FailureCode = SrcSync, psp.StatusFailed, nonEmpty(obs.FailureCode, "DECLINED")
		if o.RawStatus == "" {
			o.Mapped = true
		}
		_, err := s.apply(ctx, tx, p, o)
		return err
	case psp.ClassRetryableBeforeSend, psp.ClassRateLimited, psp.ClassNotSupported, psp.ClassInvalidRequest:
		// nothing reached the provider (or our request was invalid): a definite pre-submit failure (P4, INTERNAL)
		code := map[psp.Class]string{psp.ClassRetryableBeforeSend: "PROVIDER_UNAVAILABLE_NOT_SENT", psp.ClassRateLimited: "PROVIDER_RATE_LIMITED_NOT_SENT",
			psp.ClassNotSupported: "METHOD_NOT_SUPPORTED", psp.ClassInvalidRequest: "INVALID_REQUEST"}[obs.Class]
		_, err := s.apply(ctx, tx, p, Obs{Source: SrcInternal, Target: psp.StatusFailed, FailureCode: code, AttemptID: attemptID,
			Mapped: true, Correlation: p.Correlation, Reason: "the request did not reach the provider"})
		return err
	default: // INDETERMINATE, AUTH_CONFIG_ERROR (no documented no-processing guarantee), NOT_FOUND on create
		reason := nonEmpty(obs.UnknownReason, psp.UnknownMalformed)
		_, err := s.apply(ctx, tx, p, Obs{Source: SrcInternal, Target: psp.StatusUnknown, UnknownReason: reason, AttemptID: attemptID,
			Mapped: true, Correlation: p.Correlation, Reason: "provider outcome unknown: " + reason})
		return err
	}
}

// insertCall commits a provider call row IN_FLIGHT (before the call).
func (s *Service) insertCall(ctx context.Context, tx pgx.Tx, p payRow, op string, start, deadline time.Time) (string, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(attempt_no), 0) + 1 FROM app.payment_attempts WHERE payment_intent_id = $1`, p.ID).Scan(&n); err != nil {
		return "", err
	}
	id := ids.New()
	_, err := tx.Exec(ctx, `INSERT INTO app.payment_attempts (id, payment_intent_id, attempt_no, operation, provider_id, our_reference,
		correlation_id, started_at, deadline_at) VALUES ($1, $2, $3, $4, $5, $2, $6, $7, $8)`, id, p.ID, n, op, p.ProviderID, p.Correlation, start, deadline)
	return id, err
}

var classification = map[psp.Class]string{
	psp.ClassAccepted: "ACCEPTED", psp.ClassDefiniteFailure: "REJECTED", psp.ClassRetryableBeforeSend: "DEFINITELY_NOT_SENT",
	psp.ClassIndeterminate: "OUTCOME_UNKNOWN", psp.ClassNotFound: "NOT_FOUND", psp.ClassAuthConfig: "AUTH_CONFIG_ERROR",
	psp.ClassRateLimited: "RATE_LIMITED", psp.ClassNotSupported: "NOT_SUPPORTED", psp.ClassInvalidRequest: "REJECTED",
}

// completeCall records a call's result exactly once (app.attempt_complete_once).
func (s *Service) completeCall(ctx context.Context, tx pgx.Tx, attemptID string, obs psp.Observation) error {
	cl, ok := classification[obs.Class]
	if !ok {
		cl = "OUTCOME_UNKNOWN"
	}
	var unknown *string
	if cl == "OUTCOME_UNKNOWN" {
		r := nonEmpty(obs.UnknownReason, psp.UnknownMalformed)
		unknown = &r
	}
	var errClass *string
	if obs.Class != psp.ClassAccepted {
		c := string(obs.Class)
		errClass = &c
	}
	var http *int
	if obs.HTTPStatus > 0 {
		h := obs.HTTPStatus
		http = &h
	}
	raw := obs.RawStatus
	if len(raw) > 200 {
		raw = raw[:200]
	}
	lat := int(obs.Latency.Milliseconds())
	_, err := tx.Exec(ctx, `UPDATE app.payment_attempts SET completed_at = $2, classification = $3, error_class = $4, unknown_reason = $5,
		http_status = $6, provider_status_raw = nullif($7, ''), provider_reference_returned = nullif($8, ''), latency_ms = $9
		WHERE id = $1 AND classification = 'IN_FLIGHT'`, attemptID, s.now(), cl, errClass, unknown, http, raw, obs.ProviderRef, lat)
	return err
}

// openSession records the donor's next step. A redirect URL must be https on a host the adapter declares; an
// invalid one is never shown (open-redirect protection) and is logged for operations.
func (s *Service) openSession(ctx context.Context, tx pgx.Tx, p *payRow, route psp.Route, next psp.NextAction, donorID, sessionID,
	token string) error {
	kind := map[string]string{psp.NextRedirect: "REDIRECT", psp.NextAwaitHandset: "HANDSET_APPROVAL", psp.NextShowInstructions: "INSTRUCTIONS"}[next.Type]
	if kind == "" {
		return nil
	}
	var redirect *string
	if kind == "REDIRECT" {
		if !safeRedirect(next.RedirectURL, route.Adapter.RedirectHosts()) {
			s.Logger.Warn("payments: provider redirect URL refused (ALERT)", "payment_id", p.ID, "provider", route.Provider.Code)
			return nil
		}
		redirect = &next.RedirectURL
	}
	var instr *string
	if next.InstructionsCode != "" && len(next.InstructionsCode) <= 64 {
		instr = &next.InstructionsCode
	}
	exp := s.now().Add(SessionTTL)
	if next.ExpiresAt != nil && next.ExpiresAt.After(s.now()) && next.ExpiresAt.Before(exp.Add(24*time.Hour)) {
		exp = *next.ExpiresAt
	}
	// the return token is bound to this session and donor, single use; only its hash is stored
	_, err := tx.Exec(ctx, `INSERT INTO app.payment_sessions (id, payment_intent_id, donor_user_id, kind, status, redirect_url, instructions_code,
		return_token_hash, expires_at) VALUES ($1, $2, $3, $4, 'OPEN', $5, $6, $7, $8)`,
		sessionID, p.ID, donorID, kind, redirect, instr, crypto.HashToken(token), exp)
	return err
}

// safeRedirect allows only https URLs on the adapter's declared hosts, without credentials or odd ports.
func safeRedirect(raw string, hosts []string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || len(raw) > 2048 || u.Path == "" {
		return false
	}
	for _, h := range hosts {
		if strings.EqualFold(u.Hostname(), h) {
			return true
		}
	}
	return false
}

// ReturnFromSession handles a donor coming back from a hosted page. It NEVER confirms a payment: it consumes
// the single-use token, marks the session RETURNED and schedules an authenticated status query.
func (s *Service) ReturnFromSession(ctx context.Context, donorID, sessionID, token string) (PaymentAttempt, error) {
	if !ids.Valid(sessionID) || !crypto.ValidTokenFormat(token) {
		return PaymentAttempt{}, ErrSessionInvalid
	}
	var view PaymentAttempt
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var pid, donor, status string
		var hash []byte
		var exp time.Time
		err := tx.QueryRow(ctx, `SELECT payment_intent_id, donor_user_id, status, return_token_hash, expires_at FROM app.payment_sessions
			WHERE id = $1 FOR UPDATE`, sessionID).Scan(&pid, &donor, &status, &hash, &exp)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionInvalid
		}
		if err != nil {
			return err
		}
		if donor != donorID || status != "OPEN" || !crypto.Equal(hash, crypto.HashToken(token)) {
			return ErrSessionInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE app.payment_sessions SET status = 'RETURNED', returned_at = $2 WHERE id = $1`, sessionID, s.now()); err != nil {
			return err
		}
		p, err := lockPay(ctx, tx, pid)
		if err != nil {
			return err
		}
		if !terminal(p.Status) {
			if _, err := tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = $2 WHERE id = $1`, p.ID, s.now()); err != nil {
				return err
			}
		}
		view, err = s.attemptView(ctx, tx, p)
		return err
	})
	return view, err
}
