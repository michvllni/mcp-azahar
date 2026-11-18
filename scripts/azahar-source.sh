#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# azahar-source.sh - fetch Azahar's source tarball and license for a release.
#
# SYNOPSIS
#   scripts/azahar-source.sh [-o DIR] [-l] [-h]
#
# DESCRIPTION
#   Downloads azahar-unified-source-<version>.tar.xz of the Azahar version
#   pinned in .devcontainer/Dockerfile (AZAHAR_VERSION), verifies it against
#   AZAHAR_SOURCE_SHA256 there, and extracts its LICENSE.txt as
#   azahar-LICENSE.txt. The container image ships Azahar's binary, so each
#   release attaches this tarball as the corresponding source (D10).
#   Prints the tarball path.
#
# OPTIONS
#   -o DIR  Output directory (default: .release/azahar-source)
#   -l      Keep only azahar-LICENSE.txt, delete the tarball (CI builds)
#   -h      Show this help
#
# EXAMPLE
#   scripts/azahar-source.sh
set -euo pipefail

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/.release/azahar-source"
license_only=false
while getopts ":o:lh" opt; do
  case "$opt" in
    o) out="$OPTARG" ;;
    l) license_only=true ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

# arg NAME: print the value of `ARG NAME=...` in the dev container Dockerfile.
arg() {
  sed -n "s/^ARG $1=//p" "$root/.devcontainer/Dockerfile"
}

version="$(arg AZAHAR_VERSION)"
sha="$(arg AZAHAR_SOURCE_SHA256)"
if [ -z "$version" ] || [ -z "$sha" ]; then
  echo "AZAHAR_VERSION or AZAHAR_SOURCE_SHA256 missing in the Dockerfile" >&2
  exit 1
fi

mkdir -p "$out"
tarball="$out/azahar-unified-source-$version.tar.xz"
curl -fsSL --retry 3 -o "$tarball" "https://github.com/azahar-emu/azahar/releases/download/$version/azahar-unified-source-$version.tar.xz"
echo "$sha  $tarball" | sha256sum -c - >&2
tar -xJf "$tarball" -O "azahar-unified-source-$version/LICENSE.txt" > "$out/azahar-LICENSE.txt"
if $license_only; then
  rm -f "$tarball"
  echo "$out/azahar-LICENSE.txt"
else
  echo "$tarball"
fi
