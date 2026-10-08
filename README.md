# FundZim

**Fund anyone in Zimbabwe, from anywhere.**

FundZim is a Zimbabwe-first, donation-based crowdfunding platform. Individuals and verified organisations in
Zimbabwe raise funds for medical, education, funeral, emergency, community and other approved causes; donors
in Zimbabwe and abroad contribute using payment methods that suit them (target rails include EcoCash,
OneMoney, InnBucks, O'Mari, ZimSwitch, local banks, Visa and Mastercard) through licensed payment service
providers.

FundZim is **not** an investment, lending, equity or rewards platform, and it is not a wallet or a payment
provider.

> **Status: Stage 0 — Product Definition & Engineering Foundation.** The repository contains the product
> specification, architecture, security, financial and compliance foundations, and a scaffolded Next.js app.
> There is no backend, no payment integration, and no real money handling yet. Nothing here is a claim of
> regulatory approval or compliance — open legal questions are tracked in
> [`docs/COMPLIANCE.md`](docs/COMPLIANCE.md).

## Stack

| Layer | Choice |
|---|---|
| Web | Next.js 16 · React 19 · TypeScript · Tailwind CSS v4 (presentation only) — [ADR-003](docs/adr/ADR-003-nextjs-frontend.md) |
| API | Go modular monolith, REST `/api/v1/` — [ADR-001](docs/adr/ADR-001-modular-monolith.md), [ADR-002](docs/adr/ADR-002-go-backend.md) |
| Data | PostgreSQL (authoritative, incl. double-entry ledger) — [ADR-004](docs/adr/ADR-004-postgresql.md), [ADR-006](docs/adr/ADR-006-double-entry-ledger.md) |
| Cache / rate limiting | Redis (never authoritative) |
| Files | S3-compatible storage; public media and private KYC strictly separated — [ADR-008](docs/adr/ADR-008-s3-object-storage.md), [ADR-009](docs/adr/ADR-009-kyc-storage-separation.md) |
| Runtime | Containers; Docker Compose locally — [ADR-012](docs/adr/ADR-012-container-first.md) |

## Repository layout

```
apps/web/      Next.js frontend (scaffold)
apps/api/      Go API + worker entrypoint (Stage 3)
internal/      Go domain modules (Stage 3)
migrations/    SQL migrations (Stage 2/3)
docs/          Specifications, roadmap, ADRs
deploy/        Local/production deployment config (Stage 3+)
scripts/       Developer scripts (secret scan)
tests/         Cross-cutting e2e, financial-invariant and performance suites
```

## Getting started

Prerequisites and full setup: [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

```bash
cp .env.example .env          # placeholders only; nothing reads it until Stage 3
make web-install              # or: npm --prefix apps/web ci
make dev                      # or: npm --prefix apps/web run dev  →  http://localhost:3000
make help                     # all targets
```

## Documentation

| Document | What it covers |
|---|---|
| [CLAUDE.md](CLAUDE.md) | Binding engineering rules for humans and AI assistants |
| [PRODUCT](docs/PRODUCT.md) | Vision, scope, users, campaign lifecycle, KYC levels |
| [ROADMAP](docs/ROADMAP.md) | Stages 0–20 with dependencies and acceptance criteria |
| [ARCHITECTURE](docs/ARCHITECTURE.md) | System design, modules, boundaries, API conventions |
| [MONEY](docs/MONEY.md) | Money value model — integer minor units, currencies, rounding |
| [LEDGER](docs/LEDGER.md) | Double-entry ledger design and invariants |
| [PAYMENTS](docs/PAYMENTS.md) | Provider abstraction, payment states, webhooks, idempotency, payouts |
| [DATABASE](docs/DATABASE.md) | Database standards, roles, constraints, migrations |
| [SECURITY](docs/SECURITY.md) · [THREAT-MODEL](docs/THREAT-MODEL.md) · [AUDIT](docs/AUDIT.md) | Security baseline, threats, audit logging |
| [COMPLIANCE](docs/COMPLIANCE.md) | Regulatory boundary and the `LEGAL_REVIEW_REQUIRED` register |
| [DATA-CLASSIFICATION](docs/DATA-CLASSIFICATION.md) · [PRIVACY](docs/PRIVACY.md) | Data classes, handling, privacy architecture |
| [OBSERVABILITY](docs/OBSERVABILITY.md) · [FRONTEND](docs/FRONTEND.md) · [TESTING](docs/TESTING.md) · [DEVELOPMENT](docs/DEVELOPMENT.md) | Operating, building and testing standards |
| [ADRs](docs/adr/README.md) | Architecture Decision Records |

## Licence

Not yet decided. All rights reserved by the project owner until a licence is chosen.
