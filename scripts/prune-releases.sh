#!/usr/bin/env bash
set -euo pipefail

keep="${1:-20}"
dry_run="${2:-false}"
if [[ ! "$keep" =~ ^[1-9][0-9]*$ || ! "$dry_run" =~ ^(true|false)$ ]]; then
  echo "usage: $0 [positive release count] [true|false dry run]" >&2
  exit 1
fi

# Fetch all pages before deleting, so deletion cannot shift pagination offsets.
release_ids=$(gh api --paginate --slurp 'repos/{owner}/{repo}/releases?per_page=100' |
  jq -r --argjson keep "$keep" 'add | sort_by(.created_at) | reverse | .[$keep:] | .[].id')
while IFS= read -r release_id; do
  [[ -n "$release_id" ]] || continue
  if [[ "$dry_run" == true ]]; then
    echo "Would delete release: $release_id"
  else
    # Deleting a release through the API leaves its Git tag intact.
    gh api --method DELETE "repos/{owner}/{repo}/releases/$release_id"
  fi
done <<< "$release_ids"
