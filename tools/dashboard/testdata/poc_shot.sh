#!/bin/sh
# Стенд вложения чата (ветка poc-chat): картинка после загрузки обязана лежать
# на диске байт в байт такой же, какой её вставили, и отдаваться ручкой без
# порчи. Пользователь видел «битое изображение», и без побайтной сверки спорить
# об этом было нечем.
#
# Зовётся: sh testdata/poc_shot.sh <база> <токен> <сессия>
set -e
BASE=${1:-http://127.0.0.1:7131}
TOKEN=${2:-poc-chat}
SID=${3:?нужен id сессии}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
JAR="$TMP/jar"

python3 - "$TMP/src.png" <<'PY'
import struct, sys, zlib
# Шумная картинка: сжатием не схлопнется, любая порча видна побайтно.
w = h = 64
raw = b"".join(b"\x00" + bytes((x * 7 + y * 13) % 256 for x in range(w * 3)) for y in range(h))
def chunk(t, d):
    c = t + d
    return struct.pack(">I", len(d)) + c + struct.pack(">I", zlib.crc32(c) & 0xffffffff)
png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
       + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))
open(sys.argv[1], "wb").write(png)
PY

curl -s -c "$JAR" -X POST -H 'Content-Type: application/json' \
  -d "{\"token\": \"$TOKEN\"}" "$BASE/api/login" >/dev/null
python3 - "$TMP/src.png" "$TMP/body.json" <<'PY'
import base64, json, sys
data = base64.b64encode(open(sys.argv[1], "rb").read()).decode()
json.dump({"kind": "image/png", "data": data}, open(sys.argv[2], "w"))
PY
OUT=$(curl -s -b "$JAR" -H "Origin: $BASE" -H 'Content-Type: application/json' \
  -X POST --data-binary @"$TMP/body.json" "$BASE/api/projects/devkit/chats/$SID/shot")
NAME=$(printf '%s' "$OUT" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("name",""))')
PATHF=$(printf '%s' "$OUT" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("path",""))')
[ -n "$NAME" ] || { echo "вложение не легло: $OUT"; exit 1; }

cmp "$TMP/src.png" "$PATHF" || { echo "файл на диске разошёлся с исходником"; exit 1; }
curl -s -b "$JAR" -o "$TMP/got.png" "$BASE/api/projects/devkit/chats/$SID/shot?name=$NAME"
cmp "$TMP/src.png" "$TMP/got.png" || { echo "отданная ручкой картинка разошлась с исходником"; exit 1; }
# od разделяет байты переменным числом пробелов, поэтому магия сверяется питоном.
python3 -c 'import sys; sys.exit(0 if open(sys.argv[1],"rb").read(8)==b"\x89PNG\r\n\x1a\n" else 1)' "$TMP/got.png" \
  || { echo "магия PNG потерялась"; exit 1; }
rm -f "$PATHF"
echo "вложение чата: файл на диске и отданный ручкой байт в байт равны исходнику, магия PNG цела"
