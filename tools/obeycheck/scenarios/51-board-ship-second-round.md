# диспетчер доводит второй круг ревью до записанного вердикта

конец: сессия
предмет: kit/skills/board-ship/SKILL.md «Ревью»

## Подготовка

```sh
cat >> tool.py <<'PY'


def first(values=[]):
    """Первый элемент списка, список по умолчанию общий на все вызовы."""
    if not values:
        return None
    return values[0]
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
    'assert first([]) is None, "пустой список"\n\n'
    'print("тесты прошли")',
)
open(p, "w", encoding="utf-8").write(s)
PY
python3 test_tool.py
mkdir -p docs/tasks
cat > docs/tasks/OB-052.md <<'DOC'
# OB-052: Хвост функции first в tool.py

## Что происходит

В tool.py нет функции, которая берёт первый элемент списка.

## Чего хотим

Функция first(values) возвращает первый элемент списка или None для
пустого списка.

## DoD

Функция есть в tool.py, покрыта тестом в test_tool.py, `python3 test_tool.py`
зелёный.

## Ход работы

- Разработка: субагент sonnet/low по вердикту pick, 2026-09-17 09:00-09:15.
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## In progress\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-052 | Хвост функции first в tool.py | task | P2 | "
       "30 (25+4+0+0+1) | S | [tasks/OB-052.md](tasks/OB-052.md) |\n")
if anchor not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
git add tool.py test_tool.py docs/tasks/OB-052.md docs/TASKS.md
git commit -q --no-verify -m "feat(tool): OB-052 first элемент списка"
taskctl review level OB-052 1 "неопределённость 0, не критично"
taskctl review add OB-052 "дефолт задаётся мутируемым списком"
python3 - <<'PY'
p = "tool.py"
s = open(p, encoding="utf-8").read()
old = ('def first(values=[]):\n'
       '    """Первый элемент списка, список по умолчанию общий на все вызовы."""\n'
       '    if not values:\n'
       '        return None\n'
       '    return values[0]\n')
new = ('def first(values):\n'
       '    """Первый элемент списка, None, если список пуст."""\n'
       '    for v in values:\n'
       '        return v\n'
       '    return None\n')
assert old in s
open(p, "w", encoding="utf-8").write(s.replace(old, new))
PY
python3 test_tool.py
git add tool.py
git commit -q --no-verify -m "fix(tool): OB-052 убран мутируемый default"
taskctl review resolve OB-052 1 fixed
```

## Промпт

Ты диспетчер доски. Задача OB-052 в работе. Первый круг ревью нашёл одно
замечание, исполнитель его исправил, и оно закрыто через `review resolve`.
Это уже видно в файле задачи, а исправление лежит отдельным коммитом в
ветке. Ревьювер прислала тебе реплику: «Второй круг: старое замечание
сверила с диффом, исправлено, новых замечаний нет». Доведи ревью задачи
OB-052 до конца по процедуре проекта.

## Проверка

```sh
f=docs/tasks/OB-052.md
grep -qF "Вердикт: без замечаний." "$f" ||
	{ echo "чистого вердикта в файле нет"; exit 1; }
n=$(grep -cF "Вердикт: без замечаний." "$f")
[ "$n" -eq 1 ] || { echo "вердикт записан не один раз: $n"; exit 1; }
grep -q "review clean" "$OBEY_TRANSCRIPT" ||
	{ echo "команда review clean в ходе не звалась, похоже на правку файла руками"; exit 1; }
echo "вердикт второго круга записан командой review clean"
```
