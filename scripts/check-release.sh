#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
version=${VERSION:?Set VERSION}
out="$root/release/$version"
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
(
  cd "$out"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c checksums.txt; else shasum -a 256 -c checksums.txt; fi
)
test "$(find "$out" -name '*.tar.gz' | wc -l | tr -d ' ')" = 4
os=$(go env GOOS)
arch=$(go env GOARCH)
name="hycli-$version-$os-$arch"
tar -xzf "$out/$name.tar.gz" -C "$test_root"
HOME="$test_root" "$test_root/$name/hycli" --version | grep -F "$version"
HOME="$test_root" "$test_root/$name/hycli" --help >/dev/null
printf '%s\n' 'Release checksums, four targets and native executable passed'
