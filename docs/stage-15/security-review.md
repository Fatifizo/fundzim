# Stage 15 — Security review

Scope: `internal/notifications`, its HTTP routes, the migration, the web pages and the moved notice consumers.
Review performed by the implementing engineer (Claude); an independent review is still required (no external
review claimed).

| Threat | Control | Test |
|---|---|---|
| Reading another user's notifications (IDOR) | every inbox query filters `recipient_user_id = caller`; foreign ids answer 404 | `TestNotificationsInboxIsolationAndStaffRBAC` |
| Staff surfaces reached by users / wrong role | `communications.*` permissions per route; consent overview COMPLIANCE only; step-up for every mutation | same + Playwright RBAC |
| Self-approval of a template | maker / submitter / linked-account check in Go and a table CHECK; `SELF_APPROVAL_FORBIDDEN` | `TestNotificationsDeadLetterReplayAndTemplates` |
| Changing approved content | content immutable after DRAFT (trigger); hash stored | same |
| Template code execution / data reach | placeholder-only templates with an allow-list; no functions | unit `TestRenderIsPlaceholderOnlyAndSafe` |
| HTML / header injection | HTML only from escaped text; subjects without CR/LF; control characters stripped from values | unit tests |
| Forged or replayed provider callbacks | HMAC-SHA256 over `t.body`, 5-minute window, provider event id stored once, payload ≤ 8 KiB and schema-checked, IP rate limit 600/min; every rejection recorded (`notification_webhook_rejections`) and alerted | `TestNotificationsWebhookSecurityAndCrashRecovery` |
| Callback for someone else's message | a callback applies only to the delivery with that provider message id **and** provider; an unknown id is stored as evidence with reason `UNKNOWN_MESSAGE` and changes nothing | same |
| Fabricated consent | grants only from the user's own settings request (`USER_SETTINGS` CHECK); no staff write path | `TestNotificationsConsentAndPreferences` |
| Messages after withdrawal | routing re-run before every send and replay | same + dead-letter replay test |
| Sensitive messages to unverified destinations | unverified e-mail or phone ⇒ `CONTACT_UNVERIFIED` (one documented exception: the existing-account warning to the registering address) | same |
| Fake payment success | success only from `payments.payment_succeeded`; synthetic sources in-app only | `TestNotificationsFinancialSafety` |
| Tipping-off (LR-008) | risk/compliance staff-only; no rule, score, signal or screening data in any template | template catalogue review; financial/staff tests |
| Broadcast abuse | no broadcast or free-form send endpoint exists; staff cannot message users | route table review |
| Replay abuse | per-dead-letter, step-up, reason, audited, one winner under concurrency, full re-check | dead-letter test |
| Secrets and personal data in logs/audit | audit metadata has keys and codes only (no bodies, destinations, tokens); credential messages stay in `auth` and are never stored as notifications | code review |
| Open redirect via deep links | relative-path CHECK on `deep_link` | migration |
| Enumeration through suppression or consent endpoints | user endpoints act only on the caller; staff suppression takes an account id, never an address | code review |

Findings fixed during Stage 15:

* Template text in the HTML part was not escaped (unit test caught it) → HTML built from escaped text only.
* A worker bug assigned `failure_code` twice in one UPDATE, so successful sends were left PROCESSING and later resent
  through crash recovery → fixed; the integration suite now covers the success path end to end.
* The consent evidence reference used a client-supplied `X-Request-ID` header → the server-assigned request id is used.
* A simulator acceptance counted as WhatsApp template approval for any provider → only when the active provider is the
  simulator.
* Staff accounts lost their security e-mails (creation was limited to in-app for staff) → security e-mail stays
  mandatory for every account; other staff channels remain in-app only.
