-- Stage 15 finalisation: indexes for the notifications operations snapshot (ADR-047 §10). The conflicting-evidence
-- count and the marketing branch of the consent-enforcement check scanned every delivery / notification on each
-- snapshot (parallel sequential scans; under concurrent snapshots on a large development dataset the parallel hash joins
-- exhausted the container's shared memory). Partial indexes keep both checks proportional to what they look for.
-- Owner module: notifications. Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

CREATE INDEX ix_notification_deliveries_conflicting ON app.notification_deliveries (updated_at) WHERE conflicting_evidence;
CREATE INDEX ix_notifications_marketing ON app.notifications (created_at) WHERE category = 'MARKETING';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX app.ix_notifications_marketing;
DROP INDEX app.ix_notification_deliveries_conflicting;
-- +goose StatementEnd
