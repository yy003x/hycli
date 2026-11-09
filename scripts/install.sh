#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
prefix=${PREFIX:-"$HOME/.local"}
target="$prefix/bin/hycli"
source="$root/build/hycli"
test -x "$source" || {
  printf '%s\n' 'Build first: make build' >&2
  exit 1
}

if [ -L "$target" ]; then
  printf 'Refusing to replace symlink: %s\n' "$target" >&2
  exit 1
elif [ -e "$target" ]; then
  # 只替换本产品的 Go 二进制；不以通用版本输出判断文件归属。
  go version -m "$target" | awk '$1 == "path" && $2 == "github.com/yy003x/hycli/cmd/hycli" { found=1 } END { exit !found }' || {
    printf 'Refusing unrelated command: %s\n' "$target" >&2
    exit 1
  }
fi

mkdir -p "$prefix/bin"
stage=$(mktemp "$prefix/bin/.hycli-install.XXXXXX")
trap 'rm -f "$stage"' EXIT HUP INT TERM
cp "$source" "$stage"
chmod 755 "$stage"
mv -f "$stage" "$target"
printf 'Installed %s (standalone Go binary)\n' "$target"
