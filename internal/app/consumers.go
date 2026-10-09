package app

import (
	"context"
	"fmt"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
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

	blobs, err := privateBlobs(d.Config)
	if err != nil {
		return fmt.Errorf("verification storage: %w", err)
	}
	d.Verification, err = NewVerificationModules(ctx, VerificationDeps{AppPool: d.DB, Config: d.Config, Clock: d.Clock, Logger: d.Logger,
		Registry: d.Metrics.Registry, Blobs: blobs, Orgs: o, Auth: a, WithScans: true})
	if err != nil {
		return fmt.Errorf("verification consumers: %w", err)
	}
	registerVerificationConsumers(reg, d.Verification, d.DB, o, mailAdapter{d.Email}, d.Clock)
	registerRemediationConsumers(reg, d.Verification)                             // Stage 6 stream R (ADR-037)
	registerCampaignConsumers(reg, d.Verification, d.DB, o, mailAdapter{d.Email}) // Stage 6 campaigns (ADR-036)
	registerCampaignMediaConsumers(reg, d.Verification)                           // Stage 6 stream M
	return nil
}

// privateBlobs builds the worker's storage client: the private-bucket credentials (it scans and promotes private
// documents) and, when configured, the public-media credential (Stage 6 stream M: it scans campaign images and
// stores their re-encoded derivatives). Config enforces the STORAGE_PUBLIC_* trio all-or-nothing.
func privateBlobs(cfg config.Config) (*pstorage.Client, error) {
	if !cfg.Storage.Enabled() {
		return nil, nil
	}
	creds := map[pstorage.Class]pstorage.Credential{}
	add := func(class pstorage.Class, c config.StorageCredential) {
		if c.Bucket != "" {
			creds[class] = pstorage.Credential{Bucket: c.Bucket, AccessKeyID: c.AccessKeyID.Reveal(), SecretAccessKey: c.SecretAccessKey.Reveal()}
		}
	}
	add(pstorage.PrivateIdentityDocuments, cfg.Storage.KYC)
	add(pstorage.PrivateComplianceDocuments, cfg.Storage.Evidence)
	add(pstorage.PublicCampaignMedia, cfg.Storage.Public) // Stage 6 stream M
	return pstorage.New(pstorage.Options{Endpoint: cfg.Storage.Endpoint, Region: cfg.Storage.Region, ForcePathStyle: cfg.Storage.ForcePathStyle,
		Credentials: creds})
}
