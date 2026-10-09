package campaigns

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// PolicyRules is the campaign policy (app.campaign_policies.rules). All values are INTERNAL_POLICY
// configuration, never legal requirements; decisions record the version they used.
type PolicyRules struct {
	Gates struct {
		CreateDraftMinLevel          string `json:"create_draft_min_level"`
		SubmitMinLevel               string `json:"submit_min_level"`
		OrganisationMinLevel         string `json:"organisation_min_level"`
		OrganisationSubmitPermission string `json:"organisation_submit_permission"`
	} `json:"gates"`
	Content struct {
		Title       [2]int `json:"title"`
		Summary     [2]int `json:"summary"`
		Story       [2]int `json:"story"`
		UpdateTitle [2]int `json:"update_title"`
		UpdateBody  [2]int `json:"update_body"`
	} `json:"content"`
	Goals map[string]struct {
		MinMinor int64 `json:"min_minor"`
		MaxMinor int64 `json:"max_minor"`
	} `json:"goals"`
	MaxResubmissions  int  `json:"max_resubmissions"`
	OwnerPauseAllowed bool `json:"owner_pause_allowed"`
	RequireCoverImage bool `json:"require_cover_image"`
	MaxGalleryImages  int  `json:"max_gallery_images"`
	MaterialEdit      struct {
		GoalIncreaseRatioPercent int64 `json:"goal_increase_ratio_percent"`
	} `json:"material_edit"`
	Tiers map[string]struct {
		SecondApproval bool `json:"second_approval"`
	} `json:"tiers"`
	Beneficiary struct {
		MinorDisclosure                     string `json:"minor_disclosure"`
		ThirdPartyDisclosureRequiresConsent bool   `json:"third_party_disclosure_requires_consent"`
	} `json:"beneficiary"`
	Screening struct {
		Provider *string `json:"provider"`
		Status   string  `json:"status"`
	} `json:"screening"`
}

// Policy is an approved policy version.
type Policy struct {
	Version string
	Rules   PolicyRules
}

var (
	policyMu    sync.Mutex
	policyCache *Policy
	policyAt    time.Time
)

// ActivePolicy returns the APPROVED campaign policy (cached for 30 seconds).
func (s *Service) ActivePolicy(ctx context.Context) (Policy, error) {
	policyMu.Lock()
	defer policyMu.Unlock()
	if policyCache != nil && time.Since(policyAt) < 30*time.Second {
		return *policyCache, nil
	}
	var p Policy
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT policy_version, rules FROM app.campaign_policies WHERE status = 'APPROVED'`).Scan(&p.Version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, errors.New("campaigns: no approved campaign policy")
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p.Rules); err != nil {
		return p, err
	}
	policyCache, policyAt = &p, time.Now()
	return p, nil
}

// SecondApproval reports whether the tier needs four eyes.
func (p Policy) SecondApproval(tier string) bool { return p.Rules.Tiers[tier].SecondApproval }
