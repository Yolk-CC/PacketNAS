#!/bin/bash
# M3 smoke test: Live Photo parsing, extraction API, gallery integration.
# Usage: bash scripts/smoke-m3.sh [binary]
set -eu

BIN="${1:-./pocket-nas}"
PORT="${PORT:-18095}"
ROOT="$(mktemp -d)"
trap 'kill $SRV_PID 2>/dev/null || true; rm -rf "$ROOT" /tmp/m3gen /tmp/m3-embedded.mp4 /tmp/m3-comp.mov /tmp/m3hdr' EXIT

echo "== seeding test media in $ROOT =="
ffmpeg -v error -f lavfi -i "testsrc=duration=1:size=320x240:rate=10" -c:v libx264 -y "$ROOT/embedded.mp4"
ffmpeg -v error -f lavfi -i "testsrc=duration=0.2:size=640x480:rate=1" -frames:v 1 -y "$ROOT/base.jpg"
ffmpeg -v error -f lavfi -i "testsrc=duration=1:size=320x240:rate=10" -y "$ROOT/IMG_0001.mov"
echo "fake heic" > "$ROOT/IMG_0001.heic"
cp "$ROOT/base.jpg" "$ROOT/plain.jpg"

# Build a pixel_legacy Motion Photo: JPEG + XMP APP1 + appended MP4.
python3 - "$ROOT" <<'EOF'
import struct, sys
root = sys.argv[1]
jpg = open(root + "/base.jpg", "rb").read()
mp4 = open(root + "/embedded.mp4", "rb").read()
xmp = b'http://ns.adobe.com/xap/1.0/\x00' + (
    '<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF><rdf:Description '
    'xmlns:GCamera="http://ns.google.com/photos/1.0/camera/" '
    'GCamera:MicroVideo="1" GCamera:MicroVideoOffset="%d"/>'
    '</rdf:RDF></x:xmpmeta>' % len(mp4)).encode()
app1 = b'\xff\xe1' + struct.pack(">H", len(xmp) + 2) + xmp
open(root + "/motion.jpg", "wb").write(jpg[:2] + app1 + jpg[2:] + mp4)
EOF
rm "$ROOT/base.jpg"
mv "$ROOT/embedded.mp4" /tmp/m3-embedded.mp4   # keep out of the indexed tree
cp "$ROOT/IMG_0001.mov" /tmp/m3-comp.mov

echo "== starting server =="
"$BIN" -root "$ROOT" -port "$PORT" &
SRV_PID=$!
sleep 1
BASE="http://127.0.0.1:$PORT"

echo "== waiting for scan =="
for i in $(seq 1 40); do
  OUT=$(curl -sf "$BASE/api/gallery/scan")
  case "$OUT" in *'"scanning":false'*) break;; esac
  sleep 0.5
done
echo "scan: $OUT"
echo "$OUT" | grep -q '"indexed":4' || { echo "FAIL: expected 4 indexed"; exit 1; }

echo "== gallery live flags =="
G=$(curl -sf "$BASE/api/gallery")
echo "$G"
echo "$G" | grep -q '"path":"/motion.jpg"[^}]*"isLivePhoto":true,"liveType":"pixel_legacy"' || { echo "FAIL: motion.jpg flags"; exit 1; }
echo "$G" | grep -q '"path":"/IMG_0001.heic"[^}]*"isLivePhoto":true,"liveType":"ios"' || { echo "FAIL: heic pairing"; exit 1; }
echo "$G" | grep -q '"path":"/plain.jpg"[^}]*"isLivePhoto":false' || { echo "FAIL: plain.jpg flagged"; exit 1; }

echo "== /api/livephoto embedded: byte-exact =="
curl -sf -o /tmp/m3gen "$BASE/api/livephoto/motion.jpg" -D /tmp/m3hdr
grep -qi "content-type: video/mp4" /tmp/m3hdr || { echo "FAIL: content-type"; exit 1; }
cmp /tmp/m3-embedded.mp4 /tmp/m3gen || { echo "FAIL: extracted bytes differ"; exit 1; }
echo "  byte-exact OK"

echo "== /api/livephoto Range 206 =="
CODE=$(curl -s -o /tmp/m3gen -w "%{http_code}" -H "Range: bytes=0-99" "$BASE/api/livephoto/motion.jpg")
[ "$CODE" = "206" ] || { echo "FAIL: range $CODE"; exit 1; }
[ "$(wc -c < /tmp/m3gen)" = "100" ] || { echo "FAIL: range size"; exit 1; }

echo "== /api/livephoto iOS companion =="
curl -sf -o /tmp/m3gen "$BASE/api/livephoto/IMG_0001.heic"
cmp /tmp/m3-comp.mov /tmp/m3gen || { echo "FAIL: ios bytes differ"; exit 1; }
echo "  ios OK"

echo "== non-live 404 =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/livephoto/plain.jpg")
[ "$CODE" = "404" ] || { echo "FAIL: expected 404, got $CODE"; exit 1; }

echo "== traversal 403 =="
CODE=$(curl -s -o /dev/null -w "%{http_code}" --path-as-is "$BASE/api/livephoto/../../etc/passwd")
[ "$CODE" = "403" ] || { echo "FAIL: expected 403, got $CODE"; exit 1; }

echo "ALL M3 SMOKE TESTS PASSED"
