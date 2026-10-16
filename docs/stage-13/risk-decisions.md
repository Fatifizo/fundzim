# Stage 13 — Risk decisions

Three things are kept apart (ADR-045 §5): the engine's **recommendation** (`risk_evaluations.recommended_action`), the
**automated actions** it is allowed to take (`risk_evaluation_actions`, always `automated = true`), and the **human
outcome** of a case (`risk_cases.decision_outcome`, maker-checker).

## 1. Recommendation → automated action

| Recommended action | Automated action | Human step |
|---|---|---|
| ALLOW | NO_ACTION recorded | — |
| ALLOW_WITH_MONITORING | monitoring event `RULE_MONITORING_MATCH` | visible on `/admin/risk/monitoring` |
| REVIEW_REQUIRED | open or link one case (priority from the rules) with the evaluation and matched signals as evidence | investigate |
| TEMPORARY_HOLD | the case, plus a temporary WITHDRAWAL_HOLD (subject: user, organisation or campaign) or PAYOUT_DESTINATION_HOLD (destination named by the signal) of 1 hour–7 days | release or maintain through the case |
| ESCALATE, BLOCK, or a CRITICAL classification | a **P1** case; nothing is blocked automatically | a human decides (and escalates to compliance) |

## 2. Automated decision limits

The engine never: bans or suspends an account, confiscates or moves funds, declares fraud, files or drafts a regulatory
report, terminates or suspends a campaign, applies a compliance restriction, clears a screening, releases a hold, or makes
an irreversible decision. The only automated enforcement is a temporary, payout-protective hold that always comes with a
case for a human (CHECK `ck_risk_holds_automated`: payout-protective type, linked evaluation, expiry ≤ 7 days). Whether
even that is a "solely automated decision" under CDPA s25 is LR-071 (open); the hold is reviewable and time-bound.

## 3. Human outcomes (maker-checker)

| Outcome | Effect on the case's holds |
|---|---|
| NO_FURTHER_ACTION | released (proposer = release maker, approver = checker; step-up) |
| FALSE_POSITIVE | released; signal reviews label the signals; nothing is deleted |
| RISK_CONFIRMED_HOLD_MAINTAINED | temporary automated holds are replaced by manual holds without expiry |
| ESCALATED_TO_COMPLIANCE | (requires prior escalation) as above; compliance decides on restrictions |

No outcome is named or worded as fraud (CHECKs on outcomes, events, rule codes, hold reasons and explanations).

## 4. Auditability

Each evaluation is immutable and reproducible (`inputs_hash`, rule version ids); actions, case events, hold events and
decisions are append-only (`risk_events`, `audit`); evaluations, actions and evidence cannot be updated or deleted
(triggers). Outbox events let other modules observe decisions without reading risk tables.
