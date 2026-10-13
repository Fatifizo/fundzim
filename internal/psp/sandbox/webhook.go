package sandbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// SignatureHeader carries "t=<unix seconds>,kid=<current|previous>,v1=<hex HMAC-SHA256(secret, t + "." + body)>".
// This is the SIMULATOR's scheme only; real providers are verified with their own documented procedure (PCR-017).
const SignatureHeader = "FundZim-Sandbox-Signature"

// DefaultReplayWindow applies when the account has none configured.
const DefaultReplayWindow = 300 * time.Second

// Payload is the simulator's webhook body.
type Payload struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Created   int64       `json:"created"`
	Livemode  bool        `json:"livemode"`
	Simulated bool        `json:"simulated"`
	Data      PayloadData `json:"data"`
}

// PayloadData is the event's payment data. The payer is masked at the source.
type PayloadData struct {
	PaymentReference  string `json:"payment_reference"`
	ProviderPaymentID string `json:"provider_payment_id"`
	Status            string `json:"status"`
	AmountMinor       string `json:"amount_minor"`
	Currency          string `json:"currency"`
	PayerMasked       string `json:"payer_masked,omitempty"`
}

// Sign returns the signature header value for body at time t with the given key slot.
func Sign(secret []byte, kid string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + ts + ",kid=" + kid + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

var sigRE = regexp.MustCompile(`^t=([0-9]{1,12}),kid=([a-z]{1,16}),v1=([0-9a-f]{64})$`)

// Verify implements psp.WebhookVerifier: constant-time HMAC comparison with the slot named by kid (current or
// previous, for rotation overlap) and a timestamp inside the account's replay window.
func (a *Adapter) Verify(ctx context.Context, acct psp.Account, in psp.Inbound) (psp.VerifiedBy, error) {
	h := strings.TrimSpace(in.Header.Get(SignatureHeader))
	if h == "" {
		return psp.VerifiedBy{}, psp.ErrSignatureMissing
	}
	m := sigRE.FindStringSubmatch(h)
	if m == nil {
		return psp.VerifiedBy{}, psp.ErrSignatureInvalid
	}
	var ref, slot string
	switch m[2] {
	case "current":
		ref, slot = acct.WebhookSecretRef, "CURRENT"
	case "previous":
		ref, slot = acct.WebhookSecretRefPrevious, "PREVIOUS"
	default:
		return psp.VerifiedBy{}, psp.ErrUnknownKey
	}
	if ref == "" {
		return psp.VerifiedBy{}, psp.ErrUnknownKey
	}
	secret, err := a.Secrets.Resolve(ctx, ref)
	if err != nil {
		return psp.VerifiedBy{}, psp.ErrUnknownKey
	}
	ts, _ := strconv.ParseInt(m[1], 10, 64)
	want := Sign(secret, m[2], time.Unix(ts, 0), in.Body)
	got := "t=" + m[1] + ",kid=" + m[2] + ",v1=" + m[3]
	if !hmac.Equal([]byte(want), []byte(got)) {
		return psp.VerifiedBy{}, psp.ErrSignatureInvalid
	}
	win := acct.ReplayWindow
	if win <= 0 {
		win = DefaultReplayWindow
	}
	if d := in.ReceivedAt.Sub(time.Unix(ts, 0)); d > win || d < -win {
		return psp.VerifiedBy{}, psp.ErrTimestamp
	}
	return psp.VerifiedBy{Method: "SIGNATURE", KeySlot: slot}, nil
}

var digitsRE = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var evtRE = regexp.MustCompile(`^sbx_evt_[0-9a-f]{20}$`)

// Parse implements psp.WebhookVerifier.
func (a *Adapter) Parse(_ context.Context, in psp.Inbound) ([]psp.WebhookEvent, error) {
	var p Payload
	dec := json.NewDecoder(bytes.NewReader(in.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, psp.ErrMalformed
	}
	if !evtRE.MatchString(p.ID) || p.Type == "" || len(p.Type) > 64 || p.Data.Status == "" || len(p.Data.Status) > 40 {
		return nil, psp.ErrMalformed
	}
	class := "OTHER"
	if strings.HasPrefix(p.Type, "payment.") {
		class = "PAYMENT"
	}
	ev := psp.WebhookEvent{ProviderEventID: p.ID, Class: class, TypeRaw: p.Type, ProviderReference: p.Data.ProviderPaymentID,
		OurReference: p.Data.PaymentReference, RawStatus: p.Data.Status, Simulated: p.Simulated && !p.Livemode}
	if p.Created > 0 {
		t := time.Unix(p.Created, 0).UTC()
		ev.OccurredAt = &t
	}
	if p.Data.AmountMinor != "" {
		if !digitsRE.MatchString(p.Data.AmountMinor) {
			return nil, psp.ErrMalformed
		}
		n, _ := strconv.ParseInt(p.Data.AmountMinor, 10, 64)
		ev.ReportedAmount, ev.ReportedCcy = &n, p.Data.Currency
	}
	// allow-listed, masked fields only
	ev.Redacted = map[string]any{"id": p.ID, "type": p.Type, "created": p.Created, "livemode": p.Livemode, "simulated": p.Simulated,
		"data": map[string]any{"payment_reference": p.Data.PaymentReference, "provider_payment_id": p.Data.ProviderPaymentID,
			"status": p.Data.Status, "amount_minor": p.Data.AmountMinor, "currency": p.Data.Currency, "payer_masked": p.Data.PayerMasked}}
	return []psp.WebhookEvent{ev}, nil
}

// statusRank orders simulated provider states so a replayed earlier status does not move the provider back.
var statusRank = map[string]int{"pending": 1, "action_required": 2, "failed": 3, "declined": 3, "expired": 3, "cancelled": 3,
	"paid": 4, "partially_refunded": 5, "refunded": 6}

// DeliverDue signs and POSTs due scheduled webhooks to FundZim's endpoint (at-least-once: a lease guards the
// HTTP call so it never runs inside a transaction; a crash re-sends, and the inbox dedupes by event id).
func (a *Adapter) DeliverDue(ctx context.Context, batch int) (int, error) {
	type due struct {
		ID, PaymentID, EventID, Type, Status, Currency, SignWith, Our, Ref string
		Amount                                                             int64
		Offset, Attempts                                                   int
	}
	var list []due
	now := a.now()
	err := db.WithTx(ctx, a.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT w.id, w.sandbox_payment_id, w.provider_event_id, w.event_type, w.raw_status, w.currency, w.sign_with,
			p.our_reference::text, p.provider_payment_ref, w.amount_minor, w.timestamp_offset_seconds, w.attempts
			FROM app.sandbox_scheduled_webhooks w JOIN app.sandbox_provider_payments p ON p.id = w.sandbox_payment_id
			WHERE w.delivered_at IS NULL AND w.due_at <= $1 ORDER BY w.due_at, w.id LIMIT $2 FOR UPDATE OF w SKIP LOCKED`, now, batch)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
			err := r.Scan(&d.ID, &d.PaymentID, &d.EventID, &d.Type, &d.Status, &d.Currency, &d.SignWith, &d.Our, &d.Ref, &d.Amount, &d.Offset, &d.Attempts)
			return d, err
		})
		if err != nil {
			return err
		}
		for _, d := range list { // lease: not picked again for 30 s
			if _, err := tx.Exec(ctx, `UPDATE app.sandbox_scheduled_webhooks SET due_at = $2, attempts = attempts + 1 WHERE id = $1`,
				d.ID, now.Add(30*time.Second)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, d := range list {
		// the simulated provider's own state moves when the event happens (never backwards; unmapped states
		// such as "limbo" are taken as they come)
		var cur string
		_ = a.Pool.QueryRow(ctx, `SELECT raw_status FROM app.sandbox_provider_payments WHERE id = $1`, d.PaymentID).Scan(&cur)
		if r, ok := statusRank[d.Status]; !ok || r > statusRank[cur] {
			_, _ = a.Pool.Exec(ctx, `UPDATE app.sandbox_provider_payments SET raw_status = $2 WHERE id = $1`, d.PaymentID, d.Status)
		}
		code := 0
		if d.SignWith != "none" {
			code = a.post(ctx, d.EventID, d.Type, d.Status, d.Our, d.Ref, d.Amount, d.Currency, d.SignWith, d.Offset)
		}
		if code == 0 && d.SignWith != "none" && d.Attempts < 5 {
			continue // network failure: retried after the lease
		}
		if _, err := a.Pool.Exec(ctx, `UPDATE app.sandbox_scheduled_webhooks SET delivered_at = $2, delivery_status = $3 WHERE id = $1`,
			d.ID, a.now(), code); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// post builds, signs and sends one simulated webhook; it returns the HTTP status (0 on a network error).
func (a *Adapter) post(ctx context.Context, eventID, typ, status, our, ref string, amount int64, ccy, signWith string, offset int) int {
	body, _ := json.Marshal(Payload{ID: eventID, Type: typ, Created: a.now().Unix(), Livemode: false, Simulated: true,
		Data: PayloadData{PaymentReference: our, ProviderPaymentID: ref, Status: status, AmountMinor: strconv.FormatInt(amount, 10),
			Currency: ccy, PayerMasked: "****1234"}})
	at := a.now().Add(time.Duration(offset) * time.Second)
	var secret []byte
	kid := "current"
	switch signWith {
	case "previous":
		kid = "previous"
		secret, _ = a.Secrets.Resolve(ctx, "secretref://fundzim/sandbox/webhook-previous")
	case "wrong":
		secret = []byte("this-is-not-the-sandbox-webhook-secret-0000")
	default:
		secret, _ = a.Secrets.Resolve(ctx, "secretref://fundzim/sandbox/webhook-current")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "fundzim-sandbox-simulator/1")
	req.Header.Set(SignatureHeader, Sign(secret, kid, at, body))
	resp, err := a.HTTP.Do(req)
	if err != nil {
		a.Logger.Warn("sandbox: webhook delivery failed", "event_id", eventID, "err", err)
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// ---- River periodic job ------------------------------------------------------------------------------------

// KindDeliver is the periodic delivery job kind.
const KindDeliver = "sandbox.deliver_webhooks"

// DeliverArgs is the (empty) periodic job argument.
type DeliverArgs struct{}

// Kind implements river.JobArgs.
func (DeliverArgs) Kind() string { return KindDeliver }

// InsertOpts implements river.JobArgsWithInsertOpts: one pending run at a time.
func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Second}}
}

// DeliverWorker runs DeliverDue.
type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	Adapter *Adapter
}

// Timeout implements river.Worker.
func (w *DeliverWorker) Timeout(*river.Job[DeliverArgs]) time.Duration { return 30 * time.Second }

// Work implements river.Worker.
func (w *DeliverWorker) Work(ctx context.Context, _ *river.Job[DeliverArgs]) error {
	if _, err := w.Adapter.DeliverDue(ctx, 50); err != nil {
		return fmt.Errorf("sandbox: deliver: %w", err)
	}
	return nil
}

// PeriodicJobs runs delivery every second.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Second),
		func() (river.JobArgs, *river.InsertOpts) { return DeliverArgs{}, nil },
		&river.PeriodicJobOpts{ID: KindDeliver, RunOnStart: true})}
}
