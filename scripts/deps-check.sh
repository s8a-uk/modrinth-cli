#!/bin/sh
# Fails if the module has any third-party dependency. The project uses the Go standard library only.
set -eu

fail=0

if grep -Eq '^[[:space:]]*(require|replace|tool|toolchain)\b' go.mod; then
  echo "deps-check: go.mod must not contain require/replace/tool/toolchain directives" >&2
  fail=1
fi

if [ -e go.sum ]; then
  echo "deps-check: go.sum must not exist" >&2
  fail=1
fi

# With GOPROXY=off, `go list` fails outright if go.mod names a module that is not cached.
if ! mods=$(go list -m all 2>&1); then
  echo "deps-check: go list -m all failed:" >&2
  echo "$mods" >&2
  fail=1
elif [ "$(printf '%s\n' "$mods" | wc -l | tr -d ' ')" != "1" ]; then
  echo "deps-check: expected exactly 1 module, found:" >&2
  echo "$mods" >&2
  fail=1
fi

# Every imported package outside this module must be in the standard library.
if ! deps=$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>&1); then
  echo "deps-check: go list -deps failed:" >&2
  echo "$deps" >&2
  fail=1
else
  nonstd=$(printf '%s\n' "$deps" | grep -v '^github.com/s8a-uk/modrinth-cli' | grep -v '^$' || true)
  if [ -n "$nonstd" ]; then
    echo "deps-check: non-standard-library imports found:" >&2
    echo "$nonstd" >&2
    fail=1
  fi
fi

if [ "$fail" = 0 ]; then
  echo "deps-check: ok (standard library only)"
fi
exit "$fail"
