# Stage 7 prerequisite assessment

Date: 2026-10-09. Branch `stage-7/public-experience`, created from `stage-6/campaigns` at `ca898ae`. Inputs: the
Stage 6 completion report, `docs/stage-handover/STAGE-6-TO-STAGE-7.md`, `docs/stage-6/*`, and the code (web app,
public APIs, caching, rate limits, OpenAPI, CI). Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**,
**BLOCKED**, **NOT_VERIFIED**. No acceptance record was fabricated.

| Prerequisite | Status | Evidence / condition |
|---|---|---|
| Formal acceptance of Stages 1–6 | **NOT_VERIFIED** | No acceptance record exists in the repository, the git history or the conversation (searched `docs/` and commit messages). Stage 7 was started on the owner's explicit request. Carried as a known issue |
| Stage 6 campaign engine and public read APIs | SATISFIED | Public search, detail, updates and media endpoints exist and are covered by integration tests (Stage 6 testing.md) |
| Approved-version semantics | SATISFIED | Public views render `approved_version_id` only; re-review keeps the old version public |
| Stage 6 staff moderation UI against the real stack | **NOT_VERIFIED → remediation A** | Stage 6 covered staff pages against the mock API only (KI-S6-19). Stage 7 adds a real-stack Playwright suite (`apps/web/e2e-real`) |
| Third-party fundraising restriction | SATISFIED (preserved) | `campaign.individual_for_others.enabled = false` (LR-046 – LR-048); Stage 7 does not change it (remediation B) |
| API consistency (KI-S6-08/09/10) | SATISFIED_WITH_CONDITIONS → remediation D | Standardised in Stage 7 for the campaign family (ADR-038 §7); Stage 5 verification and compliance APIs keep their documented behaviour |
| Public data safety | SATISFIED_WITH_CONDITIONS → remediation E | Allow-lists re-validated by a recursive key test over every public endpoint |
| Public caching | **was BLOCKED (defect found)** → fixed | `httpx.WriteJSON` overwrote every handler's `Cache-Control` with `no-store`, so the Stage 6 "cacheable" headers never took effect. Fixed: handlers' policies are kept; errors are always `no-store` |
| Remote CI | SATISFIED | First fully green run in Stage 6 (`db647ae`); Stage 7 extends it with a real-stack E2E job |
| Rate limiting | SATISFIED | Valkey-backed limiter with protective fallback; Stage 7 adds `public.report.*` and `public.event.ip` |
| Media pipeline | SATISFIED_WITH_CONDITIONS | Scan + re-encode; ClamAV does not detect EICAR inside images (KI-S6-15) |
| ZWG, screening, fundraising authority, minors' media | BLOCKED (accepted) | Preserved refusals (LR-043, SCREENING_PROVIDER_NOT_SELECTED, LR-046 – LR-048, LR-070) |

Conclusion: Stage 6 is usable for Stage 7 **with conditions**. The remediation items (A, D, E) are done in
Stage 7. Item B is preserved. Item C (acceptance) cannot be done by engineering and stays NOT_VERIFIED.
