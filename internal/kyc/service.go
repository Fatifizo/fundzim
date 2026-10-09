// Package kyc implements individual (KYC) and organisation (KYB) verification: verification profiles, cases,
// encrypted identities, KYB persons, verification documents metadata, reviewer decisions and the versioned
// verification policy (ADR-015, ADR-034, ADR-035; Stage 1 kyc-architecture / kyb-architecture).
//
// Data protection: the module uses its own connection pool (role fundzim_kyc, schema kyc only). Identity
// attributes are AES-256-GCM encrypted with the KYC key (AAD binds ciphertext to its row and field) and
// searchable only through HMAC blind indexes. Audit events, evidence records and outbox events are written
// through the SECURITY DEFINER gateways in the same transaction as the domain change. Nothing identifying is
// logged; reviewer notes are encrypted and never returned to subjects.
//
// Trust decisions are separate (brief §3): a verified email or phone is not identity verification; an
// identity-verified person is not payout-verified; screening is recorded as NOT performed while no provider
// is approved, so approvals always carry that condition.
package kyc

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Levels (kyc-architecture §3).
const (
	LevelUnverified       = "UNVERIFIED"
	LevelBasic            = "BASIC_VERIFIED"
	LevelIdentity         = "IDENTITY_VERIFIED"
	LevelPayout           = "PAYOUT_VERIFIED"
	OrgLevelUnverified    = "ORG_UNVERIFIED"
	OrgLevelRegistered    = "ORG_REGISTERED_VERIFIED"
	OrgLevelKYB           = "ORG_KYB_VERIFIED"
	OrgLevelPayout        = "ORG_PAYOUT_VERIFIED"
	ProfileActive         = "ACTIVE"
	ProfilePendingReview  = "PENDING_REVIEW"
	ProfileRejected       = "REJECTED"
	ProfileSuspended      = "SUSPENDED"
	RiskLow               = "LOW"
	RiskStandard          = "STANDARD"
	RiskEnhanced          = "ENHANCED"
	RiskRestricted        = "RESTRICTED"
	KindKYC               = "KYC"
	KindKYB               = "KYB"
	SystemActorID         = "00000000-0000-0000-0000-000000000001"
	screeningNotPerformed = "SCREENING_PROVIDER_NOT_SELECTED"
)

var levelRank = map[string]int{LevelUnverified: 0, LevelBasic: 1, LevelIdentity: 2, LevelPayout: 3,
	OrgLevelUnverified: 0, OrgLevelRegistered: 1, OrgLevelKYB: 2, OrgLevelPayout: 3}

// AtLeast reports whether level satisfies min (same family).
func AtLeast(level, min string) bool { return levelRank[level] >= levelRank[min] }

// Deps are the module dependencies.
type Deps struct {
	Pool    *pgxpool.Pool // role fundzim_kyc
	AppPool *pgxpool.Pool // role fundzim_app: users / organisations reads through their packages only
	Orgs    *organisations.Service
	Storage *storage.Service // PRIVATE_KYC objects (scan status, open)
	AEAD    *crypto.AEAD     // KYC field key
	Keyed   *crypto.Keyed    // KYC blind-index key
	Clock   clock.Clock
	Logger  *slog.Logger
}

// Service is the kyc module.
type Service struct{ Deps }

// New builds the service.
func New(d Deps) *Service { return &Service{Deps: d} }

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// aad binds a ciphertext to its table, row and field (moving it elsewhere makes decryption fail).
func aad(table, id, field string) []byte { return []byte("kyc." + table + ":" + id + ":" + field) }
