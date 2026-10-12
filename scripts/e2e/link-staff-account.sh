#!/usr/bin/env bash
# TEST ENVIRONMENTS ONLY — fallback fixture, not used by default. Links a staff account to a personal account
# directly in the database, as the Go integration tests do (TestCampaignSelfReviewFourEyesAndPrivacy):
#   scripts/e2e/link-staff-account.sh <staff-user-id> <personal-user-id>
# The suite normally creates the link through the real ceremony (staff UI request + Mailpit token confirmed by
# the personal account); this script exists for debugging when that ceremony is unavailable.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
e2e_require_test_env
[[ $# -eq 2 ]] || { echo "usage: $0 <staff-user-id> <personal-user-id>" >&2; exit 2; }
[[ "$1$2" =~ ^[0-9a-f-]{72}$ ]] || { echo "ids must be UUIDs" >&2; exit 2; }
e2e_psql <<SQL
UPDATE app.users SET staff_personal_user_id = '$2' WHERE id = '$1' AND staff_personal_user_id IS NULL;
SQL
echo "staff account linked (test environment)"
