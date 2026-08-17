#!/bin/bash
# M7 集成冒烟测试：多共享路径 + 设置 API
# 用法: bash scripts/smoke-m7.sh [二进制路径]
set -u
BIN=${1:-./bin/pocket-nas}
PORT=18107
ROOT=/tmp/pocketnas-m7-root
SHARE_A=/tmp/pocketnas-m7-shareA
SHARE_B=/tmp/pocketnas-m7-shareB

echo "== 准备数据 =="
rm -rf $ROOT $SHARE_A $SHARE_B
mkdir -p $ROOT $SHARE_A/sub $SHARE_B
echo "photo" > $SHARE_A/pic.jpg
echo "doc"  > $SHARE_B/readme.txt
echo "secret" > $ROOT/outside.txt   # 根下、共享之外

echo "== 启动服务 =="
$BIN -root $ROOT -port $PORT > /tmp/pocketnas-m7.log 2>&1 &
SRV=$!
trap "kill $SRV 2>/dev/null" EXIT
sleep 1

BASE=http://127.0.0.1:$PORT
FAIL=0
check() { # check <描述> <期望> <实际>
  if [ "$2" == "$3" ]; then echo "  PASS: $1"; else echo "  FAIL: $1 (期望$2 实际$3)"; FAIL=1; fi
}

echo "== 1. 初始为兼容模式 =="
curl -s "$BASE/api/settings/shares" | grep -q '"legacy":true' && echo "  PASS: legacy=true" || { echo "  FAIL: legacy 初始状态"; FAIL=1; }
curl -s "$BASE/api/files?path=/" | grep -q 'outside.txt' && echo "  PASS: 兼容模式可见整根" || { echo "  FAIL: 兼容模式根列表"; FAIL=1; }

echo "== 2. PUT 两个共享 =="
CODE=$(curl -s -o /tmp/m7-put.json -w '%{http_code}' -X PUT "$BASE/api/settings/shares" \
  -d "{\"shares\":[{\"name\":\"photos\",\"path\":\"$SHARE_A\"},{\"name\":\"docs\",\"path\":\"$SHARE_B\"}]}")
check "PUT shares 200" 200 $CODE
grep -q '"legacy":false' /tmp/m7-put.json && echo "  PASS: legacy=false" || { echo "  FAIL: PUT 响应"; FAIL=1; }
[ -f $ROOT/.pocketnas/settings.json ] && echo "  PASS: settings.json 已写入" || { echo "  FAIL: settings.json 未写入"; FAIL=1; }

echo "== 3. 共享模式根列表 =="
LIST=$(curl -s "$BASE/api/files?path=/")
echo "$LIST" | grep -q '"photos"' && echo "$LIST" | grep -q '"docs"' && echo "  PASS: 只见两个共享" || { echo "  FAIL: $LIST"; FAIL=1; }
echo "$LIST" | grep -q 'outside.txt' && { echo "  FAIL: 共享外文件泄漏"; FAIL=1; } || echo "  PASS: 共享外文件不可见"

echo "== 4. 共享内访问 =="
curl -s "$BASE/api/files?path=/photos" | grep -q 'pic.jpg' && echo "  PASS: /photos 列表" || { echo "  FAIL: /photos 列表"; FAIL=1; }
curl -s "$BASE/api/files?path=/photos/sub" > /dev/null && check "子目录 200" 200 $(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/files?path=/photos/sub")

echo "== 5. 越权访问 =="
check "不存在共享 404" 404 $(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/files?path=/etc")
check ".. 穿越 403" 403 $(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/files?path=/photos/..")
check ".. 越共享 403" 403 $(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/files?path=/photos/../docs")

echo "== 6. 非法共享 400 invalid_share =="
CODE=$(curl -s -o /tmp/m7-bad.json -w '%{http_code}' -X PUT "$BASE/api/settings/shares" \
  -d "{\"shares\":[{\"name\":\"a/b\",\"path\":\"$SHARE_A\"}]}")
check "非法名称 400" 400 $CODE
grep -q '"invalid_share"' /tmp/m7-bad.json && echo "  PASS: invalid_share 错误码" || { echo "  FAIL: $(cat /tmp/m7-bad.json)"; FAIL=1; }
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/api/settings/shares" \
  -d '{"shares":[{"name":"ghost","path":"/no/such/dir"}]}')
check "不存在路径 400" 400 $CODE

echo "== 7. browse 端点 =="
curl -s "$BASE/api/system/browse?path=$ROOT" | grep -q '"dirs"' && echo "  PASS: browse 根" || { echo "  FAIL: browse"; FAIL=1; }
BROWSE=$(curl -s "$BASE/api/system/browse?path=$(dirname $SHARE_A)")
echo "$BROWSE" | grep -q "$(basename $SHARE_A)" && echo "  PASS: browse 列出共享目录" || { echo "  FAIL: $BROWSE"; FAIL=1; }
check "browse 文件 400" 400 $(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/system/browse?path=$SHARE_A/pic.jpg")

echo "== 8. PUT 空数组恢复兼容模式 =="
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/api/settings/shares" -d '{"shares":[]}')
check "清空 200" 200 $CODE
curl -s "$BASE/api/settings/shares" | grep -q '"legacy":true' && echo "  PASS: 恢复 legacy" || { echo "  FAIL: 恢复 legacy"; FAIL=1; }
curl -s "$BASE/api/files?path=/" | grep -q 'outside.txt' && echo "  PASS: 兼容模式整根恢复" || { echo "  FAIL: 兼容模式恢复"; FAIL=1; }

echo "== 9. 重启后共享持久化 =="
curl -s -X PUT "$BASE/api/settings/shares" \
  -d "{\"shares\":[{\"name\":\"photos\",\"path\":\"$SHARE_A\"}]}" > /dev/null
kill $SRV; wait $SRV 2>/dev/null
$BIN -root $ROOT -port $PORT > /tmp/pocketnas-m7-2.log 2>&1 &
SRV=$!
sleep 1
curl -s "$BASE/api/settings/shares" | grep -q '"photos"' && echo "  PASS: 重启后共享仍在" || { echo "  FAIL: 持久化"; FAIL=1; }
curl -s "$BASE/api/files?path=/" | grep -q 'outside.txt' && { echo "  FAIL: 重启后泄漏整根"; FAIL=1; } || echo "  PASS: 重启后仍是共享模式"

echo
if [ $FAIL -eq 0 ]; then echo "== M7 冒烟全部通过 =="; else echo "== M7 冒烟存在失败 =="; exit 1; fi
