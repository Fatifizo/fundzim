# Source after .env: points every database URL at the scratch database fundzim_perf (same roles, same passwords).
#   set -a; . ./.env; . tests/perf/env.sh; set +a
for v in DATABASE_URL DATABASE_MIGRATION_URL DATABASE_WORKER_URL DATABASE_KYC_URL DATABASE_COMPLIANCE_URL; do
  eval "cur=\${$v:-}"
  [ -n "$cur" ] && eval "$v=\$(printf '%s' \"\$cur\" | sed 's#/fundzim?#/fundzim_perf?#')"
done
unset cur v
