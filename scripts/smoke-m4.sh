#!/bin/bash
# M4 smoke test: multi-resolution transcoding API.
# Usage: bash scripts/smoke-m4.sh [binary]
set -eu

BIN="${1:-./pocket-nas}"
PORT="${PORT:-18097}"
ROOT="$(mktemp -d)"
trap 'kill $SRV_PID 2>/dev/null || true; rm -rf "$ROOT" /tmp/m4-*' EXIT

echo "== seeding: 5s 1280x720 video =="
ffmpeg -v error -f lavfi -i "testsrc=duration=5:size=1280x720:rate=15" \
       -f lavfi -i "sine=frequency=440:duration=5" \
       -c:v libx264 -pix_fmt yuv420p -shortest -y "$ROOT/clip.mp4"
echo "not a video" > "$ROOT/doc.txt"

"$BIN" -root "$ROOT" -port "$PORT" &
SRV_PID=$!
sleep 1
BASE="http://127.0.0.1:$PORT"

echo "== first 360p request -> 202 =="
CODE=$(curl -s -o /tmp/m4-resp -w "%{http_code}" "$BASE/api/video/clip.mp4?res=360p")
[ "$CODE" = "202" ] || { echo "FAIL: expected 202, got $CODE"; cat /tmp/m4-resp; exit 1; }
cat /tmp/m4-resp; echo

echo "== other APIs unaffected during transcode =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/files?path=/")
[ "$CODE" = "200" ] || { echo "FAIL: /api/files $CODE during transcode"; exit 1; }
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/media/file/clip.mp4")
[ "$CODE" = "200" ] || { echo "FAIL: /api/media/file $CODE during transcode"; exit 1; }
echo "  files + original streaming OK"

echo "== poll status to done =="
for i in $(seq 1 100); do
  OUT=$(curl -sf "$BASE/api/video/status/clip.mp4?res=360p")
  case "$OUT" in *'"status":"done"'*) break;; *'"status":"failed"'*) echo "FAIL: job failed: $OUT"; exit 1;; esac
  sleep 1
done
echo "status: $OUT"
echo "$OUT" | grep -q '"status":"done"' || { echo "FAIL: never done"; exit 1; }
echo "$OUT" | grep -q '"progress":100' || { echo "FAIL: progress != 100"; exit 1; }

echo "== GET after done -> 200 video/mp4, height 360 =="
curl -sf -D /tmp/m4-hdr -o /tmp/m4-out.mp4 "$BASE/api/video/clip.mp4?res=360p"
grep -qi "content-type: video/mp4" /tmp/m4-hdr || { echo "FAIL: content-type"; exit 1; }
H=$(ffprobe -v quiet -print_format json -show_streams /tmp/m4-out.mp4 | grep -o '"height": [0-9]*' | head -1 | grep -o '[0-9]*')
[ "$H" = "360" ] || { echo "FAIL: height=$H want 360"; exit 1; }
echo "  height=360 OK"

echo "== Range 206 on transcoded output =="
CODE=$(curl -s -o /tmp/m4-range -w "%{http_code}" -H "Range: bytes=0-99" "$BASE/api/video/clip.mp4?res=360p")
[ "$CODE" = "206" ] || { echo "FAIL: range $CODE"; exit 1; }
[ "$(wc -c < /tmp/m4-range)" = "100" ] || { echo "FAIL: range size"; exit 1; }

echo "== original byte-identical =="
curl -sf -o /tmp/m4-orig "$BASE/api/video/clip.mp4?res=original"
cmp "$ROOT/clip.mp4" /tmp/m4-orig || { echo "FAIL: original differs"; exit 1; }
echo "  original OK"

echo "== concurrent duplicate requests -> single output =="
N0=$(find "$ROOT/.pocketnas/transcode" -name "*.mp4" | wc -l)
python3 - "$BASE" <<'PYEOF'
import sys, threading, urllib.request
base = sys.argv[1]
def hit():
    try: urllib.request.urlopen(base + "/api/video/clip.mp4?res=720p", timeout=30)
    except Exception: pass
ts = [threading.Thread(target=hit) for _ in range(4)]
[t.start() for t in ts]
[t.join() for t in ts]
PYEOF
for i in $(seq 1 100); do
  OUT=$(curl -sf "$BASE/api/video/status/clip.mp4?res=720p")
  case "$OUT" in *'"status":"done"'*) break;; *'"status":"failed"'*) echo "FAIL: job failed: $OUT"; exit 1;; esac
  sleep 1
done
echo "$OUT" | grep -q '"status":"done"' || { echo "FAIL: 720p never done"; exit 1; }
N=$(find "$ROOT/.pocketnas/transcode" -name "*.mp4" | wc -l)
[ "$N" = "$((N0+1))" ] || { echo "FAIL: outputs $N0->$N, want exactly one new (dedup)"; exit 1; }
echo "  dedup OK"

echo "== non-video 400 =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/video/doc.txt?res=720p")
[ "$CODE" = "400" ] || { echo "FAIL: expected 400, got $CODE"; exit 1; }

echo "== invalid res 400 / traversal 403 =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/video/clip.mp4?res=4k")
[ "$CODE" = "400" ] || { echo "FAIL: res=4k got $CODE"; exit 1; }
CODE=$(curl -s -o /dev/null -w "%{http_code}" --path-as-is "$BASE/api/video/../../etc/passwd?res=360p")
[ "$CODE" = "403" ] || { echo "FAIL: traversal got $CODE"; exit 1; }

echo "== gallery resolutions field =="
curl -sf "$BASE/api/gallery" | grep -q '"resolutions":\["360p","720p","1080p","original"\]' \
  || { echo "FAIL: resolutions field missing"; exit 1; }

echo "ALL M4 SMOKE TESTS PASSED"
