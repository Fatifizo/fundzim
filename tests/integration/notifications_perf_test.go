//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// TestNotificationsPerf measures the notification engine with in-process calls on the server's composition (simulators
// active). Opt-in: FUNDZIM_IT_PERF=1; FUNDZIM_PERF_OUT writes notifications-perf.md. Measured, never estimated.
func TestNotificationsPerf(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	x := newNotifIT(t)
	const workers = 8
	recips := make([]string, workers)
	for i := range recips {
		recips[i] = x.taggedUser(t, "", "").id
	}
	var out string
	add := func(res poRes) {
		out += res.row()
		t.Log(res.row())
		if res.errors > 0 { // a measurement with failed calls is a defect, not a number (an unindexed snapshot once failed here)
			t.Errorf("%s: %d failed calls", res.name, res.errors)
		}
	}
	header := "| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |\n|---|---:|---:|---:|---:|---:|---:|---:|\n"
	add(poMeasure("notification creation (recipient resolution, routing for 4 channels, in-app render)", 400, workers, func(w, i int) error {
		_, err := x.n.Notify(ctx(t), "perf:"+ids.New(), notifications.Intent{TemplateKey: "CAMPAIGN_APPROVED", Category: "CAMPAIGN", Recipients: []string{recips[w]}})
		return err
	}))
	c := notifications.Content{EmailSubject: strp("Case {{case_reference}}"), EmailText: strp("Hello {{display_name}},\n\nCase {{case_reference}} was updated."),
		InAppTitle: "Case {{case_reference}}", InAppBody: "Updated."}
	add(poMeasure("template rendering (e-mail text + HTML)", 5000, workers, func(w, i int) error {
		_, err := notifications.Render(c, map[string]string{"display_name": "Tendai", "case_reference": "#12"}, notifications.ChEmail, false)
		return err
	}))
	// the delivery sweep drains this run's deliveries (other tests' scheduled retries in the shared database are excluded)
	const pending = `SELECT count(*) FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE n.recipient_user_id = ANY($1::uuid[]) AND d.status IN ('QUEUED','FAILED_RETRYABLE','PROCESSING')`
	queued := x.count(t, pending, recips)
	t0 := time.Now()
	x.drive(t, "perf deliveries drained", func() bool { return x.count(t, pending, recips) == 0 })
	drain := time.Since(t0)
	row := fmt.Sprintf("| delivery sweeps + simulator callbacks: %d external deliveries drained | 1 | %d | — | — | %.1f (total) | %.0f | 0 |\n",
		queued, queued, float64(drain.Microseconds())/1000, float64(queued)/drain.Seconds())
	out += row
	t.Log(row)
	u := x.taggedUser(t, "", "")
	for i := 0; i < 200; i++ {
		_, _ = x.n.Notify(ctx(t), "perf-inbox:"+ids.New(), notifications.Intent{TemplateKey: "CAMPAIGN_PAUSED", Category: "CAMPAIGN", Recipients: []string{u.id}})
	}
	add(poMeasure("in-app inbox page (25 of 200)", 500, workers, func(w, i int) error {
		_, _, err := x.n.Inbox(ctx(t), u.id, notifications.InboxFilter{Limit: 25})
		return err
	}))
	add(poMeasure("unread count", 1000, workers, func(w, i int) error {
		_, err := x.n.UnreadCount(ctx(t), u.id)
		return err
	}))
	sim := x.n.Providers["email_simulator"].(*notifications.Simulator)
	add(poMeasure("webhook verification + evidence (unknown message)", 500, workers, func(w, i int) error {
		h, body := sim.Signed(ids.New(), "sim_perf"+fmt.Sprint(i), "DELIVERED", time.Now(), sim.Key)
		_, err := x.n.HandleWebhook(context.Background(), "email_simulator", http.Header(h), body)
		return err
	}))
	add(poMeasure("communications dashboard", 100, workers, func(w, i int) error {
		_, err := x.n.Overview(ctx(t))
		return err
	}))
	add(poMeasure("communications report (30 days)", 50, workers, func(w, i int) error {
		_, err := x.n.Reports(ctx(t), time.Now().Add(-30*24*time.Hour), time.Now())
		return err
	}))
	add(poMeasure("operations snapshot (notifications)", 100, workers, func(w, i int) error {
		_, err := x.n.OpsSnapshot(ctx(t))
		return err
	}))
	var notifs, dels, attempts int
	_ = x.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM app.notifications), (SELECT count(*) FROM app.notification_deliveries),
		(SELECT count(*) FROM app.notification_attempts)`).Scan(&notifs, &dels, &attempts)
	md := fmt.Sprintf("## Notification engine performance\n\nmeasured %s UTC; GOMAXPROCS=%d; development stack (compose PostgreSQL 17.11, default settings); "+
		"in-process service calls (simulators); dataset after the run: %d notifications, %d deliveries, %d attempts.\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), notifs, dels, attempts) + header + out
	fmt.Println(md)
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "notifications-perf.md"), []byte(md), 0o644)
	}
}

func strp(s string) *string { return &s }
