#!/usr/bin/env bash
set -euo pipefail
[[ $# -eq 0 ]] || { echo "This script takes no arguments" >&2; exit 2; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON=""
for candidate in python3 python; do
  if command -v "$candidate" >/dev/null 2>&1 && "$candidate" -c 'import sys; sys.exit(sys.version_info < (3, 10))' >/dev/null 2>&1; then
    PYTHON="$candidate"
    break
  fi
done
[[ -n "$PYTHON" ]] || { echo "need Python 3" >&2; exit 1; }
exec "$PYTHON" "${HERE}/compile.py" windows
