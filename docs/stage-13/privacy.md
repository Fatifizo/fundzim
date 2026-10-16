# Stage 13 — Privacy and retention

Inputs: [DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md), [PRIVACY.md](../PRIVACY.md),
[identity-data-protection.md](../security/identity-data-protection.md); open items LR-012 (retention), LR-034 (data-subject
rights), LR-071 (automated decisions), LR-074 (minimisation), LR-011 (cross-border processing).

## 1. What risk stores

| Data | Classification | Notes |
|---|---|---|
| Signal facts | C2 | ids, codes and amounts only; the catalogue and `SignalFromEvent` drop anything else |
| Session data | C2 | `identity.signed_in` carries a **keyed hash of the user agent salted with the user id** (HMAC with the platform blind-index key, truncated) — it can only tell "a browser not seen before for this account"; it cannot link two accounts. No IP address, no device fingerprint, no geolocation is stored by risk |
| Campaign titles | public | `title_hash` of the public title only |
| Evaluations, cases, holds, monitoring | C2/C3 operational | staff-only; owners see coarse categories |
| Staff notes and evidence | C3 | references to other modules' records; identity documents stay in `kyc` (C3 RESTRICTED) and are never copied; the UI asks not to paste documents, full account numbers or secrets |
| Screening | TEST synthetic | synthetic references; no name, date of birth, nationality or document is sent or stored |

Not collected (no approved privacy basis): IP-based identity inference, device fingerprinting, invasive identifiers,
behavioural biometrics. **Shared IP addresses are never used to infer identity.** Full bank or wallet identifiers never appear
in risk data or dashboards (the payouts module keeps them encrypted and masked).

## 2. Access

Risk data is reachable only through staff routes with `risk.operations.view` (COMPLIANCE, FINANCE) and the mutation
permissions; non-staff get `404`; scores are never exposed publicly or to owners. Case and evidence views are logged
(`risk_case_access_log`). Staff cannot work cases about themselves or linked accounts.

## 3. Retention

**No retention period is invented.** Risk records (signals, evaluations, cases, evidence, holds, screening rows, events) are
kept and never purged until LR-012 is answered; evidence carries `retention_class = RISK_RECORD_LEGAL_REVIEW_REQUIRED`
(CHECK). False positives are labelled, never deleted. Erasure and access requests (LR-034) against risk records are an open
question: risk records may be needed for AML record-keeping (LR-008/LR-060), which conflicts with erasure — compliance/legal
decision.

## 4. Automated decisions

The engine's only automated effect on a person is a temporary payout-protective hold with a case for a human review
([risk-decisions.md](risk-decisions.md)). Whether this is a solely automated decision with significant effects, and what
notice or explanation the data subject must receive, is LR-071 (open). Owners are told only that an action is "on hold
pending a review" (no tipping-off detail, LR-008).
