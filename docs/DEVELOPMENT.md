# FundZim — Development Guide

> **Status:** Stage 0. Only `apps/web` (a Next.js scaffold) contains code. The Go API, database, local
> services and test suites arrive from Stage 3. This guide says exactly what works today and what is pending —
> it does not describe features that do not exist.

Related: [CLAUDE.md](../CLAUDE.md) (binding engineering rules) · [ARCHITECTURE.md](ARCHITECTURE.md) ·
[TESTING.md](TESTING.md) · [SECURITY.md](SECURITY.md) · [ROADMAP.md](ROADMAP.md) · [adr/README.md](adr/README.md)

---

## 1. Prerequisites

| Tool | Version | Needed from | Status on the current dev machine (2026-10-08) |
|---|---|---|---|
| Git | any recent | Stage 0 | Installed. **No `user.name`/`user.email` configured yet.** |
| Node.js via nvm | Node 24 LTS (v24.21.0) | Stage 0 (`apps/web`) | Installed (nvm v0.40.8, default alias `lts/*`). |
| npm | 11.x (ships with Node 24) | Stage 0 | Installed. |
| GNU make | 4.x | Stage 0 (convenience) | **Not installed.** `sudo apt install make` |
| gitleaks | latest stable | Stage 0 (`make security`), required in CI | **Not installed.** Install from the official GitHub releases (verify checksum). |
| Go | current stable, pinned in `go.mod` toolchain directive | Stage 3 | **Not installed.** Install from go.dev official tarball (verify checksum) or a pinned version manager. |
| Docker Engine + Compose v2 | recent | Stage 3 (local Postgres/Redis/MinIO/Mailpit/ClamAV) | Installed. Confirm your user can run `docker` without sudo. |
| golangci-lint, govulncheck | pinned versions | Stage 3 | Not installed. Installed via `go install` at pinned versions in Stage 3. |

Install Node in a new shell with:

```bash
export NVM_DIR="$HOME/.nvm"; . "$NVM_DIR/nvm.sh"   # already in ~/.bashrc
nvm use default
node -v   # v24.x
```

Without `make`, every web target can be run directly: `npm --prefix apps/web run <dev|build|lint>`.

---

## 2. Repository layout

```
/
├── CLAUDE.md           Binding engineering rules (read first)
├── README.md           Project overview
├── Makefile            Standard developer commands
├── .env.example        Placeholder-only configuration template (tracked)
├── .editorconfig       Editor conventions (tabs for Go/Make, 2 spaces elsewhere)
├── .gitignore
├── apps/
│   ├── web/            Next.js 16 (App Router, TS, Tailwind v4) — presentation only
│   └── api/            Go API + worker entrypoint (README only until Stage 3)
├── internal/           Go domain modules of the modular monolith (README only until Stage 3)
├── migrations/         SQL migrations (README only until Stage 2/3)
├── docs/               Specifications; docs/adr/ = Architecture Decision Records
├── deploy/             Deployment config (local compose arrives Stage 3)
├── scripts/            Developer scripts (check-secrets.sh)
└── tests/              Cross-cutting suites: e2e, financial, security, perf (from Stage 3+)
```

A single Go module will live at the repository root (`go.mod`, created in Stage 3) so that `apps/api` and
`internal/` share one module and Go's `internal/` visibility rules apply. Unit tests live beside the code;
`tests/` holds only cross-cutting suites. See [ARCHITECTURE.md](ARCHITECTURE.md) for module boundaries.

---

## 3. Standard commands

Run `make help` for the live list. What each target does **today**:

| Target | Stage 0 behaviour | From Stage 3 (once `go.mod` exists) |
|---|---|---|
| `make help` | Lists targets. | Same. |
| `make web-install` | `npm ci` in `apps/web` (reproducible install from lockfile). | Same. |
| `make dev` | Runs the Next.js dev server on http://localhost:3000. Prints a note that the API and local services do not exist yet. | Will also start local services (compose) and the API. |
| `make test` | Prints that no Go module and no web test runner exist yet and that **nothing was tested**. Exits 0 without claiming success. | Runs `go test -race ./...` plus web tests once configured. |
| `make lint` | ESLint on `apps/web`. Go skipped (prints why). | Adds `go vet` and `golangci-lint`. |
| `make build` | Production build of `apps/web`. Go skipped. | Adds Go binary build into `bin/`. |
| `make migrate` | Prints that no migration tool exists yet and **exits 1** (so scripts cannot mistake it for success). | Applies migrations with the tool chosen in Stage 2/3. |
| `make security` | `scripts/check-secrets.sh`, gitleaks if installed (otherwise a warning), `npm audit --audit-level=high` (currently fails — see §9). | Adds `govulncheck`. CI adds Trivy image scans. |
| `make clean` | Removes build artefacts (`bin`, `dist`, `.next`, `out`, tsbuildinfo). Never touches `.env` or data volumes. | Same. |

Rule: targets never fake output. A target that cannot do its job yet says so.

---

## 4. Environment configuration

- `.env.example` (tracked) lists every variable, grouped: APP, DATABASE, REDIS, STORAGE, AUTH, EMAIL, SMS,
  PAYMENTS, OBSERVABILITY, SECURITY. It contains **placeholders only**.
- For local development: `cp .env.example .env` and replace `<set-locally>` values with locally generated
  values (e.g. `openssl rand -base64 32`). Never reuse a local value anywhere else.
- `.env` and all `.env.*` (except `.env.example`) are git-ignored. `scripts/check-secrets.sh` flags any tracked
  env file.
- `NEXT_PUBLIC_*` variables are compiled into the browser bundle and are public. Never name a secret
  `NEXT_PUBLIC_*` ([FRONTEND.md](FRONTEND.md) §8).
- In Stage 0 nothing reads `.env`; the Go API reads it from Stage 3 via a typed config loader that validates
  every variable at startup and refuses unsafe combinations in production (e.g. `SMS_PROVIDER=log`,
  `sslmode=disable`, local field-encryption keys, the sandbox payment provider, or any security control
  switched off: `MALWARE_SCAN_ENABLED=false`, `AUDIT_HASH_CHAIN_ENABLED=false`, `RATE_LIMIT_ENABLED=false`).
  The full list is in [SECURITY.md](SECURITY.md) §14.
- **Staging/production secrets** come from a secret manager and are injected at runtime. They are never
  committed, never baked into images, never printed in logs or CI output, and are rotated on staff departure
  or suspected exposure. See [SECURITY.md](SECURITY.md).

---

## 5. Branching, commits and reviews

- `main` is protected (once hosted): no direct pushes, PR required, CI green required, ≥1 reviewer approval;
  ≥2 approvals (including the tech lead) for changes touching `internal/ledger`, `internal/payments`,
  `internal/payouts`, `internal/fees`, `internal/reconciliation`, `internal/kyc`, `internal/auth`,
  `migrations/`, or security controls.
- Branch names: `stage-N/<short-topic>`, `fix/<topic>`, `docs/<topic>`, `chore/<topic>`.
- **Conventional Commits**: `feat(campaigns): …`, `fix(ledger): …`, `docs(adr): …`, `chore(web): …`,
  `test(payments): …`, `refactor(…)`, `ci: …`, `security(…)`. Breaking changes use `!` and a `BREAKING CHANGE:`
  footer.
- One logical change per commit; migrations in their own commit with the code that needs them.
- Commits must not contain secrets, real PII, generated build output, or `.env` files. Run `make security`
  before pushing.
- Never rewrite published history on `main`. Never force-push shared branches.
- PR description: what/why, linked stage and ADRs, `LR-xxx` items touched, test evidence (commands run and
  results), screenshots for UI, migration notes, rollback notes.

---

## 6. Code style

**Go (from Stage 3)**
- `gofmt`/`goimports` formatting is mandatory; `go vet` and `golangci-lint` (config committed in Stage 3)
  must pass.
- Errors wrapped with context (`fmt.Errorf("…: %w", err)`); domain errors are typed and mapped to stable API
  error codes at the HTTP boundary only.
- `context.Context` first parameter for I/O; no global mutable state; clock and ID generator injected.
- Logging via `log/slog` with the redaction rules in [OBSERVABILITY.md](OBSERVABILITY.md).
- No `float32`/`float64` in any money path — enforced by a lint rule/arch test in Stage 3.
- Module boundaries: import only another module's public package; arch test fails on cycles or on imports
  of another module's internals.

**TypeScript / web**
- ESLint (Next.js config) must pass; `tsc --noEmit` strict mode. Prettier added with the first real UI work.
- No `any` in money, auth or API client code. Amounts are `string` (minor units) end-to-end.
- Follow [FRONTEND.md](FRONTEND.md). Read `apps/web/AGENTS.md`: this Next.js version differs from older
  documentation — consult `apps/web/node_modules/next/dist/docs/` before writing Next.js code.

**SQL**
- Lowercase snake_case identifiers; explicit constraints named per [DATABASE.md](DATABASE.md) (`pk_<table>`, `uq_<table>_<cols>`, `ck_<table>_<rule>`, `fk_<table>_<column>`);
  `timestamptz` only; money as `amount_minor BIGINT` + `currency CHAR(3)`. See [DATABASE.md](DATABASE.md).

**Formatting** — `.editorconfig`: UTF-8, LF, final newline, 2-space indent, tabs for Go and Makefiles.

---

## 7. Architecture Decision Records

ADRs live in `docs/adr/` (index: [adr/README.md](adr/README.md); template: `ADR-000-template.md`).

When to write one: any change to a decision in an existing ADR, a new core technology/dependency with
lasting impact, a change to module boundaries, money/ledger/payment/KYC/security architecture, data storage
location, or anything a future engineer would ask "why did we do this?" about.

How:
1. Copy `ADR-000-template.md` to `ADR-NNN-short-title.md` with the next free number.
2. Fill every section: Status, Context, Decision, Consequences, Alternatives, Security implications,
   Financial implications (where applicable).
3. Status starts `Proposed`; becomes `Accepted` after review. A replaced ADR is set to
   `Superseded by ADR-NNN` — never edit an accepted ADR's decision in place.
4. Add it to the index and update affected docs in the same PR.

---

## 8. Definition of Done

A change is done only when **all** apply:

1. Implements the agreed scope for its stage and nothing from later stages.
2. Consistent with CLAUDE.md, the relevant docs and ADRs — or ships a new/superseding ADR.
3. Tests added per [TESTING.md](TESTING.md); all suites pass locally and in CI; no test skipped, deleted or
   weakened.
4. Financial changes: idempotency, DB-level invariants, concurrency and failure tests present; invariant
   checker passes.
5. Security: authorization checks and IDOR tests on every new endpoint; inputs validated; no secrets/PII in
   logs; sensitive actions audited.
6. Lint, type checks, secrets scan and dependency scan pass (or exceptions documented with expiry).
7. Docs updated (including `.env.example` for new config, and the `LR` register for new regulatory
   dependencies).
8. Migrations reviewed, forward-only, and tested on empty and existing schemas.
9. Accessibility checks pass for UI changes (axe + keyboard walkthrough).
10. Reviewed and approved per §5.

---

## 9. Known environment issues (Stage 0)

- `npm audit` in `apps/web` reports 5 high-severity advisories, all in the `braces` → `micromatch` →
  `fast-glob` chain under `eslint-config-next` (development tooling, not shipped to browsers). `npm audit fix
  --force` proposes a breaking change; not applied. Re-evaluate on the next `eslint-config-next` update.
  Consequence: `make security` currently exits non-zero.
- `make`, Go and gitleaks are not installed on the current machine (§1).
- Git identity is not configured; nothing has been committed yet.

---

## 10. Stage completion report template

Every stage ends with this report, then work **stops** until the user accepts the stage and asks for the
next one. Do not claim tests that were not run.

```markdown
# FUNDZIM STAGE N COMPLETION REPORT

## Stage
Stage N — <Stage name from docs/ROADMAP.md>

## Status
PASS / PARTIAL / FAIL

## Executive Summary
Concise description of what was completed.

## Repository
Repository path:
Git status:
Current branch:
Commit hash, if committed:

## Implemented
Detailed list.

## Documentation Created / Updated
Every significant document and its purpose.

## Architecture Decisions
ADRs created, accepted, or superseded.

## Repository Structure
Resulting high-level tree.

## Financial Safety Controls
Controls defined or implemented (and where enforced: app / DB / both).

## Security Controls
Controls defined or implemented.

## Compliance / Legal Questions
Every LEGAL_REVIEW_REQUIRED item touched or blocking (LR IDs).

## Assumptions
Important assumptions made.

## Tests / Validation Performed
Exact commands run and what they covered. Do not claim tests that were not run.

## Validation Results
Passed:
Failed:
Warnings:

## Secrets Scan
How the repository (and history) was checked for credentials, and the result.

## Known Issues

## Technical Debt
Debt intentionally accepted, with reason and planned resolution stage.

## Deviations From Stage Prompt
Each deviation and why. If none: None.

## Files Changed
Concise list.

## Recommended Stage N+1 Inputs
Information to research or decide before the next stage.

## Stage N Acceptance Recommendation
PASS / PASS WITH CONDITIONS / FAIL — with reasons.
```
