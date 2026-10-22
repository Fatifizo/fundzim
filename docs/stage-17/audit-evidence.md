# Stage 17 — Financial audit trail and evidence

## 1. Evidence

| Artefact | Evidence |
|---|---|
| Statement file | private evidence object (C2, SSE-C, scanned) + `audit.evidence_records` (PSP_TRANSACTION_REPORT / PSP_SETTLEMENT_REPORT / BANK_STATEMENT) with the file's SHA-256, size, media type, collector; `recon.imports.content_sha256` is the same hash; parsing re-reads through storage's hash-verifying reader and re-checks the hash |
| Each line | `raw_line_sha256` (hash of the record's fields) + `raw_line_locator` (`row:N` / `json:$.lines[N]`) inside the stored file |
| Run | `decisions_sha256` over every decision (deterministic; a replay of the same input yields the same hash) |
| Match | rule set id/version, matcher version, compared keys (explanation) |
| Resolution | proposal (canonical JSON) and `proposal_sha256`; `approved_sha256` must equal it; evidence references (`import:`, `item:`, `batch:`, `evidence:`, `document:` …); execution result |
| Ledger adjustment | the ledger's own proposed/approved lines hashes and journal |

Evidence is never overwritten: imports are immutable after parsing, lines immutable except their status, superseded
statements stay with their lines, exceptions and history are never deleted (`forbid_mutation`).

## 2. Audit events

`reconciliation.import.received`, `.import.parsed`, `.import.failed`, `.run.queued`, `.run.completed`,
`.exception.opened`, `.exception.{assign,start,await_evidence,resume,comment,close,reopen}`, `.exception.escalated`,
`.resolution.{proposed,approve,reject,cancel,executed}`, `.rule_set.{proposed,approve,reject,withdraw,retire}`,
`.report.exported`. Owning modules add theirs (payments `payments.payment.status_reconciled`, payout events with source
RECON, ledger `ledger.adjustment.*`). Audit metadata carries ids, codes, counts and hashes — never file content,
account numbers or personal data (the never-embed list applies).

## 3. Retention

No retention period is set: statement evidence, lines, matches and exceptions are financial records whose retention is
LEGAL_REVIEW_REQUIRED (LR-012, LR-076). Nothing is purged (KI-S17-06).
