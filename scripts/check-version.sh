#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# check-version.sh - verify a release tag matches the version in main.go.
#
# SYNOPSIS
#   check-version.sh [-f FILE] TAG
#
# DESCRIPTION
#   Reads `const version = "X.Y.Z"` from FILE and exits non-zero unless TAG
#   is exactly "v" followed by that version. Used by the release workflow
#   before anything is built or published.
#
# OPTIONS
#   -f FILE  Go file holding the version constant
#            (default: cmd/mcp-azahar/main.go)
#   -h       Show this help
#
# ARGUMENTS
#   TAG      Git tag to check, e.g. v0.1.0 or refs/tags/v0.1.0
#
# EXAMPLE
#   scripts/check-version.sh "$GITHUB_REF_NAME"
set -euo pipefail

file="cmd/mcp-azahar/main.go"

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

while getopts ":f:h" opt; do
  case "$opt" in
    f) file="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 1 ] || { usage >&2; exit 2; }

tag="${1#refs/tags/}"
count="$(grep -cE '^const version = "[^"]+"$' "$file" || true)"
if [ "$count" != "1" ]; then
  echo "error: expected exactly one 'const version = \"...\"' line in $file, found $count" >&2
  exit 1
fi
version="$(sed -nE 's/^const version = "([^"]+)"$/\1/p' "$file")"

if [ "$tag" != "v$version" ]; then
  echo "error: tag $tag does not match version $version in $file (expected tag v$version)" >&2
  exit 1
fi
echo "ok: tag $tag matches version $version in $file"
