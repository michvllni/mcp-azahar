#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# install-azahar.sh - download a pinned Azahar AppImage and extract it.
#
# SYNOPSIS
#   install-azahar.sh [-t TAG] [-s SHA256] [-d DIR]
#
# DESCRIPTION
#   Downloads azahar.AppImage from the given azahar-emu/azahar GitHub release,
#   verifies its SHA-256 and extracts the embedded squashfs into DIR with
#   unsquashfs. The AppImage itself is never executed: containers have no FUSE,
#   and extracting without running the runtime avoids that dependency.
#   The squashfs offset is the end of the AppImage runtime's ELF section
#   header table (e_shoff + e_shentsize * e_shnum), read with readelf.
#   Start Azahar via DIR/AppRun (it injects the bundled libstdc++ when needed).
#   Requires: curl, readelf (binutils), unsquashfs (squashfs-tools), sha256sum.
#   Run as root or with write access to DIR's parent.
#
# OPTIONS
#   -t TAG     Release tag (default: $AZAHAR_VERSION or 2127.0-rc3)
#   -s SHA256  Expected SHA-256 of azahar.AppImage (default: $AZAHAR_SHA256 or
#              the digest pinned below for the default tag)
#   -d DIR     Target directory, replaced if it exists (default: /opt/azahar)
#   -h         Show this help
#
# EXAMPLE
#   sudo .devcontainer/install-azahar.sh -t 2127.0-rc3 \
#     -s 173dcedb78357c4920ddfca385fdce273e2e9e31b874564830068061f98fc1da
set -euo pipefail

tag="${AZAHAR_VERSION:-2127.0-rc3}"
sha="${AZAHAR_SHA256:-173dcedb78357c4920ddfca385fdce273e2e9e31b874564830068061f98fc1da}"
dir="/opt/azahar"

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

while getopts ":t:s:d:h" opt; do
  case "$opt" in
    t) tag="$OPTARG" ;;
    s) sha="$OPTARG" ;;
    d) dir="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
img="$tmp/azahar.AppImage"

curl -fsSL -o "$img" "https://github.com/azahar-emu/azahar/releases/download/${tag}/azahar.AppImage"
echo "${sha}  ${img}" | sha256sum -c -

shoff="$(readelf -h "$img" | awk -F: '/Start of section headers/ {print $2+0}')"
shentsize="$(readelf -h "$img" | awk -F: '/Size of section headers/ {print $2+0}')"
shnum="$(readelf -h "$img" | awk -F: '/Number of section headers/ {print $2+0}')"
offset=$((shoff + shentsize * shnum))

rm -rf "$dir"
unsquashfs -q -o "$offset" -d "$dir" "$img" >/dev/null
echo "$tag" > "$dir/AZAHAR_VERSION"
echo "Azahar $tag extracted to $dir (start with $dir/AppRun)"
