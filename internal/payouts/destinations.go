// Package payouts will own payout requests and processing (Stage 11). Stage 5 builds ONLY payout destination
// records and their verification (Stage 2 draft 0016 subset; ADR-034 §4). Nothing in this package can move
// money: there are no payout requests, provider calls or ledger postings, and every destination reports
// eligible_for_payout = false.
//
// Verification outcomes are separate and evidence-based (brief §11–12): FORMAT (syntax only), OWNERSHIP
// (bank letter / statement reviewed by staff, or a provider confirmation — none is integrated, so provider
// lookups end in PROVIDER_CONFIRMATION_REQUIRED) and COMPLIANCE (staff approval). The development mock
// provider is marked non_production and can never confirm ownership (DB CHECK).
package payouts

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nyaruka/phonenumbers"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/beneficiaries"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// CoolingOff is the period after creation or any change during which a destination cannot be used for a
// payout (EC-06). INTERNAL_RISK starting point; moves to the limits registry with payouts (Stage 11).
const CoolingOff = 72 * time.Hour

// Rails → category and display name. Target categories only: no provider is assumed to support payouts to
// all of them (PROVIDER_CONFIRMATION_REQUIRED, docs/payments/provider-questions.md).
var rails = map[string]struct{ category, name string }{
	"ECOCASH": {"MOBILE_MONEY_WALLET", "EcoCash"}, "ONEMONEY": {"MOBILE_MONEY_WALLET", "OneMoney"},
	"INNBUCKS": {"MOBILE_MONEY_WALLET", "InnBucks"}, "OMARI": {"MOBILE_MONEY_WALLET", "O'Mari"},
	"ZIMSWITCH": {"BANK_ACCOUNT", "ZimSwitch bank transfer"}, "BANK_TRANSFER": {"BANK_ACCOUNT", "Bank transfer"},
}

var (
	ErrNotFound   = errs.New(errs.NotFound, "DESTINATION_NOT_FOUND", "No such payout destination.")
	errNotAllowed = errs.New(errs.Conflict, "ACTION_NOT_ALLOWED", "This action is not allowed in the destination's current status.")
	errSelf       = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot verify a destination you own, created or changed, or one of your organisation.")
	errAssigned   = errs.New(errs.Conflict, "NOT_ASSIGNED", "Assign the destination to yourself before deciding it.")
	bankAccountRe = regexp.MustCompile(`^[0-9]{6,20}$`)
	bankCodeRe    = regexp.MustCompile(`^[A-Z0-9]{2,11}$`)
)

// Service is the payouts module (destinations only; app pool).
type Service struct {
	Pool   *pgxpool.Pool
	KYC    *kyc.Service
	Benefs *beneficiaries.Service
	Orgs   *organisations.Service
	AEAD   *crypto.AEAD  // app-pool field key (C3)
	Keyed  *crypto.Keyed // app-pool blind-index key
	Clock  clock.Clock
	Logger *slog.Logger
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }
func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Input creates or changes a destination.
type Input struct {
	OwnerOrganisationID *string `json:"owner_organisation_id"`
	Payee               *struct {
		Type          string  `json:"type"`
		BeneficiaryID *string `json:"beneficiary_id"`
	} `json:"payee"`
	Rail              string  `json:"rail"`
	Currency          string  `json:"currency"`
	HolderName        *string `json:"holder_name"`
	AccountIdentifier *string `json:"account_identifier"`
	BankCode          *string `json:"bank_code"`
}

// Destination is the owner's (and reviewer's) view; the account identifier is masked.
type Destination struct {
	ID                              string     `json:"id"`
	Owner                           Owner      `json:"owner"`
	Payee                           Payee      `json:"payee"`
	Category                        string     `json:"category"`
	Rail                            string     `json:"rail"`
	ProviderName                    string     `json:"provider_name"`
	Currency                        string     `json:"currency"`
	HolderName                      string     `json:"holder_name"`
	MaskedIdentifier                string     `json:"masked_identifier"`
	Status                          string     `json:"status"`
	Checks                          Checks     `json:"checks"`
	VerificationMethod              *string    `json:"verification_method"`
	CoolingOffUntil                 time.Time  `json:"cooling_off_until"`
	LastReviewedAt                  *time.Time `json:"last_reviewed_at"`
	ExpiresAt                       *time.Time `json:"expires_at"`
	EligibleForPayout               bool       `json:"eligible_for_payout"`
	CreatedAt                       time.Time  `json:"created_at"`
	UpdatedAt                       time.Time  `json:"updated_at"`
	Version                         int        `json:"version"`
	ownerUser, ownerOrg, assignedTo *string
	createdBy, lastChangedBy        string
	detailsVersion                  int
}

// Owner / Payee / Checks.
type Owner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type Payee struct {
	Type          string  `json:"type"`
	BeneficiaryID *string `json:"beneficiary_id"`
}
type Checks struct {
	FormatValidated bool   `json:"format_validated"`
	Ownership       string `json:"ownership"`
	Compliance      string `json:"compliance"`
}

const selectD = `SELECT id, owner_user_id, owner_organisation_id, created_by, payee_type, beneficiary_id, category, rail, currency, holder_name,
	masked_suffix, status, ownership_status, compliance_status, verification_method, cooling_off_until, verified_at, expires_at, assigned_to,
	last_changed_by, details_version, version, created_at, updated_at FROM app.payout_destinations`

func scanD(row pgx.Row) (Destination, error) {
	var d Destination
	var masked string
	err := row.Scan(&d.ID, &d.ownerUser, &d.ownerOrg, &d.createdBy, &d.Payee.Type, &d.Payee.BeneficiaryID, &d.Category, &d.Rail, &d.Currency,
		&d.HolderName, &masked, &d.Status, &d.Checks.Ownership, &d.Checks.Compliance, &d.VerificationMethod, &d.CoolingOffUntil, &d.LastReviewedAt,
		&d.ExpiresAt, &d.assignedTo, &d.lastChangedBy, &d.detailsVersion, &d.Version, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	d.MaskedIdentifier = "••••" + masked
	d.ProviderName = rails[d.Rail].name
	d.Checks.FormatValidated = true // a destination is only stored after its format check passed (FORMAT check row)
	d.EligibleForPayout = false     // no payout processing exists in Stage 5
	if d.ownerUser != nil {
		d.Owner = Owner{"USER", *d.ownerUser}
	} else if d.ownerOrg != nil {
		d.Owner = Owner{"ORGANISATION", *d.ownerOrg}
	}
	return d, err
}

func (s *Service) load(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, lock bool) (Destination, error) {
	if !ids.Valid(id) {
		return Destination{}, ErrNotFound
	}
	sql := selectD + ` WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanD(q.QueryRow(ctx, sql, id))
}

func (s *Service) canEdit(ctx context.Context, d Destination, userID string) (bool, error) {
	if d.ownerUser != nil {
		return *d.ownerUser == userID, nil
	}
	return s.Orgs.IsAdmin(ctx, *d.ownerOrg, userID)
}

// normalise validates the identifier format for the rail and returns (canonical, masked suffix). FORMAT
// validity says nothing about who owns the account.
func normalise(rail, ident, bankCode string) (string, string, []errs.Detail) {
	ident = strings.TrimSpace(ident)
	switch rails[rail].category {
	case "MOBILE_MONEY_WALLET":
		n, err := phonenumbers.Parse(ident, "ZW")
		if err != nil || !phonenumbers.IsValidNumberForRegion(n, "ZW") {
			return "", "", []errs.Detail{{Field: "account_identifier", Code: "INVALID_ACCOUNT_FORMAT"}}
		}
		if t := phonenumbers.GetNumberType(n); t != phonenumbers.MOBILE && t != phonenumbers.FIXED_LINE_OR_MOBILE {
			return "", "", []errs.Detail{{Field: "account_identifier", Code: "INVALID_ACCOUNT_FORMAT"}}
		}
		e := phonenumbers.Format(n, phonenumbers.E164)
		return e, e[len(e)-3:], nil
	case "BANK_ACCOUNT":
		v := strings.NewReplacer(" ", "", "-", "").Replace(ident)
		var det []errs.Detail
		if !bankAccountRe.MatchString(v) {
			det = append(det, errs.Detail{Field: "account_identifier", Code: "INVALID_ACCOUNT_FORMAT"})
		}
		if !bankCodeRe.MatchString(strings.ToUpper(strings.TrimSpace(bankCode))) {
			det = append(det, errs.Detail{Field: "bank_code", Code: "INVALID_FORMAT"})
		}
		if len(det) > 0 {
			return "", "", det
		}
		return v, v[len(v)-4:], nil
	}
	return "", "", []errs.Detail{{Field: "rail", Code: "INVALID_VALUE"}}
}

func (s *Service) event(ctx context.Context, tx pgx.Tx, id string, version int, typ, from, to, reason, actorID, actorType string) error {
	var aid any
	if actorID != "" {
		aid = actorID
	} else {
		actorType = "SYSTEM"
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.payout_destination_events (id, destination_id, destination_version, event_type, from_status, to_status,
		actor_type, actor_id, reason_code, occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		ids.New(), id, version, typ, nullable(from), to, actorType, aid, nullable(reason), s.now())
	return err
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Service) emit(ctx context.Context, tx pgx.Tx, typ string, d Destination, extra map[string]any) error {
	payload := map[string]any{"destination_id": d.ID, "owner_type": d.Owner.Type, "owner_id": d.Owner.ID}
	for k, v := range extra {
		payload[k] = v
	}
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "payout_destination", AggregateID: d.ID, EventType: typ, Payload: payload,
		CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}

func (s *Service) formatCheck(ctx context.Context, tx pgx.Tx, id string, detailsVersion int) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.payout_destination_checks (id, destination_id, details_version, check_kind, method, result)
		VALUES ($1, $2, $3, 'FORMAT', 'FORMAT_RULES', 'PASS')`, ids.New(), id, detailsVersion)
	return err
}

// Create records a new destination (UNVERIFIED, cooling-off running).
func (s *Service) Create(ctx context.Context, actorID string, in Input) (string, error) {
	r, ok := rails[in.Rail]
	var det []errs.Detail
	if !ok {
		return "", httpx.Validation(errs.Detail{Field: "rail", Code: "INVALID_VALUE"})
	}
	if in.Currency != "USD" && in.Currency != "ZWG" {
		det = append(det, errs.Detail{Field: "currency", Code: "INVALID_VALUE"})
	}
	holder := ""
	if in.HolderName != nil {
		holder = strings.Join(strings.Fields(*in.HolderName), " ")
	}
	if n := utf8.RuneCountInString(holder); n < 2 || n > 140 {
		det = append(det, errs.Detail{Field: "holder_name", Code: "INVALID_LENGTH"})
	}
	bank := ""
	if in.BankCode != nil {
		bank = strings.ToUpper(strings.TrimSpace(*in.BankCode))
	}
	ident := ""
	if in.AccountIdentifier != nil {
		ident = *in.AccountIdentifier
	}
	canonical, masked, fdet := normalise(in.Rail, ident, bank)
	det = append(det, fdet...)
	var ownerUser, ownerOrg any
	if in.OwnerOrganisationID != nil {
		admin, err := s.Orgs.IsAdmin(ctx, *in.OwnerOrganisationID, actorID)
		if err != nil {
			return "", err
		}
		if !admin {
			return "", errs.New(errs.NotFound, "ORGANISATION_NOT_FOUND", "No such organisation.")
		}
		ownerOrg = *in.OwnerOrganisationID
	} else {
		ownerUser = actorID
	}
	payeeType, var0 := "OWNER", any(nil)
	if in.Payee != nil && in.Payee.Type == "BENEFICIARY" {
		payeeType = "BENEFICIARY"
		if in.Payee.BeneficiaryID == nil {
			det = append(det, errs.Detail{Field: "payee.beneficiary_id", Code: "MISSING_FIELD"})
		} else {
			b, err := s.Benefs.Get(ctx, actorID, *in.Payee.BeneficiaryID)
			if err != nil || (b.Owner.Type == "USER") != (ownerUser != nil) || (ownerOrg != nil && b.Owner.ID != ownerOrg) {
				det = append(det, errs.Detail{Field: "payee.beneficiary_id", Code: "NOT_FOUND"})
			} else {
				var0 = b.ID
			}
		}
	} else if in.Payee != nil && in.Payee.Type != "OWNER" {
		det = append(det, errs.Detail{Field: "payee.type", Code: "INVALID_VALUE"})
	}
	if len(det) > 0 {
		return "", httpx.Validation(det...)
	}
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return "", err
	}
	id := ids.New()
	ct, err := s.AEAD.Seal([]byte(canonical), []byte("app.payout_destinations:"+id+":account"))
	if err != nil {
		return "", err
	}
	bidx := s.Keyed.Sum("payout_account", in.Rail, bank, canonical)
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var reused int
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT owner_ref) FROM app.payout_destinations WHERE account_bidx = $1 AND status <> 'RETIRED'
			AND owner_ref <> coalesce($2::uuid, $3::uuid)`, bidx, ownerUser, ownerOrg).Scan(&reused); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT create_dest`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.payout_destinations (id, owner_user_id, owner_organisation_id, created_by, payee_type, beneficiary_id,
			category, rail, currency, holder_name, bank_code, account_ciphertext, account_key_id, account_bidx, masked_suffix, status,
			cooling_off_until, last_changed_by, policy_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'UNVERIFIED',$16,$4,$17)`,
			id, ownerUser, ownerOrg, actorID, payeeType, var0, r.category, in.Rail, in.Currency, holder, nullable(bank), ct, s.AEAD.KeyID(), bidx,
			masked, s.now().Add(CoolingOff), pol.Version)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT create_dest`)
			if strings.Contains(err.Error(), "uq_payout_destinations_live_account") {
				return errs.New(errs.Conflict, "DESTINATION_EXISTS", "You already have this payout destination.")
			}
			return err
		}
		if err := s.formatCheck(ctx, tx, id, 1); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, 1, "CREATED", "", "UNVERIFIED", "", actorID, "USER"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payout_destination.created", TargetType: "payout_destination", TargetID: id,
			Metadata: map[string]any{"rail": in.Rail, "currency": in.Currency, "reused_by_other_owners": reused}}); err != nil {
			return err
		}
		d := Destination{ID: id}
		if ownerUser != nil {
			d.Owner = Owner{"USER", actorID}
		} else {
			d.Owner = Owner{"ORGANISATION", *in.OwnerOrganisationID}
		}
		return s.emit(ctx, tx, "payouts.destination_created", d, map[string]any{"reused_by_other_owners": reused})
	})
	return id, err
}

// Get returns a destination the caller may see (owner user, or member of the owner organisation).
func (s *Service) Get(ctx context.Context, actorID, id string) (Destination, error) {
	d, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return d, err
	}
	if d.ownerUser != nil && *d.ownerUser != actorID {
		return Destination{}, ErrNotFound
	}
	if d.ownerOrg != nil {
		role, err := s.Orgs.MemberRole(ctx, *d.ownerOrg, actorID)
		if err != nil {
			return d, err
		}
		if role != organisations.RoleAdmin { // destinations are ORG_ADMIN-only (financial details)
			return Destination{}, ErrNotFound
		}
	}
	return d, nil
}

// View returns any destination (reviewer endpoints).
func (s *Service) View(ctx context.Context, id string) (Destination, error) {
	return s.load(ctx, s.Pool, id, false)
}

// List returns the caller's destinations and those of organisations they administer.
func (s *Service) List(ctx context.Context, actorID string) ([]Destination, error) {
	orgs, err := s.Orgs.OrganisationsOf(ctx, actorID)
	if err != nil {
		return nil, err
	}
	var admin []string
	for _, o := range orgs {
		if ok, err := s.Orgs.IsAdmin(ctx, o, actorID); err != nil {
			return nil, err
		} else if ok {
			admin = append(admin, o)
		}
	}
	if admin == nil {
		admin = []string{}
	}
	rows, err := s.Pool.Query(ctx, selectD+` WHERE (owner_user_id = $1 OR owner_organisation_id = ANY($2::uuid[])) AND status <> 'RETIRED'
		ORDER BY created_at DESC LIMIT 100`, actorID, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Destination{}
	for rows.Next() {
		d, err := scanD(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Change replaces the account details: a new details version, verification reset, cooling-off restarted.
func (s *Service) Change(ctx context.Context, actorID, id string, in Input, expectVersion int) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if ok, err := s.canEdit(ctx, d, actorID); err != nil {
			return err
		} else if !ok {
			return ErrNotFound
		}
		if expectVersion > 0 && expectVersion != d.Version {
			return errs.New(errs.Conflict, "CASE_STATE_CHANGED", "The destination changed in the meantime. Reload and try again.")
		}
		if d.Status != "UNVERIFIED" && d.Status != "PENDING_VERIFICATION" && d.Status != "VERIFIED" && d.Status != "REJECTED" {
			return errNotAllowed
		}
		var oldCT []byte
		var oldBank *string
		if err := tx.QueryRow(ctx, `SELECT account_ciphertext, bank_code FROM app.payout_destinations WHERE id = $1`, id).Scan(&oldCT, &oldBank); err != nil {
			return err
		}
		pt, err := s.AEAD.Open(oldCT, []byte("app.payout_destinations:"+id+":account"))
		if err != nil {
			return err
		}
		ident, bank, holder := string(pt), "", d.HolderName
		if oldBank != nil {
			bank = *oldBank
		}
		if in.AccountIdentifier != nil {
			ident = *in.AccountIdentifier
		}
		if in.BankCode != nil {
			bank = strings.ToUpper(strings.TrimSpace(*in.BankCode))
		}
		var det []errs.Detail
		if in.HolderName != nil {
			holder = strings.Join(strings.Fields(*in.HolderName), " ")
			if n := utf8.RuneCountInString(holder); n < 2 || n > 140 {
				det = append(det, errs.Detail{Field: "holder_name", Code: "INVALID_LENGTH"})
			}
		}
		canonical, masked, fdet := normalise(d.Rail, ident, bank)
		det = append(det, fdet...)
		if len(det) > 0 {
			return httpx.Validation(det...)
		}
		ct, err := s.AEAD.Seal([]byte(canonical), []byte("app.payout_destinations:"+id+":account"))
		if err != nil {
			return err
		}
		bidx := s.Keyed.Sum("payout_account", d.Rail, bank, canonical)
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET account_ciphertext = $2, account_bidx = $3, masked_suffix = $4, holder_name = $5,
			bank_code = $6, status = 'UNVERIFIED', ownership_status = 'NOT_STARTED', compliance_status = 'PENDING', verification_method = NULL,
			verified_at = NULL, verified_by = NULL, expires_at = NULL, assigned_to = NULL, assigned_at = NULL, requested_at = NULL,
			cooling_off_until = $7, last_changed_by = $8, details_version = details_version + 1 WHERE id = $1 RETURNING version`,
			id, ct, bidx, masked, holder, nullable(bank), s.now().Add(CoolingOff), actorID).Scan(&version); err != nil {
			if strings.Contains(err.Error(), "uq_payout_destinations_live_account") {
				return errs.New(errs.Conflict, "DESTINATION_EXISTS", "You already have this payout destination.")
			}
			return err
		}
		if err := s.formatCheck(ctx, tx, id, d.detailsVersion+1); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "DETAILS_CHANGED", d.Status, "UNVERIFIED", "", actorID, "USER"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "payout_destination.changed", TargetType: "payout_destination", TargetID: id}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "payouts.destination_changed", d, nil)
	})
}

// LockEditable locks the destination and checks the owner may attach evidence (before a verification request).
func (s *Service) LockEditable(ctx context.Context, tx pgx.Tx, actorID, id string) error {
	d, err := s.load(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if ok, err := s.canEdit(ctx, d, actorID); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	switch d.Status {
	case "UNVERIFIED", "REJECTED", "EXPIRED", "SUSPENDED":
		return nil
	}
	return errs.New(errs.Conflict, "CASE_NOT_EDITABLE", "Evidence cannot be added while the destination is being reviewed or once it is verified.")
}

// CanView reports whether actorID may see the destination's evidence.
func (s *Service) CanView(ctx context.Context, actorID, id string) (bool, error) {
	_, err := s.Get(ctx, actorID, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// WithTx exposes a transaction on the module pool.
func (s *Service) WithTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return s.tx(ctx, fn)
}

// RequestVerification asks for review. Documentary methods need CLEAN ownership evidence; PROVIDER_LOOKUP
// records the development mock result PROVIDER_CONFIRMATION_REQUIRED (non-production) and still needs
// documentary evidence before a reviewer can confirm ownership.
func (s *Service) RequestVerification(ctx context.Context, actorID, id, method string) error {
	switch method {
	case "BANK_LETTER", "BANK_STATEMENT", "MOBILE_MONEY_STATEMENT", "PROVIDER_LOOKUP":
	default:
		return httpx.Validation(errs.Detail{Field: "method", Code: "INVALID_VALUE"})
	}
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	var reqs []kyc.Requirement
	if method != "PROVIDER_LOOKUP" {
		reqs = []kyc.Requirement{pol.Rules.PayoutDestination.OwnershipEvidence}
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if ok, err := s.canEdit(ctx, d, actorID); err != nil {
			return err
		} else if !ok {
			return ErrNotFound
		}
		if d.Status == "PENDING_VERIFICATION" {
			return nil
		}
		if d.Status != "UNVERIFIED" && d.Status != "REJECTED" && d.Status != "EXPIRED" && d.Status != "SUSPENDED" {
			return errNotAllowed
		}
		if len(reqs) > 0 {
			det, err := s.KYC.RequirementsCheck(ctx, "destination_id", id, reqs)
			if err != nil {
				return err
			}
			if len(det) > 0 {
				e := errs.New(errs.Unprocessable, "SUBMISSION_INCOMPLETE", "Upload ownership evidence (bank letter or statement) first.")
				e.Details = det
				return e
			}
		}
		ownership := "PENDING"
		if method == "PROVIDER_LOOKUP" {
			ownership = "PROVIDER_CONFIRMATION_REQUIRED"
			if _, err := tx.Exec(ctx, `INSERT INTO app.payout_destination_checks (id, destination_id, details_version, check_kind, method, result,
				non_production, provider_name, detail_code) VALUES ($1, $2, $3, 'OWNERSHIP', 'DEV_MOCK_PROVIDER', 'PROVIDER_CONFIRMATION_REQUIRED', true,
				'dev-mock (NOT a provider; never confirms ownership)', 'NO_PROVIDER_INTEGRATED')`, ids.New(), id, d.detailsVersion); err != nil {
				return err
			}
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = 'PENDING_VERIFICATION', ownership_status = $2, compliance_status = 'PENDING',
			verification_method = $3, requested_at = $4 WHERE id = $1 RETURNING version`, id, ownership, method, s.now()).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "VERIFICATION_REQUESTED", d.Status, "PENDING_VERIFICATION", "", actorID, "USER"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payout_destination.verification_requested", TargetType: "payout_destination", TargetID: id,
			Metadata: map[string]any{"method": method}})
	})
}

// Retire removes a destination from use (history kept).
func (s *Service) Retire(ctx context.Context, actorID, id string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if ok, err := s.canEdit(ctx, d, actorID); err != nil {
			return err
		} else if !ok {
			return ErrNotFound
		}
		if d.Status == "RETIRED" {
			return nil
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = 'RETIRED' WHERE id = $1 RETURNING version`, id).Scan(&version); err != nil {
			return errNotAllowed
		}
		if err := s.event(ctx, tx, id, version, "RETIRED", d.Status, "RETIRED", "", actorID, "USER"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payout_destination.retired", TargetType: "payout_destination", TargetID: id})
	})
}

// AssignedTo returns the assigned reviewer (nil when unassigned).
func (d Destination) AssignedTo() *string { return d.assignedTo }
