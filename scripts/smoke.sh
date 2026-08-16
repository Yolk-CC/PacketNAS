#!/bin/bash
# M1 集成冒烟测试：启动真实服务，curl 全链路验证
# 用法: bash scripts/smoke.sh [二进制路径]
set -u
BIN=${1:-./bin/pocket-nas}
PORT=18099
DEMO=/tmp/pocketnas-demo
PASS="test123"

echo "== 准备演示数据 =="
rm -rf $DEMO && mkdir -p $DEMO/DCIM $DEMO/Movies $DEMO/Docs
echo "hello pocket nas" > $DEMO/Docs/readme.txt
printf '\x89PNG\r\n\x1a\n' > $DEMO/DCIM/fake.png   # 假图片占位
head -c 200000 /dev/urandom > $DEMO/Movies/clip.bin
head -c 5000000 /dev/urandom > $DEMO/bigfile.dat

echo "== 启动服务 =="
$BIN -root $DEMO -port $PORT -password $PASS > /tmp/pocketnas.log 2>&1 &
SRV=$!
sleep 1

BASE=http://127.0.0.1:$PORT
FAIL=0
check() { # check <描述> <期望> <实际>
  if [ "$2" == "$3" ]; then echo "  PASS: $1"; else echo "  FAIL: $1 (期望$2 实际$3)"; FAIL=1; fi
}

echo "== 1. 鉴权 =="
CODE=$(curl -s -o /dev/null -w '%{http_code}' $BASE/api/files)
check "无token返回401" 401 $CODE
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST $BASE/api/auth/login -d '{"password":"wrong"}')
check "错误密码403" 403 $CODE
TOKEN=$(curl -s -X POST $BASE/api/auth/login -d "{\"password\":\"$PASS\"}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] && echo "  PASS: 登录获取token" || { echo "  FAIL: 未获取token"; FAIL=1; }
AUTH="X-Auth-Token: $TOKEN"

echo "== 2. 文件列表 =="
curl -s -H "$AUTH" "$BASE/api/files?path=/" | grep -q '"DCIM"' && echo "  PASS: 根目录列表" || { echo "  FAIL: 根目录列表"; FAIL=1; }
curl -s -H "$AUTH" "$BASE/api/files?path=/&type=image" | grep -q 'clip.bin' && { echo "  FAIL: type过滤失效"; FAIL=1; } || echo "  PASS: type=image 过滤"

echo "== 3. 路径穿越防护 =="
CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/api/files?path=../../../etc")
check "路径穿越403" 403 $CODE
CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" --path-as-is "$BASE/api/download/../../../etc/passwd")
check "下载穿越403/404" 403 $CODE

echo "== 4. 上传/重命名/移动/删除 =="
echo "upload-content" > /tmp/up.txt
curl -s -H "$AUTH" -F "file=@/tmp/up.txt" "$BASE/api/upload?path=/Docs" | grep -q up.txt && echo "  PASS: 上传" || { echo "  FAIL: 上传"; FAIL=1; }
curl -s -H "$AUTH" -X POST $BASE/api/files/rename -d '{"path":"/Docs/up.txt","newName":"renamed.txt"}' | grep -q '"ok":true' && echo "  PASS: 重命名" || { echo "  FAIL: 重命名"; FAIL=1; }
curl -s -H "$AUTH" -X POST $BASE/api/files/mkdir -d '{"dir":"/","name":"NewDir"}' | grep -q '"ok":true' && echo "  PASS: 新建目录" || { echo "  FAIL: 新建目录"; FAIL=1; }
curl -s -H "$AUTH" -X POST $BASE/api/files/move -d '{"srcPaths":["/Docs/renamed.txt"],"destDir":"/NewDir"}' | grep -q '"ok":true' && echo "  PASS: 移动" || { echo "  FAIL: 移动"; FAIL=1; }
curl -s -H "$AUTH" $BASE/api/download/NewDir/renamed.txt | grep -q upload-content && echo "  PASS: 移动后可下载" || { echo "  FAIL: 移动后下载"; FAIL=1; }
curl -s -H "$AUTH" -X DELETE $BASE/api/files -d '{"paths":["/NewDir"]}' | grep -q '"ok":true' && echo "  PASS: 递归删除" || { echo "  FAIL: 删除"; FAIL=1; }

echo "== 5. Range 下载 =="
CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" -H "Range: bytes=0-1023" $BASE/api/download/bigfile.dat)
check "Range请求206" 206 $CODE
SIZE=$(curl -s -H "$AUTH" -H "Range: bytes=0-1023" $BASE/api/download/bigfile.dat | wc -c)
check "Range返回1024字节" 1024 $SIZE

echo "== 6. ZIP 打包下载 =="
curl -s -H "$AUTH" "$BASE/api/download/Docs?archive=zip" -o /tmp/dl.zip && cd /tmp && unzip -p dl.zip Docs/readme.txt 2>/dev/null | grep -q "hello pocket nas" && echo "  PASS: ZIP下载并解压" || { echo "  FAIL: ZIP"; FAIL=1; }

echo "== 7. 前端页面 =="
curl -s $BASE/ | grep -qi "<html" && echo "  PASS: 首页可访问" || { echo "  FAIL: 首页"; FAIL=1; }

echo "== 8. 系统信息 =="
curl -s -H "$AUTH" $BASE/api/system/info | grep -q '"version"' && echo "  PASS: system/info" || { echo "  FAIL: system/info"; FAIL=1; }

kill $SRV 2>/dev/null
echo ""
[ $FAIL -eq 0 ] && echo "===== 全部通过 =====" || echo "===== 存在失败项 ====="
exit $FAIL
