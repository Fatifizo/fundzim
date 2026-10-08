package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
)

// Job kinds. Stable once shipped: River matches workers by kind string.
const (
	KindRelay   = "outbox.relay"
	KindDeliver = "outbox.deliver"
	KindPurge   = "outbox.purge_dispatched"
)

// Defaults (background-processing §6.5, [default] values).
const (
	DefaultBatchSize          = 100
	DefaultRelayBudget        = 10 * time.Second
	DefaultDeliverMaxAttempts = 8
	DefaultRetention          = 7 * 24 * time.Hour
)

// DefaultDeliverBackoff is the consumer retry schedule (5 s → 1 h, jittered).
var DefaultDeliverBackoff = jobs.Backoff{Base: 5 * time.Second, Cap: time.Hour}

// allStates makes delivery-job uniqueness hold for the job's whole retained lifetime (contract: River
// uniqueness on (consumer, event_id)). The inbox row is the durable guard after River prunes the job.
var allStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStateCancelled, rivertype.JobStateCompleted,
	rivertype.JobStateDiscarded, rivertype.JobStatePending, rivertype.JobStateRetryable,
	rivertype.JobStateRunning, rivertype.JobStateScheduled,
}

// Metrics are the outbox metric families.
type Metrics struct {
	lag        prometheus.Gauge
	batchSize  prometheus.Histogram
	dispatched prometheus.Counter
	enqueued   prometheus.Counter
	purged     prometheus.Counter
}

// NewMetrics registers the outbox metric families on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		lag: prometheus.NewGauge(prometheus.GaugeOpts{Name: "fundzim_outbox_lag_seconds",
			Help: "Age of the oldest undispatched, available outbox event (0 when none)."}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "fundzim_outbox_relay_batch_size",
			Help: "Outbox events claimed per relay batch.", Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250, 500}}),
		dispatched: prometheus.NewCounter(prometheus.CounterOpts{Name: "fundzim_outbox_events_dispatched_total",
			Help: "Outbox events marked dispatched by the relay."}),
		enqueued: prometheus.NewCounter(prometheus.CounterOpts{Name: "fundzim_outbox_deliveries_enqueued_total",
			Help: "outbox.deliver jobs inserted by the relay (excluding unique-skipped duplicates)."}),
		purged: prometheus.NewCounter(prometheus.CounterOpts{Name: "fundzim_outbox_events_purged_total",
			Help: "Dispatched outbox events deleted after the retention window."}),
	}
	reg.MustRegister(m.lag, m.batchSize, m.dispatched, m.enqueued, m.purged)
	return m
}

// Lag returns the lag gauge (tests).
func (m *Metrics) Lag() prometheus.Gauge { return m.lag }

// Relay moves outbox rows into River delivery jobs.
type Relay struct {
	Pool     *pgxpool.Pool
	Registry *Registry
	Metrics  *Metrics
	Logger   *slog.Logger

	BatchSize          int           // rows claimed per transaction (default 100)
	Budget             time.Duration // a run loops over batches until empty or this budget (default 10 s)
	DeliverQueue       string        // queue for outbox.deliver jobs (default jobs.QueueDefault)
	DeliverMaxAttempts int           // default 8

	// EventTypes, when set, restricts the relay to these event types. Production leaves it empty (every
	// event is relayed; events without subscribers are marked dispatched). Used to isolate tests.
	EventTypes []string
	// Now, when set, replaces the database clock for the available_at cutoff (tests).
	Now func() time.Time
}

// RunOnce relays batches until no claimable rows remain or the budget is spent. client inserts the
// delivery jobs (InsertManyTx) in the claiming transaction. Returns the number of events dispatched.
func (r *Relay) RunOnce(ctx context.Context, client *river.Client[pgx.Tx]) (int, error) {
	if client == nil {
		return 0, errors.New("outbox: relay needs a River client")
	}
	budget := r.Budget
	if budget <= 0 {
		budget = DefaultRelayBudget
	}
	deadline := time.Now().Add(budget)
	batch := r.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	total := 0
	for {
		n, err := r.relayBatch(ctx, client, batch)
		total += n
		if err != nil {
			return total, err
		}
		if n < batch || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
	}
	r.updateLag(ctx)
	return total, nil
}

type claimed struct{ id, eventType string }

func (r *Relay) relayBatch(ctx context.Context, client *river.Client[pgx.Tx], batch int) (int, error) {
	var n int
	err := db.WithTx(ctx, r.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var cutoff *time.Time
		if r.Now != nil {
			t := r.Now().UTC()
			cutoff = &t
		}
		var types []string
		if len(r.EventTypes) > 0 {
			types = r.EventTypes
		}
		rows, err := tx.Query(ctx, `
			SELECT id::text, event_type FROM app.outbox_events
			 WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL
			   AND available_at <= coalesce($1::timestamptz, now())
			   AND ($2::text[] IS NULL OR event_type = ANY($2::text[]))
			 ORDER BY available_at, id
			 LIMIT $3
			 FOR UPDATE SKIP LOCKED`, cutoff, types, batch)
		if err != nil {
			return fmt.Errorf("outbox: claim: %w", err)
		}
		events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claimed, error) {
			var c claimed
			err := row.Scan(&c.id, &c.eventType)
			return c, err
		})
		if err != nil {
			return fmt.Errorf("outbox: claim scan: %w", err)
		}
		n = len(events)
		if n == 0 {
			return nil
		}
		params, idList := r.plan(events)
		if len(params) > 0 {
			res, err := client.InsertManyTx(ctx, tx, params)
			if err != nil {
				return fmt.Errorf("outbox: insert deliveries: %w", err)
			}
			if r.Metrics != nil {
				for _, x := range res {
					if !x.UniqueSkippedAsDuplicate {
						r.Metrics.enqueued.Inc()
					}
				}
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.outbox_events SET dispatched_at = now(), attempts = attempts + 1
			 WHERE id = ANY($1::uuid[])`, idList); err != nil {
			return fmt.Errorf("outbox: mark dispatched: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if r.Metrics != nil {
		r.Metrics.batchSize.Observe(float64(n))
		r.Metrics.dispatched.Add(float64(n))
	}
	return n, nil
}

// plan builds one delivery job per (event, subscriber). Events without subscribers are still marked
// dispatched (they appear only in idList).
func (r *Relay) plan(events []claimed) ([]river.InsertManyParams, []string) {
	queue := r.DeliverQueue
	if queue == "" {
		queue = jobs.QueueDefault
	}
	maxAttempts := r.DeliverMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultDeliverMaxAttempts
	}
	var params []river.InsertManyParams
	idList := make([]string, 0, len(events))
	for _, e := range events {
		idList = append(idList, e.id)
		for _, consumer := range r.Registry.Consumers(e.eventType) {
			params = append(params, river.InsertManyParams{
				Args: DeliverArgs{Consumer: consumer, EventID: e.id},
				InsertOpts: &river.InsertOpts{
					Queue:       queue,
					MaxAttempts: maxAttempts,
					UniqueOpts:  river.UniqueOpts{ByArgs: true, ByState: allStates},
				},
			})
		}
	}
	return params, idList
}

func (r *Relay) updateLag(ctx context.Context) {
	if r.Metrics == nil {
		return
	}
	var lag float64
	err := r.Pool.QueryRow(ctx, `
		SELECT coalesce(extract(epoch FROM now() - min(available_at)), 0)::float8
		  FROM app.outbox_events
		 WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL AND available_at <= now()`).Scan(&lag)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Warn("outbox lag query failed", slog.String("error_category", "dependency"), slog.String("error", err.Error()))
		}
		return
	}
	r.Metrics.lag.Set(max(lag, 0))
}

// RelayArgs is the periodic relay job (every second, leader only, unique per second).
type RelayArgs struct{}

// Kind implements river.JobArgs.
func (RelayArgs) Kind() string { return KindRelay }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (RelayArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueOutbox, MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{ByPeriod: time.Second}}
}

// RelayWorker runs Relay.RunOnce as a River job.
type RelayWorker struct {
	river.WorkerDefaults[RelayArgs]
	Relay *Relay
}

var relayBackoff = jobs.Backoff{Base: time.Second, Cap: 30 * time.Second}

// NextRetry implements river.Worker.
func (w *RelayWorker) NextRetry(job *river.Job[RelayArgs]) time.Time {
	return relayBackoff.NextRetry(job.JobRow)
}

// Timeout implements river.Worker.
func (w *RelayWorker) Timeout(*river.Job[RelayArgs]) time.Duration { return 15 * time.Second }

// Work implements river.Worker.
func (w *RelayWorker) Work(ctx context.Context, _ *river.Job[RelayArgs]) error {
	_, err := w.Relay.RunOnce(ctx, river.ClientFromContext[pgx.Tx](ctx))
	return err
}

// PeriodicRelay is the periodic-job definition for the worker client.
func PeriodicRelay(interval time.Duration) *river.PeriodicJob {
	if interval <= 0 {
		interval = time.Second
	}
	return river.NewPeriodicJob(river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) { return RelayArgs{}, nil },
		&river.PeriodicJobOpts{ID: KindRelay, RunOnStart: true})
}

// DeliverArgs identifies one delivery. Args carry identifiers only (BP-8).
type DeliverArgs struct {
	Consumer string `json:"consumer"`
	EventID  string `json:"event_id"`
}

// Kind implements river.JobArgs.
func (DeliverArgs) Kind() string { return KindDeliver }

// DeliverWorker runs a consumer's handler for one event.
type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	Pool     *pgxpool.Pool
	Registry *Registry
	Logger   *slog.Logger
	Backoff  jobs.Backoff  // default DefaultDeliverBackoff
	JobLimit time.Duration // per-attempt timeout (default 30 s)
}

// NextRetry implements river.Worker.
func (w *DeliverWorker) NextRetry(job *river.Job[DeliverArgs]) time.Time {
	b := w.Backoff
	if b.Base <= 0 {
		b = DefaultDeliverBackoff
	}
	return b.NextRetry(job.JobRow)
}

// Timeout implements river.Worker.
func (w *DeliverWorker) Timeout(*river.Job[DeliverArgs]) time.Duration {
	if w.JobLimit > 0 {
		return w.JobLimit
	}
	return 30 * time.Second
}

// ErrEventMissing means the outbox row no longer exists (purged) and the consumer never processed it.
var ErrEventMissing = errors.New("outbox: event not found")

// Work implements river.Worker: inbox check → load → handler (no transaction) → inbox row.
func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	a := job.Args
	if !ids.Valid(a.EventID) || !consumerRE.MatchString(a.Consumer) {
		return river.JobCancel(errors.New("outbox: malformed delivery arguments"))
	}
	done, err := w.processed(ctx, a)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	d, err := w.load(ctx, a.EventID)
	if errors.Is(err, ErrEventMissing) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	d.Attempt = job.Attempt
	h := w.Registry.handler(a.Consumer, d.EventType)
	if h == nil {
		// A deploy may have removed or renamed the subscription; retry so an operator sees the discard.
		return fmt.Errorf("outbox: no handler for consumer %s and event type %s", a.Consumer, d.EventType)
	}
	if db.InTx(ctx) {
		return errors.New("outbox: handler must not run inside a transaction")
	}
	if err := h(ctx, d); err != nil {
		return fmt.Errorf("consumer %s: %w", a.Consumer, err)
	}
	if _, err := w.Pool.Exec(ctx, `
		INSERT INTO app.inbox_events (id, consumer, event_id, event_type) VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer, event_id) DO NOTHING`, ids.New(), a.Consumer, a.EventID, d.EventType); err != nil {
		// The effect happened but is not recorded; the retry re-runs the (idempotent) handler.
		return fmt.Errorf("outbox: record inbox: %w", err)
	}
	return nil
}

func (w *DeliverWorker) processed(ctx context.Context, a DeliverArgs) (bool, error) {
	var exists bool
	err := w.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.inbox_events WHERE consumer = $1 AND event_id = $2)`,
		a.Consumer, a.EventID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("outbox: inbox check: %w", err)
	}
	return exists, nil
}

func (w *DeliverWorker) load(ctx context.Context, eventID string) (Delivery, error) {
	var d Delivery
	var corr *string
	err := w.Pool.QueryRow(ctx, `
		SELECT id::text, event_type, aggregate_type, aggregate_id::text, correlation_id, payload, occurred_at
		  FROM app.outbox_events WHERE id = $1`, eventID).
		Scan(&d.EventID, &d.EventType, &d.AggregateType, &d.AggregateID, &corr, &d.Payload, &d.OccurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrEventMissing
	}
	if err != nil {
		return d, fmt.Errorf("outbox: load event: %w", err)
	}
	if corr != nil {
		d.CorrelationID = *corr
	}
	return d, nil
}
