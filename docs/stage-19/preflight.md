# Stage 19 — Preflight: repository, environment and governance

Recorded 2026-10-23 before any change (brief §3). Every value below was observed in this session.

## 1. Repository

| Check | Observed |
|---|---|
| `git status --short --branch` | `## stage-18/security-hardening...origin/stage-18/security-hardening`, clean |
| `git rev-parse HEAD` | `801babef97c0a21891a48f30580f7bda32485755` |
| `git remote -v` | `origin git@github-fatifizo:Fatifizo/fundzim.git` (fetch, push) |
| `git diff --stat`, `git diff --check` | empty |
| Remote branches | `main` and `stage-1/…` to `stage-18/security-hardening`; no `stage-19/*` existed |
| `main` | `a552de2` (Stage 0 foundation); `stage-18/security-hardening` is 88 commits ahead |
| Integration strategy | stacked stage branches: each stage branches from the previous stage's head; nothing has been merged to `main` and no pull request exists (`docs/DEVELOPMENT.md` §5: `main` is protected once hosted; PR + green CI + reviewers) |
| Stage 17 code present | yes — `internal/reconciliation`, `recon` schema, Stage 17 commits up to `f5e9d00` are ancestors of `801babe` |
| Stage 18 code present | yes — ADR-050, `internal/audit/chain.go`, `operations/security.go`, `scripts/backup`, migration `20261023090000_security_hardening.sql` |
| Stage 18 remote CI | run **39984705098** on `801babe`: all 8 jobs `success` (observed through the API before this stage; see the Stage 18 report). Run 39961618431 on `7ae778f` is the one recorded in the acceptance register |

Stage branch: `stage-19/financial-concurrency`, created from `801babe` (`git checkout -b`); nothing was merged and no
history was rewritten.

## 2. Governance

`docs/governance/stage-acceptance-register.md`: Stages 0–18 **NOT_RECORDED**. A Stage 18 recommendation of PASS WITH
CONDITIONS is not owner acceptance. Stage 19 proceeds as controlled non-production engineering (brief §3.3) on the
owner's explicit request; it accepts nothing retrospectively and Stage 19 itself is not accepted by this work.

## 3. Environment isolation (brief §3.4)

| Requirement | Evidence |
|---|---|
| Disposable / isolated database | local compose PostgreSQL 17.11 on 127.0.0.1 (`fundzim-postgres`), development data only; ledger designated TEST (`ledger.ledger_environment`); CI uses its own throwaway stack per job |
| No production resources reachable | every `DATABASE_*_URL` points at `127.0.0.1:5432/fundzim`; secrets come from the local OpenBao dev server (`secretref://fundzim/development/…`, `…/sandbox/…`) seeded by `fundzim-secrets-init`; no provider credential exists anywhere (ADR-040, no provider selected) |
| Outbound provider execution disabled | `psp.LiveExecutionEnabled = false`, `payouts.LiveExecutionEnabled = false`, kill switches off, PRODUCTION refused at startup and by CHECKs (re-verified by `TestFinancialGateConstantsStayOff`, `TestSecurityFinancialGatesInDatabase` in this stage's runs) |
| Test fixtures distinguishable | simulator payments `is_simulated` / `execution_environment` LOCAL_SIMULATOR or PROVIDER_SANDBOX, `test_payment`; synthetic journals `source_system = synthetic`, `evidence_class = SIMULATED`, metadata `fixture = TEST_ONLY`; payouts `ck_payout_requests_test_only_stage11` |
| Workers cannot consume production queues | the compose worker consumes the local River queues of the local database only |
| Object storage | local Garage (`fundzim-storage`), development buckets |
| Destructive cleanup restricted | Stage 19 deletes nothing; barrier and fault tests lock and terminate only backends blocked by a row the test itself created; scratch databases `fundzim_perf_s19a/b` were created for the ledger harness and contain only harness data |
| Shared accounting periods | Stage 19 tests never open, close or reopen a period (KI-S18-24 remains a known isolation limit of `TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings`; see known-issues.md) |
| Load and fault tests | opt-in (`FUNDZIM_IT_S19_LOAD`, `FUNDZIM_IT_WORKER_KILL_CMD`), run only against this local stack under `flock /tmp/claude-1000/fundzim-stack.lock`; never against a shared or third-party environment |

Host: 16 vCPU Intel Xeon Platinum 8160, 30 GiB RAM, Ubuntu 25.10, Docker; PostgreSQL container limited to 1 GiB memory.
The superuser connection used for fault injection (`pg_terminate_backend`) is the local stack's `postgres` role from the
generated `.env`; it is used only to terminate backends blocked by the test's own row lock.

## 4. Documents inspected before changes

CLAUDE.md, README.md, docs/ARCHITECTURE.md, docs/SECURITY.md, docs/ROADMAP.md, docs/stage-handover/STAGE-17-TO-STAGE-18.md,
docs/stage-handover/STAGE-18-TO-STAGE-19.md, docs/stage-18/known-issues.md, docs/stage-18/security-testing.md, ADR-043,
ADR-049, ADR-050, docs/governance/stage-acceptance-register.md, `.github/workflows/ci.yml`, the payouts, payments, ledger
and reconciliation code and migrations they cite.

## 5. Prerequisite result

| Prerequisite | Status |
|---|---|
| Stage 17 and 18 implementation present | SATISFIED |
| Stage 18 CI evidence | SATISFIED (run 39984705098, all 8 jobs, `801babe`) |
| Isolated test environment | SATISFIED_WITH_CONDITIONS (local single-node stack shared by the compose worker; tests scope to their own records) |
| Formal acceptance of earlier stages | NOT_RECORDED (governance condition, not an engineering blocker for non-production work) |
| Provider, accounting, legal decisions | BLOCKED for production (unchanged); not needed for simulator-based validation |
