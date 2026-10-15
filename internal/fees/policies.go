package fees

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Fee policies (ADR-044 §3–§5). A policy is an immutable scope; its versions carry the terms. Terms are editable
// only in DRAFT; submission freezes them (terms_hash); approval is maker-checker with step-up; activation and
// suspension need step-up. Every lifecycle change of a version invalidates the ACTIVE quotes in its currency.

// Version statuses.
const (
	VDraft     = "DRAFT"
	VPending   = "PENDING_APPROVAL"
	VApproved  = "APPROVED"
	VRejected  = "REJECTED"
	VActive    = "ACTIVE"
	VSuspended = "SUSPENDED"
	VRetired   = "RETIRED"
)

var (
	codeRE   = regexp.MustCompile(`^[a-z][a-z0-9_.]{2,63}$`)
	methodRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)
	digitsRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})$`)
)

// NewPolicy creates a policy (its scope is immutable).
type NewPolicy struct {
	Code             string  `json:"code"`
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	Category         string  `json:"category"`
	TransactionType  string  `json:"transaction_type"`
	Currency         string  `json:"currency"`
	PaymentMethod    *string `json:"payment_method"`
	ProviderID       *string `json:"provider_id"`
	CampaignCategory *string `json:"campaign_category"`
	CampaignID       *string `json:"campaign_id"`
}

// Policy is a policy with its versions (staff view).
type Policy struct {
	ID               string    `json:"id"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Category         string    `json:"category"`
	TransactionType  string    `json:"transaction_type"`
	Currency         string    `json:"currency"`
	PaymentMethod    *string   `json:"payment_method"`
	ProviderID       *string   `json:"provider_id"`
	CampaignCategory *string   `json:"campaign_category"`
	CampaignID       *string   `json:"campaign_id"`
	Specificity      int       `json:"specificity"`
	FeeEnvironment   string    `json:"fee_environment"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	Versions         []Version `json:"versions"`
	// ActiveVersionID is the version that applies now (nil: none).
	ActiveVersionID *string `json:"active_version_id"`
}

// VersionInput creates or edits a DRAFT version. Amounts are strings of digits (MONEY.md §3.3).
type VersionInput struct {
	Model           string     `json:"model"`
	RateBps         *int64     `json:"rate_bps"`
	FixedMinor      *string    `json:"fixed_minor"`
	MinFeeMinor     *string    `json:"min_fee_minor"`
	MaxFeeMinor     *string    `json:"max_fee_minor"`
	Tiers           []TierDoc  `json:"tiers"`
	RoundingMode    string     `json:"rounding_mode"`
	Responsibility  string     `json:"responsibility"`
	DonorShareBps   *int64     `json:"donor_share_bps"`
	RefundTreatment *string    `json:"refund_treatment"`
	EffectiveFrom   time.Time  `json:"effective_from"`
	EffectiveTo     *time.Time `json:"effective_to"`
}

// Version is a policy version (staff view).
type Version struct {
	ID                string     `json:"id"`
	PolicyID          string     `json:"policy_id"`
	Category          string     `json:"category"`
	VersionNo         int        `json:"version_no"`
	Status            string     `json:"status"`
	Terms             TermsDoc   `json:"terms"`
	Responsibility    string     `json:"responsibility"`
	DonorShareBps     *int64     `json:"donor_share_bps"`
	RefundTreatment   *string    `json:"refund_treatment"`
	EffectiveFrom     time.Time  `json:"effective_from"`
	EffectiveTo       *time.Time `json:"effective_to"`
	AlgorithmVersion  string     `json:"algorithm_version"`
	TermsHash         *string    `json:"terms_hash"`
	FeeEnvironment    string     `json:"fee_environment"`
	CreatedBy         string     `json:"created_by"`
	SubmittedBy       *string    `json:"submitted_by"`
	SubmittedAt       *time.Time `json:"submitted_at"`
	ApprovedBy        *string    `json:"approved_by"`
	ApprovedAt        *time.Time `json:"approved_at"`
	ApprovalReason    *string    `json:"approval_reason"`
	RejectedBy        *string    `json:"rejected_by"`
	RejectedAt        *time.Time `json:"rejected_at"`
	RejectionReason   *string    `json:"rejection_reason"`
	ActivatedBy       *string    `json:"activated_by"`
	ActivatedAt       *time.Time `json:"activated_at"`
	SuspendedBy       *string    `json:"suspended_by"`
	SuspendedAt       *time.Time `json:"suspended_at"`
	SuspensionReason  *string    `json:"suspension_reason"`
	ReactivatedBy     *string    `json:"reactivated_by"`
	RetiredBy         *string    `json:"retired_by"`
	RetiredAt         *time.Time `json:"retired_at"`
	RetirementReason  *string    `json:"retirement_reason"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	internalTermsHash []byte
	policyCurrency    string
}

const versionCols = `v.id, v.policy_id, v.category, v.version_no, v.status, v.model, v.rate_bps, v.fixed_minor, v.min_fee_minor, v.max_fee_minor,
	v.tiers, v.rounding_mode, v.responsibility, v.donor_share_bps, v.refund_treatment, v.effective_from, v.effective_to, v.algorithm_version,
	v.terms_hash, v.fee_environment, v.created_by, v.submitted_by, v.submitted_at, v.approved_by, v.approved_at, v.approval_reason,
	v.rejected_by, v.rejected_at, v.rejection_reason, v.activated_by, v.activated_at, v.suspended_by, v.suspended_at, v.suspension_reason,
	v.reactivated_by, v.retired_by, v.retired_at, v.retirement_reason, v.created_at, v.updated_at, p.currency`

// versionDest returns the scan destinations of versionCols plus the raw term columns.
type rawTerms struct {
	model, rounding         string
	rate, fixed, minF, maxF *int64
	tiers                   []byte
}

func versionDest(v *Version, t *rawTerms) []any {
	return []any{&v.ID, &v.PolicyID, &v.Category, &v.VersionNo, &v.Status, &t.model, &t.rate, &t.fixed, &t.minF, &t.maxF, &t.tiers, &t.rounding,
		&v.Responsibility, &v.DonorShareBps, &v.RefundTreatment, &v.EffectiveFrom, &v.EffectiveTo, &v.AlgorithmVersion, &v.internalTermsHash,
		&v.FeeEnvironment, &v.CreatedBy, &v.SubmittedBy, &v.SubmittedAt, &v.ApprovedBy, &v.ApprovedAt, &v.ApprovalReason, &v.RejectedBy,
		&v.RejectedAt, &v.RejectionReason, &v.ActivatedBy, &v.ActivatedAt, &v.SuspendedBy, &v.SuspendedAt, &v.SuspensionReason,
		&v.ReactivatedBy, &v.RetiredBy, &v.RetiredAt, &v.RetirementReason, &v.CreatedAt, &v.UpdatedAt, &v.policyCurrency}
}

func (v *Version) finish(t rawTerms) error {
	terms := Terms{Model: t.model, RateBps: t.rate, Fixed: t.fixed, Min: t.minF, Max: t.maxF, Rounding: t.rounding}
	if len(t.tiers) > 0 {
		var docs []TierDoc
		if err := json.Unmarshal(t.tiers, &docs); err != nil {
			return err
		}
		var err error
		if terms.Tiers, err = TiersFromDocs(docs); err != nil {
			return err
		}
	}
	v.Terms = terms.Doc()
	if len(v.internalTermsHash) > 0 {
		h := hexs(v.internalTermsHash)
		v.TermsHash = &h
	}
	return nil
}

func scanVersion(r pgx.Row) (Version, error) {
	var v Version
	var t rawTerms
	err := r.Scan(versionDest(&v, &t)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	return v, v.finish(t)
}

func hexs(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

// input returns the calculation input of a version.
func (v Version) input() (*PolicyInput, error) {
	t, err := v.Terms.Terms()
	if err != nil {
		return nil, err
	}
	p := &PolicyInput{VersionID: v.ID, Terms: t, Responsibility: v.Responsibility}
	if v.DonorShareBps != nil {
		p.DonorShareBps = *v.DonorShareBps
	}
	if v.RefundTreatment != nil {
		p.RefundTreatment = *v.RefundTreatment
	}
	return p, nil
}

// canonical is the terms document hashed at submission (the approval binds to it).
func (v Version) canonical() ([]byte, error) {
	return json.Marshal(struct {
		PolicyID        string   `json:"policy_id"`
		VersionNo       int      `json:"version_no"`
		Category        string   `json:"category"`
		Terms           TermsDoc `json:"terms"`
		Responsibility  string   `json:"responsibility"`
		DonorShareBps   *int64   `json:"donor_share_bps"`
		RefundTreatment *string  `json:"refund_treatment"`
		EffectiveFrom   string   `json:"effective_from"`
		EffectiveTo     *string  `json:"effective_to"`
		Algorithm       string   `json:"algorithm_version"`
	}{v.PolicyID, v.VersionNo, v.Category, v.Terms, v.Responsibility, v.DonorShareBps, v.RefundTreatment,
		v.EffectiveFrom.UTC().Format(time.RFC3339Nano), timeStr(v.EffectiveTo), v.AlgorithmVersion})
}

func timeStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

// ---- policies ---------------------------------------------------------------------------------------------------

func trimP(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

func detailsError(det []errs.Detail) error {
	e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
	e.Details = det
	return e
}

// CreatePolicy creates a fee policy scope (fee.config.request).
func (s *Service) CreatePolicy(ctx context.Context, staffID string, in NewPolicy) (Policy, error) {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	in.PaymentMethod, in.ProviderID, in.CampaignCategory, in.CampaignID = trimP(in.PaymentMethod), trimP(in.ProviderID), trimP(in.CampaignCategory), trimP(in.CampaignID)
	var det []errs.Detail
	if !codeRE.MatchString(in.Code) {
		det = append(det, errs.Detail{Field: "code", Code: "INVALID_FORMAT"})
	}
	if n := utf8.RuneCountInString(in.Name); n < 3 || n > 120 {
		det = append(det, errs.Detail{Field: "name", Code: "INVALID_LENGTH"})
	}
	if utf8.RuneCountInString(in.Description) > 2000 {
		det = append(det, errs.Detail{Field: "description", Code: "INVALID_LENGTH"})
	}
	want := map[string]string{CatPlatform: TxDonation, CatProcessing: TxDonation, CatPayout: TxPayout, CatRefund: TxRefund, CatChargeback: TxChargeback}
	switch in.Category {
	case CatFX, CatOther:
		return Policy{}, ErrCategoryDisabled
	case CatPlatform, CatProcessing, CatPayout, CatRefund, CatChargeback:
		if in.TransactionType == "" {
			in.TransactionType = want[in.Category]
		}
		if in.TransactionType != want[in.Category] {
			det = append(det, errs.Detail{Field: "transaction_type", Code: "INVALID_FOR_CATEGORY"})
		}
	default:
		det = append(det, errs.Detail{Field: "category", Code: "INVALID_VALUE"})
	}
	if in.PaymentMethod != nil && !methodRE.MatchString(*in.PaymentMethod) {
		det = append(det, errs.Detail{Field: "payment_method", Code: "INVALID_FORMAT"})
	}
	if in.ProviderID != nil && !ids.Valid(*in.ProviderID) {
		det = append(det, errs.Detail{Field: "provider_id", Code: "INVALID_FORMAT"})
	}
	if in.CampaignID != nil && !ids.Valid(*in.CampaignID) {
		det = append(det, errs.Detail{Field: "campaign_id", Code: "INVALID_FORMAT"})
	}
	if in.CampaignCategory != nil && !codeRE.MatchString(*in.CampaignCategory) {
		det = append(det, errs.Detail{Field: "campaign_category", Code: "INVALID_FORMAT"})
	}
	if len(det) > 0 {
		return Policy{}, detailsError(det)
	}
	if err := s.currencyUsable(ctx, in.Currency); err != nil {
		return Policy{}, err
	}
	id := ids.New()
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app.fee_policies (id, code, name, description, category, transaction_type, currency,
			payment_method, provider_id, campaign_category, campaign_id, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			id, in.Code, in.Name, in.Description, in.Category, in.TransactionType, in.Currency, in.PaymentMethod, in.ProviderID,
			in.CampaignCategory, in.CampaignID, staffID); err != nil {
			if isUnique(err, "uq_fee_policies_code") {
				return errs.New(errs.Conflict, "FEE_POLICY_CODE_TAKEN", "A fee policy with this code already exists.")
			}
			return mapDB(err)
		}
		if err := event(ctx, tx, "fee_policy", id, "POLICY_CREATED", staff(staffID), map[string]any{"code": in.Code, "category": in.Category,
			"currency": in.Currency}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.policy.created", TargetType: "fee_policy", TargetID: id,
			Metadata: map[string]any{"category": in.Category, "currency": in.Currency}})
	})
	if err != nil {
		return Policy{}, err
	}
	return s.Policy(ctx, id)
}

func isUnique(err error, constraint string) bool {
	var pg interface{ SQLState() string }
	if !errors.As(err, &pg) || pg.SQLState() != "23505" {
		return false
	}
	return strings.Contains(err.Error(), constraint)
}

const policyCols = `id, code, name, description, category, transaction_type, currency, payment_method, provider_id::text, campaign_category,
	campaign_id::text, specificity, fee_environment, created_by, created_at`

func scanPolicy(r pgx.Row) (Policy, error) {
	var p Policy
	err := r.Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Category, &p.TransactionType, &p.Currency, &p.PaymentMethod, &p.ProviderID,
		&p.CampaignCategory, &p.CampaignID, &p.Specificity, &p.FeeEnvironment, &p.CreatedBy, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Policy returns a policy with all its versions and the version that applies now.
func (s *Service) Policy(ctx context.Context, id string) (Policy, error) {
	if !ids.Valid(id) {
		return Policy{}, ErrNotFound
	}
	p, err := scanPolicy(s.Pool.QueryRow(ctx, `SELECT `+policyCols+` FROM app.fee_policies WHERE id = $1`, id))
	if err != nil {
		return p, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+versionCols+` FROM app.fee_policy_versions v JOIN app.fee_policies p ON p.id = v.policy_id
		WHERE v.policy_id = $1 ORDER BY v.version_no DESC`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Versions = []Version{}
	for rows.Next() {
		var v Version
		var t rawTerms
		if err := rows.Scan(versionDest(&v, &t)...); err != nil {
			return p, err
		}
		if err := v.finish(t); err != nil {
			return p, err
		}
		p.Versions = append(p.Versions, v)
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	now := s.now()
	var best *Version
	for i := range p.Versions {
		v := &p.Versions[i]
		if v.Status == VActive && !v.EffectiveFrom.After(now) && (v.EffectiveTo == nil || v.EffectiveTo.After(now)) &&
			(best == nil || v.EffectiveFrom.After(best.EffectiveFrom)) {
			best = v
		}
	}
	if best != nil {
		p.ActiveVersionID = &best.ID
	}
	return p, nil
}

// PolicyFilter filters the staff policy list.
type PolicyFilter struct {
	Category, Currency, Status string
}

// Policies lists policies (newest first) with their versions.
func (s *Service) Policies(ctx context.Context, f PolicyFilter) ([]Policy, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM app.fee_policies p WHERE ($1 = '' OR category = $1) AND ($2 = '' OR currency = $2)
		AND ($3 = '' OR EXISTS (SELECT 1 FROM app.fee_policy_versions v WHERE v.policy_id = p.id AND v.status = $3))
		ORDER BY created_at DESC, id DESC LIMIT 200`, f.Category, f.Currency, f.Status)
	if err != nil {
		return nil, err
	}
	idsList, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make([]Policy, 0, len(idsList))
	for _, id := range idsList {
		p, err := s.Policy(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ---- versions ---------------------------------------------------------------------------------------------------

func (in VersionInput) terms() (Terms, []errs.Detail) {
	var det []errs.Detail
	parse := func(field string, v *string) *int64 {
		if v == nil {
			return nil
		}
		if !digitsRE.MatchString(*v) {
			det = append(det, errs.Detail{Field: field, Code: "INVALID_FORMAT"})
			return nil
		}
		n, _ := strconv.ParseInt(*v, 10, 64)
		return &n
	}
	t := Terms{Model: in.Model, RateBps: in.RateBps, Rounding: in.RoundingMode, Fixed: parse("fixed_minor", in.FixedMinor),
		Min: parse("min_fee_minor", in.MinFeeMinor), Max: parse("max_fee_minor", in.MaxFeeMinor)}
	for i, td := range in.Tiers {
		if (td.UpToMinor != nil && !digitsRE.MatchString(*td.UpToMinor)) || !digitsRE.MatchString(td.FixedMinor) {
			det = append(det, errs.Detail{Field: "tiers[" + strconv.Itoa(i) + "]", Code: "INVALID_FORMAT"})
		}
	}
	if len(det) == 0 && len(in.Tiers) > 0 {
		tiers, err := TiersFromDocs(in.Tiers)
		if err != nil {
			det = append(det, errs.Detail{Field: "tiers", Code: "INVALID_VALUE"})
		}
		t.Tiers = tiers
	}
	if len(det) == 0 {
		if err := t.Validate(); err != nil {
			det = append(det, errs.Detail{Field: "terms", Code: "INVALID_VALUE"})
		}
	}
	return t, det
}

// validateVersion checks a version's terms against its category (mirrors ck_fee_policy_versions_category).
func validateVersion(category string, in VersionInput) (Terms, error) {
	t, det := in.terms()
	add := func(f, c string) { det = append(det, errs.Detail{Field: f, Code: c}) }
	switch in.Responsibility {
	case PlatformAbsorbed, DonorCovered, OrganiserCovered, Shared:
	default:
		add("responsibility", "INVALID_VALUE")
	}
	if (in.Responsibility == Shared) != (in.DonorShareBps != nil) || (in.DonorShareBps != nil && (*in.DonorShareBps < 1 || *in.DonorShareBps > 9999)) {
		add("donor_share_bps", "INVALID_VALUE")
	}
	if in.RefundTreatment != nil {
		switch *in.RefundTreatment {
		case RefundProportional, RefundProportionalExceptFixed, RefundNonRefundable:
		default:
			add("refund_treatment", "INVALID_VALUE")
		}
	}
	switch category {
	case CatPlatform:
		if in.Model == ModelPassThrough {
			add("model", "INVALID_FOR_CATEGORY")
		}
		if in.Responsibility == PlatformAbsorbed {
			add("responsibility", "INVALID_FOR_CATEGORY") // waive the fee with ZERO_FEE instead
		}
		if in.RefundTreatment == nil {
			add("refund_treatment", "REQUIRED")
		}
	case CatProcessing:
		if in.Model != ModelPassThrough {
			add("model", "INVALID_FOR_CATEGORY")
		}
		if in.RefundTreatment != nil {
			add("refund_treatment", "NOT_ALLOWED")
		}
	default:
		if in.Model != ModelPassThrough {
			add("model", "INVALID_FOR_CATEGORY")
		}
		if in.RefundTreatment != nil {
			add("refund_treatment", "NOT_ALLOWED")
		}
		if in.Responsibility != OrganiserCovered && in.Responsibility != PlatformAbsorbed {
			add("responsibility", "INVALID_FOR_CATEGORY")
		}
	}
	if in.EffectiveFrom.IsZero() {
		add("effective_from", "REQUIRED")
	}
	if in.EffectiveTo != nil && !in.EffectiveTo.After(in.EffectiveFrom) {
		add("effective_to", "BEFORE_START")
	}
	if len(det) > 0 {
		return Terms{}, detailsError(det)
	}
	return t, nil
}

func tiersJSON(t Terms) ([]byte, error) {
	if len(t.Tiers) == 0 {
		return nil, nil
	}
	return json.Marshal(t.Doc().Tiers)
}

func toUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// CreateVersion adds a DRAFT version to a policy (fee.config.request). One draft or pending version per policy.
func (s *Service) CreateVersion(ctx context.Context, staffID, policyID string, in VersionInput) (Version, error) {
	if !ids.Valid(policyID) {
		return Version{}, ErrNotFound
	}
	p, err := scanPolicy(s.Pool.QueryRow(ctx, `SELECT `+policyCols+` FROM app.fee_policies WHERE id = $1`, policyID))
	if err != nil {
		return Version{}, err
	}
	t, err := validateVersion(p.Category, in)
	if err != nil {
		return Version{}, err
	}
	tiers, err := tiersJSON(t)
	if err != nil {
		return Version{}, err
	}
	id := ids.New()
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM app.fee_policies WHERE id = $1 FOR UPDATE`, policyID); err != nil {
			return err
		}
		var no int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version_no), 0) + 1 FROM app.fee_policy_versions WHERE policy_id = $1`, policyID).Scan(&no); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.fee_policy_versions (id, policy_id, category, version_no, status, model, rate_bps, fixed_minor,
			min_fee_minor, max_fee_minor, tiers, rounding_mode, responsibility, donor_share_bps, refund_treatment, effective_from, effective_to,
			algorithm_version, created_by) VALUES ($1, $2, $3, $4, 'DRAFT', $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			id, policyID, p.Category, no, t.Model, t.RateBps, t.Fixed, t.Min, t.Max, tiers, t.Rounding, in.Responsibility, in.DonorShareBps,
			in.RefundTreatment, in.EffectiveFrom.UTC(), toUTC(in.EffectiveTo), AlgorithmVersion, staffID); err != nil {
			return mapDB(err)
		}
		if err := event(ctx, tx, "fee_policy_version", id, "VERSION_DRAFTED", staff(staffID), map[string]any{"policy_id": policyID,
			"version_no": no}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.policy_version.drafted", TargetType: "fee_policy_version", TargetID: id})
	})
	if err != nil {
		return Version{}, err
	}
	return s.Version(ctx, id)
}

// UpdateDraft replaces the terms of a DRAFT version (its author only).
func (s *Service) UpdateDraft(ctx context.Context, staffID, versionID string, in VersionInput) (Version, error) {
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		v, err := s.lockVersion(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if v.Status != VDraft {
			return ErrPolicyState
		}
		if v.CreatedBy != staffID {
			return errs.New(errs.Forbidden, "FEE_DRAFT_NOT_YOURS", "Only the author of a draft can edit it.")
		}
		t, err := validateVersion(v.Category, in)
		if err != nil {
			return err
		}
		tiers, err := tiersJSON(t)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.fee_policy_versions SET model = $2, rate_bps = $3, fixed_minor = $4, min_fee_minor = $5,
			max_fee_minor = $6, tiers = $7, rounding_mode = $8, responsibility = $9, donor_share_bps = $10, refund_treatment = $11,
			effective_from = $12, effective_to = $13, version = version + 1 WHERE id = $1`, versionID, t.Model, t.RateBps, t.Fixed, t.Min, t.Max,
			tiers, t.Rounding, in.Responsibility, in.DonorShareBps, in.RefundTreatment, in.EffectiveFrom.UTC(), toUTC(in.EffectiveTo)); err != nil {
			return mapDB(err)
		}
		return event(ctx, tx, "fee_policy_version", versionID, "DRAFT_EDITED", staff(staffID), nil, now)
	})
	if err != nil {
		return Version{}, err
	}
	return s.Version(ctx, versionID)
}

// Version returns one version.
func (s *Service) Version(ctx context.Context, id string) (Version, error) {
	if !ids.Valid(id) {
		return Version{}, ErrNotFound
	}
	return scanVersion(s.Pool.QueryRow(ctx, `SELECT `+versionCols+` FROM app.fee_policy_versions v JOIN app.fee_policies p ON p.id = v.policy_id
		WHERE v.id = $1`, id))
}

func (s *Service) lockVersion(ctx context.Context, tx pgx.Tx, id string) (Version, error) {
	if !ids.Valid(id) {
		return Version{}, ErrNotFound
	}
	return scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM app.fee_policy_versions v JOIN app.fee_policies p ON p.id = v.policy_id
		WHERE v.id = $1 FOR UPDATE OF v`, id))
}

// Decision is a lifecycle decision's input.
type Decision struct {
	Reason string `json:"reason"`
	// TermsHash, when given, must equal the version's terms hash (the checker approves exactly what they saw).
	TermsHash *string `json:"terms_hash"`
}

func reason(r string) (string, error) {
	r = strings.TrimSpace(r)
	if n := utf8.RuneCountInString(r); n < 5 || n > 2000 {
		return "", validation("reason", "INVALID_LENGTH")
	}
	return r, nil
}

// Submit freezes a DRAFT (DRAFT → PENDING_APPROVAL): terms become immutable and are hashed. Never retroactive.
func (s *Service) Submit(ctx context.Context, staffID, versionID string) (Version, error) {
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		v, err := s.lockVersion(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if v.Status != VDraft {
			return ErrPolicyState
		}
		if v.EffectiveFrom.Before(now.Add(-time.Minute)) {
			return ErrRetroactive
		}
		doc, err := v.canonical()
		if err != nil {
			return err
		}
		h := sha256.Sum256(doc)
		if _, err := tx.Exec(ctx, `UPDATE app.fee_policy_versions SET status = 'PENDING_APPROVAL', submitted_by = $2, submitted_at = $3,
			terms_hash = $4, version = version + 1 WHERE id = $1`, versionID, staffID, now, h[:]); err != nil {
			return mapDB(err)
		}
		if err := event(ctx, tx, "fee_policy_version", versionID, "VERSION_SUBMITTED", staff(staffID), map[string]any{"terms_hash": hexs(h[:])}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.policy_version.submitted", TargetType: "fee_policy_version", TargetID: versionID})
	})
	if err != nil {
		return Version{}, err
	}
	return s.Version(ctx, versionID)
}

// Approve is the checker step (fee.config.approve; fresh step-up). The checker is never the maker or linked to them.
func (s *Service) Approve(ctx context.Context, staffID string, stepUpAt time.Time, versionID string, d Decision) (Version, error) {
	return s.decide(ctx, staffID, stepUpAt, versionID, d, true)
}

// Reject is the checker's refusal (PENDING_APPROVAL → REJECTED; terminal).
func (s *Service) Reject(ctx context.Context, staffID string, stepUpAt time.Time, versionID string, d Decision) (Version, error) {
	return s.decide(ctx, staffID, stepUpAt, versionID, d, false)
}

func (s *Service) decide(ctx context.Context, staffID string, stepUpAt time.Time, versionID string, d Decision, approve bool) (Version, error) {
	r, err := reason(d.Reason)
	if err != nil {
		return Version{}, err
	}
	if stepUpAt.IsZero() {
		return Version{}, ErrStepUp
	}
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		v, err := s.lockVersion(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if v.Status != VPending {
			return ErrPolicyState
		}
		if d.TermsHash != nil && (v.TermsHash == nil || *d.TermsHash != *v.TermsHash) {
			return errs.New(errs.Conflict, "FEE_TERMS_CHANGED", "The terms you reviewed are not the terms of this version.")
		}
		sub := ""
		if v.SubmittedBy != nil {
			sub = *v.SubmittedBy
		}
		if self, err := s.linked(ctx, staffID, v.CreatedBy, sub); err != nil {
			return err
		} else if self {
			return ErrSelfApproval
		}
		if approve {
			_, err = tx.Exec(ctx, `UPDATE app.fee_policy_versions SET status = 'APPROVED', approved_by = $2, approved_at = $3,
				approved_step_up_at = $4, approval_reason = $5, version = version + 1 WHERE id = $1`, versionID, staffID, now, minTime(stepUpAt.UTC(), now), r)
		} else {
			_, err = tx.Exec(ctx, `UPDATE app.fee_policy_versions SET status = 'REJECTED', rejected_by = $2, rejected_at = $3,
				rejection_reason = $4, version = version + 1 WHERE id = $1`, versionID, staffID, now, r)
		}
		if err != nil {
			return mapDB(err)
		}
		ev, action := "VERSION_APPROVED", "fee.policy_version.approved"
		if !approve {
			ev, action = "VERSION_REJECTED", "fee.policy_version.rejected"
		}
		if err := event(ctx, tx, "fee_policy_version", versionID, ev, staff(staffID), map[string]any{"reason": r}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "fee_policy_version", TargetID: versionID, Reason: r})
	})
	if err != nil {
		return Version{}, err
	}
	return s.Version(ctx, versionID)
}

func minTime(a, b time.Time) time.Time {
	if a.After(b) {
		return b
	}
	return a
}

// Activate puts an APPROVED version in force from its effective_from (fresh step-up). It invalidates the ACTIVE
// quotes in the policy's currency (they were computed without it).
func (s *Service) Activate(ctx context.Context, staffID string, stepUpAt time.Time, versionID string) (Version, error) {
	return s.lifecycle(ctx, staffID, stepUpAt, versionID, "activate", "")
}

// Suspend takes an ACTIVE version out of force (fresh step-up, reason).
func (s *Service) Suspend(ctx context.Context, staffID string, stepUpAt time.Time, versionID, why string) (Version, error) {
	return s.lifecycle(ctx, staffID, stepUpAt, versionID, "suspend", why)
}

// Reactivate returns a SUSPENDED version to force (fee.config.approve; not by its suspender or anyone linked).
func (s *Service) Reactivate(ctx context.Context, staffID string, stepUpAt time.Time, versionID, why string) (Version, error) {
	return s.lifecycle(ctx, staffID, stepUpAt, versionID, "reactivate", why)
}

// Retire ends a version for good (DRAFT, APPROVED, ACTIVE or SUSPENDED → RETIRED).
func (s *Service) Retire(ctx context.Context, staffID string, stepUpAt time.Time, versionID, why string) (Version, error) {
	return s.lifecycle(ctx, staffID, stepUpAt, versionID, "retire", why)
}

var pastTense = map[string]string{"activate": "ACTIVATED", "suspend": "SUSPENDED", "reactivate": "REACTIVATED", "retire": "RETIRED"}

func (s *Service) lifecycle(ctx context.Context, staffID string, stepUpAt time.Time, versionID, action, why string) (Version, error) {
	if stepUpAt.IsZero() {
		return Version{}, ErrStepUp
	}
	var r string
	if action != "activate" {
		var err error
		if r, err = reason(why); err != nil {
			return Version{}, err
		}
	}
	now := s.now()
	cur, err := s.Version(ctx, versionID)
	if err != nil {
		return Version{}, err
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockCurrency(ctx, tx, cur.policyCurrency, true); err != nil {
			return err
		}
		v, err := s.lockVersion(ctx, tx, versionID)
		if err != nil {
			return err
		}
		var q string
		args := []any{versionID, staffID, now}
		switch action {
		case "activate":
			if v.Status != VApproved {
				return ErrPolicyState
			}
			q = `UPDATE app.fee_policy_versions SET status = 'ACTIVE', activated_by = $2, activated_at = $3, activated_step_up_at = $4,
				version = version + 1 WHERE id = $1`
			args = append(args, minTime(stepUpAt.UTC(), now))
		case "suspend":
			if v.Status != VActive {
				return ErrPolicyState
			}
			q = `UPDATE app.fee_policy_versions SET status = 'SUSPENDED', suspended_by = $2, suspended_at = $3, suspension_reason = $4,
				reactivated_by = NULL, reactivated_at = NULL, version = version + 1 WHERE id = $1`
			args = append(args, r)
		case "reactivate":
			if v.Status != VSuspended {
				return ErrPolicyState
			}
			sus := ""
			if v.SuspendedBy != nil {
				sus = *v.SuspendedBy
			}
			if self, err := s.linked(ctx, staffID, sus); err != nil {
				return err
			} else if self {
				return ErrSelfApproval
			}
			q = `UPDATE app.fee_policy_versions SET status = 'ACTIVE', reactivated_by = $2, reactivated_at = $3, version = version + 1 WHERE id = $1`
		case "retire":
			switch v.Status {
			case VDraft, VApproved, VActive, VSuspended:
			default:
				return ErrPolicyState
			}
			q = `UPDATE app.fee_policy_versions SET status = 'RETIRED', retired_by = $2, retired_at = $3, retirement_reason = $4,
				version = version + 1 WHERE id = $1`
			args = append(args, r)
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return mapDB(err)
		}
		n, err := invalidateQuotes(ctx, tx, v.policyCurrency, "fee policy version "+versionID+" "+strings.ToLower(pastTense[action]), now)
		if err != nil {
			return err
		}
		if err := event(ctx, tx, "fee_policy_version", versionID, "VERSION_"+pastTense[action], staff(staffID),
			map[string]any{"reason": r, "quotes_invalidated": n}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.policy_version." + strings.ToLower(pastTense[action]), TargetType: "fee_policy_version",
			TargetID: versionID, Reason: r, Metadata: map[string]any{"quotes_invalidated": n}})
	})
	if err != nil {
		return Version{}, err
	}
	return s.Version(ctx, versionID)
}

// invalidateQuotes marks the ACTIVE quotes in a currency INVALIDATED (a policy they depend on may have changed).
func invalidateQuotes(ctx context.Context, tx pgx.Tx, currency, why string, now time.Time) (int64, error) {
	tag, err := tx.Exec(ctx, `UPDATE app.fee_quotes SET status = 'INVALIDATED', invalidated_at = $2, invalidation_reason = $3
		WHERE status = 'ACTIVE' AND currency = $1`, currency, now, why)
	return tag.RowsAffected(), err
}

// ---- selection --------------------------------------------------------------------------------------------------

// Scope is the transaction a policy is selected for.
type Scope struct {
	Category, TransactionType, Currency, PaymentMethod, ProviderID, CampaignCategory, CampaignID string
	At                                                                                           time.Time
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Resolve selects the policy version that applies to a scope at a time (ADR-044 §4): ACTIVE, in its window, the
// most specific scope, and within a policy the latest effective start. nil: no policy applies.
func (s *Service) Resolve(ctx context.Context, q querier, sc Scope) (*Version, error) {
	if q == nil {
		q = s.Pool
	}
	rows, err := q.Query(ctx, `SELECT `+versionCols+`, p.specificity FROM app.fee_policy_versions v JOIN app.fee_policies p ON p.id = v.policy_id
		WHERE v.status = 'ACTIVE' AND p.category = $1 AND p.transaction_type = $2 AND p.currency = $3
		  AND (p.payment_method IS NULL OR p.payment_method = $4) AND (p.provider_id IS NULL OR p.provider_id::text = $5)
		  AND (p.campaign_category IS NULL OR p.campaign_category = $6) AND (p.campaign_id IS NULL OR p.campaign_id::text = $7)
		  AND v.effective_from <= $8 AND (v.effective_to IS NULL OR v.effective_to > $8)
		ORDER BY p.specificity DESC, v.effective_from DESC, v.id LIMIT 4`,
		sc.Category, sc.TransactionType, sc.Currency, sc.PaymentMethod, sc.ProviderID, sc.CampaignCategory, sc.CampaignID, sc.At.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type cand struct {
		v    Version
		spec int
	}
	var cands []cand
	for rows.Next() {
		var c cand
		var t rawTerms
		if err := rows.Scan(append(versionDest(&c.v, &t), &c.spec)...); err != nil {
			return nil, err
		}
		if err := c.v.finish(t); err != nil {
			return nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}
	// equal specificity across two different policies cannot happen (unique scope_key); refuse it if it ever does
	for _, c := range cands[1:] {
		if c.spec == cands[0].spec && c.v.PolicyID != cands[0].v.PolicyID {
			return nil, ErrPolicyAmbiguous
		}
	}
	return &cands[0].v, nil
}

// ActivePolicy is the summary of a policy version in force (all TEST; rates are synthetic configuration).
type ActivePolicy struct {
	VersionID       string    `json:"version_id"`
	PolicyID        string    `json:"policy_id"`
	Name            string    `json:"name"`
	Category        string    `json:"category"`
	TransactionType string    `json:"transaction_type"`
	Currency        string    `json:"currency"`
	Scope           string    `json:"scope"`
	Terms           TermsDoc  `json:"terms"`
	Responsibility  string    `json:"responsibility"`
	DonorShareBps   *int64    `json:"donor_share_bps"`
	RefundTreatment *string   `json:"refund_treatment"`
	EffectiveFrom   time.Time `json:"effective_from"`
	FeeEnvironment  string    `json:"fee_environment"`
	Label           string    `json:"label"`
}

// TestLabel marks every fee figure shown to users or staff in Stage 12.
const TestLabel = "TEST — synthetic configuration, not FundZim's commercial rates"

// ActivePolicies lists the versions in force now that are not scoped to one campaign.
func (s *Service) ActivePolicies(ctx context.Context, currency, txType string) ([]ActivePolicy, error) {
	now := s.now()
	rows, err := s.Pool.Query(ctx, `SELECT v.id FROM app.fee_policy_versions v JOIN app.fee_policies p ON p.id = v.policy_id
		WHERE v.status = 'ACTIVE' AND v.effective_from <= $1 AND (v.effective_to IS NULL OR v.effective_to > $1)
		  AND p.campaign_id IS NULL AND ($2 = '' OR p.currency = $2) AND ($3 = '' OR p.transaction_type = $3)
		ORDER BY p.category, p.currency, p.specificity, v.effective_from DESC`, now, currency, txType)
	if err != nil {
		return nil, err
	}
	vids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []ActivePolicy{}
	seen := map[string]bool{}
	for _, id := range vids {
		v, err := s.Version(ctx, id)
		if err != nil {
			return nil, err
		}
		if seen[v.PolicyID] { // within a policy only the latest start applies
			continue
		}
		seen[v.PolicyID] = true
		p, err := scanPolicy(s.Pool.QueryRow(ctx, `SELECT `+policyCols+` FROM app.fee_policies WHERE id = $1`, v.PolicyID))
		if err != nil {
			return nil, err
		}
		out = append(out, ActivePolicy{VersionID: v.ID, PolicyID: p.ID, Name: p.Name, Category: p.Category, TransactionType: p.TransactionType,
			Currency: p.Currency, Scope: scopeLabel(p), Terms: v.Terms, Responsibility: v.Responsibility, DonorShareBps: v.DonorShareBps,
			RefundTreatment: v.RefundTreatment, EffectiveFrom: v.EffectiveFrom, FeeEnvironment: v.FeeEnvironment, Label: TestLabel})
	}
	return out, nil
}

func scopeLabel(p Policy) string {
	var parts []string
	if p.PaymentMethod != nil {
		parts = append(parts, "method "+*p.PaymentMethod)
	}
	if p.ProviderID != nil {
		parts = append(parts, "provider "+*p.ProviderID)
	}
	if p.CampaignCategory != nil {
		parts = append(parts, "category "+*p.CampaignCategory)
	}
	if p.CampaignID != nil {
		parts = append(parts, "campaign "+*p.CampaignID)
	}
	if len(parts) == 0 {
		return "platform-wide"
	}
	return strings.Join(parts, ", ")
}
