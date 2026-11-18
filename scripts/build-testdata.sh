#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# build-testdata.sh - rebuild committed test binaries from their sources.
#
# SYNOPSIS
#   build-testdata.sh [-h]
#
# DESCRIPTION
#   Builds internal/symbols/testdata/symbols.elf from symbols.c with the
#   ARM bare-metal toolchain (gcc-arm-none-eabi, in the dev container):
#   32-bit little-endian ARM with DWARF, linked at 0x00100000 like a 3DSX,
#   and symbols-nodebug.elf, the same without DWARF.
#   The ELF is committed so tests do not need the toolchain; rerun this
#   after changing the source and commit both.
#
# OPTIONS
#   -h  Show this help
#
# EXAMPLE
#   scripts/build-testdata.sh
set -euo pipefail

# usage: print the header help block. No arguments; writes to stdout.
usage() {
  sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p}' "$0"
}

case "${1:-}" in
  "") ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

cd "$(git rev-parse --show-toplevel)/internal/symbols/testdata"
arm-none-eabi-gcc -march=armv6k -mfloat-abi=hard -mfpu=vfp -g -O0 \
  -ffreestanding -nostdlib -Wl,-Ttext=0x00100000 -Wl,-e,main \
  -Wl,--build-id=none -fno-ident \
  -o symbols.elf symbols.c
arm-none-eabi-strip --strip-debug -o symbols-nodebug.elf symbols.elf
echo "built $(pwd)/symbols.elf and symbols-nodebug.elf"
