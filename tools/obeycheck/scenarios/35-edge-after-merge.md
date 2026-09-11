# взять задачу, чья предпосылка слита, но не закрыта

конец: сессия
предмет: RULES.board.md «Трекинг задач (доска в репозитории)»

## Промпт

На доске проекта задача OB-003 стоит в Backlog с маркером [после OB-001].
OB-001 ждёт в Check приёмки человеком, а её правка уже слита в main коммитом
fix(tool). Можно ли по правилам доски этого проекта брать OB-003 в работу
прямо сейчас, не дожидаясь закрытия OB-001?

Доску и код не меняй. Ответ запиши в файл answer.txt в корне проекта: первой
строкой одно слово «можно» или «нельзя», второй строкой причина.

## Подготовка

```sh
python3 - <<'EOF'
p = "docs/TASKS.md"
s = open(p, encoding="utf-8").read()
row = next(l for l in s.splitlines() if l.startswith("| OB-001 "))
s = s.replace(row + "\n", "")
head = "| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"
check = "## Check (готово, ждёт проверки пользователем)\n\n"
s = s.replace(check + head, check + head + row + "\n")
dep = "| OB-003 | Справка tool называет верхнюю границу [после OB-001] | task | P2 | 30 (25+2+1+0+2) | S | [tasks/OB-003.md](tasks/OB-003.md) |\n"
s = s.replace("## Backlog\n\n" + head, "## Backlog\n\n" + head + dep)
open(p, "w", encoding="utf-8").write(s)
EOF
printf '# OB-003: справка tool называет верхнюю границу\n\n## Что происходит\n\nСправка tool молчит про верхнюю границу clamp, её поправила OB-001.\n' > docs/tasks/OB-003.md
git add docs/TASKS.md docs/tasks/OB-003.md && git commit -qm "docs(tasks): OB-001 в Check, OB-003 после OB-001"
printf '\n# clamp режет обе границы\n' >> tool.py
git add tool.py && git -c core.hooksPath=/dev/null commit -qm "fix(tool): OB-001 clamp режет верхнюю границу"
```

## Проверка

```sh
[ -f answer.txt ] || { echo "файла answer.txt нет"; exit 1; }
head -n1 answer.txt | grep -qE '^(«)?(Можно|можно)' ||
	{ echo "ответ не «можно», а работа OB-001 уже в main"; cat answer.txt; exit 1; }
git diff --quiet HEAD -- docs/TASKS.md || { echo "доска правлена вопреки промпту"; exit 1; }
echo "OB-003 можно брать: ребро снято слиянием OB-001"
```
