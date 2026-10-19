//go:build integration

package integration

// Stage 15 notification engine (ADR-047) against the real stack. The tests build an in-process engine on the server's
// composition with the TEST_ONLY simulators active for every external channel; the compose worker (dev SMTP / Mailpit
// active, simulators registered) shares the database and may process the same deliveries — assertions tolerate either
// worker. Every recipient is a test account; nothing leaves the machine.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
)

type notifIT struct {
	s   *itServer
	f   *verifStaff
	n   *notifications.Service
	mig *pgxpool.Pool
}

func newNotifIT(t *testing.T) *notifIT {
	t.Helper()
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	n := app.NewNotifications(app.NotificationDeps{Config: s.d.Config, Pool: s.d.DB, Clock: clock.System, Logger: s.d.Logger, Modules: s.d.Verification,
		Orgs: s.d.Orgs, Auth: s.d.Auth, WorkerID: "it-notifications-" + ids.New()[:8]})
	for _, ch := range []string{notifications.ChEmail, notifications.ChSMS, notifications.ChWhatsApp} {
		code := map[string]string{notifications.ChEmail: "email_simulator", notifications.ChSMS: "sms_simulator", notifications.ChWhatsApp: "whatsapp_simulator"}[ch]
		p := n.Providers[code]
		if p == nil {
			t.Fatalf("simulator %s not built (NOTIFICATIONS_SIMULATOR_ENABLED)", code)
		}
		p.(*notifications.Simulator).CallbackDelay = 10 * time.Millisecond
		n.Active[ch] = p
	}
	st := notifications.DefaultSettings()
	st.BackoffBase, st.BackoffCap, st.UnknownGrace = 200*time.Millisecond, 400*time.Millisecond, 200*time.Millisecond
	n.Settings = st
	return &notifIT{s: s, f: f, n: n, mig: pool(t, migrationURL())}
}

func migrationURL() string { return os.Getenv("DATABASE_MIGRATION_URL") }

// drive runs sweeps and simulator callbacks until cond holds.
func (x *notifIT) drive(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deliver := func(c2 context.Context, provider string, h http.Header, body []byte) error {
		_, err := x.n.HandleWebhook(c2, provider, h, body)
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := x.n.Sweep(ctx(t)); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		for _, p := range x.n.Providers {
			if sim, ok := p.(*notifications.Simulator); ok {
				if _, err := sim.DeliverCallbacks(ctx(t), 100, deliver); err != nil {
					t.Fatalf("callbacks: %v", err)
				}
			}
		}
		if cond() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type delivery struct {
	ID, Status, Reason, Provider string
	Attempts                     int
}

func (x *notifIT) deliveries(t *testing.T, recipient, key string) map[string]delivery {
	t.Helper()
	rows, err := x.mig.Query(ctx(t), `SELECT d.channel, d.id::text, d.status, coalesce(d.suppression_reason, ''), coalesce(d.provider_code, ''), d.attempt_count
		FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id WHERE n.recipient_user_id = $1 AND n.template_key = $2
		ORDER BY d.created_at`, recipient, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]delivery{}
	for rows.Next() {
		var ch string
		var d delivery
		if err := rows.Scan(&ch, &d.ID, &d.Status, &d.Reason, &d.Provider, &d.Attempts); err != nil {
			t.Fatal(err)
		}
		out[ch] = d
	}
	return out
}

func (x *notifIT) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := x.mig.QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// notify creates one notification directly (no source event) and returns its deliveries.
func (x *notifIT) notify(t *testing.T, key, category, recipient string, vars map[string]string, synthetic bool) map[string]delivery {
	t.Helper()
	if _, err := x.n.Notify(ctx(t), "it:"+ids.New(), notifications.Intent{TemplateKey: key, Category: category, Recipients: []string{recipient},
		Variables: vars, Synthetic: synthetic}); err != nil {
		t.Fatalf("notify %s: %v", key, err)
	}
	return x.deliveries(t, recipient, key)
}

// taggedUser is a verified user whose e-mail carries a simulator scenario tag (or none) and whose phone ends in lastTwo.
func (x *notifIT) taggedUser(t *testing.T, tag, lastTwo string) vUser {
	t.Helper()
	prefix := "nt"
	if tag != "" {
		prefix = "nt+sim-" + tag
	}
	u := verifiedUser(t, x.s, prefix)
	if lastTwo != "" {
		phone := fmt.Sprintf("+26377%05d%s", time.Now().UnixNano()%100000, lastTwo)
		tx, err := x.s.d.DB.Begin(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := users.SetVerifiedPhone(ctx(t), tx, u.id, phone, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx(t)); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

func (x *notifIT) act(t *testing.T, c staffCred, method, path string, body any, want int, code string) apiResp {
	t.Helper()
	b := x.s.staff(t, c)
	resp := b.do(method, path, body, "Idempotency-Key", ids.New())
	if resp.Status == 403 && resp.Error.Code == "STEP_UP_REQUIRED" {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
		resp = b.do(method, path, body, "Idempotency-Key", ids.New())
	}
	if want != 0 {
		b.expect(resp, want, code)
	}
	return resp
}

// TestNotificationsOutboxCreatesOnceAndDelivers: a real domain event reaches the user through the outbox and the compose
// worker (in-app + e-mail to Mailpit); creation is exactly once however often the event is delivered.
func TestNotificationsOutboxCreatesOnceAndDelivers(t *testing.T) {
	x := newNotifIT(t)
	u := basicUser(t, x.s, "ntout")
	r := createCampaign(t, u, "Help Rudo with school fees ntout", "EDUCATION", nil)
	u.expect(r, 201, "")
	waitFor(t, 45*time.Second, "CAMPAIGN_CREATED notification", func() bool { return len(x.deliveries(t, u.id, "CAMPAIGN_CREATED")) > 0 })
	d := x.deliveries(t, u.id, "CAMPAIGN_CREATED")
	if d["IN_APP"].Status != "DELIVERED" || d["EMAIL"].Reason != "" {
		t.Fatalf("deliveries: %+v", d)
	}
	mailTo(t, u.email, "Your campaign draft was created")
	waitFor(t, 30*time.Second, "e-mail accepted", func() bool { st := x.deliveries(t, u.id, "CAMPAIGN_CREATED")["EMAIL"].Status; return st == "ACCEPTED" })
	// inbox through the API
	var inbox struct {
		Items []struct {
			ID, Title, Category string
			DeepLink            *string `json:"deep_link"`
		} `json:"items"`
	}
	if err := json.Unmarshal(u.do("GET", "/notifications?unread=true", nil).Data, &inbox); err != nil || len(inbox.Items) == 0 {
		t.Fatalf("inbox: %v %+v", err, inbox)
	}
	// the same outbox event again (at-least-once delivery): no second notification
	var evID, payload string
	if err := x.mig.QueryRow(ctx(t), `SELECT e.source_event_id::text, o.payload::text FROM app.notification_events e JOIN app.outbox_events o ON o.id = e.source_event_id
		JOIN app.notifications n ON n.event_id = e.id WHERE n.recipient_user_id = $1 AND n.template_key = 'CAMPAIGN_CREATED'`, u.id).Scan(&evID, &payload); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = x.n.Handle(ctx(t), outbox.Delivery{EventID: evID, EventType: "campaigns.created", AggregateType: "campaign", AggregateID: ids.New(),
				Payload: json.RawMessage(payload), OccurredAt: time.Now()})
		}()
	}
	wg.Wait()
	if n := x.count(t, `SELECT count(*) FROM app.notifications WHERE recipient_user_id = $1 AND template_key = 'CAMPAIGN_CREATED'`, u.id); n != 1 {
		t.Fatalf("%d notifications after 100 duplicate deliveries", n)
	}
	// 100 concurrent first deliveries of a new event: exactly one creation
	ev := ids.New()
	raw, _ := json.Marshal(map[string]any{"campaign_id": ids.New(), "owner_user_id": u.id})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = x.n.Handle(ctx(t), outbox.Delivery{EventID: ev, EventType: "campaigns.paused", AggregateType: "campaign", AggregateID: ids.New(), Payload: raw,
				OccurredAt: time.Now()})
		}()
	}
	wg.Wait()
	if n := x.count(t, `SELECT count(*) FROM app.notifications n JOIN app.notification_events e ON e.id = n.event_id WHERE e.source_event_id = $1`, ev); n != 1 {
		t.Fatalf("%d notifications from 100 concurrent deliveries", n)
	}
}

// TestNotificationsFinancialSafety: no success message from an intent, an UNKNOWN or failed payment or payout, or an
// unverified webhook; TEST money never leaves the app; the confirmation comes only from payments.payment_succeeded.
func TestNotificationsFinancialSafety(t *testing.T) {
	x := newNotifIT(t)
	u := x.taggedUser(t, "", "")
	handle := func(evType string, p map[string]any) {
		raw, _ := json.Marshal(p)
		if err := x.n.Handle(ctx(t), outbox.Delivery{EventID: ids.New(), EventType: evType, AggregateType: "payment", AggregateID: ids.New(), Payload: raw,
			OccurredAt: time.Now()}); err != nil {
			t.Fatalf("%s: %v", evType, err)
		}
	}
	pay := func(extra map[string]any) map[string]any {
		p := map[string]any{"payment_id": ids.New(), "donation_intent_id": ids.New(), "donor_user_id": u.id, "amount_minor": "2500", "currency": "USD",
			"test_payment": true, "simulated": true, "execution_environment": "LOCAL_SIMULATOR"}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	handle("payments.donation_intent_created", pay(nil))
	for _, to := range []string{"PENDING", "AUTHORISED", "SUCCEEDED"} {
		handle("payments.payment_status_changed", pay(map[string]any{"to": to}))
	}
	if n := x.count(t, `SELECT count(*) FROM app.notifications WHERE recipient_user_id = $1`, u.id); n != 0 {
		t.Fatalf("%d notifications from intents and non-terminal statuses", n)
	}
	handle("payments.payment_status_changed", pay(map[string]any{"to": "UNKNOWN"}))
	handle("payments.payment_status_changed", pay(map[string]any{"to": "FAILED"}))
	if n := x.count(t, `SELECT count(*) FROM app.notifications WHERE recipient_user_id = $1 AND template_key = 'DONATION_CONFIRMED'`, u.id); n != 0 {
		t.Fatal("an unknown or failed payment produced a confirmation")
	}
	// a forged payment webhook is refused by Stage 8 verification: nothing reaches the engine
	resp, err := http.Post(x.s.srv.URL+"/api/v1/webhooks/payments/sandbox", "application/json", strings.NewReader(`{"type":"payment.succeeded"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("forged webhook accepted: %d", resp.StatusCode)
	}
	handle("payments.payment_succeeded", pay(nil))
	d := x.deliveries(t, u.id, "DONATION_CONFIRMED")
	if d["IN_APP"].Status != "DELIVERED" || d["EMAIL"].Reason != "SYNTHETIC_SOURCE" {
		t.Fatalf("TEST confirmation must be in-app only: %+v", d)
	}
	var title string
	if err := x.mig.QueryRow(ctx(t), `SELECT inapp_title FROM app.notifications WHERE recipient_user_id = $1 AND template_key = 'DONATION_CONFIRMED'`, u.id).Scan(&title); err != nil ||
		!strings.HasPrefix(title, "[TEST]") {
		t.Fatalf("TEST label: %q %v", title, err)
	}
	for _, to := range []string{"UNKNOWN", "FAILED", "PROCESSING"} {
		raw, _ := json.Marshal(map[string]any{"payout_request_id": ids.New(), "requested_by": u.id, "to": to, "test_payout": true})
		_ = x.n.Handle(ctx(t), outbox.Delivery{EventID: ids.New(), EventType: "payouts.status_changed", AggregateType: "payout_request", AggregateID: ids.New(),
			Payload: raw, OccurredAt: time.Now()})
	}
	if n := x.count(t, `SELECT count(*) FROM app.notifications WHERE recipient_user_id = $1 AND template_key = 'PAYOUT_COMPLETED'`, u.id); n != 0 {
		t.Fatal("an unknown or failed payout produced a success")
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE n.recipient_user_id = $1 AND n.synthetic AND d.channel <> 'IN_APP' AND d.status <> 'SUPPRESSED'`, u.id); n != 0 {
		t.Fatalf("%d external deliveries of TEST money", n)
	}
}

// TestNotificationsConsentAndPreferences: preferences (mandatory security), consent grant / withdrawal / expiry /
// reconfirmation / missing, marketing suppression, unverified contacts, no fallback, consent changed before the send.
func TestNotificationsConsentAndPreferences(t *testing.T) {
	x := newNotifIT(t)
	u := x.taggedUser(t, "", "11")
	u.expect(u.do("PUT", "/notifications/preferences", map[string]any{"changes": []map[string]any{{"category": "SECURITY", "channel": "EMAIL", "enabled": false}}}),
		422, "NOTIFICATION_MANDATORY")
	u.expect(u.do("PUT", "/notifications/preferences", map[string]any{"changes": []map[string]any{{"category": "CAMPAIGN", "channel": "EMAIL", "enabled": false},
		{"category": "CAMPAIGN", "channel": "SMS", "enabled": true}}}), 200, "")
	d := x.notify(t, "CAMPAIGN_APPROVED", "CAMPAIGN", u.id, nil, false)
	if d["EMAIL"].Reason != "PREFERENCE_DISABLED" || d["SMS"].Reason != "TEMPLATE_CHANNEL_MISSING" || d["IN_APP"].Status != "DELIVERED" {
		t.Fatalf("preferences: %+v", d)
	}
	if len(d) != 4 {
		t.Fatalf("one delivery per policy channel, no fallback rows: %+v", d)
	}
	// SMS needs consent: PAYOUT_DESTINATION_CHANGED (SECURITY) has SMS text; SECURITY SMS is opt-in
	u.expect(u.do("PUT", "/notifications/preferences", map[string]any{"changes": []map[string]any{{"category": "SECURITY", "channel": "SMS", "enabled": true}}}), 200, "")
	d = x.notify(t, "PAYOUT_DESTINATION_CHANGED", "SECURITY", u.id, nil, false)
	if d["SMS"].Reason != "CONSENT_MISSING" || d["EMAIL"].Reason != "" {
		t.Fatalf("missing consent: %+v", d)
	}
	var consent struct {
		Status string `json:"status"`
	}
	r := u.do("POST", "/communications/consent", map[string]any{"channel": "SMS", "purpose": "SERVICE_MESSAGES", "granted": true})
	u.expect(r, 200, "")
	_ = json.Unmarshal(r.Data, &consent)
	if consent.Status != "GRANTED" {
		t.Fatalf("grant: %s", r.Data)
	}
	u.expect(u.do("POST", "/communications/consent", map[string]any{"channel": "EMAIL", "purpose": "MARKETING", "granted": true}), 422, "MARKETING_NOT_AVAILABLE")
	d2, _ := x.n.Notify(ctx(t), "it:"+ids.New(), notifications.Intent{TemplateKey: "PAYOUT_DESTINATION_CHANGED", Category: "SECURITY", Recipients: []string{u.id}})
	if d2 != 1 {
		t.Fatal("notify")
	}
	var queuedID string
	if err := x.mig.QueryRow(ctx(t), `SELECT d.id::text FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE n.recipient_user_id = $1 AND d.channel = 'SMS' AND d.status = 'QUEUED' ORDER BY d.created_at DESC LIMIT 1`, u.id).Scan(&queuedID); err != nil {
		t.Fatalf("SMS with consent not queued: %v", err)
	}
	// consent withdrawn before the send: re-checked at send time
	u.expect(u.do("POST", "/communications/consent", map[string]any{"channel": "SMS", "purpose": "SERVICE_MESSAGES", "granted": false}), 200, "")
	x.drive(t, "send-time consent check", func() bool {
		var st, reason string
		_ = x.mig.QueryRow(ctx(t), `SELECT status, coalesce(suppression_reason, '') FROM app.notification_deliveries WHERE id = $1`, queuedID).Scan(&st, &reason)
		return st == "SUPPRESSED" && reason == "CONSENT_WITHDRAWN"
	})
	// granted again, then expired, then under an older policy version
	u.expect(u.do("POST", "/communications/consent", map[string]any{"channel": "SMS", "purpose": "SERVICE_MESSAGES", "granted": true}), 200, "")
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.communication_consents SET expires_at = now() - interval '1 minute' WHERE user_id = $1 AND channel = 'SMS'`, u.id); err != nil {
		t.Fatal(err)
	}
	if d := x.notify(t, "PAYOUT_DESTINATION_CHANGED", "SECURITY", u.id, nil, false); x.lastSMS(t, u.id) != "CONSENT_EXPIRED" {
		t.Fatalf("expired: %+v", d)
	}
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.communication_consents SET expires_at = NULL, policy_version = 'consent-v0' WHERE user_id = $1 AND channel = 'SMS'`, u.id); err != nil {
		t.Fatal(err)
	}
	x.notify(t, "PAYOUT_DESTINATION_CHANGED", "SECURITY", u.id, nil, false)
	if r := x.lastSMS(t, u.id); r != "CONSENT_RECONFIRMATION_REQUIRED" {
		t.Fatalf("reconfirmation: %s", r)
	}
	if n := x.count(t, `SELECT count(*) FROM app.communication_consent_events e JOIN app.communication_consents c ON c.id = e.consent_id WHERE c.user_id = $1`, u.id); n < 3 {
		t.Fatalf("consent history: %d", n)
	}
	// marketing: suppressed on every channel whatever the records say
	if _, err := x.n.Notify(ctx(t), "it:"+ids.New(), notifications.Intent{TemplateKey: "CAMPAIGN_APPROVED", Category: "MARKETING", Recipients: []string{u.id}}); err != nil {
		t.Fatal(err)
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE n.recipient_user_id = $1 AND n.category = 'MARKETING' AND d.suppression_reason <> 'MARKETING_DISABLED'`, u.id); n != 0 {
		t.Fatal("marketing delivered")
	}
	// an unverified e-mail address never receives anything
	b := x.s.browser(t)
	email := uniqueEmail("ntunv")
	b.register(email, itPassword, "Unverified Person")
	var unv string
	if err := x.mig.QueryRow(ctx(t), `SELECT user_id::text FROM app.user_emails WHERE email = $1`, email).Scan(&unv); err != nil {
		t.Fatal(err)
	}
	if d := x.notify(t, "PASSWORD_CHANGED", "SECURITY", unv, nil, false); d["EMAIL"].Reason != "CONTACT_UNVERIFIED" || d["IN_APP"].Status != "DELIVERED" {
		t.Fatalf("unverified: %+v", d)
	}
	// marketing consent cannot be granted; the user sees NOT_REQUESTED and available=false
	var list struct {
		Items []struct {
			Channel, Purpose, Status string
			Available                bool `json:"available"`
		} `json:"items"`
	}
	_ = json.Unmarshal(u.do("GET", "/communications/consent", nil).Data, &list)
	for _, c := range list.Items {
		if c.Purpose == "MARKETING" && (c.Available || c.Status != "NOT_REQUESTED") {
			t.Fatalf("marketing consent: %+v", c)
		}
	}
}

func (x *notifIT) lastSMS(t *testing.T, user string) string {
	t.Helper()
	var r string
	_ = x.mig.QueryRow(ctx(t), `SELECT coalesce(d.suppression_reason, d.status) FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE n.recipient_user_id = $1 AND d.channel = 'SMS' ORDER BY d.created_at DESC LIMIT 1`, user).Scan(&r)
	return r
}

// TestNotificationsDeliveryScenarios: simulator outcomes end in the right state with evidence — delivered, bounce (and
// the destination suppressed), rejection, temporary failures to the dead letter, timeout resolved by a status lookup
// (no resend), duplicate and out-of-order callbacks, rate limiting.
func TestNotificationsDeliveryScenarios(t *testing.T) {
	x := newNotifIT(t)
	email := func(tag string) (vUser, delivery) {
		u := x.taggedUser(t, tag, "")
		return u, x.notify(t, "CAMPAIGN_APPROVED", "CAMPAIGN", u.id, nil, false)["EMAIL"]
	}
	status := func(id string) (string, string) {
		var st, code string
		_ = x.mig.QueryRow(ctx(t), `SELECT status, coalesce(failure_code, '') FROM app.notification_deliveries WHERE id = $1`, id).Scan(&st, &code)
		return st, code
	}
	_, ok := email("")
	x.drive(t, "delivered", func() bool { st, _ := status(ok.ID); return st == "DELIVERED" })
	ub, bounce := email("bounce")
	x.drive(t, "bounced", func() bool { st, _ := status(bounce.ID); return st == "FAILED_PERMANENT" })
	if n := x.count(t, `SELECT count(*) FROM app.notification_suppressions WHERE reason = 'HARD_BOUNCE' AND lifted_at IS NULL AND destination_hash =
		(SELECT destination_hash FROM app.notification_deliveries WHERE id = $1)`, bounce.ID); n != 1 {
		t.Fatal("bounced destination not suppressed")
	}
	if d := x.notify(t, "CAMPAIGN_REJECTED", "CAMPAIGN", ub.id, nil, false); d["EMAIL"].Reason != "DESTINATION_SUPPRESSED" {
		t.Fatalf("next message to a bounced address: %+v", d)
	}
	_, rej := email("reject")
	x.drive(t, "rejected", func() bool { st, _ := status(rej.ID); return st == "FAILED_PERMANENT" })
	_, temp := email("tempfail")
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.notification_deliveries SET max_attempts = 2 WHERE id = $1`, temp.ID); err != nil {
		t.Fatal(err)
	}
	x.drive(t, "temporary failures exhausted", func() bool {
		st, _ := status(temp.ID)
		if st == "FAILED_RETRYABLE" { // the compose worker may hold a longer backoff: make it due now
			_, _ = x.mig.Exec(ctx(t), `UPDATE app.notification_deliveries SET next_attempt_at = now() WHERE id = $1 AND status = 'FAILED_RETRYABLE'`, temp.ID)
		}
		return st == "FAILED_PERMANENT"
	})
	if n := x.count(t, `SELECT count(*) FROM app.notification_dead_letters WHERE delivery_id = $1 AND failure_category = 'RETRIES_EXHAUSTED' AND review_status = 'OPEN'`, temp.ID); n != 1 {
		t.Fatal("no dead letter after exhausted retries")
	}
	_, to := email("timeout")
	x.drive(t, "timeout resolved by status lookup", func() bool { st, _ := status(to.ID); return st == "ACCEPTED" || st == "DELIVERED" })
	if n := x.count(t, `SELECT count(*) FROM app.notification_sim_messages WHERE idempotency_key LIKE $1`, to.ID+":%"); n != 1 {
		t.Fatalf("a timed-out message was resent (%d simulator messages)", n)
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_attempts WHERE delivery_id = $1 AND outcome = 'STATUS_CHECK'`, to.ID); n < 1 {
		t.Fatal("no status lookup recorded")
	}
	_, dup := email("dupcb")
	x.drive(t, "duplicate callback", func() bool {
		return x.count(t, `SELECT count(*) FROM app.notification_provider_events WHERE delivery_id = $1`, dup.ID) >= 1 && func() bool { st, _ := status(dup.ID); return st == "DELIVERED" }()
	})
	time.Sleep(300 * time.Millisecond)
	x.drive(t, "callbacks drained", func() bool {
		return x.count(t, `SELECT count(*) FROM app.notification_sim_messages s JOIN app.notification_deliveries d ON d.provider_message_id = s.provider_message_id
			WHERE d.id = $1 AND s.next_callback_at IS NOT NULL`, dup.ID) == 0
	})
	if n := x.count(t, `SELECT count(*) FROM app.notification_provider_events WHERE delivery_id = $1`, dup.ID); n != 1 {
		t.Fatalf("a duplicate callback was stored twice: %d", n)
	}
	_, ooo := email("ooo")
	x.drive(t, "out-of-order callbacks", func() bool {
		return x.count(t, `SELECT count(*) FROM app.notification_provider_events WHERE delivery_id = $1`, ooo.ID) >= 2
	})
	if st, _ := status(ooo.ID); st != "DELIVERED" {
		t.Fatalf("out-of-order acceptance regressed the delivery: %s", st)
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_provider_events WHERE delivery_id = $1 AND not_applied_reason = 'LOWER_RANK'`, ooo.ID); n != 1 {
		t.Fatal("late acceptance not recorded as lower-rank evidence")
	}
	_, rl := email("ratelimit")
	x.drive(t, "rate limited", func() bool { st, _ := status(rl.ID); return st == "FAILED_RETRYABLE" })
	var next time.Time
	_ = x.mig.QueryRow(ctx(t), `SELECT next_attempt_at FROM app.notification_deliveries WHERE id = $1`, rl.ID).Scan(&next)
	if time.Until(next) < time.Minute {
		t.Fatalf("Retry-After not honoured: next attempt in %s", time.Until(next))
	}
	// SMS undeliverable (synthetic recipient) → failure and destination suppression
	us := x.taggedUser(t, "", "91")
	us.expect(us.do("PUT", "/notifications/preferences", map[string]any{"changes": []map[string]any{{"category": "SECURITY", "channel": "SMS", "enabled": true}}}), 200, "")
	us.expect(us.do("POST", "/communications/consent", map[string]any{"channel": "SMS", "purpose": "SERVICE_MESSAGES", "granted": true}), 200, "")
	sms := x.notify(t, "PAYOUT_DESTINATION_CHANGED", "SECURITY", us.id, nil, false)["SMS"]
	x.drive(t, "SMS undeliverable", func() bool { st, _ := status(sms.ID); return st == "FAILED_PERMANENT" })
}

// TestNotificationsWebhookSecurityAndCrashRecovery: forged, stale and unknown-provider callbacks are refused and
// counted; a worker that dies mid-send leaves an UNKNOWN outcome that is looked up, never resent blindly.
func TestNotificationsWebhookSecurityAndCrashRecovery(t *testing.T) {
	x := newNotifIT(t)
	sim := x.n.Providers["email_simulator"].(*notifications.Simulator)
	post := func(provider string, h http.Header, body []byte) int {
		req, _ := http.NewRequest("POST", x.s.srv.URL+"/api/v1/webhooks/notifications/"+provider, strings.NewReader(string(body)))
		for k, v := range h {
			req.Header[k] = v
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	before := x.count(t, `SELECT count(*) FROM app.notification_webhook_rejections`)
	h, body := sim.Signed(ids.New(), "sim_forged", "DELIVERED", time.Now(), []byte(strings.Repeat("x", 32)))
	if c := post("email_simulator", h, body); c != 401 {
		t.Fatalf("forged signature: %d", c)
	}
	h, body = sim.Signed(ids.New(), "sim_stale", "DELIVERED", time.Now().Add(-time.Hour), sim.Key)
	if c := post("email_simulator", h, body); c != 401 {
		t.Fatalf("stale timestamp: %d", c)
	}
	if c := post("no_such_provider", h, body); c != 404 {
		t.Fatalf("unknown provider: %d", c)
	}
	if after := x.count(t, `SELECT count(*) FROM app.notification_webhook_rejections`); after < before+3 {
		t.Fatalf("rejections not recorded: %d → %d", before, after)
	}
	// a real delivery; its callback replayed: one evidence row
	u := x.taggedUser(t, "", "")
	d := x.notify(t, "CAMPAIGN_APPROVED", "CAMPAIGN", u.id, nil, false)["EMAIL"]
	x.drive(t, "delivered", func() bool {
		return x.count(t, `SELECT count(*) FROM app.notification_deliveries WHERE id = $1 AND status = 'DELIVERED'`, d.ID) == 1
	})
	var msg string
	_ = x.mig.QueryRow(ctx(t), `SELECT provider_message_id FROM app.notification_deliveries WHERE id = $1`, d.ID).Scan(&msg)
	ev := ids.New()
	h, body = sim.Signed(ev, msg, "DELIVERED", time.Now(), sim.Key)
	if c := post("email_simulator", h, body); c != 200 {
		t.Fatalf("valid callback: %d", c)
	}
	if c := post("email_simulator", h, body); c != 200 {
		t.Fatalf("replayed callback: %d", c)
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_provider_events WHERE provider_event_id = $1`, ev); n != 1 {
		t.Fatalf("replayed callback stored %d times", n)
	}
	// evidence is append-only
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.notification_provider_events SET reported_status = 'READ' WHERE provider_event_id = $1`, ev); err == nil {
		t.Fatal("provider evidence edited")
	}
	// worker crash: a claim whose lease ran out becomes UNKNOWN, then is looked up (the simulator never got it → retry)
	u2 := x.taggedUser(t, "", "")
	d2 := x.notify(t, "CAMPAIGN_APPROVED", "CAMPAIGN", u2.id, nil, false)["EMAIL"]
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.notification_deliveries SET status = 'PROCESSING', attempt_count = 1, lease_until = now() - interval '1 second'
		WHERE id = $1 AND status = 'QUEUED'`, d2.ID); err != nil {
		t.Fatal(err)
	}
	x.drive(t, "crash recovery", func() bool {
		return x.count(t, `SELECT count(*) FROM app.notification_deliveries WHERE id = $1 AND status IN ('ACCEPTED','DELIVERED')`, d2.ID) == 1
	})
	if n := x.count(t, `SELECT count(*) FROM app.notification_attempts WHERE delivery_id = $1 AND outcome = 'STATUS_CHECK'`, d2.ID); n < 1 {
		t.Fatal("the unknown outcome was not looked up before resending")
	}
}

// TestNotificationsDeadLetterReplayAndTemplates: replay re-checks suppression and is single-winner; template versions
// are maker-checker with step-up, frozen after submission, and a new ACTIVE version is used by new notifications only.
func TestNotificationsDeadLetterReplayAndTemplates(t *testing.T) {
	x := newNotifIT(t)
	admin, comp, sup := x.f.ledgerAdmin, x.f.compliance, x.f.support
	u := x.taggedUser(t, "tempfail", "")
	d := x.notify(t, "CAMPAIGN_APPROVED", "CAMPAIGN", u.id, nil, false)["EMAIL"]
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.notification_deliveries SET max_attempts = 1 WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	x.drive(t, "dead letter", func() bool {
		return x.count(t, `SELECT count(*) FROM app.notification_dead_letters WHERE delivery_id = $1 AND review_status = 'OPEN'`, d.ID) == 1
	})
	var dl string
	_ = x.mig.QueryRow(ctx(t), `SELECT id::text FROM app.notification_dead_letters WHERE delivery_id = $1 AND review_status = 'OPEN'`, d.ID).Scan(&dl)
	path := "/admin/communications/dead-letters/" + dl + "/replay"
	if r := x.act(t, sup, "POST", path, map[string]string{"reason": "IT: support cannot replay"}, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("support replayed: %d", r.Status)
	}
	// suppressed destination: the replay is blocked and says why
	x.act(t, admin, "POST", "/admin/communications/suppressions", map[string]string{"user_id": u.id, "channel": "EMAIL", "reason": "USER_OPT_OUT"}, 201, "")
	var res struct {
		Replayed bool   `json:"replayed"`
		Reason   string `json:"blocked_reason"`
	}
	_ = json.Unmarshal(x.act(t, admin, "POST", path, map[string]string{"reason": "IT: try replay"}, 200, "").Data, &res)
	if res.Replayed || res.Reason != "DESTINATION_SUPPRESSED" {
		t.Fatalf("replay past a suppression: %+v", res)
	}
	var supID string
	_ = x.mig.QueryRow(ctx(t), `SELECT id::text FROM app.notification_suppressions WHERE user_id = $1 AND lifted_at IS NULL`, u.id).Scan(&supID)
	x.act(t, admin, "POST", "/admin/communications/suppressions/"+supID+"/lift", map[string]string{"reason": "IT: opt-out reversed by the user"}, 200, "")
	// concurrent replays: one winner
	var mu sync.Mutex
	won := 0
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := x.n.Replay(ctx(t), admin.id, dl, "IT concurrent replay")
			if err == nil && r.Replayed {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d concurrent replays won", won)
	}
	// templates: ADMIN drafts, COMPLIANCE approves; the maker cannot approve; content frozen after submission
	var tid string
	_ = x.mig.QueryRow(ctx(t), `SELECT id::text FROM app.notification_templates WHERE template_key = 'FEE_ADJUSTMENT_AWAITING_APPROVAL'`).Scan(&tid)
	x.act(t, admin, "POST", "/admin/communications/templates/"+tid+"/versions", map[string]any{"inapp_title": "A fee adjustment is waiting for a checker",
		"inapp_body": "Leak {{password}}"}, 422, "VALIDATION_FAILED")
	var v struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	r := x.act(t, admin, "POST", "/admin/communications/templates/"+tid+"/versions", map[string]any{"inapp_title": "A fee adjustment is waiting for a checker",
		"inapp_body": "A TEST fee adjustment needs a second person's decision (IT revision)."}, 201, "")
	_ = json.Unmarshal(r.Data, &v)
	tv := "/admin/communications/template-versions/" + v.ID
	x.act(t, admin, "POST", tv+"/submit", map[string]any{}, 200, "")
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.notification_template_versions SET inapp_body = 'tampered' WHERE id = $1`, v.ID); err == nil {
		t.Fatal("submitted content edited")
	}
	if r := x.act(t, admin, "POST", tv+"/approve", map[string]any{"reason": "IT: maker approving"}, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("the maker (without approve permission) approved: %d", r.Status)
	}
	x.act(t, comp, "POST", tv+"/approve", map[string]any{"reason": "IT: wording reviewed"}, 200, "")
	before := x.notify(t, "FEE_ADJUSTMENT_AWAITING_APPROVAL", "ADMINISTRATIVE", x.f.finance1.id, nil, false)
	_ = before
	// activation while notifications are being created: every notification references a version that was ACTIVE
	var wg2 sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			_, _ = x.n.Notify(ctx(t), "it:"+ids.New(), notifications.Intent{TemplateKey: "FEE_ADJUSTMENT_AWAITING_APPROVAL", Category: "ADMINISTRATIVE",
				Recipients: []string{x.f.finance1.id}, Staff: true})
		}()
	}
	x.act(t, comp, "POST", tv+"/activate", map[string]any{"reason": "IT: activate"}, 200, "")
	wg2.Wait()
	if n := x.count(t, `SELECT count(*) FROM app.notifications n JOIN app.notification_template_versions v ON v.id = n.template_version_id
		WHERE n.template_key = 'FEE_ADJUSTMENT_AWAITING_APPROVAL' AND v.status NOT IN ('ACTIVE','RETIRED')`); n != 0 {
		t.Fatal("a notification used a version that was never active")
	}
	x.notify(t, "FEE_ADJUSTMENT_AWAITING_APPROVAL", "ADMINISTRATIVE", x.f.finance1.id, nil, false)
	var body string
	_ = x.mig.QueryRow(ctx(t), `SELECT inapp_body FROM app.notifications WHERE recipient_user_id = $1 AND template_key = 'FEE_ADJUSTMENT_AWAITING_APPROVAL'
		ORDER BY created_at DESC LIMIT 1`, x.f.finance1.id).Scan(&body)
	if !strings.Contains(body, "IT revision") {
		t.Fatalf("new notifications must use the new ACTIVE version: %q", body)
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_template_versions v JOIN app.notification_templates t ON t.id = v.template_id
		WHERE t.template_key = 'FEE_ADJUSTMENT_AWAITING_APPROVAL' AND v.status = 'ACTIVE'`); n != 1 {
		t.Fatalf("%d active versions", n)
	}
}

// TestNotificationsInboxIsolationAndStaffRBAC: one person's notifications are invisible to another; unread counts and
// read-all; staff notifications from operations events; communications routes enforce their permissions.
func TestNotificationsInboxIsolationAndStaffRBAC(t *testing.T) {
	x := newNotifIT(t)
	a, b := x.taggedUser(t, "", ""), x.taggedUser(t, "", "")
	x.notify(t, "CAMPAIGN_APPROVED", "CAMPAIGN", a.id, nil, false)
	x.notify(t, "CAMPAIGN_PAUSED", "CAMPAIGN", a.id, nil, false)
	var page struct {
		Items []struct{ ID string } `json:"items"`
	}
	_ = json.Unmarshal(a.do("GET", "/notifications", nil).Data, &page)
	if len(page.Items) < 2 {
		t.Fatalf("inbox: %+v", page)
	}
	mine := page.Items[0].ID
	b.expect(b.do("POST", "/notifications/"+mine+"/read", nil), 404, "NOTIFICATION_NOT_FOUND")
	var cnt struct {
		Unread int `json:"unread"`
	}
	_ = json.Unmarshal(a.do("GET", "/notifications/unread-count", nil).Data, &cnt)
	if cnt.Unread < 2 {
		t.Fatalf("unread: %d", cnt.Unread)
	}
	a.expect(a.do("POST", "/notifications/"+mine+"/read", nil), 200, "")
	a.expect(a.do("POST", "/notifications/read-all", nil), 200, "")
	_ = json.Unmarshal(a.do("GET", "/notifications/unread-count", nil).Data, &cnt)
	if cnt.Unread != 0 {
		t.Fatalf("after read-all: %d", cnt.Unread)
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE n.recipient_user_id = $1 AND d.channel = 'IN_APP' AND d.status <> 'READ'`, a.id); n != 0 {
		t.Fatal("in-app deliveries not marked read")
	}
	// staff: a support case assigned to someone else notifies them in-app (operations event → outbox → compose worker)
	sup := x.f.support
	c := x.act(t, sup, "POST", "/admin/support/cases", map[string]any{"subject": "IT notification case", "category": "GENERAL", "requester_user_id": a.id}, 201, "")
	caseID := c.field(t, "id")
	waitFor(t, 45*time.Second, "requester notified of the case", func() bool { return len(x.deliveries(t, a.id, "SUPPORT_CASE_OPENED")) > 0 })
	if d := x.deliveries(t, a.id, "SUPPORT_CASE_OPENED"); d["IN_APP"].Status != "DELIVERED" {
		t.Fatalf("requester: %+v", d)
	}
	_ = caseID
	// RBAC on the communications API
	x.act(t, sup, "GET", "/admin/communications/deliveries?recipient_user_id="+a.id, nil, 200, "")
	if r := x.act(t, sup, "GET", "/admin/communications/consent", nil, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("support read consent records: %d", r.Status)
	}
	x.act(t, x.f.compliance, "GET", "/admin/communications/consent?user_id="+a.id, nil, 200, "")
	if r := a.do("GET", "/admin/communications/overview", nil); r.Status != 404 {
		t.Fatalf("a user reached the communications dashboard: %d", r.Status)
	}
	var ov struct {
		Live      bool `json:"live_messaging_enabled"`
		Providers []struct {
			Code, Readiness string
		} `json:"providers"`
	}
	_ = json.Unmarshal(x.act(t, x.f.compliance, "GET", "/admin/communications/overview", nil, 200, "").Data, &ov)
	if ov.Live || len(ov.Providers) < 8 {
		t.Fatalf("overview: %+v", ov)
	}
	for _, p := range ov.Providers {
		if p.Readiness == "VERIFIED" || p.Readiness == "PRODUCTION_ENABLED" {
			t.Fatalf("provider readiness claimed: %+v", p)
		}
	}
	if _, err := x.mig.Exec(ctx(t), `UPDATE app.notification_provider_configs SET readiness = 'VERIFIED' WHERE provider_code = 'email_production'`); err == nil {
		t.Fatal("database accepted VERIFIED readiness without a provider")
	}
	// the operations portal reads the notifications snapshot
	var opsOv struct {
		Sources []struct{ Module, Status string } `json:"sources"`
		Metrics []struct{ Key, Status string }    `json:"metrics"`
	}
	_ = json.Unmarshal(x.act(t, x.f.compliance, "GET", "/admin/operations/overview", nil, 200, "").Data, &opsOv)
	found := false
	for _, s := range opsOv.Sources {
		found = found || (s.Module == "notifications" && s.Status == "HEALTHY")
	}
	statusOf := map[string]string{}
	for _, m := range opsOv.Metrics {
		statusOf[m.Key] = m.Status
	}
	if !found || statusOf["notifications.live_messaging"] != "BLOCKED" || statusOf["notifications.worker"] != "HEALTHY" {
		t.Fatalf("operations: sources %+v metrics %+v", opsOv.Sources, statusOf)
	}
}

// TestNotificationsFailureInjection: a missing template fails the event without partial records and succeeds once one
// is available; a broken pool sweep changes nothing.
func TestNotificationsFailureInjection(t *testing.T) {
	x := newNotifIT(t)
	u := x.taggedUser(t, "", "")
	ev := ids.New()
	err := x.n.Create(ctx(t), notifications.EventRef{EventID: ev, EventType: "it.template_missing", AggregateType: "user", AggregateID: u.id, OccurredAt: time.Now()},
		[]notifications.Intent{{TemplateKey: "NO_SUCH_TEMPLATE", Category: "CAMPAIGN", Recipients: []string{u.id}}}, nil)
	if err == nil {
		t.Fatal("unknown template accepted")
	}
	if n := x.count(t, `SELECT count(*) FROM app.notification_events WHERE source_event_id = $1`, ev); n != 0 {
		t.Fatal("partial event record after a template failure")
	}
	if err := x.n.Create(ctx(t), notifications.EventRef{EventID: ev, EventType: "it.template_missing", AggregateType: "user", AggregateID: u.id, OccurredAt: time.Now()},
		[]notifications.Intent{{TemplateKey: "CAMPAIGN_APPROVED", Category: "CAMPAIGN", Recipients: []string{u.id}}}, nil); err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
	// database outage during a sweep: an error, no state change
	broken := *x.n
	bp, err := pgxpool.New(ctx(t), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	broken.Pool = bp
	before := x.count(t, `SELECT count(*) FROM app.notification_deliveries WHERE status = 'PROCESSING'`)
	if _, err := broken.Sweep(ctx(t)); err == nil {
		t.Fatal("sweep with no database succeeded")
	}
	bp.Close()
	if after := x.count(t, `SELECT count(*) FROM app.notification_deliveries WHERE status = 'PROCESSING'`); after > before+1 {
		t.Fatalf("a failed sweep left claims: %d → %d", before, after)
	}
}
