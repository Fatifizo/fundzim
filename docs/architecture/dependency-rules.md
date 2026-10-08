# Module Dependency Rules

**Stage 2 — design only.** These rules turn [design-baseline.md §3 and §5](../stage-2/design-baseline.md) and
[ADR-021](../adr/ADR-021-module-boundaries-and-ownership.md) into checkable statements. The N×N view is
[dependency-matrix.md](dependency-matrix.md); package layout is [go-module-design.md](go-module-design.md);
concept ownership is [domain-boundaries.md](domain-boundaries.md). Background job mechanics (outbox dispatcher,
retries, dead letters) are specified in [background-processing.md](background-processing.md).

Nothing here is compiled or tested yet: Go is not installed (prerequisite-assessment §4). Every CI check below
is a **Stage 3 deliverable**.

---

## 1. Vocabulary

| Term | Meaning |
|---|---|
| Module | One of the 19 directories `internal/<module>` listed in baseline §2. |
| Root package | `internal/<module>` itself: the public service interface, request/response types, error codes, permission names. The **only** package another module may import. |
| Sub-package | Anything below the root (`service`, `store`, `http`, `jobs`, `events`, adapters, `<module>test`). Private to the module, plus `internal/app` for wiring. |
| Import edge | A Go `import` from a package of module A to the root package of module B. |
| Event coupling | A consumes an outbox event produced by B. Creates **no** import edge. |
| Tx-joining call | A call to another module's root-interface method that takes the caller's `db.Tx` and executes inside the caller's transaction. |

## 2. Compile-time import rules

| # | Rule | Enforced by |
|---|---|---|
| D1 | A module imports only the root packages of the modules listed for it in baseline §3. | `TestModuleImportGraph` (§5.1) |
| D2 | No package imports a sub-package of another module. `internal/app` is the only exception (wiring). `admin` is **not** an exception: it imports root packages only. | `TestNoForeignSubpackageImports` |
| D3 | `platform` imports nothing under `internal/`. Platform sub-packages may import each other only in the direction listed in [go-module-design.md §3.1](go-module-design.md). | `TestModuleImportGraph` |
| D4 | Nothing imports `internal/app`, and nothing imports `apps/api/cmd/*`. | `TestModuleImportGraph` |
| D5 | Provider SDKs and HTTP clients for PSPs are imported only by `internal/psp/adapters/*`. KYC vendor clients only by `internal/kyc/vendor/*`. Screening vendor clients only by `internal/compliance/screening/*`. SMS/email clients only by `internal/notifications/channels/*`. Object-store (S3) and scanner clients only by `internal/storage/*`. | `TestVendorImportsConfined` (allow-list of third-party import paths per directory) |
| D6 | `pgx` is imported only by `internal/platform/db` and `internal/*/store`. Services receive `db.Tx` / `db.Querier` (platform types), never raw pgx. | `TestPgxConfined` |
| D7 | `<module>test` packages (fakes, builders) are imported only from `_test.go` files. | `TestTestPackagesOnlyInTests` |
| D8 | No `float32` / `float64` in any package under `internal/` that handles money (`platform/money`, `ledger`, `fees`, `payments`, `payouts`, `reconciliation`, `psp`, `campaigns`), and no `math/big.Float` anywhere. | `go vet` analyser `nofloatmoney` (Stage 3) |
| D9 | A domain module never imports `net/http` outside its `http` sub-package (and `psp/adapters`, `kyc/vendor`, `compliance/screening`, `notifications/channels`, which make outbound calls). Services are transport-agnostic. | `TestTransportConfined` |
| D10 | Only `internal/kyc/**` imports the KYC crypto key class or the `fundzim_kyc` pool handle; only `internal/compliance/**` imports the `fundzim_compliance` pool handle. Pool handles are distinct Go types (`db.KYCPool`, `db.CompliancePool`), so a wrong wiring fails to compile. | Type system + `TestPoolHandlesConfined` |
| D11 | `auth` may call only `notifications.Transactional.SendNow` (OTP delivery); `campaigns` and `payments` may call only `compliance.Restrictions.ActiveTx` (read-only, inside the caller's app-pool transaction, over `compliance.v_active_restrictions`). Enforced by an allow-list of root-interface methods per restricted edge. | `TestRestrictedEdgesMethods` |

## 3. Runtime and data rules

Import rules are necessary but not sufficient: a module could still reach another module's data through SQL.

| # | Rule | Enforced by |
|---|---|---|
| R1 | **Table ownership.** A module's SQL (its `store/queries/*.sql`) references only tables it owns (baseline §5), plus platform reference tables it is granted read access to (`app.currencies`, `app.markets`, `app.feature_flags` through `platform`). | `TestQueryTableOwnership` (§5.2) |
| R2 | **No cross-module joins**, not even read-only, not even in reporting. Cross-module reads go through the owner's root interface or a projection the reader owns. Reporting reads use approved `fundzim_readonly` views (owned by the module that owns the underlying tables). | R1 test; code review |
| R3 | **Foreign keys across modules** (baseline §10, as built in `design/sql`). A table may reference another module's table **only** when the target is a stable reference or identity row, and the FK is an integrity constraint, never a read or write path: no module queries or writes through it. Schema direction: `kyc`, `risk`, `compliance`, `recon`, `ledger` → `app` reference tables; `recon` → `ledger` (matches/resolutions → the journals they confirm or create); `app` and every other schema → `audit.evidence_records` (evidence ids); `app` → `risk.holds` (hold id recorded on disputes and recovery cases); `app` → `ledger` journals/adjustments (journal ids recorded on refunds, chargebacks, reservations, reversals, recovery events); **never `app` → `kyc`**, and `ledger` → only `app.currencies`. Allowed targets (complete list from the drafts): `app.currencies`, `app.markets`, `app.users`, `app.user_emails`, `app.user_phone_numbers` (auth identities), `app.organisations`, `app.campaigns`, `app.beneficiaries`, `app.institution_payees`, `app.stored_objects`, `app.fee_schedule_versions`, `app.payment_providers`, `app.provider_accounts`, `app.provider_capabilities`, `app.provider_webhook_inbox`, `audit.evidence_records`, `risk.holds`, `ledger.ledger_transactions`, `ledger.ledger_adjustments`, `ledger.ledger_posting_batches`. Adding a target needs a schema-doc change and review. | Schema review ([schema-overview.md](../database/schema-overview.md)); a Stage 3 test diffs `information_schema` cross-module FKs against this list |
| R4 | **One transaction, one pool.** A database transaction belongs to exactly one pool (`app`, `kyc`, `compliance`). Tx-joining calls are possible only between modules whose tables are reachable from that pool's role. `db.Tx` carries its pool identity and every store asserts it ([go-module-design.md §3.2](go-module-design.md)). | Runtime assertion in `store.New(tx)`; integration tests with the real roles |
| R5 | **No external I/O inside a transaction.** PSP calls, KYC vendor calls, screening vendor calls, SMS/email, object-store writes and malware scans happen before `BEGIN` or after `COMMIT`. A transaction may only *enqueue* work (outbox row or River job, both in PostgreSQL). | `platform/db.WithTx` panics in tests if a `platform/httpclient` call is made while a tx is open on the context; code review |
| R6 | **Only `ledger` writes ledger tables**, only through named posting rules (`ledger.Post*`) or approved adjustments. | R1 test + DB grants (ledger schema) |
| R7 | **Only `audit` writes audit tables.** Every module calls `audit.Recorder` inside its business transaction. | R1 test + grants |
| R8 | **Only `kyc` touches `kyc.*` and the `kyc/` prefix of the private bucket.** There are three object-storage credentials (baseline §12 I-19): `public-media` (public bucket), `private-kyc` (`kyc/` prefix, held only by `kyc`), and `private-evidence` (`compliance/` and `reports/` prefixes, held only by `storage`). Neither private credential can read the other's prefix; `storage` never holds a credential that can read `kyc/`. | R1 test + `fundzim_kyc` role + bucket policies tested in Stage 5 |
| R9 | **Only `psp` adapters talk to PSPs.** `payments`, `payouts` and `reconciliation` call `psp` interfaces. | D5 |
| R10 | **Holds only through `risk`.** No module models its own "blocked" flag for payouts; payout blocking is a `risk.holds` row (baseline §5.14). | Code review; payouts eligibility tests |
| R11 | **Events carry IDs and non-sensitive facts only.** No C3 values, no confidential hold or case reasons, no secrets in outbox payloads ([trust-boundaries.md §4](trust-boundaries.md)). | Event schema review + `TestEventPayloadClassification` (field allow-list per event) |
| R12 | **Consumers are idempotent.** Every consumer records `(consumer, event_id)` in `app.inbox_events` in the same transaction as its effect. | `platform/outbox.Consume` helper; consumer tests replay every event twice |

## 4. Event-only couplings

### 4.1 Mechanism

1. The producer writes an `app.outbox_events` row **in the same transaction** as the state change
   (`platform/outbox.Writer.Append(ctx, tx, evt)`). For transactions on the `kyc` or `compliance` pool, the
   pool's role needs INSERT on `app.outbox_events` (through `app.enqueue_outbox`, a `SECURITY DEFINER` function; baseline §12 I-13).
2. The dispatcher (worker) claims outbox rows in order and, in one transaction, inserts one River job per
   registered consumer and marks the row dispatched. The routing table (event name → consumer job kinds) lives
   in `internal/app/events.go` (composition root), so neither side imports the other.
3. The consumer job runs `platform/outbox.Consume(ctx, consumerName, evt, fn)`: it inserts
   `(consumer, event_id)` into `app.inbox_events` (unique) and runs `fn` in the same transaction. A duplicate
   is a no-op.
4. **Payload contract.** The producer owns a versioned payload (`internal/<m>/events`, plus JSON golden
   fixtures in `internal/<m>/events/testdata/<event>.v<N>.json`). Consumers **decode into their own local
   struct** (tolerant reader: unknown fields ignored, required fields validated). A contract test (§5.4)
   decodes every producer fixture with every registered consumer's decoder. Breaking payload changes create
   `v<N+1>`; producers emit both versions until every consumer has moved.
5. Envelope fields (all events): `event_id` (UUIDv7), `event_name`, `version`, `producer` (module),
   `aggregate_type`, `aggregate_id`, `occurred_at`, `recorded_at`, `correlation_id`, `request_id`,
   `actor_type`, `actor_id` (opaque ID only).

Events are **facts about the producer's own aggregate** (past tense). They are never commands to the consumer
("create a recovery case"); the consumer decides what the fact means for it.

### 4.2 Catalogue of inter-module events

Consumers listed with ✓ also hold an import edge to the producer; the rest are event-only (E in the matrix).
"Consistency" states what the consumer may assume about lag.

| Event | Producer | Consumers | Consumer reaction | Consistency and safety net |
|---|---|---|---|---|
| `user.registered` | users | risk; notifications | risk: start linkage graph node; notifications: welcome (if consented) | Seconds; no financial decision depends on it |
| `user.contact_changed` (phone/email) | users | auth ✓; risk; notifications | auth: revoke OTP challenges and sessions bound to the old factor; risk: signal + `ACCOUNT_HOLD`/`PAYOUT_HOLD` per policy (T-05); notifications: alert **old and new** channels | Payout safety does not wait for this: payout request and submit re-check holds and destination age (EC-06, EC-12, EC-16) |
| `auth.login_succeeded`, `auth.otp_failed`, `auth.session_revoked` | auth | risk | risk signals (velocity, device change) | Best-effort monitoring |
| `auth.role_granted`, `auth.role_revoked`, `auth.break_glass_started` | auth | notifications | Alert security owner and second senior staff (SECURITY §5.2, §5.5) | Seconds; the grant itself is maker-checker and audited synchronously |
| `organisation.created` | organisations | kyc ✓ | Open a `kyb_organisations` profile at `ORG_UNVERIFIED` | Seconds; KYB gates fail closed while the profile is missing |
| `organisation.member_changed`, `organisation.invitation_created` | organisations | notifications; kyc ✓ | notifications: invite / role-change messages; kyc: re-verification trigger for representatives (kyb §3.4) | Seconds |
| `kyc.level_changed`, `kyc.status_changed` | kyc | users; risk; compliance ✓; payouts ✓; campaigns ✓; notifications | users: update the verification **mirror** (display, filtering); risk: monitoring; compliance: screening at `IDENTITY_VERIFIED`, PEP flow; payouts: re-check in-flight payouts (Y7); campaigns: re-evaluate submission gates; notifications: owner message | **Projection is eventual.** Every gate that matters (EC-03, campaign submission) calls `kyc` live. |
| `kyb.level_changed`, `kyb.status_changed` | kyc | organisations; compliance ✓; payouts ✓; campaigns ✓; notifications | organisations: update `organisation_verifications`; others as above | As above |
| `kyc.fundraising_authority_expiring`, `kyc.fundraising_authority_expired` | kyc | campaigns ✓; beneficiaries ✓; notifications | campaigns: re-review flag and `PAYOUT_HOLD` via risk; notifications: owner reminder | Payout path also checks authority live through `beneficiaries`/`kyc` |
| `storage.object_promoted`, `storage.object_rejected` | storage | campaigns ✓; kyc ✓; notifications | campaigns: attach media variant; kyc: mark document ready for review; notifications: "upload rejected" | Seconds; media stays hidden until promoted |
| `beneficiary.verification_decided` | beneficiaries | campaigns ✓; payouts ✓; compliance; notifications | campaigns: submission/approval gate; payouts: re-check (EC-04); compliance: screen beneficiary; notifications | EC-04 is checked live at request and submit |
| `risk.hold_placed`, `risk.hold_released` | risk | payouts ✓; campaigns ✓; notifications | payouts: invalidate approvals (Y7) or retry deferred submits; campaigns: post `hold:{id}:applied` / inverse for campaign-scope `COMPLIANCE_HOLD` (see §4.3); notifications: owner "payouts paused — under review" **without reason** (LR-008) | EC-12 reads holds live, so the event is not the safety mechanism |
| `risk.alert_raised` | risk | compliance ✓ | Open or link a compliance case from a monitoring alert | Minutes acceptable; alert itself is durable |
| `compliance.case_blocking_changed` | compliance | payouts ✓ | Re-check in-flight payouts (EC-13) | Payload: subject refs, case id and blocked flag only (no type, reason or STR flag), mirroring `v_payout_blocking_cases` |
| `compliance.restriction_applied`, `compliance.restriction_lifted` | compliance | campaigns ✓; risk; notifications | campaigns: pause submissions for `CREATE_CAMPAIGN`; risk: monitoring; notifications: neutral message (LR-008) | Restriction is checked live by the acting module (`campaigns`/`payments` import `compliance`) |
| `campaign.submitted`, `campaign.material_edit_submitted` | campaigns | risk; compliance | risk: assessment; compliance: owner/beneficiary screening | Review cannot complete until screening and risk results exist (campaign-approval-policy §2) |
| `campaign.status_changed` (approved, activated, suspended, completed) | campaigns | notifications; risk | Owner/follower messages; monitoring | — |
| `campaign.frozen`, `campaign.unfrozen` | campaigns | payouts ✓; notifications; risk | payouts: re-check (freeze hold already blocks); notifications: owner and staff | Freeze hold + ledger move are atomic with the freeze (§ TX-13 in [system-overview.md](system-overview.md)) |
| `campaign.cancelled` | campaigns | payouts ✓; payments ✓; notifications | payouts: cancel payouts not yet `SUBMITTED` (Y5); payments: stop new intents (also checked live) and open the refund disposition workflow for staff; notifications | Payouts at request/submit check EC-01 live |
| `campaign.report_submitted` | campaigns | risk; compliance | Signal, triage | — |
| `payment.succeeded`, `payment.status_changed` | payments | notifications; risk | Receipt to donor; monitoring | — |
| `refund.completed`, `refund.failed` | payments | notifications; risk | Donor/owner messages; monitoring | — |
| `refund.shortfall` | payments | **payouts** | Open a recovery case for `refund_recoverable` shortfall and place `RECOVERY_HOLD` | **E** (payments ✗ payouts). Seconds of lag; see F-14 in trust-boundaries §6.4 |
| `dispute.opened`, `dispute.closed` | payments | risk; notifications | monitoring; owner messages | `DISPUTE_HOLD` is placed synchronously in the dispute transaction (TX-08) |
| `dispute.shortfall` | payments | **payouts** | Open a recovery case for `chargeback_recoverable` shortfall, place `RECOVERY_HOLD` | **E**. The `DISPUTE_HOLD` placed in TX-08 already blocks payouts while the case is created |
| `payout.requested`, `payout.status_changed` | payouts | notifications; risk | Owner messages; monitoring (velocity) | — |
| `payout_destination.changed` | payouts | notifications; risk; compliance | All verified channels notified (old and new); risk signal; rescreen payee | `DESTINATION_HOLD` placed in the same transaction (TX-19) |
| `settlement.matched` | reconciliation | payments | Schedule `payment:{id}:release` after the post-settlement hold period (PD-14) | **E**. Funds stay in `campaign_unsettled` (not withdrawable) until release is posted; lag is safe |
| `reconciliation.discrepancy_opened` | reconciliation | risk; notifications | SEV1 class → `PROVIDER_HOLD` for provider+currency; page FINANCE | EC-20 additionally reads settlement freshness from `psp` live (C-2) |
| `ledger.invariant_failed`, `ledger.pool_integrity_breached` | ledger | risk; notifications | risk: `PROVIDER_HOLD` for provider+currency (SC-2); notifications: SEV1 page | EC-19 also runs the pool-integrity check live before every submit |

### 4.3 Why some effects are synchronous and some are events

Use a **tx-joining call** when the invariant must hold at commit, otherwise an **event**:

| Effect | Mode | Reason |
|---|---|---|
| Ledger posting for a state transition | Tx-joining (`ledger.Post*`) | CLAUDE.md rule 5: transition and journal commit together |
| Audit event for an action | Tx-joining (`audit.Recorder`) | An action without its audit record must not commit |
| Placing `DISPUTE_HOLD`, `DESTINATION_HOLD`, `CAMPAIGN_FREEZE`, `COMPLIANCE_HOLD` | Tx-joining (`risk.Holds.Place`) | Payouts must be blocked from the moment the cause commits |
| Ledger move for a campaign-scope `COMPLIANCE_HOLD` (`hold:{id}:applied`) | **Event** (`risk.hold_placed` → campaigns) | `compliance` may not import `ledger` or `campaigns` (`campaigns` imports `compliance`), and runs on its own pool. Safe because EC-12 blocks on the hold row itself, which is atomic with the decision; the ledger move is defence in depth and follows within seconds. |
| Recovery case after a dispute or refund shortfall | **Event** | `payments` ✗ `payouts`; the synchronous `DISPUTE_HOLD` covers the gap |
| KYC level mirror on `users` / `organisation_verifications` | **Event** | Different pool (kyc), and display-only data |
| Notifications | **Event** | External I/O (R5) |

## 5. CI enforcement (Stage 3)

All checks run in `go test ./tests/architecture/...` and fail the build. The allowed-dependency data lives in
one file, `tests/architecture/modules.yaml`, which mirrors baseline §3 and §5; a test asserts that file and
the baseline tables agree (it parses the two Markdown tables), so the documents and the check cannot drift.

### 5.1 Import graph test

```go
// Sketch, not compiled (Go not installed in Stage 2).
// tests/architecture/imports_test.go
func TestModuleImportGraph(t *testing.T) {
    allowed := loadModules(t, "modules.yaml")          // module -> allowed root imports
    pkgs := goList(t, "./internal/...", "./apps/...")  // `go list -deps -json`, parsed
    for _, p := range pkgs {
        from := moduleOf(p.ImportPath)                  // "" for non-internal packages
        for _, imp := range p.Imports {                 // direct imports only: transitive ones are covered by recursion
            to := moduleOf(imp)
            if from == "" || to == "" || from == to { continue }
            if from == "app" { continue }               // composition root
            if !isRootPackage(imp) { t.Errorf("%s imports sub-package %s", p.ImportPath, imp); continue }
            if !allowed[from].Has(to) { t.Errorf("forbidden import %s -> %s (%s)", from, to, p.ImportPath) }
        }
    }
    assertAcyclic(t, allowed)                           // Kahn's algorithm, as in dependency-matrix §3
}
```

`go list -deps -json ./...` is part of the standard toolchain, so no extra dependency is needed. A
`golang.org/x/tools/go/analysis` analyser is an option later if editor-time feedback is wanted.

### 5.2 Query table-ownership test

sqlc compiles `internal/<m>/store/queries/*.sql` against the migrations. The test parses every query file with
the PostgreSQL parser (`pg_query_go`, the same parser sqlc uses) and collects every `RangeVar`
(schema.table) referenced, including in CTEs, subqueries, `INSERT ... SELECT`, `UPDATE ... FROM` and `JOIN`s:

```go
// Sketch, not compiled.
func TestQueryTableOwnership(t *testing.T) {
    owners := loadOwnership(t, "modules.yaml") // "ledger.ledger_entries" -> "ledger", from baseline §5
    for _, f := range glob(t, "internal/*/store/queries/*.sql") {
        mod := moduleOfPath(f)
        for _, rel := range relationsIn(t, f) { // via pg_query parse tree
            o, ok := owners[rel]
            switch {
            case !ok:                 t.Errorf("%s: unknown relation %s (add to baseline §5 via ADR)", f, rel)
            case o == mod:            // own table
            case isGrantedRef(rel, mod): // e.g. app.currencies read by any module
            default:                  t.Errorf("%s: module %s references %s owned by %s", f, mod, rel, o)
            }
        }
    }
}
```

The same test rejects dynamic SQL: store packages may not call `Exec`/`Query` with a non-constant string
(a vet check), so every statement passes through sqlc.

### 5.3 Other architecture tests

| Test | Checks |
|---|---|
| `TestNoForeignSubpackageImports` | D2 |
| `TestVendorImportsConfined`, `TestPgxConfined`, `TestTransportConfined`, `TestTestPackagesOnlyInTests`, `TestPoolHandlesConfined` | D5, D6, D9, D7, D10 |
| `TestEveryRouteHasPolicy` | Every route registered on the router declares a policy (public, user, owner-of, staff permission) — SECURITY §5.1 deny by default |
| `TestEveryAuditActionDeclared` | Audit actions used in code are in the closed catalogue ([audit-evidence-model.md §4](../compliance/audit-evidence-model.md)) |
| `TestErrorCodesOwned` | Every error code is declared once, in the root package of the module that owns it, and appears in [error-model.md](../api/error-model.md) |
| `TestStateEdgesMatchSQL` | Each Go state machine's edge list equals the rows inserted into `app.status_transitions` for that machine |

### 5.4 Event contract tests

| Test | Checks |
|---|---|
| `TestEventRoutingTargetsExist` | Every event in `internal/app/events.go` is declared by its producer and every consumer job kind is registered |
| `TestConsumersDecodeProducerFixtures` | Each consumer's decoder accepts every fixture version the producer still emits |
| `TestEventPayloadClassification` | Payload fields are on the event's allow-list; no field name matches the C3/secret deny-list (`*_number`, `*_ciphertext`, `dob`, `reason_text` on confidential holds, …) |
| `TestEventCatalogueDocumented` | Every routed event appears in §4.2 of this document |

## 6. Requesting a new dependency

1. Try, in order: (a) an event the consumer already receives; (b) a projection the consumer owns, fed by
   events; (c) moving the logic to a module that already has the needed imports. Most "I need to read X"
   requests are solved by (a) or (b).
2. If a compile-time edge is still needed, write an ADR that amends ADR-021: the edge, the use cases, the
   alternatives tried, and a proof that the new graph is acyclic (re-run the topological order in
   [dependency-matrix.md §3](dependency-matrix.md)).
3. Financial modules (`ledger`, `payments`, `payouts`, `fees`, `reconciliation`, `psp`) and `kyc`: the ADR
   needs technical-lead review (ADR-001 rule 4).
4. In the same change: update baseline §3, `tests/architecture/modules.yaml`, the matrix, and this document.
   The baseline-vs-yaml test fails if only some are updated.
5. **Table ownership moves** follow the same process (baseline §5 + `modules.yaml` + the owning schema doc).

A "temporary" exception comment in the test file is not an allowed mechanism.

## 7. Design concerns found in Stage 2 and their resolution

The first draft of this design found needs the original baseline did not cover. The lead amended
[design-baseline.md](../stage-2/design-baseline.md) §3, §5.4, §5.11 and §5.14; no interim wiring exceptions
(ports in `internal/app`) remain.

| ID | Need | Resolution |
|---|---|---|
| C-1 | `campaigns` must refuse campaign creation and `payments` must refuse donations from a subject with an active `CREATE_CAMPAIGN` / `DONATE` restriction (baseline §5.14). | **Baseline §3 amended:** `campaigns` → `compliance` and `payments` → `compliance`, read-only restriction checks (`compliance.Restrictions.ActiveTx`). The check runs **inside** the caller's app-pool transaction over the view `compliance.v_active_restrictions` (subject, capability, expires_at; no reasons), granted to `fundzim_app` — no gap between check and commit. |
| C-2 | EC-20 (settlement freshness) needs reconciliation state, but `payouts` may not import `reconciliation`. | **Solved in-graph:** `reconciliation` records each completed run per provider+currency, and each SEV1 discrepancy, as `psp.provider_health_events`; `payouts` reads `psp.Health.RailStatus`. No row → stale → `DEFER` (fails closed). Open schema item: add `SETTLEMENT_RECONCILED`, `RECON_SEV1_OPENED`, `RECON_SEV1_CLOSED` to `ck_provider_health_events_kind` (and a currency column if absent) in `design/sql/0007_fees_psp.sql`. |
| C-3 | Non-KYC evidence objects (dispute packs, statement files, destination-ownership evidence, STR attachments) are stored via `storage` (baseline §6 #2). | **Baseline §3 amended:** `compliance`, `payments`, `payouts`, `reconciliation` → `storage`. They write the object through `storage`, then register it with `audit.Evidence.Record`; `reconciliation` reads statement content with `storage.Open`. |
| C-4 | Transactions on the `kyc` and `compliance` pools must write `audit.*`, `app.outbox_events` and (TX-22) `risk.holds` atomically. | **Resolved:** audit and outbox writes from the `kyc`/`compliance` pools go through `SECURITY DEFINER` functions (`audit.append_event`, `audit.append_security_event`, `app.enqueue_outbox`; baseline §12 I-13), and the compliance role has minimal privileges on `risk.holds`/`hold_events`/`monitoring_alerts` (I-14). |
| C-5 | EC-15 must be evaluated in the payout's app-pool transaction. | **Baseline §5.14 amended:** view `compliance.v_screening_status` (subject, latest result, screened_at, list version; no hit details) granted to `fundzim_app`; `v_payout_blocking_cases` now also carries `case_id`. |
| C-6 | Tables implied by Stage 1 but absent from the baseline. | **Baseline §5 amended:** `app.break_glass_grants`, `app.staff_conflict_declarations` (`auth`); `kyc.gate_policies`, `kyc.vendor_callback_inbox` (`kyc`); `compliance.screening_callback_inbox` (`compliance`). |
| C-7 | `auth` must deliver OTP codes without passing them through the outbox or job tables. | **Baseline §3 amended:** `auth` (now layer 4) → `notifications`, for synchronous OTP delivery only (`notifications.Transactional.SendNow`, outside any transaction, after the challenge row commits; only the HMAC of the code is stored). |
| C-8 | Stage 1 handover §5/§9 asks for a generic `approval_requests` table. | **Deliberate deviation (lead decision):** per-module request tables (`payout_approvals`, `refund_requests`, `ledger_adjustments`, `role_assignment_requests`, `limit_change_requests`, `fee_schedule_change_requests`, `compliance_restrictions`, `break_glass_grants`, `hold_release_requests`, `payout_destination_override_requests`, `feature_flag_changes`, maker/checker columns on `payment_disputes` and `campaign_moderation_actions`; baseline §12 I-3, I-9) plus the `platform/makerchecker` code library. Each table carries its own DB check (`approved_by <> requested_by`). |
