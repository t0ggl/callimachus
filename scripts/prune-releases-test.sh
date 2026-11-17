#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export PRUNE_TEST_DIR="$tmp"
export PATH="$tmp:$PATH"
cat > "$tmp/gh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == 'api --paginate --slurp repos/{owner}/{repo}/releases?per_page=100' ]]; then
  cat "$PRUNE_TEST_DIR/releases.json"
elif [[ "$*" == 'api --method DELETE repos/{owner}/{repo}/releases/'* ]]; then
  echo "$*" >> "$PRUNE_TEST_DIR/deleted"
else
  echo "Unexpected gh call: $*" >&2
  exit 1
fi
MOCK
chmod +x "$tmp/gh"
# Unsorted releases across pages must retain the newest release.
echo '[[{"id":1,"created_at":"2025-01-01"},{"id":3,"created_at":"2025-03-01"}],[{"id":2,"created_at":"2025-02-01"}]]' > "$tmp/releases.json"
bash scripts/prune-releases.sh 1 true > "$tmp/actual"
printf 'Would delete release: 2\nWould delete release: 1\n' > "$tmp/expected"
diff -u "$tmp/expected" "$tmp/actual"
[[ ! -e "$tmp/deleted" ]]
bash scripts/prune-releases.sh 1
printf 'api --method DELETE repos/{owner}/{repo}/releases/2\napi --method DELETE repos/{owner}/{repo}/releases/1\n' > "$tmp/expected"
diff -u "$tmp/expected" "$tmp/deleted"
rm "$tmp/deleted"
bash scripts/prune-releases.sh 20
[[ ! -e "$tmp/deleted" ]]
echo '[[]]' > "$tmp/releases.json"
bash scripts/prune-releases.sh 1
[[ ! -e "$tmp/deleted" ]]
if bash scripts/prune-releases.sh -1 2>/dev/null; then
  echo 'Expected invalid retention count to fail' >&2
  exit 1
fi
# A failed listing must stop before any deletion.
echo 'not-json' > "$tmp/releases.json"
if bash scripts/prune-releases.sh 1 2>/dev/null; then
  echo 'Expected invalid API response to fail' >&2
  exit 1
fi
[[ ! -e "$tmp/deleted" ]]
echo 'Release pruning tests passed.'
