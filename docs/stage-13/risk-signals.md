# Stage 13 — Risk signals

A signal is an append-only fact recorded from a **trusted domain event** (outbox delivery from another module) or by the
risk module itself (screening). One store: `risk.risk_signals` (Stage 5 table, extended). A signal is never deleted or
edited; a false positive is labelled by a separate review ([case-management.md](case-management.md)).

## 1. Normalised attributes

| Attribute | Meaning |
|---|---|
| `signal_type` | catalogue code (an unknown type is refused) |
| `subject_type`, `subject_id` | what the fact is about: USER, ORGANISATION, CAMPAIGN, BENEFICIARY, PAYOUT_DESTINATION, DONATION_INTENT, PAYOUT, AUTHENTICATION_SESSION (plus Stage 5 KYC_CASE, KYB_CASE, PAYMENT, DEVICE, DONOR_GUEST) |
| `category` | IDENTITY_RISK, CAMPAIGN_RISK, PAYMENT_RISK, WITHDRAWAL_RISK, DESTINATION_RISK, SESSION_RISK, DEVICE_RISK, VELOCITY_RISK, COMPLIANCE_RISK, PROVIDER_RISK, FEE_MANIPULATION_RISK, CHARGEBACK_RISK |
| `severity` | INFO, LOW, MEDIUM, HIGH (catalogue default) |
| `confidence` | 0–100: how reliable the fact is (e.g. a public report is an allegation: 60; a potential screening match: 50) |
| `kind` | OBSERVED (from an event) or DERIVED (inferred by a named rule version — never presented as a verified fact; CHECK) |
| `environment` | TEST (CHECK) |
| `source_module`, `source_event_id`, `source_event_type` | provenance; dedupe key with the type |
| `observed_at`, `recorded_at` | event time and recording time |
| `correlation_id` | the originating request's correlation id (cross-event correlation) |
| `evidence_ref` | optional reference to evidence |
| `facts` | ids, codes and amounts only — never names, documents, account numbers, IP addresses or device fingerprints |

## 2. Catalogue

| Type | Source event | Subject | Category | Severity | Confidence |
|---|---|---|---|---|---|
| DOCUMENT_REJECTED, KYC_REJECTED, KYB_REJECTED, DUPLICATE_IDENTITY, BENEFICIARY_REJECTED, BENEFICIARY_ESCALATED | Stage 5 (storage, kyc, beneficiaries) | owner | IDENTITY_RISK | LOW–HIGH | 90–100 |
| DESTINATION_CHANGED, DESTINATION_REJECTED | `payouts.destination_changed/_rejected` | owner | DESTINATION_RISK | LOW, MEDIUM | 100 |
| CAMPAIGN_CREATED | `campaigns.created` (+ `title_hash`) | owner | CAMPAIGN_RISK | INFO | 100 |
| CAMPAIGN_REPORTED | `campaigns.report_escalated` | campaign | CAMPAIGN_RISK | MEDIUM | 60 |
| CAMPAIGN_REVIEW_ESCALATED | `campaigns.review_escalated` | campaign | CAMPAIGN_RISK | MEDIUM | 80 |
| DONATION_INTENT_CREATED | `payments.donation_intent_created` | donor | VELOCITY_RISK | INFO | 100 |
| PAYMENT_FAILED | `payments.payment_status_changed` → FAILED | donor | PAYMENT_RISK | LOW | 100 |
| PROVIDER_EVIDENCE_CONTRADICTION | `payments.evidence_contradiction` | payment | PROVIDER_RISK | MEDIUM | 80 |
| WITHDRAWAL_REQUESTED | `payouts.requested` | requester | WITHDRAWAL_RISK | INFO | 100 |
| PAYOUT_FAILED, PAYOUT_RETURNED | `payouts.status_changed` → FAILED / REVERSED | requester | WITHDRAWAL_RISK | LOW, MEDIUM | 100 |
| MFA_CHANGED, PASSWORD_CHANGED, PASSWORD_RESET_REQUESTED, ACCOUNT_SUSPENDED, SIGNED_IN | `identity.*` | user | SESSION_RISK | INFO–MEDIUM | 70–100 |
| RESTRICTION_APPLIED | `compliance.restriction_applied` | restricted subject | COMPLIANCE_RISK | HIGH | 100 |
| FEE_POLICY_CHANGED, FEE_ADJUSTMENT_REQUESTED | `fees.policy_version_changed`, `fees.adjustment_requested` | staff actor | FEE_MANIPULATION_RISK | LOW | 100 |
| SCREENING_POTENTIAL_MATCH, SCREENING_CONFIRMED_MATCH | risk screening (TEST) | screened subject | COMPLIANCE_RISK | HIGH | 50, 100 |

No provider risk signal is invented (no provider is selected); `PROVIDER_EVIDENCE_CONTRADICTION` comes from FundZim's own
Stage 9 evidence recording.

## 3. Validation and deduplication

`SignalFromEvent` refuses malformed payloads (`ErrInvalidPayload`: missing or non-UUID ids, unknown subject types) — the
consumer logs and acknowledges them, it never guesses. Codes are shortened and normalised; amounts must be digit strings;
`ua_hash` must be a lowercase hex keyed hash (a raw user agent is dropped). The unique key (`source_event_id`,
`signal_type`) makes redelivery a no-op; the per-subject advisory lock serialises evaluations of one subject.

## 4. Correlation

Signals of one subject are correlated by the subject's stable identifier — never by IP address or shared network data —
through rule conditions (counts, distinct values, ordered sequences within windows) and by the case deduplication key
(one open case per subject and case type). `correlation_id` links a signal to the originating request for investigators.
