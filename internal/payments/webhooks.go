package payments

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// ApplyWebhook is the psp handler for PAYMENT events (payment-state-machine §7 step 7): one transaction applies
// the event through apply and completes, parks or marks the inbox row unrecognised. An error schedules a retry.
func (s *Service) ApplyWebhook(ctx context.Context, ev psp.InboxEvent) error {
	adapter, ok := s.PSP.Adapter(ev.ProviderCode)
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if !ok {
			return s.PSP.MarkUnrecognised(ctx, tx, ev.ID, "ADAPTER_NOT_REGISTERED")
		}
		pid := ev.OurReference
		if !ids.Valid(pid) && ev.ProviderReference != "" {
			err := tx.QueryRow(ctx, `SELECT payment_intent_id::text FROM app.payment_provider_references
				WHERE provider_id = $1 AND ref_kind = 'PAYMENT' AND reference = $2`, ev.ProviderID, ev.ProviderReference).Scan(&pid)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		p, err := lockPay(ctx, tx, pid)
		if errors.Is(err, ErrPaymentNotFound) || (err == nil && (p.ProviderID != ev.ProviderID || p.Simulated != ev.Simulated)) {
			return s.PSP.MarkUnrecognised(ctx, tx, ev.ID, "UNKNOWN_PAYMENT_REFERENCE")
		}
		if err != nil {
			return err
		}
		canonical, mapped, version := adapter.MapPaymentStatus(ev.RawStatus)
		out, err := s.apply(ctx, tx, &p, Obs{Source: SrcWebhook, Target: canonical, RawStatus: ev.RawStatus, Mapped: mapped,
			MappingVersion: version, ProviderRef: ev.ProviderReference, ReportedAmount: ev.ReportedAmount, ReportedCcy: ev.ReportedCcy,
			ProviderEventID: ev.ProviderEventID, InboxID: ev.ID, OccurredAt: ev.OccurredAt, Correlation: p.Correlation})
		if err != nil {
			return err
		}
		switch {
		case out.Unrecognised:
			return s.PSP.MarkUnrecognised(ctx, tx, ev.ID, "UNMAPPED_PROVIDER_STATUS")
		case out.Parked:
			return s.PSP.Park(ctx, tx, ev.ID, p.ID, psp.StatusSucceeded)
		}
		return s.PSP.MarkProcessed(ctx, tx, ev.ID)
	})
}
