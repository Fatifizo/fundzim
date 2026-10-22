# Stage 17 — Bank statement reconciliation

## 1. Scope (Model A)

Under Model A (ADR-013) a licensed provider collects, holds and disburses donor funds. FundZim's own bank account
receives **only FundZim's platform-fee income**, remitted by the provider. A bank statement therefore reconciles against
the FEE_REMITTANCE lines of provider settlement reports — and anything that looks like a donation arriving in FundZim's
account is an alarm, not a match. No bank integration exists or is assumed; the only source is
`fundzim_operating_bank_test` (TEST, staff-supplied canonical files).

## 2. Statement fields

STATEMENT: `account_ref` (required), period, optional opening/closing balance (signed, single currency). LINE:
BANK_TRANSFER, CREDIT or DEBIT, BOOKED, amount, currency, `counterparty_ref` (the transfer reference as printed),
`value_date`, `booked_at`. The account number is never stored: last four characters plus an HMAC
(`reconciliation.bank_account`); the file itself is private C2 evidence.

## 3. Matching

| Bank line | Decision |
|---|---|
| CREDIT whose reference names a fee remittance (batch reference, provider reference or line id of a FEE_REMITTANCE line) with the same amount and currency | match (subject FEE_REMITTANCE_LINE, rule `REMITTANCE_REFERENCE`); the remittance line becomes MATCHED |
| reference contains a FundZim payment id | **POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT (CRITICAL)** — Model A forbids donor funds reaching FundZim |
| several remittances named | AMBIGUOUS_MATCH |
| remittance found, amount differs | AMOUNT_MISMATCH |
| remittance found, currency differs | CURRENCY_MISMATCH (CRITICAL) |
| remittance already matched by another bank line | DUPLICATE_TRANSACTION |
| no remittance named, CREDIT | UNMATCHED_BANK_ENTRY (HIGH) |
| any DEBIT | UNMATCHED_BANK_ENTRY (CRITICAL): FundZim's account pays nothing to donors or campaigns |

A bank line is **never allocated by amount alone**. A remittance line inside a parsed bank period older than the
settlement horizon without a matching bank credit → MISSING_BANK_CREDIT (MEDIUM, closes when found).

## 4. Balances

Opening and closing balances are stored as reported (evidence) but not reconciled against a ledger account in Stage 17:
`asset:fundzim_operating_bank` has no postings outside synthetic fixtures, and fee remittance postings
(`FEE_REMITTANCE`) wait for the accounting review (KI-S17-04).
