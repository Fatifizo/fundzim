package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// directory resolves organisation admins and staff by permission for the notification engine.
type directory struct {
	orgs *organisations.Service
	auth *auth.Service
}

func (d directory) OrganisationAdmins(ctx context.Context, orgID string) ([]string, error) {
	if d.orgs == nil {
		return nil, nil
	}
	return d.orgs.AdminIDs(ctx, orgID)
}

func (d directory) StaffWithPermission(ctx context.Context, permission string) ([]string, error) {
	if d.auth == nil {
		return nil, nil
	}
	return d.auth.StaffWithPermission(ctx, permission)
}

// NotificationDeps are what the engine needs from a process (API or worker).
type NotificationDeps struct {
	Config   config.Config
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Modules  *VerificationModules // simulator callback keys (app keyed secret)
	Orgs     *organisations.Service
	Auth     *auth.Service
	Email    notifications.EmailSender
	SMS      notifications.SMSSender
	WorkerID string
}

// NewNotifications builds the Stage 15 notification engine (ADR-047). Providers: the TEST_ONLY simulators when
// NOTIFICATIONS_SIMULATOR_ENABLED, the development SMTP/Mailpit senders in development and test (configuration refuses
// them elsewhere). No production provider exists.
func NewNotifications(d NotificationDeps) *notifications.Service {
	cfg := d.Config.Notifications
	s := &notifications.Service{Pool: d.Pool, Clock: d.Clock, Logger: d.Logger, Directory: directory{orgs: d.Orgs, auth: d.Auth},
		Production: d.Config.App.Env == config.EnvProduction, PublicURL: d.Config.Auth.PublicURL, WorkerID: d.WorkerID,
		Settings: notifications.DefaultSettings(), Active: map[string]notifications.Provider{}, Providers: map[string]notifications.Provider{}}
	add := func(p notifications.Provider) { s.Providers[p.Code()] = p }
	if cfg.SimulatorEnabled && d.Modules != nil && d.Modules.appKeyed != nil {
		for _, ch := range []string{notifications.ChEmail, notifications.ChSMS, notifications.ChWhatsApp} {
			add(notifications.NewSimulator(d.Pool, d.Clock, ch, d.Modules.appKeyed.Sum("notifications.simulator."+ch+".callback.v1")))
		}
	}
	if cfg.EmailProvider == "dev_smtp" && d.Email != nil {
		add(notifications.DevSMTP{Sender: d.Email})
	}
	if cfg.SMSProvider == "dev_mailpit" && d.SMS != nil {
		add(notifications.DevSMS{Sender: d.SMS})
	}
	active := map[string]string{notifications.ChEmail: map[string]string{"simulator": "email_simulator", "dev_smtp": "email_dev_smtp"}[cfg.EmailProvider],
		notifications.ChSMS:      map[string]string{"simulator": "sms_simulator", "dev_mailpit": "sms_dev_mailpit"}[cfg.SMSProvider],
		notifications.ChWhatsApp: map[string]string{"simulator": "whatsapp_simulator"}[cfg.WhatsAppProvider]}
	for ch, code := range active {
		if p := s.Providers[code]; p != nil && p.ValidateConfiguration() == nil {
			s.Active[ch] = p
		}
	}
	return s
}

// registerNotificationRoutes registers the notifications API (user inbox, preferences, consent, staff communications,
// provider webhooks).
func registerNotificationRoutes(r *httpx.Router, n *notifications.Service, ids notifications.Identities, idem *idempotency.Store,
	limiter ratelimit.Limiter, logger *slog.Logger) {
	h := &notifications.HTTP{S: n, Identities: ids}
	pol := ratelimit.DefaultPolicies()
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		if limiter != nil && rt.RateLimited {
			opts = append(opts, httpx.With(ratelimit.Middleware(limiter, pol.NotificationsConsentUser, ratelimit.ByPrincipal, logger)))
		}
		if limiter != nil && rt.Webhook {
			opts = append(opts, httpx.With(ratelimit.Middleware(limiter, pol.NotificationsWebhookIP, ratelimit.ByClientIP, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// registerNotificationConsumers subscribes the engine to every mapped event type (worker).
func registerNotificationConsumers(reg *outbox.Registry, n *notifications.Service) {
	for _, ev := range notifications.EventTypes() {
		reg.Subscribe(notifications.ConsumerDispatch, ev, n.Handle)
	}
}

// ---- worker jobs ----------------------------------------------------------------------------------------------------

const (
	kindNotificationSweep   = "notifications.deliver"
	kindNotificationSimHook = "notifications.simulator_callbacks"
)

type notificationSweepArgs struct{}

func (notificationSweepArgs) Kind() string { return kindNotificationSweep }

type notificationSweepWorker struct {
	river.WorkerDefaults[notificationSweepArgs]
	N *notifications.Service
}

func (w *notificationSweepWorker) Timeout(*river.Job[notificationSweepArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *notificationSweepWorker) Work(ctx context.Context, _ *river.Job[notificationSweepArgs]) error {
	_, err := w.N.Sweep(ctx)
	return err
}

type notificationSimArgs struct{}

func (notificationSimArgs) Kind() string { return kindNotificationSimHook }

type notificationSimWorker struct {
	river.WorkerDefaults[notificationSimArgs]
	N *notifications.Service
}

func (w *notificationSimWorker) Work(ctx context.Context, _ *river.Job[notificationSimArgs]) error {
	deliver := func(ctx context.Context, provider string, h http.Header, body []byte) error {
		_, err := w.N.HandleWebhook(ctx, provider, h, body) // the same verification path as POST /api/v1/webhooks/notifications/{provider}
		return err
	}
	for _, p := range w.N.Providers {
		if sim, ok := p.(*notifications.Simulator); ok {
			if _, err := sim.DeliverCallbacks(ctx, 200, deliver); err != nil {
				return err
			}
		}
	}
	return nil
}

// notificationJobs registers the delivery sweep (every 5 s) and, with the simulators, their callbacks (every 2 s), on the
// notifications queue so they never hold the default queue's workers (media scans, campaign jobs).
func notificationJobs(workers *river.Workers, n *notifications.Service, simulators bool) []*river.PeriodicJob {
	river.AddWorker(workers, &notificationSweepWorker{N: n})
	periodic := []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(5*time.Second), func() (river.JobArgs, *river.InsertOpts) {
		return notificationSweepArgs{}, &river.InsertOpts{Queue: jobs.QueueNotifications, UniqueOpts: river.UniqueOpts{ByPeriod: 5 * time.Second}}
	}, &river.PeriodicJobOpts{ID: kindNotificationSweep, RunOnStart: true})}
	if simulators {
		river.AddWorker(workers, &notificationSimWorker{N: n})
		periodic = append(periodic, river.NewPeriodicJob(river.PeriodicInterval(2*time.Second), func() (river.JobArgs, *river.InsertOpts) {
			return notificationSimArgs{}, &river.InsertOpts{Queue: jobs.QueueNotifications, UniqueOpts: river.UniqueOpts{ByPeriod: 2 * time.Second}}
		}, &river.PeriodicJobOpts{ID: kindNotificationSimHook, RunOnStart: true}))
	}
	return periodic
}

func workerID() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "worker"
	}
	return "worker:" + h
}
