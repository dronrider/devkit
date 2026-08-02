#!/bin/sh
# Сценарий проверки DK-070 на синтетической доске: живой проект не трогается.
set -e
W=$(mktemp -d)
cd "$W"
git init -q -b main .
git config user.email t@t
git config user.name t
mkdir -p docs/tasks .devkit
printf 'deploy = true\ntest = true\n' > .devkit/deploy.local
cat > docs/TASKS.md <<'EOF'
# Тест: задачи (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-001 | Первая | task | P2 | 30 (25+5+0+0+0) | S | [tasks/XR-001.md](tasks/XR-001.md) |
| XR-003 | Третья | task | P2 | 30 (25+5+0+0+0) | S | [tasks/XR-003.md](tasks/XR-003.md) |

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-002 | Вторая | task | P2 | 30 (25+5+0+0+0) | S | [tasks/XR-002.md](tasks/XR-002.md) |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

Нет.
EOF
printf '# Тест: сделано\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n' > docs/TASKS-archive.md
printf 'old\n' > code.txt
git add .
git commit -qm seed
printf 'new\n' > code.txt
git commit -qam "feat: правка соседней задачи без номера"
S=$(git rev-parse --short HEAD)

# XR-002 стоит в Check без собственной записи выката, зато её файл цитирует
# чужую: ровно так в файл попадает реальный вывод по сценарию проверки.
cat > docs/tasks/XR-002.md <<EOF
# XR-002: вторая

## Сценарий проверки

Вывод merge на синтетической доске:

\`\`\`
## Выкат

- 2026-08-01 слито: $S
\`\`\`
EOF
# У XR-001 своё ревью закрыто, а внутри блока лежит процитированное чужое
# замечание без исхода.
cat > docs/tasks/XR-001.md <<'EOF'
# XR-001: первая

## Сценарий проверки

Вывод taskctl review show соседней задачи:

```
## Ревью

- гонка в close без исхода
```

## Ревью

- нейминг: отклонено, стиль проекта
EOF
printf '# XR-003: третья\n\n## Ревью\n\n- настоящее замечание без исхода\n' > docs/tasks/XR-003.md
git add docs
git commit -qm "docs(tasks): файлы задач"

echo "--- 1. процитированная чужая запись выката не держит очередь"
shipctl status
shipctl status | grep -q "очередь занята" && { echo "ПРОВАЛ: очередь держит цитата чужой записи"; exit 1; }

echo "--- 2. настоящее замечание без исхода слияние по-прежнему отбивает"
git checkout -qb xr-003-fix
printf 'ещё\n' > b.txt
git add b.txt
git commit -qm "feat: XR-003 правка"
shipctl merge XR-003 --test true 2>&1 | grep "замечания без исхода"
git checkout -q main

echo "--- 3. процитированное замечание слияние пропускает, запись встаёт в свой раздел"
git checkout -qb xr-001-fix
printf 'правка\n' > a.txt
git add a.txt
git commit -qm "feat: XR-001 правка"
shipctl merge XR-001 --test true | grep -E "слито|Check"
sed -n '/^## Выкат/,$p' docs/tasks/XR-001.md
shipctl status | grep "очередь занята"
echo "--- готово, дерево: $W"
