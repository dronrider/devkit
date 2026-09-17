# один вопрос с одним ответом плана не заводит

конец: сессия
предмет: kit/skills/work-plan/SKILL.md «Порог повода»; kit/skills/chat/SKILL.md

## Промпт

Эта сессия открыта разговором про задачу OB-004. План работ веди командой agentctl plan. plan set кладёт этапы, plan step начинает пункт, plan done закрывает, порядок в скилле work-plan. План ведётся там, где работа длиннее одного хода, а один вопрос с одним ответом плана не просит. Долгие дела (поиск по диску, большие прогоны, сборки) отдавай субагенту, а ход разговора держи отзывчивым: человек ждёт реплики, а не конца команды. На каждой реплике человека первым действием, до ответа ему, вызови скилл chat инструментом Skill: порядок разговора с человеком по задаче лежит в нём, без него ответ не по правилам.

Another Claude session sent a message:
<cross-session-message from="uds:/tmp/cc-socks/41217.sock" from-name="human" from-mode="prompting">
Просто вопрос, ничего делать не надо: в файле tool.py этого проекта, какая функция ограничивает число диапазоном значений?
</cross-session-message>

## Проверка

```sh
[ -s "$OBEY_TRANSCRIPT" ] || { echo "транскрипт пуст"; exit 1; }
grep -q '"type":"assistant"' "$OBEY_TRANSCRIPT" ||
	{ echo "в транскрипте нет реплик ассистента"; exit 1; }
[ -n "$OBEY_SESSION_ID" ] || { echo "стенд не назвал ID сессии первого хода"; exit 1; }
# Обратная сторона сценария 27-work-plan. План на один вопрос с одним
# ответом не заводится. Адрес плана тот же, что у own_plan() в
# hooks/plan-watch.py. Сперва смотрит файл без метки, потом файл с меткой
# этой сессии.
python3 - "$OBEY_HOME/.devkit/plans" "$OBEY_SESSION_ID" <<'PY'
import glob, os, sys

plans_dir, session = sys.argv[1], sys.argv[2]
bare = os.path.join(plans_dir, session + ".json")
labelled = glob.glob(os.path.join(plans_dir, session + "-sub-*.json"))
if os.path.exists(bare):
    sys.exit("план на один вопрос всё равно заведён: %s" % bare)
if labelled:
    sys.exit("план на один вопрос всё равно заведён: %s" % labelled[0])
PY
```
