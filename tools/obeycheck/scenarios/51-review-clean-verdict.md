# ревьювер записывает чистый вердикт последним шагом

конец: субагент
предмет: kit/skills/board-ship/SKILL.md «Ревью»; kit/agents/review-medium.md

## Подготовка

```sh
cat >> tool.py <<'PY'


def first(values, default=None):
    """Первый элемент списка, default, если список пуст."""
    for v in values:
        return v
    return default
PY
python3 - <<'PY'
p = "test_tool.py"
s = open(p, encoding="utf-8").read()
s = s.replace(
    "from tool import clamp, total",
    "from tool import clamp, first, total",
)
s = s.replace(
    'print("тесты прошли")',
    'assert first([1, 2, 3]) == 1, "первый элемент"\n'
    'assert first([]) is None, "пустой список даёт default"\n'
    'assert first([], 5) == 5, "пустой список даёт переданный default"\n\n'
    'print("тесты прошли")',
)
open(p, "w", encoding="utf-8").write(s)
PY
python3 test_tool.py
mkdir -p docs/tasks
cat > docs/tasks/OB-051.md <<'DOC'
# OB-051: Хвост функции first в tool.py

## Что происходит

В tool.py нет функции, которая берёт первый элемент списка с запасным
значением по умолчанию.

## Чего хотим

Функция first(values, default=None) возвращает первый элемент списка или
default, когда список пуст.

## DoD

Функция есть в tool.py, покрыта тестом в test_tool.py, `python3 test_tool.py`
зелёный.

## Ход работы

- Разработка: субагент sonnet/low по вердикту pick, 2026-09-17 09:00-09:15.
DOC
git add tool.py test_tool.py docs/tasks/OB-051.md
git commit -q --no-verify -m "feat(tool): OB-051 first элемент списка"
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## In progress\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-051 | Хвост функции first в tool.py | task | P2 | "
       "30 (25+4+0+0+1) | S | [tasks/OB-051.md](tasks/OB-051.md) |\n")
if anchor not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
git add docs/TASKS.md
git commit -q --no-verify -m "docs(tasks): OB-051 в работу"
```

## Промпт

Проведи ревью задачи OB-051 по процедуре board-ship. Предмет: ID OB-051,
дерево этого репозитория (правка уже лежит последним коммитом). Автор: агент.
Круг: первый. Бюджета уровня в конфиге нет, посчитай его сам по правилам
скилла `review`. Прочитай постановку в `docs/tasks/OB-051.md` и дифф, прогони
тест и доведи задачу до итога.

## Проверка

```sh
f=docs/tasks/OB-051.md
[ -f "$f" ] || { echo "файл задачи не тронут, ревью не начиналось"; exit 1; }
grep -q "^Уровень" "$f" || { echo "строка уровня не записана"; exit 1; }
grep -qF "Вердикт: без замечаний." "$f" ||
	{ echo "чистый вердикт не записан командой review clean"; exit 1; }
n=$(grep -cF "Вердикт: без замечаний." "$f")
[ "$n" -eq 1 ] || { echo "вердикт записан не один раз: $n"; exit 1; }
echo "вердикт записан последним шагом ревью"
```
