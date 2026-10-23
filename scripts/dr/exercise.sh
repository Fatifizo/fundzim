#!/usr/bin/env bash
# Stage 18 disaster-recovery exercises against the LOCAL compose stack (docs/stage-18/disaster-recovery.md §6). Never run
# against anything but the development stack: it stops and kills containers (data volumes are never removed).
#
#   flock /tmp/claude-1000/fundzim-stack.lock scripts/dr/exercise.sh > dr-report.json
#
# Each scenario injects one failure, records what the platform reported during it (readiness, health, behaviour), restores
# the component and measures the time until the platform is ready again. Prints a JSON report.
set -euo pipefail
cd "$(dirname "$0")/../.."
set -a; . ./.env; set +a
API=http://127.0.0.1:${API_HOST_PORT:-8080}; WORKER=http://127.0.0.1:${WORKER_INTERNAL_HOST_PORT:-9091}; WEB=http://127.0.0.1:${WEB_HOST_PORT:-3000}
OUT=$(mktemp -d); trap 'rm -rf "$OUT"' EXIT
results="$OUT/r.jsonl"; : > "$results"
code() { curl -s -o /dev/null -m 5 -w '%{http_code}' "$1" || true; }
t() { date +%s.%N; }
wait_for() { # wait_for <url> <status> <timeout-s>  → seconds waited (or -1)
  local s; s=$(t)
  for _ in $(seq 1 $(( $3 * 4 ))); do [ "$(code "$1")" = "$2" ] && { python3 -c "print(round($(t)-$s,2))"; return 0; }; sleep 0.25; done
  echo -1
}
record() { python3 -c "import json,sys;print(json.dumps(dict(zip(sys.argv[1::2], sys.argv[2::2]))))" "$@" >> "$results"; }
svc() { docker compose ps -q "$1"; }

wait_for "$API/api/v1/ready" 200 120 >/dev/null; wait_for "$WORKER/readyz" 200 120 >/dev/null

# 1. application service loss: the API container is killed (SIGKILL). Docker does not apply a restart policy to a
#    container killed through its own API (that counts as a manual stop), so the exercise restarts it the way an
#    orchestrator would and measures from the restart until the API is ready again.
docker kill "$(svc fundzim-api)" >/dev/null
sleep 2
during=$(code "$API/api/v1/ready"); web_during=$(code "$WEB/healthz"); web_api_during=$(code "$WEB/api/v1/ready")
docker compose start fundzim-api >/dev/null 2>&1
rec=$(wait_for "$API/api/v1/ready" 200 120)
record scenario api_container_killed during_api_ready "$during" during_web_healthz "$web_during" during_web_proxied_api "$web_api_during" \
  recovery_seconds_after_restart "$rec" mechanism "container restart (orchestrator); Docker ignores restart policies for docker kill"

# 2. database failure: PostgreSQL stopped, then started (pools reconnect; no restart of API or worker)
docker compose stop fundzim-postgres >/dev/null 2>&1
sleep 3
during_api=$(code "$API/api/v1/ready"); during_live=$(code "$API/healthz"); during_worker=$(code "$WORKER/readyz")
during_public=$(code "$API/api/v1/public/campaigns?limit=1")
s=$(t); docker compose start fundzim-postgres >/dev/null 2>&1
rec_api=$(wait_for "$API/api/v1/ready" 200 180); rec_worker=$(wait_for "$WORKER/readyz" 200 180)
record scenario database_stopped during_api_ready "$during_api" during_api_healthz "$during_live" during_worker_readyz "$during_worker" \
  during_public_read "$during_public" api_recovery_seconds_after_start "$rec_api" worker_recovery_seconds_after_start "$rec_worker"

# 3. cache loss: Valkey stopped (cache and rate limits only; limits fall back to in-process, never open)
docker compose stop fundzim-redis >/dev/null 2>&1
sleep 2
during=$(code "$API/api/v1/ready")
wrong=$(head -c9 /dev/urandom | od -An -tx1 | tr -d ' \n')   # a random wrong value, never a real credential
login=$(curl -s -o /dev/null -w '%{http_code}' -m 5 -H 'Content-Type: application/json' \
  -d "$(printf '{"email":"nobody@example.test","%s":"wrong-%s"}' password "$wrong")" "$API/api/v1/auth/login" || true)
docker compose start fundzim-redis >/dev/null 2>&1
rec=$(wait_for "$API/api/v1/ready" 200 60)
record scenario cache_stopped during_api_ready "$during" during_login_wrong_password "$login" recovery_seconds "$rec" note "readiness ignores Valkey (REDIS_REQUIRED=false); login still answers 401"

# 4. object storage outage: Garage stopped (private documents cannot be read or written: fail closed)
docker compose stop fundzim-storage >/dev/null 2>&1
sleep 2
during=$(code "$API/api/v1/ready")
s=$(t); docker compose start fundzim-storage >/dev/null 2>&1
rec=$(wait_for "$API/api/v1/ready" 200 120)
record scenario object_storage_stopped during_api_ready "$during" recovery_seconds_after_start "$rec" note "storage buckets are critical readiness checks"

# 5. worker interruption: the worker is killed; queued work resumes after the restart (River, outbox)
backlog_before=$(docker exec "$(svc fundzim-postgres)" psql -U postgres -d fundzim -Atc "SELECT count(*) FROM app.outbox_events WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL")
docker kill "$(svc fundzim-worker)" >/dev/null
sleep 5
backlog_during=$(docker exec "$(svc fundzim-postgres)" psql -U postgres -d fundzim -Atc "SELECT count(*) FROM app.outbox_events WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL")
docker compose start fundzim-worker >/dev/null 2>&1
rec=$(wait_for "$WORKER/readyz" 200 120)
sleep 15
backlog_after=$(docker exec "$(svc fundzim-postgres)" psql -U postgres -d fundzim -Atc "SELECT count(*) FROM app.outbox_events WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL")
record scenario worker_killed outbox_undispatched_before "$backlog_before" outbox_undispatched_during "$backlog_during" \
  worker_recovery_seconds_after_restart "$rec" outbox_undispatched_15s_after "$backlog_after"

# 6. secret-manager outage: OpenBao stopped. The running API keeps serving; provider secret resolution fails closed. The
#    dev server is in-memory: after a restart its contents are gone and must be re-seeded (fundzim-secrets-init) —
#    exactly the recovery a real secret manager needs from its own backup (docs/stage-18/backup-recovery.md §6).
docker compose stop fundzim-secrets >/dev/null 2>&1
sleep 2
during=$(code "$API/api/v1/ready")
docker compose start fundzim-secrets >/dev/null 2>&1
for _ in $(seq 1 60); do [ "$(code http://127.0.0.1:${SECRETS_HOST_PORT:-8200}/v1/sys/health)" = 200 ] && break; sleep 0.5; done
lost=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Vault-Token: $SECRETS_KV2_TOKEN" http://127.0.0.1:${SECRETS_HOST_PORT:-8200}/v1/secret/data/fundzim/sandbox/api || true)
s=$(t); docker compose up --exit-code-from fundzim-secrets-init fundzim-secrets-init >/dev/null 2>&1
reseed=$(python3 -c "print(round($(t)-$s,2))")
restored=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Vault-Token: $SECRETS_KV2_TOKEN" http://127.0.0.1:${SECRETS_HOST_PORT:-8200}/v1/secret/data/fundzim/sandbox/api || true)
record scenario secret_manager_stopped during_api_ready "$during" secret_after_dev_restart_http "$lost" reseed_seconds "$reseed" secret_after_reseed_http "$restored"

# 7. corrupt deployment: a release with invalid configuration must refuse to start (never start half-configured)
bad=$(docker run --rm --network none -e APP_ENV=production fundzim/api:local >/dev/null 2>"$OUT/bad.err"; echo $?)
reason=$(grep -c 'invalid configuration' "$OUT/bad.err" || true)
record scenario invalid_configuration_release exit_code "$bad" refused_with_validation_errors "$reason"

python3 - "$results" <<'PY'
import json, sys
print(json.dumps({"environment": "local compose stack (development host)", "scenarios": [json.loads(l) for l in open(sys.argv[1])]}, indent=2))
PY
