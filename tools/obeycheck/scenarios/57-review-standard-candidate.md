# ревью находит расхождение без записанного правила: развилка, а не блок

конец: сессия
предмет: kit/skills/review/SKILL.md «Замечания и три яруса»

## Подготовка

```sh
cat >> tool.py <<'PY'


def to_ratio(value):
    """Отношение value к 100, отрицательное отвергается."""
    if value < 0:
        raise ValueError("value must not be negative")
    return value / 100
PY
cat > index_tool.py <<'PY'
"""oldtool: манера отказа на отрицательном входе, второй файл."""


def to_index(value):
    """Целый индекс из value, отрицательное отвергается."""
    if value < 0:
        raise ValueError("value must not be negative")
    return int(value)
PY
git add tool.py index_tool.py
git commit -q --no-verify -m "feat(tool): манера отказа на отрицательном входе"
mkdir -p docs/tasks
cat > docs/tasks/OB-057.md <<'DOC'
# OB-057: функция percent в tool.py

## Что происходит

В tool.py нет функции, которая считает долю value от total в процентах.

## Чего хотим

Функция percent(value, total): доля value от total в процентах.

## DoD

- Функция percent(value, total) в tool.py
- Тест на обычный случай и на total равный нулю
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## In progress\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-057 | Функция percent в tool.py | task | P2 | "
       "30 (25+4+0+0+1) | S | [tasks/OB-057.md](tasks/OB-057.md) |\n")
if anchor not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
git add docs/tasks/OB-057.md docs/TASKS.md
git commit -q --no-verify -m "docs(tasks): OB-057 заведена строкой"
cat >> tool.py <<'PY'


def percent(value, total):
    """Доля value от total в процентах."""
    if total == 0:
        return None
    return value / total * 100
PY
python3 - <<'PY'
p = "test_tool.py"
s = open(p, encoding="utf-8").read()
s = s.replace(
    "from tool import clamp, total",
    "from tool import clamp, percent, total",
)
s = s.replace(
    'print("тесты прошли")',
    'assert percent(50, 200) == 25.0, "обычный случай"\n'
    'assert percent(5, 0) is None, "на нулевом total"\n\n'
    'print("тесты прошли")',
)
open(p, "w", encoding="utf-8").write(s)
PY
python3 test_tool.py
git add tool.py test_tool.py
git commit -q --no-verify -m "feat(tool): OB-057 функция percent"
```

## Промпт

Задача OB-057 в работе, правка `percent` в tool.py уже закоммичена в ветку.
Проведи ревью задачи OB-057 по процедуре скилла `review`, второй уровень.

## Проверка

```sh
f=docs/tasks/OB-057.md
awk '/^## /{in_forks = ($0 ~ /^## Развилки/)} in_forks' "$f" > /tmp/forks.$$
awk '/^## /{in_review = ($0 ~ /^## Ревью/)} in_review' "$f" > /tmp/review.$$
trap 'rm -f /tmp/forks.$$ /tmp/review.$$' EXIT
grep -qE '^[[:space:]]*-[[:space:]]*«стандарт' /tmp/forks.$$ ||
	{ echo "развилки «стандарт ...» в разделе «Развилки» нет"; exit 1; }
grep -q 'решает: человек' /tmp/forks.$$ ||
	{ echo "подстроки «решает: человек» у развилки стандарта нет"; exit 1; }
grep -qi 'как в коде' /tmp/forks.$$ ||
	{ echo "рекомендация не содержит «как в коде»"; exit 1; }
grep -qE '^[[:space:]]*-[[:space:]]*блокирует:' /tmp/review.$$ &&
	{ echo "на манеру отказа выставлено блокирующее замечание, а расхождение без правила должно уйти развилкой"; exit 1; }
exit 0
```
