package app

import (
	"context"
	"fmt"

	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// registerConsumers subscribes outbox consumers in the worker process (interface contract §2.3).
// Handlers run outside any transaction (they may send email/SMS) and must be idempotent: delivery is at
// least once. Consumer names are part of the dedupe key; never rename one.
func registerConsumers(ctx context.Context, d *WorkerDeps, reg *outbox.Registry) error {
	a, o, err := NewAuthService(ctx, IdentityDeps{Pool: d.DB, Config: d.Config, Clock: d.Clock, Logger: d.Logger})
	if err != nil {
		return fmt.Errorf("identity consumers: %w", err)
	}
	registerIdentityConsumers(reg, a, o, mailAdapter{d.Email})
	return nil
}
