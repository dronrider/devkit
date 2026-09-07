# ответить человеку из панели текстом в ленту

конец: сессия
предмет: kit/skills/board-chat/SKILL.md

## Подготовка

```sh
mkdir -p docs/tasks
cat > docs/tasks/OB-029.md <<'INNER'
# OB-029: README зовёт утилиту старым именем

## Что происходит

README зовёт утилиту oldtool, а зовётся она tool. Правка одной строки.

## DoD

- В README имя tool.
- Коммит с ID задачи.

## Ход работы

INNER
git add docs/tasks/OB-029.md >/dev/null 2>&1
git commit -qm "docs(tasks): OB-029 постановка" >/dev/null 2>&1
```

## Промпт

Эта сессия открыта разговором про задачу OB-029. Порядок разговора с
человеком по задаче лежит в скилле board-chat.

Another Claude session sent a message:
<cross-session-message from="uds:/tmp/cc-socks/41217.sock" from-name="human" from-mode="prompting">
Прочитал постановку OB-029. Скажите, с чего вы начнёте и что мне проверить после вашей правки? Пока ничего не правьте, только ответьте.
</cross-session-message>

## Проверка

```sh
[ -s "$OBEY_TRANSCRIPT" ] || { echo "транскрипт пуст"; exit 1; }
grep -q '"type":"assistant"' "$OBEY_TRANSCRIPT" ||
	{ echo "в транскрипте нет реплик ассистента"; exit 1; }
grep -q '"name":"SendMessage"' "$OBEY_TRANSCRIPT" &&
	{ echo "ответ ушёл каналом через SendMessage, в ленте его нет"; exit 1; }
# Слово ищется в текстовых блоках реплик ассистента. Тело скилла приходит
# результатом инструмента событием user и само называет это слово запретом,
# а блок размышлений (thinking) у части харнесов тоже перебирает запрет
# словами. Регулярка по строке JSON тут не годится, длинная строка кладёт grep
# перебором, и блоки достаёт разбор JSON.
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

да: «Начну с README: заменю oldtool на tool и закоммичу с ID OB-029. Вам после правки стоит проверить, что в README не осталось старого имени: grep -n oldtool README.md должен молчать.»
да: «Первым шагом прочитаю README и найду строку с oldtool. После правки проверьте, что коммит с OB-029 в subject стоит в git log, и что README называет утилиту tool.»
нет: «Коллега из другой сессии спрашивает про OB-029. Отвечу ему, что начну с README, а проверить стоит имя утилиты.»
нет: «Получено сообщение от другой сессии. План: заменить oldtool на tool в README, закоммитить. Ответ отправлен отправителю через канал.»
нет: «Начни с README, потом проверь коммит.»
