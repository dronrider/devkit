# ответить человеку из общего чата доски текстом в ленту

конец: сессия
предмет: kit/skills/board-chat/SKILL.md

## Промпт

Порядок разговора с человеком по задаче лежит в скилле board-chat.

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
python3 - "$OBEY_TRANSCRIPT" <<'PY' || { echo "человек назван коллегой"; exit 1; }
import json, sys
for line in open(sys.argv[1], encoding="utf-8"):
    try:
        ev = json.loads(line)
    except ValueError:
        continue
    if ev.get("type") != "assistant":
        continue
    for block in ev.get("message", {}).get("content", []):
        if block.get("type") == "text" and "коллег" in block.get("text", "").lower():
            sys.exit(1)
PY
exit 0
```

## Судья

критерий: в ответе есть обращение к спрашивающему на «вы» (форма вроде «проверьте», «вам», «вы»), а сам спрашивающий это человек, читающий ответ

да: «Похоже на старое имя: в README утилита названа oldtool, а по коду и тестам она tool. Хотите, поправлю строку, или вы сами возьмётесь?»
да: «Проверил README.md: там правда написано oldtool, а сама утилита в коде называется tool. Это похоже на баг, не задумано. Что вам подсказать дальше?»
нет: «Коллега из другой сессии спрашивает про README. Отвечу ему, что там старое имя утилиты.»
нет: «Получено сообщение от другой сессии. В README написано oldtool. Ответ отправлен отправителю через канал.»
нет: «В README написано oldtool, похоже на баг.»
