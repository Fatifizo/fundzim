# Stage 20 — Production launch blocker register

**Production launch: BLOCKED. Launch authorisation: NOT GRANTED.** Every item below must be closed with recorded
evidence (or, where marked, an authorised and recorded risk acceptance) before any launch can be considered. The
financial kill switches and gate constants stay off until then. IDs refer to findings-register.md.

| # | Blocker | Category | IDs | Closure evidence required | Responsible |
|---|---|---|---|---|---|
| B-01 | No payment or payout provider selected; only the simulator exists | provider | ADR-040, PCR-001…066 | selection ADR, contract, sandbox credentials in the secret manager, conformance suite passing against the provider sandbox, PCR answers | owner |
| B-02 | 31 P0 legal/regulatory items unresolved; no counsel review | legal | LR P0 | written determinations per register §6 | counsel, owner |
| B-03 | Privacy: no POTRAZ licence/DPO, no approved notices, no retention schedule, no data-subject workflow | legal / privacy | LR-010, LR-012, LR-057, LR-074, KI-S18-13, KI-S18-14 | licence, appointments, approved notices and schedule, implemented DSR workflow | counsel, DPO, owner |
| B-04 | No accounting review of the ledger model; no fee model | accounting | KI-S10-02, PD-01 | accountant's written review; recorded fee model | accountant, owner |
| B-05 | Reconciliation coverage not guaranteed (windowed internal detectors) | financial / implementation | KI-S19-09 | approved coverage requirement; incremental reconciliation with deterministic coverage tests | owner, tech lead |
| B-06 | No approved capacity/latency/volume targets; measurements on a dev host only; audit-chain ceiling | capacity | KI-S19-01, F-S20-04 | approved targets; measurements on production-like infrastructure meeting them | owner, SRE |
| B-07 | Staff security conditions: WebAuthn, MFA reset, break-glass workflow, internal endpoints | security | F-S18-03, -04, -05, -12 | implemented controls or recorded risk acceptances | owner, security |
| B-08 | No KMS/HSM; no re-encryption job | security | KI-S18-02, KI-S18-03 | KMS selection ADR and implementation | owner |
| B-09 | No independent penetration test | security | KI-S18-11, KI-S18-21 | test report and remediation | owner |
| B-10 | Unfixed CRITICAL/HIGH web-image CVEs need a risk decision | security | KI-S18-10 | recorded decision (accept with compensating controls, or new base image and clean scan) | owner |
| B-11 | No provisioned infrastructure, supervisor, HA decision, off-host backups, secret-manager backups; RPO/RTO unapproved; full-loss recovery untested | infrastructure | KI-S18-01, -12, -19, -20 | provisioned environment, approved RPO/RTO, restore drill and DR exercise there | owner, SRE |
| B-12 | No alert delivery, on-call, operators, support staffing | operations | KI-S18-15, F-S20-05 | configured destinations, rota, runbook drills | owner |
| B-13 | `main` unprotected; no release baseline, tag or release/rollback procedure; unsigned provenance | release / governance | F-S20-01, F-S20-02, F-S20-06, KI-S18-08 | protection rule visible in the API; tagged baseline with green CI; signed attestations | repository owner, tech lead |
| B-14 | Poll budget values are internal defaults | provider | KI-S19-04 | provider semantics + owner approval | owner |
| B-15 | Concurrent index migrations procedure missing for live data | infrastructure | KI-S19-15 | procedure and rehearsal | tech lead |
| B-16 | Stages 0–20 not formally accepted | governance | register | owner entries in the register | owner |
| B-17 | No explicit launch authorisation and go/no-go record with named approvers | governance | ROADMAP Stage 20 | signed go/no-go | owner |

Not blocking (engineering, fixed or limited): F-S20-03, F-S20-07, F-S20-08 (fixed); KI-S19-16, KI-S19-17 and the
LOW Stage 19 items (tracked in findings-register.md).
