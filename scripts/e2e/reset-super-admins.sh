#!/usr/bin/env bash
# TEST ENVIRONMENTS ONLY. Revokes every active SUPER_ADMIN assignment so the one-time bootstrap ceremony
# (`fundzimctl bootstrap-admins`) can run again on a shared development database. This is exactly what the Go
# integration helper resetSuperAdmins (tests/integration/identity_test.go) does: revocation is the only
# mutation app.role_assignments allows, and the revocation is attributed to the system actor with a reason.
# Allowed by docs/stage-7/interface-contracts.md §6 as a test-environment fixture.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
e2e_require_test_env
e2e_psql <<'SQL'
UPDATE app.role_assignments
   SET revoked_at = now(), revoked_by = '00000000-0000-0000-0000-000000000001', revoke_reason = 'real-stack e2e reset'
 WHERE revoked_at IS NULL AND role_id = md5('role:SUPER_ADMIN')::uuid;
SQL
echo "active SUPER_ADMIN assignments revoked (test environment)"
