#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
version=${VERSION:?Set VERSION to a semantic version without v}
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || { printf '%s\n' 'Invalid release version' >&2; exit 1; }
out="$root/release/$version"
test ! -e "$out" || { printf 'Release output already exists: %s\n' "$out" >&2; exit 1; }
mkdir -p "$root/release"
stage=$(mktemp -d "$root/release/.stage.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for platform in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  os=${platform%/*}
  arch=${platform#*/}
  name="hycli-$version-$os-$arch"
  mkdir "$stage/$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X github.com/yy003x/hycli/internal/buildinfo.Version=$version" -o "$stage/$name/hycli" ./cmd/hycli
  cp README.md "$stage/$name/"
  tar -czf "$stage/$name.tar.gz" -C "$stage" "$name"
  rm -r "$stage/$name"
done
(
  cd "$stage"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum *.tar.gz >checksums.txt
  else
    shasum -a 256 *.tar.gz >checksums.txt
  fi
)
mv "$stage" "$out"
printf 'Release assets: %s\n' "$out"
