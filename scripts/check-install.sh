#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
mkdir -p "$test_root/prefix/bin" "$test_root/unrelated/bin" "$test_root/symlink/bin"
PREFIX="$test_root/prefix" sh "$root/scripts/install.sh"
test ! -L "$test_root/prefix/bin/hycli"
PREFIX="$test_root/prefix" sh "$root/scripts/install.sh"
HOME="$test_root" "$test_root/prefix/bin/hycli" --help >/dev/null
printf '%s\n' 'unrelated command' > "$test_root/unrelated/bin/hycli"
if PREFIX="$test_root/unrelated" sh "$root/scripts/install.sh" >/dev/null 2>&1; then
  printf '%s\n' 'Installer replaced an unrelated command' >&2
  exit 1
fi
test "$(cat "$test_root/unrelated/bin/hycli")" = 'unrelated command'
ln -s "$test_root/unrelated/bin/hycli" "$test_root/symlink/bin/hycli"
if PREFIX="$test_root/symlink" sh "$root/scripts/install.sh" >/dev/null 2>&1; then
  printf '%s\n' 'Installer replaced a symlink' >&2
  exit 1
fi
test -L "$test_root/symlink/bin/hycli"
test "$(cat "$test_root/unrelated/bin/hycli")" = 'unrelated command'
test ! -e "$test_root/.sn"
printf '%s\n' 'Install: fresh installation, idempotence, executable isolation and collision protection passed'
