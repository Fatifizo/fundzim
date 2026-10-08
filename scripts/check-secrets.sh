#!/usr/bin/env bash
# Lightweight local secret scan for FundZim.
#
# This is a safety net, NOT a replacement for gitleaks (which also scans git history
# and is required in CI). It scans files that git would track (tracked + untracked,
# respecting .gitignore) for common credential patterns and forbidden files.
#
# Exit status: 0 = nothing found, 1 = possible secret found, 2 = usage/environment error.
set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "must run inside the git repository" >&2; exit 2; }

mapfile -d '' FILES < <(git ls-files -z --cached --others --exclude-standard)
[[ ${#FILES[@]} -gt 0 ]] || { echo "no files to scan"; exit 0; }

found=0
report() { echo "POSSIBLE SECRET: $1"; found=1; }

# 1. Files that must never be committed.
for f in "${FILES[@]}"; do
  case "$f" in
    .env.example) ;;
    .env|.env.*|*/.env|*/.env.*) report "$f (environment file must not be tracked)" ;;
    *.pem|*.key|*.p12|*.pfx|*.jks|*id_rsa*|*id_ed25519*) report "$f (key material file)" ;;
  esac
done

# 2. Content patterns. Kept specific to limit false positives.
PATTERNS=(
  '-----BEGIN ([A-Z]+ )?PRIVATE KEY-----'
  'AKIA[0-9A-Z]{16}'                                   # AWS access key id
  'ASIA[0-9A-Z]{16}'                                   # AWS temporary key id
  'gh[pousr]_[A-Za-z0-9]{36,}'                         # GitHub tokens
  'github_pat_[A-Za-z0-9_]{50,}'
  'xox[baprs]-[A-Za-z0-9-]{10,}'                       # Slack tokens
  'sk_live_[A-Za-z0-9]{16,}'                           # live payment-provider style keys
  'rk_live_[A-Za-z0-9]{16,}'
  'sk-ant-[A-Za-z0-9_-]{20,}'                          # Anthropic API keys
  'AIza[0-9A-Za-z_-]{35}'                              # Google API keys
  'eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}'  # JWTs
  '[a-z]+://[^:/?#[:space:]"]+:[^@/[:space:]"<]{6,}@'  # credentials embedded in URLs
  '(password|passwd|secret|api_key|apikey|access_key|private_key|client_secret|webhook_secret)["'"'"']?[[:space:]]*[:=][[:space:]]*["'"'"'][^"'"'"'<$[:space:]{}]{8,}["'"'"']'
)

for p in "${PATTERNS[@]}"; do
  # -I skips binary files; lockfiles are excluded (integrity hashes match nothing above but are huge).
  while IFS= read -r hit; do
    report "$hit"
  done < <(printf '%s\0' "${FILES[@]}" | grep -zv -e 'package-lock.json$' -e '^scripts/check-secrets.sh$' \
           | xargs -0 -r grep -InEi -e "$p" -- 2>/dev/null | cut -c1-200 || true)
done

if [[ $found -ne 0 ]]; then
  echo
  echo "Possible secrets found. Remove them, rotate any real credential, and re-run."
  echo "If a match is a documented placeholder or false positive, change the text so it is"
  echo "unambiguous (e.g. <set-locally>) rather than weakening this script."
  exit 1
fi

echo "check-secrets: no secrets found in ${#FILES[@]} files (pattern scan; run gitleaks for history)."
