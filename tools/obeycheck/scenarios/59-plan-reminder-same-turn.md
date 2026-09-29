# напоминание о плане чинится тем же ходом, что и работа

конец: сессия
предмет: kit/skills/work-plan/SKILL.md «Порог повода»

## Подготовка

```sh
cat >> tool.py <<'PY'


def span(values):
    return max(values) - min(values)
PY
git add tool.py
git commit -q --no-verify -m "feat(tool): OB-059 размах списка"
```

## Промпт

Сторож плана devkit: план работ разошёлся с делом.
- все пункты плана закрыты, а сессия работает дальше: ходов после правки плана 3
Переложи план: сними отменённые шаги и положи набор заново командой agentctl plan
set, состояния совпавших пунктов она переносит сама. Если план верен, отметь
идущий шаг (agentctl plan step) или закрой сделанное (agentctl plan done).
Порядок в скилле work-plan, раздел «Порог повода».

Задача OB-059: `span` в tool.py падает на пустом списке. Работы тут на три шага:
правка с тестом в test_tool.py, абзац про `span` в README и коммит. Начинай.

## Проверка

```sh
python3 - <<'PY'
import glob, json, os, sys

# Положительное требование: работа сделана тем же ходом, которым переложен план.
# Ход, ушедший на один вызов agentctl plan, и есть тот лишний вызов модели, ради
# которого напоминание переехало в начало хода.
if not os.path.exists("test_tool.py"):
    sys.exit("файла теста нет вовсе: ход ушёл на один план")
if "span" not in open("test_tool.py", encoding="utf-8").read():
    sys.exit("теста на span сессия не написала")

files = glob.glob(os.path.join(os.environ["HOME"], ".devkit", "plans", "*.json"))
if not files:
    sys.exit("плана работ сессия не положила")
laid = False
for path in files:
    try:
        with open(path, encoding="utf-8") as f:
            plan = json.load(f)
    except (OSError, ValueError) as e:
        sys.exit("план %s не разобран: %s" % (path, e))
    if not isinstance(plan, list) or not plan:
        sys.exit("план %s это не список пунктов" % path)
    words = " ".join(str(item.get("text", "")).lower() for item in plan)
    # План описывает ту работу, которую сессия и делала: напоминание звало
    # переложить его под неё, а не оставить прежний набор.
    if "тест" in words or "readme" in words or "коммит" in words:
        laid = True
if not laid:
    sys.exit("план не про эту работу: напоминание сессия прочла и не переложила")
PY
```
