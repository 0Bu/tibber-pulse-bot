#!/usr/bin/env bash
# Verify that the Tibber Pulse Bridge local web server is enabled (param 39).
# Usage: verify.sh <bridge-lan-ip> <sticker-password>
# The data endpoint returns raw binary SML despite the .json suffix; we only
# check the HTTP status, so -I (HEAD) is enough and we never print the body.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <bridge-lan-ip> <sticker-password>" >&2
  exit 2
fi

host="$1"
pass="$2"
url="http://${host}/data.json?node_id=1"

code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' -u "admin:${pass}" -I "$url" || true)

case "$code" in
  200)
    echo "OK: web server enabled, /data.json answered 200 — bot can poll/push."
    ;;
  401)
    echo "FAIL: 401 Unauthorized — web server is up but the password is wrong." >&2
    exit 1
    ;;
  000|"")
    echo "FAIL: no response from ${host} — bridge not on the LAN, or param 39 did not persist." >&2
    exit 1
    ;;
  *)
    echo "UNEXPECTED: HTTP ${code} from ${url} — investigate." >&2
    exit 1
    ;;
esac
