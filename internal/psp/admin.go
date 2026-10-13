package psp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// ProviderRef is the provider object used in API responses.
type ProviderRef struct {
	Code        string `json:"code"`
	DisplayName string `json:"display_name"`
	Simulated   bool   `json:"simulated"`
}

// Ref returns the API object for a provider.
func (p Provider) Ref() ProviderRef {
	return ProviderRef{Code: p.Code, DisplayName: p.DisplayName, Simulated: p.IsSandbox}
}

// ---- keyset cursors (newest first: "<RFC3339Nano>|<uuid>") ---------------------------------------------------

// ParseCursor parses a newest-first keyset cursor; "" means the first page.
func ParseCursor(c string) (*time.Time, string, error) {
	if c == "" {
		return nil, "00000000-0000-0000-0000-000000000000", nil
	}
	ts, id, ok := strings.Cut(c, "|")
	t, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil || !ids.Valid(id) {
		return nil, "", httpx.Validation(errs.Detail{Field: "cursor", Code: "INVALID_FORMAT"})
	}
	return &t, id, nil
}

// NextCursor returns the cursor after the last item when the page is full.
func NextCursor(n, limit int, at time.Time, id string) *string {
	if n < limit {
		return nil
	}
	c := at.UTC().Format(time.RFC3339Nano) + "|" + id
	return &c
}

// PageLimit clamps a requested page size (default 50, max 100).
func PageLimit(n int) int {
	if n <= 0 || n > 100 {
		return 50
	}
	return n
}

// ---- providers view ---------------------------------------------------------------------------------------

// AdminAccount is an account as staff see it: reference NAMES and whether they resolve, never values.
type AdminAccount struct {
	ID                       string  `json:"id"`
	Environment              string  `json:"environment"`
	Label                    string  `json:"label"`
	Status                   string  `json:"status"`
	APICredentialsSecretRef  string  `json:"api_credentials_secret_ref"`
	WebhookSecretRef         *string `json:"webhook_secret_ref"`
	WebhookSecretRefPrevious *string `json:"webhook_secret_ref_previous"`
	SecretsResolvable        bool    `json:"secrets_resolvable"`
	ReplayWindowSeconds      int     `json:"webhook_replay_window_seconds"`
}

// AdminCapability is a current capability row.
type AdminCapability struct {
	ID             string          `json:"id"`
	Method         string          `json:"method"`
	MethodCategory string          `json:"method_category"`
	Currency       string          `json:"currency"`
	CustodyModel   string          `json:"custody_model"`
	EvidenceStatus string          `json:"evidence_status"`
	EvidenceSource *string         `json:"evidence_source"`
	PCRRef         *string         `json:"pcr_ref"`
	Routable       bool            `json:"routable"`
	Version        int             `json:"capability_version"`
	Flags          map[string]bool `json:"flags"`
}

// AdminProvider is a provider with accounts, capabilities and circuit state.
type AdminProvider struct {
	Code             string            `json:"code"`
	DisplayName      string            `json:"display_name"`
	Simulated        bool              `json:"simulated"`
	Status           string            `json:"status"`
	SelectionStatus  string            `json:"selection_status"`
	ConfirmationMode string            `json:"confirmation_mode"`
	Circuit          string            `json:"circuit"`
	Accounts         []AdminAccount    `json:"accounts"`
	Capabilities     []AdminCapability `json:"capabilities"`
}

// GateState describes the live-execution gates (ADR-039 §6).
type GateState struct {
	Enabled    bool `json:"enabled"`
	Constant   bool `json:"constant"`
	KillSwitch bool `json:"kill_switch"`
}

// ProvidersView is GET /admin/payments/providers.
type ProvidersView struct {
	Items          []AdminProvider `json:"items"`
	LiveExecution  GateState       `json:"live_execution"`
	SandboxEnabled bool            `json:"sandbox_enabled"`
}

// Gates returns the live-execution gate state.
func (s *Service) Gates(ctx context.Context) GateState {
	live, _ := s.LiveExecution(ctx)
	return GateState{Enabled: live, Constant: LiveExecutionEnabled, KillSwitch: s.KillSwitch(ctx)}
}

// AdminProviders builds the providers view.
func (s *Service) AdminProviders(ctx context.Context) (ProvidersView, error) {
	ps, err := s.Providers(ctx)
	if err != nil {
		return ProvidersView{}, err
	}
	out := ProvidersView{Items: []AdminProvider{}, LiveExecution: s.Gates(ctx), SandboxEnabled: s.Cfg.SandboxEnabled}
	for _, p := range ps {
		ap := AdminProvider{Code: p.Code, DisplayName: p.DisplayName, Simulated: p.IsSandbox, Status: p.Status,
			SelectionStatus: p.SelectionStatus, ConfirmationMode: p.ConfirmationMode, Circuit: s.ProviderCircuit(ctx, p.ID),
			Accounts: []AdminAccount{}, Capabilities: []AdminCapability{}}
		rows, err := s.Pool.Query(ctx, `SELECT `+accountCols+` FROM app.provider_accounts WHERE provider_id = $1 ORDER BY environment, account_label`, p.ID)
		if err != nil {
			return ProvidersView{}, err
		}
		accts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Account, error) { return scanAccount(r) })
		if err != nil {
			return ProvidersView{}, err
		}
		for _, a := range accts {
			resolvable := Resolvable(ctx, s.Secrets, a.APISecretRef) &&
				(a.WebhookSecretRef == "" || Resolvable(ctx, s.Secrets, a.WebhookSecretRef))
			ap.Accounts = append(ap.Accounts, AdminAccount{ID: a.ID, Environment: a.Environment, Label: a.Label, Status: a.Status,
				APICredentialsSecretRef: a.APISecretRef, WebhookSecretRef: nonEmpty(a.WebhookSecretRef),
				WebhookSecretRefPrevious: nonEmpty(a.WebhookSecretRefPrevious), SecretsResolvable: resolvable,
				ReplayWindowSeconds: int(a.ReplayWindow / time.Second)})
		}
		caps, err := s.Capabilities(ctx, p.ID)
		if err != nil {
			return ProvidersView{}, err
		}
		for _, c := range caps {
			ap.Capabilities = append(ap.Capabilities, c.Admin())
		}
		out.Items = append(out.Items, ap)
	}
	return out, nil
}

// Admin renders a capability for staff and the public capability list.
func (c Capability) Admin() AdminCapability {
	return AdminCapability{ID: c.ID, Method: c.Method, MethodCategory: MethodCategory(c.Method), Currency: c.Currency,
		CustodyModel: c.CustodyModel, EvidenceStatus: c.EvidenceStatus, EvidenceSource: c.EvidenceSource, PCRRef: c.PCRRef,
		Routable: c.Routable, Version: c.Version, Flags: map[string]bool{"refund_api": c.RefundAPI, "partial_refund": c.PartialRefund,
			"idempotent_create": c.IdempotentCreate, "supports_cancel": c.SupportsCancel, "signed_webhooks": c.SignedWebhooks,
			"status_api": c.StatusAPI, "separate_capture": c.SeparateCapture, "documented_expiry": c.DocumentedExpiry}}
}

// PublicCapability is GET /payment-providers/capabilities (no accounts, no secrets).
type PublicCapability struct {
	Provider ProviderRef `json:"provider"`
	AdminCapability
}

// PublicCapabilities lists the current capabilities of ENABLED providers (sandbox rows only when it runs).
func (s *Service) PublicCapabilities(ctx context.Context) ([]PublicCapability, error) {
	ps, err := s.Providers(ctx)
	if err != nil {
		return nil, err
	}
	out := []PublicCapability{}
	for _, p := range ps {
		if ok, err := s.Usable(ctx, p); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		caps, err := s.Capabilities(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range caps {
			out = append(out, PublicCapability{Provider: p.Ref(), AdminCapability: c.Admin()})
		}
	}
	return out, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---- inbox views ------------------------------------------------------------------------------------------

// InboxItem is a webhook inbox row (summary).
type InboxItem struct {
	ID                 string      `json:"id"`
	Provider           ProviderRef `json:"provider"`
	ProviderEventID    string      `json:"provider_event_id"`
	EventClass         string      `json:"event_class"`
	EventTypeRaw       string      `json:"event_type_raw"`
	OurReference       *string     `json:"our_reference"`
	ProviderReference  *string     `json:"provider_reference"`
	Status             string      `json:"status"`
	Attempts           int         `json:"attempts"`
	LastErrorCode      *string     `json:"last_error_code"`
	Simulated          bool        `json:"simulated"`
	SignatureKeySlot   *string     `json:"signature_key_slot"`
	VerificationMethod string      `json:"verification_method"`
	ReceivedAt         time.Time   `json:"received_at"`
	ProcessedAt        *time.Time  `json:"processed_at"`
}

// InboxDetail adds the redacted payload and processing facts.
type InboxDetail struct {
	InboxItem
	PayloadRedacted   map[string]any    `json:"payload_redacted"`
	RawBodySHA256     string            `json:"raw_body_sha256"`
	DispatchedTo      *string           `json:"dispatched_to"`
	ParkedSubjectID   *string           `json:"parked_subject_id"`
	ParkedUntilStatus *string           `json:"parked_until_status"`
	ParkDeadlineAt    *time.Time        `json:"park_deadline_at"`
	NextAttemptAt     *time.Time        `json:"next_attempt_at"`
	HeadersSubset     map[string]string `json:"headers_subset"`
}

const inboxCols = `i.id, p.code, p.display_name, p.is_sandbox, i.provider_event_id, i.event_class, i.event_type_raw, i.our_reference::text,
	i.provider_reference, i.status, i.attempts, i.last_error_code, i.is_simulated, i.signature_key_slot, i.verification_method,
	i.received_at, i.processed_at`

func scanInbox(r pgx.Row, extra ...any) (InboxItem, error) {
	var it InboxItem
	dest := []any{&it.ID, &it.Provider.Code, &it.Provider.DisplayName, &it.Provider.Simulated, &it.ProviderEventID, &it.EventClass,
		&it.EventTypeRaw, &it.OurReference, &it.ProviderReference, &it.Status, &it.Attempts, &it.LastErrorCode, &it.Simulated,
		&it.SignatureKeySlot, &it.VerificationMethod, &it.ReceivedAt, &it.ProcessedAt}
	err := r.Scan(append(dest, extra...)...)
	return it, err
}

var inboxStatuses = map[string]bool{InboxReceived: true, InboxDispatched: true, InboxParked: true, InboxFailedRetrying: true,
	InboxProcessed: true, InboxUnrecognised: true, InboxDeadLettered: true}

// InboxList lists inbox rows newest first.
func (s *Service) InboxList(ctx context.Context, status, cursor string, limit int) ([]InboxItem, *string, error) {
	if status != "" && !inboxStatuses[status] {
		return nil, nil, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	before, beforeID, err := ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = PageLimit(limit)
	rows, err := s.Pool.Query(ctx, `SELECT `+inboxCols+` FROM app.provider_webhook_inbox i JOIN app.payment_providers p ON p.id = i.provider_id
		WHERE ($1 = '' OR i.status = $1) AND ($2::timestamptz IS NULL OR (i.received_at, i.id) < ($2, $3::uuid))
		ORDER BY i.received_at DESC, i.id DESC LIMIT $4`, status, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxItem, error) { return scanInbox(r) })
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = NextCursor(len(items), limit, last.ReceivedAt, last.ID)
	}
	return items, next, nil
}

// InboxForReference lists the inbox rows carrying our reference (a payment id), oldest first.
func (s *Service) InboxForReference(ctx context.Context, ourRef string) ([]InboxItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+inboxCols+` FROM app.provider_webhook_inbox i JOIN app.payment_providers p ON p.id = i.provider_id
		WHERE i.our_reference = $1 ORDER BY i.received_at, i.id`, ourRef)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxItem, error) { return scanInbox(r) })
}

// InboxGet returns one inbox row with its redacted payload.
func (s *Service) InboxGet(ctx context.Context, id string) (InboxDetail, error) {
	if !ids.Valid(id) {
		return InboxDetail{}, ErrInboxNotFound
	}
	var d InboxDetail
	var payload, headers, sum []byte
	it, err := scanInbox(s.Pool.QueryRow(ctx, `SELECT `+inboxCols+`, i.payload_redacted, i.raw_body_sha256, i.dispatched_to,
		i.parked_subject_id::text, i.parked_until_status, i.park_deadline_at, i.next_attempt_at, i.headers_subset
		FROM app.provider_webhook_inbox i JOIN app.payment_providers p ON p.id = i.provider_id WHERE i.id = $1`, id),
		&payload, &sum, &d.DispatchedTo, &d.ParkedSubjectID, &d.ParkedUntilStatus, &d.ParkDeadlineAt, &d.NextAttemptAt, &headers)
	if errors.Is(err, pgx.ErrNoRows) {
		return InboxDetail{}, ErrInboxNotFound
	}
	if err != nil {
		return InboxDetail{}, err
	}
	d.InboxItem = it
	d.RawBodySHA256 = hex.EncodeToString(sum)
	_ = json.Unmarshal(payload, &d.PayloadRedacted)
	_ = json.Unmarshal(headers, &d.HeadersSubset)
	if d.HeadersSubset == nil {
		d.HeadersSubset = map[string]string{}
	}
	return d, nil
}

// Rejection is a rejected webhook delivery (no payload).
type Rejection struct {
	ID           string    `json:"id"`
	ProviderCode string    `json:"provider_code"`
	ReasonCode   string    `json:"reason_code"`
	BodyBytes    int       `json:"body_bytes"`
	BodySHA256   *string   `json:"body_sha256"`
	ReceivedAt   time.Time `json:"received_at"`
}

// Rejections lists rejected deliveries newest first.
func (s *Service) Rejections(ctx context.Context, cursor string, limit int) ([]Rejection, *string, error) {
	before, beforeID, err := ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = PageLimit(limit)
	rows, err := s.Pool.Query(ctx, `SELECT id, provider_code, reason_code, body_bytes, body_sha256, received_at FROM app.provider_webhook_rejections
		WHERE ($1::timestamptz IS NULL OR (received_at, id) < ($1, $2::uuid)) ORDER BY received_at DESC, id DESC LIMIT $3`, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Rejection, error) {
		var x Rejection
		var sum []byte
		err := r.Scan(&x.ID, &x.ProviderCode, &x.ReasonCode, &x.BodyBytes, &sum, &x.ReceivedAt)
		if sum != nil {
			h := hex.EncodeToString(sum)
			x.BodySHA256 = &h
		}
		return x, err
	})
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = NextCursor(len(items), limit, last.ReceivedAt, last.ID)
	}
	return items, next, nil
}

// InboxCounts returns inbox rows by status and rejections in the last 24 hours.
func (s *Service) InboxCounts(ctx context.Context) (map[string]int, int, error) {
	out := map[string]int{}
	rows, err := s.Pool.Query(ctx, `SELECT status, count(*) FROM app.provider_webhook_inbox GROUP BY status`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return nil, 0, err
		}
		out[st] = n
	}
	rows.Close()
	var rej int
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.provider_webhook_rejections WHERE received_at > $1`, s.now().Add(-24*time.Hour)).Scan(&rej)
	return out, rej, err
}
