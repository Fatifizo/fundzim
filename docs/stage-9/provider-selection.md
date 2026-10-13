# Stage 9 — Provider selection

**Decision: `DECISION_DEFERRED`** ([ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md)). Date: 2026-10-13.

| Question | Answer |
|---|---|
| Was a provider selected? | **No.** No owner decision exists, and no provider passes the selection gates ([provider-evaluation.md](provider-evaluation.md) §1) |
| Was a provider-selection approval recorded? | **No.** None was given; none was invented |
| Which providers have a real adapter? | **None** |
| Which providers have a FundZim sandbox account? | **None** (no credentials supplied; Claude Code did not sign up on the owner's behalf) |
| Which provider executes in Stage 9? | The FundZim sandbox **simulator** only (`LOCAL_SIMULATOR`, never production) |

## Owner actions that unblock selection

1. Send PCR-001, PCR-002, PCR-003 (and PCR-004..018) in writing to the due-diligence candidates
   ([provider-questions.md](../payments/provider-questions.md)).
2. Obtain counsel's view on LR-001, LR-003, LR-004 and LR-036/037/039/041 ([legal-review.md](legal-review.md)).
3. Choose a provider, sign its merchant/sandbox terms and obtain **sandbox** credentials.
4. Put the credentials into the secret manager under `fundzim/sandbox/<provider>/…`
   ([secret-management.md](secret-management.md) §5). Never send them by chat or commit them.
5. Ask engineering to add the adapter and run the sandbox certification
   ([sandbox-certification.md](sandbox-certification.md)); record the selection in an ADR superseding ADR-040.
