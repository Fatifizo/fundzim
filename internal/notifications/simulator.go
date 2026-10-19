package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Simulator is a TEST_ONLY provider for one channel (email_simulator, sms_simulator, whatsapp_simulator). It sends
// nothing anywhere: it records the message, answers deterministically from the destination and later reports status
// through the same signed webhook path a real provider would use. Scenario selection:
//
//   - email: a "+sim-<scenario>" tag in the local part (alice+sim-bounce@example.test);
//   - SMS / WhatsApp: the destination number's last two digits.
//
// The signing scheme (HMAC-SHA256 over "<unix>.<body>", header X-FundZim-Sim-Signature: t=<unix>,v1=<hex>) is FundZim's
// own; a real provider's scheme must come from its documentation (PROVIDER_CONFIRMATION_REQUIRED).
type Simulator struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	code    string
	channel string
	Key     []byte
	// CallbackDelay before the first callback (default 1 s).
	CallbackDelay time.Duration
}

// NewSimulator returns the simulator for a channel.
func NewSimulator(pool *pgxpool.Pool, clk clock.Clock, channel string, key []byte) *Simulator {
	code := map[string]string{ChEmail: "email_simulator", ChSMS: "sms_simulator", ChWhatsApp: "whatsapp_simulator"}[channel]
	return &Simulator{Pool: pool, Clock: clk, code: code, channel: channel, Key: key}
}

func (s *Simulator) Code() string    { return s.code }
func (s *Simulator) Channel() string { return s.channel }
func (s *Simulator) Simulated() bool { return true }
func (s *Simulator) Capabilities() Capabilities {
	return Capabilities{DeliveryStatus: true, Webhooks: true, HTML: s.channel == ChEmail, ReadReceipts: s.channel == ChWhatsApp,
		FreeForm: false, IdempotencyKey: true}
}
func (s *Simulator) ValidateConfiguration() error {
	if s.code == "" || len(s.Key) < 32 || s.Pool == nil {
		return errors.New("notifications: simulator misconfigured")
	}
	return nil
}

// Scenarios.
const (
	ScDelivered     = "DELIVERED"
	ScBounced       = "BOUNCED"
	ScRejected      = "REJECTED"
	ScTempFailure   = "TEMPORARY_FAILURE"
	ScPermFailure   = "PERMANENT_FAILURE"
	ScTimeout       = "TIMEOUT"
	ScRateLimited   = "RATE_LIMITED"
	ScDuplicate     = "DUPLICATE_CALLBACK"
	ScOutOfOrder    = "OUT_OF_ORDER_CALLBACK"
	ScUndeliverable = "UNDELIVERABLE"
	ScFailed        = "MESSAGE_FAILED"
	ScInvalidHook   = "INVALID_WEBHOOK"
)

var emailScenarios = map[string]string{"bounce": ScBounced, "reject": ScRejected, "tempfail": ScTempFailure, "permfail": ScPermFailure,
	"timeout": ScTimeout, "ratelimit": ScRateLimited, "dupcb": ScDuplicate, "ooo": ScOutOfOrder}

// phoneScenarios: last two digits of the destination number.
var phoneScenarios = map[string]map[string]string{
	ChSMS:      {"91": ScUndeliverable, "92": ScRejected, "93": ScTimeout, "94": ScTempFailure, "95": ScDuplicate, "96": ScOutOfOrder, "98": ScRateLimited},
	ChWhatsApp: {"91": ScFailed, "92": ScRejected, "93": ScTimeout, "94": ScTempFailure, "95": ScDuplicate, "96": ScOutOfOrder, "97": ScInvalidHook, "98": ScRateLimited},
}

// ScenarioFor returns the scenario a destination selects.
func ScenarioFor(channel, to string) string {
	if channel == ChEmail {
		local := to
		if at := strings.LastIndexByte(to, '@'); at > 0 {
			local = to[:at]
		}
		if i := strings.Index(local, "+sim-"); i >= 0 {
			tag := local[i+5:]
			if j := strings.IndexAny(tag, "-."); j >= 0 {
				tag = tag[:j]
			}
			if sc, ok := emailScenarios[tag]; ok {
				return sc
			}
		}
		return ScDelivered
	}
	if len(to) >= 2 {
		if sc, ok := phoneScenarios[channel][to[len(to)-2:]]; ok {
			return sc
		}
	}
	return ScDelivered
}

type simCallback struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	BadSig  bool   `json:"bad_signature,omitempty"`
	DelayMs int    `json:"delay_ms"`
}

// plan is what a scenario reports synchronously and later.
func (s *Simulator) plan(sc string) (sync string, sendErr *SendError, callbacks []simCallback) {
	ev := func(st string, ms int) simCallback { return simCallback{EventID: ids.New(), Status: st, DelayMs: ms} }
	first := "ACCEPTED"
	if s.channel == ChWhatsApp {
		first = "SUBMITTED"
	}
	switch sc {
	case ScRejected:
		return "", &SendError{Class: ClassPermanent, Code: "PROVIDER_REJECTED", InvalidDestination: true}, nil
	case ScPermFailure:
		return "", &SendError{Class: ClassPermanent, Code: "PROVIDER_PERMANENT_FAILURE"}, nil
	case ScTempFailure:
		return "", &SendError{Class: ClassTemporary, Code: "PROVIDER_TEMPORARY_FAILURE"}, nil
	case ScRateLimited:
		return "", &SendError{Class: ClassRateLimited, Code: "PROVIDER_RATE_LIMITED", RetryAfter: 2 * time.Minute}, nil
	case ScTimeout:
		// the simulator received it but the answer is lost: a status lookup finds it ACCEPTED
		return first, &SendError{Class: ClassTimeout, Code: "PROVIDER_TIMEOUT"}, []simCallback{ev("DELIVERED", 0)}
	case ScBounced:
		return first, nil, []simCallback{ev("BOUNCED", 0)}
	case ScUndeliverable:
		return first, nil, []simCallback{ev("UNDELIVERABLE", 0)}
	case ScFailed:
		return first, nil, []simCallback{ev("FAILED", 0)}
	case ScDuplicate:
		d := ev("DELIVERED", 0)
		return first, nil, []simCallback{d, d}
	case ScOutOfOrder:
		// delivered (and read) reported before the acceptance it follows
		cbs := []simCallback{ev("DELIVERED", 0)}
		if s.channel == ChWhatsApp {
			cbs = []simCallback{ev("READ", 0), ev("DELIVERED", 100)}
		}
		return first, nil, append(cbs, ev("ACCEPTED", 200))
	case ScInvalidHook:
		c := ev("DELIVERED", 0)
		c.BadSig = true
		return first, nil, []simCallback{c}
	}
	cbs := []simCallback{ev("DELIVERED", 0)}
	if s.channel == ChWhatsApp {
		cbs = append(cbs, ev("READ", 200))
	}
	return first, nil, cbs
}

// Send records the message (once per idempotency key) and answers with the scenario.
func (s *Simulator) Send(ctx context.Context, m Outbound) (SendResult, error) {
	if inTx(ctx) {
		return SendResult{}, ErrInTransaction
	}
	if m.Channel != s.channel || m.To == "" || m.IdempotencyKey == "" {
		return SendResult{}, &SendError{Class: ClassPermanent, Code: "INVALID_MESSAGE"}
	}
	sc := ScenarioFor(s.channel, m.To)
	sync, sendErr, cbs := s.plan(sc)
	if sync == "" { // refused before acceptance: nothing is recorded
		return SendResult{}, sendErr
	}
	msgID := "sim_" + strings.ReplaceAll(ids.New(), "-", "")
	delay := s.CallbackDelay
	if delay <= 0 {
		delay = time.Second
	}
	raw, _ := json.Marshal(cbs)
	var existing string
	err := s.Pool.QueryRow(ctx, `INSERT INTO app.notification_sim_messages (id, provider_code, provider_message_id, idempotency_key, scenario,
		pending_callbacks, next_callback_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider_code, idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key RETURNING provider_message_id`,
		ids.New(), s.code, msgID, m.IdempotencyKey, sc, raw, s.Clock.Now().Add(delay)).Scan(&existing)
	if err != nil {
		return SendResult{}, &SendError{Class: ClassTemporary, Code: "SIMULATOR_STORE_FAILED"}
	}
	if sendErr != nil {
		return SendResult{}, sendErr
	}
	return SendResult{Status: sync, ProviderMessageID: existing}, nil
}

// Status finds a message by idempotency key (the answer to a timed-out send).
func (s *Simulator) Status(ctx context.Context, key string) (StatusResult, error) {
	var msgID string
	err := s.Pool.QueryRow(ctx, `SELECT provider_message_id FROM app.notification_sim_messages WHERE provider_code = $1 AND idempotency_key = $2`,
		s.code, key).Scan(&msgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return StatusResult{Found: false}, nil
	}
	if err != nil {
		return StatusResult{}, err
	}
	st := "ACCEPTED"
	if s.channel == ChWhatsApp {
		st = "SUBMITTED"
	}
	return StatusResult{Found: true, Status: st, ProviderMessageID: msgID}, nil
}

const simSigHeader = "X-FundZim-Sim-Signature"

// SimWebhookWindow is the accepted callback age (and clock skew).
const SimWebhookWindow = 5 * time.Minute

func (s *Simulator) sign(key []byte, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

type simPayload struct {
	EventID    string    `json:"event_id"`
	MessageID  string    `json:"message_id"`
	Status     string    `json:"status"`
	OccurredAt time.Time `json:"occurred_at"`
}

var simStatuses = map[string]bool{"SUBMITTED": true, "ACCEPTED": true, "DELIVERED": true, "READ": true, "BOUNCED": true, "REJECTED": true,
	"UNDELIVERABLE": true, "FAILED": true, "TEMPORARY_FAILURE": true}

// VerifyWebhook checks the signature and timestamp, then the payload.
func (s *Simulator) VerifyWebhook(h http.Header, body []byte, now time.Time) (Callback, error) {
	parts := map[string]string{}
	for _, kv := range strings.Split(h.Get(simSigHeader), ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
			parts[k] = v
		}
	}
	ts, err := strconv.ParseInt(parts["t"], 10, 64)
	if err != nil || parts["v1"] == "" {
		return Callback{}, ErrWebhookSignature
	}
	if !hmac.Equal([]byte(s.sign(s.Key, ts, body)), []byte(fmt.Sprintf("t=%d,v1=%s", ts, parts["v1"]))) {
		return Callback{}, ErrWebhookSignature
	}
	at := time.Unix(ts, 0)
	if at.Before(now.Add(-SimWebhookWindow)) || at.After(now.Add(SimWebhookWindow)) {
		return Callback{}, ErrWebhookTimestamp
	}
	var p simPayload
	if len(body) > 4096 || json.Unmarshal(body, &p) != nil || !ids.Valid(p.EventID) || !strings.HasPrefix(p.MessageID, "sim_") ||
		len(p.MessageID) > 80 || !simStatuses[p.Status] || p.OccurredAt.IsZero() {
		return Callback{}, ErrWebhookPayload
	}
	return Callback{ProviderEventID: p.EventID, ProviderMessageID: p.MessageID, Status: p.Status, OccurredAt: p.OccurredAt}, nil
}

// Signed builds a signed callback (also used by tests to forge or replay callbacks deliberately).
func (s *Simulator) Signed(eventID, messageID, status string, at time.Time, key []byte) (http.Header, []byte) {
	body, _ := json.Marshal(simPayload{EventID: eventID, MessageID: messageID, Status: status, OccurredAt: at.UTC()})
	h := http.Header{}
	h.Set(simSigHeader, s.sign(key, at.Unix(), body))
	h.Set("Content-Type", "application/json")
	return h, body
}

// DeliverCallbacks sends due simulated callbacks to deliver (the engine's webhook entry point). Returns how many.
func (s *Simulator) DeliverCallbacks(ctx context.Context, limit int, deliver func(ctx context.Context, provider string, h http.Header, body []byte) error) (int, error) {
	n := 0
	for n < limit {
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return n, err
		}
		var id, msgID string
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT id::text, provider_message_id, pending_callbacks FROM app.notification_sim_messages
			WHERE provider_code = $1 AND next_callback_at <= $2 ORDER BY next_callback_at LIMIT 1 FOR UPDATE SKIP LOCKED`, s.code, s.Clock.Now()).
			Scan(&id, &msgID, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return n, nil
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return n, err
		}
		var cbs []simCallback
		_ = json.Unmarshal(raw, &cbs)
		if len(cbs) == 0 {
			_, err = tx.Exec(ctx, `UPDATE app.notification_sim_messages SET next_callback_at = NULL WHERE id = $1`, id)
			if err := commitOr(ctx, tx, err); err != nil {
				return n, err
			}
			continue
		}
		cb, rest := cbs[0], cbs[1:]
		key := s.Key
		if cb.BadSig {
			key = []byte("not-the-simulator-key-not-the-simulator-key")
		}
		h, body := s.Signed(cb.EventID, msgID, cb.Status, s.Clock.Now(), key)
		_ = deliver(ctx, s.code, h, body) // refusals are recorded by the receiver; the simulator moves on like a provider
		next := any(nil)
		if len(rest) > 0 {
			next = s.Clock.Now().Add(time.Duration(rest[0].DelayMs) * time.Millisecond)
		}
		rawRest, _ := json.Marshal(rest)
		_, err = tx.Exec(ctx, `UPDATE app.notification_sim_messages SET pending_callbacks = $2, next_callback_at = $3 WHERE id = $1`, id, rawRest, next)
		if err := commitOr(ctx, tx, err); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func commitOr(ctx context.Context, tx pgx.Tx, err error) error {
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
