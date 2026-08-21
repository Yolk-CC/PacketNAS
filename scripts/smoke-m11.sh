#!/usr/bin/env bash
# smoke-m11.sh — M11 服务端人脸识别冒烟测试。
#
# 始终覆盖：降级路径（无运行库/模型时 API 的 available=false / 503）。
# 可选覆盖（网络可达时自动启用）：真实 buffalo_s 模型 + 3 张人脸图
# （2 张同一人）→ 识别 → 聚类 2 人物 → 命名 → 导出 → 删库 → 导入 →
# 人物关系保留。模型/图片下载失败时自动跳过真模型部分（CI 里靠缓存）。
#
# 用法: [M11_CACHE=/path/cache] bash scripts/smoke-m11.sh
set -u
cd "$(dirname "$0")/.."
ROOT_DIR=$(pwd)

PASS=0; FAIL=0; SKIPPED=0
ok()   { echo "  [OK] $1"; PASS=$((PASS+1)); }
bad()  { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }
skip() { echo "  [SKIP] $1"; SKIPPED=$((SKIPPED+1)); }

GO=${GO:-go}
PORT=${PORT:-18311}
BASE="http://127.0.0.1:$PORT"
WORK=$(mktemp -d)
trap 'kill $SRV_PID 2>/dev/null; rm -rf "$WORK"' EXIT

echo "== build =="
$GO build -o "$WORK/pocket-nas" ./cmd/pocket-nas || { echo "build failed"; exit 1; }
BIN="$WORK/pocket-nas"
# /mnt/agents 等 noexec 挂载下无法直接运行，拷到 /tmp
if ! "$BIN" -version >/dev/null 2>&1; then
  cp "$BIN" /tmp/pocket-nas-m11-smoke && BIN=/tmp/pocket-nas-m11-smoke
fi

fetch() { # url dst
  for u in "$1" "https://ghfast.top/$1"; do
    if curl -fsSL --max-time 300 -o "$2" "$u" 2>/dev/null && [ -s "$2" ]; then return 0; fi
  done
  return 1
}

start_server() { # root
  "$BIN" -root "$1" -addr 127.0.0.1 -port $PORT >"$WORK/server.log" 2>&1 &
  SRV_PID=$!
  for i in $(seq 1 50); do
    curl -fsS "$BASE/api/system/info" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  echo "server failed to start"; cat "$WORK/server.log"; exit 1
}
stop_server() { kill $SRV_PID 2>/dev/null; wait $SRV_PID 2>/dev/null; }

echo "== 1. 降级路径（无模型/运行库）=="
DEG="$WORK/degraded"; mkdir -p "$DEG"
start_server "$DEG"
ST=$(curl -fsS "$BASE/api/faces/status")
echo "$ST" | grep -q '"available":false' && ok "status available=false" || bad "status: $ST"
CODE=$(curl -s -o "$WORK/body.json" -w '%{http_code}' "$BASE/api/faces/persons")
[ "$CODE" = 503 ] && grep -q faces_unavailable "$WORK/body.json" \
  && ok "persons → 503 faces_unavailable" || bad "persons: $CODE $(cat $WORK/body.json)"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/faces/scan")
[ "$CODE" = 503 ] && ok "scan → 503" || bad "scan: $CODE"
stop_server

echo "== 2. 真实模型端到端（可选）=="
CACHE=${M11_CACHE:-$WORK/cache}; mkdir -p "$CACHE"
MODELS="$CACHE/models"; mkdir -p "$MODELS"

ORT_TGZ="$CACHE/onnxruntime-linux-x64-1.17.3.tgz"
[ -f "$ORT_TGZ" ] || fetch "https://github.com/microsoft/onnxruntime/releases/download/v1.17.3/onnxruntime-linux-x64-1.17.3.tgz" "$ORT_TGZ"
BS_ZIP="$CACHE/buffalo_s.zip"
[ -f "$BS_ZIP" ] || fetch "https://github.com/deepinsight/insightface/releases/download/v0.7/buffalo_s.zip" "$BS_ZIP"

IMGDIR="$CACHE/images"; mkdir -p "$IMGDIR"
for img in obama.jpg obama2.jpg biden.jpg; do
  [ -f "$IMGDIR/$img" ] || fetch "https://raw.githubusercontent.com/ageitgey/face_recognition/master/examples/$img" "$IMGDIR/$img"
done

if [ ! -s "$ORT_TGZ" ] || [ ! -s "$BS_ZIP" ] || [ ! -s "$IMGDIR/obama.jpg" ] || [ ! -s "$IMGDIR/obama2.jpg" ] || [ ! -s "$IMGDIR/biden.jpg" ]; then
  skip "模型或测试图下载失败（网络不可达），真模型部分跳过"
else
  tar xzf "$ORT_TGZ" -C "$CACHE" --strip-components=2 \
    onnxruntime-linux-x64-1.17.3/lib/libonnxruntime.so.1.17.3 2>/dev/null
  cp "$CACHE/libonnxruntime.so.1.17.3" "$MODELS/libonnxruntime.so"
  (cd "$CACHE" && unzip -oq buffalo_s.zip det_500m.onnx w600k_mbf.onnx -d "$MODELS")

  R1="$WORK/photos"; mkdir -p "$R1/.pocketnas"
  cp "$IMGDIR"/obama.jpg "$IMGDIR"/obama2.jpg "$IMGDIR"/biden.jpg "$R1/"
  cp -r "$MODELS"/* "$R1/.pocketnas/" 2>/dev/null; mkdir -p "$R1/.pocketnas/models"
  cp "$MODELS"/* "$R1/.pocketnas/models/"

  start_server "$R1"
  # 等相册扫描完成并触发人脸队列
  for i in $(seq 1 100); do
    ST=$(curl -fsS "$BASE/api/faces/status")
    echo "$ST" | grep -q '"available":true' && break
    sleep 0.3
  done
  echo "$ST" | grep -q '"available":true' && ok "engine available" || { bad "engine: $ST"; stop_server; exit 1; }

  curl -fsS -X POST "$BASE/api/faces/scan" >/dev/null
  for i in $(seq 1 300); do
    ST=$(curl -fsS "$BASE/api/faces/status")
    echo "$ST" | grep -q '"pending":0' && echo "$ST" | grep -q '"scanning":false' && break
    sleep 0.5
  done
  FT=$(echo "$ST" | grep -o '"facesTotal":[0-9]*' | cut -d: -f2)
  [ "${FT:-0}" -ge 3 ] && ok "识别出 $FT 张人脸" || bad "facesTotal=$FT ($ST)"

  PERSONS=$(curl -fsS "$BASE/api/faces/persons")
  NP=$(echo "$PERSONS" | grep -o '"id"' | wc -l)
  [ "$NP" = 2 ] && ok "聚类为 2 个人物" || bad "persons=$NP: $PERSONS"

  # obama/obama2 应同组：找到含 2 张照片的人物并命名
  PID=$(python3 -c "
import json,sys
ps=json.loads('''$PERSONS''')
big=max(ps,key=lambda p:p['faceCount'])
print(big['id'])")
  PHOTOS=$(curl -fsS "$BASE/api/faces/persons/$PID/photos")
  NPH=$(echo "$PHOTOS" | grep -o '"path"' | wc -l)
  [ "$NPH" = 2 ] && ok "同一人聚到 2 张照片" || bad "person $PID photos=$NPH: $PHOTOS"

  curl -fsS -X PUT "$BASE/api/faces/persons/$PID" -H 'Content-Type: application/json' \
    -d '{"name":"Obama"}' >/dev/null && ok "命名 Obama" || bad "命名失败"

  curl -fsS "$BASE/api/faces/export" -o "$WORK/export.json.gz" && [ -s "$WORK/export.json.gz" ] \
    && ok "导出" || bad "导出失败"

  CROP_FID=$(python3 -c "
import json,gzip
d=json.load(gzip.open('$WORK/export.json.gz'))
print(d['faces'][0]['id'])")
  CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/faces/crop/$CROP_FID")
  [ "$CODE" = 200 ] && ok "人脸裁剪图" || bad "crop: $CODE"
  stop_server

  echo "== 3. 迁移导入（不重新识别）=="
  rm -f "$R1/.pocketnas/faces.db"*
  start_server "$R1"
  RES=$(curl -fsS -X POST "$BASE/api/faces/import" -H 'Content-Type: application/gzip' \
    --data-binary @"$WORK/export.json.gz")
  echo "$RES" | grep -q '"faces":3' && ok "导入 3 张人脸: $RES" || bad "导入: $RES"
  # 等待后台哈希链接
  FOUND=0
  for i in $(seq 1 100); do
    P2=$(curl -fsS "$BASE/api/faces/persons")
    if echo "$P2" | grep -q '"Obama"'; then
      PID2=$(python3 -c "
import json
ps=json.loads('''$P2''')
print([p['id'] for p in ps if p.get('name')=='Obama'][0])")
      PH2=$(curl -fsS "$BASE/api/faces/persons/$PID2/photos")
      NPH2=$(echo "$PH2" | grep -o '"path"' | wc -l)
      [ "$NPH2" = 2 ] && { FOUND=1; break; }
    fi
    sleep 0.3
  done
  [ "$FOUND" = 1 ] && ok "导入后人物关系保留（Obama=2 张照片）" || bad "迁移验证失败: $P2"
  stop_server
fi

echo
echo "smoke-m11: pass=$PASS fail=$FAIL skip=$SKIPPED"
[ "$FAIL" = 0 ]
