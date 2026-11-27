#!/usr/bin/env bash
set -euo pipefail

image=$1
server=
worker=
cleanup() {
  if [[ $? != 0 ]]; then
    if [[ -n "$server" ]]; then docker logs "$server" >&2 || true; fi
    if [[ -n "$worker" ]]; then docker logs "$worker" >&2 || true; fi
  fi
  if [[ -n "$worker" ]]; then docker rm -fv "$worker" >/dev/null; fi
  if [[ -n "$server" ]]; then docker rm -fv "$server" >/dev/null; fi
}
trap cleanup EXIT

server=$(docker run -d --network none --read-only --tmpfs /tmp:mode=1777 \
  -e CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT=/books/incoming \
  -e CALLIMACHUS_STAGING_POLL_INTERVAL=100ms \
  -e CALLIMACHUS_OPENLIBRARY_ENABLED=false \
  -e CALLIMACHUS_CONVERT_EBOOKS=true -e CALLIMACHUS_EBOOK_TARGET_FORMAT=AZW3 "$image")
docker exec "$server" curl --fail --silent --show-error --max-time 2 --retry 5 --retry-connrefused \
  http://127.0.0.1:8080/healthz
docker exec "$server" sh -ec '
  test "$(id -u):$(id -g)" = 1000:1000
  test "$(stat -c %u:%g /config)" = 1000:1000
  test "$(stat -c %u:%g /books)" = 1000:1000
  test -s /config/callimachus.db
'

# Check the metadata tool and hand off a completed download to the server.
docker exec -i "$server" sh -ec '
  mkdir -p /books/incoming
  cat > /books/smoke.epub
  ebook-meta /books/smoke.epub --to-opf=/tmp/smoke.opf
  test -s /tmp/smoke.opf
  test -d "$XDG_CONFIG_HOME/calibre"
  mv /books/smoke.epub /books/incoming/smoke.epub
' < testdata/epubs/epub2_valid.epub

worker=$(docker run -d --network none --read-only --tmpfs /tmp:mode=1777 \
  --volumes-from "$server" --entrypoint /usr/local/bin/callimachus-worker "$image")
timeout 30 grep -m 1 'conversion job completed' < <(docker logs --follow "$worker" 2>&1)
docker exec "$worker" sh -ec '
  test "$(id -u):$(id -g)" = 1000:1000
  test -d "$XDG_CONFIG_HOME/calibre"
  test -n "$(find /books -name "*.azw3" -size +0c -print -quit)"
  test -n "$(find /books -name "*.opf" -size +0c -print -quit)"
'
docker stop --timeout 5 "$worker" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$worker")" = 0
