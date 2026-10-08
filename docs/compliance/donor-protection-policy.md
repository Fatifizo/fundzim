# FundZim Donor Protection Policy (Stage 1 design)

Status: **Stage 1 design — provisional.** Nothing here is approved by the business owner or reviewed by
counsel. Every rule separates three sources of obligation, which must never be blended:

| Source | Meaning | Who decides | Where tracked |
|---|---|---|---|
| **PLATFORM POLICY** | A choice FundZim makes about how it treats donors and campaign owners | Business owner (provisional recommendation given here) | `PD-xxx` in [PRODUCT.md](../PRODUCT.md) §13 / Stage 2 handover |
| **PROVIDER RULE** | A constraint imposed by the PSP, card scheme or mobile-money operator (refund windows, chargeback rules, reversal rights) | Provider contract and documentation | `PCR-xxx` in [provider-questions.md](../payments/provider-questions.md) |
| **LEGAL REQUIREMENT** | An obligation imposed by Zimbabwean law or regulation | Qualified Zimbabwean counsel | `LR-xxx` in [open-legal-questions.md](open-legal-questions.md) |

Where a legal requirement is later confirmed, it **overrides** the platform policy below. The platform policy
is a floor for donor treatment, never a claim of legal sufficiency.

Related: [refund-and-dispute-architecture.md](../payments/refund-and-dispute-architecture.md),
[refund-and-reversal-flows.md](../payments/refund-and-reversal-flows.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md),
[campaign-approval-policy.md](campaign-approval-policy.md),
[compliance-case-management.md](compliance-case-management.md),
[incident-response-workflows.md](incident-response-workflows.md), [LEDGER.md](../LEDGER.md),
[PAYMENTS.md](../PAYMENTS.md), [PRODUCT.md](../PRODUCT.md).

---

## 1. Principles

1. **Donations are gifts, not purchases.** FundZim does not promise outcomes on behalf of campaign owners.
   It does promise that it reviews campaigns, acts on credible fraud reports, and does not knowingly pay out
   funds raised by fraud.
2. **Refunds move money back along the original rail.** A refund goes to the instrument that paid (same
   provider, same donor wallet or card). Refunds to a different instrument, wallet or person are prohibited —
   this is the main refund-abuse vector (threat F-05 in [THREAT-MODEL.md](../THREAT-MODEL.md)).
3. **Refunds are financial operations.** Each one is idempotent, ledger-backed (reserve on approval, settle on
   provider confirmation, reverse on failure — [LEDGER.md](../LEDGER.md) §6.3) and maker-checker approved
   ([operational-controls.md](operational-controls.md)).
4. **Freeze first, decide later.** When fraud is credibly alleged, FundZim stops money leaving (freeze) before
   it decides who is right. Freezing never edits history; it is a hold record plus a ledger move from
   `campaign_payable` to `campaign_held`.
5. **Donor communication is truthful and non-defamatory.** FundZim tells donors what it has done (paused,
   refunded) and not conclusions it cannot yet support (LR-088).
6. **No double recovery.** A donation that is refunded cannot also be charged back successfully, and vice
   versa; the payment state machine ([transaction-lifecycle.md](../payments/transaction-lifecycle.md)) and
   provider reconciliation detect and resolve overlap.

---

## 2. Donor refund requests (donor changes their mind / donated in error)

| Aspect | Rule |
|---|---|
| **PLATFORM POLICY (provisional)** | A donor may request a refund within a refund window (`PD-30`) **only if the funds have not been paid out** from that campaign's available balance. Within the window and before payout, approval is routine. After the window or after payout, refunds are discretionary and require a COMPLIANCE or FINANCE reason. Recommended window: short (days, not weeks) because campaigns are often time-critical (funeral, medical). Approving authority: business owner. |
| **PROVIDER RULE** | Whether the rail supports refunds at all, partial refunds, refund windows, refund fees, and whether a refund is returned to the original wallet/card. Mobile-money refunds may be manual or unsupported on some rails — `PCR-006 — refund capability per rail`. If a rail does not support refunds, the platform policy must say so **before** the donor pays (disclosure on the payment page). |
| **LEGAL REQUIREMENT** | Whether donors have statutory cancellation or refund rights for online payments that are gifts — LR-018. Disclosure duties for refund terms — LR-018, LR-022. |
| **Ledger** | Reserve (`campaign_payable` → `refund_payable`) on approval; settle on provider confirmation; reverse on provider failure. The platform fee treatment on refund (refund fee to donor or retain) is `PD-32`; PSP fee treatment follows LR-028 / [LEDGER.md](../LEDGER.md) G-3. |
| **Process** | Donor request → SUPPORT logs (maker) → FINANCE approves (checker, ≠ maker) → provider refund call (idempotency key = `refund_id`) → state `PARTIALLY_REFUNDED` / `REFUNDED` on provider confirmation → donor notified. |

Not refundable by platform policy (provisional): donations where the donor has already obtained a
chargeback; donations older than the provider's refund window (provider rule wins).

---

## 3. Duplicate payments

A donor is charged twice for one intended donation (double tap, retry after timeout, USSD prompt repeated).

| Aspect | Rule |
|---|---|
| **Prevention** | Client idempotency key per donation attempt; provider reference = `payment_id` reused on retry; UNKNOWN outcomes are resolved by status query, never by creating a second payment ([PAYMENTS.md](../PAYMENTS.md) §8, §10). |
| **Detection** | Duplicate heuristic in reconciliation and monitoring: same donor identifier + campaign + amount + currency within a short window, both `SUCCEEDED` ([transaction-monitoring.md](transaction-monitoring.md)). |
| **PLATFORM POLICY (provisional)** | Confirmed duplicates are refunded in full **regardless of refund window or payout status** — FundZim bears the timing risk if the campaign has already been paid out (recovery per §8). Approving authority: business owner. Duplicate refunds still require maker-checker. |
| **PROVIDER RULE** | Same refund capability constraints as §2 (`PCR-006 — refund capability per rail`). |
| **LEGAL REQUIREMENT** | Possible consumer-protection duty to reverse erroneous charges — LR-018. |
| **Incident** | A *system-caused* duplicate (FundZim bug) is a duplicate financial transaction incident ([incident-response-workflows.md](incident-response-workflows.md) §8), not a routine refund. |

---

## 4. Failed and cancelled campaigns

| Situation | PLATFORM POLICY (provisional) | LEGAL / PROVIDER |
|---|---|---|
| Campaign **rejected before publication** | No donations were possible (only `ACTIVE` accepts donations — [PRODUCT.md](../PRODUCT.md) §6.1). Nothing to resolve. | — |
| Campaign **cancelled by owner, no payout yet** | Staff review required if funds raised. Default recommendation: refund all donors, unless the owner demonstrates the funds will still be used for the stated purpose and COMPLIANCE approves payout. | LR-019 (disposition of funds), LR-018 |
| Campaign **cancelled by owner after partial payout** | Remaining available balance: refund donors pro-rata **or** pay out for the stated purpose — `PD-04` (existing). Paid-out funds are not clawed back unless fraud or misuse is found (§8). | LR-019 |
| Campaign **did not reach its goal** | Not a failure. Funds remain payable (goals are targets, not thresholds). No "all-or-nothing" model in MVP. | — |
| Beneficiary **died / need disappeared** (e.g. medical patient died) | Owner must declare the change; campaign is re-reviewed; funds may be redirected to funeral costs or the estate only with COMPLIANCE approval and updated beneficiary verification ([beneficiary-verification.md](beneficiary-verification.md)). Donors notified. | LR-015 (deceased beneficiaries), LR-019 |
| Campaign **cancelled by FundZim** (policy breach, not fraud) | Freeze, then decide per LR-019. Default recommendation: refund. | LR-019, LR-022 |

Refunding a whole campaign is a **bulk refund** operation: one maker-checker approval covers the batch, each
donation still gets its own `refund_id`, idempotency key, ledger postings and provider call. Rails without
refund support produce a list for manual donor contact (`PCR-006 — refund capability per rail`).

---

## 5. Fraudulent campaign complaints

Reports arrive via the "Report campaign" button, support channels, social media, PSP notices or monitoring
alerts ([transaction-monitoring.md](transaction-monitoring.md)).

| Step | Rule |
|---|---|
| Intake | Every report creates or attaches to a compliance case ([compliance-case-management.md](compliance-case-management.md)). Reporter identity is protected from the campaign owner. |
| Triage SLA | `PD-31` placeholder. Funeral/emergency/medical campaigns with active payout requests triaged first. |
| Immediate action | If the report is credible (specific, evidenced, or corroborated by risk signals): **SUSPEND** (donations and payouts paused) or **FREEZE** (payouts frozen, funds held) per [campaign-approval-policy.md](campaign-approval-policy.md) §7. Pending payouts not yet `SUBMITTED` are pulled back to `PENDING_REVIEW` or cancelled. |
| Investigation | Owner asked for evidence; beneficiary contacted independently where possible; documents re-verified. |
| Outcomes | (a) Cleared → unfreeze (maker-checker) and resume. (b) Misrepresentation, not criminal → campaign cancelled, refunds per §4. (c) Fraud → FROZEN → CANCELLED, refunds of unpaid funds, recovery of paid funds (§8), suspicious-transaction escalation (LR-008), account suspension. |
| PLATFORM POLICY | Whether FundZim refunds donors **from its own funds** when fraud funds were already paid out and unrecoverable (a "donor guarantee") — `PD-34`. Recommendation for MVP: **no guarantee**, but a published commitment to refund all unpaid funds and pursue recovery. |
| LEGAL | Obligation to report fraud to police or the FIU, and tipping-off restrictions on what can be said to the owner — LR-008. Donor notification wording — LR-088. |

---

## 6. Chargebacks (card donations)

| Aspect | Rule |
|---|---|
| **PROVIDER RULE** | Card scheme dispute windows, evidence formats, representment process, chargeback fees, and whether the PSP debits FundZim or the campaign's held funds — `PCR-005 — chargeback process and liability`. These rules are not under FundZim's control and override platform policy on timing. |
| **Ledger** | Dispute opened → disputed amount moved to `campaign_held` if still available; payment `DISPUTED`. Lost → `CHARGED_BACK`, posting per [LEDGER.md](../LEDGER.md) §6.4. Won → hold released. |
| **PLATFORM POLICY (provisional)** | FundZim represents (contests) disputes where it holds evidence of a genuine donation (successful 3-D Secure where available, donor confirmation, no fraud signals). High-risk campaign tiers may carry a payout reserve or delay so card funds are not fully paid out inside the typical dispute window — `PD-33`. |
| **Loss allocation** | Who bears a lost chargeback after payout (FundZim, the campaign owner via recovery, or the PSP) — `PD-34` + LR-020. |
| **Fraud linkage** | A chargeback spike on one campaign opens a case and can trigger a freeze ([transaction-monitoring.md](transaction-monitoring.md)). |

---

## 7. Disputes after payout

When a refund, chargeback or fraud finding arrives after the campaign's funds were paid out:

1. The ledger records the obligation honestly: the campaign's payable goes negative only through an explicit
   **recoverable** posting (`asset:chargeback_recoverable` or a beneficiary recovery receivable to be defined
   in Stage 10), never by silently netting against another campaign's money. **One campaign's funds are never
   used to cover another campaign's losses.**
2. Future donations to the same campaign may be withheld to offset the recoverable amount — only if the
   campaign terms permit this (`PD-34`, LR-022).
3. The owner is notified with the reason and the amount.
4. If unrecoverable, the loss moves to `expense:chargeback_losses` (FundZim bears it) by a maker-checker
   ledger adjustment with a written reason.

---

## 8. Beneficiary recovery obligations

| Aspect | Rule |
|---|---|
| **PLATFORM POLICY (provisional)** | Campaign terms (accepted at submission, evidence-recorded per [audit-evidence-model.md](audit-evidence-model.md)) oblige the owner — and, where they sign, an organisation or beneficiary representative — to repay funds paid out as a result of fraud, misrepresentation, duplicate payment or chargeback. |
| **Mechanism** | Recovery case → demand notice → offset against future payouts (if permitted) → repayment through a supported rail → write-off decision (maker-checker) → possible referral to authorities. |
| **LEGAL** | Enforceability of recovery and offset clauses, and limits on recovering from a third-party beneficiary who never agreed to FundZim's terms — **LR-085** (→ LR-080) (new). Terms of service — LR-022. |
| **Not permitted** | Debiting a beneficiary wallet or bank account without a fresh, authorised payment by the beneficiary. FundZim never "pulls" money back from a payout destination unless the provider rail and a signed mandate allow it. |

---

## 9. Donor communication

| Event | Donor receives | Channel | Timing | Content rules |
|---|---|---|---|---|
| Payment `SUCCEEDED` | Donation confirmation (not a tax receipt — LR-017) | Email/SMS per consent | On authoritative confirmation, never on redirect | Amount, currency, campaign, provider reference, refund policy link |
| Payment `UNKNOWN` / `PENDING` too long | "We're still confirming your payment" | Same | When the payment has been unresolved past a threshold (config) | Never say "failed" unless authoritative; tell donor not to pay again |
| Payment `FAILED` / `EXPIRED` | Failure notice | Same | On authoritative status | Donor was not charged (if provider confirms) |
| Refund approved / completed | Refund notices | Same | On approval, then on provider confirmation | Expected timing per rail |
| Campaign `SUSPENDED` / `FROZEN` | "This campaign is paused while we review it" | Email (donors who opted in to updates) | Within the case SLA | **No accusations**; no reasons that would tip off a suspect (LR-008, LR-088) |
| Campaign `CANCELLED` | What happens to their donation (refund or redirect) | Email/SMS | When disposition is decided | Must match LR-019 outcome |
| Fraud confirmed | Outcome and refund status | Email/SMS | After legal/compliance clearance | Wording approved by COMPLIANCE (LR-088) |

Marketing communications are separate and consent-based (LR-023). Anonymous donors still receive
transactional messages at their contact details; anonymity is about public display, not about contact.

---

## 10. Administrative escalation

| Level | Who | Triggers | Powers |
|---|---|---|---|
| L1 | SUPPORT | Donor/owner contact, refund requests within policy | Log requests, request refunds (maker), view masked data |
| L2 | REVIEWER / FINANCE | Refund approval, campaign concerns, payout queries | Approve refunds (FINANCE, checker), suspend campaigns (REVIEWER) |
| L3 | COMPLIANCE | Fraud reports, chargeback clusters, sanctions hits, recovery cases | Freeze, open cases, STR escalation (LR-008), recovery |
| L4 | Business owner / designated senior officer | Write-offs above threshold, donor guarantee decisions, regulator contact, media | Final decisions on PD-30..PD-34 exceptions; regulator liaison (LR-032) |

Every escalation is a case transition with actor, reason and timestamp ([compliance-case-management.md](compliance-case-management.md)).
Complaint-handling timelines and any statutory complaints or ombudsman route — **LR-086** (new).

---

## 11. New legal questions (introduced here)

| ID | Area | Question | Why it matters | Impact | Owner | Status |
|---|---|---|---|---|---|---|
| LR-085 (→ LR-080) | Recovery from beneficiaries | Are contractual recovery and offset clauses enforceable against campaign owners, organisations and third-party beneficiaries who received payouts later found to be fraudulent, duplicated or charged back? What process is required before offsetting future donations? | Determines whether FundZim can recover paid-out funds or must absorb losses | Recovery cases, offset feature, terms of service, ledger recoverable accounts | Counsel | OPEN |
| LR-086 | Complaints handling | Are there statutory complaint-handling timelines, record-keeping or escalation routes (e.g. a consumer or payments complaints body) for donor and owner complaints? | Drives SLA configuration and complaint records | Case management SLAs, donor communications | Counsel | OPEN |
| LR-088 | Donor notification & defamation | What may FundZim tell donors and the public about a campaign under investigation or found fraudulent without defamation exposure or breaching tipping-off rules? | Communication templates for suspended/frozen/cancelled campaigns | Notification templates, public notice pages | Counsel | OPEN |

(LR-087 and LR-089 are introduced in [incident-response-workflows.md](incident-response-workflows.md) and
[operational-controls.md](operational-controls.md).)

## 12. Business decisions required (owner approval)

| ID | Decision | Provisional recommendation | Impact |
|---|---|---|---|
| PD-30 | Donor refund window and discretionary refund policy | Short routine window, before payout only; discretionary afterwards | Refund volume, campaign-owner trust, provider refund costs |
| PD-31 | Review and complaint SLAs per risk tier | Fast-track for funeral/emergency with tighter payout controls | Staffing, queue design |
| PD-32 | Platform fee treatment on refunds and chargebacks | Refund the platform fee on duplicate and fraud refunds; retain on discretionary refunds | Revenue, ledger posting rules (with LR-028) |
| PD-33 | Payout reserve / delay for high-risk tiers and card-heavy campaigns | Percentage or time-based holdback configured per tier, value TBD | Chargeback exposure vs beneficiary urgency; uses `campaign_reserve` |
| PD-34 | Loss allocation after payout (chargebacks, fraud) and whether to offer a donor guarantee | No guarantee in MVP; FundZim bears unrecoverable chargebacks; recovery pursued | Financial exposure, terms of service (LR-020, LR-085 (→ LR-080)) |
