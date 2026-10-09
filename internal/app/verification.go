package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/beneficiaries"
	"github.com/Fatifizo/fundzim/internal/compliance"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
	"github.com/Fatifizo/fundzim/internal/risk"
	"github.com/Fatifizo/fundzim/internal/storage"
	"github.com/Fatifizo/fundzim/internal/users"
	"github.com/Fatifizo/fundzim/internal/verification"

	"github.com/prometheus/client_golang/prometheus"
)

// Stage 5 key ids (local keys; KMS in Stage 18).
const (
	kycFieldKeyID        = "local-kyc-field-1"
	kycBlindIndexKeyID   = "local-kyc-bidx-1"
	complianceNotesKeyID = "local-compliance-1"
)

// VerificationDeps are what the Stage 5 modules need from the process (API or worker).
type VerificationDeps struct {
	AppPool   *pgxpool.Pool
	Config    config.Config
	Clock     clock.Clock
	Logger    *slog.Logger
	Registry  prometheus.Registerer
	Blobs     *pstorage.Client
	Orgs      *organisations.Service
	Auth      *auth.Service // StaffHasPermission for compliance eligibility (nil in the worker is tolerated)
	WithScans bool          // worker: build the malware scanner
}

// VerificationModules is the built Stage 5 module set.
type VerificationModules struct {
	KYCPool, CompliancePool *pgxpool.Pool
	Storage                 *storage.Service
	KYC                     *kyc.Service
	Beneficiaries           *beneficiaries.Service
	Payouts                 *payouts.Service
	Risk                    *risk.Service
	Compliance              *compliance.Service
	HTTP                    *verification.Service
}

// Close closes the restricted pools.
func (m *VerificationModules) Close() {
	if m == nil {
		return
	}
	if m.KYCPool != nil {
		m.KYCPool.Close()
	}
	if m.CompliancePool != nil {
		m.CompliancePool.Close()
	}
}

func decodeKey(v config.Secret) []byte {
	if v == "" {
		return nil
	}
	b, _ := hex.DecodeString(v.Reveal())
	return b
}

// relations implements compliance.Relations on the app pool.
type relations struct {
	pool *pgxpool.Pool
	orgs *organisations.Service
	auth *auth.Service
}

func (r relations) PersonalAccount(ctx context.Context, staffID string) (string, error) {
	return users.PersonalAccount(ctx, r.pool, staffID)
}
func (r relations) IsOrganisationMember(ctx context.Context, orgID, userID string) (bool, error) {
	role, err := r.orgs.MemberRole(ctx, orgID, userID)
	return role != "", err
}
func (r relations) StaffHasPermission(ctx context.Context, staffID, perm string) (bool, error) {
	if r.auth == nil {
		return false, fmt.Errorf("staff permission lookup unavailable")
	}
	return r.auth.StaffHasPermission(ctx, staffID, perm)
}

// riskSummary adapts risk.Service to verification.RiskReader.
type riskSummary struct{ r *risk.Service }

func (a riskSummary) Summary(ctx context.Context, subjectType, subjectID string) (any, error) {
	as, signals, err := a.r.Latest(ctx, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"assessment": as, "signals": signals, "note": "A risk score routes cases to people; it is never proof of fraud."}, nil
}

// NewVerificationModules builds storage, kyc, beneficiaries, payouts, risk, compliance and the HTTP layer.
func NewVerificationModules(ctx context.Context, d VerificationDeps) (*VerificationModules, error) {
	cfg := d.Config
	m := &VerificationModules{}
	var err error
	m.KYCPool, err = db.NewPool(ctx, db.Options{URL: cfg.Database.KYCURL.Reveal(), MaxConns: 8, ConnectTimeout: cfg.Database.ConnectTimeout, AppName: "fundzim-kyc"})
	if err != nil {
		return nil, fmt.Errorf("kyc pool: %w", err)
	}
	m.CompliancePool, err = db.NewPool(ctx, db.Options{URL: cfg.Database.ComplianceURL.Reveal(), MaxConns: 4, ConnectTimeout: cfg.Database.ConnectTimeout,
		AppName: "fundzim-compliance"})
	if err != nil {
		m.Close()
		return nil, fmt.Errorf("compliance pool: %w", err)
	}
	fail := func(e error) (*VerificationModules, error) { m.Close(); return nil, e }
	var scanner storage.Scanner
	if d.WithScans {
		if scanner, err = storage.NewScanner(cfg.Verification.Scanner, cfg.Verification.ClamAVAddr, cfg.Verification.ScanTimeout, string(cfg.App.Env)); err != nil {
			return fail(err)
		}
	}
	var blobs storage.BlobStore = unavailableBlobs{}
	if d.Blobs != nil {
		blobs = d.Blobs
	}
	m.Storage, err = storage.New(storage.Config{AppEnv: string(cfg.App.Env), UploadMaxBytes: cfg.Verification.UploadMaxBytes, UploadTTL: cfg.Verification.UploadTTL,
		ScanTimeout: cfg.Verification.ScanTimeout, TicketKey: decodeKey(cfg.Security.DocumentTicketKey), TicketTTL: cfg.Verification.DocumentTicketTTL,
		SSEKey: decodeKey(cfg.Security.StorageSSEKey)},
		storage.Deps{Pool: d.AppPool, Blobs: blobs, Scanner: scanner, Clock: d.Clock, Logger: d.Logger, Metrics: storage.NewMetrics(d.Registry)})
	if err != nil {
		return fail(err)
	}
	kycAEAD, err := crypto.NewAEAD(kycFieldKeyID, cfg.Security.KYCFieldKey.Reveal())
	if err != nil {
		return fail(fmt.Errorf("kyc field key: %w", err))
	}
	kycKeyed, err := crypto.NewKeyed(kycBlindIndexKeyID, cfg.Security.KYCBlindIndexKey.Reveal())
	if err != nil {
		return fail(fmt.Errorf("kyc blind index key: %w", err))
	}
	appAEAD, err := crypto.NewAEAD(fieldKeyID, cfg.Security.FieldEncryptionKey.Reveal())
	if err != nil {
		return fail(err)
	}
	appKeyed, err := crypto.NewKeyed(blindIndexKeyID, cfg.Security.BlindIndexKey.Reveal())
	if err != nil {
		return fail(err)
	}
	notesAEAD, err := crypto.NewAEAD(complianceNotesKeyID, cfg.Security.ComplianceFieldKey.Reveal())
	if err != nil {
		return fail(fmt.Errorf("compliance key: %w", err))
	}
	m.KYC = kyc.New(kyc.Deps{Pool: m.KYCPool, AppPool: d.AppPool, Orgs: d.Orgs, Storage: m.Storage, AEAD: kycAEAD, Keyed: kycKeyed, Clock: d.Clock, Logger: d.Logger})
	m.Beneficiaries = &beneficiaries.Service{Pool: d.AppPool, KYC: m.KYC, Orgs: d.Orgs, Clock: d.Clock, Logger: d.Logger}
	m.Payouts = &payouts.Service{Pool: d.AppPool, KYC: m.KYC, Benefs: m.Beneficiaries, Orgs: d.Orgs, AEAD: appAEAD, Keyed: appKeyed, Clock: d.Clock, Logger: d.Logger}
	m.Risk = risk.New(d.AppPool, d.Clock, d.Logger)
	m.Compliance, err = compliance.New(compliance.Deps{Pool: m.CompliancePool, Clock: d.Clock, Logger: d.Logger, NotesAEAD: notesAEAD,
		Relations: relations{pool: d.AppPool, orgs: d.Orgs, auth: d.Auth}, StepUpMaxAge: cfg.Auth.StepUpMaxAge})
	if err != nil {
		return fail(err)
	}
	m.HTTP = &verification.Service{KYC: m.KYC, Benefs: m.Beneficiaries, Payouts: m.Payouts, Storage: m.Storage, Orgs: d.Orgs,
		Risk: riskSummary{m.Risk}, Clock: d.Clock, Logger: d.Logger, StepUpMaxAge: cfg.Auth.StepUpMaxAge,
		TicketTTL: cfg.Verification.DocumentTicketTTL, MaxUpload: cfg.Verification.UploadMaxBytes}
	if d.Auth != nil {
		m.HTTP.StaffCan = d.Auth.StaffHasPermission
	}
	return m, nil
}

// uploadExemptions lets the document upload route exceed the global JSON body limit (S stream).
func uploadExemptions(cfg config.Config) []httpx.BodyLimitExemption {
	return []httpx.BodyLimitExemption{{Pattern: "POST /api/v1/verification/documents", Max: cfg.Verification.UploadMaxBytes + 64<<10}}
}

// registerVerificationRoutes registers the Stage 5 routes (contract §7). Object-level authorisation is in the
// handlers; staff decisions check the type-specific permission and a fresh step-up in the handler.
func registerVerificationRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, logger *slog.Logger) {
	v := m.HTTP
	user, perm := httpx.User(), httpx.Permission
	idemOpt := func(scope string) httpx.RouteOption {
		return httpx.With(idempotency.Middleware(idem, idempotency.Optional, scope, logger))
	}
	r.HandleFunc("GET /api/v1/kyc/status", user, v.KYCStatus)
	r.HandleFunc("POST /api/v1/kyc/cases", user, v.CreateKYCCase, idemOpt("kyc.case_create"))
	r.HandleFunc("GET /api/v1/kyc/cases/current", user, v.CurrentKYCCase)
	r.HandleFunc("PATCH /api/v1/kyc/cases/{case_id}", user, v.UpdateKYCCase)
	r.HandleFunc("POST /api/v1/kyc/cases/{case_id}/submit", user, v.SubmitKYCCase)
	r.HandleFunc("POST /api/v1/kyc/cases/{case_id}/withdraw", user, v.WithdrawKYCCase)

	r.HandleFunc("POST /api/v1/organisations/{org_id}/kyb", user, v.CreateKYB)
	r.HandleFunc("GET /api/v1/organisations/{org_id}/kyb", user, v.GetKYB)
	r.HandleFunc("PATCH /api/v1/organisations/{org_id}/kyb", user, v.UpdateKYB)
	r.HandleFunc("POST /api/v1/organisations/{org_id}/kyb/persons", user, v.AddKYBPerson)
	r.HandleFunc("DELETE /api/v1/organisations/{org_id}/kyb/persons/{person_id}", user, v.RemoveKYBPerson)
	r.HandleFunc("POST /api/v1/organisations/{org_id}/kyb/submit", user, v.SubmitKYB)
	r.HandleFunc("POST /api/v1/organisations/{org_id}/kyb/withdraw", user, v.WithdrawKYB)

	r.HandleFunc("POST /api/v1/beneficiaries", user, v.CreateBeneficiary, idemOpt("beneficiaries.create"))
	r.HandleFunc("GET /api/v1/beneficiaries", user, v.ListBeneficiaries)
	r.HandleFunc("GET /api/v1/beneficiaries/{beneficiary_id}", user, v.GetBeneficiary)
	r.HandleFunc("PATCH /api/v1/beneficiaries/{beneficiary_id}", user, v.UpdateBeneficiary)
	r.HandleFunc("POST /api/v1/beneficiaries/{beneficiary_id}/submit", user, v.SubmitBeneficiary)
	r.HandleFunc("POST /api/v1/beneficiaries/{beneficiary_id}/withdraw", user, v.WithdrawBeneficiary)

	r.HandleFunc("POST /api/v1/payout-destinations", user, v.CreateDestination, idemOpt("payout_destinations.create"))
	r.HandleFunc("GET /api/v1/payout-destinations", user, v.ListDestinations)
	r.HandleFunc("GET /api/v1/payout-destinations/{destination_id}", user, v.GetDestination)
	r.HandleFunc("PATCH /api/v1/payout-destinations/{destination_id}", user, v.ChangeDestination)
	r.HandleFunc("POST /api/v1/payout-destinations/{destination_id}/verification", user, v.RequestDestinationVerification)
	r.HandleFunc("DELETE /api/v1/payout-destinations/{destination_id}", user, v.RetireDestination)

	// documents: owners (users) and reviewers (staff; checked in the handler)
	authn := httpx.Authenticated()
	r.HandleFunc("POST /api/v1/verification/documents", user, v.UploadDocument)
	r.HandleFunc("GET /api/v1/verification/documents", user, v.ListDocuments)
	r.HandleFunc("GET /api/v1/verification/documents/{document_id}", authn, v.GetDocument)
	r.HandleFunc("POST /api/v1/verification/documents/{document_id}/access", authn, v.DocumentAccess)
	r.HandleFunc("GET /api/v1/verification/documents/{document_id}/content", authn, v.DocumentContent)
	r.HandleFunc("DELETE /api/v1/verification/documents/{document_id}", user, v.DeleteDocument)

	// reviewers
	r.HandleFunc("GET /api/v1/admin/verification/cases", perm("kyc.case.review"), v.Queue)
	r.HandleFunc("GET /api/v1/admin/verification/cases/{case_id}", perm("kyc.case.review"), v.CaseDetail)
	for _, a := range []string{"assign", "start-review", "request-info", "approve", "second-approval", "reject", "escalate", "return",
		"suspend", "reinstate", "reopen", "revoke"} {
		r.HandleFunc("POST /api/v1/admin/verification/cases/{case_id}/"+a, perm("kyc.case.review"), v.Action(a))
	}
	r.HandleFunc("POST /api/v1/admin/verification/cases/{case_id}/reveal-identity-number", perm("kyc.identity_number.reveal"), v.RevealIDNumber)
	r.HandleFunc("GET /api/v1/admin/verification/policies", httpx.Staff(), v.ListPolicies)
	r.HandleFunc("POST /api/v1/admin/verification/policies", perm("verification_policy.request"), v.ProposePolicy)
	r.HandleFunc("POST /api/v1/admin/verification/policies/{policy_id}/approve", perm("verification_policy.approve"), v.DecidePolicy(true))
	r.HandleFunc("POST /api/v1/admin/verification/policies/{policy_id}/reject", perm("verification_policy.approve"), v.DecidePolicy(false))

	for _, rt := range m.Compliance.Routes() {
		var opts []httpx.RouteOption
		if rt.Pattern == "POST /api/v1/admin/compliance/cases" {
			opts = append(opts, idemOpt("admin.compliance_case_open"))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// ---- worker side: consumers, notifications and sweeps ------------------------------------------------------

type eventPayload struct {
	CaseID        string `json:"case_id"`
	Kind          string `json:"kind"`
	SubjectType   string `json:"subject_type"`
	SubjectID     string `json:"subject_id"`
	Level         string `json:"level"`
	Status        string `json:"status"`
	BeneficiaryID string `json:"beneficiary_id"`
	DestinationID string `json:"destination_id"`
	OwnerType     string `json:"owner_type"`
	OwnerID       string `json:"owner_id"`
	Message       string `json:"message"`
}

// notifications (no identity details, no document content: brief §26)
var verificationNotices = map[string][2]string{
	"kyc.case_submitted":                               {"We received your verification", "Thank you. Your verification was submitted and is waiting for review. We will email you when it has been reviewed."},
	"kyc.information_requested":                        {"More information needed for your verification", "A reviewer needs more information to complete your verification. Sign in to FundZim and open your verification dashboard to respond."},
	"kyc.case_approved":                                {"Your verification was approved", "Your verification was approved. Sign in to see what you can now do on FundZim."},
	"kyc.case_rejected":                                {"Your verification was not approved", "Your verification was not approved. Sign in to FundZim to see the reason and your options."},
	"kyc.verification_expired":                         {"Your verification has expired", "Your verification is no longer current. Sign in to FundZim to verify again."},
	"kyc.verification_suspended":                       {"Your verification is suspended", "Your verification has been suspended pending review. Contact support if you have questions."},
	"kyc.verification_revoked":                         {"Your verification was revoked", "Your verification was revoked. Contact support if you have questions."},
	"beneficiaries.verification_submitted":             {"Beneficiary submitted for verification", "A beneficiary you declared was submitted for verification."},
	"beneficiaries.verification_information_requested": {"More information needed for a beneficiary", "A reviewer needs more information about a beneficiary you declared. Sign in to respond."},
	"beneficiaries.verification_approved":              {"Beneficiary verified", "A beneficiary you declared has been verified."},
	"beneficiaries.verification_rejected":              {"Beneficiary not verified", "A beneficiary you declared was not verified. Sign in to see the reason."},
	"beneficiaries.verification_expired":               {"Beneficiary verification expired", "A beneficiary verification expired because the requested information was not provided."},
	"payouts.destination_verified":                     {"Payout destination review completed", "Your payout destination was verified. Payouts are not available on FundZim yet."},
	"payouts.destination_rejected":                     {"Payout destination review completed", "Your payout destination could not be verified. Sign in to see the reason."},
	"payouts.destination_information_requested":        {"More information needed for a payout destination", "A reviewer needs more information about your payout destination. Sign in to respond."},
	"payouts.destination_suspended":                    {"Payout destination suspended", "One of your payout destinations was suspended pending review."},
	"payouts.destination_expired":                      {"Payout destination verification expired", "One of your payout destinations needs to be verified again."},
}

// recipients resolves notification recipients' email addresses (subject user, or organisation admins).
func recipients(ctx context.Context, pool *pgxpool.Pool, orgs *organisations.Service, subjectType, subjectID string) ([]string, error) {
	var userIDs []string
	switch subjectType {
	case "USER":
		userIDs = []string{subjectID}
	case "ORGANISATION":
		ids, err := orgs.AdminIDs(ctx, subjectID)
		if err != nil {
			return nil, err
		}
		userIDs = ids
	}
	var out []string
	for _, id := range userIDs {
		a, err := users.ByID(ctx, pool, id)
		if err != nil {
			continue
		}
		out = append(out, a.Email)
	}
	return out, nil
}

// registerVerificationConsumers subscribes Stage 5 consumers in the worker.
func registerVerificationConsumers(reg *outbox.Registry, m *VerificationModules, appPool *pgxpool.Pool, orgs *organisations.Service, mail auth.Mailer, clk clock.Clock) {
	m.Storage.RegisterConsumers(reg)
	for _, c := range m.Risk.Consumers() {
		reg.Subscribe(c.Name, c.EventType, c.Handle)
	}
	for _, c := range m.Compliance.Consumers() {
		reg.Subscribe(c.Name, c.EventType, c.Handle)
	}
	parse := func(del outbox.Delivery) (eventPayload, error) {
		var p eventPayload
		return p, json.Unmarshal(del.Payload, &p)
	}
	// users mirror of the KYC level (organisation levels are read from kyc directly)
	reg.Subscribe("users.kyc_mirror", "kyc.level_changed", func(ctx context.Context, del outbox.Delivery) error {
		p, err := parse(del)
		if err != nil || p.SubjectType != "USER" {
			return err
		}
		return users.SetKYCMirror(ctx, appPool, p.SubjectID, p.Level, p.Status, del.OccurredAt)
	})
	// PAYOUT_VERIFIED upgrade for identity-verified individuals whose own destination was verified
	reg.Subscribe("kyc.payout_ownership", "payouts.destination_verified", func(ctx context.Context, del outbox.Delivery) error {
		p, err := parse(del)
		if err != nil || p.OwnerType != "USER" {
			return err
		}
		return m.KYC.RecordPayoutOwnership(ctx, p.OwnerID, p.DestinationID)
	})
	for evType, tpl := range verificationNotices {
		tpl := tpl
		reg.Subscribe("verification.notify", evType, func(ctx context.Context, del outbox.Delivery) error {
			p, err := parse(del)
			if err != nil {
				return err
			}
			st, sid := p.SubjectType, p.SubjectID
			if st == "" {
				st, sid = p.OwnerType, p.OwnerID
			}
			to, err := recipients(ctx, appPool, orgs, st, sid)
			if err != nil {
				return err
			}
			for _, addr := range to {
				if err := mail.SendEmail(ctx, addr, tpl[0], tpl[1]+"\n\nThis is an automated message from FundZim."); err != nil {
					return err
				}
			}
			return nil
		})
	}
}

// VerificationSweepArgs runs the verification lifecycle sweeps (expiry, information-request timeouts).
type VerificationSweepArgs struct{}

// Kind implements river.JobArgs.
func (VerificationSweepArgs) Kind() string { return "verification.sweep" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (VerificationSweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
}

// VerificationSweepWorker expires stale verifications so they never stay trusted indefinitely (brief §19).
type VerificationSweepWorker struct {
	river.WorkerDefaults[VerificationSweepArgs]
	M      *VerificationModules
	Logger *slog.Logger
}

// Work implements river.Worker.
func (w *VerificationSweepWorker) Work(ctx context.Context, _ *river.Job[VerificationSweepArgs]) error {
	res, err := w.M.KYC.Sweep(ctx, 500)
	if err != nil {
		return err
	}
	nb, err := w.M.Beneficiaries.Sweep(ctx, 500)
	if err != nil {
		return err
	}
	nd, err := w.M.Payouts.Sweep(ctx, 500)
	if err != nil {
		return err
	}
	if res.Expired+res.InfoExpired+nb+nd > 0 {
		w.Logger.Info("verification sweep", slog.Int("kyc_expired", res.Expired), slog.Int("info_expired", res.InfoExpired),
			slog.Int("beneficiaries_expired", nb), slog.Int("destinations_expired", nd))
	}
	return nil
}

// verificationPeriodic schedules the sweep.
func verificationPeriodic() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return VerificationSweepArgs{}, nil },
		&river.PeriodicJobOpts{ID: VerificationSweepArgs{}.Kind(), RunOnStart: true})}
}

// unavailableBlobs is the blob store when object storage is not configured: every operation fails with 503,
// so documents can never be accepted or served without real private storage.
type unavailableBlobs struct{}

func (unavailableBlobs) PutSSE(context.Context, pstorage.Ref, io.ReadSeeker, int64, string, pstorage.SSEKey) error {
	return storage.ErrStorageUnavailable
}
func (unavailableBlobs) GetSSE(context.Context, pstorage.Ref, pstorage.SSEKey) (io.ReadCloser, error) {
	return nil, storage.ErrStorageUnavailable
}
func (unavailableBlobs) CopySSE(context.Context, pstorage.Ref, pstorage.Ref, pstorage.SSEKey) error {
	return storage.ErrStorageUnavailable
}
func (unavailableBlobs) Delete(context.Context, pstorage.Ref) error {
	return storage.ErrStorageUnavailable
}
