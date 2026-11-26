#!/bin/sh
# Anonymous, read-only smoke test against the production API (api.modrinth.com):
# a few requests that check that the commands still work and the responses still
# have the fields mdr relies on. Usage: sh scripts/drift-smoke.sh <path to mdr>
# Run by .github/workflows/drift.yml every week; never sends a token.
set -u

mdr=${1:?usage: drift-smoke.sh <path to mdr>}
unset MODRINTH_TOKEN MODRINTH_TOKEN_FILE MODRINTH_STAGING_TOKEN MODRINTH_STAGING_TOKEN_FILE
export MODRINTH_CONFIG_DIR="${TMPDIR:-/tmp}/mdr-drift-config" # no saved credentials
fail=0
err=$(mktemp)
trap 'rm -f "$err"' EXIT

# check <name> <substring the stdout must contain> <mdr arguments...>
check() {
  name=$1 want=$2
  shift 2
  out=$("$mdr" -o json "$@" 2>"$err")
  rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "FAIL $name: exit $rc: $(tail -n 1 "$err")"
    fail=1
    return
  fi
  case $out in
    *"$want"*) echo "ok   $name" ;;
    *) echo "FAIL $name: stdout lacks $want: $(printf '%.300s' "$out")"; fail=1 ;;
  esac
}

check stats '"projects":' stats --fields projects,versions
check search '"slug":"sodium"' search sodium --limit 1 --fields slug
check "project get" '{"id":"AANobbMI","slug":"sodium","project_type":"mod"}' project get sodium --fields id,slug,project_type
check "version list" '"project_id":"AANobbMI"' version list sodium --limit 1 --fields id,project_id,files.hashes.sha1
check "tag list" '{"name":"fabric"}' tag list loader --fields name
check "file get" '{"id":"ygf8cVZg","project_id":"AANobbMI"}' file get c4293c1483f3b39c1c6c4c69143e8bb2c6e53daf --fields id,project_id
check "forge_updates" '"homepage":"https://modrinth.com/mod/sodium"' api --api-version none /updates/sodium/forge_updates.json --fields homepage,promos

exit $fail
