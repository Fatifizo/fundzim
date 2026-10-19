# Stage 15 — WhatsApp

## Status

No WhatsApp Business Solution Provider is selected. `whatsapp_simulator` (TEST_ONLY) is the only adapter;
`whatsapp_production` is NOT_SELECTED. `NOTIFICATIONS_WHATSAPP_PROVIDER` = `disabled | simulator`.

## Template-only outbound

There is **no free-form outbound WhatsApp message**. A WhatsApp delivery sends a template name, a language and the
template's variable values as parameters — nothing else. A version has a WhatsApp part only when it carries
`whatsapp_template_name` and `whatsapp_language`; none of the 61 baseline templates does, so every WhatsApp delivery is
currently `SUPPRESSED: TEMPLATE_CHANNEL_MISSING` (or an earlier reason).

## Provider approval is never assumed

`whatsapp_provider_status` = `NOT_SUBMITTED | SUBMITTED_TO_SIMULATOR | SIMULATOR_ACCEPTED | SIMULATOR_REJECTED | SUBMITTED |
APPROVED | REJECTED`. The admin action "Submit WhatsApp template to the simulator" yields `SIMULATOR_ACCEPTED` or
`SIMULATOR_REJECTED` and is labelled TEST. A database trigger refuses `APPROVED` without provider evidence, and none
exists. Routing requires `APPROVED`, or `SIMULATOR_ACCEPTED` when the active WhatsApp provider is the simulator
(`simulatedChannel`); a simulator acceptance never counts for a real provider.

## Consent and windows

Like SMS: opt-in preference **and** a GRANTED `WHATSAPP / SERVICE_MESSAGES` consent record, a verified phone number,
no suppression, no synthetic source. Meta's messaging policies (opt-in evidence, 24-hour customer-service windows,
template categories and pricing) are PROVIDER_CONFIRMATION_REQUIRED and LR-023 — none was encoded as fact.

## Simulator scenarios (last two digits)

91 failed, 92 rejected, 93 timeout, 94 temporary failure, 95 duplicate callbacks, 96 out of order, 97 invalid webhook
signature (the callback is rejected and recorded in `notification_webhook_rejections`), 98 rate limited. Read receipts
(READ) are simulated for delivered messages.

## Stage 16

WhatsApp *share links* (wa.me) for campaigns are Stage 16 (discovery and sharing) and need no provider.
