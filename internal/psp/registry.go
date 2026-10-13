package psp

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/featureflags"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Account environments.
const (
	EnvSandbox    = "SANDBOX"
	EnvStaging    = "STAGING"
	EnvProduction = "PRODUCTION"
)

// Routing errors (interface-contracts §5).
var (
	ErrMethodUnavailable   = errs.New(errs.Unprocessable, "PAYMENT_METHOD_UNAVAILABLE", "This payment method is not available for this donation.")
	ErrExecutionDisabled   = errs.New(errs.Unavailable, "PAYMENT_EXECUTION_DISABLED", "Payments are not available on FundZim yet.")
	ErrPaymentsUnavailable = errs.New(errs.Unavailable, "PAYMENTS_UNAVAILABLE", "Payments are not available on FundZim yet.")
	ErrAmountOutOfRange    = errs.New(errs.Unprocessable, "AMOUNT_OUT_OF_RANGE", "This amount is outside what this payment method accepts.")
	ErrProviderUnavailable = errs.New(errs.Unavailable, "PAYMENT_PROVIDER_UNAVAILABLE", "This payment method is temporarily unavailable. Try again later or choose another method.")
)

// Config is the psp configuration.
type Config struct {
	SandboxEnabled bool // PAYMENTS_SANDBOX_ENABLED (refused in production by config loading)
	// SandboxCircuitBreaker makes the circuit breaker apply to the sandbox too. Off by default: the simulator's
	// timeout scenarios are deliberate and would otherwise open the circuit for every following scenario. Real
	// providers are always subject to it.
	SandboxCircuitBreaker bool
	// ExecEnv is the execution environment of this process (Stage 9): DISABLED | LOCAL_SIMULATOR |
	// PROVIDER_SANDBOX | PRODUCTION. It decides which providers and which account environment may execute.
	ExecEnv string
	// Allowlist holds the provider codes allowed to execute in this process (PAYMENTS_PROVIDER_ALLOWLIST).
	Allowlist map[string]bool
}

// Service is the psp public service.
type Service struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Logger  *slog.Logger
	Secrets SecretResolver
	Cfg     Config
	Metrics *Metrics

	mu       sync.RWMutex
	adapters map[string]Adapter
	handlers map[string]EventHandler
}

// New builds the service.
func New(pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger, secrets SecretResolver, cfg Config, m *Metrics) *Service {
	if clk == nil {
		clk = clock.System
	}
	if m == nil {
		m = NewMetrics(nil)
	}
	return &Service{Pool: pool, Clock: clk, Logger: logger, Secrets: secrets, Cfg: normaliseConfig(cfg), Metrics: m,
		adapters: map[string]Adapter{}, handlers: map[string]EventHandler{}}
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

// Register adds an adapter (composition root). A sandbox adapter is registered only when the sandbox is enabled.
func (s *Service) Register(a Adapter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters[a.Code()] = a
}

// Adapter returns the registered adapter for a provider code.
func (s *Service) Adapter(code string) (Adapter, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.adapters[code]
	return a, ok
}

// Provider is a registry row.
type Provider struct {
	ID               string
	Code             string
	DisplayName      string
	IsSandbox        bool
	SelectionStatus  string
	Status           string
	ConfirmationMode string
	SandboxStatus    string
	SelectionADR     string
}

// Capability is a current capability row.
type Capability struct {
	ID                      string
	ProviderID              string
	Version                 int
	Method                  string
	Currency                string
	CustodyModel            string
	EvidenceStatus          string
	EvidenceSource          *string
	PCRRef                  *string
	Routable                bool
	RefundAPI               bool
	PartialRefund           bool
	IdempotentCreate        bool
	SupportsCancel          bool
	SignedWebhooks          bool
	StatusAPI               bool
	SeparateCapture         bool
	DocumentedExpiry        bool
	DocumentedExpirySeconds *int
}

const providerCols = `id, code, display_name, is_sandbox, selection_status, status, confirmation_mode, sandbox_status,
	coalesce(selection_adr, '')`

func scanProvider(row pgx.Row) (Provider, error) {
	var p Provider
	err := row.Scan(&p.ID, &p.Code, &p.DisplayName, &p.IsSandbox, &p.SelectionStatus, &p.Status, &p.ConfirmationMode, &p.SandboxStatus,
		&p.SelectionADR)
	return p, err
}

// ErrProviderNotFound means no provider with that code or id.
var ErrProviderNotFound = errors.New("psp: provider not found")

// ProviderByCode returns a provider by code.
func (s *Service) ProviderByCode(ctx context.Context, code string) (Provider, error) {
	p, err := scanProvider(s.Pool.QueryRow(ctx, `SELECT `+providerCols+` FROM app.payment_providers WHERE code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	return p, err
}

// ProviderByID returns a provider by id.
func (s *Service) ProviderByID(ctx context.Context, id string) (Provider, error) {
	if !ids.Valid(id) {
		return Provider{}, ErrProviderNotFound
	}
	p, err := scanProvider(s.Pool.QueryRow(ctx, `SELECT `+providerCols+` FROM app.payment_providers WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	return p, err
}

// LiveExecution reports whether real payment execution is enabled: the constant AND the kill switch.
func (s *Service) LiveExecution(ctx context.Context) (bool, error) {
	if !LiveExecutionEnabled {
		return false, nil
	}
	on, err := featureflags.Enabled(ctx, s.Pool, KillSwitchFlag)
	if errors.Is(err, featureflags.ErrUnknownFlag) {
		return false, nil
	}
	return on, err
}

// KillSwitch reports the kill-switch flag value (for the operations view only).
func (s *Service) KillSwitch(ctx context.Context) bool {
	on, err := featureflags.Enabled(ctx, s.Pool, KillSwitchFlag)
	return err == nil && on
}

const accountCols = `id, provider_id, environment, account_label, coalesce(merchant_reference, ''), api_credentials_secret_ref,
	coalesce(webhook_secret_ref, ''), coalesce(webhook_secret_ref_previous, ''), coalesce(webhook_replay_window_seconds, 0), status`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	var win int
	err := row.Scan(&a.ID, &a.ProviderID, &a.Environment, &a.Label, &a.MerchantReference, &a.APISecretRef,
		&a.WebhookSecretRef, &a.WebhookSecretRefPrevious, &win, &a.Status)
	a.ReplayWindow = time.Duration(win) * time.Second
	return a, err
}

// ErrNoAccount means the provider has no enabled account for the environment.
var ErrNoAccount = errors.New("psp: no enabled provider account")

// ActiveAccount returns the provider's enabled account for the current environment (oldest label first).
func (s *Service) ActiveAccount(ctx context.Context, p Provider) (Account, error) {
	env := s.accountEnv(p)
	if env == "" {
		return Account{}, ErrNoAccount
	}
	a, err := scanAccount(s.Pool.QueryRow(ctx, `SELECT `+accountCols+` FROM app.provider_accounts
		WHERE provider_id = $1 AND environment = $2 AND status = 'ENABLED' ORDER BY created_at, id LIMIT 1`, p.ID, env))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

// AccountByID returns an account by id (any status: used to service existing payments).
func (s *Service) AccountByID(ctx context.Context, id string) (Account, error) {
	a, err := scanAccount(s.Pool.QueryRow(ctx, `SELECT `+accountCols+` FROM app.provider_accounts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

const capCols = `id, provider_id, capability_version, method, currency, custody_model, evidence_status, evidence_source, pcr_ref,
	routable, refund_api, partial_refund, idempotent_create, supports_cancel, signed_webhooks, status_api, separate_capture,
	documented_expiry, documented_expiry_seconds`

func scanCapability(row pgx.Row) (Capability, error) {
	var c Capability
	err := row.Scan(&c.ID, &c.ProviderID, &c.Version, &c.Method, &c.Currency, &c.CustodyModel, &c.EvidenceStatus, &c.EvidenceSource,
		&c.PCRRef, &c.Routable, &c.RefundAPI, &c.PartialRefund, &c.IdempotentCreate, &c.SupportsCancel, &c.SignedWebhooks,
		&c.StatusAPI, &c.SeparateCapture, &c.DocumentedExpiry, &c.DocumentedExpirySeconds)
	return c, err
}

// Capabilities returns the current capability rows of a provider (method, currency order).
func (s *Service) Capabilities(ctx context.Context, providerID string) ([]Capability, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+capCols+` FROM app.provider_capabilities_current WHERE provider_id = $1 ORDER BY method, currency`, providerID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Capability, error) { return scanCapability(r) })
}

// CapabilityByID returns one capability row (any version).
func (s *Service) CapabilityByID(ctx context.Context, id string) (Capability, error) {
	return scanCapability(s.Pool.QueryRow(ctx, `SELECT `+capCols+` FROM app.provider_capabilities WHERE id = $1`, id))
}

// Providers lists every registered provider.
func (s *Service) Providers(ctx context.Context) ([]Provider, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+providerCols+` FROM app.payment_providers ORDER BY is_sandbox, code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Provider, error) { return scanProvider(r) })
}

// RouteQuery asks for a provider for one payment.
type RouteQuery struct {
	Method      string
	Currency    string
	AmountMinor int64
	Simulated   bool // the donation intent is a simulation: only sandbox providers may serve it
}

// Route is the routing decision recorded on the payment.
type Route struct {
	Provider   Provider
	Account    Account
	Capability Capability
	Adapter    Adapter
	Reason     string
}

// Route selects a provider (payment-provider-interfaces §8). It never routes on unverified evidence, never to a
// provider outside this process's execution environment or allowlist, never to an account whose credentials do not
// resolve inside its own environment, never to MERCHANT_SETTLEMENT (routable is false for both, and the payment FK
// requires routable), never to a disabled
// provider or account, never past an open circuit, never outside the provider's declared limits, and never to a
// real provider while live execution is disabled. SPLIT_DIRECT (Model C) is not routed: no campaign has a
// Model C settlement model.
func (s *Service) Route(ctx context.Context, q RouteQuery) (Route, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+capCols+` FROM app.provider_capabilities_current
		WHERE method = $1 AND currency = $2 ORDER BY provider_id`, q.Method, q.Currency)
	if err != nil {
		return Route{}, err
	}
	caps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Capability, error) { return scanCapability(r) })
	if err != nil {
		return Route{}, err
	}
	var candidates []Route
	disabledByGate, outOfRange, outage, noCredentials := false, false, false, false
	for _, c := range caps {
		if !c.Routable || c.CustodyModel != "PSP_POOL" {
			continue
		}
		p, err := s.ProviderByID(ctx, c.ProviderID)
		if err != nil {
			return Route{}, err
		}
		if p.IsSandbox != q.Simulated {
			continue
		}
		usable, err := s.Usable(ctx, p)
		if err != nil {
			return Route{}, err
		}
		if !usable {
			if p.Status == "ENABLED" {
				disabledByGate = true
			}
			continue
		}
		a, ok := s.Adapter(p.Code)
		if !ok {
			continue
		}
		acct, err := s.ActiveAccount(ctx, p)
		if errors.Is(err, ErrNoAccount) {
			continue
		}
		if err != nil {
			return Route{}, err
		}
		if open, err := outageOpen(ctx, s.Pool, p.ID); err != nil {
			return Route{}, err
		} else if open {
			outage = true // staff-declared outage
			continue
		}
		if !s.CredentialsUsable(ctx, acct) {
			noCredentials = true // credentials missing, unresolvable or outside the account's environment: fail closed
			continue
		}
		lim, ok := a.Limits(c.Method, c.Currency)
		if !ok {
			continue // unknown provider limit: excluded (fail closed)
		}
		if q.AmountMinor < lim.MinMinor || q.AmountMinor > lim.MaxMinor {
			outOfRange = true
			continue
		}
		if st, err := s.Circuit(ctx, p.ID, OpCreatePayment, c.Currency); err != nil {
			return Route{}, err
		} else if st == CircuitOpen && (!p.IsSandbox || s.Cfg.SandboxCircuitBreaker) {
			outage = true
			continue
		}
		candidates = append(candidates, Route{Provider: p, Account: acct, Capability: c, Adapter: a,
			Reason: "routable PSP_POOL capability v" + itoa(c.Version) + " (" + c.EvidenceStatus + ")"})
	}
	if len(candidates) == 0 {
		switch {
		case outOfRange:
			return Route{}, ErrAmountOutOfRange
		case disabledByGate:
			return Route{}, ErrExecutionDisabled
		case outage, noCredentials:
			return Route{}, ErrProviderUnavailable
		}
		return Route{}, ErrMethodUnavailable
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Provider.Code < candidates[j].Provider.Code })
	return candidates[0], nil
}

// MethodOption is a method currently available for a currency.
type MethodOption struct {
	Method    string `json:"method"`
	Category  string `json:"category"`
	Currency  string `json:"currency"`
	Simulated bool   `json:"simulated"`
}

// AvailableMethods lists the methods that would route now for a currency (simulated or real).
func (s *Service) AvailableMethods(ctx context.Context, currency string, simulated bool) ([]MethodOption, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT c.method FROM app.provider_capabilities_current c
		JOIN app.payment_providers p ON p.id = c.provider_id
		WHERE c.currency = $1 AND c.routable AND c.custody_model = 'PSP_POOL' AND p.status <> 'RETIRED' AND p.is_sandbox = $2
		ORDER BY c.method`, currency, simulated)
	if err != nil {
		return nil, err
	}
	methods, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []MethodOption{}
	for _, m := range methods {
		// confirm through the router so the list never offers what would be refused
		if _, err := s.Route(ctx, RouteQuery{Method: m, Currency: currency, AmountMinor: 1, Simulated: simulated}); err != nil {
			if errs.As(err) != nil && errs.As(err).Code == ErrAmountOutOfRange.Code {
				out = append(out, MethodOption{Method: m, Category: MethodCategory(m), Currency: currency, Simulated: simulated})
			}
			continue
		}
		out = append(out, MethodOption{Method: m, Category: MethodCategory(m), Currency: currency, Simulated: simulated})
	}
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Metrics are psp's Prometheus metrics (no ids in labels).
type Metrics struct {
	WebhooksReceived *prometheus.CounterVec // provider, duplicate
	WebhooksRejected *prometheus.CounterVec // provider, reason
	ProviderCalls    *prometheus.CounterVec // provider, operation, classification
	ProviderErrors   *prometheus.CounterVec // provider, error_class
	Duration         *prometheus.HistogramVec
}

// NewMetrics registers psp metrics on reg (nil = unregistered, for tests).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		WebhooksReceived: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_webhooks_received_total",
			Help: "Verified provider webhooks received (duplicate=true when already stored)."}, []string{"provider", "duplicate"}),
		WebhooksRejected: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_webhooks_rejected_total",
			Help: "Rejected provider webhooks by reason (nothing stored but a rejection row)."}, []string{"provider", "reason"}),
		ProviderCalls: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_provider_calls_total",
			Help: "Outbound provider calls by operation and classification."}, []string{"provider", "operation", "classification"}),
		ProviderErrors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_provider_errors_total",
			Help: "Provider calls that did not end ACCEPTED, by error class."}, []string{"provider", "error_class"}),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "payment_processing_duration_seconds",
			Help: "Payment processing durations by stage.", Buckets: prometheus.DefBuckets}, []string{"stage"}),
	}
	if reg != nil {
		reg.MustRegister(m.WebhooksReceived, m.WebhooksRejected, m.ProviderCalls, m.ProviderErrors, m.Duration)
	}
	return m
}

// refCacheTTL bounds the display-only provider cache (names and flags change only by migration).
const refCacheTTL = 30 * time.Second

type refEntry struct {
	p  Provider
	at time.Time
}

var (
	refMu    sync.Mutex
	refCache = map[string]refEntry{}
)

// ProviderForDisplay returns a provider for response rendering (lists and detail views) from a short-lived cache, so
// a page of N payments does not cost N registry queries. Routing and webhook checks never use it: they read the
// registry directly so status changes apply immediately.
func (s *Service) ProviderForDisplay(ctx context.Context, id string) (Provider, error) {
	refMu.Lock()
	e, ok := refCache[id]
	refMu.Unlock()
	if ok && s.Clock.Now().Sub(e.at) < refCacheTTL {
		return e.p, nil
	}
	p, err := s.ProviderByID(ctx, id)
	if err != nil {
		return Provider{}, err
	}
	refMu.Lock()
	refCache[id] = refEntry{p: p, at: s.Clock.Now()}
	refMu.Unlock()
	return p, nil
}
