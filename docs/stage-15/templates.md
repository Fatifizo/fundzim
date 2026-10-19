# Stage 15 — Templates

## Model

`app.notification_templates` (one row per key: category, sensitive flag, allowed variables, origin) and
`app.notification_template_versions` (immutable content per version and locale: e-mail subject and text, SMS text,
WhatsApp template name and language, in-app title and body, SHA-256 content hash, status, maker, submitter, approver,
reasons, timestamps, optimistic `version`). 61 templates are seeded with an ACTIVE version 1 (`approval_source =
MIGRATION_BASELINE`): 27 moved from the Stage 4–6 notices, 34 new. English only.

## Rendering (`render.go`)

* Placeholders are `{{variable}}` only — no conditionals, loops, functions, includes or expressions. A template cannot
  execute code or reach data the engine did not pass.
* A version is validated on save: every placeholder must be in the template's allow-list (plus `display_name`), braces
  must balance, the subject cannot contain line breaks and the SMS text must fit three segments.
* Values are cleaned (control characters removed, trimmed, ≤ 200 characters). A missing value is a render error — the
  delivery is dead-lettered `RENDER_FAILED`; a message is never sent with a gap.
* HTML e-mail is built from the escaped text only ([email.md](email.md)).
* The content hash is SHA-256 over subject, text, SMS, in-app title and body joined by U+001F; the migration computes
  the same form in SQL for the seeded versions.

## Lifecycle (maker-checker)

```
DRAFT ──submit──► PENDING_APPROVAL ──approve──► APPROVED ──activate──► ACTIVE ──(another version activated)──► RETIRED
  │                     └──reject──► RETIRED          └──retire──► RETIRED
  └──retire──► RETIRED
```

| Step | Permission | Step-up | Rule |
|---|---|---|---|
| create / edit draft | `communications.template.request` (ADMIN) | yes | content editable only while DRAFT (trigger) |
| submit | `communications.template.request` | yes | content frozen from here (trigger) |
| approve / reject | `communications.template.approve` (COMPLIANCE) | yes | never the maker, the submitter or an account linked to them (`app.actor_identities`; Go check and table CHECK) — `SELF_APPROVAL_FORBIDDEN` |
| activate | `communications.template.approve` | yes | same separation; the previous ACTIVE version of the locale is retired in the same transaction |
| retire | `communications.template.approve` | yes | DRAFT or APPROVED only — an ACTIVE version is replaced by activating another, never left empty |

Every step is audited (`notifications.template.<action>`, template key and reason; no content). Only the migrator may
insert a `MIGRATION_BASELINE` version (trigger).

## Which version is used

A notification records the template version id it was rendered with (`notifications.template_version_id`, and every
delivery renders that same version), so activating a new version while notifications are queued never changes
messages already created. Tested: "template activation during render".

## Preview

`POST /admin/communications/template-versions/{id}/preview` renders every channel with sample values; nothing is sent
or stored.

## Translations

`locale` exists (default `en`). No Shona or Ndebele text was written: translations need a reviewed source, and
fabricating them was out of bounds. Adding a locale is a new version with that locale through the same maker-checker
flow (the recipient locale preference is a Stage 16+ item).

## WhatsApp

See [whatsapp.md](whatsapp.md): template name and language only; simulator acceptance is never provider approval.
