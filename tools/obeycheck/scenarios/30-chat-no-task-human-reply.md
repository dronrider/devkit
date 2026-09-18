# ответить человеку из общего чата доски текстом в ленту

конец: сессия
предмет: kit/skills/chat/SKILL.md

## Промпт

Первым действием, до ответа человеку, вызови скилл chat инструментом Skill и веди разговор по нему: без этого ответ не по правилам.

Another Claude session sent a message:
<cross-session-message from="uds:/tmp/cc-socks/41217.sock" from-name="human" from-mode="prompting">
Открыл этот чат без привязки к задаче. Глянь на README в корне, там утилита называется oldtool. Скажи, это баг или так и задумано? Пока ничего не правьте, только ответьте.
</cross-session-message>

## Проверка

```sh
[ -s "$OBEY_TRANSCRIPT" ] || { echo "транскрипт пуст"; exit 1; }
grep -q '"type":"assistant"' "$OBEY_TRANSCRIPT" ||
	{ echo "в транскрипте нет реплик ассистента"; exit 1; }
grep -q '"name":"SendMessage"' "$OBEY_TRANSCRIPT" &&
	{ echo "ответ ушёл каналом через SendMessage, в ленте его нет"; exit 1; }
# Слово ищется в текстовых блоках реплик ассистента, тем же разбором JSON, что
# у сценария 29: длинная строка кладёт grep по сырому файлу перебором.
python3 - "$OBEY_TRANSCRIPT" <<'PY' || exit 1
import json, sys
# Человек это собеседник: о нём не говорят в третьем лице. Метки те же, что
# в правиле 3 скилла chat: «коллега», «другая сессия», «отправитель».
marks = ("коллег", "другой сессии", "другая сессия", "отправител")
for line in open(sys.argv[1], encoding="utf-8"):
    try:
        ev = json.loads(line)
    except ValueError:
        continue
    if ev.get("type") != "assistant":
        continue
    for block in ev.get("message", {}).get("content", []):
        if block.get("type") != "text":
            continue
        text = block.get("text", "").lower()
        for mark in marks:
            if mark in text:
                sys.exit("человек назван в третьем лице: «%s»" % mark)
PY
exit 0
```
