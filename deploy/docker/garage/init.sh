#!/bin/sh
# LOCAL DEVELOPMENT ONLY. Initialises the single-node Garage cluster through its admin API (v2):
#   1. assign and apply the node layout;
#   2. import the three scoped access keys (design-baseline §12 I-19, I-26);
#   3. create three buckets and grant each key read/write on exactly ONE bucket.
# Idempotent: re-running after the first success changes nothing. Never point this at a shared Garage.
set -eu
A="${GARAGE_ADMIN_URL:-http://fundzim-storage:3903}"
AUTH="Authorization: Bearer ${GARAGE_ADMIN_TOKEN:?}"
# Responses are pretty-printed JSON; compact() strips whitespace so the greps below are layout-independent.
api() { curl -fsS -H "$AUTH" -H 'Content-Type: application/json' "$@"; }
compact() { tr -d ' \n\r\t'; }

echo "waiting for Garage admin API"
i=0; until curl -fsS "$A/health" >/dev/null 2>&1 || api "$A/v2/GetClusterStatus" >/dev/null 2>&1; do
  i=$((i+1)); [ "$i" -gt 60 ] && { echo "Garage admin API not reachable"; exit 1; }; sleep 1; done

status=$(api "$A/v2/GetClusterStatus")
node=$(echo "$status" | compact | grep -o '"id":"[0-9a-f]*"' | head -1 | cut -d'"' -f4)
[ -n "$node" ] || { echo "no node id"; exit 1; }
ver=$(api "$A/v2/GetClusterLayout" | compact | grep -o '"version":[0-9]*' | head -1 | cut -d: -f2)
if [ "${ver:-0}" = "0" ]; then
  echo "assigning the single-node layout"
  api -X POST "$A/v2/UpdateClusterLayout" -d "{\"roles\":[{\"id\":\"$node\",\"zone\":\"local\",\"capacity\":10000000000,\"tags\":[\"dev\"]}]}" >/dev/null
  api -X POST "$A/v2/ApplyClusterLayout" -d '{"version":1}' >/dev/null
fi

import_key() { # name id secret
  if api "$A/v2/GetKeyInfo?id=$2" >/dev/null 2>&1; then echo "key $1 exists"; return; fi
  api -X POST "$A/v2/ImportKey" -d "{\"name\":\"$1\",\"accessKeyId\":\"$2\",\"secretAccessKey\":\"$3\"}" >/dev/null
  echo "imported key $1"
}
bucket_id() { # alias
  api "$A/v2/GetBucketInfo?globalAlias=$1" 2>/dev/null | compact | grep -o '"id":"[0-9a-f]*"' | head -1 | cut -d'"' -f4
}
ensure_bucket() { # alias key_id
  id=$(bucket_id "$1" || true)
  if [ -z "$id" ]; then
    api -X POST "$A/v2/CreateBucket" -d "{\"globalAlias\":\"$1\"}" >/dev/null
    id=$(bucket_id "$1")
    echo "created bucket $1"
  fi
  api -X POST "$A/v2/AllowBucketKey" -d "{\"bucketId\":\"$id\",\"accessKeyId\":\"$2\",\"permissions\":{\"read\":true,\"write\":true,\"owner\":false}}" >/dev/null
  echo "granted $1 to its key"
}

import_key fundzim-public-media     "${STORAGE_PUBLIC_ACCESS_KEY_ID:?}"   "${STORAGE_PUBLIC_SECRET_ACCESS_KEY:?}"
import_key fundzim-private-kyc      "${STORAGE_KYC_ACCESS_KEY_ID:?}"      "${STORAGE_KYC_SECRET_ACCESS_KEY:?}"
import_key fundzim-private-evidence "${STORAGE_EVIDENCE_ACCESS_KEY_ID:?}" "${STORAGE_EVIDENCE_SECRET_ACCESS_KEY:?}"
ensure_bucket "${STORAGE_PUBLIC_BUCKET:?}"   "$STORAGE_PUBLIC_ACCESS_KEY_ID"
ensure_bucket "${STORAGE_KYC_BUCKET:?}"      "$STORAGE_KYC_ACCESS_KEY_ID"
ensure_bucket "${STORAGE_EVIDENCE_BUCKET:?}" "$STORAGE_EVIDENCE_ACCESS_KEY_ID"
echo "garage init complete"
