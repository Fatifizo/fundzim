package app

import (
	"github.com/Fatifizo/fundzim/internal/campaigns/updates"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Stage 6 stream U: campaign updates wiring (internal/campaigns/updates). The updates service is built next to the
// campaigns core and the media service; it serves owner, staff-moderation and public routes in the API process.

// CampaignUpdatesService is the type of VerificationModules.CampaignUpdates (an alias, so verification.go needs no
// extra import).
type CampaignUpdatesService = updates.Service

// newCampaignUpdates builds the updates service on the campaigns core. Without the core it is not built (nil); without
// the media service every media reference is refused (fail closed).
func newCampaignUpdates(m *VerificationModules, d VerificationDeps) *updates.Service {
	if m.Campaigns == nil {
		return nil
	}
	deps := updates.Deps{Pool: d.AppPool, Core: m.Campaigns, Orgs: d.Orgs, Clock: d.Clock, Logger: d.Logger}
	if m.CampaignMedia != nil {
		deps.Media = m.CampaignMedia
	}
	return updates.New(deps)
}

// registerCampaignUpdateRoutes registers the campaign updates API (each route declares its own policy).
func registerCampaignUpdateRoutes(r *httpx.Router, m *VerificationModules) {
	if m == nil || m.CampaignUpdates == nil {
		return
	}
	for _, rt := range m.CampaignUpdates.Routes() {
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler)
	}
}
