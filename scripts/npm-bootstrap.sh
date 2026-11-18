#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# npm-bootstrap.sh - one-time npm setup for trusted publishing (maintainer).
#
# SYNOPSIS
#   scripts/npm-bootstrap.sh [-n] [-h]
#
# DESCRIPTION
#   npm can configure a trusted publisher only for a package that exists. For
#   each of the 7 packages (mcp-azahar and mcp-azahar-<os>-<cpu>) this script
#   publishes a placeholder version 0.0.0-bootstrap (dist-tag "bootstrap",
#   deprecated) unless the package exists, then runs
#     npm trust github <pkg> --file release.yml --repo michvllni/mcp-azahar --allow-publish
#   so .github/workflows/release.yml can publish it via OIDC with provenance.
#   Run it on your machine, logged in to npm (npm login) with 2FA enabled,
#   npm >= 11.15.0. npm asks for 2FA; allow "skip for 5 minutes" so the
#   remaining calls go through. A new trust configuration expires unless a
#   publish succeeds within 2 days: run this shortly before the first release.
#
# OPTIONS
#   -n  Dry run: print what would be done
#   -h  Show this help
#
# EXAMPLE
#   npm login && scripts/npm-bootstrap.sh
set -euo pipefail

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

dry=false
while getopts ":nh" opt; do
  case "$opt" in
    n) dry=true ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

repo="michvllni/mcp-azahar"
packages=(mcp-azahar-linux-x64 mcp-azahar-linux-arm64 mcp-azahar-darwin-x64
  mcp-azahar-darwin-arm64 mcp-azahar-win32-x64 mcp-azahar-win32-arm64 mcp-azahar)

# run CMD...: print the command; run it unless this is a dry run.
run() {
  echo "+ $*" >&2
  $dry || "$@"
}

npm_version="$(npm --version)"
if [ "$(printf '%s\n' 11.15.0 "$npm_version" | sort -V | head -n1)" != 11.15.0 ]; then
  echo "npm $npm_version is too old for npm trust; need >= 11.15.0 (npm install -g npm@latest)" >&2
  exit 1
fi
$dry || npm whoami >/dev/null || { echo "not logged in: run npm login first" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for pkg in "${packages[@]}"; do
  if npm view "$pkg" name >/dev/null 2>&1; then
    echo "$pkg exists, no placeholder needed" >&2
  else
    mkdir -p "$tmp/$pkg"
    jq -n --arg name "$pkg" --arg repo "$repo" '{
      name: $name, version: "0.0.0-bootstrap",
      description: "Placeholder to set up trusted publishing; use the latest version",
      repository: {type: "git", url: ("git+https://github.com/" + $repo + ".git")},
      license: "GPL-3.0-or-later"
    }' > "$tmp/$pkg/package.json"
    (cd "$tmp/$pkg" && run npm publish --access public --tag bootstrap)
    run npm deprecate "$pkg@0.0.0-bootstrap" "placeholder for trusted publishing setup, not a release"
  fi
  run npm trust github "$pkg" --file release.yml --repo "$repo" --allow-publish --yes
  $dry || sleep 2
done
echo "done: release.yml can now publish ${packages[*]}" >&2
