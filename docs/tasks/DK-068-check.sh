#!/bin/sh
# Сценарий проверки DK-068 на синтетической доске: живой проект не трогается.
set -e
W=$(mktemp -d)
cd "$W"
git init -q -b main .
git config user.email t@t
git config user.name t
taskctl init --prefix XR --name "Тест" >/dev/null
taskctl add --title "Первая" --type task --rank "25+5+0+0+0" --cost S >/dev/null
taskctl add --title "Вторая" --type task --rank "0+3+0+0+0" --cost S >/dev/null

echo "--- 1. задачу из Backlog заблокировать нельзя"
if taskctl move XR-001 blocked --reason "ждём смежника" 2>/dev/null; then
	echo "ПРОВАЛ: блокировка из Backlog прошла"
	exit 1
fi
taskctl move XR-001 blocked --reason "ждём смежника" 2>&1 | grep "ещё не в работе"

echo "--- 2. начатую можно, причина едет в заголовок"
taskctl move XR-001 in-progress >/dev/null
taskctl move XR-001 blocked --reason "ждём смежника" >/dev/null
taskctl list blocked | grep "блок: ждём смежника"

echo "--- 3. ожидание своей же задачи это dep, а не статус"
taskctl dep add XR-002 XR-001 >/dev/null
taskctl list backlog | grep "после XR-001"

echo "--- 4. обходные пути закрыты тем же правилом"
if taskctl add --title "Сразу на блокере" --type task --rank "0+1+0+0+0" --status blocked 2>/dev/null; then
	echo "ПРОВАЛ: новая строка заведена сразу в Blocked"
	exit 1
fi
taskctl add --title "Сразу на блокере" --type task --rank "0+1+0+0+0" --status blocked 2>&1 | grep "не заводят"
if taskctl dep add XR-001 XR-002 2>/dev/null; then
	echo "ПРОВАЛ: задаче на блокере дописана незакрытая зависимость"
	exit 1
fi
taskctl dep add XR-001 XR-002 2>&1 | grep "нельзя добавить незакрытую зависимость"

echo "--- 5. находка lint называет одно место, а не выбор из двух"
taskctl move XR-002 in-progress 2>/dev/null || true
python3 - "$W/docs/TASKS.md" <<'PY'
import sys, pathlib
# строку с зависимостью переносим в In progress руками: move её не пустит,
# а находка lint нужна именно на такой доске
p = pathlib.Path(sys.argv[1]); lines = p.read_text().split('\n')
row = next(i for i, l in enumerate(lines) if l.startswith('| XR-002 '))
moved = lines.pop(row)
head = next(i for i, l in enumerate(lines) if l.startswith('## In progress'))
lines.insert(head + 4, moved)
p.write_text('\n'.join(lines))
PY
taskctl lint | grep "вернуть в Backlog"
echo "--- готово, дерево: $W"
