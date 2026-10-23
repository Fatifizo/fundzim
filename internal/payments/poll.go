package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// errBusy means another process holds the payment row (webhook or another poll): try again later.
var errBusy = errors.New("payments: payment is being processed")

// PollPayment runs one authenticated status query by OUR reference and applies the answer (source POLL). It never
// writes FAILED on a timeout or an error; NOT_FOUND becomes FAILED only with documented finality; after the
// UNKNOWN horizon the payment needs reconciliation and automatic polling stops.
//
// Stage 19 (KI-S18-22, ADR-051): polling is bounded for every awaiting status. A payment still CREATED, PENDING,
// REQUIRES_ACTION or AUTHORISED past PollHorizon (from creation), or any payment that has had MaxStatusPolls status
// queries, keeps its status — an exhausted budget is not evidence of failure — gets a POLL_BUDGET_EXHAUSTED
// reconciliation reference (needs_reconciliation) and is not polled again; a later webhook or reconciliation
// evidence still resolves it through apply(). PollPayment is the explicit query (staff, tests): it runs whenever the
// budget allows; the sweep's routine poll also re-checks under the row lock that the payment is still due.
func (s *Service) PollPayment(ctx context.Context, paymentID string) (string, error) {
	return s.poll(ctx, paymentID, false)
}

func (s *Service) poll(ctx context.Context, paymentID string, routine bool) (string, error) {
	var p payRow
	var callID string
	var skip bool
	now := s.now()
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = scanPay(tx.QueryRow(ctx, paySelect+` WHERE id = $1 FOR UPDATE SKIP LOCKED`, paymentID))
		if errors.Is(err, ErrPaymentNotFound) {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payment_intents WHERE id = $1)`, paymentID).Scan(&exists)
			if exists {
				return errBusy
			}
			return ErrPaymentNotFound
		}
		if err != nil {
			return err
		}
		if terminal(p.Status) && p.Status != psp.StatusFailed && p.Status != psp.StatusExpired && p.Status != psp.StatusCancelled {
			_, err := tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = NULL WHERE id = $1`, p.ID)
			skip = true
			return err
		}
		if routine && (p.NextPollAt == nil || p.NextPollAt.After(now)) {
			skip = true // another worker polled it (or it stopped) after the sweep listed it: never a second query
			return nil
		}
		if p.Status == psp.StatusUnknown && p.PollHorizonAt != nil && now.After(*p.PollHorizonAt) {
			skip = true
			reason := reconUnknownHorizon
			var lastNotFound bool
			_ = tx.QueryRow(ctx, `SELECT classification = 'NOT_FOUND' FROM app.payment_attempts WHERE payment_intent_id = $1
				AND operation = 'GET_STATUS' AND classification <> 'IN_FLIGHT' ORDER BY attempt_no DESC LIMIT 1`, p.ID).Scan(&lastNotFound)
			if lastNotFound {
				reason = reconNotFoundHorizon
			}
			if err := s.openRecon(ctx, tx, &p, reason, Obs{Correlation: p.Correlation}); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = NULL WHERE id = $1`, p.ID)
			return err
		}
		var polls int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND operation = 'GET_STATUS'`,
			p.ID).Scan(&polls); err != nil {
			return err
		}
		if polls >= MaxStatusPolls || (p.Status != psp.StatusUnknown && now.After(p.CreatedAt.Add(PollHorizon))) {
			skip = true
			if err := s.openRecon(ctx, tx, &p, reconPollBudget, Obs{Correlation: p.Correlation}); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = NULL WHERE id = $1`, p.ID)
			return err
		}
		callID, err = s.insertCall(ctx, tx, p, "GET_STATUS", now, now.Add(StatusDeadline))
		if err != nil {
			return err
		}
		lease := now.Add(StatusDeadline + InFlightGrace)
		_, err = tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = $2, poll_attempts = poll_attempts + 1 WHERE id = $1`, p.ID, lease)
		return err
	})
	if err != nil || skip {
		return p.Status, err
	}
	prov, err := s.PSP.ProviderByID(ctx, p.ProviderID)
	if err != nil {
		return p.Status, err
	}
	adapter, ok := s.PSP.Adapter(prov.Code)
	acct, aerr := s.PSP.AccountByID(ctx, p.AccountID)
	var obs psp.Observation
	if !ok || aerr != nil {
		// the adapter is not registered here (e.g. sandbox disabled): nothing was sent
		obs = psp.Observation{Class: psp.ClassRetryableBeforeSend}
	} else {
		start := s.Clock.Now()
		callCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), now.Add(StatusDeadline))
		obs = adapter.GetPayment(callCtx, psp.CallMeta{CorrelationID: p.Correlation, AttemptID: callID, Deadline: now.Add(StatusDeadline),
			Account: acct, ReferenceCreatedAt: p.CreatedAt}, p.ID)
		cancel()
		s.PSP.RecordCall(ctx, prov.ID, prov.Code, psp.OpGetPayment, p.Method, p.Currency, obs)
		s.PSP.Metrics.Duration.WithLabelValues("status_poll").Observe(s.Clock.Now().Sub(start).Seconds())
	}
	recreate := false
	err = db.WithTx(context.WithoutCancel(ctx), s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := lockPay(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if err := s.completeCall(ctx, tx, callID, obs); err != nil {
			return err
		}
		switch obs.Class {
		case psp.ClassAccepted:
			if _, err := s.apply(ctx, tx, &cur, Obs{Source: SrcPoll, Target: obs.Canonical, RawStatus: obs.RawStatus, Mapped: obs.Mapped,
				MappingVersion: obs.MappingVersion, ProviderRef: obs.ProviderRef, ReportedAmount: obs.ReportedAmount, ReportedCcy: obs.ReportedCcy,
				PayerToken: obs.PayerToken, PayerMasked: obs.PayerMasked, OccurredAt: obs.OccurredAt, AttemptID: callID,
				FailureCode: obs.FailureCode}); err != nil {
				return err
			}
		case psp.ClassNotFound:
			if obs.NotFoundFinal && (cur.Status == psp.StatusUnknown || cur.Status == psp.StatusCreated) {
				if _, err := s.apply(ctx, tx, &cur, Obs{Source: SrcPoll, Target: psp.StatusFailed, RawStatus: "not_found", Mapped: true,
					FailureCode: "NOT_CREATED_AT_PROVIDER", AttemptID: callID,
					Reason: "provider documents that the reference was never created"}); err != nil {
					return err
				}
			} else if cur.Status == psp.StatusUnknown && cur.PollAttempts >= 2 {
				c, err := s.PSP.CapabilityByID(ctx, cur.CapabilityID)
				if err != nil {
					return err
				}
				recreate = c.IdempotentCreate // PCR-009: re-create only when idempotent creation is verified
			}
		}
		if !terminal(cur.Status) || cur.Status == psp.StatusFailed || cur.Status == psp.StatusExpired || cur.Status == psp.StatusCancelled {
			// keep watching non-terminal payments; FAILED/EXPIRED/CANCELLED stop (a late success arrives by webhook)
			var next *time.Time
			if !terminal(cur.Status) {
				n := s.now().Add(nextPollDelay(cur.PollAttempts))
				next = &n
			}
			if _, err := tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = $2 WHERE id = $1`, cur.ID, next); err != nil {
				return err
			}
		}
		p = cur
		return nil
	})
	if err != nil {
		return p.Status, err
	}
	if recreate && ok && aerr == nil {
		return s.recreate(ctx, p, prov, adapter, acct)
	}
	return p.Status, nil
}

// recreate re-sends CreatePayment with the SAME reference (never a new one) after a not-found while UNKNOWN,
// only for capabilities with verified idempotent creation.
func (s *Service) recreate(ctx context.Context, p payRow, prov psp.Provider, adapter psp.Adapter, acct psp.Account) (string, error) {
	now := s.now()
	var callID string
	if err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := lockPay(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if cur.Status != psp.StatusUnknown {
			return errBusy
		}
		callID, err = s.insertCall(ctx, tx, cur, "CREATE", now, now.Add(CreateDeadline))
		return err
	}); err != nil {
		if errors.Is(err, errBusy) {
			return p.Status, nil
		}
		return p.Status, err
	}
	callCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), now.Add(CreateDeadline))
	obs := adapter.CreatePayment(callCtx, psp.CallMeta{CorrelationID: p.Correlation, AttemptID: callID, Deadline: now.Add(CreateDeadline),
		Account: acct, ReferenceCreatedAt: p.CreatedAt}, psp.CreatePaymentRequest{PaymentID: p.ID, AmountMinor: p.Amount, Currency: p.Currency,
		Method: p.Method, Description: "FundZim donation"})
	cancel()
	s.PSP.RecordCall(ctx, prov.ID, prov.Code, psp.OpCreatePayment, p.Method, p.Currency, obs)
	var status string
	err := db.WithTx(context.WithoutCancel(ctx), s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := lockPay(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if err := s.completeCall(ctx, tx, callID, obs); err != nil {
			return err
		}
		if obs.Class == psp.ClassAccepted {
			if _, err := s.apply(ctx, tx, &cur, Obs{Source: SrcSync, Target: obs.Canonical, RawStatus: obs.RawStatus, Mapped: obs.Mapped,
				MappingVersion: obs.MappingVersion, ProviderRef: obs.ProviderRef, AttemptID: callID}); err != nil {
				return err
			}
		}
		status = cur.Status
		return nil
	})
	return status, err
}

// RequestStatusQuery is the staff action: an audited, immediate status query. It never sets a status itself.
func (s *Service) RequestStatusQuery(ctx context.Context, staffID, paymentID, reason string) (string, error) {
	reason, ok := cleanReason(reason)
	if !ok {
		return "", invalidReason("reason")
	}
	if !ids.Valid(paymentID) {
		return "", ErrPaymentNotFound
	}
	if err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payment_intents WHERE id = $1)`, paymentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrPaymentNotFound
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payments.status_query.requested", ActorType: "staff", ActorID: staffID,
			TargetType: "payment", TargetID: paymentID, Metadata: map[string]any{"reason": reason}})
	}); err != nil {
		return "", err
	}
	st, err := s.PollPayment(ctx, paymentID)
	if errors.Is(err, errBusy) {
		return st, nil
	}
	return st, err
}

// Sweep is the periodic maintenance (every 5 s): crashed in-flight calls, due polls, donation-intent expiry and
// overdue parked events.
func (s *Service) Sweep(ctx context.Context) error {
	if err := s.sweepInFlight(ctx); err != nil {
		return fmt.Errorf("in-flight: %w", err)
	}
	// routine polls only for payments of this process's execution environment (Stage 9: one environment per process;
	// Stage 19: a process without that environment's adapters would spend the payment's poll budget on queries it
	// cannot send)
	rows, err := s.Pool.Query(ctx, `SELECT id FROM app.payment_intents WHERE next_poll_at <= $1 AND execution_environment = $2
		AND status IN ('CREATED','UNKNOWN','PENDING','REQUIRES_ACTION','AUTHORISED') ORDER BY next_poll_at LIMIT 25`, s.now(), s.ExecEnv())
	if err != nil {
		return err
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range due {
		if _, err := s.poll(ctx, id, true); err != nil && !errors.Is(err, errBusy) {
			s.Logger.Warn("payments: status poll failed", "payment_id", id, "err", err)
		}
	}
	if err := s.expireIntents(ctx); err != nil {
		return fmt.Errorf("expire: %w", err)
	}
	return s.overdueParked(ctx)
}

// sweepInFlight resolves provider calls still IN_FLIGHT past their deadline + grace (a crash between commit and
// result): the call becomes OUTCOME_UNKNOWN / CRASH_DURING_CREATE and a CREATED payment becomes UNKNOWN.
func (s *Service) sweepInFlight(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, payment_intent_id, operation FROM app.payment_attempts
		WHERE classification = 'IN_FLIGHT' AND deadline_at < $1 ORDER BY deadline_at LIMIT 50`, s.now().Add(-InFlightGrace))
	if err != nil {
		return err
	}
	type stale struct{ id, pid, op string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stale, error) {
		var x stale
		err := r.Scan(&x.id, &x.pid, &x.op)
		return x, err
	})
	if err != nil {
		return err
	}
	for _, x := range list {
		err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			p, err := scanPay(tx.QueryRow(ctx, paySelect+` WHERE id = $1 FOR UPDATE SKIP LOCKED`, x.pid))
			if err != nil {
				return err
			}
			if err := s.completeCall(ctx, tx, x.id, psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownCrashDuring}); err != nil {
				return err
			}
			if x.op == "CREATE" && p.Status == psp.StatusCreated {
				_, err = s.apply(ctx, tx, &p, Obs{Source: SrcInternal, Target: psp.StatusUnknown, UnknownReason: psp.UnknownCrashDuring,
					AttemptID: x.id, Mapped: true, Reason: "provider call outcome not recorded (process crash or database failure)"})
				return err
			}
			if !terminal(p.Status) {
				_, err = tx.Exec(ctx, `UPDATE app.payment_intents SET next_poll_at = $2 WHERE id = $1`, p.ID, s.now())
			}
			return err
		})
		if err != nil && !errors.Is(err, ErrPaymentNotFound) {
			return err
		}
	}
	return nil
}

// expireIntents expires OPEN donation intents past their window that have no live, unknown or paid attempt.
func (s *Service) expireIntents(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT d.id FROM app.donation_intents d WHERE d.status = 'OPEN' AND d.expires_at < $1
		AND NOT EXISTS (SELECT 1 FROM app.payment_intents p WHERE p.donation_intent_id = d.id
		                AND p.status IN ('CREATED','PENDING','REQUIRES_ACTION','AUTHORISED','UNKNOWN'))
		ORDER BY d.expires_at LIMIT 100`, s.now())
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range list {
		if err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var st, corr string
			if err := tx.QueryRow(ctx, `SELECT status, correlation_id FROM app.donation_intents WHERE id = $1 FOR UPDATE`, id).Scan(&st, &corr); err != nil {
				return err
			}
			if st != DonationOpen {
				return nil
			}
			if err := s.insertEvent(ctx, tx, eventRow{subject: "DONATION_INTENT", donationID: id, from: DonationOpen, to: DonationExpired,
				applied: true, obs: Obs{Source: SrcInternal, Reason: "the donation window ended", Correlation: corr}}); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE app.donation_intents SET status = 'EXPIRED', version = version + 1 WHERE id = $1`, id)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// overdueParked raises a reconciliation reference for events parked past their deadline (never discarded).
func (s *Service) overdueParked(ctx context.Context) error {
	list, err := s.PSP.OverdueParked(ctx, 50)
	if err != nil {
		return err
	}
	for _, ev := range list {
		if !ids.Valid(ev.OurReference) {
			continue
		}
		if err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			p, err := scanPay(tx.QueryRow(ctx, paySelect+` WHERE id = $1 FOR UPDATE SKIP LOCKED`, ev.OurReference))
			if err != nil {
				return err
			}
			return s.openRecon(ctx, tx, &p, reconParkedDeadline, Obs{InboxID: ev.ID, ProviderEventID: ev.ProviderEventID})
		}); err != nil && !errors.Is(err, ErrPaymentNotFound) {
			return err
		}
	}
	return nil
}

// ---- River periodic job ------------------------------------------------------------------------------------

// KindSweep is the periodic sweep job kind.
const KindSweep = "payments.sweep"

// SweepArgs is the (empty) job argument.
type SweepArgs struct{}

// Kind implements river.JobArgs.
func (SweepArgs) Kind() string { return KindSweep }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Second}}
}

// SweepWorker runs Sweep.
type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Service *Service
}

// Timeout implements river.Worker.
func (w *SweepWorker) Timeout(*river.Job[SweepArgs]) time.Duration { return 2 * time.Minute }

// Work implements river.Worker.
func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	return w.Service.Sweep(ctx)
}

// PeriodicJobs runs the sweep every 5 seconds.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(5*time.Second),
		func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil },
		&river.PeriodicJobOpts{ID: KindSweep, RunOnStart: true})}
}
