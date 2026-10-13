// Package ledger is FundZim's double-entry ledger (Stage 10; ADR-006, ADR-014, ADR-023, ADR-042). It records
// FundZim's claims and obligations for money that licensed providers collect, hold and disburse (Model A): it is
// not a statement of FundZim-owned cash and creates no spendable balance (LR-001, LR-002, LR-003).
//
// Every journal is posted through a named, versioned posting rule, in one currency, balanced, sealed in its own
// database transaction and immutable afterwards. The database re-checks everything Go checks (deferred constraint
// trigger, rule lines, account status, periods, non-negative obligations, environment designation), and only the
// dedicated fundzim_ledger role can write the ledger schema. Corrections are maker-checker reversals and
// replacements; nothing is updated or deleted.
//
// Stage 10 posts into a TEST ledger only. Production posting is disabled by ProductionPostingEnabled (a constant),
// by the database designation (ledger.ledger_environment = TEST) and by ck_*_no_production_stage10 CHECKs. Payments
// do not post: the payments consumer records each payment success as a NOT_ELIGIBLE or BLOCKED source reference.
// Only TEST_ONLY synthetic fixtures create journals.
//
// ledger imports platform and audit only. Payment facts reach reconciliation through the PaymentFacts interface,
// which the composition root implements over the payments module.
package ledger

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// ProductionPostingEnabled gates every PRODUCTION posting. It is a constant on purpose: enabling real accounting
// needs an ADR, accounting and legal approval (LR-001, LR-002, LR-003, LR-028) and owner acceptance, not a setting.
const ProductionPostingEnabled = false

// Ledger environments (one per database: ledger.ledger_environment).
const (
	EnvTest       = "TEST"
	EnvProduction = "PRODUCTION"
)

// Directions.
const (
	Debit  = "DEBIT"
	Credit = "CREDIT"
)

// Source systems.
const (
	SourcePayments       = "payments"
	SourceReconciliation = "reconciliation"
	SourceStaff          = "staff"
	SourceSynthetic      = "synthetic_fixture"
)

// Evidence classes (brief §23). REJECTED evidence is never posted; SIMULATED only in a TEST ledger.
const (
	EvidenceSimulated          = "SIMULATED"
	EvidenceProviderReported   = "PROVIDER_REPORTED"
	EvidenceProviderVerified   = "PROVIDER_VERIFIED"
	EvidenceSettlementVerified = "SETTLEMENT_VERIFIED"
	EvidenceManuallyReviewed   = "MANUALLY_REVIEWED"
	EvidenceRejected           = "REJECTED"
)

// Account classes: the closed chart of accounts (settlement-and-custody-model §3.2; ledger.account_class_signature).
const (
	ClassPSPClearing           = "asset:psp_clearing"
	ClassPSPSettled            = "asset:psp_settled"
	ClassOperatingBank         = "asset:fundzim_operating_bank"
	ClassChargebackRecoverable = "asset:chargeback_recoverable"
	ClassRefundRecoverable     = "asset:refund_recoverable"
	ClassPSPPayoutFloat        = "asset:psp_payout_float"
	ClassCampaignUnsettled     = "liability:campaign_unsettled"
	ClassCampaignPayable       = "liability:campaign_payable"
	ClassCampaignReserve       = "liability:campaign_reserve"
	ClassCampaignPayoutPend    = "liability:campaign_payout_pending"
	ClassPayoutInTransit       = "liability:payout_in_transit"
	ClassCampaignHeld          = "liability:campaign_held"
	ClassRefundPayable         = "liability:refund_payable"
	ClassPlatformFees          = "revenue:platform_fees"
	ClassPSPProcessingFees     = "expense:psp_processing_fees"
	ClassChargebackLosses      = "expense:chargeback_losses"
	ClassRefundLosses          = "expense:refund_losses"
	ClassEquityPlatform        = "equity:platform"
	ClassSuspense              = "suspense:settlement_discrepancy"
)

// ClassSpec mirrors ledger.account_class_signature (a unit test keeps them identical).
type ClassSpec struct {
	Type, NormalBalance, Kind, OwnerType string
}

// Chart is the chart of accounts.
var Chart = map[string]ClassSpec{
	ClassPSPClearing:           {"ASSET", Debit, "CLAIM_ON_PROVIDER", "PROVIDER"},
	ClassPSPSettled:            {"ASSET", Debit, "PROVIDER_POOL", "PROVIDER"},
	ClassOperatingBank:         {"ASSET", Debit, "FUNDZIM_CASH", "PLATFORM"},
	ClassChargebackRecoverable: {"ASSET", Debit, "RECOVERABLE", "PLATFORM"},
	ClassRefundRecoverable:     {"ASSET", Debit, "RECOVERABLE", "CAMPAIGN"},
	ClassPSPPayoutFloat:        {"ASSET", Debit, "PROVIDER_POOL", "PROVIDER"},
	ClassCampaignUnsettled:     {"LIABILITY", Credit, "OBLIGATION", "CAMPAIGN"},
	ClassCampaignPayable:       {"LIABILITY", Credit, "OBLIGATION", "CAMPAIGN"},
	ClassCampaignReserve:       {"LIABILITY", Credit, "OBLIGATION", "CAMPAIGN"},
	ClassCampaignPayoutPend:    {"LIABILITY", Credit, "OBLIGATION", "CAMPAIGN"},
	ClassPayoutInTransit:       {"LIABILITY", Credit, "OBLIGATION", "PROVIDER"},
	ClassCampaignHeld:          {"LIABILITY", Credit, "OBLIGATION", "CAMPAIGN"},
	ClassRefundPayable:         {"LIABILITY", Credit, "OBLIGATION", "PLATFORM"},
	ClassPlatformFees:          {"REVENUE", Credit, "RESULT", "PLATFORM"},
	ClassPSPProcessingFees:     {"EXPENSE", Debit, "RESULT", "PLATFORM"},
	ClassChargebackLosses:      {"EXPENSE", Debit, "RESULT", "PLATFORM"},
	ClassRefundLosses:          {"EXPENSE", Debit, "RESULT", "PLATFORM"},
	ClassEquityPlatform:        {"EQUITY", Credit, "RESULT", "PLATFORM"},
	ClassSuspense:              {"SUSPENSE", Debit, "SUSPENSE", "PROVIDER"},
}

// NonNegative reports whether an account class may never go below zero (L8/L13: obligations and recoverables).
func (c ClassSpec) NonNegative() bool { return c.Kind == "OBLIGATION" || c.Kind == "RECOVERABLE" }

// AccountRef names an account: its class and, for owned classes, the owner (campaign or provider id).
type AccountRef struct {
	Class   string
	OwnerID string
}

// Code is the deterministic account code (ck_ledger_accounts_code).
func (a AccountRef) Code() string {
	if a.OwnerID == "" {
		return a.Class
	}
	return a.Class + ":" + a.OwnerID
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ParseAccountCode splits "class[:owner]" (owner is a lower-case uuid).
func ParseAccountCode(code string) (AccountRef, bool) {
	if spec, ok := Chart[code]; ok {
		return AccountRef{Class: code}, spec.OwnerType == "PLATFORM" // an owned class needs its owner suffix
	}
	i := strings.LastIndex(code, ":")
	if i < 0 {
		return AccountRef{}, false
	}
	ref := AccountRef{Class: code[:i], OwnerID: code[i+1:]}
	spec, ok := Chart[ref.Class]
	if !ok || spec.OwnerType == "PLATFORM" || !uuidRE.MatchString(ref.OwnerID) {
		return AccountRef{}, false
	}
	return ref, true
}

// Line is one journal line. Amount is positive minor units; Direction carries the sign.
type Line struct {
	Account   AccountRef
	Direction string
	Amount    int64
}

// Dr and Cr build lines.
func Dr(a AccountRef, amount int64) Line { return Line{Account: a, Direction: Debit, Amount: amount} }
func Cr(a AccountRef, amount int64) Line { return Line{Account: a, Direction: Credit, Amount: amount} }

// Actor is who posts: a SYSTEM component name or a STAFF user id.
type Actor struct {
	Type string // SYSTEM | STAFF
	ID   string
}

// Posting is one journal request.
type Posting struct {
	Rule           string
	IdempotencyKey string // deterministic from the business event; never random or time-based
	Currency       string
	OccurredAt     time.Time
	Description    string
	SourceSystem   string
	SourceType     string
	SourceID       string
	SourceEventID  string
	EvidenceClass  string
	ProviderCode   string
	ProviderRef    string
	AdjustmentID   string
	Reverses       string
	Reason         string
	Actor          Actor
	CorrelationID  string
	Metadata       map[string]string
	Lines          []Line
}

// Errors (stable codes).
var (
	ErrInvalidPosting      = errs.New(errs.Unprocessable, "LEDGER_INVALID_POSTING", "The posting request is invalid.")
	ErrUnbalanced          = errs.New(errs.Unprocessable, "LEDGER_UNBALANCED", "Debits and credits differ.")
	ErrRuleViolation       = errs.New(errs.Unprocessable, "LEDGER_RULE_VIOLATION", "A line is not allowed by the posting rule.")
	ErrCurrency            = errs.New(errs.Unprocessable, "LEDGER_CURRENCY_NOT_POSTABLE", "This currency cannot be posted (unverified minor units or mismatch).")
	ErrInsufficientFunds   = errs.New(errs.Conflict, "LEDGER_INSUFFICIENT_FUNDS", "The posting would take a non-negative account below zero.")
	ErrPeriodClosed        = errs.New(errs.Conflict, "LEDGER_PERIOD_CLOSED", "The accounting period is closed for this posting.")
	ErrIdempotencyConflict = errs.New(errs.Conflict, "LEDGER_IDEMPOTENCY_CONFLICT", "This posting key was already used for a different journal.")
	ErrAccountFrozen       = errs.New(errs.Conflict, "LEDGER_ACCOUNT_FROZEN", "The account is frozen: only approved adjustments may post to it.")
	ErrAccountClosed       = errs.New(errs.Conflict, "LEDGER_ACCOUNT_CLOSED", "The account is closed.")
	ErrProductionDisabled  = errs.New(errs.Unavailable, "LEDGER_PRODUCTION_POSTING_DISABLED", "Production ledger posting is disabled.")
	ErrEnvironment         = errs.New(errs.Conflict, "LEDGER_ENVIRONMENT_MISMATCH", "The posting does not belong to this ledger environment.")
	ErrEvidence            = errs.New(errs.Unprocessable, "LEDGER_EVIDENCE_NOT_POSTABLE", "This evidence class cannot be posted in this ledger.")
	ErrRejectedByDatabase  = errs.New(errs.Conflict, "LEDGER_REJECTED", "The ledger database rejected the posting.")
	ErrNotFound            = errs.New(errs.NotFound, "LEDGER_NOT_FOUND", "No such ledger record.")
	errRetry               = errors.New("ledger: concurrent writer won; retry")
)

// Service is the ledger. Pool connects as fundzim_ledger (the only ledger writer).
type Service struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Logger *slog.Logger
	Facts  PaymentFacts // reconciliation input (nil: payment reconciliation skipped)
	// SyntheticEnabled allows TEST_ONLY synthetic fixtures (TEST ledger, non-production deployments only).
	SyntheticEnabled bool

	envMu sync.RWMutex
	env   string

	rulesMu sync.RWMutex
	rules   map[string]Rule
}

// New builds the service.
func New(pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger) *Service {
	if clk == nil {
		clk = clock.System
	}
	return &Service{Pool: pool, Clock: clk, Logger: logger}
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

// rowQuerier is a pool or a transaction. Lazy loads inside a posting use the caller's transaction: a second pool
// connection could deadlock a saturated pool (every connection inside a posting transaction waiting for one more).
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Environment returns this database's ledger designation (TEST in Stage 10).
func (s *Service) Environment(ctx context.Context) (string, error) { return s.environment(ctx, s.Pool) }

func (s *Service) environment(ctx context.Context, q rowQuerier) (string, error) {
	s.envMu.RLock()
	env := s.env
	s.envMu.RUnlock()
	if env != "" {
		return env, nil
	}
	if err := q.QueryRow(ctx, `SELECT ledger.database_environment()`).Scan(&env); err != nil {
		return "", err
	}
	s.envMu.Lock()
	s.env = env
	s.envMu.Unlock()
	return env, nil
}

// Warm loads the environment and the rule registry (startup), so postings never need a lazy load.
func (s *Service) Warm(ctx context.Context) error {
	if _, err := s.Environment(ctx); err != nil {
		return err
	}
	return s.LoadRules(ctx)
}

// Rule is a posting rule (current version) with its allowed lines.
type Rule struct {
	Code               string
	Version            int
	SourceTypes        []string
	MirrorOfReversed   bool
	RequiresApproval   bool
	AllowsPriorPeriod  bool
	SingleOwnerPerType bool
	Lines              map[string]bool // "DIRECTION|class" allowed
	Required           []string        // "DIRECTION|class" required
}

// LoadRules (re)loads the current version of every posting rule.
func (s *Service) LoadRules(ctx context.Context) error { return s.loadRules(ctx, s.Pool) }

func (s *Service) loadRules(ctx context.Context, q rowQuerier) error {
	rows, err := q.Query(ctx, `SELECT r.code, r.version, r.allowed_source_types, r.line_check = 'MIRROR_OF_REVERSED', r.requires_approval,
		r.allows_prior_period, r.single_owner_per_type FROM ledger.ledger_posting_rules r
		WHERE r.version = (SELECT max(version) FROM ledger.ledger_posting_rules x WHERE x.code = r.code)`)
	if err != nil {
		return err
	}
	rules := map[string]Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.Code, &r.Version, &r.SourceTypes, &r.MirrorOfReversed, &r.RequiresApproval, &r.AllowsPriorPeriod,
			&r.SingleOwnerPerType); err != nil {
			rows.Close()
			return err
		}
		r.Lines = map[string]bool{}
		rules[r.Code] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	lrows, err := q.Query(ctx, `SELECT rule_code, rule_version, direction, account_class, is_required FROM ledger.ledger_posting_rule_lines`)
	if err != nil {
		return err
	}
	defer lrows.Close()
	for lrows.Next() {
		var code, dir, class string
		var ver int
		var req bool
		if err := lrows.Scan(&code, &ver, &dir, &class, &req); err != nil {
			return err
		}
		r, ok := rules[code]
		if !ok || r.Version != ver {
			continue
		}
		r.Lines[dir+"|"+class] = true
		if req {
			r.Required = append(r.Required, dir+"|"+class)
		}
		rules[code] = r
	}
	if err := lrows.Err(); err != nil {
		return err
	}
	s.rulesMu.Lock()
	s.rules = rules
	s.rulesMu.Unlock()
	return nil
}

func (s *Service) rule(ctx context.Context, code string) (Rule, bool, error) {
	return s.ruleQ(ctx, s.Pool, code)
}

func (s *Service) ruleQ(ctx context.Context, q rowQuerier, code string) (Rule, bool, error) {
	s.rulesMu.RLock()
	rules := s.rules
	s.rulesMu.RUnlock()
	if rules == nil {
		if err := s.loadRules(ctx, q); err != nil {
			return Rule{}, false, err
		}
		s.rulesMu.RLock()
		rules = s.rules
		s.rulesMu.RUnlock()
	}
	r, ok := rules[code]
	return r, ok, nil
}

var (
	keyRE      = regexp.MustCompile(`^[a-z_]+:[A-Za-z0-9:_.-]{1,250}$`)
	currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)
	srcTypeRE  = regexp.MustCompile(`^[a-z_]{3,40}$`)
	amountRE   = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
)

func invalid(field, code string) error {
	e := errs.New(errs.Unprocessable, ErrInvalidPosting.Code, ErrInvalidPosting.Message)
	e.Details = []errs.Detail{{Field: field, Code: code}}
	return e
}

// Validate checks a posting against everything Go can know without the database (the database re-checks all of
// it): rule, environment, evidence, currency format, lines (≥ 2, positive, known classes, owners, rule lines,
// required lines, single owner per type), balance (L1), reversal and approval shape, idempotency key format.
func (s *Service) Validate(ctx context.Context, env string, p Posting) (Rule, error) {
	return s.validate(ctx, s.Pool, env, p)
}

func (s *Service) validate(ctx context.Context, q rowQuerier, env string, p Posting) (Rule, error) {
	r, ok, err := s.ruleQ(ctx, q, p.Rule)
	if err != nil {
		return Rule{}, err
	}
	if !ok {
		return Rule{}, invalid("rule", "UNKNOWN_RULE")
	}
	if env == EnvProduction && !ProductionPostingEnabled {
		return Rule{}, ErrProductionDisabled
	}
	switch p.EvidenceClass {
	case EvidenceSimulated:
		if env != EnvTest {
			return Rule{}, ErrEvidence
		}
	case EvidenceProviderReported:
		if env == EnvProduction {
			return Rule{}, ErrEvidence // production needs verified evidence
		}
	case EvidenceProviderVerified, EvidenceSettlementVerified, EvidenceManuallyReviewed:
	default:
		return Rule{}, ErrEvidence // REJECTED or unknown
	}
	switch p.SourceSystem {
	case SourceSynthetic:
		if env != EnvTest || p.EvidenceClass != EvidenceSimulated {
			return Rule{}, ErrEnvironment
		}
	case SourcePayments, SourceReconciliation, SourceStaff:
	default:
		return Rule{}, invalid("source_system", "INVALID_VALUE")
	}
	if !keyRE.MatchString(p.IdempotencyKey) {
		return Rule{}, invalid("idempotency_key", "INVALID_FORMAT")
	}
	if !currencyRE.MatchString(p.Currency) {
		return Rule{}, ErrCurrency
	}
	if !srcTypeRE.MatchString(p.SourceType) || !contains(r.SourceTypes, p.SourceType) {
		return Rule{}, invalid("source_type", "NOT_ALLOWED_BY_RULE")
	}
	if !uuidRE.MatchString(p.SourceID) {
		return Rule{}, invalid("source_id", "INVALID_FORMAT")
	}
	if p.OccurredAt.IsZero() || p.OccurredAt.After(s.now().Add(15*time.Minute)) {
		return Rule{}, invalid("occurred_at", "INVALID_VALUE")
	}
	if p.Actor.Type != "SYSTEM" && p.Actor.Type != "STAFF" || p.Actor.ID == "" {
		return Rule{}, invalid("actor", "REQUIRED")
	}
	if strings.TrimSpace(p.Description) == "" || len(p.Description) > 500 {
		return Rule{}, invalid("description", "REQUIRED")
	}
	if (p.Reverses != "") != r.MirrorOfReversed {
		return Rule{}, invalid("reverses", "REVERSAL_RULE_MISMATCH")
	}
	if r.RequiresApproval && p.AdjustmentID == "" {
		return Rule{}, invalid("adjustment_id", "APPROVAL_REQUIRED")
	}
	if (p.Reverses != "" || p.AdjustmentID != "") && len(strings.TrimSpace(p.Reason)) < 10 {
		return Rule{}, invalid("reason", "TOO_SHORT")
	}
	if len(p.Lines) < 2 {
		return Rule{}, invalid("lines", "AT_LEAST_TWO")
	}
	var dr, cr int64
	owners := map[string]map[string]bool{}
	present := map[string]bool{}
	for _, l := range p.Lines {
		spec, ok := Chart[l.Account.Class]
		if !ok {
			return Rule{}, invalid("lines", "UNKNOWN_ACCOUNT_CLASS")
		}
		if (spec.OwnerType == "PLATFORM") != (l.Account.OwnerID == "") || (l.Account.OwnerID != "" && !uuidRE.MatchString(l.Account.OwnerID)) {
			return Rule{}, invalid("lines", "INVALID_OWNER")
		}
		if l.Amount <= 0 {
			return Rule{}, invalid("lines", "AMOUNT_NOT_POSITIVE")
		}
		switch l.Direction {
		case Debit:
			if dr > (1<<62)-l.Amount {
				return Rule{}, invalid("lines", "AMOUNT_TOO_LARGE")
			}
			dr += l.Amount
		case Credit:
			if cr > (1<<62)-l.Amount {
				return Rule{}, invalid("lines", "AMOUNT_TOO_LARGE")
			}
			cr += l.Amount
		default:
			return Rule{}, invalid("lines", "INVALID_DIRECTION")
		}
		k := l.Direction + "|" + l.Account.Class
		if !r.MirrorOfReversed && !r.Lines[k] {
			return Rule{}, ErrRuleViolation
		}
		present[k] = true
		if spec.OwnerType != "PLATFORM" {
			if owners[spec.OwnerType] == nil {
				owners[spec.OwnerType] = map[string]bool{}
			}
			owners[spec.OwnerType][l.Account.OwnerID] = true
		}
	}
	if dr != cr {
		return Rule{}, ErrUnbalanced
	}
	for _, k := range r.Required {
		if !present[k] {
			return Rule{}, ErrRuleViolation
		}
	}
	if r.SingleOwnerPerType {
		for _, set := range owners {
			if len(set) > 1 {
				return Rule{}, ErrRuleViolation
			}
		}
	}
	return r, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// lineKey is the canonical multiset element used for idempotency equivalence.
func lineKey(code, dir string, amount int64) string {
	return code + "|" + dir + "|" + itoa(amount)
}

func sortedKeys(lines []Line) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, lineKey(l.Account.Code(), l.Direction, l.Amount))
	}
	sort.Strings(out)
	return out
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
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
