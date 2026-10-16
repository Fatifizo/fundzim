# Stage 13 — Risk architecture

Decision: [ADR-045](../adr/ADR-045-risk-engine-cases-holds-screening.md). Module: `internal/risk` (schema `risk`, app pool
`fundzim_app` / `fundzim_worker`); imports `platform` and `audit` only (a leaf, `internal/archtest`). Other modules reach it
through its public service: payments (`DonationHolds`), campaigns (`PublicationHolds`), payouts (`WithdrawalHolds`,
`MostSignificantScreening`, `HighestClassification`, limits). Linked identities and staff permissions come in through small
interfaces wired in `internal/app/risk.go` (`kyc.Service.ActorIdentities`, `auth.Service.StaffHasPermission`).

## 1. Flow

```
domain event (outbox) ──► risk.record_signal consumer ──► SignalFromEvent (validate, ids/codes only)
                                                            │
                                   RecordSignal (one transaction, per-subject advisory lock)
                                     ├─ insert risk_signals (dedupe: source_event_id + signal_type)
                                     ├─ Stage 5 model v1 (onboarding signals; unchanged)
                                     └─ Stage 13 evaluateSignal
                                          ├─ ACTIVE rule versions for the subject type (none → nothing)
                                          ├─ subject's signals in the longest rule window + risk state
                                          ├─ bands (approved limits or engineering defaults)
                                          ├─ EvaluateRules (pure, deterministic)
                                          ├─ risk_evaluations (+ explanation, inputs hash)  ── idempotent per trigger signal
                                          ├─ risk_subject_profiles (latest)
                                          ├─ automated actions: monitoring event | case (open/link) + evidence | temporary hold
                                          └─ outbox risk.evaluation_recorded (+ case/hold events), audit
```

Everything above happens in the signal's transaction: an error rolls back the whole signal (nothing partial), and the
outbox consumer redelivers. A redelivered event is a duplicate signal and changes nothing.

## 2. Components

| Component | Responsibility | File |
|---|---|---|
| Signal catalogue | type → category, severity, default confidence; Stage 13 event mapping | `catalog.go` |
| Rule engine | parse/validate declarative rules; evaluate; score; classify; explain | `engine.go` |
| Pipeline | evaluation, persistence, automated actions | `evaluate.go` |
| Rule administration | versions, maker-checker, lifecycle, simulation | `rules.go` |
| Case management | cases, assignment, evidence, signal reviews, decisions, escalation | `cases.go` |
| Hold registry | holds, activation, release, expiry, enforcement queries | `holds.go` |
| Screening | provider interface, requests, results, review, polling, callbacks | `screening.go` |
| Screening simulator | TEST provider (9 scenarios), signed callbacks | `screening_sandbox.go` |
| Reconciliation, reports | consistency checks; dashboard and quality counts | `reconcile.go`, `reports.go` |
| HTTP, jobs | staff API, screening webhook; periodic workers | `http.go`, `jobs.go` |

## 3. Data ownership and boundaries

`risk` owns every `risk.*` table (archtest). It never reads another module's tables: payment, payout and campaign facts
arrive as event payloads (ids, codes and amounts). It never writes a ledger table, never changes a payment, payout or
campaign status and never applies a compliance restriction. Screening tables live in the `risk` schema — a deviation from
the Stage 2 baseline (compliance ownership), recorded in ADR-045 §8; they hold synthetic references only.

## 4. Deviations from the Stage 2 design

| Design | Stage 13 | Why |
|---|---|---|
| `compliance.screening_*` tables and `/webhooks/screening/{vendor}` | `risk.risk_screening_*`, `/webhooks/screening/{provider}` | no provider and no identity data; the foundation is provider-neutral and TEST-only (move with a provider, KI-S13-05) |
| Monitoring alerts with dispositions | risk cases + monitoring events + signal reviews | one investigation object with maker-checker decisions |
| Hold release requests as a separate table | release fields and maker-checker on the hold | simpler, same separation of duties, enforced in the database |
