#!/usr/bin/env bash
# Shared helpers for the real-stack E2E fixture scripts (scripts/e2e/*). Test environments only.
set -euo pipefail

E2E_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$E2E_ROOT"

# Refuse anything but a local/CI development or test stack: these scripts change staff role assignments.
e2e_require_test_env() {
  local env_file="$E2E_ROOT/.env"
  [[ -f "$env_file" ]] || { echo "scripts/e2e: $env_file not found (run ./scripts/dev-env-init.sh)" >&2; exit 2; }
  local app_env
  app_env="$(set -a; . "$env_file"; set +a; printf '%s' "${APP_ENV:-}")"
  case "$app_env" in
    development|test) ;;
    *) echo "scripts/e2e: refusing to run with APP_ENV=$app_env (development or test only)" >&2; exit 2 ;;
  esac
}

# docker compose, through `sg docker` when the current shell is not yet in the docker group.
e2e_compose() {
  if docker info >/dev/null 2>&1; then
    docker compose "$@"
  else
    local quoted
    quoted="$(printf '%q ' "$@")"
    sg docker -c "docker compose $quoted"
  fi
}

# Runs SQL from stdin in the compose PostgreSQL as the superuser (single transaction, stop on error).
e2e_psql() {
  e2e_compose exec -T fundzim-postgres psql -U postgres -d fundzim -v ON_ERROR_STOP=1 -X -q -At --single-transaction "$@"
}
