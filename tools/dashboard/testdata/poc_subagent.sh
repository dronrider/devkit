#!/bin/sh
# Стенд боковых журналов субагента (ветка poc-chat).
#
# Работа субагента лежит не в транскрипте сессии, а в боковом файле
# subagents/agent-<id>.jsonl рядом с ним, и записи там помечены isSidechain,
# который разбор транскрипта отсеивает. Стенд сверяет две вещи на настоящем
# боковом файле: что записи субагента вообще доезжают до ленты и что ни один
# класс не теряется по дороге (текст ответа модели и размышления). Текста у
# размышлений в боковых файлах нет вовсе, харнес его туда не пишет, поэтому
# проверяется длительность: по ней лента и подписывает такую запись.
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
print("боковой журнал субагента: все классы записей в ленте, размышления с длительностью")
'
