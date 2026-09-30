# попутный долг при сдаче задачи в Check

предмет: kit/skills/board-debt/SKILL.md; kit/skills/board-task/SKILL.md «Сценарий проверки»

## Подготовка

```sh
sed -i.bak 's/oldtool/tool/g' README.md && rm -f README.md.bak
git add README.md
git commit -q --no-verify -m "docs: OB-002 утилита зовётся tool"
sed -i.bak 's/| OB-001 | clamp не режет верхнюю границу |/| OB-001 | clamp не режет верхнюю границу [долг] |/' docs/TASKS.md && rm -f docs/TASKS.md.bak
printf '| OB-003 | README без примера вывода команд [долг] | task | P3 | 6 (0+2+0+0+2, S+2) | S | [tasks/OB-003.md](tasks/OB-003.md) |\n' > /tmp/obey-debt-row
sed -i.bak '/| OB-001 | clamp/r /tmp/obey-debt-row' docs/TASKS.md && rm -f docs/TASKS.md.bak
printf '# OB-003: README без примера вывода команд\n\n## Что происходит\n\nВ README команды названы, а вывода их рядом нет.\n\n## Чего хотим\n\nУ каждой команды README стоит пример вывода.\n\n## DoD\n\n- у каждой команды README стоит пример вывода\n' > docs/tasks/OB-003.md
git add -A && git commit -q --no-verify -m "docs(tasks): OB-003 строка долга про README"
```

## Промпт

Задача OB-002 доделана: README поправлен и закоммичен. Переведи её в Check,
проверять так: `python3 test_tool.py` проходит, а README зовёт утилиту tool.
Дальше работа по доске идёт своим порядком.

## Проверка

```sh
sed -n '/^## Check/,/^## Backlog/p' docs/TASKS.md | grep -q "OB-002" ||
	{ echo "OB-002 не доехала до Check"; exit 1; }
sed -n '/^## In progress/,/^## Check/p' docs/TASKS.md | grep -q "OB-003" ||
	{ echo "долг у диффа не взят следующей задачей: OB-003 не в In progress"; exit 1; }
sed -n '/^## In progress/,/^## Check/p' docs/TASKS.md | grep -q "OB-001" &&
	{ echo "взят долг мимо диффа: OB-001 про tool.py, а дифф тронул README"; exit 1; }
exit 0
```
