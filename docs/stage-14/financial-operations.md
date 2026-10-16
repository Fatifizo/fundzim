# Stage 14 — Financial operations (payments, providers, ledger, payouts, fees)

All money in this environment is TEST money. Pages: `/admin/operations/payments` (`payment.view`),
`/admin/operations/ledger` (`ledger.view`, read-only), `/admin/operations/payouts` (`payout.view`; fee figures need
`fee.view`). No amount is totalled or combined across currencies on these pages; they show counts.

| Area | Source | Highlights |
|---|---|---|
| Payments | `payments.OpsSnapshot` | UNKNOWN payments (TEST_ONLY; never FAILED by assumption), refund requests awaiting approval (makers listed), open reconciliation references; live donations `BLOCKED`; search by payment id or donation intent id |
| Providers | `psp.OpsSnapshot` | per-provider health derived from recorded observations (no observation = UNKNOWN; the sandbox is SIMULATED); live execution gate `BLOCKED`; dead-lettered webhooks as exceptions |
| Ledger | `ledger.OpsSnapshot` (ledger pool) | adjustments awaiting a checker, open reconciliation exceptions, latest integrity result per invariant (`ledger.integrity`), production posting `BLOCKED`; the portal never writes ledger tables |
| Payouts | `payouts.OpsSnapshot` | awaiting approval, approved-not-submitted, in flight, UNKNOWN (reservation kept), open exceptions, destinations awaiting verification; real payouts `BLOCKED`; search by payout request id or campaign id |
| Fees | `fees.OpsSnapshot` | policy versions, adjustments and provider schedules awaiting a checker; open fee reconciliation items; production fees `BLOCKED` |

Exception resolution stays with the module: the exception center's triage can record an owner and notes but refuses
RESOLVED while the module still reports the exception (`OPS_EXCEPTION_DOMAIN_OPEN`). Nothing in Stage 14 releases a
reservation, resubmits a payout or settles an unknown outcome.
