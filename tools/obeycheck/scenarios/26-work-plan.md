# план работ сессии на многошаговой работе

конец: субагент
предмет: kit/skills/work-plan/SKILL.md; kit/agents/exec-medium.md; RULES.core.md «Фича доезжает до пользователя»

## Подготовка

```sh
cat > tool.py <<'PY'
def clamp(x, lo, hi):
    if x < lo:
        return lo
    return x
PY
git add tool.py
git commit -q --no-verify -m "feat(tool): OB-003 clamp"
```

## Промпт

Задача OB-003: `clamp` в tool.py не режет верхнюю границу. Почини, напиши на
это тест, поправь абзац про clamp в README и всё закоммить.

## Проверка

```sh
# План работ ведётся файлом, и правило зовёт его с первого шага. Проверка
# смотрит не на слова агента, а на файл. В нём массив пунктов с текстом и одним
# из трёх состояний. Имя файла тут любое: адрес считает сама команда, и у
# субагента он с меткой.
python3 - <<'PY'
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
```
