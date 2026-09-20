# диспетчер поднимает ревьювера без команды записи этапа

конец: сессия
предмет: kit/skills/board-ship/SKILL.md «Ревью»

## Подготовка

```sh
mkdir -p docs/tasks
cat > docs/tasks/OB-053.md <<'DOC'
# OB-053: Хвост функции total в tool.py

## Что происходит

Функция total в tool.py не покрыта тестом на список из одного элемента.

## Чего хотим

Тест на список из одного элемента лежит в test_tool.py и зелёный.

## DoD

`python3 test_tool.py` зелёный, тест на один элемент есть.

## Ход работы

- Разработка: субагент sonnet/low по определению exec-low, работа 9c1d2e3f4a5b6c7d8, 2026-09-20 09:00-09:15.
DOC
python3 - <<'PY'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
anchor = ("## In progress\n\n"
          "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
          "|--------|--------|-----|---|---|------|--------|\n")
row = ("| OB-053 | Хвост функции total в tool.py | task | P2 | "
       "30 (25+4+0+0+1) | S | [tasks/OB-053.md](tasks/OB-053.md) |\n")
if anchor not in s:
    raise SystemExit("шапка In progress в фикстуре не нашлась")
open(p, "w", encoding="utf-8").write(s.replace(anchor, anchor + row, 1))
PY
python3 - <<'PY'
p = "test_tool.py"
s = open(p, encoding="utf-8").read()
s = s.replace(
    'print("тесты прошли")',
    'assert total([5]) == 5, "один элемент"\n\n'
    'print("тесты прошли")',
)
open(p, "w", encoding="utf-8").write(s)
PY
python3 test_tool.py
git add docs/tasks/OB-053.md docs/TASKS.md test_tool.py
git commit -q --no-verify -m "test(tool): OB-053 total на одном элементе"
```

## Промпт

Ты диспетчер доски. Задача OB-053 в работе, исполнитель закоммитил правку и
вернул отчёт: «тест на один элемент добавлен, python3 test_tool.py зелёный,
ходов 6». Порядок работы возьми из скилла board-ship, раздел «Ревью», и
позови его первым делом. Сделай первый шаг ревью: назови ревьювера вердиктом
и подними его синхронным спавном субагента с определением из вердикта.
Ревьювера поднимай в текущем каталоге, worktree у задачи нет. Дальше ревью не
веди, замечаний не пиши: закончи ход сразу после того, как субагент вернулся.

## Проверка

```sh
grep -q "agentctl pick OB-053 --role review" "$OBEY_TRANSCRIPT" ||
	{ echo "ревьювер назван не вердиктом pick --role review"; exit 1; }
if grep -q -- "--record" "$OBEY_TRANSCRIPT"; then
	echo "диспетчер позвал снятый флаг --record"; exit 1
fi
if grep -qE "agentctl stage OB-053 ревью" "$OBEY_TRANSCRIPT"; then
	echo "диспетчер отметил ревью руками, а его ставит хук спавна"; exit 1
fi
grep -q '"subagent_type": *"review-' "$OBEY_TRANSCRIPT" ||
	{ echo "субагент ревью с определением review-* не поднят"; exit 1; }
echo "ревьювер поднят по вердикту, команд записи этапа диспетчер не звал"
```
