#!/usr/bin/env bash
# TEST ENVIRONMENTS ONLY. Runs the real super-admin bootstrap ceremony for the real-stack E2E suite:
#   scripts/e2e/bootstrap-admins.sh <admin-a-email> <admin-a-name> <admin-b-email> <admin-b-name>
# It revokes active SUPER_ADMINs first (reset-super-admins.sh), then runs
#   go run ./apps/api/cmd/fundzimctl bootstrap-admins -admin-a-email … -justification …
# with the repository .env loaded. Both admins receive invitation emails (worker → Mailpit).
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
e2e_require_test_env
[[ $# -eq 4 ]] || { echo "usage: $0 <admin-a-email> <admin-a-name> <admin-b-email> <admin-b-name>" >&2; exit 2; }
"$E2E_ROOT/scripts/e2e/reset-super-admins.sh"
export PATH="$HOME/.local/go/bin:$PATH"
set -a; . "$E2E_ROOT/.env"; set +a
exec go run ./apps/api/cmd/fundzimctl bootstrap-admins \
  -admin-a-email "$1" -admin-a-name "$2" -admin-b-email "$3" -admin-b-name "$4" \
  -justification "real-stack E2E bootstrap ceremony (test environment)"
