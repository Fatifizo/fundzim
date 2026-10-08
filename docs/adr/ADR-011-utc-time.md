# ADR-011: UTC internal time

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (applies from Stage 3)

## Context

Financial records, webhook replay windows, idempotency key expiry, campaign end dates and reconciliation
cut-offs all depend on time. Servers, containers and providers may run in different time zones. Most users are
in Zimbabwe (Africa/Harare, CAT, UTC+2, no daylight saving), but donors are worldwide.

## Decision

- All timestamps are stored as PostgreSQL **`timestamptz`** and processed in **UTC**. They are serialised in
  the API as **RFC 3339 with `Z`**, e.g. `2026-10-08T07:30:00Z`.
- Display uses **Africa/Harare** by default, or the user's preference, and converts only at the presentation
  edge.
- Financial correctness never depends on the server's local time zone. Containers run with `TZ=UTC`.
- A **clock is injected** (`internal/platform/clock`) so time-dependent logic (expiry, replay windows,
  cut-offs) is testable. Direct `time.Now()` calls in domain code are disallowed by lint or review.
- Business dates (e.g. a "daily" reconciliation or report period) are defined explicitly with a time zone and
  boundaries. The default reporting day is the Africa/Harare calendar day, expressed as UTC intervals. This is
  documented per report.
- Ledger transactions record both `occurred_at` (when the economic event happened, per the provider) and
  `posted_at` (when FundZim recorded it).
- Campaign end dates are stored as UTC instants derived from the owner's chosen local date and time.

## Consequences

### Positive
- Unambiguous ordering and comparison, and reproducible tests via the injected clock.

### Negative / costs
- Every display surface must convert. Mistakes show the wrong local time, but they are display bugs, not data
  corruption.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Store local Harare time | Breaks for international donors and providers, and becomes ambiguous if Zimbabwe ever adopts DST. |
| `timestamp without time zone` | Ambiguous meaning, which leads to bugs. |

## Security implications
Replay-window checks and token or OTP expiry rely on consistent UTC. Server clocks must be NTP-synchronised,
and clock skew is monitored.

## Financial implications
Reconciliation cut-offs and settlement periods are deterministic. `occurred_at` and `posted_at` support audit
and late-arriving events.

## Related
ADR-004, [DATABASE.md](../DATABASE.md), [LEDGER.md](../LEDGER.md), [OBSERVABILITY.md](../OBSERVABILITY.md).
