# Stage 12 — Security review

| Control | Implementation | Tested |
|---|---|---|
| Authentication | every fee route needs a session; admin routes do not exist for non-staff (404) | `TestFeeSecurity` |
| RBAC | `fee.view` (FINANCE, COMPLIANCE, BUSINESS_APPROVER), `fee.config.request` (FINANCE), `fee.config.approve` (BUSINESS_APPROVER), `fee.adjust.request` (FINANCE), `fee.adjust.approve` (BUSINESS_APPROVER), `fee.fixtures` (ADMIN); SUPPORT and others get 403 | `TestFeeSecurity`, `TestRoutePolicies` |
| Step-up | every staff mutation permission requires step-up (middleware); the step-up time is recorded on approvals and activations | `TestFeePolicyMakerChecker…` |
| Maker-checker | policy versions, provider schedules, manual adjustments, reactivation after suspension: the checker is never the maker or anyone linked (`app.actor_identities`), in Go and in triggers/CHECKs | `TestFeePolicyMakerChecker…`, `TestFeePolicyLinkedAccountCannotApprove`, `TestFeeAdjustments…` |
| Immutability | published terms (trigger), calculations, components, assessments (only `accounting_status` PENDING→POSTED), adjustments (only the decision), accounting references, provider reports, history — no UPDATE/DELETE | `TestFee*Immutability`, DB tests |
| No unrestricted overrides | no endpoint sets a fee, a rate on a quote, or a journal; clients send campaign/amount/currency/method only (unknown fields refused); manual adjustments are maker-checker and never post | `TestFeeQuotes…`, `TestRoutePolicies` |
| Tenant isolation | quotes are owner-only (others 404); organiser fee views need campaign ownership or organisation membership (others 404) | `TestFeeQuotes…`, `TestFeeSecurity` |
| Idempotency | quote, policy, version, schedule and adjustment creation need `Idempotency-Key` (middleware; reuse with another body refused); business uniqueness on assessments, adjustments, journals | `TestFeeQuotes…`, `TestFeeConcurrency` |
| Replay | intent binding: a quote binds once (`FEE_QUOTE_ALREADY_USED`); expired/invalidated quotes refused; fixture reversals replay by id | `TestFeeQuotes…`, `TestFeeAccounting…` |
| Rate limiting | `fees.quote.user` 60/hour per user (Valkey; in-memory fallback) | `TestFeeSecurity`, `TestThrottleUsesTheFeeQuotePolicyPerUser` |
| CSRF | the session-bound CSRF token (`X-CSRF-Token`, HMAC of the session; Stage 4 auth middleware) applies to every cookie-authenticated mutation | Stage 4 tests |
| Environment isolation | `fee_environment = 'TEST'` CHECKs; `ProductionFeesEnabled = false`; fixtures need a TEST ledger and `LEDGER_SYNTHETIC_FIXTURES_ENABLED`; VERIFIED provider fees refused; SYNTHETIC only for the sandbox | `TestFeeSecurity` |
| Ledger privilege | fees never writes ledger tables: journals go through `ledger.Service` on the `fundzim_ledger` pool; the app role has no ledger privilege | `TestFeeSecurity` |
| Data minimisation | quotes expose no user id; fee records hold no personal data beyond staff/user ids; audit metadata holds categories and ids only | `TestFeeSecurity` |
| Concurrency | per-currency advisory lock (quotes vs policy changes); unique constraints; row locks | `TestFeeConcurrency` |
| Fee manipulation | rates only from approved, active versions; terms hashed at submission and optionally bound at approval; selection refuses ambiguity; recomputation in reconciliation | unit + IT |

Residual risks (see [known-issues.md](known-issues.md)): no automated fraud detection for fee manipulation (Stage 13);
granting the fee roles is itself maker-checker (Stage 4), but one person holding both FINANCE and BUSINESS_APPROVER is
still stopped only by the per-version maker ≠ checker rule; a fee change has one checker (no dual approval for high-impact
changes yet).
