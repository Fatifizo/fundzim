# Stage 15 — Dead letters

`app.notification_dead_letters`: one row per delivery that needs a person — `RETRIES_EXHAUSTED`,
`PERMANENT_FAILURE`, `OUTCOME_UNKNOWN` (unresolved after 24 h) or `RENDER_FAILURE` — with the attempt count, last error
code (`^[A-Z][A-Z0-9_]+$`, never a provider message) and review status `OPEN | REPLAYED | DISCARDED`.

`/admin/communications/dead-letters` (`communications.view`) lists them (all by default, filterable); replay and
discard need `communications.replay` (ADMIN, SECURITY_ADMIN), a reason and a fresh step-up, and are audited
(`notifications.dead_letter.replayed|discarded`).

## Replay safety

A replay re-queues **the same delivery** (same notification, template version and recipient). Before re-queueing,
in one transaction with both rows locked:

1. the dead letter must still be OPEN (`NOTIFICATION_STATE` otherwise — concurrent replays: exactly one wins, tested);
2. the delivery must be FAILED_PERMANENT or UNKNOWN (`DELIVERY_NOT_FAILED`);
3. an UNKNOWN delivery whose provider has the message is **not** resent (`PROVIDER_HAS_MESSAGE`; the provider is asked
   before the transaction);
4. the source event must still exist (`SOURCE_EVENT_MISSING`) and the recipient too (`RECIPIENT_MISSING`);
5. routing is decided again: consent, preference, suppression, contact verification, synthetic source, live
   messaging. Any suppression blocks the replay with that reason (e.g. `CONSENT_WITHDRAWN`).

A blocked replay changes nothing and tells the operator why ("Not replayed: the person withdrew consent."). There is no
bulk replay and no way to change the destination or content of a replay.
