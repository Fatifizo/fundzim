package kyc

import "context"

// RepresentativeHas reports whether userID currently holds a representative authority for the organisation
// that includes permission (e.g. org.campaign.submit), written on KYB approval (kyb-architecture.md). The
// authority must be within its validity window and not revoked; the organisation's own KYB level is checked
// separately (OrganisationLevel).
func (s *Service) RepresentativeHas(ctx context.Context, orgID, userID, permission string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kyc.representative_authorities ra
		JOIN kyc.kyb_organisations o ON o.id = ra.kyb_organisation_id
		WHERE o.organisation_id = $1 AND ra.user_id = $2 AND $3 = ANY (ra.permissions) AND ra.revoked_at IS NULL
		  AND ra.valid_from <= $4 AND ra.valid_to > $4)`, orgID, userID, permission, s.now()).Scan(&ok)
	return ok, err
}
