# Stage 14 — Incident management

Pages `/admin/operations/incidents`, `/admin/operations/incidents/{id}`. View: `ops.incident.view` (all operational
roles incl. SUPPORT). Declare and manage: `ops.incident.manage` (ADMIN, SECURITY_ADMIN; step-up).

- Declare: title (3–200), severity SEV1–SEV4, affected module (lower-case name), optional start time and note. From an
  alert, `alert_key` is stored and the database keeps **one open incident per alert key** (a second declaration
  returns the open one); after it is CLOSED the same alert opens a new incident.
- Lifecycle `ops_incident` (see [operations-architecture.md](operations-architecture.md)); RESOLVED needs a resolution
  note; a CLOSED incident only accepts post-incident notes.
- Owner (must hold `ops.incident.manage`), severity re-rating and notes are timeline entries
  (`app.ops_incident_events`, append-only). Updates carry the version (`OPS_RECORD_CHANGED` when stale).
- No status page, customer communication or paging is sent (Stage 15 / later).

`TestOperationsIncidentsAndAlerts` covers dedupe, the full lifecycle, post-incident notes, SUPPORT view-only, and a new
incident after closure.
