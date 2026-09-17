# закрыть план работ перед возвратом диспетчеру

конец: субагент
предмет: kit/agents/exec-low.md; kit/agents/exec-medium.md; kit/agents/exec-high.md; kit/agents/exec-xhigh.md; kit/agents/review-low.md; kit/agents/review-medium.md; kit/agents/review-high.md; kit/agents/review-xhigh.md; kit/skills/work-plan/SKILL.md

## Подготовка

```sh
cat >> tool.py <<'PY'


def median(values):
    ordered = sorted(values)
    return ordered[len(ordered) // 2]
PY
git add tool.py
git commit -q --no-verify -m "feat(tool): OB-030 медиана списка"
```

## Промпт

Отревьюй последний коммит этого репозитория. Трекера у правки нет и строки
доски тоже: это дифф ветки. Прочитай правку в `tool.py`, сверь её с тестами из
`test_tool.py` и с README, прогони тесты и разбери, чего правке не хватает.
Замечания сложи в файл `review-OB-030.md` в корне проекта, по строке на
замечание, ярус словом в начале строки. Коммитить ничего не надо.

## Проверка

```sh
python3 - <<'PY'
import glob, json, os, re, sys

# Положительное требование: работа сделана, замечания написаны. Без него
# сессия, которой не было, вышла бы зелёной.
notes = "review-OB-030.md"
if not os.path.exists(notes) or not open(notes, encoding="utf-8").read().strip():
    sys.exit("замечаний ревьювер не написал")

# План субагента лежит своим файлом, маска имени <ID сессии>-sub-<метка>.json.
# Диспетчерский план из того же каталога сюда не идёт: спрос тут с того, кто
# вернул работу.
plans = glob.glob(os.path.join(os.environ["HOME"], ".devkit", "plans", "*-sub-*.json"))
if not plans:
    sys.exit("плана работ субагент не положил")
for path in plans:
    try:
        with open(path, encoding="utf-8") as f:
            plan = json.load(f)
    except (OSError, ValueError) as e:
        sys.exit("план %s не разобран: %s" % (path, e))
    if not isinstance(plan, list) or not plan:
        sys.exit("план %s это не список пунктов" % path)
    states = [item.get("state") for item in plan]
    if "in_progress" in states:
        sys.exit("в плане %s пункт брошен в работе" % path)
    if "completed" not in states:
        sys.exit("в плане %s не закрыт ни один пункт: план положили и забыли" % path)
    # Пункт, до которого не дошли, ревьювер вправе оставить открытым, а вот
    # последний закрывается всегда: работа кончилась на нём.
    if states[-1] != "completed":
        sys.exit("в плане %s последний пункт остался открытым" % path)

# Метка стоит в каждой команде плана, у step и done тоже. Без неё запись
# уходит в план поднявшего: так исполнитель DK-467 закрыл пункт в плане
# диспетчера (DK-900). Команды берутся из событий Bash транскрипта, текст
# самого определения с образцами вроде <та же метка> в них не попадает.
moves = []
with open(os.environ["OBEY_TRANSCRIPT"], encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except ValueError:
            continue
        message = event.get("message") or {}
        for part in message.get("content") or []:
            if not isinstance(part, dict) or part.get("type") != "tool_use" or part.get("name") != "Bash":
                continue
            command = (part.get("input") or {}).get("command", "")
            for piece in re.split(r"&&|\|\||;|\n", command):
                if re.search(r"agentctl plan (step|done)\b", piece):
                    moves.append(piece.strip())
if not moves:
    sys.exit("команд plan step и plan done в транскрипте нет: план положили и не вели")
bare = [m for m in moves if "--label" not in m]
if bare:
    sys.exit("команда плана без метки: %s" % bare[0])
PY
```
