# Stage 11 — Provider abstraction

`internal/psp/payouts.go`:

```go
type PayoutProvider interface {
    CreatePayout(ctx, CallMeta, CreatePayoutRequest) Observation   // PayoutID = our reference, every attempt
    GetPayout(ctx, CallMeta, payoutID string) Observation          // status by OUR reference
    CancelPayout(ctx, CallMeta, payoutID string) Observation       // NOT_SUPPORTED where unsupported
    MapPayoutStatus(raw string) (canonical string, mapped bool, version string)
    PayoutLimits(rail, currency string) (Limits, bool)             // unknown ⇒ REVIEW, never assumed
}
```

Webhook verification and parsing reuse `psp.WebhookVerifier` and the Stage 8 inbox (class PAYOUT is dispatched to
`payouts`). Outcomes use the Stage 8 classification: ACCEPTED, DEFINITE_FAILURE, RETRYABLE_BEFORE_SEND (nothing reached
the provider: the same reference may be retried), INDETERMINATE (⇒ UNKNOWN, never resubmitted), NOT_FOUND (final only
with the provider's documented finality), AUTH_CONFIG_ERROR, RATE_LIMITED, NOT_SUPPORTED, INVALID_REQUEST.
Canonical payout statuses: PROCESSING, COMPLETED, FAILED, RETURNED, UNKNOWN.

**Registry.** Payout capability rows are versioned `app.provider_capabilities` rows with `payout_api` and `payout_rails`.
`RoutePayout` selects a routable capability (VERIFIED evidence, authoritative status path) for the rail and currency, a
**sandbox** provider enabled for sandbox use, an enabled SANDBOX account with environment-scoped secret references and a
registered adapter implementing `PayoutProvider`. Real providers are never routed for payouts in Stage 11.
`GET /admin/payouts/providers` shows each payout capability; values no provider has confirmed (cancellation, limits,
settlement requirements, confirmation status) are `UNKNOWN`; the simulator's are labelled as simulator contract.

**Not implemented (no provider):** a real adapter, account-number decryption for a real disbursement (the simulator receives
only the masked destination), provider-documented idempotent payout creation, payout cancellation, payout statements.
