# Stage 15 — In-app notifications

## What the user sees

* `/dashboard/notifications` — the caller's notifications, newest first, cursor-paginated (25 per page), filter
  read/unread and category; each item shows title, body, category, time, an `Unread` badge, `Important` for HIGH
  priority, `TEST data` for synthetic sources, an "Open" link to a relative FundZim page and "Mark as read".
  "Mark all as read" marks everything up to now.
* `/admin/notifications` — the same centre for staff accounts (assignments, incidents, approvals waiting,
  escalations).
* Unread counts: the dashboard home, the account menu ("Notifications") and the admin shell show
  "Notifications: N unread", polled every 60 s from `GET /api/v1/notifications/unread-count`. If the count cannot be
  loaded the link says "Notifications (could not load)" — never 0.

## Rules

* In-app is the record: every notification has an in-app copy rendered at creation (`notifications.title/body`) so
  later template changes never alter history; the template version id is stored.
* A user reads only their own notifications (`recipient_user_id` = caller, checked in SQL); another user's id answers
  404 `NOTIFICATION_NOT_FOUND` (no enumeration).
* Deep links are relative paths under `/dashboard`, `/admin`, `/campaigns` or `/settings` (database CHECK); the page
  opened checks access itself.
* SECURITY in-app notices are mandatory (cannot be turned off). RISK, COMPLIANCE and ADMINISTRATIVE are staff-only.
* Marking read is idempotent; it sets `read_at` once.
* Real-time push (SSE/WebSocket) was not needed: polling every 60 s is the simplest approach the brief allows.

## Accessibility

Lists are labelled (`aria-label="Notifications"`), unread state is announced ("Unread:" prefix), each action button has
a unique accessible name ("Mark as read: <title>"), confirmations are `role="status"` and receive focus, and the
pages pass the axe checks in the Playwright suite (desktop and mobile).
