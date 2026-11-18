#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# install-go-tools.sh - install pinned golangci-lint and goreleaser binaries.
#
# SYNOPSIS
#   install-go-tools.sh [-l VERSION] [-r VERSION] [-d DIR]
#
# DESCRIPTION
#   Downloads the linux/amd64 release archives of golangci-lint and goreleaser
#   from GitHub, verifies them against the checksum files published with each
#   release, and installs the binaries into DIR.
#   Requires: curl, tar, sha256sum. Run as root or with write access to DIR.
#
# OPTIONS
#   -l VERSION  golangci-lint version without "v" (default: $GOLANGCI_LINT_VERSION or 2.15.0)
#   -r VERSION  goreleaser version without "v" (default: $GORELEASER_VERSION or 2.20.2)
#   -d DIR      Install directory (default: /usr/local/bin)
#   -h          Show this help
#
# EXAMPLE
#   sudo .devcontainer/install-go-tools.sh -l 2.15.0 -r 2.20.2
set -euo pipefail

lint="${GOLANGCI_LINT_VERSION:-2.15.0}"
rel="${GORELEASER_VERSION:-2.20.2}"
dir="/usr/local/bin"

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

while getopts ":l:r:d:h" opt; do
  case "$opt" in
    l) lint="$OPTARG" ;;
    r) rel="$OPTARG" ;;
    d) dir="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"

# fetch: download a URL into the current directory. Arguments: URL. No output.
fetch() { curl -fsSLO "$1"; }

base="https://github.com/golangci/golangci-lint/releases/download/v${lint}"
fetch "$base/golangci-lint-${lint}-linux-amd64.tar.gz"
fetch "$base/golangci-lint-${lint}-checksums.txt"
grep " golangci-lint-${lint}-linux-amd64.tar.gz\$" "golangci-lint-${lint}-checksums.txt" | sha256sum -c -
tar xzf "golangci-lint-${lint}-linux-amd64.tar.gz"
install -m 0755 "golangci-lint-${lint}-linux-amd64/golangci-lint" "$dir/"

base="https://github.com/goreleaser/goreleaser/releases/download/v${rel}"
fetch "$base/goreleaser_Linux_x86_64.tar.gz"
fetch "$base/checksums.txt"
grep " goreleaser_Linux_x86_64.tar.gz\$" checksums.txt | sha256sum -c -
mkdir goreleaser && tar xzf goreleaser_Linux_x86_64.tar.gz -C goreleaser
install -m 0755 goreleaser/goreleaser "$dir/"

"$dir/golangci-lint" version
"$dir/goreleaser" --version | grep GitVersion
