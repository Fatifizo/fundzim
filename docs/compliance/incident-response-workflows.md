# FundZim Incident and Escalation Workflows (Stage 1 design)

Status: **Stage 1 design.** These playbooks extend the outline in [SECURITY.md](../SECURITY.md) §22. Kill
switches arrive in Stages 11 and 14, monitoring in Stages 13 and 17, and full runbooks with drills in
Stage 18 ([ROADMAP.md](../ROADMAP.md)). **No reporting obligation below is confirmed**: every
external-notification step names the open legal question it depends on.

Related: [SECURITY.md](../SECURITY.md), [OBSERVABILITY.md](../OBSERVABILITY.md),
[THREAT-MODEL.md](../THREAT-MODEL.md), [operational-controls.md](operational-controls.md),
[audit-evidence-model.md](audit-evidence-model.md), [compliance-case-management.md](compliance-case-management.md),
[transaction-monitoring.md](transaction-monitoring.md), [donor-protection-policy.md](donor-protection-policy.md),
[LEDGER.md](../LEDGER.md), [PAYMENTS.md](../PAYMENTS.md).

---

## 1. Severity mapping

Uses the SEV scale from [SECURITY.md](../SECURITY.md) §22.

| Playbook | Default severity | Escalate to SEV1 when |
|---|---|---|
| §3 Suspected fraudulent campaign | SEV3 | Payout already in flight or completed; organised pattern across campaigns |
| §4 Stolen account | SEV2 | Payout destination changed or payout requested; staff account involved → SEV1 |
| §5 Compromised payment credentials | **SEV1** | Always |
| §6 Unauthorised payout | **SEV1** | Always |
| §7 Missing settlement | SEV2 | Amount above a configured threshold, or the provider is unresponsive past a deadline |
| §8 Duplicate financial transaction | SEV2 | Duplicate payout, or duplicate ledger posting |
| §9 Provider outage | SEV2 | Payouts in `UNKNOWN` at scale, or the outage lasts beyond a configured window |
| §10 Webhook failure | SEV2 | Signature failures suggest forgery attempts; DLQ contains `SUCCEEDED` events |
| §11 Ledger imbalance | **SEV1** | Always (CLAUDE.md: stop and report) |
| §12 Data breach | **SEV1** if C3/C4 data, otherwise SEV2 | Any KYC data or credentials |
| §13 Regulatory inquiry | SEV2 (handled as a priority case, not an outage) | Request implies an investigation of FundZim itself |

## 2. Common workflow (every playbook)

1. **Open an incident** record: id, severity, commander (on-call lead), scribe, start time (UTC). All
   actions taken are logged against the incident id; kill-switch use is audited
   ([audit-evidence-model.md](audit-evidence-model.md) §4.7).
2. **Contain before investigating** whenever money can still move.
3. **Preserve evidence** before remediation: export relevant audit events, ledger transactions,
   `webhook_inbox` rows, provider reports and logs as hash-verified evidence records; place a **legal hold**.
4. **Never repair financial history in place.** Corrections are new balanced ledger transactions under
   maker-checker ([LEDGER.md](../LEDGER.md) §6.9).
5. **Escalation tree:** on-call engineer → incident commander → (SEV1) business owner + COMPLIANCE lead +
   FINANCE lead → external parties (provider, counsel, authorities) **only via the business owner or
   COMPLIANCE lead**, never by an individual engineer.
6. **Close** with a post-incident review: timeline, root cause, financial impact (per currency), control gaps,
   actions, threat model update.

Containment tools referenced below (built Stages 11/14): global payout freeze, per-provider payout freeze,
per-provider collection disable, per-campaign freeze, new-campaign submission pause, staff account suspension,
session revocation, credential rotation.

---

## 3. Suspected fraudulent campaign

| Phase | Actions |
|---|---|
| **Detection** | Donor/public reports; reviewer suspicion; monitoring rules (image reuse, donor concentration, rapid withdrawal after spike, chargeback cluster — [transaction-monitoring.md](transaction-monitoring.md)); provider notice |
| **Containment** | SUSPEND (credible) or FREEZE (strong signals or payout pending): payouts not yet `SUBMITTED` pulled back; available balance → `campaign_held`. Suspend linked campaigns by the same owner or destination if indicated. |
| **Investigation** | Compliance case: re-verify documents with the issuing institution, contact the beneficiary independently, review owner KYC, device and account links, destination ownership, donor patterns |
| **Evidence preservation** | Campaign versions, images, review decisions, KYC decision records, payout history, communication logs; legal hold |
| **Escalation** | COMPLIANCE lead; business owner if paid out or media exposure; possible STR (LR-008) — **no tipping-off** of the owner |
| **Recovery** | Cleared → unfreeze (maker-checker). Fraud → cancel, refund unpaid funds ([donor-protection-policy.md](donor-protection-policy.md) §5), recovery case for paid funds (LR-085 (→ LR-080)), account suspension |
| **Reporting requiring legal confirmation** | STR to the FIU and tipping-off limits (LR-008); police report; donor notification wording (LR-088); provider notification under contract (`PCR-020 — fraud reporting duties to provider`) |

## 4. Stolen account (account takeover, SIM swap — T-05, F-04)

| Phase | Actions |
|---|---|
| **Detection** | User report; new device plus destination change; SIM-swap signal where available (`PCR`/operator API — not assumed); login anomalies |
| **Containment** | Revoke all sessions; lock the account; place a payout hold on all the user's campaigns; cancel payouts not yet `SUBMITTED`; for payouts `SUBMITTED`/`PROCESSING`, request provider cancellation where supported (`PCR-014 — payout cancel`) |
| **Investigation** | Timeline of logins, OTPs, contact and destination changes, payouts; identify any money moved |
| **Evidence preservation** | Auth audit events, session records, OTP delivery logs (no OTP values), destination change records |
| **Escalation** | SEV1 if payout moved or a staff account was involved; FINANCE lead for in-flight payouts |
| **Recovery** | Identity re-verification (IDENTITY level re-check with liveness), restore contacts, reset MFA, remove attacker destinations, re-verify the original destination, lift holds after cooling-off; recovery attempt for misdirected funds via the provider |
| **Reporting requiring legal confirmation** | Whether an account compromise is a personal-data breach requiring notification (LR-010); police report |

## 5. Compromised payment credentials (PSP API keys, webhook secrets, portal accounts)

| Phase | Actions |
|---|---|
| **Detection** | Secret-scan alert; provider notice; unexpected API activity (calls not originating from FundZim egress); unmatched transactions in reconciliation; leaked credential report |
| **Containment** | **Immediately**: per-provider payout freeze; rotate the credential (secret manager) and invalidate the old one with the provider; rotate webhook secret (accept the new one only); disable compromised portal users |
| **Investigation** | Provider-side activity log for the exposure window (`PCR-021 — API audit log availability`); compare every provider transaction in the window with FundZim records |
| **Evidence preservation** | Secret-manager access logs, CI logs, provider logs; legal hold |
| **Escalation** | SEV1 → business owner, provider security contact |
| **Recovery** | Resume after full reconciliation of the exposure window; root-cause the leak (repo, CI, laptop) |
| **Reporting requiring legal confirmation** | Contractual notice to the provider (`PCR`); regulator notification of an operational security incident — **LR-087** (new) |

## 6. Unauthorised payout

A payout that FundZim did not approve under its controls (portal-initiated, insider bypass, bug, forged
instruction), or a payout to a destination that was not the approved snapshot.

| Phase | Actions |
|---|---|
| **Detection** | Reconciliation finds a provider debit with no matching `payout_id`; destination hash mismatch; owner reports they did not receive funds; maker-checker bypass alert |
| **Containment** | Global (or per-provider) payout freeze; suspend the involved staff accounts pending review; request provider recall/reversal where supported (`PCR-014 — payout recall`) |
| **Investigation** | Trace through audit events, approvals, provider portal logs, and the deploy history (was it a bug?) |
| **Evidence preservation** | All of the above; snapshot of approval records; legal hold |
| **Escalation** | SEV1 → business owner, FINANCE lead, COMPLIANCE lead, provider; insider suspicion → HR/counsel |
| **Recovery** | Ledger: post the actual outflow (unmatched provider debit → `suspense:settlement_discrepancy`, then to a recoverable or loss account under maker-checker); restore the campaign's entitlement honestly; recovery case; fix the control gap before unfreezing |
| **Reporting requiring legal confirmation** | LR-087 (regulator incident reporting), LR-008 (if criminal), provider contract |

## 7. Missing settlement

The provider reported or confirmed successful collections, but the corresponding settlement did not arrive
or is short, past the expected settlement window (`PCR-007 — settlement timelines`).

| Phase | Actions |
|---|---|
| **Detection** | Reconciliation: `psp_clearing` items older than the expected window; settlement report total ≠ expected; `suspense:settlement_discrepancy` balance non-zero |
| **Containment** | Funds not settled are **not** released to `campaign_payable` (by design, [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md)); if the shortfall affects already-released funds, pause payouts for that provider |
| **Investigation** | Item-level match against provider statements; query the provider with item references |
| **Evidence preservation** | Settlement reports (hash-verified), reconciliation run outputs |
| **Escalation** | FINANCE lead; business owner above threshold or after deadline; provider account manager |
| **Recovery** | Provider pays or corrects; ledger records settlement as it actually happens; unresolved items stay in suspense with ageing alerts — never written off silently |
| **Reporting requiring legal confirmation** | Complaint/escalation routes against a licensed provider, and any regulator involvement — LR-087, LR-004 |

## 8. Duplicate financial transaction

Duplicate charge, duplicate refund, duplicate payout, or duplicate ledger posting.

| Phase | Actions |
|---|---|
| **Detection** | Uniqueness-constraint violations (expected and harmless — they mean the guard worked); reconciliation duplicates; donor reports of double charge; payout received twice |
| **Containment** | If a code path produced it: disable that path (feature flag or per-provider freeze); stop retries |
| **Investigation** | Why idempotency failed: missing key, key not reused, provider ignored the reference, UNKNOWN resolved incorrectly |
| **Evidence preservation** | Request logs, idempotency records, provider references |
| **Escalation** | SEV1 for duplicate payouts or postings; SEV2 for duplicate donor charges |
| **Recovery** | Duplicate charge → refund ([donor-protection-policy.md](donor-protection-policy.md) §3). Duplicate payout → recovery case + provider recall. Duplicate ledger posting → reversing transaction referencing the duplicate (maker-checker). Add a regression test before the path is re-enabled. |
| **Reporting requiring legal confirmation** | Consumer redress duties (LR-018) |

## 9. Provider outage

| Phase | Actions |
|---|---|
| **Detection** | Error-rate and latency alerts per provider; webhook silence; status page or provider notice |
| **Containment** | Disable the provider's methods on the donation page (show other available methods or "temporarily unavailable"); **do not** mark in-flight payments FAILED; payouts to that provider held at `APPROVED` (not submitted); in-flight payouts stay `SUBMITTED/PROCESSING/UNKNOWN` |
| **Investigation** | Scope (collections, payouts, webhooks, status API); affected payment and payout ids |
| **Evidence preservation** | Error logs, timestamps of every unresolved operation |
| **Escalation** | SEV2; SEV1 if many payouts are `UNKNOWN` or the outage is prolonged |
| **Recovery** | After recovery: status-poll every `PENDING`/`UNKNOWN` payment and payout; reconcile the outage window item by item before re-enabling payouts; donor messaging per [donor-protection-policy.md](donor-protection-policy.md) §9 |
| **Reporting requiring legal confirmation** | Usually none for FundZim; provider SLA claims (`PCR-008 — SLA and incident communication`) |

## 10. Webhook failure

Signature failures, endpoint downtime, processing errors, dead-lettered events.

| Phase | Actions |
|---|---|
| **Detection** | `webhook.signature.failed` spike; DLQ growth (SEV2, pages — [OBSERVABILITY.md](../OBSERVABILITY.md)); webhook silence while payments complete at the provider |
| **Containment** | Signature failures: keep rejecting (never relax verification); check for secret rotation mismatch vs attack; rate-limit sources. Processing failures: payments remain non-final; polling takes over |
| **Investigation** | Inspect redacted inbox rows; check provider delivery logs; check for out-of-order or replayed events |
| **Evidence preservation** | Inbox rows (already append-only), DLQ contents |
| **Escalation** | SEV1 if a forged event was processed or if DLQ holds `SUCCEEDED` events older than the alert window |
| **Recovery** | Fix and replay DLQ events through the normal pipeline (idempotent); reconcile; never hand-edit payment states |
| **Reporting requiring legal confirmation** | None by default; if forged webhooks caused financial loss → §6 / LR-087 |

## 11. Ledger imbalance

Any invariant failure: unbalanced transaction, negative available balance, projection ≠ recompute, audit
hash-chain break.

| Phase | Actions |
|---|---|
| **Detection** | Deferred-constraint failure (transaction rejected — the guard worked, still investigated); nightly recompute mismatch; invariant monitor; hash-chain verification |
| **Containment** | **Stop the affected flow** (global payout freeze if scope is unclear). Engineers must not patch data or loosen checks (CLAUDE.md) |
| **Investigation** | Identify the first divergent transaction; replay postings in a copy; check the deploy history and migrations |
| **Evidence preservation** | DB snapshot (restricted environment), ledger export, audit export; legal hold |
| **Escalation** | SEV1 → FINANCE lead, business owner |
| **Recovery** | Correcting transactions under maker-checker with incident reference; full recompute must match before unfreezing; regression test added |
| **Reporting requiring legal confirmation** | If customer funds may be misstated to a provider or regulator — LR-087, LR-002 |

## 12. Data breach

| Phase | Actions |
|---|---|
| **Detection** | Mass `kyc.document.viewed`; unusual exports; bucket access anomalies; leaked data found externally; vendor breach notice |
| **Containment** | Revoke credentials and sessions; block the access path; rotate keys if key material may be exposed; isolate affected systems |
| **Investigation** | Determine data classes (C2/C3/C4), number of subjects, time window, and whether data was exfiltrated |
| **Evidence preservation** | Access logs, bucket logs, KMS logs, audit events; forensic images where needed; legal hold |
| **Escalation** | SEV1 → business owner, counsel, DPO (LR-010) |
| **Recovery** | Close the vulnerability; re-encrypt or crypto-shred affected objects if keys were exposed; notify affected users per legal advice; credit/ID-fraud guidance for KYC exposure |
| **Reporting requiring legal confirmation** | Notification to the data protection authority and data subjects, timelines and content (LR-010, LR-034); provider and vendor contractual notices (LR-033); cross-border implications (LR-011) |

## 13. Regulatory inquiry

A regulator, the FIU, law enforcement or a court asks for information, inspection or action.

| Phase | Actions |
|---|---|
| **Detection** | Formal letter, email, visit or call to any staff member |
| **Containment** | Route to the COMPLIANCE lead and business owner immediately; no staff member answers substantively alone; verify the requester's identity and authority |
| **Investigation** | Scope of the request; legal basis (LR-032); whether it relates to an STR (tipping-off — LR-008) |
| **Evidence preservation** | Legal hold on the scoped records **before** any routine retention job can run |
| **Escalation** | Counsel engaged for every request until a standing procedure is approved |
| **Recovery / response** | Export through the audited export path (`evidence.exported`, hashes, recipient, legal basis); log the disclosure; follow up on required actions (e.g. freeze on instruction) |
| **Reporting requiring legal confirmation** | Disclosure obligations and limits (LR-032), confidentiality of the request (LR-008), data protection (LR-010) |

---

## 14. New legal question (introduced here)

| ID | Area | Question | Why it matters | Impact | Owner | Status |
|---|---|---|---|---|---|---|
| LR-087 | Incident reporting (non-data) | Must FundZim (as a platform, not itself a licensed PSP) notify the RBZ, another regulator, or its payment providers of operational or security incidents such as unauthorised payouts, compromised payment credentials, missing settlements or ledger misstatements? Within what time and in what form? | Notification steps in §5–§7 and §11 are currently placeholders | Incident runbooks, provider contracts, readiness checklist | Counsel | OPEN |
