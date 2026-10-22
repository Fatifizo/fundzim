# Stage 17 — Statement imports and settlement management

Code: `internal/reconciliation/format.go` (parser), `imports.go` (pipeline). API: `POST /admin/reconciliation/imports`
(multipart, `reconciliation.import`, step-up), `POST /admin/reconciliation/imports/sandbox`.

## 1. Sources (seeded, TEST only)

| Code | Kind | Authority | Provider |
|---|---|---|---|
| `sandbox_transactions_test` | PROVIDER_TRANSACTION_REPORT | 2 | sandbox simulator |
| `sandbox_settlement_test` | PROVIDER_SETTLEMENT_REPORT | 3 | sandbox simulator |
| `fundzim_operating_bank_test` | BANK_STATEMENT | 4 | — (FundZim's own account, Model A) |

`ck_recon_sources_test_only_stage17` refuses any other environment. A real provider needs a selection ADR, its
documented format, a parser for it, a PRODUCTION source and stage acceptance.

## 2. Pipeline

1. **Receive** (API): media type `text/csv` or `application/json` declared on the file part; ≤ 10 MiB (the storage
   upload cap); SHA-256 of the bytes computed first; an identical file (any source, any filename) is refused
   409 `DUPLICATE_IMPORT` with the existing import id. A `supersedes_import_id` must name a PARSED import of the same source.
2. **Store as evidence**: storage upload, purpose `RECONCILIATION_REPORT` (private evidence bucket, C2, retention class
   FINANCIAL, SSE-C, quarantine). Storage accepts CSV/JSON for this purpose only and only when the content is valid UTF-8
   without control characters or markup (`storage.SniffText`).
3. **Record**: audit event `reconciliation.import.received` and an `audit.evidence_records` row (type
   `PSP_TRANSACTION_REPORT` / `PSP_SETTLEMENT_REPORT` / `BANK_STATEMENT`, same SHA-256, storage reference) and the import
   row, in one transaction. The original filename is kept for display only (path stripped, 200 characters).
4. **Scan**: ClamAV in the worker. REJECTED → import FAILED `SCAN_REJECTED`.
5. **Parse**: the worker reads the object through storage's hash-verifying reader, re-checks the hash against the import
   (`EVIDENCE_HASH_MISMATCH`), parses and validates (§3). **All or nothing**: any invalid record fails the file with a
   validation report (first 200 problems; locator, field, code — never file content) and nothing is recorded.
6. **Record lines** in one transaction (§4), then PARSED with the statement metadata (written once).

## 3. Canonical format `FUNDZIM_CANONICAL_V1`

FundZim's own provider-neutral contract — not any provider's format. Integer minor units as digit strings (no sign,
decimal point, exponent or separator; at most 10¹⁵), ISO 4217 currencies (USD, ZWG; the provider spelling `ZiG` is
normalised to ZWG and kept verbatim as `raw_currency_code`), RFC 3339 timestamps with an offset, `YYYY-MM-DD` dates.

### 3.1 Records

| Record | Fields | Rules |
|---|---|---|
| STATEMENT (exactly one, first) | `ref`, `currency` (optional), `period_start`, `period_end`, `opening_balance_minor` / `closing_balance_minor` (optional pair, signed, need `currency`), `declared_lines`, `account_ref` (bank only, required there) | period positive, ≤ 93 days, not ending more than a day in the future; `declared_lines` equals the LINE count |
| BATCH (settlement reports only) | `ref`, `currency`, `value_date` (settlement date), `gross_credits_minor`, `gross_debits_minor`, `fees_minor`, `net_minor` (signed), `declared_lines` | **credits − debits − fees = net**; the batch's lines must add up to its header (credits and debits by direction, fees from line fees; fee remittances excluded) |
| LINE | `ref` (line id), `kind`, `direction`, `status`, `amount_minor` (> 0), `fee_minor`, `net_minor` (= amount − fee when both), `currency`, `provider_ref`, `merchant_ref` (our payment/payout id), `batch_ref`, `counterparty_ref` (bank), `value_date`, `booked_at` | booked inside the period; batches precede their lines; a reference is required (provider or merchant) except FEE and ADJUSTMENT lines; bank lines need `counterparty_ref`; references `[A-Za-z0-9][A-Za-z0-9_.:/-]*` (a cell starting with `=`, `+`, `-` or `@` is refused) |

Allowed kinds by source: provider reports — PAYMENT (CREDIT; SUCCEEDED/FAILED/PENDING), REFUND, CHARGEBACK, REVERSAL
(DEBIT), PAYOUT (DEBIT; COMPLETED/FAILED/PENDING/RETURNED), PAYOUT_REVERSAL (CREDIT; COMPLETED), FEE (DEBIT; BOOKED),
FEE_REMITTANCE (DEBIT; BOOKED), ADJUSTMENT (either; BOOKED); bank statements — BANK_TRANSFER (either; BOOKED) only.

### 3.2 CSV

Header row with exactly these columns, in this order: `record_type,ref,kind,direction,status,amount_minor,fee_minor,
net_minor,currency,provider_ref,merchant_ref,batch_ref,counterparty_ref,value_date,booked_at,period_start,period_end,
opening_balance_minor,closing_balance_minor,declared_lines,gross_credits_minor,gross_debits_minor,fees_minor,account_ref`.
Every row has every column; fields not allowed for the record type must be empty; cells ≤ 256 characters; a UTF-8
BOM is ignored. Parsed with Go's `encoding/csv` (strict quoting) — no spreadsheet engine, no formula evaluation.

### 3.3 JSON

```json
{"format": "FUNDZIM_CANONICAL_V1",
 "statement": {"ref": "ST-1", "currency": "USD", "period_start": "2026-10-01T00:00:00Z", "period_end": "2026-10-21T00:00:00Z", "declared_lines": "1"},
 "batches": [],
 "lines": [{"ref": "L1", "kind": "PAYMENT", "direction": "CREDIT", "status": "SUCCEEDED", "amount_minor": "1250", "currency": "USD",
            "merchant_ref": "0192…", "provider_ref": "sbx_…", "booked_at": "2026-10-10T08:00:00Z"}]}
```

Same field names; every value is a JSON string (a JSON number is refused: never a float); unknown fields are refused.

### 3.4 Duplicates and conflicts inside a file

The same line id twice with the same content: `DUPLICATE_ROW`; with different content: `CONFLICTING_LINE_IN_FILE`; both
fail the file.

## 4. Recording lines

- Identity is `(source, external line id, revision)`. A line already recorded with the same content (any revision) is
  counted `lines_duplicate` and not stored again (overlapping statements).
- Different content for a recorded line, without a re-issue: stored as the next revision with status CONFLICT and a
  CONFLICTING_EVIDENCE exception (HIGH); never matched.
- A corrected re-issue (`supersedes_import_id`): the new lines are the next revision; in the same transaction the old
  import becomes SUPERSEDED, its lines and batches SUPERSEDED and their active matches REVOKED (never deleted) — the
  next run decides the new lines. All versions stay as evidence.
- A settlement batch already recorded with identical totals from an overlapping report is reused; different totals
  without a re-issue fail the file (`CONFLICTING_BATCH`).
- Bank statements: `account_ref` is never stored — only its last four characters and an HMAC under the application
  blind-index key (`reconciliation.bank_account`); the file itself stays in the private evidence bucket.

## 5. Settlement batches

Each settlement batch is evaluated after every run that touches its source and window:
`expected net = Σ credits − Σ debits − fees`, where a matched line uses FundZim's assessed provider fee when FundZim
knows it and the reported fee otherwise. The batch is MATCHED when every line (fee remittances excluded) is MATCHED or
explained (IGNORED) and the expectation equals the provider's header net; otherwise DISCREPANCY, with SETTLEMENT_SHORT
or SETTLEMENT_OVER (HIGH; signed values in the detail) when the nets differ. A database trigger refuses MATCHED unless the
lines add up to the header and are settled. Nothing is posted (ADR-049 §8).

## 6. Sandbox statements

`POST /imports/sandbox` (source `sandbox_*`, currency, period ≤ 93 days ending now at the latest) builds a canonical JSON
statement from the simulator's own payments and payouts (`psp/sandbox.StatementLines`): line id = provider reference +
status, merchant reference = our id. A settlement report lists succeeded collections in one batch. The simulator has no
fee model: no fee is reported (`NOT_REPORTED`, never 0). Imported like any file (origin `SANDBOX_GENERATOR`); refused
where the sandbox is disabled and in production.

## 7. Late evidence

Delayed reports are imported whenever they arrive (the period is the evidence's own); a run's window is the union of the
source's parsed periods for the currency (last 93 days of periods), so late lines are decided by the next run. Revised
statements supersede explicitly; contradictory lines become CONFLICTING_EVIDENCE; nothing is overwritten. No bank or
provider cut-off is invented: the only timing parameters are the rule set's (matching-engine.md §4).
