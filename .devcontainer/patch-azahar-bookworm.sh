#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# patch-azahar-bookworm.sh - make the extracted Azahar AppImage run on Debian 12.
#
# SYNOPSIS
#   patch-azahar-bookworm.sh [-a DIR]
#
# DESCRIPTION
#   Stopgap for a container still running on Debian bookworm (the Dockerfile
#   already uses trixie and does not call this). Azahar 2127
#   AppImages need glibc >= 2.39 (bookworm: 2.36) and a newer HarfBuzz than
#   bookworm's 6.0 (bundled pango uses hb_ot_color_has_paint).
#   The script:
#   1. downloads the Debian trixie packages listed in PKGS (deb.debian.org,
#      falling back to snapshot.debian.org once superseded), verifies their
#      SHA-256 and copies their libraries into DIR/usr/lib (the bundle's lib
#      dir, which every bundled library already searches via RUNPATH $ORIGIN);
#   2. copies the bundle's newer libstdc++/libgcc_s from DIR/checkrt there too
#      (what the AppImage's AppRun does via LD_LIBRARY_PATH);
#   3. points DIR/usr/bin/azahar's interpreter at the new ld-linux and turns
#      its RUNPATH into an RPATH, so system libraries loaded into Azahar
#      (Mesa, X11) resolve glibc from DIR/usr/lib as well.
#   The system glibc is untouched and no LD_LIBRARY_PATH is needed, so child
#   processes Azahar spawns (e.g. sh) run with the system libraries.
#   Afterwards start Azahar as DIR/usr/bin/azahar, not via AppRun.
#   Requires: curl, dpkg-deb, sha256sum, patchelf. Run as root after
#   install-azahar.sh. Delete this script once the dev container has been
#   rebuilt on trixie.
#
# OPTIONS
#   -a DIR  Extracted Azahar AppImage (default: /opt/azahar)
#   -h      Show this help
#
# EXAMPLE
#   sudo .devcontainer/patch-azahar-bookworm.sh -a /opt/azahar
set -euo pipefail

# Debian trixie packages: "<package> <version> <sha256> <pool dir>".
PKGS="
libc6 2.41-12+deb13u4 967aa62605721081c3eb2a17650611a792aa802d76a6511d1840242623d204c9 g/glibc
libharfbuzz0b 10.2.0-1+deb13u1 932165b933e5eb633293afe984a983ebe89cb27c923ce7208626f1da6c0c7941 h/harfbuzz
"
SNAPSHOT="20261118T000000Z"

app="/opt/azahar"

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

while getopts ":a:h" opt; do
  case "$opt" in
    a) app="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

lib="$app/usr/lib"
bin="$app/usr/bin/azahar"
if [ ! -d "$lib" ] || [ ! -f "$bin" ]; then
  echo "error: no extracted Azahar in $app" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# install_pkg: download, verify and copy one package's libraries into $lib.
# Arguments: package, version, sha256, pool dir. Output: sha256sum status.
install_pkg() {
  local pkg="$1" ver="$2" sha="$3" pool="$4"
  local path="pool/main/${pool}/${pkg}_${ver/+/%2B}_amd64.deb"
  local deb="$tmp/$pkg.deb"
  curl -fsSL -o "$deb" "https://deb.debian.org/debian/$path" \
    || curl -fsSL -o "$deb" "https://snapshot.debian.org/archive/debian/${SNAPSHOT}/$path"
  echo "${sha}  ${deb}" | sha256sum -c -
  dpkg-deb -x "$deb" "$tmp/$pkg"
  cp -a "$tmp/$pkg/usr/lib/x86_64-linux-gnu/." "$lib/"
}

while read -r pkg ver sha pool; do
  [ -n "$pkg" ] || continue
  install_pkg "$pkg" "$ver" "$sha" "$pool"
done <<< "$PKGS"

cp -a "$app"/checkrt/cxx/libstdc++.so.6 "$app"/checkrt/gcc/libgcc_s.so.1 "$lib/"

patchelf --set-interpreter "$lib/ld-linux-x86-64.so.2" "$bin"
# $ORIGIN is literal: the dynamic loader expands it, not the shell.
# shellcheck disable=SC2016
patchelf --force-rpath --set-rpath '$ORIGIN/../lib' "$bin"
"$lib/ld-linux-x86-64.so.2" --version | head -1
echo "patched $bin (interpreter $lib/ld-linux-x86-64.so.2, RPATH \$ORIGIN/../lib)"
