# ревьювер ведёт план под меткой с хвостом роли

конец: субагент
предмет: kit/agents/review-low.md; kit/agents/review-medium.md; kit/agents/review-high.md; kit/agents/review-xhigh.md; kit/skills/work-plan/SKILL.md

## Подготовка

```sh
cat >> tool.py <<'PY'


def median(values):
    ordered = sorted(values)
    return ordered[len(ordered) // 2]
PY
git add tool.py
git commit -q --no-verify -m "feat(tool): OB-047 медиана списка"
```

## Промпт

Отревьюй последний коммит этого репозитория. Трекера у правки нет и строки
доски тоже: это дифф ветки, задача OB-047. Прочитай правку в `tool.py`, сверь
её с тестами из `test_tool.py` и с README, прогони тесты и разбери, чего
правке не хватает. Замечания сложи в файл `review-OB-047.md` в корне проекта,
по строке на замечание, ярус словом в начале строки. Коммитить ничего не надо.

## Проверка

```sh
python3 - <<'PY'
import glob, json, os, sys

# Положительное требование: работа сделана, замечания написаны. Без него
# сессия, которой не было, вышла бы зелёной.
notes = "review-OB-047.md"
if not os.path.exists(notes) or not open(notes, encoding="utf-8").read().strip():
    sys.exit("замечаний ревьювер не написал")

# План ревьювера лежит своим файлом <ID сессии>-sub-<метка>.json, и метка
# несёт хвост роли: метка задачи без хвоста принадлежит исполнителю, и
# ревьювер под ней затирал план исполнителя (DK-888, DK-1022).
plans = glob.glob(os.path.join(os.environ["HOME"], ".devkit", "plans", "*-sub-*.json"))
if not plans:
    sys.exit("плана работ ревьювер не положил")
tails = [p for p in plans if p.endswith("-review.json")]
if not tails:
    sys.exit("метка плана без хвоста роли: %s" % ", ".join(os.path.basename(p) for p in plans))
for path in tails:
    with open(path, encoding="utf-8") as f:
        plan = json.load(f)
    if not isinstance(plan, list) or not plan:
        sys.exit("план %s это не список пунктов" % path)
PY
```
