#!/usr/bin/env bash
set -euo pipefail

# Run Semgrep OSS SAST across the repository.
if command -v semgrep >/dev/null 2>&1; then
  SEMGREP_CMD=(semgrep)
elif command -v uvx >/dev/null 2>&1; then
  SEMGREP_CMD=(uvx semgrep)
elif command -v pipx >/dev/null 2>&1; then
  SEMGREP_CMD=(pipx run semgrep)
else
  echo "error: semgrep is required (install via 'pip install semgrep', 'pipx install semgrep', or 'uvx semgrep')" >&2
  exit 1
fi

exec "${SEMGREP_CMD[@]}" scan \
  --config p/default \
  --metrics=off \
  --error \
  --exclude-rule yaml.github-actions.security.github-actions-mutable-action-tag.github-actions-mutable-action-tag \
  "$@"
