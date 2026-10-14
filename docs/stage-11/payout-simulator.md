# Stage 11 — Payout simulator

`internal/psp/sandbox/payouts.go`. Deterministic by the amount's last two minor-unit digits (or an explicit
`sandbox_scenario` outside production). Its provider-side state is `app.sandbox_provider_payouts` (masked synthetic
destination only); its webhooks are signed with the sandbox test secrets and POSTed through FundZim's own verification
path. It never contacts real payout infrastructure and exists only when `PAYMENTS_SANDBOX_ENABLED` (refused in
production). Limits: USD 1.00 – 10 000.00 to ECOCASH, ONEMONEY, BANK_TRANSFER (simulator facts).

| Suffix | Scenario | Simulated provider behaviour |
|---|---|---|
| 00 | SUCCESS | accepted (processing); `completed` webhook after 1 s |
| 01 | PENDING | accepted; stays processing (no webhook) |
| 02 | FAILURE | synchronous definite rejection (`DESTINATION_REJECTED`) |
| 03 | TIMEOUT | created, but our call times out (⇒ UNKNOWN); `completed` webhook after 3 s |
| 04 | UNKNOWN | call times out and nothing was created; status query NOT_FOUND, final after 60 s |
| 05 | DUPLICATE_CALLBACK | `completed` webhook delivered 3 times (same event id) |
| 06 | OUT_OF_ORDER_CALLBACK | `completed` at 1 s, then a late `processing` at 3 s |
| 07 | INVALID_SIGNATURE | `completed` signed with a wrong key (rejected), provider state changes silently |
| 08 | DELAYED_SUCCESS | `completed` after 12 s |
| 09 | PROVIDER_UNAVAILABLE | connection refused before send (RETRYABLE_BEFORE_SEND) |
| 10 | PARTIAL_PROVIDER_RESPONSE | created; malformed answer (⇒ UNKNOWN); `completed` after 2 s |
| 11 | FAILURE_AFTER_PROCESSING | accepted; `failed` webhook after 1 s |
| 12 | RETURNED_AFTER_COMPLETION | `completed` at 1 s, `returned` at 4 s |
| — | WORKER_RESTART | not a provider behaviour: a CREATE_PAYOUT left IN_FLIGHT by a dead worker |

## Results (TestPayoutSimulatorScenarios, 2026-10-14, local stack)

```
SUCCESS                                       COMPLETED  applied transitions 6, not applied observations 0
PENDING (then a scheduled completion)         COMPLETED  applied transitions 6, not applied observations 0
FAILURE                                       FAILED     applied transitions 5, not applied observations 0
TIMEOUT (then success)                        COMPLETED  applied transitions 6, not applied observations 0
DUPLICATE_CALLBACK                            COMPLETED  applied transitions 6, not applied observations 0 (deliveries 3, stored once)
OUT_OF_ORDER_CALLBACK                         COMPLETED  applied transitions 6, not applied observations 1
INVALID_SIGNATURE (resolved by polling)       COMPLETED  applied transitions 6, not applied observations 0
DELAYED_SUCCESS                               COMPLETED  applied transitions 6, not applied observations 0
PROVIDER_UNAVAILABLE (same-reference retry)   SUBMITTED
PARTIAL_PROVIDER_RESPONSE                     COMPLETED  applied transitions 6, not applied observations 0
FAILURE_AFTER_PROCESSING                      FAILED     applied transitions 6, not applied observations 0
RETURNED_AFTER_COMPLETION                     REVERSED   applied transitions 7, not applied observations 0
WORKER_RESTART                                UNKNOWN (CRASH_DURING_CREATE; reservation BLOCKED)
```

Notes: duplicate deliveries are deduplicated by the inbox (UNIQUE provider + event id) before the payout sees them; the
late lower-rank webhook is recorded as not applied (LOWER_RANK); PROVIDER_UNAVAILABLE keeps retrying with the **same**
reference and backoff (all attempts DEFINITELY_NOT_SENT) and stays SUBMITTED with the money in transit; a crashed
in-flight call becomes UNKNOWN (CRASH_DURING_CREATE) and is never resent (exactly one CREATE_PAYOUT attempt).
Staff controls (development only, `payout.sandbox.control`): `GET /admin/payouts/sandbox/{id}`,
`POST /admin/payouts/sandbox/{id}/webhooks`.
