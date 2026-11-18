#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# server-json.sh - render the MCP Registry entry (server.json) for a release.
#
# SYNOPSIS
#   scripts/server-json.sh -v VERSION [-o FILE] [-h]
#
# DESCRIPTION
#   Writes server.json from the repository root with VERSION as the entry's
#   version, the npm package version and the container image tag (D10).
#   mcp-publisher (release.yml) publishes the result to the official MCP
#   Registry after the npm packages and the image exist.
#
# OPTIONS
#   -v VERSION  Release version (SemVer, without "v"; required)
#   -o FILE     Output file (default: dist/registry/server.json)
#   -h          Show this help
#
# EXAMPLE
#   scripts/server-json.sh -v 0.1.0
set -euo pipefail

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

root="$(cd "$(dirname "$0")/.." && pwd)"
version="" out="$root/dist/registry/server.json"
while getopts ":v:o:h" opt; do
  case "$opt" in
    v) version="$OPTARG" ;;
    o) out="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
if [ $# -ne 0 ] || [ -z "$version" ]; then
  usage >&2
  exit 2
fi

mkdir -p "$(dirname "$out")"
jq --arg v "$version" '
  .version = $v
  | .packages |= map(
      if .registryType == "npm" then .version = $v
      elif .registryType == "oci" then .identifier = (.identifier | sub(":[^:/]*$"; "") + ":" + $v)
      else . end)' "$root/server.json" > "$out"
echo "$out"
