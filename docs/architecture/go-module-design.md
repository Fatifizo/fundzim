# Go Module Design

**Stage 2 — design only.** Package layout, platform sub-packages, the composition root and, for **each of the
19 modules** in [design-baseline.md §2](../stage-2/design-baseline.md), its responsibilities, tables, public
service interface, repositories, dependencies, events, error codes, authorisation, transaction boundaries and
test strategy.

> **All Go code in this document is a sketch, not compiled — Go is not installed in Stage 2**
> ([prerequisite-assessment.md §4](../stage-2/prerequisite-assessment.md)). Names and signatures are the
> intended shape; Stage 3 compiles them and may adjust details without changing the boundaries. Exact library
> versions are pinned in Stage 3.

Related: [dependency-rules.md](dependency-rules.md) · [dependency-matrix.md](dependency-matrix.md) ·
[domain-boundaries.md](domain-boundaries.md) · [system-overview.md](system-overview.md) (transaction catalogue
TX-xx) · [component-diagram.md](component-diagram.md) · [error-model.md](../api/error-model.md) ·
[authorization-matrix.md](../api/authorization-matrix.md) · [payment-state-machine.md](payment-state-machine.md) ·
[payout-state-machine.md](payout-state-machine.md) · [ledger-posting-model.md](ledger-posting-model.md) ·
[ledger-invariants.md](ledger-invariants.md) · [payment-provider-interfaces.md](payment-provider-interfaces.md) ·
[background-processing.md](background-processing.md) · schema docs under [docs/database/](../database/)

---

## 1. Repository layout

One Go module at the repository root (baseline §1; module path chosen in Stage 3, placeholder
`github.com/Fatifizo/fundzim`).

```
go.mod
apps/api/cmd/api/main.go           # HTTP mode:   app.Run(ctx, app.ModeAPI)
apps/api/cmd/worker/main.go        # worker mode: app.Run(ctx, app.ModeWorker)
apps/api/cmd/fundzimctl/main.go    # operator CLI: migrate, ledger recompute, invariants, audit chain verify
internal/
  app/                             # composition root (only package that imports sub-packages of modules)
    app.go  wiring.go  router.go  jobs.go  events.go  ports.go  config.go
  platform/                        # shared kernel (§3)
    config/ db/ money/ ids/ clock/ errs/ actor/ httpx/ httpclient/ log/ crypto/
    outbox/ jobs/ idempotency/ makerchecker/ blob/ featureflags/ market/ testkit/
  <module>/                        # one directory per module (§4), e.g. internal/payments/
    <module>.go                    # root package: Service interface(s), types, errors, permissions, event names
    service/                       # implementation of the root interfaces
    store/                         # sqlc: queries/*.sql, generated *.go, store.go (pool assertion)
    http/                          # handlers, request/response DTOs, route registration with policies
    jobs/                          # River workers, event consumers, periodic job definitions
    events/                        # producer payload types + testdata/<event>.v<N>.json fixtures
    <module>test/                  # fakes and builders for other modules' tests (_test.go import only)
  psp/adapters/{sandbox,<provider>}/      # provider adapters (anti-corruption layer)
  kyc/vendor/{manual,<vendor>}/           # KYC vendor adapters
  compliance/screening/{<vendor>}/        # screening vendor adapters
  notifications/channels/{sms,email}/     # delivery channels
  storage/{objectstore,scanner}/          # S3-compatible client, ClamAV client
tests/
  architecture/                     # import graph, query ownership, route policy, event contracts (dependency-rules §5)
  integration/                      # real PostgreSQL with real roles (Stage 3+)
api/openapi/fundzim-v1.yaml
migrations/                         # goose (Stage 3+)
```

### 1.1 Why not `internal/<m>/internal/...`

Go's `internal` rule would make `store`, `service`, `http` and `jobs` unimportable by other modules at
compile time, which is attractive. It would also make them unimportable by `internal/app`, which must wire
handlers, workers and constructors. Exporting constructors through the root package would put
implementation dependencies (sqlc, pgx) into every importer's build graph. The architecture test D2
(dependency-rules §2) gives the same guarantee with one documented exception (`internal/app`), so the flat
layout is used.

## 2. Conventions shared by every module

### 2.1 Root package contents

| Item | Example | Rule |
|---|---|---|
| Service interfaces | `payments.Service`, `payments.Refunds` | Small, role-based interfaces; one module may expose several |
| Command and result types | `payments.CreateDonationCmd` | Explicit fields; money as `money.Money`; no pgx or HTTP types |
| Typed IDs | `type ID ids.UUID` | Owned by the module |
| Status enums | `type IntentStatus string` with constants | Must equal the SQL `CHECK` set and `app.status_transitions` rows (`TestStateEdgesMatchSQL`) |
| Error codes | `const CodeCampaignNotFound errs.Code = "CAMPAIGN_NOT_FOUND"` | Declared once; registry in [error-model.md](../api/error-model.md) |
| Permissions | `const PermCampaignReview actor.Permission = "campaign.review"` | Each module exports `Permissions() []actor.Permission`; `internal/app` passes the union to `auth` at start-up (auth imports no domain module) and a test asserts it equals the `permissions` reference rows; matrix in [authorization-matrix.md](../api/authorization-matrix.md) |
| Event names | `const EventPaymentSucceeded = "payment.succeeded"` | Payload types live in `events/`, not in the root |

### 2.2 Method shapes

```go
// Sketch, not compiled.
// (a) Self-contained operation: opens its own transaction on the module's pool.
CreateDonation(ctx context.Context, cmd CreateDonationCmd) (DonationResult, error)

// (b) Tx-joining operation: runs inside the caller's transaction (P1 in system-overview §2).
//     Always takes db.Tx as the second parameter and never commits or rolls back.
Post(ctx context.Context, tx db.Tx, p Posting) (TxID, error)

// (c) Read used as a decision input outside the caller's transaction (P2).
LevelAndStatus(ctx context.Context, subject SubjectRef) (LevelStatus, error)
```

- Authorisation happens **inside** the service, first: `actor.Require(ctx, Perm...)` for permissions, then an
  ownership/membership check scoped in the repository query (`WHERE owner_user_id = $actor`). System actors
  (`actor.System("payments.status_poll")`) carry an explicit permission set per job kind.
- Every mutating method writes its audit event and outbox events in the same transaction.
- Errors returned across module boundaries are `*errs.Error` with a code owned by the callee; callers may
  wrap them but must not re-code them unless they translate to their own code deliberately (for example
  `payouts` turns a `kyc` error into `PAYOUT_NOT_ELIGIBLE`).
- Context carries: principal, request ID, correlation ID, justification (staff), clock is injected (not on
  context).

### 2.3 Store (repository) pattern

```go
// Sketch, not compiled. internal/payments/store/store.go
type Store struct{ q *sqlcgen.Queries }

func New(q db.Querier) *Store {
    if tx, ok := q.(db.Tx); ok && tx.Pool() != db.PoolApp {
        panic("payments store used on non-app pool") // R4; caught by tests long before production
    }
    return &Store{q: sqlcgen.New(q)}
}
```

Queries are hand-written SQL in `store/queries/*.sql`, compiled by sqlc (ADR-031). Each query file
references only the module's own tables (`TestQueryTableOwnership`). Locking reads are explicit
(`-- name: LockIntent :one` with `FOR UPDATE`). Pagination is keyset (cursor) only.

## 3. Platform (shared kernel)

### 3.1 Sub-packages and internal import direction

| Package | Purpose | May import (platform-internal) |
|---|---|---|
| `clock` | `Clock` interface, real UTC clock, fake for tests | — |
| `ids` | UUIDv7, `public_code` (10-char Crockford base32), receipt numbers | `clock` |
| `errs` | Coded domain error type, kinds | — |
| `money` | `Money`, `Currency`, arithmetic with currency checks, allocation, basis points | `errs` |
| `market` | Currency and market registry (`app.currencies`, `app.markets`) | `db`, `money` |
| `config` | Typed config, env parsing, redacting `LogValue` | — |
| `log` | `slog` JSON with field allow-list and redaction | `config` |
| `crypto` | Keyring by key class, envelope encryption, HMAC, blind index | `config`, `errs` |
| `db` | Pools, `Tx`, `WithTx`, retry, pool identities | `config`, `errs`, `log` |
| `actor` | Principal, system actors, permission type, `Require` | `errs` |
| `outbox` | Event envelope, `Writer.Append`, `Consume` (inbox dedupe), dispatcher | `db`, `ids`, `clock`, `jobs` |
| `jobs` | River wrapper: enqueue in tx, register workers, periodic jobs, system actor per kind | `db`, `actor`, `log` |
| `idempotency` | Client idempotency keys (`app.idempotency_keys`) | `db`, `errs`, `clock` |
| `makerchecker` | SoD and approval-validity checks (code only, no table) | `actor`, `errs`, `clock` |
| `blob` | Opaque object reference (`Ref{BucketClass, Key, SHA256, Size, ContentType}`) | — |
| `featureflags` | Audited flags (`app.feature_flags`) | `db` |
| `httpclient` | Outbound HTTP with deadlines, SSRF guard, tx-open guard, redacted logging | `db` (to detect open tx), `log` |
| `httpx` | Router helpers, envelope, error rendering, strict JSON, middleware, `Authenticator` port, route policy | `errs`, `actor`, `log`, `idempotency`, `ids` |
| `testkit` | Test DB, fake clock, builders (test-only) | any |

Platform-owned tables: `app.currencies`, `app.markets`, `app.idempotency_keys`, `app.outbox_events`,
`app.inbox_events`, `app.feature_flags`, `app.feature_flag_changes` (maker-checker for flags marked `requires_approval`, I-3), and the River tables in `queue` (baseline §5.1).

### 3.2 `db`

```go
// Sketch, not compiled.
package db

type PoolID uint8
const (PoolApp PoolID = iota + 1; PoolKYC; PoolCompliance)

type Querier interface { // pgx-compatible subset used by sqlc
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
type Tx interface { Querier; Pool() PoolID }

// Distinct handle types so that wiring the wrong pool does not compile (D10).
type AppPool struct{ p *pgxpool.Pool }
type KYCPool struct{ p *pgxpool.Pool }
type CompliancePool struct{ p *pgxpool.Pool }
type Pool interface{ AppPool | KYCPool | CompliancePool }

type TxOptions struct {
    Name       string        // for metrics/tracing, e.g. "payments.capture"
    Isolation  Isolation     // default ReadCommitted
    ReadOnly   bool
    MaxRetries int           // default 3; retried on SQLSTATE 40001 and 40P01 only
}

// WithTx runs fn in a transaction, retrying the whole closure on serialization failure or deadlock.
// fn must not perform external I/O (R5): httpclient panics in tests if called while a tx is on ctx.
func WithTx[P Pool](ctx context.Context, p P, o TxOptions, fn func(ctx context.Context, tx Tx) error) error
```

### 3.3 `money`, `errs`, `actor`

```go
// Sketch, not compiled.
package money
type Currency string                       // ISO 4217, validated against market registry
type Money struct{ minor int64; cur Currency }
func New(minor int64, c Currency) (Money, error)        // rejects unknown currency
func (m Money) Add(o Money) (Money, error)               // ErrCurrencyMismatch, overflow checked
func (m Money) Sub(o Money) (Money, error)
func (m Money) Cmp(o Money) (int, error)                 // never compares across currencies
func (m Money) Allocate(weights ...int64) ([]Money, error) // largest remainder (MONEY.md)
func (m Money) ApplyBasisPoints(bp int64, r Rounding) (Money, error)
// JSON: {"amount_minor":"10000","currency":"USD"} — amount_minor is a string.

package errs
type Code string
type Kind uint8 // Invalid, Unauthenticated, Forbidden, NotFound, Conflict, Unprocessable, RateLimited, Unavailable, Internal
type Error struct { Code Code; Kind Kind; Message string; Details []FieldError; Category string; cause error }
func E(code Code, kind Kind, msg string, opts ...Option) *Error

package actor
type Kind string // USER, STAFF, SYSTEM
type Permission string
type Principal struct {
    Kind        Kind
    UserID      ids.UUID
    PersonID    ids.UUID          // staff: verified person (maker-checker compares this, T-44)
    SessionID   ids.UUID
    Permissions PermissionSet     // resolved by auth at authentication time
    MFAAt, StepUpAt time.Time
    SystemJob   string            // SYSTEM only
}
func FromContext(ctx context.Context) (Principal, bool)
func Require(ctx context.Context, p Permission) error   // FORBIDDEN if missing
func System(job string, perms ...Permission) Principal
```

### 3.4 `outbox`, `jobs`, `idempotency`, `makerchecker`, `crypto`

```go
// Sketch, not compiled.
package outbox
type Event struct {
    Name string; Version int
    AggregateType string; AggregateID ids.UUID
    Payload any                    // marshalled to JSON; field allow-list checked in tests (R11)
}
type Writer interface { Append(ctx context.Context, tx db.Tx, evts ...Event) error }
type Delivered struct { EventID ids.UUID; Name string; Version int; Payload json.RawMessage; /* envelope */ }
// Consume records (consumer, event_id) in app.inbox_events inside tx and runs fn once.
func Consume(ctx context.Context, tx db.Tx, consumer string, d Delivered, fn func(ctx context.Context, tx db.Tx) error) error

package jobs
type Client interface {
    Enqueue(ctx context.Context, tx db.Tx, args Args, opts ...Opt) error // transactional enqueue (River InsertTx)
}
func Register[A Args](reg *Registry, w Worker[A], o WorkerOptions)        // queue, max attempts, timeout, system perms

package idempotency
type Claim struct{ Status ClaimStatus; StoredResponse []byte }
type Store interface {
    Claim(ctx context.Context, tx db.Tx, scope, key string, requestHash [32]byte) (Claim, error)
    Complete(ctx context.Context, tx db.Tx, scope, key string, status int, body []byte) error
}

package makerchecker
type Request struct { MakerPersonIDs []ids.UUID; CreatedAt, ExpiresAt time.Time; PayloadHash [32]byte }
// ValidateApproval enforces: checker ≠ every maker/initiator, not expired, step-up fresh,
// distinct from earlier approvers (DUAL), and no declared conflict (conflicts supplied by caller).
func ValidateApproval(req Request, checker actor.Principal, prior []ids.UUID, conflicts []ids.UUID, now time.Time, stepUpMax time.Duration) error
// Codes: APPROVER_CONFLICT, APPROVAL_EXPIRED, STEP_UP_REQUIRED, APPROVER_ALREADY_APPROVED

package crypto
type KeyClass string // "kyc-identity", "kyc-document", "payout-destination", "otp-hmac", "session-hmac", "blind-index:<field>"
type Sealed struct{ Ciphertext []byte; KeyID string }
type Keyring interface {
    Seal(ctx context.Context, c KeyClass, plaintext, aad []byte) (Sealed, error)
    Open(ctx context.Context, c KeyClass, s Sealed, aad []byte) ([]byte, error)
    BlindIndex(c KeyClass, normalised []byte) []byte
    MAC(c KeyClass, msg []byte) []byte
}
```

Key classes are bound to modules at wiring time: only `kyc` receives a keyring view that can open
`kyc-*` classes; only `payouts` can open `payout-destination`; `auth` gets `otp-hmac` and `session-hmac`.

**Platform error codes** (generic, rendered by `httpx`): `MALFORMED_REQUEST`, `VALIDATION_FAILED`,
`FORBIDDEN`, `NOT_FOUND`, `CONCURRENT_MODIFICATION`, `RATE_LIMITED`, `PAYLOAD_TOO_LARGE`,
`UNSUPPORTED_MEDIA_TYPE`, `IDEMPOTENCY_KEY_REQUIRED`, `IDEMPOTENCY_KEY_REUSED`,
`IDEMPOTENCY_REQUEST_IN_PROGRESS`, `CURRENCY_MISMATCH`, `CURRENCY_NOT_SUPPORTED`, `APPROVER_CONFLICT`,
`APPROVAL_EXPIRED`, `APPROVER_ALREADY_APPROVED`, `JUSTIFICATION_REQUIRED`, `FEATURE_DISABLED`,
`INTERNAL_ERROR`, `SERVICE_UNAVAILABLE`.

**Tests:** property tests for `money` (no overflow, allocation sums exactly, currency mismatch always errors);
`WithTx` retry tests against real PostgreSQL forcing 40001/40P01; redaction tests for `log` and `config`;
`httpx` strict decoding (unknown fields rejected); `idempotency` replay/conflict/in-flight cases;
`makerchecker` table tests for every SoD rule; outbox dispatcher crash-between-steps tests.

## 4. Module template

Every module section below uses the same subsections. "Tables" are exactly baseline §5. Event names follow
[dependency-rules.md §4.2](dependency-rules.md). Error codes are the codes the module **owns**; HTTP status
mapping and wording are in [error-model.md](../api/error-model.md). Permission names follow
[operational-controls.md §1](../compliance/operational-controls.md) and SECURITY §5; names marked *(new)* are
proposed here and must be confirmed in [authorization-matrix.md](../api/authorization-matrix.md).

## 5. Modules

### 5.1 `platform`

Specified in §3. Layer 0; imports nothing under `internal/`. Owns the platform tables listed in §3.1.

---

### 5.2 `audit` (layer 1)

**Responsibilities.** Record audit events (financial/business chain) and security audit events (separate hash
chain); record evidence metadata (`storage_ref` + SHA-256 + retention class) and two-person evidence holds;
serve scoped audit queries to authorised staff; verify hash chains. The action catalogue is a closed constant
set ([audit-evidence-model.md §4](../compliance/audit-evidence-model.md)).

**Tables** (schema `audit`): `audit_events`, `security_audit_events`, `evidence_records`, `evidence_holds`.

**Public interface (sketch, not compiled).**

```go
package audit

type Action string   // generated constants from the catalogue; unknown actions fail TestEveryAuditActionDeclared
type Outcome string  // SUCCESS, DENIED, FAILED

type Event struct {
    Action        Action
    TargetType    string
    TargetID      ids.UUID
    Outcome       Outcome
    Reason        string          // required for staff actions
    OnBehalfOf    *ids.UUID
    EvidenceIDs   []EvidenceID
    LedgerTxIDs   []ids.UUID
    Metadata      map[string]string // allow-listed keys only; never C3/C4 (audit-evidence-model §5)
}
// Actor, request_id, correlation_id and justification are taken from ctx.

type Recorder interface {
    Record(ctx context.Context, tx db.Tx, e Event) (ids.UUID, error)
    RecordSecurity(ctx context.Context, tx db.Tx, e SecurityEvent) (ids.UUID, error)
}

type EvidenceID ids.UUID
type EvidenceInput struct {
    Kind           string        // e.g. DISPUTE_EVIDENCE_PACK, SETTLEMENT_STATEMENT, KYC_DOCUMENT_REF
    Object         blob.Ref      // stored by the caller (kyc or storage) before the tx
    SubjectType    string
    SubjectID      ids.UUID
    RetentionClass string        // periods pending LR-012
}
type Evidence interface {
    Record(ctx context.Context, tx db.Tx, in EvidenceInput) (EvidenceID, error)
    RequestHold(ctx context.Context, tx db.Tx, id EvidenceID, reason string) (HoldID, error) // maker
    ApproveHold(ctx context.Context, holdID HoldID, justification string) error             // checker ≠ maker
    ReleaseHold(ctx context.Context, holdID HoldID, justification string) error             // two-person
}

type Reader interface {
    Search(ctx context.Context, q Query) (Page[EventView], error)       // scope by permission
    VerifyChain(ctx context.Context, c Chain, from, to int64) (ChainReport, error)
}
```

**Repositories.** `audit/store`: insert-only queries for events and evidence; `UPDATE` only on
`evidence_records.legal_hold` (baseline §4); chain head read. Must accept a `db.Tx` from **any** pool
(app, kyc, compliance), because every business transaction records its own audit event. From the kyc and
compliance pools the write goes through the `SECURITY DEFINER` functions `audit.append_event` /
`audit.append_security_event` (baseline §12 I-13), which `Recorder` selects by `tx.Pool()`.

**External dependencies.** Imports `platform`. No external systems. Object bytes are never handled here.

**Events.** Produces none (audit is a sink; alerting on audit patterns is done by observability rules).
Consumes none.

**Error codes.** `AUDIT_ACTION_UNKNOWN` (programming error, 500), `EVIDENCE_NOT_FOUND`,
`EVIDENCE_HASH_MISMATCH`, `EVIDENCE_HOLD_ACTIVE` (deletion/retention attempt on held evidence),
`AUDIT_CHAIN_BROKEN` (operator tooling only).

**Authorisation.** Writes: any authenticated service call (no permission; the caller's action is what is
authorised). Reads: `audit.read` scoped (COMPLIANCE), financial scope (FINANCE), security events only
(SECURITY_ADMIN), full (SUPER_ADMIN) — SECURITY §5.2. `evidence.hold` for holds (COMPLIANCE), checker
distinct.

**Transaction boundaries.** Always joins the caller's transaction (`Record`, `RecordSecurity`,
`Evidence.Record`). Hash-chain insertion must **not** serialise every business transaction behind one lock;
the chain mechanism (partitioned chains or a sealing job) is specified in
[audit-notification-schema.md](../database/audit-notification-schema.md).

**Test strategy.** Chain verification detects modified, deleted and reordered rows; append-only enforcement
(UPDATE/DELETE/TRUNCATE fail under `fundzim_app`, `fundzim_kyc`, `fundzim_compliance`); metadata allow-list
rejects deny-listed keys; concurrency test: 50 parallel business transactions record events without
deadlock or chain fork.

---

### 5.3 `users` (layer 2)

**Responsibilities.** User and staff accounts (`account_kind`), profiles, verified emails and phone numbers,
account status (active, suspended, closed), the read-only verification **mirror** (projection fed by `kyc`
events), staff person binding.

**Tables** (schema `app`): `users`, `user_profiles`, `user_emails`, `user_phone_numbers`.

**Public interface (sketch, not compiled).**

```go
package users

type ID ids.UUID
type AccountKind string // USER, STAFF
type Status string      // ACTIVE, SUSPENDED, CLOSED

type User struct {
    ID ID; Kind AccountKind; Status Status
    DisplayName string
    VerificationMirror Mirror // level + status copy; display/filter only (domain-boundaries §3.1)
}

type Service interface {
    Get(ctx context.Context, id ID) (User, error)                              // self or permitted staff
    GetMany(ctx context.Context, ids []ID) ([]UserSummary, error)              // non-sensitive summaries
    Register(ctx context.Context, cmd RegisterCmd) (User, error)               // after OTP verification by auth
    AddContact(ctx context.Context, tx db.Tx, cmd AddContactCmd) (ContactID, error) // joins auth's verify tx
    ChangePrimaryPhone(ctx context.Context, cmd ChangePhoneCmd) error          // step-up; emits user.contact_changed
    UpdateProfile(ctx context.Context, cmd UpdateProfileCmd) error
    Suspend(ctx context.Context, cmd SuspendCmd) error                         // staff
    ResolveByVerifiedContact(ctx context.Context, c ContactRef) (ID, error)    // auth login lookup
    IsActive(ctx context.Context, id ID) (bool, error)                         // EC-02
}
```

**Repositories.** `users/store`: user CRUD scoped by id; contact lookups by normalised value (unique per
verified contact); projection upsert guarded by `source_version`.

**External dependencies.** platform, audit. No external systems.

**Events.** Produces `user.registered`, `user.contact_changed`, `user.status_changed`. Consumes
`kyc.level_changed`, `kyc.status_changed`, `kyc.profile_snapshot` (mirror update; job
`users.on_kyc_level_changed`).

**Error codes.** `USER_NOT_FOUND`, `USER_SUSPENDED`, `CONTACT_ALREADY_IN_USE`, `CONTACT_NOT_VERIFIED`,
`PRIMARY_CONTACT_REQUIRED`.

**Authorisation.** Self for own profile and contacts; `user.view` (masked) for SUPPORT;
`account.recovery.assist` (step-up gated) for support-mediated recovery; `staff.suspend` (SECURITY_ADMIN) for
staff accounts. Contact values are masked in staff DTOs by default.

**Transaction boundaries.** Registration and contact changes are one tx each (row + audit + outbox).
`AddContact` joins `auth`'s OTP-verification tx so a verified contact and its OTP consumption commit
together. Mirror updates are consumer txs (TX-27).

**Test strategy.** Contact uniqueness under concurrent registration; projection ignores out-of-order
versions; staff DTO masking; IDOR tests on profile routes.

---

### 5.4 `storage` (layer 2)

**Responsibilities.** Upload sessions and presigned PUTs to a quarantine prefix; magic-byte sniffing, size
limits; malware scan orchestration; promotion to the final location; image re-encoding and variants for
public media (EXIF/GPS stripped); presigned GETs (≤ 5 min) after the caller's permission check; orphan
cleanup. Serves `public-media` and the `compliance/` and `reports/` prefixes (credential `private-evidence`).
**Never** holds a credential for the `kyc/` prefix (that is `kyc`'s own `private-kyc` client).

**Tables** (schema `app`): `stored_objects`, `upload_sessions`.

**Public interface (sketch, not compiled).**

```go
package storage

type Purpose string // CAMPAIGN_MEDIA, CAMPAIGN_UPDATE_MEDIA, KYC_DOCUMENT (delegated to kyc), EVIDENCE_COMPLIANCE, STATEMENT_FILE
type ObjectID ids.UUID
type ObjectState string // PENDING_UPLOAD, QUARANTINED, CLEAN, REJECTED, PROMOTED, DELETED

type Service interface {
    CreateUploadSession(ctx context.Context, cmd CreateUploadCmd) (UploadSession, error) // presigned PUT, limits by purpose
    CompleteUpload(ctx context.Context, sessionID ids.UUID) (ObjectID, error)            // TX-28, enqueues scan
    Get(ctx context.Context, id ObjectID) (Object, error)                                // metadata incl. blob.Ref when PROMOTED
    PresignGet(ctx context.Context, id ObjectID, ttl time.Duration) (URL, error)         // caller authorises first
    Open(ctx context.Context, ref blob.Ref) (io.ReadCloser, error)                       // server-side read (e.g. reconciliation statement import)
    PutPrivate(ctx context.Context, cmd PutPrivateCmd) (blob.Ref, error)                 // server-generated evidence (e.g. dispute packs) to compliance/ prefix
}
```

Object-store credentials (baseline §12 I-19): **three**. `public-media` (public bucket) and
`private-evidence` (`compliance/` and `reports/` prefixes of the private bucket) are held by `storage`;
`private-kyc` (`kyc/` prefix, dedicated KMS key) is built by `internal/app` and passed **only** to `kyc`.
Neither private credential can read the other's prefix, and `storage` never holds a credential that can read
`kyc/`. KYC uploads: `kyc` calls `storage.Service.CreateUploadSession` with
`Purpose=KYC_DOCUMENT` and its own client (`WithObjectStore(kycClient)`), so the pipeline (sniffing, scan,
promotion) is shared but `storage` never holds `kyc/` credentials. `stored_objects` keeps only C2 metadata
(state, size, sniffed type, purpose); the C3 link from document to person lives in `kyc.kyc_documents`.

**Repositories.** `storage/store`: session and object state transitions with `version`; orphan scan query.
**Sub-packages:** `objectstore` (S3-compatible client per bucket class), `scanner` (ClamAV over the internal
network), `imaging` (re-encode, variants).

**External dependencies.** platform, audit. Object storage, malware scanner.

Importers (baseline §3): `kyc`, `campaigns`, `compliance`, `payments`, `payouts`, `reconciliation`. The last four
store evidence or statement objects in the `compliance/` prefix and then register them with
`audit.Evidence.Record`.

**Events.** Produces `storage.object_promoted`, `storage.object_rejected`. Consumes none.

**Error codes.** `UPLOAD_SESSION_NOT_FOUND`, `UPLOAD_SESSION_EXPIRED`, `UPLOAD_TOO_LARGE`,
`UPLOAD_TYPE_NOT_ALLOWED`, `UPLOAD_INCOMPLETE`, `OBJECT_NOT_FOUND`, `OBJECT_NOT_READY`, `OBJECT_REJECTED`.

**Authorisation.** Upload sessions are created for the authenticated principal and bound to a purpose and a
target (campaign id, case id); the owning module validates the target before asking for a session. Presigned
GETs only after the calling module's own permission check; `storage` re-checks the object's purpose against
the requested access class.

**Transaction boundaries.** TX-28. Object writes and scans happen between transactions; a crash leaves an
object without committed metadata, removed by `storage.cleanup_orphans`.

**Test strategy.** Magic-byte vs declared type mismatch; decompression-bomb and polyglot fixtures; scanner
down keeps objects quarantined; promotion is idempotent; public URL never issued for a non-promoted object.

---

### 5.5 `ledger` (layer 2)

**Responsibilities.** Double-entry ledger per [LEDGER.md](../LEDGER.md) and
[settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md): lazily created per-currency
accounts, balanced single-currency journals created **only** by named posting rules or approved
adjustments, balance projections in the same transaction, invariant runs (L1–L10, SC-1–SC-6), pool
integrity, periods. Domain-agnostic: owners are `(owner_type, owner_id)` without foreign keys.

**Tables** (schema `ledger`): `ledger_accounts`, `ledger_transactions`, `ledger_entries`, `ledger_balances`,
`ledger_posting_rules`, `ledger_posting_rule_lines`, `ledger_posting_batches`, `ledger_adjustments`, `ledger_periods`,
`ledger_invariant_runs`.

**Public interface (sketch, not compiled).** Full rule catalogue and keys:
[ledger-posting-model.md](ledger-posting-model.md).

```go
package ledger

type TxID ids.UUID
type AccountRef struct { Code string; OwnerType string; OwnerID ids.UUID; Currency money.Currency } // e.g. campaign_payable
type Source struct { Type string; ID ids.UUID; Provider string; ProviderReference string }

// Named posting rules. Each validates its own invariants (e.g. SC-1: reservation debits campaign_payable only)
// and is idempotent on its key; a key replay with different lines returns ErrIdempotencyConflict + alert.
type Poster interface {
    PostDonationCaptured(ctx context.Context, tx db.Tx, p DonationCaptured) (TxID, error)   // T1 payment:{id}:capture
    PostPSPFee(ctx context.Context, tx db.Tx, p PSPFee) (TxID, error)                       // T2
    PostSettlementMatched(ctx context.Context, tx db.Tx, p SettlementMatched) (TxID, error)  // T3 / T3′
    PostRelease(ctx context.Context, tx db.Tx, p Release) (TxID, error)                     // T4 / T4r (target per campaign state)
    PostPayoutReserved(ctx context.Context, tx db.Tx, p PayoutMove) (TxID, error)           // Y1
    PostPayoutReleased(ctx context.Context, tx db.Tx, p PayoutMove) (TxID, error)           // Y4/Y5
    PostPayoutSubmitted(ctx context.Context, tx db.Tx, p PayoutMove) (TxID, error)          // Y8
    PostPayoutCompleted(ctx context.Context, tx db.Tx, p PayoutMove) (TxID, error)          // Y10
    PostPayoutFailed(ctx context.Context, tx db.Tx, p PayoutMove) (TxID, error)             // Y11
    PostPayoutReversed(ctx context.Context, tx db.Tx, p PayoutMove) (TxID, error)           // Y14
    PostRefund(ctx context.Context, tx db.Tx, p RefundPosting) (TxID, error)                // reserve/settle/release/fund_shortfall
    PostDispute(ctx context.Context, tx db.Tx, p DisputePosting) (TxID, error)              // open/hold/won/lost/fund_shortfall
    PostRecovery(ctx context.Context, tx db.Tx, p RecoveryPosting) (TxID, error)            // receive/setoff/write_off
    PostCampaignFreeze(ctx context.Context, tx db.Tx, p FreezeMove) (TxID, error)           // campaign:{id}:freeze:{n} / unfreeze
    PostHoldApplied(ctx context.Context, tx db.Tx, p HoldMove) (TxID, error)                // hold:{id}:applied / released
    PostFeeRemittance(ctx context.Context, tx db.Tx, p FeeRemittance) (TxID, error)
}

type Balances interface {
    // Locks the listed accounts' balance rows in ascending account-id order (FOR UPDATE) and returns them.
    LockCampaignBalances(ctx context.Context, tx db.Tx, campaignID ids.UUID, cur money.Currency) (CampaignBalances, error)
    CampaignBalances(ctx context.Context, campaignID ids.UUID, cur money.Currency) (CampaignBalances, error) // display
    HasSettlementFor(ctx context.Context, tx db.Tx, paymentID ids.UUID) (bool, error)  // T3 exists (release guard)
    CheckPoolIntegrity(ctx context.Context, tx db.Tx, provider string, cur money.Currency) (PoolIntegrity, error) // SC-2, EC-19
}

type Adjustments interface { // maker-checker
    Propose(ctx context.Context, cmd ProposeAdjustmentCmd) (AdjustmentID, error) // ledger.adjustment.create
    Approve(ctx context.Context, id AdjustmentID, justification string) (TxID, error) // ledger.adjustment.approve, checker ≠ maker; TX-20
    Reject(ctx context.Context, id AdjustmentID, reason string) error
}

type Invariants interface {
    Run(ctx context.Context, scope InvariantScope) (RunID, error)  // nightly recompute; writes ledger_invariant_runs
}
```

**Repositories.** `ledger/store`: account get-or-create (`ON CONFLICT DO NOTHING` + select), journal insert
with entries, balance projection update (the only `UPDATE` grant in the schema), idempotency-key lookup with
line comparison, recompute queries. Deferred constraint trigger enforces balance per currency at commit.

**External dependencies.** platform, audit. None external.

**Events.** Produces `ledger.invariant_failed`, `ledger.pool_integrity_breached`,
`ledger.adjustment_approved`. Consumes none.

**Error codes.** `LEDGER_UNBALANCED` (500 + SEV1: should be impossible from rules), `LEDGER_CURRENCY_MIXED`,
`LEDGER_IDEMPOTENCY_CONFLICT` (SEV1), `LEDGER_RULE_NOT_ALLOWED` (e.g. SC-1, SC-5 allow-lists),
`LEDGER_INSUFFICIENT_AVAILABLE` (reservation exceeds `campaign_payable`), `LEDGER_PERIOD_CLOSED`,
`LEDGER_ADJUSTMENT_NOT_FOUND`, `LEDGER_ADJUSTMENT_INVALID_STATE`.

**Authorisation.** Posting rules: no end-user permission (they are invoked by owning services inside
authorised operations; only system or service context). Adjustments: `ledger.adjustment.create`,
`ledger.adjustment.approve` (FINANCE, different person). Balance reads for display: through the owning
module (campaigns/payouts), never directly by HTTP.

**Transaction boundaries.** Every posting joins the caller's tx (P1). `Adjustments.Approve` opens its own
(TX-20). Lock order: balance rows ascending by account id, after the caller's aggregate row lock
([ledger-posting-model.md](ledger-posting-model.md) is authoritative).

**Test strategy.** Property tests: every rule balances per currency for random amounts; idempotent replay
returns the same id; replay with different lines fails; SC-1/SC-5 rejections; concurrency: N parallel
reservations never drive `campaign_payable` negative (real PostgreSQL, Stage 3); full-recompute equals
projection after randomized workloads; append-only enforced by grants and triggers.

---

### 5.6 `fees` (layer 2)

**Responsibilities.** Versioned fee schedules (platform fee in basis points, fee bearer, minimums per
currency), maker-checker for changes, effective-dated activation (never retroactive), pure fee calculation
and preview. **Never posts.**

**Tables** (schema `app`): `fee_schedules`, `fee_schedule_versions`, `fee_schedule_change_requests`.

**Public interface (sketch, not compiled).**

```go
package fees

type ScheduleVersion string
type Quote struct {
    Gross        money.Money
    PlatformFee  money.Money
    Bearer       Bearer           // CAMPAIGN | DONOR (PD-01/PD-19 as data)
    Version      ScheduleVersion  // recorded on the capture journal
}
type Calculator interface {
    QuoteDonation(ctx context.Context, in DonationFeeInput) (Quote, error)   // amount, currency, method, category, at
    QuotePayout(ctx context.Context, in PayoutFeeInput) (PayoutQuote, error)
}
type Admin interface {
    ProposeChange(ctx context.Context, cmd ProposeFeeChangeCmd) (ChangeRequestID, error) // fee.config.request (FINANCE)
    ApproveChange(ctx context.Context, id ChangeRequestID, justification string) error   // business owner role; TX-29
    List(ctx context.Context, q ListQuery) ([]ScheduleVersionView, error)
}
```

**Repositories.** `fees/store`: effective-version lookup by `(market, currency, method, category, at)`,
change-request workflow.

**External dependencies.** platform, audit.

**Events.** Produces `fees.schedule_version_activated` (informational; no consumers required). Consumes none.

**Error codes.** `FEE_SCHEDULE_NOT_CONFIGURED` (fails closed: no donation without an effective schedule),
`FEE_CHANGE_RETROACTIVE`, `FEE_CHANGE_REQUEST_NOT_FOUND`, `FEE_CHANGE_INVALID_STATE`.

**Authorisation.** Calculation: internal callers only (no HTTP route). Fee preview for donors is exposed by
`payments`/`campaigns` routes. Changes: `fee.config.request` (maker, FINANCE), approval by the business-owner
role *(permission `fee.config.approve`, new)*.

**Transaction boundaries.** Calculation is read-only, outside or inside the caller's tx (it only reads
versions). TX-29 for activation.

**Test strategy.** Rounding tests with basis points and largest remainder; boundary effective dates in UTC;
no version effective → error; checker ≠ maker; property: fee ≤ gross, never negative.

---

### 5.7 `psp` (layer 2)

**Responsibilities.** Provider registry and accounts (configuration with secret **references**), versioned
capability registry intersected with configuration, provider adapter interfaces and adapters (anti-corruption
layer, [domain-boundaries.md §5](domain-boundaries.md)), the sandbox provider, webhook verification and the
single provider webhook inbox, inbound classification and dispatch, provider health (circuit-breaker state,
settlement freshness — C-2). No business rules.

**Tables** (schema `app`): `payment_providers`, `provider_capabilities`, `provider_accounts`,
`provider_webhook_inbox`, `provider_health_events`.

**Public interface (sketch, not compiled).** Full adapter contracts:
[payment-provider-interfaces.md](payment-provider-interfaces.md) (authoritative; ADR-029).

```go
package psp

type ProviderID string // stable code, e.g. "sandbox"
type Kind string       // PAYMENT, REFUND, DISPUTE, PAYOUT, OTHER (= provider_webhook_inbox.event_class; reversals are DISPUTE class)

// Adapter interfaces implemented in psp/adapters/<p>; consumed by payments, payouts, reconciliation.
type PaymentProvider interface { CreatePayment(...); GetPayment(...); CancelPayment(...) }
type RefundProvider interface { RefundPayment(...); GetRefund(...) }
type PayoutProvider interface { CreatePayout(...); GetPayoutStatus(...); VerifyDestination(...) }
type ProviderWebhookVerifier interface { VerifyWebhook(...); ParseWebhook(...) }
type ProviderReconciliationSource interface { ListStatements(...); FetchStatement(...); PoolBalance(...) }

type Registry interface {
    Payments(id ProviderID) (PaymentProvider, error)      // ErrProviderDisabled, ErrNotSupported
    Refunds(id ProviderID) (RefundProvider, error)
    Payouts(id ProviderID) (PayoutProvider, error)
    Reconciliation(id ProviderID) (ProviderReconciliationSource, error)
    Capabilities(ctx context.Context, id ProviderID) (Capabilities, CapabilityVersion, error) // code ∩ config
    Candidates(ctx context.Context, q RouteQuery) ([]Candidate, error) // method × currency × amount; excludes MERCHANT_SETTLEMENT for donations
}

type Inbox interface {
    // HTTP path (TX-01): verify on raw bytes, insert row, enqueue psp.webhook_dispatch. No domain writes.
    Ingest(ctx context.Context, provider ProviderID, raw RawWebhook) (IngestResult, error)
    // Worker path: consumers claim and complete rows inside their own transaction.
    Claim(ctx context.Context, tx db.Tx, inboxID ids.UUID) (VerifiedEvent, error)
    MarkProcessed(ctx context.Context, tx db.Tx, inboxID ids.UUID) error
    MarkParked(ctx context.Context, tx db.Tx, inboxID ids.UUID, reason string, recheckAt time.Time) error
    MarkIgnored(ctx context.Context, tx db.Tx, inboxID ids.UUID, reason string) error // known-ignorable types only; unknown types alert
    // Wiring (internal/app): which job kind processes which Kind. psp names no consumer module.
    RegisterConsumer(kind Kind, enqueue func(ctx context.Context, tx db.Tx, inboxID ids.UUID) error)
}

type Health interface {
    RecordCallOutcome(ctx context.Context, p ProviderID, op string, outcome CallOutcome) // circuit breaker
    // C-2: needs new provider_health_events.event_kind values SETTLEMENT_RECONCILED, RECON_SEV1_OPENED, RECON_SEV1_CLOSED (0007 draft).
    RecordSettlementReconciled(ctx context.Context, tx db.Tx, p ProviderID, cur money.Currency, at time.Time) error // by reconciliation
    RecordSev1Discrepancy(ctx context.Context, tx db.Tx, p ProviderID, cur money.Currency, open bool) error
    RailStatus(ctx context.Context, p ProviderID, cur money.Currency) (RailStatus, error) // EC-18, EC-20 (fails closed)
}

type Admin interface { // provider enable/disable per method/currency (kill switch as data), capability versions
    SetEnabled(ctx context.Context, cmd SetProviderEnabledCmd) error
    PublishCapabilityVersion(ctx context.Context, cmd CapabilityVersionCmd) error
}
```

**Repositories.** `psp/store`: inbox insert `ON CONFLICT (provider, provider_event_id) DO NOTHING`, claim
`FOR UPDATE SKIP LOCKED`, status updates; capability versions; health events (append-only).

**External dependencies.** platform, audit. PSP APIs (adapters only), secret manager (credentials by
reference).

**Events.** Produces `psp.provider_disabled`, `psp.rail_degraded` (operations; consumed by notifications for
staff alerts — optional). Consumes none (it is called by reconciliation for health updates).

**Error codes.** `PROVIDER_NOT_FOUND`, `PROVIDER_DISABLED`, `PROVIDER_OPERATION_NOT_SUPPORTED`,
`PROVIDER_CUSTODY_MODEL_REFUSED`, `WEBHOOK_SIGNATURE_INVALID`, `WEBHOOK_REPLAY_WINDOW_EXCEEDED`,
`WEBHOOK_PAYLOAD_INVALID`, `INBOX_EVENT_NOT_FOUND`. Webhook errors are never shown in detail to callers
(401 with no body detail).

**Authorisation.** Webhook route: provider signature, no session. Admin operations:
`provider.config.change` *(new)* for FINANCE with maker-checker for enabling a provider or method in
production; `killswitch.activate` for disabling (SECURITY_ADMIN/FINANCE). Adapters receive no principal.

**Transaction boundaries.** TX-01 (ingest). Adapter calls are never inside a tx (R5). `Claim`/`Mark*` join
the consumer's tx so inbox completion commits with the domain change (TX-04).

**Test strategy.** Sandbox provider scripts every Stage 1 scenario (decline, timeout, duplicate webhook,
out-of-order, late success, refund and payout failures, every custody model and capability combination —
handover §7); signature tests with rotated secrets; replay window; body-size limits; adapter contract test
suite run against every adapter (same table of cases); routing refuses `MERCHANT_SETTLEMENT`; status mapping
of unknown strings → `UNKNOWN` + alert.

---

### 5.8 `risk` (layer 2, leaf)

**Responsibilities.** Limits registry (REGULATORY / PROVIDER / INTERNAL_RISK, versioned, maker-checker,
most-restrictive resolution, fail closed when unconfigured); **all holds** and their history; risk signals;
assessments (Stage 11 minimal rules, Stage 13 scored model behind the same interface); monitoring rules and
alerts. Places `PROVIDER_HOLD` in reaction to reconciliation and ledger events.

**Tables** (schema `risk`): `limits`, `limit_change_requests`, `holds`, `hold_events`, `hold_release_requests`
(maker-checker releases, I-3), `risk_signals`, `risk_assessments`, `monitoring_rules`, `monitoring_alerts`.

**Public interface (sketch, not compiled).**

```go
package risk

type HoldType string  // CAMPAIGN_FREEZE, COMPLIANCE_HOLD, PAYOUT_HOLD, DISPUTE_HOLD, DESTINATION_HOLD, RECOVERY_HOLD, ACCOUNT_HOLD, PROVIDER_HOLD
type ScopeType string // CAMPAIGN, USER, ORGANISATION, BENEFICIARY, PAYOUT_DESTINATION, PROVIDER_CURRENCY (type↔scope compatibility per 0008 draft)
type Scope struct { Type ScopeType; ID ids.UUID; Currency *money.Currency } // Currency only for PROVIDER_CURRENCY

type PlaceHoldCmd struct {
    Type HoldType; Scope Scope
    ReasonCode string; ReasonText string // internal note, no C3 (C3 goes to evidence); never exposed to owners
    Confidential bool                     // compliance-originated: owner DTOs show "under review" only
    Origin Origin                         // CASE, ALERT, DISPUTE, DESTINATION_CHANGE, RECOVERY_CASE, ... + origin id
    CaseRef *ids.UUID; ReviewBy time.Time
    LedgerTxID *ids.UUID                  // when the caller posted a journal in the same tx
}
type Holds interface {
    Place(ctx context.Context, tx db.Tx, cmd PlaceHoldCmd) (HoldID, error)                // joins caller tx
    Release(ctx context.Context, tx db.Tx, id HoldID, cmd ReleaseCmd) error               // authority per hold type
    Active(ctx context.Context, tx db.Tx, scopes []Scope, blocking Effect) ([]Hold, error) // EC-12; locks rows FOR SHARE
    ActiveForOwnerView(ctx context.Context, scopes []Scope) ([]OwnerHoldView, error)      // reason-less
}

type Limits interface {
    // Resolve returns every applicable limit by type and the most restrictive; Unconfigured=true fails closed.
    Resolve(ctx context.Context, key LimitKey, scope LimitScope, cur *money.Currency, at time.Time) (Resolution, error)
    ProposeChange(ctx context.Context, cmd ProposeLimitCmd) (ChangeRequestID, error)  // source citation required
    ApproveChange(ctx context.Context, id ChangeRequestID, justification string) error // TX-30; approval owner named on the limit
}

type Assessor interface { // risk hook (EC-16), Stage 11 minimal → Stage 13 scored
    AssessPayout(ctx context.Context, in PayoutRiskInput) (Assessment, error)
    AssessCampaign(ctx context.Context, in CampaignRiskInput) (Assessment, error)
    AssessDonation(ctx context.Context, in DonationRiskInput) (Assessment, error) // card testing, velocity
}

type Signals interface { Record(ctx context.Context, tx db.Tx, s Signal) error }
```

**Repositories.** `risk/store`: hold insert + `hold_events` append; active-hold query by scope set and type
mask; limit version resolution by `(key, scope, currency, at)`; alerts.

**External dependencies.** platform, audit. None external.

**Events.** Produces `risk.hold_placed`, `risk.hold_released`, `risk.alert_raised`. Consumes `user.*`,
`auth.login_succeeded`, `auth.otp_failed`, `kyc.*`, `campaign.*`, `payment.*`, `refund.*`, `dispute.*`,
`payout.*`, `payout_destination.changed`, `compliance.restriction_*`, `reconciliation.discrepancy_opened`
(SEV1 → `PROVIDER_HOLD`), `ledger.pool_integrity_breached` (→ `PROVIDER_HOLD`).

**Error codes.** `HOLD_NOT_FOUND`, `HOLD_ALREADY_RELEASED`, `HOLD_RELEASE_NOT_PERMITTED`,
`LIMIT_NOT_CONFIGURED`, `LIMIT_EXCEEDED`, `LIMIT_CHANGE_SOURCE_REQUIRED`, `LIMIT_CHANGE_INVALID_STATE`.

**Authorisation.** Hold placement: by the calling service under its own authorised operation, or by staff
with `payout.hold` (COMPLIANCE), `campaign.freeze` (via campaigns). Release authority per hold type
(payout-eligibility §5): e.g. `DESTINATION_HOLD` by system only after cooling-off and re-verification,
`COMPLIANCE_HOLD` with maker-checker, `PROVIDER_HOLD` by FINANCE. Limits: `limit.change.request` /
`limit.change.approve` *(new names)*, approval owner role recorded on the limit.

**Transaction boundaries.** `Holds.Place/Release` join the caller's tx (P1) — on the app pool, or on the
compliance pool for TX-22 (grants C-4). Limit approval TX-30. Consumers TX-27.

**Test strategy.** Every hold type × scope blocks exactly the documented operations; release authority
matrix; holds history append-only; most-restrictive limit resolution across types and scopes; unconfigured
and past-`review_by` limits disable AUTO; USD limit never applied to ZWG.

---

### 5.9 `auth` (layer 4)

**Responsibilities.** Credentials and authentication identities (phone OTP, email OTP, optional password,
WebAuthn/TOTP for staff), OTP challenges (hashed, purpose-bound), opaque server-side sessions (user and staff
kinds), MFA and recovery codes, roles/permissions/role assignments, maker-checker role grants with SoD
combination rules, permission evaluation into `actor.Principal`, CSRF, step-up, security telemetry, break-glass
grants (`break_glass_grants`), staff conflict declarations (`staff_conflict_declarations`). Implements
`platform/httpx.Authenticator`.

**Tables** (schema `app`): `authentication_identities`, `password_credentials`, `otp_challenges`, `sessions`,
`mfa_methods`, `recovery_codes`, `roles`, `permissions`, `role_permissions`, `role_assignments`,
`role_assignment_requests`, `break_glass_grants`, `staff_conflict_declarations`, `security_events`.

**Public interface (sketch, not compiled).** Details: [authentication-authorization.md](../security/authentication-authorization.md).

```go
package auth

type OTPPurpose string // LOGIN, VERIFY_PHONE, VERIFY_EMAIL, PAYOUT_DESTINATION_CHANGE, STEP_UP, ...

type Service interface {
    StartOTP(ctx context.Context, cmd StartOTPCmd) (ChallengeID, error)        // rate/budget limited; code delivered in memory via notifications.Transactional.SendNow
    VerifyOTP(ctx context.Context, cmd VerifyOTPCmd) (SessionGrant, error)     // single use, ≤5 attempts, purpose-bound
    StaffLogin(ctx context.Context, cmd StaffLoginCmd) (SessionGrant, error)    // WebAuthn/TOTP, no SMS
    StepUp(ctx context.Context, cmd StepUpCmd) error
    Logout(ctx context.Context) error
    RevokeSessions(ctx context.Context, cmd RevokeSessionsCmd) error           // session.revoke (SECURITY_ADMIN) or self
}

// Implements platform/httpx.Authenticator: session token → actor.Principal (permissions resolved per request).
type Authenticator interface {
    Authenticate(ctx context.Context, r SessionCookie, kind SessionKind) (actor.Principal, error)
    CheckCSRF(ctx context.Context, p actor.Principal, token string) error
}

type Roles interface {
    ProposeGrant(ctx context.Context, cmd ProposeGrantCmd) (RequestID, error)   // role.assign.request
    ApproveGrant(ctx context.Context, id RequestID, justification string) error // role.assign.approve; TX-23
    Revoke(ctx context.Context, cmd RevokeRoleCmd) error
    ListAssignments(ctx context.Context, q AssignmentQuery) ([]Assignment, error) // access reviews
}

type BreakGlass interface { // app.break_glass_grants (maker-checker, time-boxed)
    Start(ctx context.Context, cmd BreakGlassCmd) (GrantID, error) // one permission, ≤1h, incident ref, alerts
    End(ctx context.Context, id GrantID) error
}

type Conflicts interface { // app.staff_conflict_declarations; read by makerchecker callers (payouts, campaigns, kyc)
    Declare(ctx context.Context, cmd DeclareConflictCmd) error
    ConflictsFor(ctx context.Context, personID ids.UUID) ([]ConflictRef, error)
}
```

**Repositories.** `auth/store`: session lookup by token hash; challenge consume (`UPDATE … WHERE consumed_at
IS NULL AND attempts < 5 RETURNING`); role assignment resolution; SoD rule table (static in code, checked at
grant time).

**External dependencies.** platform, audit, users, notifications (`Transactional.SendNow` only, for OTP delivery outside any transaction; baseline §3, layer 4). WebAuthn library.

**Events.** Produces `auth.login_succeeded`, `auth.otp_failed`, `auth.session_revoked`, `auth.role_granted`,
`auth.role_revoked`, `auth.break_glass_started`. Consumes `user.contact_changed` (revoke challenges and
sessions bound to the old factor), `user.status_changed` (revoke sessions on suspension).

**Error codes.** `UNAUTHENTICATED`, `SESSION_EXPIRED`, `CSRF_FAILED`, `STEP_UP_REQUIRED`, `OTP_INVALID`,
`OTP_EXPIRED`, `OTP_ATTEMPTS_EXCEEDED`, `OTP_RATE_LIMITED`, `MFA_REQUIRED`, `MFA_INVALID`,
`SESSION_KIND_MISMATCH` (user session on staff route or vice versa — rendered as 401),
`ROLE_GRANT_SELF_NOT_ALLOWED`, `ROLE_COMBINATION_FORBIDDEN`, `ROLE_GRANT_REQUEST_NOT_FOUND`,
`BREAK_GLASS_NOT_PERMITTED`, `CONFLICT_DECLARATION_NOT_FOUND`.

**Authorisation.** Public for OTP start/verify (heavily rate-limited); self for own sessions; `role.assign.*`
SUPER_ADMIN with a different approver; `session.revoke`, `staff.suspend` SECURITY_ADMIN;
`access.review.run` SECURITY_ADMIN.

**Transaction boundaries.** OTP verify: consume challenge + create session + `users.AddContact` (joins) +
security event, one tx. Role grant TX-23. OTP delivery is outside any tx (after the challenge commits).

**Test strategy.** Brute-force and SMS-pumping tests (budgets per phone/IP/global); OTP purpose binding; session
fixation/rotation; staff session cannot reach user routes and vice versa; self-grant and SoD role combination
rejection; permission resolution snapshot per request; WebAuthn ceremony tests with test vectors.

---

### 5.10 `organisations` (layer 3)

**Responsibilities.** Organisations (profile, type, status), org roles (`ORG_ADMIN`, `ORG_MEMBER`),
membership, invitations, the KYB verification **projection** (`organisation_verifications`, display and
filtering only).

**Tables** (schema `app`): `organisations`, `organisation_roles`, `organisation_members`,
`organisation_invitations`, `organisation_verifications`.

**Public interface (sketch, not compiled).**

```go
package organisations

type ID ids.UUID
type Role string // ORG_ADMIN, ORG_MEMBER

type Service interface {
    Create(ctx context.Context, cmd CreateOrgCmd) (Organisation, error)     // creator becomes ORG_ADMIN; emits organisation.created
    Get(ctx context.Context, id ID) (Organisation, error)
    Invite(ctx context.Context, cmd InviteCmd) (InvitationID, error)        // ORG_ADMIN
    AcceptInvitation(ctx context.Context, token string) error
    ChangeMemberRole(ctx context.Context, cmd ChangeRoleCmd) error          // ORG_ADMIN; last admin cannot be removed
    RemoveMember(ctx context.Context, cmd RemoveMemberCmd) error
    MemberRole(ctx context.Context, org ID, user users.ID) (Role, bool, error) // authorisation input for campaigns/payouts (EC-02)
    VerificationDisplay(ctx context.Context, org ID) (VerificationView, error) // projection; NOT for decisions
}
```

**Repositories.** `organisations/store`: membership queries scoped by user; invitation tokens stored hashed;
projection upsert guarded by `source_version`.

**External dependencies.** platform, audit, users.

**Events.** Produces `organisation.created`, `organisation.member_changed`, `organisation.invitation_created`.
Consumes `kyb.level_changed`, `kyb.status_changed`, `kyc.profile_snapshot` (projection).

**Error codes.** `ORGANISATION_NOT_FOUND`, `ORGANISATION_ROLE_REQUIRED`, `ORGANISATION_LAST_ADMIN`,
`INVITATION_NOT_FOUND`, `INVITATION_EXPIRED`, `MEMBER_ALREADY_EXISTS`.

**Authorisation.** Org-scoped ABAC: `ORG_ADMIN` for membership and payout-destination management (the latter
enforced in `payouts` using `MemberRole`); members read. Staff: `org.view` *(new)*; KYB decisions are in `kyc`
(`org.verification.decide`).

**Transaction boundaries.** One tx per command (rows + audit + outbox). Projection updates TX-27.

**Test strategy.** Membership IDOR tests; last-admin protection; invitation token single use; projection
order-independence.

---

### 5.11 `notifications` (layer 3)

**Responsibilities.** Templates (versioned, per locale), notification jobs fanned out from events,
delivery attempts per channel (SMS, email; push/WhatsApp later with consent, LR-023), preferences and
suppressions, the synchronous transactional send used by `auth` for OTP delivery. Messages never contain C3 or
confidential reasons.

**Tables** (schema `app`): `notification_templates`, `notification_jobs`, `notification_attempts`,
`notification_preferences`, `notification_suppressions`.

**Public interface (sketch, not compiled).** Only `auth` imports `notifications` (for `SendNow`); everything
else reaches it through events. The admin handlers use `Admin` for template management.

```go
package notifications

type Channel string // SMS, EMAIL
type Transactional interface {
    // SendNow delivers one message synchronously (outside any tx) and records the attempt without the
    // variable values marked secret (OTP code). Called only by auth (OTP).
    SendNow(ctx context.Context, m TransactionalMessage) (AttemptID, error)
}
type Admin interface {
    PublishTemplate(ctx context.Context, cmd PublishTemplateCmd) error // content.moderate / ADMIN
    SetPreference(ctx context.Context, cmd PreferenceCmd) error         // self
}
```

**Repositories.** `notifications/store`: job creation keyed by `(event_id, recipient, template)` unique;
attempts append-only; suppression lookup.

**External dependencies.** platform, audit, users (recipient contact resolution). SMS and email providers
(`channels/*`).

**Events.** Produces `notification.delivery_failed` (operations metric; no domain consumers). Consumes the
events marked "notifications" in [dependency-rules.md §4.2](dependency-rules.md); payloads must carry the
non-sensitive render data the template needs.

**Error codes.** `TEMPLATE_NOT_FOUND`, `TEMPLATE_RENDER_FAILED`, `RECIPIENT_SUPPRESSED`,
`CHANNEL_UNAVAILABLE`, `NOTIFICATION_BUDGET_EXCEEDED`.

**Authorisation.** Self for preferences; ADMIN (`content.moderate`) for templates; consumers run as system
actors.

**Transaction boundaries.** Consumer tx creates `notification_jobs` (TX-27); sending happens in
`notifications.send` outside any tx; the attempt result is a separate small tx.

**Test strategy.** Template injection tests (variables escaped per channel); no C3 field reachable from any
template variable (allow-list); suppression honoured; idempotent fan-out on duplicate events; OTP code never
persisted.

---

### 5.12 `kyc` (layer 4) — T4 zone

**Responsibilities.** Individual KYC and organisation KYB: verification profiles (level + status overlay),
cases, checks (append-only, `valid_until`), documents (metadata; objects in `private-kyc/kyc/`), decisions,
identities (C3, encrypted, blind index), consents, organisation persons, beneficial owners, representative
authorities, fundraising authorities, beneficiary evidence; vendor integration (anti-corruption layer
`kyc/vendor`, manual-only mode); gate evaluation (`gate_policies`, versioned); justified, audited staff
access to documents and identity numbers. Publishes **levels and statuses only**.

**Tables** (schema `kyc`, `fundzim_kyc` pool): `verification_profiles`, `profile_events`, `kyc_cases`,
`kyc_checks`, `kyc_documents`, `kyc_decisions`, `identities`, `consents`, `kyb_organisations`, `kyb_cases`,
`kyb_checks`, `organisation_persons`, `beneficial_owners`, `representative_authorities`,
`fundraising_authorities`, `beneficiary_evidence`, `gate_policies`, `vendor_callback_inbox`.
Objects: `private-kyc` credential (`kyc/` prefix only), held by no other module.

**Public interface (sketch, not compiled).** Nothing in this interface returns C3 except the two `Reveal*`
methods, which require permission + justification + case and are audited in the security chain.

```go
package kyc

type Level string   // UNVERIFIED, BASIC_VERIFIED, IDENTITY_VERIFIED, PAYOUT_VERIFIED
type OrgLevel string // ORG_UNVERIFIED, ORG_REGISTERED_VERIFIED, ORG_KYB_VERIFIED, ORG_PAYOUT_VERIFIED
type Status string  // ACTIVE, PENDING_REVIEW, REJECTED, SUSPENDED
type LevelStatus struct { Level Level; Status Status; Version int64; ValidUntil *time.Time }
type OrgLevelStatus struct { Level OrgLevel; Status Status; Version int64 }

// Live gate reads (P2). Used by campaigns, payouts, beneficiaries, compliance.
type Gates interface {
    UserLevel(ctx context.Context, userID ids.UUID) (LevelStatus, error)
    OrganisationLevel(ctx context.Context, orgID ids.UUID) (OrgLevelStatus, error)
    RepresentativeAuthorised(ctx context.Context, orgID, userID ids.UUID) (bool, error)  // EC-03 (org)
    FundraisingAuthority(ctx context.Context, ref AuthorityRef) (AuthorityStatus, error) // validity window, single subject
    MeetsGate(ctx context.Context, subject SubjectRef, gate GateKey) (GateResult, error) // policy-configured gates (D3)
}

// Owner-facing flows (HTTP via kyc/http).
type Verification interface {
    StartSession(ctx context.Context, cmd StartVerificationCmd) (SessionView, error)    // vendor or manual
    SubmitDocument(ctx context.Context, cmd SubmitDocumentCmd) (DocumentID, error)      // object via storage pipeline with kyc client
    RecordConsent(ctx context.Context, cmd ConsentCmd) (ConsentID, error)
    StartKYB(ctx context.Context, cmd StartKYBCmd) (CaseID, error)
    RegisterFundraisingAuthority(ctx context.Context, cmd AuthorityCmd) (AuthorityRef, error)
    StoreBeneficiaryEvidence(ctx context.Context, cmd BeneficiaryEvidenceCmd) (EvidenceRef, error) // called by beneficiaries
}

// Staff review (admin handlers).
type Review interface {
    Queue(ctx context.Context, q ReviewQueueQuery) (Page[CaseSummary], error)    // kyc.case.review
    Decide(ctx context.Context, cmd DecideCmd) error                            // kyc.decision.record; HIGH-risk second approver; TX-21
    ViewDocument(ctx context.Context, cmd ViewDocumentCmd) (PresignedURL, error) // kyc.document.view + justification + case; ≤5 min
    RevealIdentityNumber(ctx context.Context, cmd RevealCmd) (string, error)    // kyc.identity_number.reveal + justification
}

// Compliance-only, case-bound access to identity attributes needed for screening (minimised).
type ScreeningAttributes interface {
    ForScreening(ctx context.Context, subject SubjectRef, caseRef *ids.UUID) (ScreeningSubject, error) // names, DOB, nationality
}
```

**Repositories.** `kyc/store` (asserts `db.PoolKYC`): profile versioned updates; append-only checks, events,
consents; partial unique index on `(id_type, id_number_bidx)` for active profiles; vendor callback inbox
(`vendor_callback_inbox`, unique per vendor event id). **Sub-packages:** `vendor` (`IdentityVerifier` adapters incl. `manual`), `crypto`
helpers bound to the `kyc-*` key classes.

**External dependencies.** platform, audit, storage (pipeline with the kyc object client), users,
organisations. KYC vendor (optional), KMS (dedicated key), `private-kyc/kyc/` prefix.

**Events.** Produces `kyc.level_changed`, `kyc.status_changed`, `kyb.level_changed`, `kyb.status_changed`,
`kyc.fundraising_authority_expiring`, `kyc.fundraising_authority_expired`, `kyc.profile_snapshot`,
`kyc.review_required` (staff queue). Consumes `organisation.created`, `organisation.member_changed`,
`storage.object_promoted` / `_rejected` (document readiness).

**Error codes.** `KYC_PROFILE_NOT_FOUND`, `KYC_LEVEL_INSUFFICIENT`, `KYC_STATUS_NOT_ACTIVE`,
`KYC_CASE_NOT_FOUND`, `KYC_CASE_INVALID_TRANSITION`, `KYC_DOCUMENT_NOT_FOUND`, `KYC_DOCUMENT_NOT_READY`,
`KYC_DUPLICATE_IDENTITY` (routes to review, never auto-merged), `KYC_CONSENT_REQUIRED`,
`KYC_VENDOR_UNAVAILABLE`, `KYB_ORGANISATION_NOT_FOUND`, `FUNDRAISING_AUTHORITY_INVALID`,
`FUNDRAISING_AUTHORITY_EXPIRED`, `KYC_ACCESS_JUSTIFICATION_REQUIRED`, `KYC_SECOND_APPROVER_REQUIRED`.

**Authorisation.** Self for own verification flows; org `ORG_ADMIN` / authorised representative for KYB.
Staff: `kyc.case.review`, `kyc.decision.record`, `org.verification.decide` (KYC_REVIEWER, COMPLIANCE),
`kyc.document.view` and `kyc.identity_number.reveal` (justified, case-bound, KYC_REVIEWER/COMPLIANCE only;
never ADMIN/SUPER_ADMIN by default). `ScreeningAttributes` only for the compliance module's system actor or
COMPLIANCE staff with a case. SoD: KYC decision and payout approval for the same subject are different people
(operational-controls §2).

**Transaction boundaries.** All writes on the kyc pool, one tx per command with audit (security audit for
views/reveals) and outbox (TX-21). Vendor calls are outside txs (P5). Gate reads are P2 from other modules'
perspective and are never part of their transactions (system-overview §3.3).

**Test strategy.** Grants: `fundzim_app` cannot read any `kyc` table (SET ROLE tests); encryption round-trip
and key rotation; blind-index duplicate detection; every view/reveal produces a security audit event; vendor
outcome `FAIL` never auto-rejects; events contain no C3 (payload classification test); manual-only mode
end-to-end.

---

### 5.13 `beneficiaries` (layer 5)

**Responsibilities.** Beneficiaries (who a campaign is for), relationships to the owner, beneficiary
verification decisions (no C3 — evidence lives in `kyc.beneficiary_evidence`), institution payees (KYB-lite)
that can receive payouts directly.

**Tables** (schema `app`): `beneficiaries`, `beneficiary_relationships`, `beneficiary_verifications`,
`institution_payees`.

**Public interface (sketch, not compiled).**

```go
package beneficiaries

type ID ids.UUID
type Type string                 // SELF, INDIVIDUAL_OTHER, MINOR, INSTITUTION, ORGANISATION (beneficiary-verification §2)
type VerificationStatus string   // UNVERIFIED, PENDING, VERIFIED, REJECTED, EXPIRED

type Service interface {
    Create(ctx context.Context, cmd CreateBeneficiaryCmd) (Beneficiary, error)         // owner; evidence via kyc.StoreBeneficiaryEvidence
    AttachEvidence(ctx context.Context, cmd AttachEvidenceCmd) error
    Get(ctx context.Context, id ID) (Beneficiary, error)                               // masked public/owner/staff views
    VerificationState(ctx context.Context, id ID) (VerificationState, error)           // EC-04 live read
    PayeeEligible(ctx context.Context, id ID) (PayeeEligibility, error)                // holder rules for EC-05
}
type Review interface {
    Decide(ctx context.Context, cmd DecideBeneficiaryCmd) error // beneficiary.verification.decide; second verifier for configured types
}
type InstitutionPayees interface {
    Register(ctx context.Context, cmd RegisterPayeeCmd) (PayeeID, error)
    Verify(ctx context.Context, cmd VerifyPayeeCmd) error       // staff, independent contact check
    Get(ctx context.Context, id PayeeID) (InstitutionPayee, error)
}
```

**Repositories.** `beneficiaries/store`: beneficiary and payee rows; verification decision history
(append-only); query scoping by campaign owner via a campaign-ownership **argument** supplied by the caller
(campaigns), never by joining campaign tables.

**External dependencies.** platform, audit, users, organisations, kyc.

**Events.** Produces `beneficiary.verification_decided`, `institution_payee.verified`. Consumes
`kyc.fundraising_authority_expired` (mark verification `EXPIRED` where the authority is a precondition).

**Error codes.** `BENEFICIARY_NOT_FOUND`, `BENEFICIARY_NOT_VERIFIED`, `BENEFICIARY_EVIDENCE_REQUIRED`,
`BENEFICIARY_CONSENT_REQUIRED`, `BENEFICIARY_VERIFICATION_INVALID_STATE`, `INSTITUTION_PAYEE_NOT_FOUND`,
`INSTITUTION_PAYEE_NOT_VERIFIED`.

**Authorisation.** `beneficiaries` may not import `campaigns`, so it cannot check campaign ownership
itself. Campaign-scoped beneficiary routes (`/campaigns/{id}/beneficiaries`) are therefore owned by
**`campaigns/http`**: `campaigns` checks that the principal is the owner or `ORG_ADMIN`, then calls
`beneficiaries` with the verified campaign context. Standalone routes (`/institution-payees`) are owned by
`beneficiaries/http`, scoped to the creating principal. Staff: `beneficiary.verification.decide`
(KYC_REVIEWER, COMPLIANCE).

**Transaction boundaries.** One tx per command (app pool). Evidence storage in `kyc` is a separate kyc-pool tx
performed **before** the beneficiary tx; the beneficiary row stores the returned opaque evidence reference.
A crash between them leaves unreferenced evidence that `kyc` ages out (retention class, LR-012).

**Test strategy.** Exactly-one-payee-reference constraint; minors' data hidden by default (PD-39); verification
history append-only; second-verifier rule; no C3 in any beneficiary DTO.

---

### 5.14 `compliance` (layer 5) — T5 zone

**Responsibilities.** Compliance cases and events (with `blocks_payouts`, confidentiality), case links by ID
to any subject, sanctions/PEP screening requests, results and hits (vendor ACL `compliance/screening`), STR
preparation (LR-008, RLS-restricted), capability restrictions (`CREATE_CAMPAIGN`, `DONATE`,
`RECEIVE_PAYOUT`, …) with expiry and maker-checker; compliance overrides/exemptions; payout-relevant narrow
reads (`v_payout_blocking_cases`, `v_screening_status`, `v_active_restrictions`, granted to `fundzim_app`).

**Tables** (schema `compliance`, `fundzim_compliance` pool): `compliance_cases`, `compliance_case_events`,
`compliance_case_links`, `screening_requests`, `sanctions_screening_results`, `screening_hits`, `str_reports`,
`compliance_restrictions`, `screening_callback_inbox`, `screening_list_sources`, `screening_suppressions`.

**Public interface (sketch, not compiled).**

```go
package compliance

type CaseID ids.UUID
type CaseStatus string // OPEN, IN_PROGRESS, AWAITING_INFO, PENDING_APPROVAL, DECIDED, CLOSED, REOPENED
type Capability string // CREATE_CAMPAIGN, DONATE, RECEIVE_PAYOUT, ...

// Payout gates (app pool, narrow views, joinable into the payout tx).
type PayoutGates interface {
    BlockingCases(ctx context.Context, tx db.Tx, subjects []SubjectRef) ([]BlockingCase, error)       // EC-13; subject + case id + flag (v_payout_blocking_cases)
    ScreeningStatus(ctx context.Context, tx db.Tx, subjects []SubjectRef) ([]ScreeningStatus, error)   // EC-15; v_screening_status (no hit details)
}

// Capability restrictions. ActiveTx is the only method campaigns/payments may call: read-only, joins the caller's
// app-pool tx, reads compliance.v_active_restrictions (subject, capability, expires_at; no reasons).
type Restrictions interface {
    ActiveTx(ctx context.Context, tx db.Tx, subject SubjectRef, c Capability) (bool, error)
    Apply(ctx context.Context, cmd ApplyRestrictionCmd) (RestrictionID, error)    // maker; RECEIVE_PAYOUT also places COMPLIANCE_HOLD (TX-22)
    ApproveRestriction(ctx context.Context, id RestrictionID, justification string) error // checker
    Lift(ctx context.Context, cmd LiftRestrictionCmd) error                       // maker-checker
}

type Cases interface {
    Open(ctx context.Context, cmd OpenCaseCmd) (CaseID, error)          // case.create (REVIEWER, SUPPORT) / case.manage
    Transition(ctx context.Context, cmd CaseTransitionCmd) error        // case.manage (COMPLIANCE)
    Link(ctx context.Context, cmd LinkCmd) error
    Get(ctx context.Context, id CaseID) (CaseView, error)               // RLS-aware; STR rows only with str.prepare
}

type Screening interface {
    Screen(ctx context.Context, subject SubjectRef, reason ScreeningReason) (RequestID, error) // async via vendor
    Disposition(ctx context.Context, cmd HitDispositionCmd) error       // false positive (maker-checker); cannot clear a confirmed match
}

type STR interface { // str.prepare; LEGAL_REVIEW_REQUIRED LR-008 for filing
    Prepare(ctx context.Context, cmd PrepareSTRCmd) (STRID, error)
}
```

**Repositories.** `compliance/store` (asserts `db.PoolCompliance`) for all tables; a second small store
`compliance/store/appviews` (asserts `db.PoolApp`) containing only the queries over
`v_payout_blocking_cases` / `v_screening_status` / `v_active_restrictions`. The RLS session setting is set by one
function after the permission check.

**External dependencies.** platform, audit, users, organisations, kyc (`ScreeningAttributes`, levels), risk
(holds), storage (STR attachments and case evidence in the `compliance/` prefix). Screening vendor (callbacks in
`screening_callback_inbox`).

**Events.** Produces `compliance.case_blocking_changed`, `compliance.restriction_applied`,
`compliance.restriction_lifted` (payloads: IDs and booleans only). Consumes `kyc.level_changed`
(screen at `IDENTITY_VERIFIED`), `kyb.level_changed`, `beneficiary.verification_decided`,
`campaign.submitted`, `campaign.material_edit_submitted`, `campaign.report_submitted`,
`payout_destination.changed` (rescreen payee), `risk.alert_raised` (alert → case).

**Error codes.** `CASE_NOT_FOUND` (also returned for RLS-hidden cases), `CASE_INVALID_TRANSITION`,
`RESTRICTION_NOT_FOUND`, `RESTRICTION_EXPIRY_REQUIRED`, `OVERRIDE_NOT_PERMITTED` (sanctions block, legal hold),
`SCREENING_UNAVAILABLE`, `SCREENING_CONFIRMED_MATCH` (internal; owner-facing callers map to a coarse code).

**Authorisation.** `case.create` (REVIEWER, SUPPORT), `case.manage`, `compliance.override.request` /
`.approve`, `str.prepare`, `payout.hold`, `evidence.hold` (COMPLIANCE). Tipping-off: no owner-facing route
returns compliance codes; `payouts` and `campaigns` translate to coarse codes.

**Transaction boundaries.** Commands on the compliance pool with audit + outbox (grants C-4); TX-22 also writes
`risk.holds` in the same tx. `PayoutGates` join the **payout's app-pool tx**. Vendor calls outside txs.

**Test strategy.** RLS: STR rows invisible without the setting and without `str.prepare`; `fundzim_app`
sees only the views; restriction requires expiry; override cannot clear a confirmed sanctions match; events
leak no reasons; screening vendor down → `DEFER` not pass.

---

### 5.15 `campaigns` (layer 6)

**Responsibilities.** Campaigns (owner user **xor** organisation, `settlement_model` MODEL_A/MODEL_C,
accepted currencies, single goal currency), categories, versioned review policies, versions for material
edits, goals, media references, updates, campaign↔beneficiary links, reviews pinned to a policy version,
status history, public reports, moderation actions, lifecycle and visibility state machines, freeze/unfreeze
orchestration (hold + ledger move atomically), the ledger move for campaign-scope `COMPLIANCE_HOLD`,
donation-acceptance and payout-eligibility facts for downstream modules, public summaries (money states per
currency from `ledger`).

**Tables** (schema `app`): `campaign_categories`, `campaign_review_policies`, `campaigns`,
`campaign_versions`, `campaign_goals`, `campaign_media`, `campaign_updates`, `campaign_beneficiaries`,
`campaign_reviews`, `campaign_status_history`, `campaign_reports`, `campaign_moderation_actions`.

**Public interface (sketch, not compiled).**

```go
package campaigns

type ID ids.UUID
type Status string     // DRAFT, SUBMITTED, UNDER_REVIEW, APPROVED, ACTIVE, COMPLETED, REJECTED, SUSPENDED, FROZEN, CANCELLED
type Visibility string // PUBLIC, UNLISTED, HIDDEN

type Owners interface { // used by payments, payouts, beneficiaries routes
    Ownership(ctx context.Context, id ID) (Ownership, error) // owner user or org; for authorisation decisions
    AssertCanManage(ctx context.Context, id ID) error        // principal is owner or ORG_ADMIN/member per action
}

type Lifecycle interface {
    CreateDraft(ctx context.Context, cmd CreateDraftCmd) (Campaign, error)     // compliance.Restrictions.ActiveTx(CREATE_CAMPAIGN) inside the tx
    Edit(ctx context.Context, cmd EditCmd) (Campaign, error)                   // material edit → new version + re-review
    Submit(ctx context.Context, id ID) error                                   // gates: kyc.MeetsGate, beneficiaries, authority
    LinkBeneficiary(ctx context.Context, cmd LinkBeneficiaryCmd) error         // owns /campaigns/{id}/beneficiaries routes
    PostUpdate(ctx context.Context, cmd PostUpdateCmd) (UpdateID, error)
    Cancel(ctx context.Context, cmd CancelCmd) error                           // emits campaign.cancelled
}

type Review interface {
    Queue(ctx context.Context, q ReviewQueueQuery) (Page[ReviewItem], error)   // campaign.review
    Decide(ctx context.Context, cmd ReviewDecisionCmd) error                   // campaign.decide; HIGH tier SoD with KYC decider
    Suspend(ctx context.Context, cmd SuspendCmd) error                         // campaign.suspend
    Unsuspend(ctx context.Context, cmd UnsuspendCmd) error
}

type Freezes interface {
    Freeze(ctx context.Context, cmd FreezeCmd) error                           // campaign.freeze; TX-17
    RequestUnfreeze(ctx context.Context, cmd UnfreezeRequestCmd) (RequestID, error) // campaign.unfreeze.request
    ApproveUnfreeze(ctx context.Context, id RequestID, justification string) error  // campaign.unfreeze.approve (new); TX-18
}

// Facts for downstream financial modules (P2 reads, or tx-joining with row lock where noted).
type Facts interface {
    CanAcceptDonation(ctx context.Context, id ID, cur money.Currency) (DonationAcceptance, error) // status, visibility, accepted currencies, settlement model
    PayoutFacts(ctx context.Context, tx db.Tx, id ID) (PayoutFacts, error)                        // EC-01; FOR SHARE lock on the campaign row
    SettlementTarget(ctx context.Context, tx db.Tx, id ID) (ReleaseTarget, error)                 // PAYABLE or HELD (FROZEN) for T4/Y4/Y11/Y14
    PublicSummary(ctx context.Context, id ID) (PublicSummary, error)                              // per-currency money states from ledger
}
```

**Repositories.** `campaigns/store`: versioned updates with `version`; status transitions guarded by
`app.guard_transition`; review queue queries keyset-paginated; owner-scoped queries
(`WHERE owner_user_id = $1 OR owner_organisation_id = ANY($2)`); public lookup by `public_code`.

**External dependencies.** platform, audit, users, organisations, kyc, beneficiaries, storage, risk, ledger,
fees, compliance (`Restrictions.ActiveTx` only, inside its own tx). None external (media via storage).

**Events.** Produces `campaign.submitted`, `campaign.material_edit_submitted`, `campaign.status_changed`,
`campaign.frozen`, `campaign.unfrozen`, `campaign.cancelled`, `campaign.report_submitted`. Consumes
`kyc.level_changed` / `kyb.level_changed` (re-evaluate pending submissions), `kyc.fundraising_authority_*`,
`beneficiary.verification_decided`, `storage.object_promoted` / `_rejected`, `risk.hold_placed` /
`risk.hold_released` (campaign-scope `COMPLIANCE_HOLD` ledger move, job `campaigns.on_risk_hold_placed`),
`compliance.restriction_applied` (pause pending submissions; creation itself is checked live).

**Error codes.** `CAMPAIGN_NOT_FOUND`, `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_EDITABLE`,
`CAMPAIGN_SUBMISSION_GATE_FAILED` (with coarse category: verification, beneficiary, authority),
`CAMPAIGN_NOT_ACCEPTING_DONATIONS`, `CAMPAIGN_CURRENCY_NOT_ACCEPTED`, `CAMPAIGN_ALREADY_FROZEN`,
`CAMPAIGN_NOT_FROZEN`, `CAMPAIGN_REVIEW_CONFLICT` (SoD), `CAMPAIGN_CREATION_RESTRICTED` (coarse; LR-008),
`CAMPAIGN_CATEGORY_NOT_FOUND`, `REVIEW_POLICY_NOT_FOUND`.

**Authorisation.** Owner/org-role ABAC on all owner routes; staff `campaign.review`, `campaign.decide`,
`campaign.suspend`, `campaign.unsuspend` (REVIEWER, COMPLIANCE), `campaign.freeze` (COMPLIANCE, FINANCE),
`campaign.unfreeze.request` / `campaign.unfreeze.approve` *(new name for the checker)* with whoever froze or
requested unable to approve; `content.moderate` (ADMIN) for moderation actions. Staff with a declared conflict
cannot review (operational-controls §2).

**Transaction boundaries.** TX-17 freeze (status + history + moderation action + `risk.Holds.Place
(CAMPAIGN_FREEZE)` + `ledger.PostCampaignFreeze` + audit + outbox) and TX-18 unfreeze. Material edit:
version row + status → re-review + `PAYOUT_HOLD` via risk when the edit touches beneficiary or payout-relevant
fields (F-10, F-13), one tx. Hold-journal consumer: `inbox_events` + `ledger.PostHoldApplied` + audit.

**Test strategy.** State machine edges Go = SQL; freeze atomicity (fault-inject between steps → nothing
committed); concurrent freeze vs payout reservation (real PostgreSQL); ownership IDOR on every route; story
sanitiser; HIGH-tier SoD; `ACTIVE`+`PUBLIC` with an active `PAYOUT_HOLD` still accepts donations but payouts
are blocked.

---

### 5.16 `payments` (layer 7) — T3

**Responsibilities.** Donations (donor-facing record, anonymity, consent), payment intents (canonical payment;
state machine of baseline §8 with ranks and parking), attempts (every outbound call, classified),
provider transactions (`raw_status` + `mapped_status`), provider references, payment events, routing
(capability, custody guard, currency settlement guard, priority, fallback only before the first call),
status polling and expiry, inbound provider event processing (payment, refund, dispute, reversal kinds),
release of settled funds (T4), refund requests (maker-checker) and refund transactions (incl. manual
fallback `MANUAL_PAYOUT`), disputes and chargebacks with their journals and holds.
State machine detail: [payment-state-machine.md](payment-state-machine.md).

**Tables** (schema `app`): `donations`, `payment_intents`, `payment_attempts`, `payment_transactions`,
`payment_provider_references`, `payment_events`, `refund_requests`, `refund_transactions`,
`payment_disputes`, `chargebacks`.

**Public interface (sketch, not compiled).**

```go
package payments

type PaymentID ids.UUID // = payment_intents.id; provider idempotency reference
type IntentStatus string // CREATED, PENDING, REQUIRES_ACTION, AUTHORISED, UNKNOWN, SUCCEEDED, FAILED, EXPIRED, CANCELLED, PARTIALLY_REFUNDED, REFUNDED, DISPUTED, CHARGED_BACK

type Donations interface {
    AvailableMethods(ctx context.Context, q MethodsQuery) ([]MethodOption, error) // campaign × currency × amount; fee preview
    CreateDonation(ctx context.Context, cmd CreateDonationCmd) (DonationResult, error) // Idempotency-Key required; TX-02 → call → TX-03
    GetPaymentStatus(ctx context.Context, id PaymentID, view Audience) (PaymentStatusView, error) // donor (guest token or user), owner (no donor identity if anonymous), staff
    ListCampaignDonations(ctx context.Context, q CampaignDonationsQuery) (Page[DonationView], error) // owner/public per opt-in
}

type Refunds interface {
    Request(ctx context.Context, cmd RequestRefundCmd) (RefundRequestID, error)  // refund.request (SUPPORT, COMPLIANCE) or donor policy flow
    Approve(ctx context.Context, id RefundRequestID, justification string) error  // refund.approve (FINANCE, ≠ requester); TX-10
    Reject(ctx context.Context, id RefundRequestID, reason string) error
    Withdraw(ctx context.Context, id RefundRequestID) error
    RequestBulk(ctx context.Context, cmd BulkRefundCmd) (BatchID, error)          // cancelled/fraud campaign; manifest hash
}

type Disputes interface {
    SubmitEvidence(ctx context.Context, cmd DisputeEvidenceCmd) error // object via storage, then audit.Evidence.Record
    Accept(ctx context.Context, id DisputeID, reason string) error
    Get(ctx context.Context, id DisputeID) (DisputeView, error)
}

// Called by reconciliation (allowed import) to resolve UNKNOWN/long PENDING with statement evidence.
type ReconciliationHooks interface {
    LookupByProviderReference(ctx context.Context, tx db.Tx, p psp.ProviderID, ref string) (PaymentRef, error)
    ResolveFromStatement(ctx context.Context, tx db.Tx, cmd ResolveCmd) error // RECON source transition (rank + graph rules)
}
```

**Repositories.** `payments/store`: intent lock by id; precedence-checked transition (`UPDATE … WHERE id=$1
AND status = $expected`); provider reference upsert with unique `(provider, kind, reference)`; due-poll
queries (`next_poll_at`, `poll_horizon_at`); refund and dispute workflows.

**External dependencies.** platform, audit, campaigns (`Facts`, `Owners`), fees (`Calculator`), ledger
(`Poster`, `Balances.HasSettlementFor`), risk (`Holds`, `Limits`, `Assessor`, `Signals`), psp (`Registry`,
`Inbox`), storage (dispute evidence objects), compliance (`Restrictions.ActiveTx(DONATE)` only, inside TX-02).
PSPs through `psp` only.

**Events.** Produces `payment.succeeded`, `payment.status_changed`, `refund.completed`, `refund.failed`,
`refund.shortfall`, `dispute.opened`, `dispute.closed`, `dispute.shortfall`. Consumes `settlement.matched`
(schedule release), `campaign.cancelled` (stop intents; open refund disposition for staff).

**Error codes.** `PAYMENT_NOT_FOUND`, `PAYMENT_METHOD_UNAVAILABLE` (422, PAYMENTS §5),
`PAYMENT_AMOUNT_OUT_OF_RANGE`, `PAYMENT_CURRENCY_NOT_SETTLEABLE` (no rail settles in the donation currency,
ADR-018), `PAYMENT_PROVIDER_UNAVAILABLE`, `PAYMENT_INVALID_TRANSITION` (internal), `DONATION_NOT_ALLOWED`
(coarse: campaign not accepting, restriction, hold), `DONATION_LIMIT_EXCEEDED`, `REFUND_NOT_ALLOWED`,
`REFUND_AMOUNT_EXCEEDS_REFUNDABLE`, `REFUND_REQUEST_NOT_FOUND`, `REFUND_INVALID_STATE`,
`REFUND_PROVIDER_UNSUPPORTED` (manual fallback required), `DISPUTE_NOT_FOUND`, `DISPUTE_INVALID_STATE`.

**Authorisation.** Donation creation: public (guest) or user, rate-limited, idempotent; status read: the
donor's session or a guest status token bound to the payment, the campaign owner (aggregate; no anonymous
donor identity — T-22), staff `payment.view` *(new)*; donor unmasking `donor.identity.unmask` (justified).
Refunds: `refund.request` (SUPPORT, COMPLIANCE), `refund.approve` (FINANCE, not the requester). Disputes:
`dispute.manage` *(new)* (FINANCE/COMPLIANCE).

**Transaction boundaries.** TX-02 … TX-11 in [system-overview.md §3.2](system-overview.md). Provider calls
always between transactions (P5); inbound processing (TX-04) commits inbox completion with the transition and
journals; disputes (TX-08) place `DISPUTE_HOLD` synchronously.

**Test strategy.** Full sandbox failure matrix (decline, timeout→UNKNOWN→resolved both ways, duplicate and
out-of-order webhooks, late success after EXPIRED per transaction-lifecycle §5 exceptions); idempotency
(same key replays, different body conflicts, concurrent same key); routing refuses `MERCHANT_SETTLEMENT` and
non-settling currency; capture journal amounts with fee versions; refund maker-checker; dispute journals per
timing (refund-and-reversal-flows §6); anonymous donor never in owner/public DTOs; concurrency: webhook and
poll racing on the same intent produce one capture.

---

### 5.17 `payouts` (layer 7) — T3

**Responsibilities.** Payout destinations (versioned, C3 ciphertext with key class `payout-destination`,
blind index, masked display) and their verifications (ownership/name match); payout requests and the state
machine (baseline §8, Y1–Y14); the eligibility engine (EC-01 … EC-22) at request, approval and pre-submit,
writing `payout_eligibility_decisions` for every evaluation; approval tiers (AUTO/SINGLE/DUAL) and approvals
with expiry; reservations; submission (destination snapshot hash), attempts, provider references, polling,
failures, reversals; recovery cases (from `dispute.shortfall` / `refund.shortfall`). Detail:
[payout-state-machine.md](payout-state-machine.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md).

**Tables** (schema `app`): `payout_destinations`, `payout_destination_verifications`, `payout_requests`,
`payout_attempts`, `payout_approvals`, `payout_reservations`, `payout_provider_references`, `payout_events`,
`payout_failures`, `payout_reversals`, `payout_eligibility_decisions`, `payout_destination_override_requests`
(staff override maker-checker, I-3), `recovery_cases`, `recovery_case_events`.

**Public interface (sketch, not compiled).**

```go
package payouts

type PayoutID ids.UUID // provider idempotency reference; never reused after an authoritative failure
type Status string     // PAYOUT_REQUESTED, PENDING_REVIEW, APPROVED, SUBMITTED, PROCESSING, UNKNOWN, COMPLETED, FAILED, REJECTED, CANCELLED, REVERSED

type Destinations interface {
    Add(ctx context.Context, cmd AddDestinationCmd) (DestinationRef, error)      // step-up; owner or ORG_ADMIN; TX-19 (DESTINATION_HOLD)
    Replace(ctx context.Context, cmd ReplaceDestinationCmd) (DestinationRef, error)
    ProposeStaffOverride(ctx context.Context, cmd OverrideCmd) (RequestID, error) // SUPPORT/COMPLIANCE with evidence
    ApproveStaffOverride(ctx context.Context, id RequestID, justification string) error // payout.destination.override.approve
    List(ctx context.Context, q DestinationQuery) ([]DestinationView, error)     // masked
}

type Requests interface {
    Request(ctx context.Context, cmd RequestPayoutCmd) (RequestResult, error)    // Idempotency-Key, step-up; TX-12
    Cancel(ctx context.Context, id PayoutID, reason string) error                // before SUBMITTED only (Y5)
    Get(ctx context.Context, id PayoutID, view Audience) (PayoutView, error)     // owner sees coarse hold/case categories only
    List(ctx context.Context, q PayoutQuery) (Page[PayoutView], error)
}

type Approvals interface {
    Queue(ctx context.Context, q ApprovalQueueQuery) (Page[ApprovalItem], error)  // payout.approve
    Approve(ctx context.Context, cmd ApproveCmd) error                            // TX-13; checker ≠ requester/destination changer; DUAL distinct
    Reject(ctx context.Context, cmd RejectCmd) error                              // payout.reject; releases reservation (Y4)
}

type Recovery interface {
    Get(ctx context.Context, id RecoveryCaseID) (RecoveryCaseView, error)
    RecordRecovery(ctx context.Context, cmd RecordRecoveryCmd) error   // set-off / repayment journals
    ProposeWriteOff(ctx context.Context, cmd WriteOffCmd) (RequestID, error)
    ApproveWriteOff(ctx context.Context, id RequestID, justification string) error // maker-checker
}

// Called by reconciliation (allowed import).
type ReconciliationHooks interface {
    LookupByProviderReference(ctx context.Context, tx db.Tx, p psp.ProviderID, ref string) (PayoutRef, error)
    ResolveFromStatement(ctx context.Context, tx db.Tx, cmd ResolveCmd) error // UNKNOWN → COMPLETED/FAILED (RECON source)
}
```

Eligibility engine internals (`payouts/service/eligibility`): one function per check, each returning
`{check_id, result, detail_code, inputs}`; the engine version and policy version are recorded on every
decision. Data sources: EC-01 `campaigns.Facts.PayoutFacts`; EC-02 `users.IsActive`,
`organisations.MemberRole`, `campaigns.Owners`; EC-03 `kyc.Gates` (P2, before tx); EC-04
`beneficiaries.VerificationState`; EC-05/06/07 own tables + `risk.Limits`; EC-08 `ledger` account currency +
`psp.Registry.Capabilities`; EC-09 `ledger.Balances.LockCampaignBalances`; EC-10/17/21 `risk.Limits`; EC-11 own
partial unique index; EC-12/18 `risk.Holds.Active`; EC-13/15 `compliance.PayoutGates` (app-pool views `v_payout_blocking_cases`, `v_screening_status`; case ids recorded in the decision inputs);
EC-14 `ledger` balances + `DISPUTE_HOLD` rows; EC-16 `risk.Assessor`; EC-18/20 `psp.Health.RailStatus`
(C-2); EC-19 `ledger.Balances.CheckPoolIntegrity`; EC-22 own approvals.

**Repositories.** `payouts/store`: payout lock by id; partial unique index for single in-flight per
(campaign, currency) (EC-11); approval insert guarded by DB trigger (approver ≠ requester, DUAL distinct,
≠ recent destination changer); destination versions with ciphertext + blind index; decision rows with
`record_hash`; due-poll and deferred-submit queries.

**External dependencies.** platform, audit, users, organisations, kyc, beneficiaries, compliance, campaigns,
ledger, risk, psp, fees, storage (destination-ownership evidence). PSPs through `psp` only; KMS for the `payout-destination` key class.

**Events.** Produces `payout.requested`, `payout.status_changed`, `payout_destination.changed`,
`recovery_case.opened` *(informational)*. Consumes `dispute.shortfall`, `refund.shortfall` (recovery cases),
`risk.hold_placed` / `risk.hold_released`, `kyc.level_changed`, `kyc.status_changed`, `kyb.*`,
`beneficiary.verification_decided`, `compliance.case_blocking_changed`, `campaign.frozen`,
`campaign.unfrozen`, `campaign.cancelled` (re-checks and Y5/Y7).

**Error codes.** `PAYOUT_NOT_FOUND`, `PAYOUT_NOT_ELIGIBLE` (with coarse category only:
`VERIFICATION`, `DESTINATION`, `BALANCE`, `LIMIT`, `UNDER_REVIEW`, `PROVIDER` — no hold or case reasons,
LR-008), `PAYOUT_ALREADY_IN_FLIGHT`, `PAYOUT_INVALID_TRANSITION`, `PAYOUT_CANCEL_NOT_ALLOWED`,
`PAYOUT_APPROVAL_NOT_REQUIRED`, `PAYOUT_DESTINATION_NOT_FOUND`, `PAYOUT_DESTINATION_NOT_VERIFIED`,
`PAYOUT_DESTINATION_COOLING_OFF`, `PAYOUT_DESTINATION_HOLDER_MISMATCH`, `PAYOUT_SNAPSHOT_MISMATCH` (internal),
`RECOVERY_CASE_NOT_FOUND`, `RECOVERY_INVALID_STATE`. Approver conflicts use platform `APPROVER_CONFLICT`.

**Authorisation.** Owner (individual) or `ORG_ADMIN` (organisation) for destinations and requests, with
step-up; `payout.approve`, `payout.reject` held **only by FINANCE** with object-level conflict checks — DUAL means
two distinct FINANCE approvers, neither the requester; COMPLIANCE never approves payouts (baseline §12 I-20);
`payout.destination.override.approve` (FINANCE) for staff overrides; recovery write-off by FINANCE +
second approver. Owner views never show hold reasons, case existence or screening outcomes.

**Transaction boundaries.** TX-12 … TX-16, TX-19, TX-25. KYC reads (P2) happen before each transaction and
the level/version used is stored in the decision `inputs`. `CreatePayout` is always after TX-14 commits;
an `UNKNOWN` payout is never resubmitted.

**Test strategy.** Every EC check fails closed in isolation (table-driven, one failing input per case);
unconfigured limit disables AUTO; pre-submit re-check catches holds/destination changes placed after approval;
concurrency: two requests for the same campaign/currency → one succeeds (partial unique index), N requests
never over-reserve; approver conflict rules (DB trigger + service); destination snapshot mismatch fails
closed; UNKNOWN never resubmitted (sandbox timeout scenarios); recovery case created exactly once per
shortfall event; masked destinations in every DTO and log.

---

### 5.18 `reconciliation` (layer 8) — T3

**Responsibilities.** Reconciliation sources per provider and currency, statement imports (API via
`psp.ProviderReconciliationSource`, or files read with `storage.Open`), runs, items, matches (three-way: record ↔
provider line ↔ ledger), discrepancies and resolutions, settlement batches and items; posting T3/T3′ and
fee-remittance journals through `ledger`; resolving `UNKNOWN` payments and payouts with statement evidence
through `payments` / `payouts` hooks; recording settlement freshness and SEV1 discrepancies in `psp` health
(C-2). Detail: [reconciliation-schema.md](../database/reconciliation-schema.md), ADR-030.

**Tables** (schema `recon`): `reconciliation_sources`, `reconciliation_imports`, `reconciliation_runs`,
`reconciliation_items`, `reconciliation_matches`, `reconciliation_discrepancies`,
`reconciliation_resolutions`, `settlement_batches`, `settlement_items`.

**Public interface (sketch, not compiled).**

```go
package reconciliation

type Service interface {
    ImportStatement(ctx context.Context, cmd ImportCmd) (ImportID, error) // reconciliation.run; file hash → audit evidence
    StartRun(ctx context.Context, cmd StartRunCmd) (RunID, error)         // reconciliation.run
    Discrepancies(ctx context.Context, q DiscrepancyQuery) (Page[DiscrepancyView], error)
    Resolve(ctx context.Context, cmd ResolveDiscrepancyCmd) error         // reconciliation.resolve; may propose a ledger adjustment (maker)
    Freshness(ctx context.Context, p psp.ProviderID, cur money.Currency) (FreshnessView, error) // staff display
}
```

**Repositories.** `reconciliation/store`: import idempotent on `(source, file_sha256)`; item matching
queries by provider reference and amount/currency/date; discrepancy ageing (SC-4).

**External dependencies.** platform, audit, ledger, psp, payments (`ReconciliationHooks`), payouts
(`ReconciliationHooks`), storage (statement files). PSP statement APIs via `psp`.

**Events.** Produces `settlement.matched`, `reconciliation.discrepancy_opened`,
`reconciliation.run_completed`. Consumes none (it is driven by schedules and staff).

**Error codes.** `RECON_SOURCE_NOT_FOUND`, `RECON_IMPORT_DUPLICATE`, `RECON_IMPORT_PARSE_FAILED`,
`RECON_RUN_IN_PROGRESS`, `RECON_DISCREPANCY_NOT_FOUND`, `RECON_RESOLUTION_INVALID`.

**Authorisation.** `reconciliation.run`, `reconciliation.resolve` (FINANCE); any ledger correction goes
through `ledger.Adjustments` maker-checker. Report access and export redaction: Stage 17.

**Transaction boundaries.** TX-24 per matched line (match + T3 + outbox), plus health event in the same tx;
`UNKNOWN` resolution calls `payments`/`payouts` hooks inside the run's tx for that item so the status change,
journal and match commit together. Statement fetches outside txs.

**Test strategy.** Golden statement fixtures per sandbox custody model; idempotent re-import; three-way
match matrix (missing capture, double posting, unknown settlement → suspense, portal payout → SEV1);
freshness lapses → EC-20 `DEFER` in payouts tests; resolution of UNKNOWN both ways.

---

### 5.19 `admin` (layer 9)

**Responsibilities.** Staff HTTP handlers under `/api/v1/admin/*` composed from other modules' **root**
services: queues (campaign review, KYC/KYB, beneficiary verification, payout approvals, refunds, compliance
cases, reconciliation discrepancies, ledger adjustments, role requests), kill switches (as data via `psp`,
`risk`), dead-letter inspection and re-queue (via `platform/jobs`), staff DTO mapping. **No tables, no
business rules, never opens a transaction.**

**Tables.** None.

**Public interface (sketch, not compiled).** `admin` exposes no service interface; it registers routes.

```go
package admin // internal/admin/http is where the code lives; root package holds only route policy constants

func Register(r *httpx.Router, deps Deps) // Deps = root interfaces of the modules used (wired in internal/app)
```

**Repositories.** None.

**External dependencies.** Root packages of any module (baseline §3). No external systems.

**Events.** None.

**Error codes.** None of its own; it renders the owning module's codes (staff-visible variants may include
detail that owner-facing routes must not).

**Authorisation.** Every route declares a staff permission policy; services re-check. Step-up and
justification middleware per route; break-glass grants surface as time-boxed permissions on the principal.

**Transaction boundaries.** None; a staff action that must change several modules atomically is a single
method on the owning module (e.g. `campaigns.Freezes.Freeze`).

**Test strategy.** Route-policy completeness; every admin route has an authorisation test per role in the
[authorization-matrix.md](../api/authorization-matrix.md); no C3 in staff DTOs unless the route is a reveal
route; dead-letter re-queue audited and cannot edit payloads.

## 6. Composition root and entrypoints

```go
// Sketch, not compiled. apps/api/cmd/api/main.go
func main() { os.Exit(app.Main(context.Background(), app.ModeAPI)) }

// internal/app/app.go
type Mode int
const (ModeAPI Mode = iota; ModeWorker; ModeCtl)

func Main(ctx context.Context, mode Mode) int {
    cfg := config.MustLoad()                          // fails fast on missing/placeholder secrets
    logger := log.New(cfg.Observability)
    pools := db.MustOpen(ctx, cfg.Database)            // AppPool, KYCPool, CompliancePool (distinct types)
    keys := crypto.MustKeyring(ctx, cfg.Security)
    w := wire(ctx, cfg, logger, pools, keys)           // builds services in topological order (component-diagram §4.3)
    switch mode {
    case ModeAPI:    return serveHTTP(ctx, cfg, w.router())
    case ModeWorker: return runWorker(ctx, cfg, w.jobs(), w.events(), w.schedules())
    default:         return 2
    }
}
```

`internal/app` files: `wiring.go` (constructors), `router.go` (route groups and module registration),
`jobs.go` (worker registration and queues), `events.go` (event → consumer routing table, the single place
where couplings are declared), `ports.go` (the `httpx.Authenticator` adapter over `auth`; no other ports),
`config.go` (per-module config slices; secrets only passed to the module that needs them).

`fundzimctl` subcommands (Stage 3+): `migrate up|status` (goose, `fundzim_migrator`), `ledger recompute`,
`ledger invariants`, `audit verify-chain`, `outbox inspect` (read-only by default).

## 7. Cross-cutting test strategy

| Level | What | Where | Stage |
|---|---|---|---|
| Unit | Pure logic: money, fees, eligibility checks, state machine edges, posting rule line construction, mapping tables | `*_test.go` beside code | 3+ |
| Store | sqlc queries against real PostgreSQL with migrations applied and **real roles** (`SET ROLE fundzim_app/kyc/compliance`) | `internal/<m>/store/*_test.go` with `platform/testkit` (testcontainers or local DB) | 3+ |
| Service | Service with real store + fakes of other modules' root interfaces (`<m>test` packages) | `internal/<m>/service` | 3+ |
| Contract | Event fixtures decode in every consumer; psp adapter contract suite; OpenAPI ↔ handlers | `tests/architecture`, `psp/adapters/*` | 3, 8 |
| Architecture | Import graph, ownership, route policy, payload classification, error code registry, state edges Go = SQL | `tests/architecture` | 3 |
| Integration | Multi-module flows on one DB: donation → capture → settlement → release → payout → completion; freeze during payout; dispute after payout | `tests/integration` | 8–11 |
| Concurrency and failure injection | Parallel reservations, webhook/poll races, crash between P5 steps, retry on 40001/40P01 | `tests/integration` (real PostgreSQL; PGlite cannot test this) | 10, 11, 19 |
| Invariants | Ledger balance per currency, projection = recompute, SC-1–SC-6, audit chain | `fundzimctl` + nightly jobs + tests | 10, 17, 19 |

Test-architecture details: [docs/testing/test-architecture.md](../testing/test-architecture.md) (Stage 2
document maintained by the lead).

## 8. Module summary

| # | Module | Layer | Pool | Tables (count) | Key public interfaces | Produces events | Leaves process? |
|---|---|---|---|---|---|---|---|
| 1 | platform | 0 | app | 7 + queue | db, money, outbox, jobs, idempotency, makerchecker, crypto | — | KMS |
| 2 | audit | 1 | any (joins) | 4 | Recorder, Evidence, Reader | — | — |
| 3 | users | 2 | app | 4 | Service | yes | — |
| 4 | storage | 2 | app | 2 | Service | yes | object store, scanner |
| 5 | ledger | 2 | app | 10 | Poster, Balances, Adjustments, Invariants | yes | — |
| 6 | fees | 2 | app | 3 | Calculator, Admin | yes | — |
| 7 | psp | 2 | app | 5 | Registry, Inbox, Health, Admin + adapter interfaces | yes | PSPs |
| 8 | risk | 2 | app (+ compliance tx) | 9 | Holds, Limits, Assessor, Signals | yes | — |
| 9 | auth | 4 | app | 14 | Service, Authenticator, Roles, BreakGlass, Conflicts | yes | via notifications (OTP) |
| 10 | organisations | 3 | app | 5 | Service | yes | — |
| 11 | notifications | 3 | app | 5 | Transactional, Admin | yes | SMS, email |
| 12 | kyc | 4 | kyc | 18 | Gates, Verification, Review, ScreeningAttributes | yes | KYC vendor, private-kyc |
| 13 | beneficiaries | 5 | app | 4 | Service, Review, InstitutionPayees | yes | — |
| 14 | compliance | 5 | compliance (+ app views) | 11 | PayoutGates, Restrictions, Cases, Screening, STR | yes | screening vendor |
| 15 | campaigns | 6 | app | 12 | Owners, Lifecycle, Review, Freezes, Facts | yes | — |
| 16 | payments | 7 | app | 10 | Donations, Refunds, Disputes, ReconciliationHooks | yes | PSPs (via psp) |
| 17 | payouts | 7 | app | 14 | Destinations, Requests, Approvals, Recovery, ReconciliationHooks | yes | PSPs (via psp) |
| 18 | reconciliation | 8 | app | 9 | Service | yes | PSPs (via psp) |
| 19 | admin | 9 | — | 0 | (routes only) | — | — |
