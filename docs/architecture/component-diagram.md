# Component Diagrams (C4)

**Stage 2 — design only.** C4-style views of FundZim: system context, containers, and components inside the
API process and the worker process. Mermaid `flowchart` is used (rather than Mermaid's experimental C4
syntax) so the diagrams render in GitHub and in IDE previews.

Related: [system-overview.md](system-overview.md) · [go-module-design.md](go-module-design.md) ·
[background-processing.md](background-processing.md) · [api-design.md](../api/api-design.md) ·
[authentication-authorization.md](../security/authentication-authorization.md) ·
[observability.md](observability.md)

---

## 1. Level 1 — System context

```mermaid
flowchart LR
    donor(["Donor<br/>guest or user, ZW or abroad"])
    owner(["Campaign owner /<br/>organisation member"])
    staff(["Staff<br/>REVIEWER · KYC_REVIEWER · SUPPORT · COMPLIANCE ·<br/>FINANCE · ADMIN · SECURITY_ADMIN · SUPER_ADMIN"])
    fz["FundZim platform<br/>orchestrates, records, reconciles, controls<br/>(never holds donor funds — Model A)"]
    psp[["Licensed PSPs<br/>collect · hold pool · settle · disburse"]]
    kycv[["KYC vendor (optional, PD-25)"]]
    scr[["Screening vendor"]]
    msg[["SMS / email providers"]]
    crawl[["WhatsApp / social crawlers"]]
    benef(["Beneficiary / institution payee<br/>(receives payout from PSP)"])

    donor -->|"donate, view status, receipts"| fz
    owner -->|"campaigns, verification, payout requests"| fz
    staff -->|"review, approve, investigate (MFA)"| fz
    fz <-->|"instructions out; signed events in"| psp
    donor -.->|"approves on handset / hosted card page"| psp
    psp -.->|"disbursement"| benef
    fz <-->|"sessions, results, callbacks"| kycv
    fz <-->|"screening, deltas"| scr
    fz -->|"OTP, receipts, alerts"| msg
    crawl -.->|"OpenGraph fetch"| fz
```

## 2. Level 2 — Containers

```mermaid
flowchart TB
    subgraph edge["Edge (Stage 18/20 product)"]
        proxy["Reverse proxy / CDN / WAF<br/>TLS, routing, coarse rate limits,<br/>trusted X-Request-ID"]
    end
    subgraph web["apps/web — Next.js 16 (presentation only)"]
        pubweb["Public + donor + owner + org UI<br/>(SSR, PWA shell)"]
        admweb["Admin portal UI<br/>(/admin group or admin host)"]
    end
    subgraph go["Go binary (one build)"]
        api["api<br/>net/http ServeMux · /api/v1"]
        wrk["worker<br/>River workers · outbox dispatcher · schedulers"]
        ctl["fundzimctl"]
    end
    subgraph pgc["PostgreSQL"]
        appS[("app · queue · ledger · audit · risk · recon")]
        kycS[("kyc (fundzim_kyc only)")]
        cmpS[("compliance (fundzim_compliance; RLS on STR rows)")]
    end
    redis[("Redis (optional, non-authoritative)")]
    pubB[("public-media")]
    privB[("private-kyc: kyc/ · compliance/")]
    kms[["KMS / secret manager"]]
    av[["ClamAV (internal)"]]
    ext[["PSPs · KYC vendor · screening · SMS · email"]]
    otel[["OTel collector"]]

    proxy --> pubweb
    proxy --> admweb
    proxy -->|"/api/v1/*"| api
    pubweb -->|"server-side /api/v1"| api
    admweb -->|"server-side /api/v1/admin"| api
    api -->|"app pool"| appS
    api -->|"kyc pool"| kycS
    api -->|"compliance pool"| cmpS
    wrk -->|"app pool"| appS
    wrk -->|"kyc pool"| kycS
    wrk -->|"compliance pool"| cmpS
    ctl -->|"migrator / readonly"| appS
    api --> redis
    wrk --> redis
    api --> kms
    wrk --> kms
    api -->|"presign"| privB
    wrk --> pubB
    wrk --> privB
    wrk --> av
    api --> ext
    wrk --> ext
    api --> otel
    wrk --> otel
```

| Container | Technology | Scales by | Notes |
|---|---|---|---|
| api | Go, stdlib `net/http` ServeMux (ADR-031) | Replicas behind the proxy | Stateless; health on `/healthz`, `/readyz`; metrics on an internal port |
| worker | Same binary, River (ADR-025) | Replicas; per-queue concurrency | Leader-only schedulers use River's periodic jobs with unique keys (no Redis lock) |
| fundzimctl | Same codebase | — | Runs migrations with `fundzim_migrator`; invariant checks with `fundzim_readonly` |
| PostgreSQL | One cluster, 8 schemas, 3 runtime roles + migrator + readonly (ADR-022) | Vertical, then read replicas for reporting only | Never a read replica for financial decisions |

## 3. Level 3 — Components of the `api` process

### 3.1 Overview

```mermaid
flowchart TB
    subgraph mw["Common middleware (all routes)"]
        m1["1 RequestID"] --> m2["2 Recover"] --> m3["3 AccessLog (allow-listed fields)"] --> m4["4 Trace span"] --> m5["5 SecurityHeaders"] --> m6["6 BodyLimit"] --> m7["7 Deadline"]
    end

    m7 --> gpub
    m7 --> gusr
    m7 --> gadm
    m7 --> gwh
    m7 --> ghl

    subgraph gpub["Group: public (/api/v1/public/*, GET campaign pages)"]
        p1["OptionalSession"] --> p2["RateLimit (per IP)"] --> p3["Policy: PUBLIC"]
    end
    subgraph gusr["Group: user (/api/v1/*)"]
        u1["Session (user cookie only)"] --> u2["CSRF (unsafe methods)"] --> u3["RateLimit (per route, per principal)"] --> u4["StepUp (marked routes)"] --> u5["Idempotency (required on money routes)"] --> u6["Policy: USER / OWNER_OF / ORG_ROLE"]
    end
    subgraph gadm["Group: staff (/api/v1/admin/*)"]
        a1["Session (staff cookie only, STAFF, MFA)"] --> a2["CSRF"] --> a3["RateLimit"] --> a4["StepUp (approvals, reveals)"] --> a5["Justification (sensitive reads)"] --> a6["Idempotency"] --> a7["Policy: PERMISSION(...)"]
    end
    subgraph gwh["Group: inbound callbacks (/api/v1/webhooks/{provider}, vendor callbacks)"]
        w1["RawBody capture (small limit)"] --> w2["RateLimit (per provider)"] --> w3["No session · no CSRF"]
    end
    subgraph ghl["Health: /healthz /readyz"]
        h1["no auth, no DB for /healthz"]
    end

    subgraph handlers["Module HTTP handlers (internal/<m>/http)"]
        hU["users.http · auth.http · organisations.http"]
        hC["campaigns.http · beneficiaries.http · storage.http"]
        hP["payments.http · payouts.http"]
        hK["kyc.http"]
        hA["admin.http (staff handlers composed from root services)"]
        hW["psp.http (webhook) · kyc.http (vendor callback) · compliance.http (screening callback)"]
    end
    gpub --> hC
    gusr --> hU
    gusr --> hC
    gusr --> hP
    gusr --> hK
    gadm --> hA
    gwh --> hW

    subgraph services["Module services (internal/<m>/service, behind root interfaces)"]
        sDom["users · auth · organisations · campaigns · beneficiaries · storage · notifications"]
        sFin["payments · payouts · ledger · fees · reconciliation · psp"]
        sTS["risk · compliance · kyc · audit"]
    end
    handlers --> services

    subgraph stores["Repositories (internal/<m>/store, sqlc)"]
        stA["app-pool stores"]
        stK["kyc store (kyc pool)"]
        stC["compliance store (compliance pool)"]
    end
    services --> stores

    subgraph plat["platform"]
        pl["db.WithTx · outbox.Writer · jobs.Client · idempotency · money · crypto · actor · errs · clock · ids · log · httpx"]
    end
    services --> plat
    handlers --> plat
```

### 3.2 Middleware chain in detail

Order matters; it follows ARCHITECTURE §5 with the Stage 2 additions (route groups, step-up, justification).

| # | Middleware | Package | Groups | Behaviour | Failure response |
|---|---|---|---|---|---|
| 1 | RequestID | `platform/httpx` | all | Accept `X-Request-ID` only from trusted proxies and only if it is a valid UUID/ULID; else generate UUIDv7; echo in response and in `meta.request_id` | — |
| 2 | Recover | `platform/httpx` | all | Recover panics, log with stack (redacted), count | 500 `INTERNAL_ERROR` |
| 3 | AccessLog | `platform/log` | all | Method, route pattern (not raw path with IDs), status, latency, principal kind, request ID; never bodies, query strings with tokens, cookies | — |
| 4 | Trace | observability | all | OTel span per request; route pattern as span name | — |
| 5 | SecurityHeaders | `platform/httpx` | all | HSTS, `nosniff`, `Referrer-Policy`, `Cache-Control: no-store` on non-public responses | — |
| 6 | BodyLimit | `platform/httpx` | all | Per group: default 64 KiB JSON; webhooks per provider config (e.g. 256 KiB); uploads never go through the API body (presigned) | 413 `PAYLOAD_TOO_LARGE` |
| 7 | Deadline | `platform/httpx` | all | Context deadline per route class (default 10 s; webhooks 5 s) | 503 `SERVICE_UNAVAILABLE` |
| 8 | Session | `auth` (implements `platform/httpx.Authenticator`) | user, staff, public(optional) | Opaque session lookup (hashed token), idle/absolute expiry, rotation; places `actor.Principal` (user id, account kind, roles → permission set, org memberships digest, `mfa_at`, `step_up_at`, session id) on the context | 401 `UNAUTHENTICATED` |
| 9 | CSRF | `auth` | user, staff | Unsafe methods: `Origin`/`Sec-Fetch-Site` check + synchroniser token | 403 `CSRF_FAILED` |
| 10 | RateLimit | `platform/httpx` (Redis-backed, in-process fallback) | all except health | Keys per route class: IP, principal, phone (OTP), provider (webhooks). Sensitive routes fail closed or fall back to a conservative in-process limit if Redis is down | 429 `RATE_LIMITED` + `Retry-After` |
| 11 | StepUp | `auth` | routes marked `StepUp` | `now − principal.step_up_at ≤ STEP_UP_MAX_AGE` | 401 `STEP_UP_REQUIRED` |
| 12 | Justification | `platform/httpx` | staff routes marked `Justified` | Requires `justification` (and case id where case-bound); attached to context for audit | 422 `JUSTIFICATION_REQUIRED` |
| 13 | Idempotency | `platform/idempotency` | routes marked `Idempotent` (required on money routes) | Claim `(scope, key)`; same hash → replay stored response; different hash → conflict; in-flight → conflict | 400 `IDEMPOTENCY_KEY_REQUIRED`, 409 `IDEMPOTENCY_KEY_REUSED`, 409 `IDEMPOTENCY_REQUEST_IN_PROGRESS` |
| 14 | Policy | `platform/httpx` | all | Route-declared policy: `Public`, `User`, `Permission(p)`, `StaffPermission(p)`. Coarse gate only; services re-check permission **and** object ownership | 403 `FORBIDDEN` (or 404 where existence is sensitive) |

Every route is registered with a declaration (sketch, not compiled):

```go
r.Handle("POST /api/v1/campaigns/{campaign_id}/payouts", h.RequestPayout,
    httpx.Policy(httpx.User()), httpx.StepUp(), httpx.Idempotent(httpx.Required),
    httpx.RateClass("payout.request"))
```

`TestEveryRouteHasPolicy` fails the build if a route lacks `Policy`.

### 3.3 Route groups to modules

| Path prefix (indicative; [api-design.md](../api/api-design.md) and the OpenAPI file are authoritative) | Module handler package |
|---|---|
| `/api/v1/auth/*` (OTP start/verify, sessions, MFA) | `auth/http` |
| `/api/v1/me`, `/api/v1/me/*` (profile, contacts) | `users/http` |
| `/api/v1/organisations/*` | `organisations/http` |
| `/api/v1/verification/*` (KYC/KYB sessions, document upload intents, consents) | `kyc/http` |
| `/api/v1/uploads/*` (upload sessions) | `storage/http` |
| `/api/v1/campaigns/*`, `/api/v1/public/campaigns/*` | `campaigns/http` |
| `/api/v1/campaigns/{id}/beneficiaries/*` | `campaigns/http` (checks ownership, then calls `beneficiaries`; go-module-design §5.13) |
| `/api/v1/institution-payees/*` | `beneficiaries/http` |
| `/api/v1/donations`, `/api/v1/payments/*`, `/api/v1/payment-methods` | `payments/http` |
| `/api/v1/campaigns/{id}/payouts`, `/api/v1/payouts/*`, `/api/v1/payout-destinations/*` | `payouts/http` |
| `/api/v1/webhooks/{provider}` | `psp/http` |
| `/api/v1/callbacks/kyc/{vendor}` (proposed), `/api/v1/webhooks/screening/{vendor}` (WHK-03, baseline §12 I-25) | `kyc/http`, `compliance/http` |
| `/api/v1/admin/*` | `admin/http` (calls root services of all modules) |

`admin` handlers are thin: decode, call one or more root-interface methods, map to staff DTOs. When one staff
action must change two modules atomically, the **owning** module exposes a single method that does it (for
example `campaigns.Freeze` places the hold and posts the journal); `admin` never opens a transaction.

### 3.4 Inside one module (pattern)

```mermaid
flowchart LR
    http["<m>/http<br/>decode · validate · DTO map"] --> root["<m> root package<br/>Service interface · types · errors · permissions"]
    jobs["<m>/jobs<br/>River workers · event consumers"] --> root
    svc["<m>/service<br/>implements Service<br/>authz · rules · tx orchestration"] -.implements.-> root
    svc --> store["<m>/store<br/>sqlc queries on own tables"]
    svc --> ev["<m>/events<br/>payload types · fixtures"]
    svc --> other["other modules' root interfaces<br/>(allowed imports only)"]
    svc --> platform["platform/*"]
    store --> db[("own tables")]
```

## 4. Level 3 — Components of the `worker` process

### 4.1 Overview

```mermaid
flowchart TB
    subgraph river["River client (queue schema)"]
        qFin["queue: financial<br/>(payments, payouts, ledger, reconciliation)"]
        qDef["queue: default"]
        qNtf["queue: notifications"]
        qBulk["queue: bulk / maintenance"]
    end
    subgraph core["Platform components"]
        disp["Outbox dispatcher<br/>outbox_events → consumer jobs"]
        sched["Periodic job scheduler<br/>(River periodic jobs, unique)"]
        dlq["Dead-letter handling<br/>(discarded jobs → alert; re-queue via admin)"]
    end
    subgraph inbound["Inbound processing"]
        pin["psp inbound dispatcher<br/>classify Kind → payments / payouts job"]
        pinp["payments inbound processor"]
        poin["payouts inbound processor"]
        kin["kyc vendor callback processor"]
        cin["screening callback processor"]
    end
    subgraph consumers["Event consumers (inbox_events dedupe)"]
        c1["users: kyc projection"]
        c2["organisations: kyb projection"]
        c3["payouts: recovery cases, re-checks, campaign cancel"]
        c4["campaigns: hold ledger move, media attach, gates"]
        c5["payments: settlement release scheduling"]
        c6["risk: signals, PROVIDER_HOLD"]
        c7["compliance: screening triggers, alert→case"]
        c8["notifications: message fan-out"]
        c9["kyc: KYB profile on organisation.created"]
    end
    subgraph workers["Module job workers"]
        w1["payments: poll status, expire, execute refund, release"]
        w2["payouts: submit, poll, approval expiry, deferred retry"]
        w3["ledger: balance recompute, invariant run, pool integrity"]
        w4["reconciliation: fetch/import, match run, suspense ageing"]
        w5["storage: scan, promote, variants, orphan cleanup"]
        w6["notifications: send, retry"]
        w7["kyc/compliance: vendor polls, rescreening, re-verification due"]
        w8["platform/auth/audit: idempotency expiry, session cleanup, hash-chain verify"]
    end
    disp --> consumers
    sched --> workers
    pin --> pinp
    pin --> poin
    river --> inbound
    river --> consumers
    river --> workers
    river --> dlq
```

### 4.2 Job kinds

[background-processing.md §6](background-processing.md) is the **authoritative** job catalogue (kinds,
queues, uniqueness, retries, timeouts, dead-letter behaviour). The table below is the per-module view used for
wiring, using the same kind names. Event-consumer kinds follow the `<module>.on_<event>` convention
(background-processing §3); kinds marked *(proposed)* are needed by this design and not yet in that catalogue.

| Kind | Owner | Trigger | Effect (transaction) |
|---|---|---|---|
| `platform.outbox_dispatch` | platform | Periodic (2 s) + producer kick | TX-26 |
| `psp.webhook_dispatch` | psp | Enqueued in TX-01 | Classify `Kind`, enqueue the registered apply job |
| `payments.webhook_apply`, `payments.refund_webhook_apply`, `payments.dispute_webhook_apply` | payments | `psp.webhook_dispatch` | TX-04 / TX-05 / TX-06 / TX-08 / TX-11 |
| `payments.status_poll`, `payments.unknown_sweep`, `payments.intent_expiry`, `payments.create_retry` | payments | Transition txs, periodic sweeper | Status query (P5) then transition; never assumes failure |
| `payments.refund_submit`, `payments.refund_status_poll` | payments | TX-10 / refund `PENDING`/`UNKNOWN` | P5 refund call; TX-11 |
| `payments.on_settlement_matched` → `payments.release_funds` *(proposed)* | payments | `settlement.matched`; release scheduled at hold-period end | TX-07 |
| `payouts.webhook_apply` | payouts | `psp.webhook_dispatch` | TX-15 / TX-16 |
| `payouts.submit`, `payouts.status_poll`, `payouts.unknown_sweep` | payouts | Approval tx, sweeper | TX-14 → call → TX-15; `UNKNOWN` never resubmitted |
| `payouts.approval_expiry` *(proposed)* | payouts | Periodic | Approvals past validity → `PENDING_REVIEW` |
| `payouts.on_risk_hold_placed`, `payouts.on_kyc_status_changed`, `payouts.on_compliance_case_blocking_changed`, `payouts.on_campaign_frozen`, `payouts.on_campaign_cancelled` *(proposed)* | payouts | Events | Re-check in-flight payouts (Y7) or cancel pre-submit (Y5) |
| `payouts.on_dispute_shortfall`, `payouts.on_refund_shortfall` *(proposed)* | payouts | Events | TX-25 (recovery case + `RECOVERY_HOLD`) |
| `ledger.invariant_verify` | ledger | Nightly + on demand | Recompute vs `ledger_balances`, SC checks; failure → `ledger.invariant_failed` / `ledger.pool_integrity_breached` |
| `reconciliation.import`, `reconciliation.match_run`, `reconciliation.discrepancy_ageing`, `reconciliation.daily_report` | reconciliation | Staff upload / schedule | Import; TX-24 per match; SC-4 escalation; reports |
| `storage.object_scan`, `storage.media_reencode`, `storage.object_promote` | storage | TX-28 | Quarantine → scan → (re-encode) → promote |
| `notifications.on_<event>` → `notifications.send` | notifications | Events | `notification_jobs` row, then channel send |
| `users.on_kyc_level_changed`, `organisations.on_kyb_level_changed` *(proposed)* | users / organisations | Events | Projection update (version-ordered) |
| `campaigns.on_risk_hold_placed` *(proposed)*, `campaigns.end_date_complete` | campaigns | Event / periodic | `hold:{id}:applied` journal for campaign-scope `COMPLIANCE_HOLD`; lifecycle completion |
| `compliance.screening_run`, `compliance.rescreen_schedule`, `compliance.on_risk_alert_raised` *(proposed)* | compliance | Events, schedule | Screening; alert → case |
| `risk.hold_expiry`, `risk.on_<event>` | risk | Periodic, events | Hold expiry via `hold_events`; signals; `PROVIDER_HOLD` on SEV1 / pool breach |
| `kyc.on_organisation_created`, `kyc.vendor_poll`, `kyc.reverification_due`, `kyc.profile_snapshot` *(proposed)* | kyc | Events, schedule | KYB profile creation; vendor results; expiry triggers; projection convergence |
| `psp.provider_health_probe` | psp | Periodic | `provider_health_events` |
| `auth.session_prune`, `platform.idempotency_expire`, `platform.retention_apply` (disabled until LR-012), `audit.chain_verify` *(proposed)* | auth / platform / audit | Periodic | Maintenance; chain break → SEV1 |

### 4.3 Composition root wiring (internal/app)

Construction follows the topological order in [dependency-matrix.md §3](dependency-matrix.md), so each service
receives only already-built dependencies:

1. `config.Load` → `log`, `clock`, `ids`, `crypto.Keyring` (KMS), `db.Open` (app, kyc, compliance pools),
   `jobs.Client` (River on the app pool), `outbox.Writer`, `idempotency.Store`.
2. Services: audit → users, storage, ledger, fees, psp (+ adapters from config), risk → organisations, notifications
   → auth, kyc (+ vendor adapter) → beneficiaries, compliance (+ screener) → campaigns → payments,
   payouts → reconciliation → admin.
3. Ports: only `httpx.Authenticator` (implemented by `auth`, needed because `platform` cannot import `auth`),
   in `internal/app/ports.go`. The former C-1/C-3/C-7 interim ports were removed when the baseline was amended.
4. HTTP: `httpx.NewRouter`; each module's `http.Register(r, svc)`; admin group; webhook group.
5. Worker: register job workers per module (`jobs.Register(...)`), the event routing table
   (`internal/app/events.go`: event name → consumer kinds), periodic schedules, inbound kind routing for `psp`.
6. Mode switch: `api` starts the HTTP server; `worker` starts River (and a health/metrics listener).
