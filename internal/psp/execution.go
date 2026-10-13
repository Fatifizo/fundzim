package psp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Execution environments (Stage 9). One per process; never chosen per request, never by the frontend.
//
//	LOCAL_SIMULATOR   the FundZim simulator only (development, test, CI); payments are simulated
//	PROVIDER_SANDBOX  real provider adapters against their SANDBOX accounts (staging); payments are TEST payments
//	PRODUCTION        PRODUCTION accounts; refused at startup while LiveExecutionEnabled is false
//	DISABLED          no payment execution at all (the production default)
const (
	ExecDisabled        = "DISABLED"
	ExecLocalSimulator  = "LOCAL_SIMULATOR"
	ExecProviderSandbox = "PROVIDER_SANDBOX"
	ExecProduction      = "PRODUCTION"
)

// Contract bases: how much of an adapter's provider contract has been verified (Stage 9 brief §12).
const (
	ContractOfficial        = "OFFICIAL_PROVIDER_CONTRACT" // written in the provider's signed or official technical contract
	ContractSandboxObserved = "PROVIDER_SANDBOX_OBSERVED"  // exercised against the provider's sandbox
	ContractDocumented      = "DOCUMENTED_BUT_UNVERIFIED"  // taken from public documentation only
	ContractSimulated       = "SIMULATED"                  // FundZim's own simulator; no provider behind it
)

// ContractInfo describes an adapter's contract basis (shown to staff; recorded in provider-contracts.md).
type ContractInfo struct {
	Basis      string   `json:"basis"`
	Sources    []string `json:"sources"`
	VerifiedOn string   `json:"verified_on,omitempty"` // date the basis was last confirmed (YYYY-MM-DD)
}

// ContractDescriber is implemented by adapters that declare their contract basis.
type ContractDescriber interface {
	Contract() ContractInfo
}

// ContractOf returns a registered adapter's contract basis; an adapter that declares none is
// DOCUMENTED_BUT_UNVERIFIED (never assumed better).
func (s *Service) ContractOf(code string) (ContractInfo, bool) {
	a, ok := s.Adapter(code)
	if !ok {
		return ContractInfo{}, false
	}
	if d, ok := a.(ContractDescriber); ok {
		return d.Contract(), true
	}
	return ContractInfo{Basis: ContractDocumented}, true
}

// normaliseConfig fills defaults that mirror config loading (tests build Config directly).
func normaliseConfig(cfg Config) Config {
	if cfg.ExecEnv == "" {
		cfg.ExecEnv = ExecDisabled
		if cfg.SandboxEnabled {
			cfg.ExecEnv = ExecLocalSimulator
		}
	}
	if cfg.Allowlist == nil {
		cfg.Allowlist = map[string]bool{}
		if cfg.SandboxEnabled {
			cfg.Allowlist["sandbox"] = true
		}
	}
	return cfg
}

// Allowed reports whether a provider code is on this process's allowlist.
func (s *Service) Allowed(code string) bool { return s.Cfg.Allowlist[code] }

// Usable reports whether payments may run through a provider now (ADR-039 §6 and the Stage 9 execution
// environments). The simulator runs only in LOCAL_SIMULATOR; a real provider runs in PROVIDER_SANDBOX when its
// sandbox is enabled, and in PRODUCTION only when selected, enabled, and live execution is on (code constant AND
// kill switch). The allowlist applies to every provider.
func (s *Service) Usable(ctx context.Context, p Provider) (bool, error) {
	if !s.Allowed(p.Code) {
		return false, nil
	}
	if p.IsSandbox {
		return s.Cfg.ExecEnv == ExecLocalSimulator && s.Cfg.SandboxEnabled && p.Status == "ENABLED" && p.SandboxStatus == "ENABLED", nil
	}
	switch s.Cfg.ExecEnv {
	case ExecProviderSandbox:
		return p.SandboxStatus == "ENABLED", nil
	case ExecProduction:
		if p.Status != "ENABLED" || p.SelectionStatus != "SELECTED" {
			return false, nil
		}
		return s.LiveExecution(ctx)
	}
	return false, nil
}

// accountEnv is the provider-account environment this process may use for a provider ("" = none).
func (s *Service) accountEnv(p Provider) string {
	if p.IsSandbox {
		return EnvSandbox
	}
	switch s.Cfg.ExecEnv {
	case ExecProviderSandbox:
		return EnvSandbox
	case ExecProduction:
		return EnvProduction
	}
	return ""
}

// CredentialScopeOK reports whether every secret reference of an account lives under the account's own
// environment (secretref://fundzim/<environment>/…), the rule the DB CHECK enforces too.
func CredentialScopeOK(a Account) bool {
	prefix := "secretref://fundzim/" + strings.ToLower(a.Environment) + "/"
	for _, ref := range []string{a.APISecretRef, a.WebhookSecretRef, a.WebhookSecretRefPrevious} {
		if ref != "" && (!strings.HasPrefix(ref, prefix) || !ValidSecretRef(ref)) {
			return false
		}
	}
	return a.APISecretRef != ""
}

// CredentialsUsable reports whether an account's credentials are in scope and resolve now (the previous webhook
// key is optional). Values are discarded immediately.
func (s *Service) CredentialsUsable(ctx context.Context, a Account) bool {
	if !CredentialScopeOK(a) {
		return false
	}
	if !Resolvable(ctx, s.Secrets, a.APISecretRef) {
		return false
	}
	return a.WebhookSecretRef == "" || Resolvable(ctx, s.Secrets, a.WebhookSecretRef)
}

// ---------------------------------------------------------------------------------------------------------
// Startup gate
// ---------------------------------------------------------------------------------------------------------

// ProviderGate is one provider's state in the startup gate report.
type ProviderGate struct {
	Code              string `json:"code"`
	Sandbox           bool   `json:"sandbox"`
	Allowlisted       bool   `json:"allowlisted"`
	AdapterRegistered bool   `json:"adapter_registered"`
	Usable            bool   `json:"usable"`
	AccountEnv        string `json:"account_environment,omitempty"`
	HasAccount        bool   `json:"has_account"`
	CredentialsUsable bool   `json:"credentials_usable"`
}

// GateReport is the startup evaluation of the execution gates (audited).
type GateReport struct {
	ExecEnv               string         `json:"execution_environment"`
	LiveExecutionConstant bool           `json:"live_execution_constant"`
	KillSwitch            bool           `json:"kill_switch"`
	Allowlist             []string       `json:"allowlist"`
	SecretsProvider       string         `json:"secrets_provider"`
	Providers             []ProviderGate `json:"providers"`
	Refused               bool           `json:"refused"`
	Reasons               []string       `json:"reasons,omitempty"`
}

// ErrStartupRefused is returned by StartupGate when this process must not start.
var ErrStartupRefused = errors.New("psp: payment execution startup refused")

// StartupGate evaluates the execution gates once at startup and returns ErrStartupRefused when the configuration
// asks for something the code forbids: PRODUCTION while live execution is disabled in code, or provider execution
// without the secret manager. The refusal needs no database. The evaluation is then audited
// (psp.execution_gate.evaluated) and each provider whose effective configuration changed since the last record gets
// psp.provider.configured (PROVIDER_CONFIGURED); those records are best effort: a database that is not reachable yet
// is logged, never a reason to start with weaker gates (the gates themselves are evaluated per request).
func (s *Service) StartupGate(ctx context.Context, secretsProvider, process string) (GateReport, error) {
	r := GateReport{ExecEnv: s.Cfg.ExecEnv, LiveExecutionConstant: LiveExecutionEnabled, SecretsProvider: secretsProvider, Allowlist: []string{}}
	for code := range s.Cfg.Allowlist {
		r.Allowlist = append(r.Allowlist, code)
	}
	sort.Strings(r.Allowlist)
	if s.Cfg.ExecEnv == ExecProduction && !LiveExecutionEnabled {
		r.Refused = true
		r.Reasons = append(r.Reasons, "PAYMENTS_EXECUTION_ENVIRONMENT=PRODUCTION while live execution is disabled in code (psp.LiveExecutionEnabled = false)")
	}
	if (s.Cfg.ExecEnv == ExecProduction || s.Cfg.ExecEnv == ExecProviderSandbox) && secretsProvider != "kv2" {
		r.Refused = true
		r.Reasons = append(r.Reasons, "provider execution requires the secret manager (SECRETS_PROVIDER=kv2)")
	}
	if err := s.evaluateProviders(ctx, &r, process); err != nil {
		s.Logger.Warn("psp: startup gate records skipped (database not reachable?)", "err", err)
	}
	if r.Refused {
		return r, ErrStartupRefused
	}
	return r, nil
}

// evaluateProviders fills the per-provider part of the report and writes the audit records.
func (s *Service) evaluateProviders(ctx context.Context, r *GateReport, process string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r.KillSwitch = s.KillSwitch(ctx)
	providers, err := s.Providers(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range providers {
		known[p.Code] = true
		g := ProviderGate{Code: p.Code, Sandbox: p.IsSandbox, Allowlisted: s.Allowed(p.Code), AccountEnv: s.accountEnv(p)}
		_, g.AdapterRegistered = s.Adapter(p.Code)
		if g.Usable, err = s.Usable(ctx, p); err != nil {
			return err
		}
		if acct, err := s.ActiveAccount(ctx, p); err == nil {
			g.HasAccount = true
			g.CredentialsUsable = s.CredentialsUsable(ctx, acct)
		} else if !errors.Is(err, ErrNoAccount) {
			return err
		}
		r.Providers = append(r.Providers, g)
		if err := s.recordConfigured(ctx, p, g, process); err != nil {
			return err
		}
	}
	for _, code := range r.Allowlist {
		if !known[code] {
			r.Reasons = append(r.Reasons, "allow-listed provider "+code+" is not registered (ignored)")
		}
	}
	outcome := "success"
	if r.Refused {
		outcome = "denied"
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "psp.execution_gate.evaluated", ActorType: "system",
			TargetType: "payment_execution", Outcome: outcome, Reason: strings.Join(r.Reasons, "; "),
			Metadata: map[string]any{"process": process, "execution_environment": r.ExecEnv, "live_execution_constant": r.LiveExecutionConstant,
				"kill_switch": r.KillSwitch, "allowlist": r.Allowlist, "key_store": r.SecretsProvider}})
	})
}

// recordConfigured audits PROVIDER_CONFIGURED when a provider's effective configuration in this execution
// environment differs from the last recorded one (so restarts do not repeat it).
func (s *Service) recordConfigured(ctx context.Context, p Provider, g ProviderGate, process string) error {
	md := map[string]any{"audit_event": "PROVIDER_CONFIGURED", "provider": p.Code, "execution_environment": s.Cfg.ExecEnv,
		"allowlisted": g.Allowlisted, "adapter_registered": g.AdapterRegistered, "usable": g.Usable, "account_environment": g.AccountEnv,
		"has_account": g.HasAccount, "references_resolve": g.CredentialsUsable}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		last, err := audit.LatestMetadata(ctx, tx, "psp.provider.configured", p.ID)
		if err != nil {
			return err
		}
		if last != nil && sameJSON(last, md) {
			return nil
		}
		md["process"] = process
		return audit.Record(ctx, tx, audit.Event{Action: "psp.provider.configured", ActorType: "system", TargetType: "payment_provider",
			TargetID: p.ID, Metadata: md})
	})
}

// ---------------------------------------------------------------------------------------------------------
// Staff provider controls (payment.operate, step-up). None of them can enable production execution.
// ---------------------------------------------------------------------------------------------------------

// Provider control errors.
var (
	ErrProviderRetired       = errs.New(errs.Conflict, "PROVIDER_RETIRED", "This provider is retired.")
	ErrProviderNotConfigured = errs.New(errs.Conflict, "PROVIDER_NOT_CONFIGURED", "This provider has no enabled sandbox account with credentials in the secret manager.")
	ErrOutageState           = errs.New(errs.Conflict, "PROVIDER_OUTAGE_STATE", "The provider is not in the state this action requires.")
	ErrReasonRequired        = errs.New(errs.Invalid, errs.CodeValidationFailed, "A reason is required.")
)

func checkReason(reason string) error {
	if n := len(strings.TrimSpace(reason)); n < 5 || n > 500 {
		return ErrReasonRequired
	}
	return nil
}

// EnableSandbox enables SANDBOX execution for a provider (PROVIDER_ENABLED_SANDBOX). A real provider must have
// an enabled SANDBOX account whose credentials are scoped and resolve. Production enablement is migration-only
// (selection under an ADR) and is not touched.
func (s *Service) EnableSandbox(ctx context.Context, actorID, providerID, reason string) (Provider, error) {
	if err := checkReason(reason); err != nil {
		return Provider{}, err
	}
	p, err := s.ProviderByID(ctx, providerID)
	if err != nil {
		return Provider{}, err
	}
	if p.Status == "RETIRED" {
		return Provider{}, ErrProviderRetired
	}
	acct, err := scanAccount(s.Pool.QueryRow(ctx, `SELECT `+accountCols+` FROM app.provider_accounts
		WHERE provider_id = $1 AND environment = 'SANDBOX' AND status = 'ENABLED' ORDER BY created_at, id LIMIT 1`, p.ID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !s.CredentialsUsable(ctx, acct)) {
		return Provider{}, ErrProviderNotConfigured
	}
	if err != nil {
		return Provider{}, err
	}
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app.payment_providers SET sandbox_status = 'ENABLED', version = version + 1 WHERE id = $1`, p.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "psp.provider.sandbox_enabled", ActorType: "staff", ActorID: actorID,
			TargetType: "payment_provider", TargetID: p.ID, Reason: reason,
			Metadata: map[string]any{"audit_event": "PROVIDER_ENABLED_SANDBOX", "provider": p.Code, "account_id": acct.ID}})
	})
	if err != nil {
		return Provider{}, err
	}
	return s.ProviderByID(ctx, p.ID)
}

// Disable stops a provider (PROVIDER_DISABLED): sandbox execution off and, for a real provider, production
// status DISABLED too (re-enabling production needs a migration under an ADR). Existing payments keep being
// serviced by status queries and webhooks only where the provider stays usable; nothing is marked failed.
func (s *Service) Disable(ctx context.Context, actorID, providerID, reason string) (Provider, error) {
	if err := checkReason(reason); err != nil {
		return Provider{}, err
	}
	p, err := s.ProviderByID(ctx, providerID)
	if err != nil {
		return Provider{}, err
	}
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := `UPDATE app.payment_providers SET sandbox_status = 'DISABLED', version = version + 1 WHERE id = $1`
		if !p.IsSandbox {
			q = `UPDATE app.payment_providers SET sandbox_status = 'DISABLED',
				status = CASE WHEN status = 'ENABLED' THEN 'DISABLED' ELSE status END, version = version + 1 WHERE id = $1`
		}
		if _, err := tx.Exec(ctx, q, p.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "psp.provider.disabled", ActorType: "staff", ActorID: actorID,
			TargetType: "payment_provider", TargetID: p.ID, Reason: reason,
			Metadata: map[string]any{"audit_event": "PROVIDER_DISABLED", "provider": p.Code, "previous_status": p.Status,
				"previous_sandbox_status": p.SandboxStatus}})
	})
	if err != nil {
		return Provider{}, err
	}
	return s.ProviderByID(ctx, p.ID)
}

// DeclareOutage records a staff-declared outage (health UNAVAILABLE until resolved; PROVIDER_UNAVAILABLE audit).
// Routing excludes the provider while the outage is open (KI-S8-12).
func (s *Service) DeclareOutage(ctx context.Context, actorID, providerID, reason string) error {
	return s.outage(ctx, actorID, providerID, reason, true)
}

// ResolveOutage closes a staff-declared outage.
func (s *Service) ResolveOutage(ctx context.Context, actorID, providerID, reason string) error {
	return s.outage(ctx, actorID, providerID, reason, false)
}

func (s *Service) outage(ctx context.Context, actorID, providerID, reason string, declare bool) error {
	if err := checkReason(reason); err != nil {
		return err
	}
	p, err := s.ProviderByID(ctx, providerID)
	if err != nil {
		return err
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM app.payment_providers WHERE id = $1 FOR UPDATE`, p.ID); err != nil {
			return err
		}
		open, err := outageOpen(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if open == declare {
			return ErrOutageState
		}
		kind, action, event := "OUTAGE_DECLARED", "psp.provider.unavailable", "PROVIDER_UNAVAILABLE"
		if !declare {
			kind, action, event = "OUTAGE_RESOLVED", "psp.provider.outage_resolved", "PROVIDER_OUTAGE_RESOLVED"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.provider_health_events (id, provider_id, operation, event_kind, detail, actor_id, observed_at)
			VALUES ($1, $2, 'ALL', $3, jsonb_build_object('reason', $4::text), $5, $6)`, ids.New(), p.ID, kind, reason, actorID, s.now()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: action, ActorType: "staff", ActorID: actorID, TargetType: "payment_provider",
			TargetID: p.ID, Reason: reason, Metadata: map[string]any{"audit_event": event, "provider": p.Code, "source": "STAFF_DECLARED"}})
	})
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func outageOpen(ctx context.Context, q queryer, providerID string) (bool, error) {
	var kind string
	err := q.QueryRow(ctx, `SELECT event_kind FROM app.provider_health_events WHERE provider_id = $1
		AND event_kind IN ('OUTAGE_DECLARED','OUTAGE_RESOLVED') ORDER BY observed_at DESC, id DESC LIMIT 1`, providerID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return kind == "OUTAGE_DECLARED", err
}

// RecordCredentialRotation audits a secret-manager version change of a provider secret
// (PROVIDER_CREDENTIAL_ROTATED). Only the reference and versions are recorded, never a value.
func (s *Service) RecordCredentialRotation(ref string, from, to int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id::text, p.code FROM app.provider_accounts a JOIN app.payment_providers p ON p.id = a.provider_id
			WHERE $1 IN (a.api_credentials_secret_ref, a.webhook_secret_ref, a.webhook_secret_ref_previous) ORDER BY a.id`, ref)
		if err != nil {
			return err
		}
		var accounts, providers []string
		for rows.Next() {
			var a, p string
			if err := rows.Scan(&a, &p); err != nil {
				rows.Close()
				return err
			}
			accounts, providers = append(accounts, a), append(providers, p)
		}
		rows.Close()
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "psp.provider.credential_rotated", ActorType: "system",
			TargetType: "secret_reference", Metadata: map[string]any{"audit_event": "PROVIDER_CREDENTIAL_ROTATED", "reference": ref,
				"from_version": from, "to_version": to, "account_ids": accounts, "providers": providers}})
	})
	if err != nil {
		s.Logger.Warn("psp: rotation audit failed", "reference", ref, "err", err)
	}
}

// sameJSON compares stored JSON with a metadata map by value, ignoring the "process" key.
func sameJSON(stored []byte, md map[string]any) bool {
	b, err := json.Marshal(md)
	if err != nil {
		return false
	}
	var x, y map[string]any
	if json.Unmarshal(stored, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	delete(x, "process")
	delete(y, "process")
	return reflect.DeepEqual(x, y)
}
