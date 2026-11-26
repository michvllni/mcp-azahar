#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# install-mcp-publisher.sh - install the MCP Registry's mcp-publisher, verified.
#
# SYNOPSIS
#   scripts/install-mcp-publisher.sh [-d DIR] [-h]
#
# DESCRIPTION
#   Downloads the pinned mcp-publisher release archive for this OS/arch from
#   github.com/modelcontextprotocol/registry, verifies its Sigstore bundle
#   with cosign (must be on PATH) against the registry's release workflow
#   for that tag, and installs the binary into DIR. Used by CI (validate)
#   and release.yml (publish), as the registry's GitHub Actions docs advise.
#
# OPTIONS
#   -d DIR  Install directory (default: ./bin)
#   -h      Show this help
#
# EXAMPLE
#   scripts/install-mcp-publisher.sh -d "$RUNNER_TEMP/bin"
set -euo pipefail

# renovate: datasource=github-releases depName=modelcontextprotocol/registry
version="${MCP_PUBLISHER_VERSION:-v1.8.16}"

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

dir="./bin"
while getopts ":d:h" opt; do
  case "$opt" in
    d) dir="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
archive="mcp-publisher_${os}_${arch}.tar.gz"
base="https://github.com/modelcontextprotocol/registry/releases/download/$version"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 -o "$tmp/$archive" "$base/$archive"
curl -fsSL --retry 3 -o "$tmp/$archive.sigstore.json" "$base/$archive.sigstore.json"
cosign verify-blob \
  --bundle "$tmp/$archive.sigstore.json" \
  --certificate-identity "https://github.com/modelcontextprotocol/registry/.github/workflows/release.yml@refs/tags/$version" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$tmp/$archive"
mkdir -p "$dir"
tar -xzf "$tmp/$archive" -C "$dir" mcp-publisher
echo "$dir/mcp-publisher"
