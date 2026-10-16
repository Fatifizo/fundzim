# Stage 13 — Risk scoring

Engine `risk-engine-v2` (`internal/risk/engine.go`, `EvaluateRules`): a **pure, deterministic** function of the ACTIVE
rule versions for the subject type, the subject's signals within the longest rule window (at most 10 000, newest first)
and the subject's risk state (enforced holds, open cases, latest screening status, previous classification).

## 1. Score

`score = min(100, Σ contribution of every fired rule)`. Contributions are integers 0–100 set per rule version under
maker-checker. No weights are hidden, learned or adjusted at run time; there is no black-box model.

## 2. Classification

| Classification | Score (engineering default) |
|---|---|
| LOW | 0–24 |
| MEDIUM | 25–49 |
| HIGH | 50–74 |
| CRITICAL | 75–100 |

Bands come from approved `risk.limits` rows `risk.v2.band.medium_min` / `high_min` / `critical_min` when all three exist
and are consistent (`band_source = LIMITS`); otherwise the engineering defaults (`band_source = ENGINEERING_DEFAULT`). No
band is presented as a regulatory or validated threshold.

## 3. Recommended action

The most severe action among fired rules (ALLOW < ALLOW_WITH_MONITORING < REVIEW_REQUIRED < TEMPORARY_HOLD < ESCALATE <
BLOCK), raised to at least REVIEW_REQUIRED for HIGH and ESCALATE for CRITICAL — a high classification always reaches a
human. What may happen automatically is limited by [risk-decisions.md](risk-decisions.md).

## 4. Explanation and confidence

Each evaluation stores: per-rule results (fired, contribution, the conditions as human-readable notes, the matched signal
ids), the explanation (fired rules with contribution, severity and summary, e.g. "signal_count(CAMPAIGN_REPORTED) in 1h =
2 ≥ 2"), the rule version ids, the band source, `confidence` = the lowest confidence of the matched signals, and
`inputs_hash` = SHA-256 of the canonical inputs (evaluation time, sorted signals, state, sorted rules, bands). Every
evaluation is shown with the notice: *"A risk score is a prioritisation aid from configured rules over recorded signals.
It is not proof of fraud or of any wrongdoing."* The database refuses an explanation containing "fraud".

## 5. Determinism and precedence

Inputs are sorted (signals by time then id; rules by code then version id) before evaluation, so order of arrival and
storage never changes the result; contributions are summed (commutative) and the action is a maximum (order-free). The
property test `TestPropertyDeterministicBoundedAndOrderIndependent` checks 12 000 random cases (seeds 1, 2, 3, 20261015):
same inputs → same score, classification, action and hash; score within 0–100; shuffling inputs changes nothing.

## 6. Relationship to model v1

Stage 5's model v1 (0–1000, ratings LOW/STANDARD/ENHANCED/RESTRICTED) still scores onboarding signals and routes
ESCALATE decisions to compliance, unchanged. The two scores are separate columns and tables; neither feeds the other
(folding v1 into reviewed v2 rules: KI-S13-08).
