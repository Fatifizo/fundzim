# Stage 9 prerequisite assessment

Date: 2026-10-13. Branch `stage-9/provider-integrations`, created from `stage-8/payments` at `e5490cf` (CI run
38364505957: all 8 jobs green). Inputs read: `CLAUDE.md`, `README.md`, `docs/ARCHITECTURE.md`, `docs/SECURITY.md`,
`docs/ROADMAP.md`, `docs/PAYMENTS.md`, the Stage 1 payment and compliance documents
(`docs/payments/provider-capability-matrix.md`, `provider-comparison.md`, `provider-questions.md`,
`docs/compliance/open-legal-questions.md`), all Stage 8 documents, ADR-039, `docs/stage-handover/STAGE-8-TO-STAGE-9.md`
and the Stage 8 code (`internal/psp`, `internal/payments`).

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**. No acceptance record,
legal opinion, provider confirmation or provider-selection approval was fabricated.

## 1. Governance

| Prerequisite | Status | Evidence / condition |
|---|---|---|
| Formal acceptance of Stages 1–8 | **NOT_VERIFIED** | [stage-acceptance-register.md](../governance/stage-acceptance-register.md): every row `NOT_RECORDED`. The Stage 8 handover says "Do not start Stage 9 until the owner accepts and asks for it"; the owner asked (Stage 9 master prompt) but did not record acceptance. Stage 9 proceeds as **non-production engineering preparation**; nothing in this stage is presented as accepted |
| Owner request to start Stage 9 | SATISFIED | The owner supplied the Stage 9 master prompt |
| Provider-selection approval | **BLOCKED** | No owner decision exists. [ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md) records `DECISION_DEFERRED` |
| Legal sign-off for payment execution | **BLOCKED** | Every LR item in [legal-review.md](legal-review.md) is OPEN |
| Production payment execution | **BLOCKED** (by design) | `psp.LiveExecutionEnabled = false` (constant), kill switch off, Stage 9 production execution guard ([payment-security.md](payment-security.md) §2) |

## 2. Stage 8 capabilities Stage 9 builds on

| Capability | Status | Evidence |
|---|---|---|
| Adapter interface `psp.Adapter` | SATISFIED | `internal/psp/psp.go`; implemented by the simulator; documented in [../stage-8/provider-interface.md](../stage-8/provider-interface.md) |
| Provider registry, capabilities, accounts | SATISFIED | `payment_providers`, `provider_capabilities` (+ `provider_capabilities_current`), `provider_accounts` with `secretref://` CHECK |
| Payment state machine and `apply()` | SATISFIED | 42 edges mirrored in SQL; UNKNOWN never becomes FAILED by assumption |
| Webhook inbox, dedupe, dispatch, dead letter | SATISFIED | `internal/psp/inbox.go`; rejection log without payloads |
| Idempotency (required keys + DB backstops) | SATISFIED | Stage 8 idempotency.md |
| Status polling and sweep | SATISFIED | `internal/payments/poll.go` |
| Refund requests (no execution) | SATISFIED | `refund_requests` stop at APPROVED |
| Reconciliation references | SATISFIED_WITH_CONDITIONS | References only; no statement source (PCR-007/016, Stage 17) |
| Sandbox simulator | SATISFIED | `internal/psp/sandbox`, scenario suffixes, HMAC webhooks |
| Secret resolution | **SATISFIED_WITH_CONDITIONS → addressed in Stage 9** | Stage 8 had only `psp.EnvResolver` (KI-S8-04). Stage 9 adds a Vault-KV-v2-compatible resolver ([ADR-041](../adr/ADR-041-secret-management.md), [secret-management.md](secret-management.md)) |
| Staff payment UI | SATISFIED | `/admin/payments/**` |
| CI (8 jobs incl. real-stack E2E) | SATISFIED | Run 38364505957 |
| Ledger (Stage 10) | **NOT PRESENT** (expected) | No ledger postings in Stage 9 |
| Sanctions screening provider | **BLOCKED** | `SCREENING_PROVIDER_NOT_SELECTED` (KI-S5-04) |

## 3. External prerequisites for real provider integration

| Prerequisite | Status | Evidence |
|---|---|---|
| A selected provider | **BLOCKED** | ADR-040 `DECISION_DEFERRED` |
| A signed merchant / partner agreement | **NOT_VERIFIED** (none supplied) | — |
| Provider sandbox credentials | **BLOCKED** | None supplied to this repository or session. Claude Code must not open accounts with third parties on the owner's behalf |
| Written provider answers to PCR-001..007, 009, 010, 017, 018 | **BLOCKED** | All UNCONFIRMED ([provider-confirmation-register.md](provider-confirmation-register.md)) |
| Official provider documentation | SATISFIED_WITH_CONDITIONS | Public developer documentation of Paynow, Pesepay and Payonify was read on 2026-10-13 ([provider-evaluation.md](provider-evaluation.md)). Public documentation is **DOCUMENTED_BUT_UNVERIFIED** until exercised in the provider's sandbox |
| Production secret manager | SATISFIED_WITH_CONDITIONS | Decision and working integration in Stage 9 (ADR-041); hosting of the manager itself is a Stage 18/20 decision |

## 4. Consequence

Stage 9 delivers **engineering preparation** (secret management, execution environment isolation, production guard,
error normalisation, health from observations, evidence capture, conformance suite, dashboards, public method
availability endpoint) and **governance artefacts** (legal review, confirmation register, evaluation, deferred
selection). It does **not** deliver a real provider integration or a provider sandbox certification: those stay
**BLOCKED** until the owner selects a provider, obtains sandbox credentials and written confirmations.
