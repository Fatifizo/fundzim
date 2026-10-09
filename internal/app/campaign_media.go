package app

import (
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/campaigns/media"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Stage 6 stream M: campaign media wiring (internal/campaigns/media). The media service is built next to the
// campaigns core in both processes: the API serves the routes; the worker processes scanned originals (it needs the
// public-media credential for that: see privateBlobs in consumers.go and the worker service in compose.yaml).

// CampaignMediaService is the type of VerificationModules.CampaignMedia (an alias, so verification.go needs no
// extra import).
type CampaignMediaService = media.Service

// newCampaignMedia builds the media service on the campaigns core and injects it into the core as its MediaInfo.
func newCampaignMedia(m *VerificationModules, d VerificationDeps) *media.Service {
	deps := media.Deps{Pool: d.AppPool, Storage: m.Storage, Clock: d.Clock, Logger: d.Logger,
		MaxUploadBytes: d.Config.Verification.UploadMaxBytes}
	if m.Campaigns != nil {
		deps.Core, deps.MaterialChanges = m.Campaigns, m.Campaigns
	}
	svc := media.New(deps)
	if m.Campaigns != nil {
		m.Campaigns.SetMedia(svc)
	}
	return svc
}

// campaignMediaExemptions lets the media upload route exceed the global JSON body limit (the handler applies the
// UPLOAD_MAX_BYTES file cap itself).
func campaignMediaExemptions(cfg config.Config) []httpx.BodyLimitExemption {
	return []httpx.BodyLimitExemption{{Pattern: media.UploadPattern, Max: cfg.Verification.UploadMaxBytes + 64<<10}}
}

// registerCampaignMediaRoutes registers the campaign media API (each route declares its own policy).
func registerCampaignMediaRoutes(r *httpx.Router, m *VerificationModules) {
	if m == nil || m.CampaignMedia == nil {
		return
	}
	for _, rt := range m.CampaignMedia.Routes() {
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler)
	}
}

// registerCampaignMediaConsumers subscribes the media consumer to storage.object_scanned (worker).
func registerCampaignMediaConsumers(reg *outbox.Registry, m *VerificationModules) {
	if m == nil || m.CampaignMedia == nil {
		return
	}
	for _, c := range m.CampaignMedia.Consumers() {
		reg.Subscribe(c.Name, c.EventType, c.Handle)
	}
}

// campaignMediaJobs registers the media sweep worker and returns its periodic job (worker).
func campaignMediaJobs(workers *river.Workers, m *VerificationModules) []*river.PeriodicJob {
	if m == nil || m.CampaignMedia == nil {
		return nil
	}
	m.CampaignMedia.AddWorkers(workers)
	return media.PeriodicJobs()
}
