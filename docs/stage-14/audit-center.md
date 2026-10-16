# Stage 14 — Audit center

Page `/admin/audit`; reader `internal/audit/search.go`; routes in `internal/operations/http.go`.

| Route | Permission | Behaviour |
|---|---|---|
| `GET /api/v1/admin/audit/events` | `audit.read` (COMPLIANCE, FINANCE, SUPER_ADMIN) | filters: action prefix, actor id, target type/id, correlation id, request id, outcome, from/to (≤ 366 days); keyset cursor on `seq` (newest first), limit ≤ 200; IP and user agent only for holders of `security_audit.read` |
| `GET /api/v1/admin/audit/security-events` | `security_audit.read` (SECURITY_ADMIN, SUPER_ADMIN) | the security stream (same filters) |
| `POST /api/v1/admin/audit/exports` | `audit.export` (COMPLIANCE, SUPER_ADMIN; step-up) | CSV or JSON, ≤ 31 days, ≤ 5,000 rows; spreadsheet formula prefixes neutralised in CSV; audited as `audit.exported` (security stream, with the filters) **before** the file is returned |

Searching is itself audited (which stream and how many results). Audit records cannot be edited or deleted: the hash-chain
triggers refuse UPDATE/DELETE for every role (verified in `TestOperationsAuditCenter`), and the portal has no edit
route. Exports contain the stored records only; no identity document or KYC value is ever in an audit record
(ADR-019).
