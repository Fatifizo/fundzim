# Stage 9 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S9-01 | Formal acceptance of Stages 1–8 (and 9) is not recorded ([stage-acceptance-register.md](../governance/stage-acceptance-register.md)) | Process gap | OPEN — owner |
| KI-S9-02 | **No payment provider selected** ([ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md) DECISION_DEFERRED); no merchant agreement, no sandbox credentials, no written PCR answers | No real provider integration; provider sandbox certification BLOCKED | BLOCKED — owner |
| KI-S9-03 | **Production payment execution BLOCKED**: live execution disabled in code; DB refuses PRODUCTION intents/payments; legal items open ([legal-review.md](legal-review.md)) | No real donations | BLOCKED (by design) |
| KI-S9-04 | Secret manager **integration** done (OpenBao/Vault KV v2, AppRole, rotation audit); **operating** a production OpenBao/Vault (HA, auto-unseal/KMS, backups, audit device, per-workload AppRoles, secret-id delivery) not done | Production blocker (Stage 18/20) | OPEN |
| KI-S9-05 | OpenBao in compose/CI is **dev mode** (in-memory, HTTP, root token); a restart of `fundzim-secrets` empties it until `fundzim-secrets-init` re-runs (`docker compose up` does it). Payments then fail closed (CREDENTIALS_UNAVAILABLE) | Local only | ACCEPTED |
| KI-S9-06 | `WEBHOOK_UNSIGNED_VERIFY_BY_API` (needed for Pesepay-style unsigned callbacks) and Paynow's form-encoded hash verification are designed but not implemented | Needed only if such a provider is selected | OPEN |
| KI-S9-07 | No refund sandbox: refund behaviour unconfirmed (PCR-006) and legal items open; refund requests still stop at APPROVED | — | DEFERRED |
| KI-S9-08 | ZIPIT has no method code; ZimSwitch and ZIPIT are PROVIDER_CONFIRMATION_REQUIRED and not operational | — | OPEN — provider confirmation |
| KI-S9-09 | International cards are not distinguished from local cards (no capability, LR-005/LR-044, PCR-005) | Cannot offer diaspora card donations | BLOCKED |
| KI-S9-10 | Health thresholds are simple (15 min window, ≥ 20 % errors ⇒ DEGRADED); the simulator's deliberate timeout scenarios make it DEGRADED in busy test runs | Tuning with real traffic | OPEN |
| KI-S9-11 | `psp.provider.configured` and `psp.execution_gate.evaluated` are written at every API/worker start (the latter always; the former on change); a database unreachable at start skips them (logged), the refusal itself never depends on the database | Audit completeness at cold start | ACCEPTED |
| KI-S9-12 | Provider health and availability are computed per request (no cache); `/admin/payments/providers` derives health for every provider | Fine for a handful of providers ([testing.md](testing.md) §4) | OPEN |
| KI-S9-13 | The provider evidence log has no retention policy (LR-012) | Data kept indefinitely | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S9-14 | `PROVIDER_SANDBOX` is exercised only with an in-test fake provider row (`itfake_*`, left RETIRED in development databases after the test) | Isolation proven; no provider proven | ACCEPTED |
| KI-S9-15 | Webhook rate limits per provider IP range unknown (KI-S8-22 unchanged; PCR-017) | Burst deliveries throttled | OPEN |
| KI-S9-16 | Public checkout remains disabled; there is no dev-only "SANDBOX TEST PAYMENT" UI (optional in the brief); simulated payments are created through the API and the staff sandbox panel | — | ACCEPTED |

Carried from Stage 8 unchanged: KI-S8-05, 06, 07, 08, 09, 10, 11, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22. Closed by
Stage 9: **KI-S8-04** (secret manager integration; operation remains KI-S9-04), **KI-S8-12** (staff outage
declaration now exists). Updated: KI-S8-03 → KI-S9-02.
