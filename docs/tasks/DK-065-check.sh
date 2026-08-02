#!/bin/sh
# Сценарий проверки DK-065 на синтетической доске: живой проект не трогается.
set -e
W=$(mktemp -d)
cd "$W"
git init -q -b main .
git config user.email t@t
git config user.name t
mkdir -p docs/tasks
cat > docs/TASKS.md <<'EOF'
# Тест: задачи (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-001 | Первая | task | P2 | 30 (25+5+0+0+0) | S | [tasks/XR-001.md](tasks/XR-001.md) |
| XR-002 | Вторая | task | P2 | 30 (25+5+0+0+0) | S | - |

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

Нет.
EOF
printf '# Тест: сделано\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n' > docs/TASKS-archive.md
printf '# XR-001: первая\n' > docs/tasks/XR-001.md
printf 'old\n' > code.txt
git add .
git commit -qm seed

echo "--- 1. ветка с коммитом без ID в subject"
git checkout -qb xr-001-fix
printf 'new\n' > code.txt
git commit -qam "fix: правка без номера задачи"
shipctl merge XR-001 --test true | grep -E "коммиты задачи записаны|нет XR-001 в subject"

echo "--- 2. запись в файле задачи"
sed -n '/## Выкат/,$p' docs/tasks/XR-001.md

echo "--- 3. очередь держится записью, а не ID в subject"
git log --format=%s -3
shipctl status | grep "очередь занята"
git checkout -qb xr-002-fix main
printf 'ещё\n' > other.txt
git add other.txt
git commit -qm "fix: вторая правка без номера"
if shipctl merge XR-002 --test true; then
	echo "ПРОВАЛ: слияние прошло при непроверенном выкате"
	exit 1
fi

echo "--- 4. lint видит осиротевший файл после правки ID"
git mv docs/tasks/XR-001.md docs/tasks/XR-0001.md
git commit -qm "docs(tasks): переименование файла руками"
taskctl lint || true
echo "--- готово, дерево: $W"
