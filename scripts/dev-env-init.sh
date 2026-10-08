#!/usr/bin/env bash
# Creates .env for LOCAL DEVELOPMENT from .env.example with freshly generated random secrets.
#
#   ./scripts/dev-env-init.sh            create .env (refuses to overwrite an existing one)
#   ./scripts/dev-env-init.sh --print    print what would be written (secrets included) — local use only
#
# Markers in .env.example:
#   <generate:password>       32 random alphanumeric characters
#   <generate:hex32>          32 random bytes, hex encoded
#   <generate:garage-key-id>  Garage access key id ("GK" + 24 hex characters)
#   ${NAME}                   value of NAME generated earlier in the same file
# Values are random per machine; they are never committed and never reused outside this machine.
set -euo pipefail
cd "$(dirname "$0")/.."

template=.env.example
target=.env
mode=write
[[ "${1:-}" == "--print" ]] && mode=print

if [[ "$mode" == write && -e "$target" ]]; then
  echo "dev-env-init: $target already exists; not overwriting it." >&2
  echo "Delete it first if you really want new local secrets (local data volumes then need resetting: make reset)." >&2
  exit 1
fi

rand_password() { # no pipe into head: avoids SIGPIPE under pipefail
  local s=""
  while (( ${#s} < 32 )); do s+="$(head -c 1024 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9')"; done
  printf '%s' "${s:0:32}"
}
rand_hex() { od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'; }

declare -A vals
out=""
while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ "$line" =~ ^([A-Z0-9_]+)=(.*)$ ]]; then
    key="${BASH_REMATCH[1]}"
    rest="${BASH_REMATCH[2]}"
    # split value and trailing comment
    value="${rest%%[[:space:]]#*}"
    comment="${rest:${#value}}"
    value="${value%"${value##*[![:space:]]}"}"
    case "$value" in
      "<generate:password>")      value="$(rand_password)" ;;
      "<generate:hex32>")         value="$(rand_hex 32)" ;;
      "<generate:garage-key-id>") value="GK$(rand_hex 12)" ;;
      "<generate:"*) echo "dev-env-init: unknown marker for $key in $template" >&2; exit 1 ;;
    esac
    while [[ "$value" =~ \$\{([A-Z0-9_]+)\} ]]; do
      ref="${BASH_REMATCH[1]}"
      [[ -n "${vals[$ref]+x}" ]] || { echo "dev-env-init: $key references undefined $ref" >&2; exit 1; }
      value="${value//\$\{$ref\}/${vals[$ref]}}"
    done
    vals[$key]="$value"
    out+="$key=$value$comment"$'\n'
  else
    out+="$line"$'\n'
  fi
done <"$template"

if [[ "$mode" == print ]]; then
  printf '%s' "$out"
else
  umask 077
  printf '%s' "$out" >"$target"
  echo "dev-env-init: wrote $target (mode 600) with new local secrets."
fi
