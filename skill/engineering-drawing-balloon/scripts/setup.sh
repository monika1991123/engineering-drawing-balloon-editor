#!/usr/bin/env bash
# Create (or reuse) the skill's private virtual environment with pinned dependencies.
set -Eeuo pipefail
SKILL="$(cd "$(dirname "$0")/.." && pwd)"
VENV="$SKILL/.venv"
PYTHON="${PYTHON:-python3}"

if "$VENV/bin/python" -c "import fitz" 2>/dev/null; then
  echo "Environment ready: $VENV"
  exit 0
fi
if ! "$PYTHON" -c 'import sys; sys.exit(sys.version_info < (3, 10))' 2>/dev/null; then
  echo "ERROR: Python 3.10+ required (found: $("$PYTHON" --version 2>&1)). Set PYTHON=/path/to/python3.x" >&2
  exit 2
fi
"$PYTHON" -m venv "$VENV"
"$VENV/bin/python" -m pip install --quiet -r "$SKILL/requirements.txt"
"$VENV/bin/python" -c "import fitz; print('Environment ready:', '$VENV', 'PyMuPDF', fitz.VersionBind)"
