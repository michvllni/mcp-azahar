#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# check-version.sh - verify a release tag matches the version in main.go.
#
# SYNOPSIS
#   check-version.sh [-f FILE] [-p true|false] TAG
#
# DESCRIPTION
#   Reads `const version = "X.Y.Z"` from FILE and exits non-zero unless TAG
#   is exactly "v" followed by that version. With -p, also fails unless the
#   GitHub release's pre-release flag matches the version: a SemVer
#   pre-release suffix (X.Y.Z-rc.1) requires a pre-release, no suffix
#   requires a full release. Used by the release workflow before anything
#   is built or uploaded.
#
# OPTIONS
#   -f FILE        Go file holding the version constant
#                  (default: cmd/mcp-azahar/main.go)
#   -p true|false  Pre-release flag of the GitHub release (optional)
#   -h             Show this help
#
# ARGUMENTS
#   TAG      Git tag to check, e.g. v0.1.0 or refs/tags/v0.1.0
#
# EXAMPLE
#   scripts/check-version.sh -p false "$GITHUB_REF_NAME"
set -euo pipefail

file="cmd/mcp-azahar/main.go"
prerelease=""

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

while getopts ":f:p:h" opt; do
  case "$opt" in
    f) file="$OPTARG" ;;
    p) prerelease="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 1 ] || { usage >&2; exit 2; }
case "$prerelease" in
  ""|true|false) ;;
  *) usage >&2; exit 2 ;;
esac

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

if [ -n "$prerelease" ]; then
  # SemVer pre-release part: after the first "-", before any "+" metadata.
  core="${version%%+*}"
  if [ "$core" != "${core#*-}" ]; then want=true; else want=false; fi
  if [ "$prerelease" != "$want" ]; then
    echo "error: release pre-release flag is $prerelease, but version $version requires $want" >&2
    exit 1
  fi
  echo "ok: pre-release flag $prerelease matches version $version"
fi
