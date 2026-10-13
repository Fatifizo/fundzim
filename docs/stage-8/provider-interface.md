# Stage 8 — Provider interface and sandbox contract

The adapter interfaces as implemented in `internal/psp/psp.go`, how calls are classified, how routing chooses a
provider, how secrets are referenced, and (§6) the **sandbox contract** — the only provider facts the registry holds.
Stage 2 design: [payment-provider-interfaces.md](../architecture/payment-provider-interfaces.md). Decisions:
[ADR-039](../adr/ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md).

> **No real provider is integrated.** The interfaces are provider-neutral; the only implementation is the FundZim
> sandbox simulator. Nothing in this document describes the behaviour, API, credentials or capabilities of any real
> payment provider or rail ([provider-capability-register.md](provider-capability-register.md)).

## 1. Interfaces

```go
type Adapter interface {
    Code() string
    PaymentProvider          // CreatePayment, GetPayment, CancelPayment → Observation
    WebhookVerifier          // Verify(acct, Inbound) (VerifiedBy, error); Parse(Inbound) ([]WebhookEvent, error)
    MapPaymentStatus(raw string) (canonical string, mapped bool, version string)
    Limits(method, currency string) (Limits, bool) // ok=false: no known limit ⇒ provider excluded
    RedirectHosts() []string                        // hosts a donor may be sent to
}
```

| Element | Rule |
|---|---|
| `CreatePaymentRequest.PaymentID` | Our reference **and** the provider idempotency key; never changes for a payment, reused on every re-create |
| `CallMeta` | Correlation id, the `payment_attempts` row id committed `IN_FLIGHT` before the call, the deadline (create 20 s, status 10 s), the resolved account, and when our reference was first sent (finality windows) |
| `CreatePaymentRequest.Metadata` | Allow-listed keys only (e.g. `sandbox_scenario`); the payments module passes none in Stage 8 |
| Translation only | Adapters classify outcomes and map statuses. They make no state, fee, eligibility or ledger decision |
| Unsupported operation | Returns class `NOT_SUPPORTED` |
| `MapPaymentStatus` | Versioned table; an unmapped raw status returns `("UNKNOWN", false)` — never a guess. Mapping happens when an event is **applied**, so an `UNRECOGNISED` inbox event can be re-dispatched after the table is extended |
| `CancelPayment` | Implemented by the sandbox; not called by `payments` in Stage 8 (a donor can cancel only an intent with no live payment) |

## 2. Classification

`psp.Class` (payment-provider-interfaces §5), the call record it produces (`payment_attempts.classification`), and
its effect.

| Class | Call classification | On `CreatePayment` (`applyCreate`) | On `GetPayment` (`PollPayment`) | Health |
|---|---|---|---|---|
| `ACCEPTED` | `ACCEPTED` | `apply` canonical status, source `SYNC` (via `CREATED → PENDING` first when needed); session if the donor must act | `apply`, source `POLL` | OK |
| `DEFINITE_FAILURE` | `REJECTED` | `FAILED` (`SYNC`), failure code from the provider (default `DECLINED`) | — | OK (provider answered) |
| `RETRYABLE_BEFORE_SEND` | `DEFINITELY_NOT_SENT` | `FAILED` (`INTERNAL` from `CREATED`, P4), code `PROVIDER_UNAVAILABLE_NOT_SENT` | No change | Error |
| `RATE_LIMITED` | `RATE_LIMITED` | `FAILED` (`INTERNAL`, P4), `PROVIDER_RATE_LIMITED_NOT_SENT` | No change | Error |
| `NOT_SUPPORTED` | `NOT_SUPPORTED` | `FAILED` (`INTERNAL`, P4), `METHOD_NOT_SUPPORTED` | No change | Error |
| `INVALID_REQUEST` | `REJECTED` | `FAILED` (`INTERNAL`, P4), `INVALID_REQUEST` | No change | Error |
| `INDETERMINATE` | `OUTCOME_UNKNOWN` + reason | **`UNKNOWN`** (`INTERNAL`, P3), reason `TIMEOUT`, `CONN_RESET_AFTER_SEND`, `HTTP_5XX`, `MALFORMED_RESPONSE`, `SIGNATURE_INVALID_RESPONSE` | No change | Error; `TIMEOUT` recorded as such |
| `AUTH_CONFIG_ERROR` | `AUTH_CONFIG_ERROR` | **`UNKNOWN`** (no documented no-processing guarantee) | No change | Error |
| `NOT_FOUND` | `NOT_FOUND` | **`UNKNOWN`** | `FAILED` (`POLL`, `NOT_CREATED_AT_PROVIDER`) **only** if the adapter sets `NotFoundFinal` and the payment is `UNKNOWN` or `CREATED`; otherwise no change, and after ≥ 2 polls while `UNKNOWN` a re-create with the **same** reference **only** if the capability has `idempotent_create` (PCR-009) | OK |

A timeout never produces `FAILED`, in Go (`applyCreate`, `sourceAllowed`) and in the database
(`ck_payment_events_unknown_internal`, `ck_payment_events_failed_source`).

## 3. Observation

`psp.Observation` keeps the canonical **and** the raw answer: `Class`, `UnknownReason`, `HTTPStatus`, `FailureCode`,
`ProviderRef`, `RawStatus` (verbatim; stored truncated to 200 characters), `Canonical`, `Mapped`, `MappingVersion`,
`Next` (`NONE`, `REDIRECT` + URL, `AWAIT_HANDSET_APPROVAL`, `SHOW_INSTRUCTIONS` + code, expiry), `ReportedAmount` +
`ReportedCcy`, `PayerToken` (HMAC'd by payments with `crypto.Keyed`, purpose `payments.payer_instrument`; the raw
token is never stored), `PayerMasked`, `OccurredAt`, `NotFoundFinal`, `Latency`. Every observation is stored:
`payment_transactions` (latest raw + mapped), `payment_events` (applied or not, with the raw status),
`payment_attempts` (per call).

## 4. Routing (`psp.Service.Route`)

Input: method, currency, amount, and whether the donation intent is simulated. For each **current** capability row
of that method and currency:

| Check | Refusal |
|---|---|
| `routable` and `custody_model = 'PSP_POOL'` (Model A; `MERCHANT_SETTLEMENT`, `UNVERIFIED`, `REQUIRES_PROVIDER_CONFIRMATION`, `NOT_SUPPORTED` rows are never candidates; `SPLIT_DIRECT` is not routed in Stage 8) | skipped |
| Provider `is_sandbox` equals the intent's simulation flag | skipped |
| `Usable`: provider `ENABLED`; sandbox ⇒ sandbox enabled; real ⇒ `LiveExecutionEnabled` (constant `false`) **and** kill switch | `PAYMENT_EXECUTION_DISABLED` (503) if an enabled provider was refused by a gate |
| An adapter is registered for the code | skipped |
| An `ENABLED` account exists for the environment (sandbox: `SANDBOX`; real: `PAYMENTS_PROVIDER_ENVIRONMENT`) | skipped |
| `Adapter.Limits(method, currency)` known | unknown limit ⇒ skipped (fail closed) |
| Amount inside the adapter's limits | `AMOUNT_OUT_OF_RANGE` (422) |
| Circuit for (provider, `CREATE_PAYMENT`, currency) not `OPEN` — applied to real providers always, to the sandbox **only** when `psp.Config.SandboxCircuitBreaker` is set | `PAYMENT_PROVIDER_UNAVAILABLE` (503) |

No candidate and no specific reason ⇒ `PAYMENT_METHOD_UNAVAILABLE` (422). Several candidates ⇒ the first by provider
code (deterministic). The routing reason (`routable PSP_POOL capability vN (VERIFIED)`) is stored on the payment.
`AvailableMethods` (public `GET /payment-methods`) lists a method only if the router would accept it.

**Circuit breaker** (`internal/psp/health.go`, technical parameters, not regulatory): every provider call writes
`CALL_OK`, `CALL_ERROR` or `TIMEOUT` to `provider_health_events` (best effort; never changes a payment outcome).
`ACCEPTED`, `NOT_FOUND` and `DEFINITE_FAILURE` count as healthy. When at least 5 of the last 5 calls within 60 s for
(provider, operation, currency) failed, `CIRCUIT_OPENED` is written; after 30 s the circuit reads `HALF_OPEN` and the
next call is the probe (success ⇒ `CIRCUIT_CLOSED`, failure ⇒ reopened). The sandbox is exempt by default because its
timeout scenarios are deliberate; `TestPaymentsProviderOutageOpensCircuit` switches it on for one server.

**Limits** (ADR-039 §8): routing applies **provider** limits from the adapter. Technical, regulatory and internal-risk
limits are applied by `payments` before routing ([donation-intents.md](donation-intents.md) §5). ADR-039 §8 also says
a real provider's limits must come from approved `PROVIDER` rows in `risk.limits`; the router does not read those
rows yet (KI-S8-26).

## 5. Secret references

| Element | Rule |
|---|---|
| Storage | `provider_accounts` holds references only: `api_credentials_secret_ref`, `webhook_secret_ref`, `webhook_secret_ref_previous` — each must match `^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$` (DB CHECK and `psp.ValidSecretRef`) |
| Resolution | `psp.SecretResolver.Resolve(ctx, ref)` at call time; values never reach the database, logs, errors or API responses |
| Stage 8 resolver | `psp.EnvResolver` — reads `SECRETREF_` + the path upper-cased with every non-alphanumeric character replaced by `_` (`secretref://fundzim/sandbox/webhook-current` → `SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT`); values shorter than 32 bytes are refused |
| Staff view | `GET /admin/payments/providers` shows reference **names** and `secrets_resolvable` (true/false), never values |
| Local values | `.env.example` placeholders `<generate:hex32>`, filled by `scripts/dev-env-init.sh`; compose passes them to the API and worker |
| Before a real provider | A KMS / secret-manager resolver replaces `EnvResolver` (KI-S8-04). Real provider secrets never go in `.env` files |

## 6. Sandbox contract

> The sandbox is a **FundZim-built simulator**. Its "provider side" is two psp-owned tables
> (`app.sandbox_provider_payments`, `app.sandbox_scheduled_webhooks`); its webhooks are signed with test secrets and
> POSTed to FundZim's own ingestion endpoint through the same verification path a real provider would use. It never
> contacts real payment infrastructure. Its method codes reuse rail codes only so routing and the staff UI can be
> exercised: a sandbox `ECOCASH` row says **nothing** about EcoCash or any other real rail, and none of the numbers
> below (limits, delays, windows) is a statement about a real provider. This section is the evidence source cited by
> the sandbox capability rows (`evidence_source = 'sandbox contract: docs/stage-8/provider-interface.md §6'`).

### 6.1 Registration

| Item | Value |
|---|---|
| Provider | `code = 'sandbox'`, display name "FundZim sandbox simulator (not a real provider)", `is_sandbox = true`, `selection_status = 'PENDING'`, `status = 'ENABLED'`, `confirmation_mode = 'WEBHOOK_SIGNED'` |
| Account | Environment `SANDBOX`, label `default`, merchant reference `fz-sandbox-merchant`, `secretref://fundzim/sandbox/api`, `secretref://fundzim/sandbox/webhook-current`, `secretref://fundzim/sandbox/webhook-previous`, replay window 300 s |
| Capabilities (v1) | `ECOCASH`/USD, `ONEMONEY`/USD, `CARD`/USD — `PSP_POOL`, `VERIFIED`, routable; `BANK_TRANSFER`/USD — `MERCHANT_SETTLEMENT` (never routable); `INNBUCKS`/USD — `UNVERIFIED` (never routable). Flags: [provider-capability-register.md](provider-capability-register.md) §2 (`idempotent_create` on `ECOCASH` only; `documented_expiry` 900 s on `CARD` only) |
| Runs when | `PAYMENTS_SANDBOX_ENABLED=true` and `APP_ENV ≠ production` (otherwise the adapter is not registered, its webhooks are rejected `PROVIDER_DISABLED`, and the control API answers 404) |

### 6.2 Limits and currencies

USD only: **100 – 1 000 000 minor units (USD 1.00 – 10 000.00)** for every method. Any other currency has no known
limit, so the router excludes the sandbox for it. ZWG is refused earlier for every donation (LR-043).

### 6.3 Scenario selection

1. `metadata["sandbox_scenario"]` naming a scenario (`^[a-z_]{3,40}$`) — not used in Stage 8: `payments` passes no
   metadata.
2. The amount's **last two minor-unit digits** (`amount_minor % 100`).
3. Otherwise `success`.

A create with a reference the simulator already knows returns the existing simulated payment (idempotent create; no
second payment).

### 6.4 Scenarios (`internal/psp/sandbox/scenarios.go`)

"Create answer" is what `CreatePayment` returns; "Initial" is the simulated provider status recorded at create;
"Hooks" are the scheduled events (delay after create; signed with the current key unless stated). A hook marked
**no webhook** changes the simulated provider status at that time without sending anything (only a status query
sees it).

| Suffix | Scenario | Create answer | Initial | Hooks |
|---|---|---|---|---|
| `00` | `success` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 1 s |
| `01` | `success_handset` | `ACCEPTED`, `REQUIRES_ACTION`, next action `AWAIT_HANDSET_APPROVAL` (expires in 10 min) | `action_required` | `paid` at 5 s |
| `02` | `decline` | `DEFINITE_FAILURE`, HTTP 402, failure code `DECLINED`, provider ref returned | `declined` | none |
| `03` | `timeout_then_success` | `INDETERMINATE` / `TIMEOUT` (the payment **is** created) | `pending` | `paid` at 3 s |
| `04` | `timeout_then_failure` | `INDETERMINATE` / `TIMEOUT` (created) | `pending` | `failed` at 3 s |
| `05` | `timeout_not_created` | `INDETERMINATE` / `TIMEOUT`; nothing created | — | none; status queries return `NOT_FOUND` |
| `06` | `duplicate_webhook` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 1 s, 3 copies with the **same** event id |
| `07` | `out_of_order_webhook` | `ACCEPTED`, `PENDING` | `pending` | `refunded` at 1 s, `paid` at 3 s, `pending` at 5 s |
| `08` | `late_success_after_expiry` | `ACCEPTED`, `PENDING` | `pending` | `expired` at 1 s, `paid` at 4 s |
| `09`–`13` | (Stage 2 refund/dispute scenarios) | not simulated in Stage 8 — behave as `success` | | |
| `14` | `bad_signature` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 1 s signed with a **wrong** key; status `paid` at 1 s, no webhook |
| `15` | `replayed_old` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 1 s with a timestamp **3600 s in the past**; status `paid` at 1 s, no webhook |
| `16` | `webhook_never` | `ACCEPTED`, `PENDING` | `pending` | status `paid` at 2 s, no webhook |
| `17` | `unmapped_status` | `ACCEPTED`, `PENDING` | `pending` | `limbo` (unmapped) at 1 s |
| `18` | `amount_mismatch` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 1 s; the simulated provider records and reports **amount + 1 minor unit** (webhook and status query) |
| `19` | `provider_unavailable` | `RETRYABLE_BEFORE_SEND` (connection refused before the request was written); nothing created | — | none |
| `20` | `provider_5xx_then_success` | `INDETERMINATE` / `HTTP_5XX`, HTTP 502 (created) | `pending` | `paid` at 3 s |
| `21` | `expired` | `ACCEPTED`, `PENDING` | `pending` | `expired` at 1 s |
| `22` | `failure_after_pending` | `ACCEPTED`, `PENDING` | `pending` | `failed` at 1 s |
| `23` | `delayed_confirmation` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 12 s |
| `24` | `pending_forever` | `ACCEPTED`, `PENDING` | `pending` | none (tests drive it with the control API) |
| `25` | `previous_key` | `ACCEPTED`, `PENDING` | `pending` | `paid` at 1 s signed with the **previous** key (rotation overlap) |
| other | `success` | as `00` | | |

Method overrides: for `CARD` the initial status is always `action_required` and the next action is `REDIRECT` to
`https://sandbox.fundzim.invalid/checkout/{provider_ref}` (expires in 15 min). Provider references are
`sbx_pay_` + 20 hex characters; event ids `sbx_evt_` + 20 hex characters.

Differences from the Stage 2 table (payment-provider-interfaces §11.1): suffixes `09`–`13` are not simulated (no
refund execution, disputes deferred); `07` sends `refunded`, `paid`, `pending` in that order; `08`'s late success comes
after 4 s instead of 2 min; `19`–`25` are new; there is no `SPLIT_DIRECT` row, no unsigned-webhook row and no second
sandbox (§11.2). Payout scenarios are out of scope (no payouts).

### 6.5 Status query and not-found finality

`GetPayment` looks up **our** reference. Found ⇒ `ACCEPTED` with the simulated raw status, provider ref, reported
amount and currency, `payer_masked = "****1234"`, and failure code `DECLINED` for `declined`/`failed`. Not found ⇒
`NOT_FOUND`, HTTP 404, and `NotFoundFinal = true` once **more than 60 s** have passed since our reference was first
sent (the sandbox's documented finality window, a PCR-010 analogue for the simulator only). An invalid reference ⇒
`INVALID_REQUEST`. A database error ⇒ `INDETERMINATE / HTTP_5XX`.

### 6.6 Webhooks: signature, payload, delivery

| Item | Contract |
|---|---|
| Header | `FundZim-Sandbox-Signature: t=<unix seconds>,kid=<current\|previous>,v1=<hex HMAC-SHA256(secret, t + "." + body)>` (format `^t=([0-9]{1,12}),kid=([a-z]{1,16}),v1=([0-9a-f]{64})$`) |
| Key slots | `kid=current` → `webhook_secret_ref`; `kid=previous` → `webhook_secret_ref_previous`; any other kid, an empty slot or an unresolvable secret ⇒ `UNKNOWN_KEY` |
| Comparison | Constant time (`hmac.Equal`) over the recomputed header |
| Replay window | `|received_at − t| ≤` the account's window (300 s; 300 s default when unset); checked after the signature |
| Body | JSON, unknown fields refused: `{id, type, created, livemode: false, simulated: true, data: {payment_reference, provider_payment_id, status, amount_minor (digit string), currency, payer_masked}}` |
| Parse rules | `id` must match `^sbx_evt_[0-9a-f]{20}$`; `type` 1–64 characters (class `PAYMENT` when it starts with `payment.`, else `OTHER`); `status` 1–40 characters; `amount_minor` `^[1-9][0-9]{0,17}$`; simulated = `simulated && !livemode`, which must equal the provider's `is_sandbox` |
| Redaction | Only the fields above are kept in `payload_redacted`; the payer is masked at the source |
| Delivery | Periodic job `sandbox.deliver_webhooks` every 1 s, batch 50, `FOR UPDATE SKIP LOCKED`, 30 s lease; the HTTP POST (`SANDBOX_WEBHOOK_URL`, 5 s client timeout, `User-Agent: fundzim-sandbox-simulator/1`) runs outside any transaction; network failures retried up to 5 times; at-least-once (the inbox dedupes) |
| Provider-side state | Moves when a hook is due, never backwards by rank (`pending` < `action_required` < `failed`/`declined`/`expired`/`cancelled` < `paid` < `partially_refunded` < `refunded`); unranked statuses (`limbo`) are taken as they come |

### 6.7 Mapping table `sandbox-map-v1`

| Raw | Canonical |
|---|---|
| `pending` | `PENDING` |
| `action_required` | `REQUIRES_ACTION` |
| `paid` | `SUCCEEDED` |
| `failed`, `declined` | `FAILED` |
| `expired` | `EXPIRED` |
| `cancelled` | `CANCELLED` |
| `partially_refunded` | `PARTIALLY_REFUNDED` |
| `refunded` | `REFUNDED` |
| anything else (e.g. `limbo`) | unmapped ⇒ `UNKNOWN`, `mapped = false`, alert |

### 6.8 Redirect host

`sandbox.fundzim.invalid` is the only host the simulator returns (`.invalid` never resolves: there is no live
checkout). `payments.safeRedirect` accepts only `https`, no user info, no explicit port, a non-empty path, ≤ 2048
characters, on a host the adapter declares; anything else is never shown to the donor and is logged as an alert.

### 6.9 Development control API

`GET /api/v1/admin/payments/sandbox/attempts/{payment_id}` (simulator-side state) and
`POST …/sandbox/attempts/{payment_id}/webhooks` (`status` ∈ `pending, action_required, paid, failed, expired,
cancelled, limbo, refunded, partially_refunded`; `delay_seconds` 0–3600; `copies` 0–10; `amount_minor`; `sign_with` ∈
`current, previous, wrong`; `timestamp_offset_seconds` ±86 400). Permission `payment.sandbox.control` (ADMIN); 404
`SANDBOX_DISABLED` unless the sandbox runs. The scheduled webhook goes through the real endpoint like any other.

## 7. What a real adapter must provide (Stage 9)

Implement `psp.Adapter` against the provider's **documented** API; classify every outcome per §2 (a timeout after the
request was written is `INDETERMINATE`, never a failure); declare limits only from approved evidence; implement the
provider's documented webhook verification (PCR-017) — never the sandbox scheme; set `NotFoundFinal` only when the
provider documents finality (PCR-010); keep a versioned mapping table; declare its redirect hosts; pass the same
integration scenarios as the sandbox. Details: [STAGE-8-TO-STAGE-9.md](../stage-handover/STAGE-8-TO-STAGE-9.md).
