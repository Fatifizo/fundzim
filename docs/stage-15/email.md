# Stage 15 — E-mail

## Providers

| Code | Readiness | Use |
|---|---|---|
| `email_simulator` | CONFIGURED_TEST, simulated | Sends nothing. Deterministic scenarios, status lookup, signed callbacks. |
| `email_dev_smtp` | CONFIGURED_TEST | Local Mailpit (compose). Refused outside `APP_ENV` development/test. SMTP acceptance is recorded as ACCEPTED, never DELIVERED. |
| `email_production` | NOT_SELECTED | No provider selected: LR-010 (controller registration), LR-011 (cross-border transfer), PROVIDER_CONFIRMATION_REQUIRED (bounce/complaint feeds, idempotency, status API, sender authentication). |

Selected by `NOTIFICATIONS_EMAIL_PROVIDER` = `disabled | simulator | dev_smtp` (development `.env`: `dev_smtp`, so the
integration suite can read real messages in Mailpit). `disabled` (the default) leaves e-mail deliveries
`SUPPRESSED: PROVIDER_NOT_CONFIGURED` — never silently sent another way.

## Messages

* Subject, plain text and HTML come from the template version. The HTML part is built **only** from the escaped plain
  text (`html.EscapeString` over the template's own characters and every variable value; paragraphs and line
  breaks become `<p>` and `<br>`). No template HTML, no remote images, no tracking pixels, no link rewriting.
* Subjects cannot contain line breaks (validation at save, replacement at render): no header injection. Variable values
  have control characters removed and are bounded to 200 characters.
* Every message ends with "This is an automated message from FundZim." Synthetic (TEST) notifications never reach
  e-mail (`SYNTHETIC_SOURCE`).
* Links are built from `APP_PUBLIC_URL` + the template's relative deep link; nothing else is linked.

## Destinations

The account's e-mail address, only when it is **verified** — except `REGISTRATION_EXISTING_ACCOUNT` (the "someone tried
to register with your address" notice, sent to the address being registered, which proves nothing about the
registrant) and `ACCOUNT_SUSPENDED` to an inactive account (`inactiveRecipientOK`). Suppressed destinations
(`notification_suppressions`) are never sent to.

## Simulator scenarios

Tag in the local part, `+sim-<scenario>` (cut at the next `-` or `.`): `bounce` (hard bounce → FAILED_PERMANENT and an
automatic HARD_BOUNCE suppression), `reject` (provider rejection → suppression PROVIDER_REJECTION), `tempfail`
(retryable), `permfail`, `timeout` (UNKNOWN, resolved by status lookup), `ratelimit` (Retry-After honoured), `dupcb`
(duplicate callbacks), `ooo` (callbacks out of order). No tag: DELIVERED.

## Limits

SMTP has no idempotency key: a retry after an ambiguous SMTP outcome can duplicate a development e-mail. The simulator
and any production adapter must support a key or a status lookup (conformance expectation, ADR-047 §4).
