# Stage 15 — Retries

Engineering defaults (`notifications.DefaultSettings`, not service commitments; KI-S14-03 applies — no approved
targets exist):

| Setting | Default |
|---|---|
| sweep interval | 5 s (River periodic job `notifications.deliver`) |
| batch | 50 deliveries per sweep |
| lease | 2 min |
| send timeout | 15 s |
| backoff | 30 s × 2^(attempt−1), capped at 30 min, ±20 % jitter; a longer provider `Retry-After` wins |
| max attempts | 6 (a replay allows up to 3 more, never above 20) |
| unknown grace | 2 min before the first status lookup |
| unknown maximum | 24 h, then dead-lettered `OUTCOME_UNKNOWN` for an operator |

## Classification (`providers.go`)

| Class | Example | Action |
|---|---|---|
| TEMPORARY | 5xx, connection refused before sending | FAILED_RETRYABLE, backoff |
| RATE_LIMITED | 429 / simulator `ratelimit` | FAILED_RETRYABLE, `Retry-After` honoured |
| TIMEOUT | no answer within the send timeout | **UNKNOWN** — looked up, never blindly resent |
| PERMANENT | invalid destination, rejection, hard bounce | FAILED_PERMANENT, dead letter; bounce/rejection also suppress the destination |

A routing re-check that cannot read consent or suppression state (`ROUTING_CHECK_FAILED`) is retryable and sends
nothing. A render failure is permanent (`RENDER_FAILURE`).

## Idempotency

The provider idempotency key is derived from the delivery id and attempt; a status lookup uses the key of the attempt
in doubt. The simulators honour keys (a resend with the same key returns the original message). SMTP has none (dev
only; [email.md](email.md)).

## Thresholds in the operations view

Backlog older than 5 min → `notifications.queue.backlog` DEGRADED; > 10 % failed attempts in the last hour →
`notifications.error_rate` DEGRADED (measured from `notification_attempts`; NOT_AVAILABLE with no attempts, never 0 %);
≥ 10 open dead letters → alert.
