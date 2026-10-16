# Stage 13 — Sanctions/PEP screening foundation

**No screening provider is selected. Production screening is BLOCKED** (`risk.ScreeningProductionEnabled = false`; CHECKs
`ck_risk_screening_requests_provider` = `sandbox` and `ck_risk_screening_requests_test_only_stage13`). Which lists, who is
screened and when are LR-009 / LR-053 / LR-065 (open); PEP handling LR-064; freezing obligations LR-066. Nothing in this
stage screens a real person, and no screening result is fabricated: every result is a synthetic TEST_ONLY output of the
simulator for a synthetic reference.

## 1. Provider interface (`ScreeningProvider`)

| Operation | Purpose |
|---|---|
| `GetProviderCapabilities()` | code, environment, webhooks, polling, synthetic-only |
| `SubmitScreening(ctx, {RequestID, SyntheticReference})` | returns the provider reference; called **outside** any database transaction |
| `GetScreeningStatus(ctx, ref)` | polling: PENDING / POTENTIAL_MATCH / NO_MATCH_REPORTED / ERROR with a sequence number |
| `RetrieveMatches(ctx, ref)` | the (synthetic) potential matches |
| `VerifyScreeningWebhook(header, body, receivedAt)` | signature, replay window, strict body |

A real adapter needs a provider selection ADR, a lawful basis and data-protection review, and identity data from `kyc`
(the move of screening under `compliance` is KI-S13-05).

## 2. Result model

| Concept | Where | Who |
|---|---|---|
| Provider result (as received) | `risk_screening_results` (immutable, one per provider event, `label = TEST_ONLY`, `applied` flag) | provider |
| Request status | `risk_screening_requests.status`: PENDING, POTENTIAL_MATCH, NO_MATCH_REPORTED, REVIEW_REQUIRED, ERROR | system |
| Analyst decision | CONFIRMED_MATCH or CLEARED (`reviewed_by` ≠ requester, not linked, step-up, reason; CHECK) | `risk.screening.review` |
| Compliance decision | a compliance case reached by escalation | compliance |

**NO_MATCH_REPORTED is what a provider said — it is never CLEARED.** A potential match opens a SCREENING_REVIEW case and
records a SCREENING_POTENTIAL_MATCH signal (confidence 50); **no one is labelled sanctioned from a potential match**. An
analyst's CONFIRMED_MATCH records a signal and escalates the case to compliance (`risk.case_escalated`); it applies no
restriction itself. CLEARED closes the potential match.

## 3. Failure policy

| Situation | Status | Effect |
|---|---|---|
| submission error | ERROR (`SUBMIT_FAILED`) | never cleared; a person may retry (ERROR → PENDING) |
| no answer by `timeout_at` (engineering default 10 min) or 20 polls | ERROR (`TIMEOUT`) + monitoring `SCREENING_TIMEOUT` | never cleared |
| provider error result | ERROR (`PROVIDER_ERROR`) | never cleared |
| invalid/forged/replayed callback | rejected (401), monitoring `SCREENING_CALLBACK_REJECTED` (body hash only) | never applied; polling continues |
| duplicate callback | stored once (unique provider event) | applied once |
| out-of-order callback | stored with `applied = false` | an older result never overwrites a newer one |
| payout with a requested screening still PENDING or ERROR | EC-15 REVIEW | never treated as clear |

## 4. Simulator (`SandboxScreening`, TEST only)

Enabled only with `RISK_SCREENING_SANDBOX_ENABLED=true` (refused in production). The scenario is part of the synthetic
reference `TEST-<SCENARIO>-<suffix>`; the simulator stores its state in `risk.sandbox_screenings`. Scenarios: NO_MATCH,
POTENTIAL_MATCH (1 synthetic match), MULTIPLE_MATCHES (3), PROVIDER_TIMEOUT (never answers), PROVIDER_ERROR, DUPLICATE_CALLBACK
(final callback twice), OUT_OF_ORDER_CALLBACK (final, then an older PENDING), INVALID_SIGNATURE (badly signed callback; the
poll then finds the result), DELAYED_RESULT (ready after 3 minutes). Matches are `TEST-MATCH-n` on `TEST_SYNTHETIC_LIST`,
labelled TEST_ONLY — no names, no real list entries. Callbacks are signed `FundZim-Screening-Signature: t=<unix>,v1=<hex
HMAC-SHA256(key, t + "." + body)>` with a key derived from the platform key (`risk.screening.sandbox.callback.v1`), a 5-minute
replay window and constant-time comparison; this scheme is the simulator's own and is never assumed for a real provider.
Delivery runs in the worker (`risk.maintenance`) or on demand (`POST /admin/risk/sandbox/screening/deliver`,
`risk.sandbox.control`, development only).
