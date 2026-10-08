# internal — Go domain modules

**Status: not started (Stage 3+).** Module boundaries are specified in
[docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md); each module is created in the stage that implements it
(see [docs/ROADMAP.md](../docs/ROADMAP.md)). Empty module directories are deliberately not created.

Planned modules: `platform` (shared kernel: config, db, clock, ids, `money`, logging, HTTP envelope),
`auth`, `users`, `organisations`, `campaigns`, `payments`, `ledger`, `payouts`, `fees`, `kyc`, `compliance`,
`risk`, `notifications`, `admin`, `audit`, `reconciliation`, `storage`.

Rules (also in [CLAUDE.md](../CLAUDE.md)): modules call each other only through public service interfaces;
no module reads another module's tables; no import cycles; only `ledger` writes ledger tables; only `kyc`
touches KYC data.
