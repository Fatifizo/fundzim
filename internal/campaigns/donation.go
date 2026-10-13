package campaigns

import (
	"context"
	"errors"
)

// ActAcceptDonation is the Stage 8 eligibility action for starting a donation (ADR-039 §7). A campaign that is
// publicly visible is not thereby able to accept donations: this action has its own gates.
const ActAcceptDonation = "ACCEPT_DONATION"

func init() {
	messages["CAMPAIGN_NOT_ACCEPTING_DONATIONS"] = "This campaign is not accepting donations."
}

// DonationTarget is what payments needs to know about a campaign to start a donation. It carries no financial
// state (there is none in this module).
type DonationTarget struct {
	CampaignID   string
	Title        string
	Status       string
	Visibility   string
	GoalCurrency string
	OwnerUserID  *string
	OwnerOrgID   *string
}

// EvaluateDonation evaluates ACCEPT_DONATION: the campaign is ACTIVE, PUBLIC or UNLISTED, has an approved
// version, and no restriction of ANY level applies to the campaign, its owner, its organisation or its
// beneficiary (stricter than ADR-037's campaign matrix because new money is involved). Reasons never reveal a
// restriction (no tipping off): every refusal uses CAMPAIGN_NOT_ACCEPTING_DONATIONS.
func (s *Service) EvaluateDonation(ctx context.Context, campaignID string) (DonationTarget, Result, error) {
	c, err := s.loadRow(ctx, s.Pool, campaignID, false)
	if errors.Is(err, ErrNotFound) {
		return DonationTarget{}, Result{}, ErrNotFound
	}
	if err != nil {
		return DonationTarget{}, Result{}, err
	}
	t := DonationTarget{CampaignID: c.ID, Title: c.Title, Status: c.Status, Visibility: c.Visibility, GoalCurrency: c.GoalCurrency,
		OwnerUserID: c.OwnerUserID, OwnerOrgID: c.OwnerOrgID}
	e := &evalCtx{s: s, ctx: ctx}
	if c.Status != StatusActive || (c.Visibility != "PUBLIC" && c.Visibility != "UNLISTED") || c.ApprovedVersionID == nil {
		e.fail("CAMPAIGN_NOT_ACCEPTING_DONATIONS", "")
	}
	level, err := s.Restricted(ctx, c.access())
	if err != nil {
		return DonationTarget{}, Result{}, err
	}
	if level != RestrictionNone {
		e.fail("CAMPAIGN_NOT_ACCEPTING_DONATIONS", "")
	}
	r := Result{Action: ActAcceptDonation, Allowed: len(e.reasons) == 0, Reasons: e.reasons}
	if r.Reasons == nil {
		r.Reasons = []Reason{}
	}
	return t, r, nil
}
