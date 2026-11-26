#!/bin/sh
# Packages the binaries of `make dist` as release archives in dist/release/:
#   mdr_<version>_<os>_<arch>.tar.gz (linux amd64/arm64, darwin arm64)
#   mdr_<version>_windows_amd64.zip
#   SHA256SUMS
# Each archive holds the binary (mdr or mdr.exe), LICENSE, README.md and
# skills/modrinth-cli/SKILL.md. Runs in the dev container: make package VERSION=vX.Y.Z
#
# usage: sh scripts/package.sh <version>   (without the leading "v", e.g. 0.1.0)
#
# SOURCE_DATE_EPOCH (seconds) sets the time stamps in the archives, so the same
# binaries give the same archives; it defaults to 0.
set -eu

version=${1:-}
case $version in
  '' | v* | *[!0-9A-Za-z.+-]*)
    echo "usage: sh scripts/package.sh <version>  (e.g. 0.1.0, without the leading v)" >&2
    exit 2
    ;;
esac

export TZ=UTC LC_ALL=C
epoch=${SOURCE_DATE_EPOCH:-0}
targets="linux_amd64 linux_arm64 darwin_arm64 windows_amd64"
out=dist/release

for t in $targets; do
  bin=dist/mdr_$t
  [ "$t" = windows_amd64 ] && bin=$bin.exe
  if [ ! -f "$bin" ]; then
    echo "package: $bin is missing; run make dist VERSION=v$version first" >&2
    exit 1
  fi
done

# The linux/amd64 binary can run here: make sure dist/ was built for this version.
got=$(./dist/mdr_linux_amd64 --version)
if [ "$got" != "mdr v$version" ]; then
  echo "package: dist/mdr_linux_amd64 --version prints \"$got\", want \"mdr v$version\"" >&2
  echo "package: run make dist VERSION=v$version first" >&2
  exit 1
fi

mkdir -p "$out"
rm -f "$out"/mdr_*.tar.gz "$out"/mdr_*.zip "$out"/SHA256SUMS
stage=$(mktemp -d "$out/stage.XXXXXX")

for t in $targets; do
  name=mdr_${version}_$t
  exe=mdr bin=dist/mdr_$t
  if [ "$t" = windows_amd64 ]; then
    exe=mdr.exe bin=$bin.exe
  fi
  mkdir -p "$stage/skills/modrinth-cli"
  install -m 0755 "$bin" "$stage/$exe"
  install -m 0644 LICENSE README.md "$stage/"
  install -m 0644 skills/modrinth-cli/SKILL.md "$stage/skills/modrinth-cli/"
  files="$exe LICENSE README.md skills/modrinth-cli/SKILL.md"
  for f in $files; do
    touch -d "@$epoch" "$stage/$f"
  done
  # $files is a fixed list of names without spaces: it is split on purpose.
  if [ "$t" = windows_amd64 ]; then
    # -X: no extra attributes (uid/gid, extended time stamps); -D: no directory entries.
    # shellcheck disable=SC2086
    (cd "$stage" && zip -q -X -D "../$name.zip" $files)
  else
    # No directory entries either: tar lists only the files.
    # shellcheck disable=SC2086
    tar -C "$stage" --format=ustar --owner=0 --group=0 --numeric-owner --mtime="@$epoch" \
      -cf - $files | gzip -n -9 > "$out/$name.tar.gz"
  fi
  rm -f "$stage/$exe" "$stage/LICENSE" "$stage/README.md" "$stage/skills/modrinth-cli/SKILL.md"
done
rmdir "$stage/skills/modrinth-cli" "$stage/skills" "$stage"

(cd "$out" && sha256sum mdr_*.tar.gz mdr_*.zip > SHA256SUMS)
echo "package: wrote $out:"
(cd "$out" && cat SHA256SUMS)
