# Stage 13 — Risk rules

A rule (`risk.risk_rules`) names what is evaluated (subject type, category); its versions (`risk.risk_rule_versions`) carry
the behaviour. **Rules are data, never code**: a JSON condition tree over a closed set of condition types.

## 1. Definition language

```json
{"when": {"all": [
  {"fact_compare":   {"signal_types": ["CAMPAIGN_REPORTED"], "fact": "reason_code", "window_seconds": 3600, "op": "=", "value": "MISLEADING"}},
  {"signal_count":   {"signal_types": ["CAMPAIGN_REPORTED"], "window_seconds": 3600, "op": ">=", "value": 2}}
]}}
```

| Condition | Meaning | Use |
|---|---|---|
| `all` / `any` | conjunction / disjunction | composition |
| `signal_count` | number of signals of the types within the window | thresholds, counts, velocity (per minute/hour/day) |
| `distinct_count` | distinct values of a fact within the window | distinct campaigns per donor, destinations per owner |
| `fact_compare` | a signal of the types with the fact compared true (numbers numerically; strings with = / !=) | amounts, codes |
| `sequence` | the types occurred in order within the window | cross-event patterns (e.g. MFA change → destination change → withdrawal) |
| `state` | ACTIVE_HOLDS, OPEN_CASES (numbers), SCREENING_STATUS, PRIOR_CLASSIFICATION (strings) | state-based rules |

Facts a rule may read: `amount_minor`, `campaign_id`, `destination_id`, `currency`, `reason_code`, `title_hash`, `ua_hash`,
`payout_request_id`, `beneficiary_id`, `method` (ids, codes, amounts — never personal data). Operators: `>= > <= < = !=`.

## 2. Rule safety (validated at create, edit and every evaluation)

Unknown fields are refused (`DisallowUnknownFields`: no `script`, `eval`, regex or SQL can be smuggled in); every signal
type must be catalogued; windows ≤ 90 days; depth ≤ 3; ≤ 12 conditions; ≤ 5 sequence steps; ≤ 10 signal types per
condition; numbers within ±10¹⁸; strings 1–64 characters; definitions ≤ 16 KiB; contribution 0–100; ALLOW is not a rule
action; TEMPORARY_HOLD only with WITHDRAWAL_HOLD or PAYOUT_DESTINATION_HOLD and 1 hour–7 days (Go and CHECK); codes and names
never contain "fraud" (Go and CHECK). The stored definition is the canonical re-encoding of the parsed tree, so what is
hashed and approved is exactly what runs. Evaluation is bounded (one indexed query, ≤ 10 000 signals per subject).

## 3. Lifecycle (`risk_rule_version`, guarded by `app.guard_transition`)

```
DRAFT ─submit─► PENDING_APPROVAL ─approve─► APPROVED ─activate─► ACTIVE ⇄ SUSPENDED
  │                     └─reject─► REJECTED              │            │
  └─retire─► RETIRED ◄──────────────────────────────────┴────────────┘
```

* **Editable only in DRAFT.** Submission freezes the definition and attributes and stores `definition_hash` (SHA-256 over
  definition, contribution, severity, action, hold type and duration, priority, engine version); the database refuses any
  later change (`risk_rule_versions_guard`).
* **Maker-checker.** Approval and rejection by `risk.rule.approve`, never the creator or submitter or an account linked to
  either (Go `linked()` and the database guard via `app.actor_identities`); a checker may bind the approval to the
  `definition_hash` they reviewed (`RISK_RULE_CHANGED` on mismatch). Reactivation is by someone other than the suspender.
* **Step-up** for every lifecycle mutation (route permission flags), recorded on approval.
* **One ACTIVE version per rule**: activating a version retires the rule's previous ACTIVE/SUSPENDED version in the same
  transaction (unique partial index as backstop). One DRAFT or PENDING version per rule.
* **No rule is seeded.** An installation evaluates nothing until two people approve a rule.

## 4. Testing a rule before approval

`POST /admin/risk/rule-versions/{id}/simulate {subject_id}` evaluates one version (any status) against a subject's recorded
signals and state in a read-only transaction: nothing is stored and no action is taken.

## 5. Events

Every lifecycle change writes `risk.risk_events`, `audit` and the outbox event `risk.rule_version_changed`.
