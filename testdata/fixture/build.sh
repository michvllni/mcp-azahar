#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# build.sh - build the integration test fixture (fixture.elf, fixture.3dsx).
#
# SYNOPSIS
#   testdata/fixture/build.sh [-o DIR] [-h]
#
# DESCRIPTION
#   Compiles main.c against libctru with devkitARM and converts the ELF to
#   a 3DSX. Needs devkitPro ($DEVKITPRO, default /opt/devkitpro), e.g. the
#   devkitpro/devkitarm container image CI uses (devkitPro's servers refuse
#   some networks, so the dev container does not ship it). The ELF keeps
#   DWARF (-g) so tests read test_state by name.
#
# OPTIONS
#   -o DIR  Output directory (default: this script's directory)
#   -h      Show this help
#
# EXAMPLE
#   docker run --rm -v "$PWD:/src" -w /src devkitpro/devkitarm testdata/fixture/build.sh -o build
set -euo pipefail

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

src="$(cd "$(dirname "$0")" && pwd)"
out="$src"
while getopts ":o:h" opt; do
  case "$opt" in
    o) out="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage >&2; exit 2; }

dkp="${DEVKITPRO:-/opt/devkitpro}"
mkdir -p "$out"
"$dkp/devkitARM/bin/arm-none-eabi-gcc" -g -O2 -Wall -Wextra -Werror \
  -march=armv6k -mtune=mpcore -mfloat-abi=hard -mtp=soft -D__3DS__ \
  -I"$dkp/libctru/include" -specs=3dsx.specs \
  "$src/main.c" -L"$dkp/libctru/lib" -lctru -lm \
  -o "$out/fixture.elf"
"$dkp/tools/bin/3dsxtool" "$out/fixture.elf" "$out/fixture.3dsx"
echo "built $out/fixture.elf and $out/fixture.3dsx"
