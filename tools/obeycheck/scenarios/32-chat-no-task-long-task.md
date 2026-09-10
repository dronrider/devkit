# долгое дело в чате без задачи уходит субагенту

конец: сессия
предмет: kit/skills/board-chat/SKILL.md

## Промпт

План работ веди командой agentctl plan. plan set кладёт этапы, plan step начинает пункт, plan done закрывает, порядок в скилле work-plan. Долгие дела (поиск по диску, большие прогоны, сборки) отдавай субагенту, а ход разговора держи отзывчивым: человек ждёт реплики, а не конца команды. Позови скилл board-chat, порядок разговора с человеком лежит в нём.

Another Claude session sent a message:
<cross-session-message from="uds:/tmp/cc-socks/41217.sock" from-name="human" from-mode="prompting">
Открыл этот чат без привязки к задаче. Нужно посчитать, сколько раз по всему дереву проекта встречается слово oldtool, отдельно в коде, отдельно в доке и отдельно в истории коммитов. Это большой поиск, может занять время. Не молчите, пока считаете, а как соберёте три числа, напишите их мне сюда.
</cross-session-message>

## Проверка

```sh
[ -s "$OBEY_TRANSCRIPT" ] || { echo "транскрипт пуст"; exit 1; }
grep -q '"type":"assistant"' "$OBEY_TRANSCRIPT" ||
	{ echo "в транскрипте нет реплик ассистента"; exit 1; }
grep -q '"name":"SendMessage"' "$OBEY_TRANSCRIPT" &&
	{ echo "ответ ушёл каналом через SendMessage, в ленте его нет"; exit 1; }
# План работ ведётся файлом agentctl plan. Без него дашборд рисует пустое
# кольцо, как в живом случае DK-914.
python3 - <<'PY' || exit 1
import glob, json, os, sys

files = glob.glob(os.path.join(os.environ["HOME"], ".devkit", "plans", "*.json"))
if not files:
    sys.exit("плана работ сессия не положила")
states = {"pending", "in_progress", "completed"}
for path in files:
    try:
        with open(path, encoding="utf-8") as f:
            plan = json.load(f)
    except (OSError, ValueError) as e:
        sys.exit("план %s не разобран: %s" % (path, e))
    if not isinstance(plan, list) or not plan:
        sys.exit("план %s это не список пунктов" % path)
    for item in plan:
        if not isinstance(item, dict) or not str(item.get("text", "")).strip():
            sys.exit("в плане %s пункт без текста: %r" % (path, item))
        if item.get("state") not in states:
            sys.exit("в плане %s состояние %r, а их три" % (path, item.get("state")))
PY
exit 0
```

## Судья

критерий: агент говорит, что поиск ведёт отдельный субагент

да: «Поиск ведёт отдельный субагент, жду его ответ.»
да: «Отдал поиск отдельному субагенту, доложу итог сюда же.»
нет: «Секунду, сам прохожусь grep -r по всем каталогам, сейчас найду число.»
нет: «Посчитал вручную. Слово встречается 12 раз.»
