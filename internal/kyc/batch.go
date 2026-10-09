package kyc

import "context"

// Levels is Level for many users in one query (public listings). Users without a profile get
// UNVERIFIED/ACTIVE; expired levels count as UNVERIFIED, exactly as Level.
func (s *Service) Levels(ctx context.Context, userIDs []string) (map[string]Status, error) {
	return s.levels(ctx, `SELECT user_id::text, level, status, risk_level, level_expires_at FROM kyc.verification_profiles WHERE user_id = ANY ($1::uuid[])`,
		userIDs, LevelUnverified)
}

// OrganisationLevels is OrganisationLevel for many organisations in one query.
func (s *Service) OrganisationLevels(ctx context.Context, orgIDs []string) (map[string]Status, error) {
	return s.levels(ctx, `SELECT organisation_id::text, level, status, risk_level, level_expires_at FROM kyc.kyb_organisations
		WHERE organisation_id = ANY ($1::uuid[])`, orgIDs, OrgLevelUnverified)
}

func (s *Service) levels(ctx context.Context, sql string, keys []string, unverified string) (map[string]Status, error) {
	out := make(map[string]Status, len(keys))
	for _, k := range keys {
		out[k] = Status{Level: unverified, Status: ProfileActive, RiskLevel: RiskStandard}
	}
	if len(keys) == 0 {
		return out, nil
	}
	now := s.now()
	rows, err := s.Pool.Query(ctx, sql, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var st Status
		if err := rows.Scan(&k, &st.Level, &st.Status, &st.RiskLevel, &st.ExpiresAt); err != nil {
			return nil, err
		}
		if st.ExpiresAt != nil && !st.ExpiresAt.After(now) {
			st.Level = unverified
		}
		out[k] = st
	}
	return out, rows.Err()
}
