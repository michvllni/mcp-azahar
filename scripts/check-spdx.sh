#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# check-spdx.sh - verify every source file carries the project's SPDX header.
#
# SYNOPSIS
#   check-spdx.sh [-h]
#
# DESCRIPTION
#   Lists tracked files (git ls-files) of type *.go, *.sh, *.yml, *.yaml,
#   *.json, *.json5 and Dockerfile and fails if any of them lacks the line
#   "SPDX-License-Identifier: GPL-3.0-or-later" within its first 5 lines.
#   Run from anywhere inside the repository.
#
# OPTIONS
#   -h  Show this help
#
# EXAMPLE
#   scripts/check-spdx.sh
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

cd "$(git rev-parse --show-toplevel)"
missing=0
while IFS= read -r -d '' f; do
  if ! head -n 5 "$f" | grep -q 'SPDX-License-Identifier: GPL-3.0-or-later'; then
    echo "missing SPDX header: $f" >&2
    missing=1
  fi
done < <(git ls-files -z -- '*.go' '*.sh' '*.yml' '*.yaml' '*.json' '*.json5' '*Dockerfile')

[ "$missing" -eq 0 ] && echo "ok: all source files have SPDX headers"
exit "$missing"
