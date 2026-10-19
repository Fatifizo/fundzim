# Stage 15 — SMS

## Providers

| Code | Readiness | Use |
|---|---|---|
| `sms_simulator` | CONFIGURED_TEST, simulated | Sends nothing; scenarios from the destination's last two digits. |
| `sms_dev_mailpit` | CONFIGURED_TEST | Turns an SMS into a Mailpit e-mail (development and test only). |
| `sms_production` | NOT_SELECTED | No aggregator selected. Network coverage, sender IDs, opt-out keywords, delivery receipts and pricing are PROVIDER_CONFIRMATION_REQUIRED; cross-border processing is LR-011. |

`NOTIFICATIONS_SMS_PROVIDER` = `disabled | simulator | dev_mailpit`.

## Who receives an SMS

SMS is opt-in. A delivery is created only when **all** hold: the category allows SMS (SECURITY, PAYMENT, WITHDRAWAL,
PAYOUT, VERIFICATION, CAMPAIGN), the user turned the channel on in preferences, a **GRANTED** consent record for
`SMS / SERVICE_MESSAGES` exists with the current policy version, the phone number is known, normalisable and verified,
it is not suppressed, the source is not synthetic, and a provider is configured. Otherwise the delivery is
`SUPPRESSED` with the reason. There is no fallback from SMS to another channel or back.

LR-023 decides whether service SMS really needs opt-in consent in Zimbabwe; until counsel answers, the stricter
reading (explicit opt-in) is applied. No statute is cited because none was confirmed.

## Phone numbers

`NormalizePhone` parses with `github.com/nyaruka/phonenumbers` (maintained libphonenumber metadata), default region ZW,
and stores E.164. Nothing about the operator or SMS reachability is inferred from the prefix — reachability is only
known from provider delivery evidence.

## Length

`EstimateSMS` counts GSM 03.38 septets (extension characters count two) or UTF-16 units: 160/153 or 70/67 per segment.
A template's SMS text may need at most three segments (validated on save and checked again after substitution).
Providers may count differently: PROVIDER_CONFIRMATION_REQUIRED. No price per segment is assumed.

## Simulator scenarios (last two digits)

91 undeliverable, 92 rejected, 93 timeout (UNKNOWN), 94 temporary failure, 95 duplicate callbacks, 96 out-of-order
callbacks, 98 rate limited; anything else delivered. Integration tests use synthetic `+26377…` numbers.

## Opt-out

Withdrawing consent in settings suppresses future and queued deliveries (re-checked before every send). Inbound
"STOP" keywords depend on the provider and are not implemented (no provider command was invented).
