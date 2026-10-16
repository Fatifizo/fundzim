# Stage 13 — Security review and threat model

Scope: `internal/risk`, the risk integrations (payments, campaigns, payouts, auth, fees, compliance), the risk API, the
screening webhook and the risk admin UI. Reviewed against [SECURITY.md](../SECURITY.md),
[operational-controls.md](../compliance/operational-controls.md) and the Stage 13 brief §60–§61. Tests: [testing.md](testing.md).

## 1. Controls

| Control | Implementation |
|---|---|
| Authentication / RBAC | staff routes only (`httpx.Permission`), non-staff → 404; `risk.operations.view` for reading; separate maker and checker permissions for rules, holds, cases and screening; ADMIN-only simulator control (registered only when the simulator is enabled) |
| Step-up | every risk mutation permission `requires_step_up`; checkers' step-up time recorded (rule approval, case decision, hold release, screening review) |
| Maker-checker incl. linked accounts | rule approval/rejection/reactivation, hold activation/release, case decisions, screening review: Go (`linked()` over `kyc.ActorIdentities`) **and** database guards (`app.actor_identities`) |
| Conflict of interest | no case, hold or screening about yourself or a linked account (Go and guards) |
| Immutability | rule versions after submission, evaluations, evaluation actions, evidence, signal reviews, events, access log, screening results: triggers refuse UPDATE/DELETE/TRUNCATE; state machines via `app.guard_transition` |
| Rule safety | declarative JSON with a closed grammar and bounds; no code, regex or SQL; validated on every evaluation |
| Idempotency | signals unique per source event and type; one evaluation per trigger signal; one open case per subject and type; one open hold per type and subject; screening results unique per provider event; idempotency keys on POST creations |
| Callback security | HMAC-SHA256 over the exact bytes, constant-time compare, 5-minute replay window, strict body (unknown fields refused), 64 KiB limit, per-IP rate limit `risk.screening_webhook.ip`; rejections never applied and recorded by body hash only |
| Fail closed | hold-registry and risk-fact errors stop donations, publication and payouts; screening errors/timeouts never clear |
| Data minimisation | ids/codes/amounts in signals; keyed per-user UA hash; no IP or fingerprint ([privacy.md](privacy.md)) |
| No fraud claims | CHECKs on rule codes and names, hold reasons, event types, case outcomes and evaluation explanations |
| Financial isolation | risk writes no ledger table, never changes payment/payout/campaign state; TEST-only environment CHECKs |

## 2. Threat model (STRIDE summary)

| Threat | Example | Mitigation | Residual |
|---|---|---|---|
| Spoofing | forged screening callback | HMAC + replay window + strict body; unknown reference 404 | simulator key is derived from the platform key (rotation with it) |
| Spoofing | staff acting as the subject's linked account | linked-identity checks in Go and DB | depends on links being recorded (Stage 4/5 process) |
| Tampering | editing a rule after approval, evidence, scores | immutability triggers; definition hash bound to the approval; evidence hashes recomputed by reconciliation | the migrator role can still disable triggers (operational control) |
| Tampering | injecting code through a rule | closed grammar, unknown fields refused | — |
| Repudiation | who released a hold / approved a rule | `risk_events`, audit hash chain, recorded step-up times | — |
| Information disclosure | owners learning they are investigated (tipping-off, LR-008) | owners see coarse categories only; risk routes 404 for non-staff | coarse refusals still reveal "on hold" |
| Information disclosure | PII in signals or logs | fact allow-list; hashed UA; no IP | free-text staff notes (policy + UI warning) |
| Denial of service | rule explosion, callback floods | bounded definitions and evaluation inputs; webhook rate limit | a broken ACTIVE rule blocks signal recording for its subject type until retired (visible: consumer retries, reconciliation finding) |
| Elevation of privilege | self-approval, self-release, activation bypass | maker-checker in Go and DB; state machine forbids skipping approval | two colluding staff (operational controls) |
| Abuse of automation | mass automated holds | automated holds limited to payout-protective types, ≤ 7 days, always with a case; rules need two approvers | a bad approved rule can hold many payouts for up to 7 days (suspend the rule; release via cases) |
| Discrimination / unfairness | rules on proxies | facts allow-list excludes personal attributes; explanations stored; quality counts | no fairness review process yet (KI-S13-10) |

## 3. Findings fixed during the stage

* Rule names containing "fraud" would have made every evaluation fail the explanation CHECK (signal recording stuck): now
  refused in Go and by `ck_risk_rules_name`.
* Evidence hashes were computed over Go struct order and could not be verified from `jsonb`: canonical hashing
  ([case-management.md](case-management.md) §5).
* Staff audit records lacked the actor id when the actor type was set explicitly (rejected by `ck_audit_events_actor_id`).
* A confirmed screening match on a new (OPEN) case could not escalate (no OPEN → ESCALATED edge): it now triages first.

## 4. Open risks

KI-S13-* in [known-issues.md](known-issues.md); notably: no production screening (BLOCKED), no fairness review, rules
depend on human configuration quality, migrator-level tampering is an operational control.
