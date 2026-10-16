# Stage 14 — Support operations

Pages `/admin/support`, `/admin/support/{id}`; API under `/api/v1/admin/support/cases`. View: `ops.support.view`
(SUPPORT, REVIEWER, KYC_REVIEWER, COMPLIANCE, FINANCE). Work: `ops.support.work` (SUPPORT; step-up).

| Field | Rule |
|---|---|
| Subject | 3–200 characters; staff are told not to enter identity numbers, bank details or passwords |
| Category | GENERAL, ACCOUNT, CAMPAIGN, DONATION, VERIFICATION, WITHDRAWAL, TECHNICAL |
| Priority | P1–P4 (default P3) |
| References | requester account id, campaign id, and one typed reference (DONATION_INTENT, PAYMENT, PAYOUT_REQUEST, KYC_CASE, KYB_CASE, BENEFICIARY, CAMPAIGN_REPORT, OTHER) — ids only |
| Due date | only when an ACTIVE internal `SUPPORT_RESOLUTION` target exists (see [reporting.md](reporting.md)) |

- Assignment: the assignee must hold `ops.support.work` (422 otherwise); assigning an OPEN/REOPENED case moves it to
  TRIAGE.
- Escalation records the team (CAMPAIGN_MODERATION, VERIFICATION, RISK_COMPLIANCE, PAYMENTS, PAYOUTS,
  FINANCIAL_OPERATIONS, TECHNICAL_OPERATIONS) and an optional reference; **the case stays with its owner** and the
  support agent gains no access to that team's records.
- RESOLVED and CLOSED need a resolution note; REOPENED clears the resolution (history keeps it).
- Updates carry the record version; a stale version is refused (`OPS_RECORD_CHANGED`).
- History (`app.ops_support_events`) is append-only (UPDATE/DELETE refused by trigger).
- Support staff get no KYC documents, bank account details, ledger internals, risk evidence or compliance notes: their
  roles do not include those permissions, and a support case stores references, not copies.
