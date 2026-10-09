package verification

import (
	"net/http"

	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// ageView is the GET/POST /me/age-attestation response: the age status plus the resulting verification level.
type ageView struct {
	kyc.AgeStatus
	Level string   `json:"level"`
	Basic []string `json:"basic_unmet,omitempty"` // POST only: BASIC_VERIFIED conditions that do not hold
}

func (s *Service) ageView(r *http.Request, userID string) (ageView, error) {
	ctx := r.Context()
	st, err := s.KYC.AgeStatus(ctx, userID)
	if err != nil {
		return ageView{}, err
	}
	lvl, err := s.KYC.Level(ctx, userID)
	if err != nil {
		return ageView{}, err
	}
	return ageView{AgeStatus: st, Level: lvl.Level}, nil
}

// GetAgeAttestation: GET /me/age-attestation (personal session).
func (s *Service) GetAgeAttestation(w http.ResponseWriter, r *http.Request) {
	v, err := s.ageView(r, principal(r).UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, v)
}

// PostAgeAttestation: POST /me/age-attestation {outcome: ATTESTED|DECLINED, statement_version, source?}
// (personal session). source is DASHBOARD (default) or CAMPAIGN_FLOW; REGISTRATION is set only by the
// registration flow. BASIC_VERIFIED is re-evaluated in the same transaction.
func (s *Service) PostAgeAttestation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Outcome          string `json:"outcome"`
		StatementVersion string `json:"statement_version"`
		Source           string `json:"source"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	switch body.Source {
	case "":
		body.Source = kyc.AgeSourceDashboard
	case kyc.AgeSourceDashboard, kyc.AgeSourceCampaignFlow:
	default:
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "source", Code: "INVALID_VALUE"}))
		return
	}
	ctx := r.Context()
	uid := principal(r).UserID
	ip := ""
	if a := authz.ClientIPFrom(ctx); a.IsValid() {
		ip = a.String()
	}
	if _, err := s.KYC.RecordAgeAttestation(ctx, uid, body.Outcome, body.Source, body.StatementVersion, ip); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.KYC.EvaluateBasic(ctx, uid) // read-only when nothing changed; returns the unmet conditions
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.ageView(r, uid)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Basic = res.Unmet
	noStore(w)
	httpx.WriteData(w, r, http.StatusCreated, v)
}
