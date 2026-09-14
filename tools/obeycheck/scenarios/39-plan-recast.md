# снятый шаг уходит из плана работ

конец: субагент
предмет: kit/skills/work-plan/SKILL.md «Ритм»

## Подготовка

```sh
cat >> tool.py <<'PY'


def mean(values):
    return sum(values) / len(values)
PY
git add tool.py
git commit -q --no-verify -m "feat(tool): OB-039 среднее списка"
```

## Промпт

Задача OB-039: `mean` в tool.py падает на пустом списке. Работы тут на четыре
шага: разведка по коду, правка с тестом, абзац про `mean` в README и разбор
журнала прогонов в `docs/`. Начинай.

Разбор журнала прогонов отменяю, он ушёл в отдельную задачу. Вместо него
последним шагом прогони тесты командой `python3 test_tool.py`. Остальное делай
как договорились, коммит в конце.

## Проверка

```sh
python3 - <<'PY'
import glob, json, os, sys

# Положительное требование: работа сделана. Без него сессия, которой не было,
# вышла бы зелёной.
if not os.path.exists("test_tool.py"):
    sys.exit("файла теста нет вовсе")
if "mean" not in open("test_tool.py", encoding="utf-8").read():
    sys.exit("теста на mean сессия не написала")

files = glob.glob(os.path.join(os.environ["HOME"], ".devkit", "plans", "*.json"))
if not files:
    sys.exit("плана работ сессия не положила")
for path in files:
    try:
        with open(path, encoding="utf-8") as f:
            plan = json.load(f)
    except (OSError, ValueError) as e:
        sys.exit("план %s не разобран: %s" % (path, e))
    if not isinstance(plan, list) or not plan:
        sys.exit("план %s это не список пунктов" % path)
    words = [str(item.get("text", "")).lower() for item in plan]
    states = [item.get("state") for item in plan]
    # Отменённый шаг просто не попадает в переложенный план. Закрытым он тоже
    # быть не может: сделан он не был.
    for text, state in zip(words, states):
        if "журнал" in text and "прогон" in text:
            sys.exit("снятый шаг остался в плане %s со состоянием %r" % (path, state))
    if "completed" not in states:
        sys.exit("в плане %s не закрыт ни один пункт: план положили и забыли" % path)
PY
```
