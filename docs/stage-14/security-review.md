# Stage 14 — Security review

Scope: `internal/operations`, the Stage 14 additions to `auth`, `audit`, the per-module snapshots, migrations
`20261017090000` and `20261017090100`, the admin shell and operations pages. Self-review by the implementer; no
external review was performed.

| Threat / requirement | Control | Evidence |
|---|---|---|
| Frontend visibility mistaken for authorization | every operations route has a permission policy (non-staff → 404); pages fetch with `serverGetOr404`; the UI hides only | `TestOperations*` call the API directly (SUPPORT → 403/404 on risk, verification, ledger, audit, staff); E2E: SUPPORT gets the 404 page for `/admin/audit`; a FINANCE request to approve a risk rule returns 403 |
| A competing approval authority | approvals are a read model; decisions go through the owning module's route | no decision route in `internal/operations`; `TestOperationsApprovalCenterRoutesToDomain` |
| Portal writing domain tables / ledger | archtest: operations SQL may reference `app.ops_*` only; operations imports `platform` and `audit` only | `internal/archtest` |
| Self-escalation of privilege | role changes stay dual-controlled requests; access-review revocations become requests; reviewer ≠ starter; nobody decides their own access (database guards with linked identities) | `TestOperationsStaffGovernance` |
| Unrestricted superuser | none exists; `SUPER_ADMIN` has no KYC, ledger, payout or risk decision permission | role inventory |
| Session hijack response | `session.revoke` ends every session of another staff member immediately; never one's own; reason; audited | `TestOperationsStaffGovernance` (revoked session gets 401) |
| Support over-reach | support roles hold no KYC document, bank detail, ledger, risk-evidence or compliance-note permission; cases store ids; escalation grants no access | role matrix; `TestOperationsSupportCases` |
| Exception triage leaking a module's data | the module's view permission is stored on the triage record (immutable) and required for list, history and triage; assignees must hold it | `TestOperationsExceptionTriage` |
| Exception "resolved" in the portal while still open | `OPS_EXCEPTION_DOMAIN_OPEN` | same test |
| Audit tampering | append-only hash chain (UPDATE/DELETE refused); no edit route; operations history tables append-only | `TestOperationsAuditCenter`, `TestOperationsSupportCases` |
| Bulk data exfiltration through export | `audit.export` + step-up; ≤ 31 days, ≤ 5,000 rows; audited before the file is returned; CSV formula injection neutralised | `TestOperationsAuditCenter` |
| Search as an enumeration oracle | exact ids/numbers only (no email, phone, name or document search); 3–100 characters; per-module permission; audited by kind and count, not text | `TestOperationsReportsSearchAndSLA` |
| Lost updates / races | version on every operations record (`OPS_RECORD_CHANGED`); unique open incident per alert key; one PROPOSED/ACTIVE SLA target per metric | concurrency cases in `TestOperationsSupportCases`, `TestOperationsApprovalCenterRoutesToDomain` |
| A slow or failing module taking down the portal | per-source timeout and panic recovery; UNAVAILABLE instead of 0 | `TestCollectBoundsSlowAndPanickingSources` |
| Sensitive values in audit metadata | `audit` rejects sensitive keys (the session-revocation event uses `revoked_count`) | found and fixed during integration testing |
| A broken risk rule blocking signals (KI-S13-11) | fault isolation; degraded evaluation fails payouts closed | `TestRiskRuleFaultIsolation` |

Residual risks are listed in [known-issues.md](known-issues.md).
