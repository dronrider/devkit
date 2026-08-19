#!/bin/sh
# Стенд боковых журналов субагента (ветка poc-chat).
#
# Работа субагента лежит не в транскрипте сессии, а в боковом файле
# subagents/agent-<id>.jsonl рядом с ним, и записи там помечены isSidechain,
# который разбор транскрипта отсеивает. Стенд сверяет на настоящем боковом
# файле три вещи: что записи субагента вообще доезжают до ленты, что ни один
# класс не теряется по дороге (текст ответа модели и размышления) и что
# хронология окна не порвана. Текста у размышлений в боковых файлах нет вовсе,
# харнес его туда не пишет, поэтому проверяется длительность: по ней лента и
# подписывает такую запись.
#
# Хронология тут не украшение. Боковой журнал сваливался в хвост ленты целиком,
# а у субагента, которого продолжают через SendMessage не первый день, записей
# тысячи: хвостовое окно состояло из него одного, а разговор человека с сессией
# уезжал за эту тысячу вверх. На экране это читалось как «весь чат в одном
# пузыре».
#
# Зовётся: sh testdata/poc_subagent.sh <база> <токен> <проект> <сессия>
set -e
BASE=${1:-http://127.0.0.1:7131}
TOKEN=${2:-poc-chat}
PROJ=${3:-devkit}
SID=${4:?нужен id сессии}
JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

DIR="$HOME/.claude/projects"
LOG=$(find "$DIR" -type d -name subagents -path "*$SID*" -exec sh -c 'ls -t "$1"/agent-*.jsonl 2>/dev/null | head -1' _ {} \; | head -1)
[ -n "$LOG" ] || { echo "боковых журналов у сессии нет, стенд пропущен"; exit 0; }

python3 - "$LOG" <<'PY'
import json, sys
from collections import Counter
c = Counter()
for ln in open(sys.argv[1], errors="replace"):
    try:
        r = json.loads(ln)
    except ValueError:
        continue
    cont = r.get("message", {}).get("content")
    if not isinstance(cont, list):
        continue
    for b in cont:
        if isinstance(b, dict):
            c[b.get("type")] += 1
print("в боковом журнале: text=%d thinking=%d tool_use=%d"
      % (c["text"], c["thinking"], c["tool_use"]))
PY

curl -s -c "$JAR" -X POST -H 'Content-Type: application/json' \
  -d "{\"token\": \"$TOKEN\"}" "$BASE/api/login" >/dev/null
curl -s -b "$JAR" "$BASE/api/projects/$PROJ/sessions/$SID?n=500" \
  | python3 -c '
import json, sys
d = json.load(sys.stdin)
items = d.get("items", [])
sub = [i for i in items if i.get("sub")]
kinds = {}
for i in sub:
    kinds[i["role"]] = kinds.get(i["role"], 0) + 1
print("в ленте от субагента: %s" % kinds)
if not sub:
    print("записей субагента в ленте нет вовсе")
    raise SystemExit(1)
# Ни один класс не теряется по дороге: и ответы модели, и размышления, и
# вызовы инструментов обязаны доехать.
for role in ("assistant", "thinking", "tool"):
    if not kinds.get(role):
        print("класс записей потерялся: %s" % role)
        raise SystemExit(1)
# Текста у размышлений в боковом журнале нет, и подписать их можно только
# длительностью: без неё в ленте осталась бы пустая строка.
bad = [i for i in sub if i["role"] == "thinking" and not i.get("text") and not i.get("spent")]
if bad:
    print("размышлений без текста и без длительности: %d" % len(bad))
    raise SystemExit(1)
# Записи ленты идут по времени, а не кусками журналов: слияние по меткам это
# и есть хронология разговора.
prev, back = "", 0
for i in items:
    at = i.get("time") or ""
    if at and prev and at < prev:
        back += 1
    if at:
        prev = at
if back:
    print("записей не по времени: %d" % back)
    raise SystemExit(1)
# Блок это подряд идущие записи одного бокового журнала: их режет всякая
# не-sub запись. Одним блоком на всё окно лента быть не должна.
blocks, prev = 0, ""
for i in items:
    cur = i.get("sub") or ""
    if cur and cur != prev:
        blocks += 1
    prev = cur
plain = len(items) - len(sub)
print("окно: записей %d, из них от субагентов %d, блоков %d, реплик сессии %d"
      % (len(items), len(sub), blocks, plain))
if blocks < 2 and not plain:
    print("всё окно ленты это один блок субагента: хронология не слита")
    raise SystemExit(1)
print("боковой журнал субагента: все классы записей в ленте, размышления с длительностью, "
      "окно по времени и разговор виден между блоками")
'
