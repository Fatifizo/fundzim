# Stage 16 — Testing

Every result below was run locally in this session (2026-10-19) on branch `stage-16/discovery`, against the compose
stack where a stack is needed, under `flock /tmp/claude-1000/fundzim-stack.lock`. **Remote CI was not run**: nothing
has been pushed (NOT_VERIFIED). No test was skipped, disabled or weakened to pass; the skips listed are pre-existing
environment skips.

## 1. Results

| Suite | Command | Result |
|---|---|---|
| Go unit | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` | 37 packages ok, no data races (final code); 304 unit tests counted in the non-race run |
| Go vet / gofmt | `go vet ./apps/api/... ./internal/... ./migrations/...`, `go vet -tags perf ./tests/perf/`, `gofmt -l apps internal migrations tests` | clean |
| Architecture | `internal/archtest` (in the unit run) | ok — `discovery` registered, imports platform/audit/campaigns only, SQL only on `app.discovery_*`; `app.campaign_locations` owned by campaigns |
| Go integration | `go test -tags integration -count=1 ./tests/integration/...` | full run 186 passed, 0 failed, 13 skipped (pre-existing environment skips); after the last fixes, `TestDiscovery*` and `TestPublicExperience*` rerun: pass |
| Migrations | `fundzimctl migrate up`, `down` ×2, `up` | round trip clean (location FKs, NOT VALID material CHECK on Down, grant procedure restored) |
| Web unit | `npm --prefix apps/web test` | 57 files, 565 tests passed |
| Web lint / typecheck | `npm --prefix apps/web run lint`, `run typecheck` | clean |
| Web build | `npm --prefix apps/web run build` | ok |
| Mock E2E | `npm --prefix apps/web run test:e2e` | full run 213 passed, 1 skipped (pre-existing); after the 204 change the affected specs rerun: 72 passed |
| Real-stack E2E | `apps/web/e2e-real` | full run 58 passed, 2 failed (the copy-link expectation predated referral links, now expects `?ref=`; and the campaign page network-idle wait, fixed by the 204 events response); the affected specs rerun: 25 and 24 passed |
| OpenAPI | Redocly lint | valid (2 pre-existing warnings) |
| Performance | `go test -tags perf -run TestDiscoveryPerf ./tests/perf/` (scratch `fundzim_perf`, 100 000 campaigns) | all scenarios 2xx; one budget miss (rotating queries p95 309.6 ms vs 300; KI-S16-16) — [mobile-performance.md](mobile-performance.md) §5 |
| Core Web Vitals (lab) | `node apps/web/scripts/cwv.mjs` | every page inside the budgets — [mobile-performance.md](mobile-performance.md) §4 |
| Secrets | `scripts/check-secrets.sh` | no secrets found in 1 370 files; gitleaks is not installed on this machine (NOT RUN) |

## 2. What the tests cover

**Unit (`internal/discovery/discovery_test.go`, campaigns, platform):** query normalisation (punctuation, length,
term count, Unicode), tsquery construction and injection (operators are never passed through), highlight round trip,
cursor encode/decode and sort binding, trending formula (log scaling, decay, minimum sample → 0), ranking-policy
weight validation, referral code format and alphabet, dedupe key (HMAC, no raw IP), bot classification, recorder
bounds and drop counting, rate-limit policy registration, queue registration.

**Integration (`tests/integration/discovery_test.go`):**
- *EndToEnd:* publish → index → search (exact, prefix, fuzzy), filters, sorts, pagination, categories, provinces,
  campaign page location, recommendations (`personalised: false`, reasons), views and shares counted once per visitor,
  owner analytics (owner only; another user 404), sitemap.
- *GovernanceAndRBAC:* featured placement maker-checker (self-approval refused in Go, CHECK and trigger, including a
  linked staff identity), window > 31 days refused, ranking policy maker-checker and immutability, permissions per
  route (public, owner, REVIEWER, COMPLIANCE, ADMIN; no SUPER_ADMIN shortcut), audit records.
- *FailureInjectionAndAbuse:* restricted, held, paused, unpublished and suspended campaigns vanish from every surface
  at once (serve-time re-check before the index catches up), registry failure → 503 DISCOVERY_UNAVAILABLE (fail
  closed), malformed cursors and queries, rate limits (429 with Retry-After), bot traffic not counted, DNT honoured by
  the web layer, referral codes for another campaign or revoked/expired codes ignored, production referral block.
- *Concurrency:* concurrent reindex of the same campaign (snapshot ordering keeps the newest), concurrent approvals
  of one placement (one wins), concurrent event ingestion (no lost or double counts).

**E2E (mock and real stack):** directory, search with highlights, filters via the no-JS form, category, trending,
featured, province pages, campaign page breadcrumbs and recommendations, share panel (WhatsApp, Facebook, X, LinkedIn,
copy link with referral), poster page and print, offline notice, owner analytics tab, staff discovery admin
(placements, ranking policies), axe (WCAG 2.x A/AA serious and critical) on every new page on desktop and Pixel 7.

**SEO:** metadata (canonical, noindex,follow on filtered variants), Open Graph and Twitter tags with image dimensions,
generated preview image (200 + ETag for a public campaign, 404 no-store otherwise), robots, sitemap (provinces,
trending, featured; never a non-public campaign), BreadcrumbList JSON-LD and its escaping.

## 3. Not tested

- Real WhatsApp, Facebook, X or LinkedIn preview rendering (no public URL; KI-S16-01).
- Field performance on real devices (KI-S16-14); manual screen-reader audit (KI-S16-13).
- Remote CI (not pushed).
