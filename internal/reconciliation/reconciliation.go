// Package reconciliation is the Stage 17 reconciliation and settlement-operations module (ADR-049, implementing ADR-030):
// immutable, hashed, deduplicated statement imports (provider transaction reports, provider settlement reports and
// FundZim's own bank statement) in FundZim's canonical format; a deterministic, versioned, explainable matcher that
// links statement lines to payments, payouts and fee remittances and checks amount, currency, status, fee and ledger
// journal; tracked exceptions with an assignment and investigation lifecycle; maker-checker resolutions (including
// statement-evidenced outcomes for UNKNOWN payments and payouts, applied by their owning modules, and ledger adjustments
// through the ledger's own maker-checker); reports, operations snapshot and worker health.
//
// It owns the recon schema and nothing else. Everything is TEST: no provider is selected (ADR-040), no real statement
// exists, no settlement is posted (SettlementPostingEnabled = false), and production imports and adjustments are refused.
package reconciliation

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Production gates (ADR-049). Each needs an ADR, a provider selection, the provider's documented statement format,
// an accounting review (KI-S10-02), legal sign-off and stage acceptance before it may change.
const (
	// ProductionImportsEnabled: a production process refuses every import (sources are also CHECK-limited to TEST).
	ProductionImportsEnabled = false
	// SettlementPostingEnabled: matches never post SETTLEMENT_MATCHED / SETTLEMENT_DISCREPANCY journals (ADR-049 §8).
	SettlementPostingEnabled = false
	// ProductionAdjustmentsEnabled: a LEDGER_ADJUSTMENT resolution is refused when the ledger is not TEST.
	ProductionAdjustmentsEnabled = false
)

// MatcherVersion identifies the matching code; every match and exception records it with the rule set version.
const MatcherVersion = "MATCHER_V1"

// Consumer and job names (dedupe keys; never rename).
const (
	ConsumerImports = "reconciliation.imports"
	JobParse        = "reconciliation.parse"
	JobMatch        = "reconciliation.match"
	JobAgeing       = "reconciliation.ageing"
)

// Permissions.
const (
	PermView         = "reconciliation.view"
	PermImport       = "reconciliation.import"
	PermRun          = "reconciliation.run"
	PermInvestigate  = "reconciliation.investigate"
	PermResolve      = "reconciliation.resolve" // checker: approve or reject resolutions (step-up)
	PermRulesRequest = "reconciliation.rules.request"
	PermRulesApprove = "reconciliation.rules.approve"
	PermExport       = "reconciliation.report.export"
)

// Source kinds and their authority ranks (ADR-049 §3).
const (
	KindTransactionReport = "PROVIDER_TRANSACTION_REPORT"
	KindSettlementReport  = "PROVIDER_SETTLEMENT_REPORT"
	KindBankStatement     = "BANK_STATEMENT"
)

// Payments is what reconciliation needs from payments (implemented by *payments.Service).
type Payments interface {
	ReconFacts(ctx context.Context, providerCode string, paymentIDs, providerRefs []string) ([]payments.ReconFact, error)
	ReconSucceededInWindow(ctx context.Context, providerCode, currency string, from, to time.Time, limit int) ([]payments.ReconFact, error)
	ApplyReconciliationOutcome(ctx context.Context, in payments.ReconOutcome) (payments.ReconApplyResult, error)
}

// Payouts is what reconciliation needs from payouts (implemented by *payouts.Service).
type Payouts interface {
	ReconFacts(ctx context.Context, providerCode string, payoutIDs, providerRefs []string) ([]payouts.ReconFact, error)
	ReconCompletedInWindow(ctx context.Context, providerCode, currency string, from, to time.Time, limit int) ([]payouts.ReconFact, error)
	ApplyReconciliationOutcome(ctx context.Context, in payouts.ReconOutcome) (bool, error)
}

// Fees is what reconciliation needs from fees (implemented by *fees.Service).
type Fees interface {
	ProviderFeeFacts(ctx context.Context, sourceType string, sourceIDs []string) (map[string]fees.ProviderFeeFact, error)
}

// Ledger is what reconciliation needs from the ledger (implemented by *ledger.Service).
type Ledger interface {
	JournalsByKeys(ctx context.Context, keys []string) (map[string]ledger.JournalRef, error)
	Environment(ctx context.Context) (string, error)
	RequestAdjustment(ctx context.Context, makerID string, in ledger.AdjustmentRequest) (ledger.Adjustment, error)
	ApproveAdjustment(ctx context.Context, checkerID, id, justification string) (ledger.Adjustment, error)
	RejectAdjustment(ctx context.Context, checkerID, id, reason string) (ledger.Adjustment, error)
	WithdrawAdjustment(ctx context.Context, makerID, id, reason string) (ledger.Adjustment, error)
	AdjustmentByID(ctx context.Context, id string) (ledger.Adjustment, error)
}

// Storage stores and reads statement files (implemented by *storage.Service).
type Storage interface {
	Upload(ctx context.Context, in storage.UploadInput) (storage.Object, error)
	Get(ctx context.Context, id string) (storage.Object, error)
	Open(ctx context.Context, id string) (io.ReadCloser, storage.Object, error)
}

// Sandbox generates TEST statements from the simulator's own records (nil in production: the sandbox is refused).
type Sandbox interface {
	StatementLines(ctx context.Context, currency string, from, to time.Time, limit int) ([]sandbox.StatementLine, error)
}

// Identities resolves linked accounts (kyc.Service.ActorIdentities) for maker-checker separation.
type Identities interface {
	ActorIdentities(ctx context.Context, actorID string) ([]string, error)
}

// Directory lists staff holding a permission (auth.Service.StaffWithPermission): assignees must be investigators.
type Directory interface {
	StaffWithPermission(ctx context.Context, permission string) ([]string, error)
}

// Service is the reconciliation module.
type Service struct {
	Pool     *pgxpool.Pool // fundzim_app (API) or fundzim_worker (jobs)
	Payments Payments
	Payouts  Payouts
	Fees     Fees
	Ledger   Ledger
	Storage  Storage
	Sandbox  Sandbox
	// read models (optional: a view without its source reports NOT_AVAILABLE)
	LedgerReads LedgerReads
	FeeReads    FeeReads
	PayoutReads PayoutReads
	Identities  Identities
	Directory   Directory
	Keyed       *crypto.Keyed // bank account references are kept only as a keyed hash
	Clock       clock.Clock
	Logger      *slog.Logger
	Production  bool
	// MaxFileBytes caps a statement file (default 10 MiB; never above the storage upload cap).
	MaxFileBytes int64
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Errors.
var (
	ErrNotFound         = errs.New(errs.NotFound, "NOT_FOUND", "Not found.")
	ErrState            = errs.New(errs.Conflict, "INVALID_STATE", "This record is not in a state that allows that action.")
	ErrStale            = errs.New(errs.Conflict, "STALE_VERSION", "This record changed since you loaded it. Reload and try again.")
	ErrSelf             = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot decide on a request you made (or one made by a linked account).")
	ErrProductionImport = errs.New(errs.Forbidden, "PRODUCTION_IMPORTS_DISABLED", "Statement imports are disabled in production: no provider is selected and only TEST evidence exists.")
	ErrNoRuleSet        = errs.New(errs.Conflict, "NO_ACTIVE_RULE_SET", "No matching rule set is active. FINANCE must propose and approve one before matching can run.")
	ErrRunActive        = errs.New(errs.Conflict, "RUN_ACTIVE", "A reconciliation run for this source and currency is already queued or running.")
	ErrSandboxOff       = errs.New(errs.Unavailable, "SANDBOX_UNAVAILABLE", "The sandbox simulator is not available in this environment.")
	ErrExpired          = errs.New(errs.Conflict, "RESOLUTION_EXPIRED", "This resolution has expired. Propose it again.")
	ErrHashMismatch     = errs.New(errs.Conflict, "PROPOSAL_CHANGED", "The proposal you reviewed is not the one on record.")
	ErrAdjustmentsOff   = errs.New(errs.Forbidden, "PRODUCTION_ADJUSTMENTS_DISABLED", "Ledger adjustments from reconciliation are only possible in the TEST ledger.")
)

// identities returns the actor and linked accounts; fails closed when the resolver errors.
func (s *Service) identities(ctx context.Context, actorID string) (map[string]bool, error) {
	out := map[string]bool{actorID: true}
	if s.Identities == nil {
		return out, nil
	}
	l, err := s.Identities.ActorIdentities(ctx, actorID)
	if err != nil {
		return nil, err
	}
	for _, id := range l {
		out[id] = true
	}
	return out, nil
}
