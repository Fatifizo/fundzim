# Stage 13 — Risk case management

A risk case (`risk.risk_cases`) is an operational investigation. Legal restrictions are not a risk outcome: they come only
from a compliance resolution, reached by escalation.

## 1. Opening, deduplication and prioritisation

Cases are opened by the engine (REVIEW_REQUIRED or above), by screening (potential match) or by staff (`risk.case.work`).
**One open case per subject and case type** (`dedupe_key`, unique partial index): later evaluations link to it, keep the most
urgent priority and the highest score seen, and add their evaluation and matched signals as evidence (each reference once).
Case types: TRANSACTION_MONITORING, CAMPAIGN_RISK, ACCOUNT_TAKEOVER, DESTINATION_RISK, WITHDRAWAL_RISK, SCREENING_REVIEW,
FEE_MANIPULATION, IDENTITY_RISK, OTHER (derived from the signal category for engine cases). Priority P1–P4: the most urgent
fired rule's priority; P1 for BLOCK, ESCALATE or CRITICAL. `due_at` = opening + an internal ENGINEERING_DEFAULT target
(P1 24 h, P2 72 h, P3 7 days, P4 14 days) used only for queue ordering and overdue monitoring — **not a regulatory deadline**
(none is known, LR-074). The queue orders by open-first, priority, due date.

## 2. Lifecycle (`risk_case`, database-guarded)

```
OPEN → TRIAGE → UNDER_INVESTIGATION ⇄ AWAITING_INFORMATION
          │            │  ⇅
          ├──────► ESCALATED ──► (UNDER_INVESTIGATION)
          └────────────┴──────► DECISION_PENDING ⇄ UNDER_INVESTIGATION (returned)
                                      │
                                   RESOLVED → CLOSED;  RESOLVED/CLOSED → REOPENED → TRIAGE
```

Assignment moves OPEN/REOPENED to TRIAGE. RESOLVED is reachable only from DECISION_PENDING (a proposal exists).

## 3. Assignment and conflicts of interest

Assignee: the caller by default, or another staff member holding `risk.case.work` (checked via
`auth.Service.StaffHasPermission`). Nobody works, is assigned, decides or places holds on a case about themselves or an
account linked to theirs (`app.actor_identities`; Go `RISK_CONFLICT_OF_INTEREST` and the database guard). Every assignment
is an append-only row.

## 4. Decisions (maker-checker)

`propose-decision` (maker, `risk.case.work`): outcome + reason → DECISION_PENDING. `approve-decision` / `return-decision`
(checker, `risk.case.approve`, fresh step-up): never the proposer or anyone linked to the proposer or the subject (Go and
database). Outcomes and their effect on holds: [risk-decisions.md](risk-decisions.md). Reopening keeps the previous decision in
the history (`risk_events` CASE_RESOLVED / CASE_REOPENED).

## 5. Evidence

Types: SIGNAL, EVALUATION, SOURCE_EVENT, VERIFICATION_REF, CAMPAIGN_REF, PAYMENT_REF, PAYOUT_REF, PROVIDER_EVIDENCE, STAFF_NOTE,
DOCUMENT_REF, SCREENING_RESULT, HOLD_REF. Evidence is **immutable** (no update/delete/truncate triggers) and **content-hashed**:
`content_hash` = SHA-256 of the canonical JSON (keys sorted at every depth, numbers verbatim), verifiable from the stored
`jsonb` (`VerifyEvidence`; reconciliation recomputes the last 30 days). A correction is a new record with `supersedes_id`;
the original stays and is shown as superseded. Evidence references other modules' records by id — identity documents stay in
`kyc` and are never copied. Retention class `RISK_RECORD_LEGAL_REVIEW_REQUIRED` (LR-012): never purged. Every case view and
evidence view is logged in `risk_case_access_log`.

## 6. Escalation to compliance

`escalate` (→ ESCALATED, team COMPLIANCE) emits `risk.case_escalated` {case id, number, type, subject, priority,
reason code}. The compliance consumer (`compliance.open_case`) opens or links a RISK_REVIEW compliance case (severity S1 for
P1, S2 for P2, else S3) with links to the subject (when it is a party) and the `RISK_CASE` (related object); redelivery is a
no-op. An analyst-confirmed screening match escalates automatically.

## 7. False positives

`signal-reviews` labels a case's signal FALSE_POSITIVE, RELEVANT or INCONCLUSIVE with an explanation (one review per signal
and case; append-only). Signals are never deleted; the quality report counts reviews per rule ([testing.md](testing.md)).

## 8. Monitoring

`risk.reconcile` (15 min) records CASE_OVERDUE (past `due_at`) and CASE_UNASSIGNED_CRITICAL (P1 unassigned for an hour)
monitoring events, deduplicated per case and day.
