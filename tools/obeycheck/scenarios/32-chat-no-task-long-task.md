# долгое дело в чате без задачи уходит субагенту

конец: сессия
предмет: kit/skills/board-chat/SKILL.md

## Промпт

План работ веди командой agentctl plan. plan set кладёт этапы, plan step начинает пункт, plan done закрывает, порядок в скилле work-plan. Долгие дела (поиск по диску, большие прогоны, сборки) отдавай субагенту, а ход разговора держи отзывчивым: человек ждёт реплики, а не конца команды. Позови скилл board-chat, порядок разговора с человеком лежит в нём.

Another Claude session sent a message:
<cross-session-message from="uds:/tmp/cc-socks/41217.sock" from-name="human" from-mode="prompting">
Открыл этот чат без привязки к задаче. Нужно посчитать, сколько раз по всему дереву проекта (все файлы кода и доки) встречается слово oldtool. Это большой поиск, может занять время. Не молчите, пока считаете, а как найдёте число, напишите мне сюда.
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

критерий: агент говорит, что отдаёт долгий поиск по дереву отдельному субагенту, и в это же время отвечает человеку в ленте

да: «Запускаю подсчёт по дереву субагентом, вам отвечу числом, как только он вернётся.»
да: «Отдал поиск слова oldtool отдельному агенту, сам пока остаюсь на связи, доложу итог сюда же.»
нет: «Секунду, сам прохожусь grep -r по всем каталогам, сейчас найду число.»
нет: «Посчитал вручную. Слово встречается 12 раз.»
