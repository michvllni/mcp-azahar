#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# npm-packages.sh - build and publish the npm packages of a release (D10).
#
# SYNOPSIS
#   scripts/npm-packages.sh -v VERSION [-d DIST] [-o OUT] [-p] [-t TAG] [-n] [-h]
#
# DESCRIPTION
#   Builds one package per platform binary goreleaser produced
#   (mcp-azahar-<linux|darwin|win32>-<x64|arm64>, read from
#   DIST/artifacts.json) and the wrapper mcp-azahar from
#   packaging/npm/mcp-azahar, all at VERSION, into OUT. With -p it publishes
#   them, platform packages first, skipping versions that already exist, so
#   a failed release job can be re-run. Publishing uses npm trusted
#   publishing (OIDC, provenance) in the release workflow; see
#   scripts/npm-bootstrap.sh for the one-time setup.
#
# OPTIONS
#   -v VERSION  Package version (SemVer, without "v"; required)
#   -d DIST     goreleaser output directory (default: dist)
#   -o OUT      Output directory (default: DIST/npm)
#   -p          Publish the packages after building them
#   -t TAG      npm dist-tag for -p (default: latest, or next for versions
#               with a pre-release suffix; a pre-release also becomes
#               latest while a package has no real release yet)
#   -n          With -p: npm publish --dry-run
#   -h          Show this help
#
# EXAMPLE
#   goreleaser build --snapshot --clean && scripts/npm-packages.sh -v 0.1.0 -p -n
set -euo pipefail

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

root="$(cd "$(dirname "$0")/.." && pwd)"
version="" dist="dist" out="" publish=false tag="" explicit_tag=false dry=()
while getopts ":v:d:o:pt:nh" opt; do
  case "$opt" in
    v) version="$OPTARG" ;;
    d) dist="$OPTARG" ;;
    o) out="$OPTARG" ;;
    p) publish=true ;;
    t) tag="$OPTARG"; explicit_tag=true ;;
    n) dry=(--dry-run) ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
if [ $# -ne 0 ] || [ -z "$version" ]; then
  usage >&2
  exit 2
fi
out="${out:-$dist/npm}"
if [ -z "$tag" ]; then
  case "$version" in
    *-*) tag=next ;;
    *) tag=latest ;;
  esac
fi

# platform_package GOOS GOARCH BINARY: write the package for one binary to
# $out and print its name.
platform_package() {
  local os cpu name dir exe
  case "$1" in windows) os=win32 ;; *) os="$1" ;; esac
  case "$2" in amd64) cpu=x64 ;; *) cpu="$2" ;; esac
  name="mcp-azahar-$os-$cpu"
  dir="$out/$name"
  exe="$(basename "$3")"
  mkdir -p "$dir/bin"
  cp "$3" "$dir/bin/$exe"
  chmod 0755 "$dir/bin/$exe"
  cp "$root/LICENSE" "$dir/LICENSE"
  jq -n --arg name "$name" --arg version "$version" --arg os "$os" --arg cpu "$cpu" '{
    name: $name, version: $version,
    description: ("mcp-azahar binary for " + $os + "/" + $cpu + "; install mcp-azahar instead"),
    homepage: "https://github.com/michvllni/mcp-azahar#readme",
    repository: {type: "git", url: "git+https://github.com/michvllni/mcp-azahar.git"},
    license: "GPL-3.0-or-later",
    os: [$os], cpu: [$cpu],
    preferUnplugged: true,
    files: ["bin/", "LICENSE"]
  }' > "$dir/package.json"
  printf 'mcp-azahar binary for %s/%s. Install the package mcp-azahar instead: https://www.npmjs.com/package/mcp-azahar\n' "$os" "$cpu" > "$dir/README.md"
  echo "$name"
}

rm -rf "$out"
mkdir -p "$out"
platforms=()
while read -r goos goarch path; do
  platforms+=("$(platform_package "$goos" "$goarch" "$path")")
done < <(jq -r '.[] | select(.type == "Binary") | "\(.goos) \(.goarch) \(.path)"' "$dist/artifacts.json")
if [ "${#platforms[@]}" -ne 6 ]; then
  echo "expected 6 binaries in $dist/artifacts.json, found ${#platforms[@]}" >&2
  exit 1
fi

wrapper="$out/mcp-azahar"
cp -R "$root/packaging/npm/mcp-azahar" "$wrapper"
cp "$root/LICENSE" "$wrapper/LICENSE"
jq --arg version "$version" '.version = $version
  | .optionalDependencies |= with_entries(.value = $version)' \
  "$root/packaging/npm/mcp-azahar/package.json" > "$wrapper/package.json"
# The wrapper must list exactly the platform packages built here.
if [ "$(jq -r '.optionalDependencies | keys | sort | join(" ")' "$wrapper/package.json")" != \
     "$(printf '%s\n' "${platforms[@]}" | sort | paste -sd' ')" ]; then
  echo "optionalDependencies of the wrapper do not match the built platform packages" >&2
  exit 1
fi
echo "built ${platforms[*]} mcp-azahar $version in $out" >&2

# published NAME: succeed if $version of NAME is on npm, fail if not (E404),
# exit the script on any other error (network, registry).
published() {
  local outp
  if outp="$(npm view "$1@$version" version 2>&1)"; then
    [ "$outp" = "$version" ]
    return
  fi
  if grep -q E404 <<<"$outp"; then
    return 1
  fi
  echo "npm view $1@$version failed: $outp" >&2
  exit 1
}

# tag_for NAME: the dist-tag to publish NAME with. A pre-release also gets
# "latest" while the package has no real release yet (only the bootstrap
# placeholder), so `npx mcp-azahar` always finds a working version.
tag_for() {
  local latest
  if [ "$tag" != next ] || $explicit_tag; then
    echo "$tag"
    return
  fi
  latest="$(npm view "$1" dist-tags.latest 2>/dev/null || true)"
  case "$latest" in
    "" | 0.0.0-bootstrap) echo latest ;;
    *) echo "$tag" ;;
  esac
}

$publish || exit 0
for name in "${platforms[@]}" mcp-azahar; do
  if published "$name"; then
    echo "$name@$version is already published, skipped" >&2
    continue
  fi
  (cd "$out/$name" && npm publish --access public --tag "$(tag_for "$name")" "${dry[@]}")
done
