# CLAUDE.md — FundZim engineering instructions

These are persistent, binding instructions for every Claude Code session (and every human engineer) working
in this repository. FundZim will orchestrate real people's money and hold sensitive identity data. When a rule
here conflicts with speed, convenience, or a request to "just make it work", the rule wins — stop and say so.

## What FundZim is

A Zimbabwe-first, donation-based crowdfunding platform: "Fund anyone in Zimbabwe, from anywhere."
Individuals and verified organisations raise funds; donors inside and outside Zimbabwe contribute through
licensed payment service providers (PSPs). Donation-only — **no** equity, securities, lending, interest,
profit-sharing, or rewards crowdfunding.

Read `docs/PRODUCT.md` for scope and `docs/ROADMAP.md` for the staged plan.

## Current stage

**Remediation phase 1 — repository integration, governance and reconciliation completeness (complete; awaiting review and
owner decisions; non-production; launch NOT authorised).** Not a product stage. Branch `remediation/reconciliation-completeness`
from `6a65d70`; nothing merged. What it built: `docs/remediation/phase-1/README.md` (preflight, integration plan, coverage
design, test evidence, performance, remediation register, final report; ADR-052); next workstream starts from
`docs/stage-handover/REMEDIATION-PHASE-1-TO-PHASE-2.md`. The development stack is stopped and its database already holds migration `20261025090000`
(F-R1-07, owner decision pending). Binding rules: **windowed reconciliation passes guarantee nothing —
coverage comes from the sweeps** (`internal/platform/reconsweep`; detectors PAYOUTS, FEE_ASSESSMENTS, PAYMENT_JOURNALS,
PAYMENT_FACTS); a checkpoint advances only by compare-and-swap after its batch was examined and recorded — never advance it
another way, never skip a record (defer it), never delete or rewrite a checkpoint (DB guard); a keyset read orders by the table's uuid column, never by a
`::text` output column (F-R1-06); windows read the newest records;
a new financial detector gets a sweep; **tests and tools never run a detector in the `default` scope** (set `ReconSweep.Scope`;
a non-payments journal source derives `<scope>.<source>`) — a run with fake facts or another config would move the worker's
checkpoint past records it never judged; **never plant a duplicate or otherwise invariant-breaking ledger journal on the shared
or disposable integration database** (append-only: L6 fails for ever and EC-19 refuses every payout) — use a scratch
`fundzim_perf*` database; **compose projects share the image tags `fundzim/api:local` / `fundzim/web:local`** — after building a
disposable project from another commit, rebuild the images from the development commit (`docker compose build`) before
`docker compose up` of the development project, or its `fundzim-migrate` applies the other commit's migrations to the shared
database (F-R1-07); run budgets are engineering defaults, the coverage freshness target is owner decision D-06.

**Stage 20 — Production certification, operational readiness & launch decision (complete; recommendation BLOCKED;
launch NOT authorised; awaiting owner decisions).** Stages 0–20 acceptance NOT_RECORDED. What Stage 20 produced:
`docs/stage-20/README.md` (preflight, evidence matrix, financial/provider/legal-privacy/accounting/security/
infrastructure-DR/operations/capacity/CI certification, findings register, owner decision pack D-01 … D-14, launch
blockers B-01 … B-17). Binding Stage 20 rules: a certification result is never upgraded because documentation exists or
a test passes — provider, legal, privacy, accounting, capacity, RPO/RTO, operator and launch decisions are the owner's
and are never recorded by Claude Code; evidence names its commit and CI run, and a run on another commit is never proof
for the candidate; certification and load evidence runs on a **disposable compose project** (`docker compose -p <name>`
after `docker compose down` of the development project **without** `-v`), never on the contaminated development
database; backups must come from one compose project (`backup.sh` derives the Garage volumes from the active project and
records `compose_project`; F-S20-07) and every backup type is proven by the restore drill; scripts use `tail -n N`
(uutils coreutils on the dev host); preserved evidence lives in `docs/stage-20/evidence/` with `SHA256SUMS`.
Reconciliation coverage (KI-S19-09) and an unprotected `main` (F-S20-01) are launch blockers.

**Stage 19 — Financial concurrency, load, failure & integrity validation (complete; awaiting acceptance; non-production;
production BLOCKED).** Stages 1–19 are complete but their acceptance has not been recorded (never mark a stage accepted
yourself); Stage 9 is PARTIAL. What Stage 19 built: `docs/stage-19/README.md` (preflight, invariants, transition
matrices, KI-S18-25, KI-S18-22, concurrency plan, load plan, fault injection, database contention, results,
benchmarks, failure analysis, known issues, security regression, final report; ADR-051); Stage 20 starts from
`docs/stage-handover/STAGE-19-TO-STAGE-20.md`. Binding Stage 19 rules: **the ledger follows the payout state** — payout
reconciliation re-reads each candidate and settles a pending ledger sync through `syncLedger` (the Recover path) before
judging; never report the completion → consumption window as a mismatch and never hide a sync the ledger refuses
(`ledger_sync = FAILED`); history events come only from the writer whose UPDATE changed the row (never insert an event
for a link another writer set); **a ledger posting locks every SYNC balance row it touches in ascending account id
before writing entries** — never lock a subset first; DEFERRED hot rows are never locked by postings; payout attempts
are numbered and recorded with the **payout row locked first** (`startAttempt`, `recordObservation`, the attempts guard)
and a status query is skipped while another of the same payout is in flight; **payment polling
is bounded** (`payments.PollHorizon` 72 h from creation, `UnknownHorizon`, `MaxStatusPolls` 500; ±20 % jitter; due
re-check under the row lock; a process sweeps only its own execution environment): an exhausted budget keeps the status,
opens POLL_BUDGET_EXHAUSTED and is never evidence of failure — never resubmit or fail a payment to resolve it; the 72 h /
500 values are internal defaults, not provider values (PCR-010). Stage 19 tests (`TestFinancial*`) use row-lock barriers
on their own records and `pg_terminate_backend` only on backends blocked by those locks (local superuser from `.env`);
`TestFinancialWorkerCrashMidFlight` and `TestFinancialLoad` are opt-in and run only against the local disposable stack;
ledger volume tiers run only on scratch databases named `fundzim_perf*`. Windowed reconciliations read the newest records
(never the oldest N); a failure or shutdown is recorded with a non-cancelled context. `TestFinancialLoad` on the shared
development database leaves thousands of sandbox payments without statement lines in Stage 17's window (KI-S19-17) —
prefer a disposable compose project for load runs.

**Stage 18 — Security hardening, compliance controls, resilience & recovery (complete; awaiting acceptance; non-production;
production BLOCKED).** Stages 1–18 are complete but their acceptance has not been recorded (never mark a stage accepted
yourself); Stage 9 is PARTIAL. What Stage 18 built: `docs/stage-18/implementation.md` (prerequisites, security
architecture, threat model, control register, authentication, authorization, secrets, cryptography, host, network,
database, containers, application, API, supply chain, monitoring, incident response, backup, DR, deployment, privacy,
compliance evidence, testing, findings and known issues, production readiness; ADR-050); Stage 19 starts from
`docs/stage-handover/STAGE-18-TO-STAGE-19.md`. Binding Stage 18 rules: **every financial gate stays off** — the 11 Go
constants and 20 gate CHECKs are a regression contract (`internal/archtest/financial_gates_test.go`,
`TestSecurityFinancialGatesInDatabase`): a new gate CHECK must be added to both lists; outside development and test every
application key is a `secretref://` reference (`FIELD_ENCRYPTION_PROVIDER=secretstore`, resolved at startup, no
fallback) — never put key material in production configuration, never add a `kms` provider without an ADR and a selected
KMS; a previous key (`*_PREVIOUS_KEYS`) stays configured until its data is re-encrypted; **audit verification is never
weakened or skipped to save time** (parallel ranges and MAC checkpoints under the worker-only `AUDIT_CHECKPOINT_KEY`; only
checkpoints under the current key count); repeated identical status polls are the only audit events not re-written;
security alerts are raised only by the scan and acknowledged/escalated/resolved only by people (`security.alert.manage`,
step-up; lifecycle guarded in the database); staff password changes need a TOTP step-up and never refresh it; readiness
endpoints never return error texts; every container keeps `cap_drop: [ALL]`, `no-new-privileges`, limits, loopback-only
ports and (FundZim's own) a read-only root, third-party images stay digest-pinned and the web container stays off the data
network (`internal/deploytest`); CI keeps `contents: read`, SHA-pinned actions, no secrets, no deployments, the gosec gate
and the Trivy gate on fixable CRITICAL/HIGH; backups are encrypted with a key kept outside the backup directory, never
recorded without a verified manifest MAC, and never deleted (incomplete sets are renamed `.INCOMPLETE`); retention
periods are never invented (`audit.retention_policies` CHECKs). Security integration tests (`TestSecurity*`) share the
development database: the alert tests scan only their own source (`ClearScope`), migration drills use their own goose
version tables and tamper tests run inside rolled-back transactions.

**Stage 17 — Financial reconciliation & settlement operations (complete; awaiting acceptance; TEST evidence only).** Stages
1–17 are complete but their acceptance has not been recorded (never mark a stage accepted yourself); Stage 9 is PARTIAL. What
Stage 17 built: `docs/stage-17/implementation.md` (prerequisites, architecture, imports and settlement, matching engine,
payment, bank, ledger, payout and fee reconciliation, unknown outcomes, exceptions, adjustments, approvals, audit evidence,
operations, security review, testing, known issues; ADR-049); Stage 18 starts from `docs/stage-handover/STAGE-17-TO-STAGE-18.md`.
Binding Stage 17 rules: `internal/reconciliation` owns the whole `recon` schema and nothing else; it reads payments, payouts,
fees and the ledger only through their services and changes them only through their own entry points (payments/payouts
`ApplyReconciliationOutcome` = their state machines with source `RECON`; the ledger only through approved adjustments) —
never write their tables; **no provider is selected**: the only statement formats are FundZim's canonical CSV/JSON (never
invent a provider schema), sources and imports are CHECK-limited to TEST, `ProductionImportsEnabled`,
`SettlementPostingEnabled` and `ProductionAdjustmentsEnabled` are false — never change them without an ADR, a provider
selection, an accounting review (KI-S10-02), legal sign-off and acceptance; imports are hashed first and refused when
identical (never deduplicate by filename), stored as private C2 evidence with an evidence record of the same hash, scanned
before parsing and parsed all or nothing; lines are immutable (revisions, CONFLICTING_EVIDENCE, explicit supersede) and
nothing in `recon` is deleted; **references find candidates, amounts only verify** — never match by amount alone, never
auto-resolve ambiguity, never compare or convert across currencies; matching parameters are maker-checker rule sets (none
seeded; no rule set = matching fails closed); statement evidence for an UNKNOWN payment or payout is applied only by an
approved, hash-bound, unexpired maker-checker resolution (checker never the maker or a linked account; Go, CHECK and
trigger) and **nothing is ever resubmitted**; exceptions close by themselves only for timing types when a later run finds
the condition cleared; no SLA is invented; CSV exports neutralise formula cells; bank account numbers are never stored
(last four + HMAC); a bank credit naming a payment is POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT (Model A). Payout
retries after RATE_LIMITED are budgeted like DEFINITELY_NOT_SENT (KI-S17-01). Reconciliation integration tests
(`TestReconciliation*`) share the development database with the compose worker (which parses statements and executes runs):
use unique tokens in every statement and line id, scope assertions to the test's own records, and reuse the active rule set.

**Stage 16 — Discovery, sharing & WhatsApp optimisation (complete; awaiting acceptance; non-production).** Stages 1–16 are
complete but their acceptance has not been recorded (never mark a stage accepted yourself); Stage 9 is PARTIAL. What Stage 16
built: `docs/stage-16/implementation.md` (prerequisites, architecture, search, indexing, ranking, trending, recommendations,
categories, locations, WhatsApp and social sharing, SEO, referrals, analytics, mobile performance, security review, testing,
known issues; ADR-048); Stage 17 starts from `docs/stage-handover/STAGE-16-TO-STAGE-17.md`. Binding Stage 16 rules:
`internal/discovery` owns only `app.discovery_*` and reads campaigns through `campaigns.Service` only; **the projection
(`app.discovery_campaign_index`) only proposes candidates — every card served anywhere comes from `campaigns.PublicCards`,
which re-checks eligibility now** (discoverable: live, PUBLIC, approved version, no compliance restriction of any level;
promotable: also ACTIVE and no CAMPAIGN/DONATION/ACCOUNT_REVIEW hold) and fails closed (503 `DISCOVERY_UNAVAILABLE`); never
serve a projection row without that check; the public page hides SUSPENDED/OFFBOARDED restrictions before the async
suspension; indexing runs on the River `discovery` queue, is idempotent and ordered by `snapshot_at` (never hold a
connection or lock while reading campaigns); featured placements are maker-checker (requester never decides; Go, CHECK and
an `actor_identities` trigger), windowed ≤ 31 days and promotable-only — Stage 7 staff picks are retired; trending is the
published formula (log-scaled unique visitors and sharers, freshness, minimum sample) — **money is never a ranking input**;
ranking weights are maker-checker policy versions (closed bounded keys, hashed, immutable; none seeded); recommendations are
fixed rules, `personalised` always false, never viewer, KYC, screening or risk data; analytics are aggregate and
cookie-less (no IP, user id, user agent or query text stored; daily keyed dedupe hashes deleted after 2 days; DNT/GPC
honoured; no browser storage; no third-party script); referral codes carry only campaign + channel, grant nothing, are never
linked to a donation, and `discovery.ProductionReferralsEnabled = false` blocks them in production until LR-023; campaign
location is coarse (province or provincial capital), optional, reviewed (material edit `LOCATION`) and never an address or
anyone's personal location (LR-090); listing variants are `noindex, follow` with the unfiltered canonical; structured data
never claims money, ratings, verification or tax status. Discovery integration tests (`TestDiscovery*`) share the
development database and the compose worker indexes their campaigns: find records by the test's own unique token.

**Stage 15 — Notifications & communications (complete; awaiting acceptance; simulators and development senders only).**
Stages 1–15 are complete but their acceptance has not been recorded (never mark a stage accepted yourself); Stage 9 is
PARTIAL. What Stage 15 built: `docs/stage-15/implementation.md` (prerequisites, architecture, event contracts, e-mail, SMS,
WhatsApp, in-app, templates, preferences, consent, delivery lifecycle, retries, dead letters, provider readiness, security
review, testing, known issues; ADR-047); Stage 16 starts from `docs/stage-handover/STAGE-15-TO-STAGE-16.md`. Binding Stage 15
rules: **one notification engine** (`internal/notifications`) fed only by the outbox through the consumer
`notifications.dispatch` — never add a second notification path or send a message from a domain module; credential messages
(verification/reset links, invitations, OTPs) stay in `auth`; **no provider is selected**: `LiveMessagingEnabled = false`
(production suppresses every external channel), provider configs are CHECK-limited to TEST and never VERIFIED or
PRODUCTION_ENABLED, simulators and `dev_*` senders are refused in production; `MarketingEnabled = false` and no marketing
consent can be granted; routing is the pure `notifications.Route` re-run before every send and replay (consent, preference,
suppression, verified contact, synthetic source) and **never falls back** to another channel; SMS and WhatsApp need a
GRANTED consent record that only the user can create (`USER_SETTINGS`); SECURITY in-app and e-mail are mandatory; synthetic
(TEST) money events notify in-app only with a `[TEST]` label, and a donation success notice comes only from
`payments.payment_succeeded`; risk and compliance outcomes never notify their subject (LR-008); templates are
placeholder-only, immutable after DRAFT and maker-checker (never the maker, submitter or a linked account); a WhatsApp template
is never APPROVED without provider evidence; acceptance is not delivery, an unknown outcome is looked up before any resend,
and provider callbacks never regress a delivered state; no Zimbabwean consent or retention rule is assumed (LR-010, LR-011,
LR-012, LR-023). Notification integration tests (`TestNotifications*`) build an in-process engine with simulators on the
shared development database alongside the compose worker: scope assertions to the test's own users. All notification
work runs on the River `notifications` queue (`outbox.Relay.ConsumerQueues`); never put it back on the default queue.
Stage 15 finalisation (KI-S11-20): payout resubmission after DEFINITELY_NOT_SENT is bounded by `PAYOUTS_SUBMIT_RETRY_BUDGET`
with jittered backoff; when spent, a `SUBMISSION_RETRIES_EXHAUSTED` exception halts automatic submission (dispatcher and
attempts guard) until an operator resolves it — never remove the halt or retry an UNKNOWN payout.

**Stage 14 — Administration & operations portal (complete; awaiting acceptance; TEST data only).** What Stage 14 built:
`docs/stage-14/implementation.md` (prerequisites, operations architecture, command center, staff management, approval center,
module operations, support, exceptions, health, incidents, audit center, reporting, security review, testing, known issues;
ADR-046); Stage 15 starts from `docs/stage-handover/STAGE-14-TO-STAGE-15.md`. Binding Stage 14 rules: `internal/operations`
imports platform and audit only and owns only `app.ops_*`; module data reaches it only through each module's own
`OpsSnapshot`/`OpsSearch` (built from that module's tables; shared types in `platform/opsview`) — never add cross-module SQL
to operations; **the portal is not an authority**: approvals link to the owning module's route (which re-checks everything),
exception resolution stays with the module (`OPS_EXCEPTION_DOMAIN_OPEN`), it never changes a domain status column, never
writes ledger tables and never releases a reservation, hold or restriction; every metric names its source, permission and
nature (REAL, SIMULATED, TEST_ONLY, UNKNOWN, NOT_AVAILABLE, BLOCKED) — a disabled capability is BLOCKED/NOT_CONFIGURED, never
HEALTHY, and a failed source is UNAVAILABLE, never 0; the UI's permission awareness (`permissions` on `/me` and the session)
is presentation only — every route keeps its policy; no superuser and no override permission; role changes and
access-review revocations are dual-controlled requests and nobody decides their own access; support roles get no KYC, bank,
ledger, risk-evidence or compliance-note access; audit records are never edited, exports are bounded, step-up and audited
before delivery; no internal SLA target is configured or invented (maker-checker `ops.sla.request` → `ops.sla.approve`);
no external notification is sent (Stage 15). Operations integration tests (`TestOperations*`) share the development
database: use the test's own records and leave fixture roles unchanged (reject any revocation request a test files).

**Stage 13 — Trust, fraud & risk engine (complete; awaiting acceptance; TEST data only).** Stages 1–13 are complete but
their acceptance has not been recorded (never mark a stage accepted yourself); Stage 9 is PARTIAL. What Stage 13 built:
`docs/stage-13/implementation.md` (prerequisites, architecture, signals, scoring, rules, decisions, campaign/payment/payout
risk, screening, case management, holds, privacy, security review, testing, known issues; ADR-045); Stage 14 starts from
`docs/stage-handover/STAGE-13-TO-STAGE-14.md`. Binding Stage 13 rules: **a risk score is a prioritisation aid, never proof of
fraud** — no code, UI, event, outcome or explanation may declare fraud (CHECKs); rules are declarative JSON (closed grammar,
bounded; never code), immutable after submission (hashed) and maker-checker (checker ≠ maker/submitter or a linked account,
Go and database), and **none is seeded**; automated enforcement is limited to cases, monitoring events and temporary
(≤ 7 days) WITHDRAWAL_HOLD / PAYOUT_DESTINATION_HOLD holds — never bans, confiscation, restrictions, reports, campaign
termination, screening clearance or hold release; legal restrictions come only from compliance (escalation
`risk.case_escalated`); `risk.risk_holds` is the single hold registry, enforced at publication, donation intents/attempts,
payout eligibility EC-12 (request and pre-submit) and payout approval — a registry error fails closed; releases and
disruptive-hold activation are maker-checker with step-up and nobody releases a hold on themselves; evidence is immutable and
SHA-256 hashed over canonical JSON; **no screening provider is selected**: `risk.ScreeningProductionEnabled = false`,
provider `sandbox` and environment TEST by CHECK, synthetic references only, the simulator is refused in production
(`RISK_SCREENING_SANDBOX_ENABLED`), NO_MATCH_REPORTED is never CLEARED, and a pending/failed/timed-out screening never clears
anyone; risk data holds ids, codes and amounts only (no IP, no device fingerprint; sign-ins carry a keyed per-user UA
hash); no retention period or regulatory deadline is invented (LR-012, LR-074). Risk integration tests (`TestRisk*`) share
the development database: activate only rules that match the test's own reason code and retire them at the end.

**Stage 12 — Fees & platform revenue (complete; awaiting acceptance; TEST fees only).** Stages 1–12 are complete but their
acceptance has not been recorded (`docs/governance/stage-acceptance-register.md` — never mark a stage accepted yourself);
Stage 9 is PARTIAL. What Stage 12 built: `docs/stage-12/implementation.md` (prerequisites, fee architecture, policy model,
calculation, rounding, provider fees, disclosure, revenue accounting, refund adjustments, chargeback fees, payout fees,
reconciliation, security review, testing, known issues; ADR-044); Stage 13 starts from
`docs/stage-handover/STAGE-12-TO-STAGE-13.md`. Binding Stage 12 rules: **every fee is a TEST fee** —
`fees.ProductionFeesEnabled = false` and `ck_*_test_only_stage12`; never seed or hard-code a commercial, tax or provider rate
(rates are synthetic configuration in maker-checker approved policy versions); provider fee evidence is SYNTHETIC (sandbox
only) or UNVERIFIED — VERIFIED is refused until a provider is selected (ADR-040); an UNKNOWN fee is never 0; quoted,
estimated, reported, confirmed, assessed and recognised fees are distinct values; fee calculation is the pure
`fees.Compute` (integer minor units, basis points, big-integer intermediates, one rounding per component, largest-remainder
splits) and every calculation stores its canonical inputs and result hashes — reconciliation must be able to recompute it;
published policy terms are immutable (submit → hash → checker approval, never the maker or a linked account; activation
and suspension need step-up); clients never send a rate or a fee; quotes are owner-only, expire and are invalidated by any
policy change in their currency (per-currency advisory lock: quotes SHARED, lifecycle changes EXCLUSIVE); test payments
are assessed but never posted; fee journals only through `ledger.Service` with `source_system = 'fees'` (SIMULATED in a TEST
ledger) — `fees` may import `ledger` (ADR-044 amends the Stage 2 graph) but never writes ledger tables; refund/chargeback
reversals post the journal before recording the adjustment; payout fees never change the reservation (organiser-borne fees
are carved out, platform-absorbed fees are `PAYOUT_FEE` expenses); currencies are never combined in reports; no
`CURRENCY_CONVERSION_FEE` (FundZim never converts). Fee integration tests (`TestFee*`) share the development database: scope
policies to the test's own campaign and retire anything global at the end.

**Stage 11 — Withdrawals & payouts (complete; awaiting acceptance; TEST payouts through the simulator only).** Stages
1–11 are complete but their acceptance has not been recorded (`docs/governance/stage-acceptance-register.md` is a proposed
template — never mark a stage accepted yourself); Stage 9 is PARTIAL. What Stage 11 built: `docs/stage-11/implementation.md`
(prerequisites, architecture, lifecycle, eligibility engine, available funds, reservations, approvals, provider
abstraction, simulator, ledger integration, reconciliation, security review, testing, known issues; ADR-043); Stage 12
starts from `docs/stage-handover/STAGE-11-TO-STAGE-12.md`. Binding Stage 11 rules: **every payout is a TEST payout** —
`payouts.LiveExecutionEnabled = false`, kill switch `payouts.live_execution.enabled` off, `PAYOUTS_EXECUTION_ENVIRONMENT`
only `DISABLED | LOCAL_SIMULATOR` (PROVIDER_SANDBOX and PRODUCTION refused), CHECKs `ck_payout_requests_no_production_stage11`
and `ck_payout_requests_test_only_stage11` — never change them without an ADR, a payout provider selection, settlement
evidence, legal sign-off and stage acceptance; production available funds are BLOCKED (no settlement evidence) and
synthetic funds are always labelled TEST_ONLY; payouts never write ledger tables (they call `ledger.Service`, journals
`source_system = payouts`, SIMULATED in a TEST ledger) and journals follow the payout state (`syncLedger`); the payout state
machine (`payout`) and the reservation machine are DB-guarded; an UNKNOWN payout keeps its reservation and is never
resubmitted (database attempts guard); FAILED never by assumption; approvals are FINANCE-only maker-checker with
linked-identity separation of duties (`app.actor_identities`) and approval rounds; the eligibility engine is a pure
function and every decision (22 checks) is stored; unconfigured limits fail closed (REVIEW + DUAL, no AUTO tier);
destination create/change/retire need a fresh step-up and invalidate approvals; owners see only coarse reason categories
(LR-008). Payout integration tests share the development database with the compose worker (which also submits, polls and
delivers webhooks): write them to tolerate it.

**Stage 10 — Immutable double-entry ledger (complete; awaiting acceptance; TEST ledger only).** What Stage 10 built: `docs/stage-10/implementation.md`
(prerequisites, architecture, chart of accounts, journal model, posting engine and rules, currency controls,
idempotency, reversals, balances, reconciliation, security review, tests, performance, known issues; ADR-042); Stage 11
starts from `docs/stage-handover/STAGE-10-TO-STAGE-11.md`. Binding Stage 10 rules: **only `internal/ledger` on the
`fundzim_ledger` pool (`DATABASE_LEDGER_URL`) writes ledger tables** — `fundzim_app` has no ledger privilege and no
other module may get one; the ledger database is designated TEST (`ledger.ledger_environment`), PRODUCTION is refused
(`ck_*_no_production_stage10`) and `ledger.ProductionPostingEnabled = false` — never change either without an ADR,
legal sign-off, an accounting review and stage acceptance; test payments are never posted (the `ledger.payments`
consumer records NOT_ELIGIBLE); payments and webhooks never write journals; journals are created POSTED and never
edited — corrections are maker-checker reversals/adjustments (`ledger_adjustments`); there is no free-form or public
posting endpoint; synthetic fixtures are TEST-only, labelled TEST_ONLY and never shown as real funds; no balance is
withdrawable; settlement reconciliation is NOT_PERFORMED until real settlement evidence exists; never weaken a ledger
trigger, CHECK or grant to make a test or benchmark pass; new money movements need a new posting rule (migration,
new version) and the Go chart must stay identical to `ledger.account_class_signature()`; the ledger perf harness
(`tests/perf/ledger`, tag `perf`) runs only against a scratch database (`fundzim_perf`), never the development
ledger (append-only). Retention of ledger records is LEGAL_REVIEW_REQUIRED (LR-012): never purge.

**Stage 9 — Zimbabwe payment provider integration readiness (complete; awaiting acceptance; no provider selected).**
What Stage 9 built: `docs/stage-9/implementation.md`
(prerequisites, legal review, provider confirmation register, evaluation, selection **DECISION_DEFERRED** (ADR-040),
capabilities, contracts, sandbox certification, webhook security, secret management (ADR-041), payment security,
reconciliation evidence, testing, known issues); Stage 10 starts from `docs/stage-handover/STAGE-9-TO-STAGE-10.md`.
Binding Stage 9 rules: **no real provider adapter exists and none may be added without a selection ADR superseding
ADR-040, sandbox credentials in the secret manager and the conformance suite (`internal/psp/psptest`) passing**;
never sign up with a provider or put provider credentials anywhere but the secret manager
(`secretref://fundzim/<environment>/…`, enforced by a DB CHECK); `PAYMENTS_EXECUTION_ENVIRONMENT` is one of
`DISABLED | LOCAL_SIMULATOR | PROVIDER_SANDBOX | PRODUCTION` per process — PRODUCTION is refused at startup and no
PRODUCTION donation intent or payment can exist (`ck_*_no_production_stage9`); every payment carries
`execution_environment` and `test_payment`, and only PRODUCTION payments may ever reach the ledger seam; provider
health comes from observations only (no observation = UNKNOWN); contradictory provider evidence is recorded in
`app.payment_provider_evidence` and escalated (`CONTRADICTORY_EVIDENCE`), never applied or discarded; provider errors
use the ten `psp.ErrCat*` categories and an outcome that may have reached the provider is never retried blindly;
production refuses `SECRETS_PROVIDER=env`, plain HTTP and static tokens. Local secrets live in the compose OpenBao
dev server (`fundzim-secrets`, seeded by `fundzim-secrets-init`). Stage 8 (`docs/stage-8/implementation.md`
(architecture, provider interface with the sandbox contract, state machine, webhooks, idempotency, refunds,
reconciliation, security, testing, performance, known issues alongside it; decisions in ADR-039) rules still hold.
Binding payment rules: the only provider is the sandbox **simulator**
(`PAYMENTS_SANDBOX_ENABLED`, refused in production); real execution is disabled by the Go constant
`psp.LiveExecutionEnabled = false`, the kill switch `payments.live_execution.enabled` and DB checks — never change
them without an ADR, legal sign-off and stage acceptance. `payments.apply()` is the only path that changes a payment's
status (edge + precedence + source rules, mirrored in SQL); a timeout or unknown outcome becomes `UNKNOWN`, never
`FAILED`; a browser return never confirms a payment; provider calls are committed `IN_FLIGHT` before the call and
never run inside a transaction; webhooks are verified, stored once (inbox) and dispatched through the outbox;
payment mutations require an `Idempotency-Key`; refund requests are maker-checker and cannot be executed; there is no
manual success override; provider secrets are `secretref://` references only. (Stage 8 had no ledger, fees or payouts; Stages 10–12
added TEST-only versions — see above.) The public campaign page still shows a disabled "Donations are not yet available." button.
Stage 7 rules still hold: public campaign data is `public, no-cache` + ETag and fetched `no-store` by the web app;
errors are always `no-store`; a handler's `Cache-Control` must not be overwritten; rate-limit errors carry their real
wait through `RetryAfter()`; campaign-family APIs use `{campaign_id}`, 428/412 for `If-Match` and
`{items, next_cursor}` lists (ADR-038 §7); a public report never changes a campaign's status; analytics are
first-party aggregate counters only. The real-stack Playwright suite (`apps/web/e2e-real`), the Go integration tests
(including `TestPayments*`, `TestLedger*`, `TestPayout*`, `TestFee*`, `TestRisk*`, `TestOperations*`, `TestNotifications*`, `TestDiscovery*`, `TestReconciliation*`, `TestSecurity*` and `TestFinancial*`) and container rebuilds share the development database: run them under
`flock /tmp/claude-1000/fundzim-stack.lock`. Never report CI as green without seeing the run. Gate on
`kyc.Service.Level`, enforce compliance restrictions through `compliance.Service.Restrictions`, and check
self-review with `kyc.Service.ActorIdentities` / `app.actor_identities()` (linked staff accounts). A
documentation-stage PASS is never permission to operate a live crowdfunding business or to move real money.
Do not start a stage until the user explicitly asks for it. Do not implement work belonging to a later stage
"while you're there". Each stage ends with a completion report (template in `docs/DEVELOPMENT.md`) and then
STOPS.

## Repository map

| Path | Purpose |
|---|---|
| `apps/web/` | Next.js 16 (App Router, TypeScript, Tailwind v4). **Presentation only.** Has its own `AGENTS.md` — read it: this Next.js version differs from older training data; consult `apps/web/node_modules/next/dist/docs/` before writing Next.js code. |
| `apps/api/` | Go entrypoints: `cmd/api` (HTTP API), `cmd/worker` (River jobs, outbox relay/delivery), `cmd/fundzimctl` (config check, migrations, `bootstrap-admins`, healthcheck). |
| `internal/` | Go code: `app/` (composition root), `platform/` (config, logging, errs, httpx, health, db, cache, storage, metrics, money, ids, crypto, authz, clock, ratelimit, idempotency, outbox, jobs, version) and domain modules `users`, `auth`, `organisations`, `audit`, `notifications`, `storage` (documents: quarantine, ClamAV scanning, SSE-C, download tickets), `kyc` (KYC/KYB cases, identities, review engine, verification policy — `fundzim_kyc` pool only), `beneficiaries`, `payouts` (payout destinations; Stage 11: withdrawal requests, eligibility engine, reservations, approvals, orchestration, reconciliation — TEST payouts only), `fees` (Stage 12: fee policies, provider fee evidence, pure calculation engine, quotes, assessments, adjustments, TEST fee journals through the ledger service, reconciliation, revenue reporting — TEST fees only), `risk` (signals, versioned model v1, routing decisions; Stage 13: configurable explainable rule engine, risk cases with immutable evidence, the central hold registry, TEST screening foundation and simulator, monitoring, reconciliation), `compliance` (compliance cases and the subject-restriction projection — `fundzim_compliance` pool only), `operations` (Stage 14: operations portal read models over per-module `OpsSnapshot`s, support cases, incidents, exception triage, access reviews, internal SLA targets — `app.ops_*` only), `verification` (HTTP orchestration; owns no tables) and `campaigns` (core: lifecycle, eligibility, review, publication, public read/search; subpackages `campaigns/media` — image upload, scan, re-encode, serving — and `campaigns/updates` — owner updates and moderation; no financial state), `ledger` (Stage 10: double-entry ledger, posting engine, adjustments, periods, integrity, reconciliation — `fundzim_ledger` pool only), `discovery` (Stage 16: search projection and indexing, featured placements, trending, recommendations, referral codes, engagement analytics, sitemap source — candidates only; campaigns re-checks every card), `reconciliation` (Stage 17: the `recon` schema — TEST statement imports, matching engine, settlement batches, exceptions, maker-checker resolutions, reports; changes payments/payouts/ledger only through their services), `psp` (Stage 8: provider registry, routing, webhook inbox, health, sandbox simulator in `psp/sandbox`; Stage 9:
execution environments, startup gate, error categories, derived health, adapter conformance suite in `psp/psptest`) and `payments` (Stage 8: donation intents, payments, state machine, refund requests, reconciliation references; no balances or postings). `archtest/` enforces the module import graph and table ownership — register every new module there; `deploytest/` (Stage 18) checks `compose.yaml`, the Dockerfiles and the CI workflow. One Go module (`github.com/Fatifizo/fundzim`) rooted at repo root. |
| `migrations/` | Executable goose SQL migrations, embedded and run by `fundzimctl migrate` as `fundzim_migrator` (`docs/development/migrations.md`). |
| `docs/` | Specifications. `docs/adr/` holds Architecture Decision Records. |
| `api/openapi/` | OpenAPI 3.1 contract for `/api/v1` (source of truth, ADR-026). |
| `design/sql/` | Stage 2 **non-executable** schema drafts + in-memory validation harness. Not migrations. |
| `deploy/` | `deploy/docker/`: API and web Dockerfiles, local Postgres role init, Garage (S3) config and init. The local stack is the root `compose.yaml`. |
| `scripts/`, `tests/` | `dev-env-init.sh` (local `.env` with generated secrets), `check-secrets.sh`; Stage 18: `backup/` (encrypted backup, manifest MAC, restore and failure drills), `dr/exercise.sh` (local DR exercise), `host/baseline-check.sh` (read-only host check); `tests/integration/` (build tag `integration`, runs against the local stack). |

## Before you change anything

1. Read the docs for the subsystem you are touching **before** editing it:
   money → `docs/MONEY.md`; ledger → `docs/LEDGER.md`; payments/webhooks/payouts → `docs/PAYMENTS.md`;
   auth/permissions/uploads → `docs/SECURITY.md`; KYC/personal data → `docs/DATA-CLASSIFICATION.md` and
   `docs/PRIVACY.md`; schema → `docs/DATABASE.md`; audit → `docs/AUDIT.md`; regulatory → `docs/COMPLIANCE.md`;
   UI → `docs/FRONTEND.md`; logging/metrics → `docs/OBSERVABILITY.md`; overall → `docs/ARCHITECTURE.md`.
   Stage 1 specifications refine these and win where they are more specific:
   operating model / custody → `docs/compliance/operating-model-decision.md`, `docs/ledger/settlement-and-custody-model.md`;
   payment & payout states → `docs/payments/transaction-lifecycle.md`, `docs/payments/payout-lifecycle.md`,
   `docs/payments/payout-eligibility-and-controls.md`; refunds/disputes → `docs/payments/refund-and-dispute-architecture.md`;
   currency → `docs/payments/currency-and-fx-policy.md`; providers → `docs/payments/provider-*.md`;
   identity → `docs/compliance/kyc-architecture.md`, `kyb-architecture.md`, `beneficiary-verification.md`,
   `docs/security/identity-data-protection.md`; AML/risk → `docs/compliance/aml-risk-framework.md` and siblings;
   staff controls → `docs/compliance/operational-controls.md`; evidence → `docs/compliance/audit-evidence-model.md`.
   Stage 2 (system design) refines all of the above and wins where more specific: module list, dependency
   graph, schemas, table names and owners → `docs/stage-2/design-baseline.md` (ADR-021/022); per-domain
   schema → `docs/database/`; architecture → `docs/architecture/`; HTTP contract → `api/openapi/fundzim-v1.yaml`
   and `docs/api/`. `design/sql/` holds NON-EXECUTABLE schema drafts (validated in-memory only) — never run
   them against a real database; executable migrations go in `/migrations` (goose, ADR-028).
   Stage 3 starts from `docs/stage-handover/STAGE-2-TO-STAGE-3.md`.
2. Check the relevant ADRs in `docs/adr/`.
3. If the change contradicts a doc or ADR, do not silently diverge. Propose a new ADR (or a superseding one)
   and get agreement first.

## Non-negotiable financial rules

1. **Never use floating point for money.** Not in Go, SQL, JSON, or TypeScript. Money is an `int64` count of
   minor units **plus** an ISO 4217 currency code (`amount_minor` + `currency`). In JSON, `amount_minor` is a
   string of digits. See `docs/MONEY.md`.
2. **Currency always travels with an amount.** A bare number is never money.
3. **Never combine currencies.** USD and ZiG (`ZWG`) are never summed, compared, netted, or converted
   implicitly. There is no automatic FX.
4. **Never mutate financial history.** Ledger, payment-event, and audit records are append-only. Corrections
   are new reversing/adjusting entries that reference the original.
5. **Never bypass the ledger.** No `campaign.balance += donation`, no `user.balance -= withdrawal`. Balances are
   derived from double-entry ledger entries (or projections updated in the same DB transaction and verifiable
   by full recompute). Invariant: per transaction and per currency, **total debits == total credits**.
6. **Never trust a browser redirect** (or any client claim) as payment confirmation. Only verified webhooks,
   authenticated provider status APIs, or reconciliation confirm payments.
7. **Every financial operation is idempotent**: payment creation, webhook processing, ledger posting, refunds,
   payout requests, payout processing. Back idempotency with database unique constraints, not just code.
8. **Every external callback is authenticated and verified** (signature, timestamp/replay window, dedupe).
9. **Unknown outcome ≠ failure.** A timeout or ambiguous provider response puts a payment or payout in
   `UNKNOWN`, resolved only by authoritative status query, webhook or reconciliation — never `FAILED` by
   assumption. A payout in `UNKNOWN` is **never resubmitted**; the provider reference (`payment_id`,
   `payout_id`, `refund_id`) is reused on every retry, and a retry after an authoritative failure is a new
   request with a new id.
10. **Enforce critical invariants in the database too** (constraints, triggers, restricted grants), not only
    in application code.
11. **Money states are distinct.** Captured ≠ settled ≠ available ≠ reserved ≠ paid out. Never present them as
    one "campaign balance". Funds become available only after a settlement match and a release posting
    (`docs/ledger/settlement-and-custody-model.md`).
12. **Payouts fail closed.** Every eligibility check must pass at request time and again immediately before
    submission. An unconfigured limit, missing verification, open hold or missing fundraising-authority
    evidence routes the payout to manual review — never to automatic approval.
13. **Stop and report** if you observe a financial invariant failure (imbalanced journal, negative available
    balance, reconciliation mismatch, duplicate posting). Do not "fix" it by editing data or loosening checks.

## Security and privacy rules

- Follow least privilege everywhere: DB roles, cloud IAM, staff permissions, service credentials.
  Admin roles do **not** implicitly get KYC document access or ledger-adjustment rights.
- Preserve user/organisation/tenant boundaries. Every read and write of a user-owned resource checks
  authorisation server-side (no IDOR). Never rely on unguessable IDs for access control.
- **Never log** passwords, OTPs, session tokens, API keys, private keys, webhook secrets, card numbers, CVV,
  full identity-document numbers or images, or full payout account numbers. Use the redaction allow-list.
- KYC data is **C3 RESTRICTED**: it lives in the `kyc` schema and the `private-kyc` bucket, is only touched by
  the `kyc` module, and every access is audited. Never treat KYC documents like campaign images.
- Never store raw card data. Use hosted/tokenised PSP flows only.
- Never commit secrets. `.env` files are git-ignored; only `.env.example` (placeholders) is tracked.
  Run `scripts/check-secrets.sh` (and gitleaks when installed) before committing.
- Do not remove, weaken, or bypass a security control to get past a development problem. Fix the problem.
- Sensitive staff actions require maker-checker (the initiator cannot approve): payouts above configurable
  thresholds or flagged by risk (every payout still passes automated policy checks), refunds, ledger
  adjustments, role grants, fee-configuration changes, limit/threshold changes, compliance overrides and
  exemptions, staff changes to payout destinations, and unfreezing campaigns
  (`docs/compliance/operational-controls.md`, ADR-017).
- Holds and freezes are new records plus ledger moves (payable → held). They never edit or delete history.
- Audit events never embed identity documents, KYC values or secrets; they reference evidence records
  (`docs/compliance/audit-evidence-model.md`, ADR-019).

## Regulatory rules

- Do not invent legal or regulatory conclusions. Do not assume FundZim may hold customer funds, operate a
  wallet/stored value, or act as a payment provider.
- **Operating model A (ADR-013):** a licensed PSP collects, holds and disburses; FundZim orchestrates and keeps
  records. Only FundZim's own platform-fee income may land in a FundZim bank account. Never integrate a
  provider in a way that settles donor funds to a FundZim account (merchant settlement = Model B in
  substance): provider routing must refuse that custody model. Moving to Model B needs a new ADR plus legal
  sign-off.
- Ledger balances are accounting records, not spendable stored value. Never build wallets, user-spendable
  balances or transfers between users.
- FundZim never converts currencies (only authorised dealers may). A rail that cannot settle in the donation
  currency is not offered for that donation.
- Mark every unresolved legal/regulatory dependency `LEGAL_REVIEW_REQUIRED` with an `LR-xxx` entry in
  `docs/compliance/open-legal-questions.md` (the authoritative register). Unconfirmed provider behaviour is
  `PROVIDER_CONFIRMATION_REQUIRED` with a `PCR-xxx` entry in `docs/payments/provider-questions.md`. Unknown
  regulatory requirements go in `docs/compliance/regulatory-requirements-register.md` (`REQ-xxx`).
- Never invent a statute, section, statutory instrument, threshold, rate, fee or provider capability. Cite a
  source you actually read, with URL and access date, or mark it unverified.
- Legal thresholds and limits are configuration (type REGULATORY / PROVIDER / INTERNAL_RISK, with source,
  approval owner, effective dates and review date) — never hard-coded.
- Never write "compliant", "licensed", "approved", "PCI compliant", "certified" or similar claims anywhere
  (code, docs, UI copy) unless the user supplies evidence.

## Engineering rules

- Prefer boring, proven technology. Modular monolith; no microservices, Kubernetes, or Kafka without an ADR
  that demonstrates the need.
- Respect the module dependency graph in `docs/stage-2/design-baseline.md` §3. A module's SQL may reference
  only tables it owns (§5). Adding a dependency or moving table ownership needs an ADR.
- Every state machine (payments, refunds, disputes, payouts, campaigns, compliance cases, KYC cases, ledger
  adjustments) is guarded in the database by `app.guard_transition` + `app.status_transitions`; keep Go and SQL
  edge lists identical.
- Go modules communicate only through each other's public service interfaces — never through another
  module's tables. No circular dependencies. Only `ledger` writes ledger tables.
- PostgreSQL is the only authoritative store. Redis is cache/rate-limit/coordination only and may be lost
  without data loss. Financial background jobs use the Postgres-backed queue.
- Timestamps: `timestamptz`, UTC internally; display in Africa/Harare or user preference. Inject the clock.
- IDs: UUIDv7. API: `/api/v1/`, standard envelope and stable `UPPER_SNAKE` error codes.
- Add tests with every feature. Financial code also needs invariant, idempotency, and concurrency tests.
- **Never disable, skip, or delete a test to make a build pass.** Never weaken an assertion to make it green.
- Do not claim tests pass unless you ran them in this session and saw them pass. Report failures with output.
- Update documentation in the same change when behaviour or architecture changes. Significant architectural
  changes require an ADR (`docs/adr/README.md`).
- Do not create placeholder files to make the repo look complete. Do not fake command output or features.

## Commands

`make help` lists targets; `make` is optional and **not installed** on the current dev machine — every target's
plain command is in `docs/DEVELOPMENT.md` §3. Go 1.27.3 (`go.mod` `go 1.27.3`) is user-local (`export PATH="$HOME/.local/go/bin:$PATH"`);
Node 24.21.0 via nvm. Docker needs docker-group access (`sudo usermod -aG docker $USER`, re-login, or `sg docker`).

```bash
./scripts/dev-env-init.sh                 # make env       — local .env with generated secrets (never overwrites)
docker compose up -d --build              # make up        — full local stack (all ports on 127.0.0.1)
go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...   # make test-go (never ./... — apps/web/node_modules has Go files)
npm --prefix apps/web test                # make test-web
set -a; . ./.env; set +a; go test -tags integration -count=1 ./tests/integration/...   # make test-integration
npm --prefix apps/web run test:e2e        # make test-e2e
gofmt -l apps internal migrations tests; go vet ./apps/api/... ./internal/... ./migrations/...; npm --prefix apps/web run lint; npm --prefix apps/web run typecheck   # make lint
CGO_ENABLED=0 go build -trimpath -o bin/ ./apps/api/cmd/...; npm --prefix apps/web run build                               # make build
set -a; . ./.env; set +a; go run ./apps/api/cmd/fundzimctl migrate up    # make migrate-up (status/down likewise)
./scripts/check-secrets.sh                # part of make security
docker compose down -v                    # make reset — DESTRUCTIVE: deletes local data volumes
```

## Stage completion

Each stage ends with a report using the template in `docs/DEVELOPMENT.md` ("Stage completion report"):
status, what was implemented, docs, ADRs, tree, controls, LEGAL_REVIEW_REQUIRED items, assumptions, tests
actually run and their results, secrets scan, known issues, technical debt, deviations, files changed, inputs
for the next stage, acceptance recommendation. Then stop and wait for the user.
