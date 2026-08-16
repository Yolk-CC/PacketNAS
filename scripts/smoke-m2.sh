#!/bin/sh
# M2 smoke test: media index, thumbnails, gallery API, Range streaming.
# Usage: scripts/smoke-m2.sh [binary]
set -e

BIN="${1:-./pocket-nas}"
PORT="${PORT:-18090}"
ROOT="$(mktemp -d)"
trap 'kill $SRV_PID 2>/dev/null; rm -rf "$ROOT"' EXIT

echo "== seeding test media in $ROOT =="
mkdir -p "$ROOT/DCIM"
# JPEG with correct dimensions (ffmpeg-generated), a PNG, and a 2s mp4.
ffmpeg -v error -f lavfi -i "testsrc=duration=0.2:size=640x480:rate=1" -frames:v 1 -y "$ROOT/DCIM/a.jpg"
ffmpeg -v error -f lavfi -i "color=red:size=320x200" -frames:v 1 -y "$ROOT/DCIM/b.png"
ffmpeg -v error -f lavfi -i "testsrc=duration=2:size=320x240:rate=10" -y "$ROOT/vid.mp4"
mkdir -p "$ROOT/.hidden" && cp "$ROOT/DCIM/a.jpg" "$ROOT/.hidden/skip.jpg"

echo "== starting server =="
"$BIN" -root "$ROOT" -port "$PORT" -password pw &
SRV_PID=$!
sleep 1

BASE="http://127.0.0.1:$PORT"
TOKEN=$(curl -sf -X POST "$BASE/api/auth/login" -d '{"password":"pw"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')
[ -n "$TOKEN" ] || { echo "FAIL: login"; exit 1; }
AUTH="X-Auth-Token: $TOKEN"

echo "== waiting for background scan =="
for i in $(seq 1 30); do
  OUT=$(curl -sf -H "$AUTH" "$BASE/api/gallery/scan")
  SCANNING=$(echo "$OUT" | sed 's/.*"scanning":\([a-z]*\).*/\1/')
  INDEXED=$(echo "$OUT" | sed 's/.*"indexed":\([0-9]*\).*/\1/')
  [ "$SCANNING" = "false" ] && break
  sleep 0.5
done
echo "scan status: $OUT"
[ "$SCANNING" = "false" ] || { echo "FAIL: scan did not finish"; exit 1; }
[ "$INDEXED" = "3" ] || { echo "FAIL: expected 3 indexed, got $INDEXED"; exit 1; }

echo "== /api/gallery =="
G=$(curl -sf -H "$AUTH" "$BASE/api/gallery?limit=200")
echo "$G"
echo "$G" | grep -q '"total":3' || { echo "FAIL: gallery total"; exit 1; }
echo "$G" | grep -q '"width":640' || { echo "FAIL: jpeg width"; exit 1; }
echo "$G" | grep -q '"duration":' || { echo "FAIL: duration field"; exit 1; }
# ordering: takenTime DESC
curl -sf -H "$AUTH" "$BASE/api/gallery?type=image" | grep -q '"total":2' || { echo "FAIL: type=image"; exit 1; }
curl -sf -H "$AUTH" "$BASE/api/gallery?type=video" | grep -q '"total":1' || { echo "FAIL: type=video"; exit 1; }
curl -sf -H "$AUTH" "$BASE/api/gallery?offset=1&limit=1" | grep -q '"total":3' || { echo "FAIL: paging total"; exit 1; }
[ "$(curl -sf -H "$AUTH" "$BASE/api/gallery?offset=1&limit=1" | grep -o '"path"' | wc -l)" = "1" ] || { echo "FAIL: paging size"; exit 1; }

echo "== /api/thumb (jpeg) =="
curl -sf -H "$AUTH" -o /tmp/thumb1.jpg -D /tmp/thumb1.h "$BASE/api/thumb/DCIM/a.jpg?w=300&h=300"
grep -qi "content-type: image/jpeg" /tmp/thumb1.h || { echo "FAIL: thumb content-type"; exit 1; }
DIMS=$(ffprobe -v quiet -print_format json -show_streams /tmp/thumb1.jpg | grep -o '"width": [0-9]*' | head -1 | grep -o '[0-9]*')
[ "$DIMS" -le 300 ] || { echo "FAIL: thumb width $DIMS > 300"; exit 1; }

echo "== /api/thumb (video) =="
curl -sf -H "$AUTH" -o /tmp/thumb2.jpg "$BASE/api/thumb/vid.mp4" || { echo "FAIL: video thumb"; exit 1; }
[ -s /tmp/thumb2.jpg ] || { echo "FAIL: empty video thumb"; exit 1; }

echo "== /api/thumb fallback (corrupt) =="
echo garbage > "$ROOT/broken.jpg"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH" "$BASE/api/thumb/broken.jpg")
[ "$CODE" = "302" ] || { echo "FAIL: expected 302, got $CODE"; exit 1; }

echo "== /api/media/file Range =="
CODE=$(curl -s -o /tmp/range.bin -w "%{http_code}" -H "$AUTH" -H "Range: bytes=0-99" "$BASE/api/media/file/vid.mp4")
[ "$CODE" = "206" ] || { echo "FAIL: expected 206, got $CODE"; exit 1; }
[ "$(wc -c < /tmp/range.bin)" = "100" ] || { echo "FAIL: range size"; exit 1; }

echo "== auth required =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/gallery")
[ "$CODE" = "401" ] || { echo "FAIL: expected 401, got $CODE"; exit 1; }

echo "== traversal blocked =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH" --path-as-is "$BASE/api/thumb/../../etc/passwd")
[ "$CODE" = "403" ] || { echo "FAIL: expected 403, got $CODE"; exit 1; }

echo "== hidden dir excluded from index =="
curl -sf -H "$AUTH" "$BASE/api/gallery?limit=200" | grep -q "skip.jpg" && { echo "FAIL: hidden file indexed"; exit 1; }

echo "ALL M2 SMOKE TESTS PASSED"
