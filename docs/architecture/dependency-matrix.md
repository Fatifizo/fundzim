# Module Dependency Matrix

**Stage 2 — design only.** This matrix is generated from [design-baseline.md §3](../stage-2/design-baseline.md)
(the contract) and the event catalogue in [dependency-rules.md §4](dependency-rules.md). If the two disagree,
the baseline wins and this file is the defect. Changing an allowed import needs an ADR that amends ADR-021.

Related: [dependency-rules.md](dependency-rules.md) · [go-module-design.md](go-module-design.md) ·
[domain-boundaries.md](domain-boundaries.md) · [ADR-021](../adr/ADR-021-module-boundaries-and-ownership.md)

---

## 1. Legend

| Mark | Meaning |
|---|---|
| ✓ | **Allowed** compile-time import of the importee's **root package** (its public service interface and types). Importing any sub-package (`store`, `service`, `http`, `jobs`, `events`, adapters) of another module is always forbidden. |
| ✗ | **Forbidden.** No import, no runtime call through a wired port, and no SQL against the importee's tables. |
| E | **Event-only.** The import is forbidden, but the row module **consumes** outbox events that the column module **produces**. The consumer decodes the event into its own local struct; it never imports the producer's `events` package (see [dependency-rules.md §4.1](dependency-rules.md)). |
| — | Self. |

Rows are the **importer**. Columns are the **importee**. Column abbreviations: `plat` platform, `aud` audit,
`usr` users, `sto` storage, `led` ledger, `fee` fees, `psp` psp, `rsk` risk, `org` organisations,
`ntf` notifications, `ath` auth, `kyc` kyc, `ben` beneficiaries, `cmp` compliance, `cpg` campaigns, `pay` payments,
`pyo` payouts, `rec` reconciliation, `adm` admin.

Where a module both imports another **and** consumes its events (for example `payouts` consumes
`risk.hold_placed` and also imports `risk`), the cell shows ✓. Those event subscriptions are listed in
[dependency-rules.md §4.2](dependency-rules.md).

## 2. The matrix

Columns and rows are in topological (layer) order, so every ✓ lies **left of the diagonal**. A ✓ to the right
of the diagonal would be a cycle; there are none.

| importer ↓ / importee → | `plat` | `aud` | `usr` | `sto` | `led` | `fee` | `psp` | `rsk` | `org` | `ntf` | `ath` | `kyc` | `ben` | `cmp` | `cpg` | `pay` | `pyo` | `rec` | `adm` |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `platform` | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `audit` | ✓ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `users` | ✓ | ✓ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | E | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `storage` | ✓ | ✓ | ✗ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `ledger` | ✓ | ✓ | ✗ | ✗ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `fees` | ✓ | ✓ | ✗ | ✗ | ✗ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `psp` | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `risk` | ✓ | ✓ | E | ✗ | E | ✗ | ✗ | — | ✗ | ✗ | E | E | ✗ | E | E | E | E | E | ✗ |
| `organisations` | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | — | ✗ | ✗ | E | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `notifications` | ✓ | ✓ | ✓ | E | E | ✗ | ✗ | E | E | — | E | E | E | ✗ | E | E | E | E | ✗ |
| `auth` | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `kyc` | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `beneficiaries` | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ | ✓ | — | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `compliance` | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✓ | ✓ | ✗ | ✗ | ✓ | E | — | E | ✗ | E | ✗ | ✗ |
| `campaigns` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✓ | ✓ | ✗ | ✗ | ✓ | ✓ | ✓ | — | ✗ | ✗ | ✗ | ✗ |
| `payments` | ✓ | ✓ | ✗ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | — | ✗ | E | ✗ |
| `payouts` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ | E | — | ✗ | ✗ |
| `reconciliation` | ✓ | ✓ | ✗ | ✓ | ✓ | ✗ | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ | — | ✗ |
| `admin` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |

`internal/app` (composition root) is not a module. It imports every module's root package **and** the
`service`, `store`, `http` and `jobs` sub-packages it needs for wiring. Nothing imports `internal/app`.

Counts: 80 allowed edges between the 18 non-admin modules, plus `admin` → 18. 27 event-only couplings.

### 2.1 Who may import each module (reverse view)

| Module | Imported by (excluding `admin`, `internal/app`) |
|---|---|
| `platform` | every module |
| `audit` | every module except `platform` |
| `users` | organisations, notifications, auth, kyc, beneficiaries, compliance, campaigns, payouts |
| `storage` | kyc, compliance, campaigns, payments, payouts, reconciliation |
| `ledger` | campaigns, payments, payouts, reconciliation |
| `fees` | campaigns, payments, payouts |
| `psp` | payments, payouts, reconciliation |
| `risk` | compliance, campaigns, payments, payouts |
| `organisations` | kyc, beneficiaries, compliance, campaigns, payouts |
| `notifications` | auth (synchronous OTP delivery only) |
| `kyc` | beneficiaries, compliance, campaigns, payouts |
| `beneficiaries` | campaigns, payouts |
| `compliance` | campaigns (restriction checks, read-only), payments (restriction checks, read-only), payouts |
| `campaigns` | payments, payouts |
| `payments` | reconciliation |
| `payouts` | reconciliation |
| `auth`, `reconciliation` | nobody (reached only through HTTP routes, jobs and events) |

Two consequences matter for implementation:

- **Nobody imports `auth`.** Services check authorisation through the principal that `platform/actor` puts
  on the context (populated by `auth`'s authenticator, wired in `internal/app`), not by calling `auth`
  ([go-module-design.md §3](go-module-design.md)).
- **Only `auth` imports `notifications`, and only for synchronous OTP delivery** (the code is passed in
  memory, never written to the outbox, jobs or logs). Every other message is triggered by an outbox event that
  carries the non-sensitive data the template needs (ADR-001 rule 5).

## 3. Acyclicity proof (topological order)

The graph is acyclic: the following order lists every module after all modules it imports (Kahn's algorithm
over the amended baseline §3 table, re-run after the C-1/C-3/C-7 amendments; Stage 3 repeats it as a CI test).

| # | Module | Layer | Imports (all earlier in this list) |
|---|---|---|---|
| 1 | `platform` | 0 | — |
| 2 | `audit` | 1 | platform |
| 3 | `users` | 2 | platform, audit |
| 4 | `storage` | 2 | platform, audit |
| 5 | `ledger` | 2 | platform, audit |
| 6 | `fees` | 2 | platform, audit |
| 7 | `psp` | 2 | platform, audit |
| 8 | `risk` | 2 | platform, audit |
| 9 | `organisations` | 3 | platform, audit, users |
| 10 | `notifications` | 3 | platform, audit, users |
| 11 | `auth` | 4 | platform, audit, users, notifications |
| 12 | `kyc` | 4 | platform, audit, storage, users, organisations |
| 13 | `beneficiaries` | 5 | platform, audit, users, organisations, kyc |
| 14 | `compliance` | 5 | platform, audit, users, organisations, kyc, risk, storage |
| 15 | `campaigns` | 6 | platform, audit, users, organisations, kyc, beneficiaries, storage, risk, ledger, fees, compliance |
| 16 | `payments` | 7 | platform, audit, campaigns, fees, ledger, risk, psp, storage, compliance |
| 17 | `payouts` | 7 | platform, audit, users, organisations, kyc, beneficiaries, compliance, campaigns, ledger, risk, psp, fees, storage |
| 18 | `reconciliation` | 8 | platform, audit, ledger, psp, payments, payouts, storage |
| 19 | `admin` | 9 | all of the above (root packages only) |
| — | `internal/app` | — | everything |

Every import points to a strictly lower layer. Because layers are a total preorder and no module imports a
module in the same or a higher layer, no cycle can exist. The Stage 1 handover asked specifically about
`payouts` ↔ `compliance` and `campaigns` ↔ `risk`: both are one-directional (`payouts → compliance`,
`campaigns → risk`), and the reverse effects travel as events (`compliance.case_blocking_changed`,
`risk.hold_placed`).

## 4. Deliberately forbidden pairs

These are the ✗ cells someone is most likely to want to "just add". Each needs an ADR to change.

| Importer → importee | Why it is forbidden | How the need is met instead |
|---|---|---|
| `payments` → `payouts` and `payouts` → `payments` | Two financial state machines must not drive each other synchronously; a bug in one would cascade into the other's money movements (baseline §3; ARCHITECTURE §4.2). | Outbox events: `dispute.shortfall` and `refund.shortfall` → `payouts` opens a recovery case. Dispute exposure for EC-14 is read from the ledger and from `DISPUTE_HOLD` rows in `risk`. |
| `ledger` → anything but platform, audit | The ledger is domain-agnostic: it knows accounts, not campaigns (ADR-006, LEDGER §5). | Callers pass `(owner_type, owner_id)` and source references; named posting rules live in `ledger`. |
| `fees` → `ledger` | Fees calculate, never post (ARCHITECTURE §4.2). | The caller (`payments`, `payouts`) posts the computed amounts. |
| `risk` → any domain module | `risk` is a leaf so that every module can place or check holds without cycles (baseline §3, ADR-021). | Domain modules call `risk`; `risk` reacts to their events (monitoring, `PROVIDER_HOLD` on reconciliation SEV1 or pool-integrity breach). |
| `organisations` → `kyc` | Stage 0 allowed a status query; it would make `kyc` → `organisations` (needed for KYB) a cycle (baseline §3). | `organisation_verifications` projection fed by `kyb.*` events; live decisions call `kyc` directly. |
| `users` → `kyc` | Same reason; `kyc` imports `users`. | The users-side verification mirror is a projection fed by `kyc.level_changed` / `kyc.status_changed`. |
| `compliance` → `payouts`, `payments`, `campaigns` | Compliance places blocks; it must not depend on the things it blocks. Since `campaigns` and `payments` now import `compliance` for restriction checks, the reverse edge would be a direct cycle. | Holds through `risk`; case links by ID; events (`compliance.case_blocking_changed`, `compliance.restriction_*`). |
| `campaigns` → `payments` / `payouts` | Campaigns are upstream of money; reading payment or payout tables from campaigns would invert ownership. | Raised and available amounts come from `ledger` projections; payout cancellation on campaign cancel is an event (`campaign.cancelled` → `payouts`). |
| `reconciliation` → `risk`, `campaigns` | Not needed for matching, and holds are a policy reaction, not part of a match. | `reconciliation.discrepancy_opened` (SEV1) → `risk` places `PROVIDER_HOLD`; settlement freshness is written to `psp` provider health (EC-20). |
| any module → `auth` | Authentication is an edge concern; services must not call it to make decisions (and it would create many edges). | `platform/actor.Principal` on the context; `platform/makerchecker` for SoD checks. |
| any module except `auth` → `notifications` | Delivery leaves the process and must never be inside a business transaction (ADR-001 rule 5). `auth`'s exception is limited to synchronous OTP delivery outside any transaction. | Outbox events consumed by `notifications`. |
| any non-`kyc` module → `kyc` sub-packages or `kyc` schema | C3 boundary (ADR-009, SECURITY §2). | `kyc` root package exposes levels, statuses and opaque references only. |
| any module → `admin` | `admin` is a pure composition of handlers; importing it would make business code depend on staff UI flows. | — |
| any module → `internal/app` | Composition root. | — |

## 5. Amendments applied in Stage 2

The first draft of this design found needs the original baseline graph did not cover. The lead amended
baseline §3 (and §5) accordingly; this matrix reflects the amended graph.

| ID | Edge added | Need |
|---|---|---|
| C-1 | `campaigns` → `compliance`, `payments` → `compliance` | Read-only checks of `CREATE_CAMPAIGN` / `DONATE` capability restrictions (baseline §5.14) |
| C-3 | `compliance`, `payments`, `payouts`, `reconciliation` → `storage` | Write evidence and statement objects, then register them with `audit` (baseline §6 #2) |
| C-7 | `auth` → `notifications` (`auth` moved to layer 4) | Synchronous OTP delivery without the code touching the outbox or job tables |

C-2 (settlement freshness for EC-20) needed no graph change: it is solved through `psp` provider health
([dependency-rules.md §7](dependency-rules.md)).
