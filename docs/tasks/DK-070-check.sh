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
| XR-004 | Четвёртая | task | P3 | 10 (5+5+0+0+0) | S | - |

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
# У XR-003 настоящего раздела «Сценарий проверки» нет, есть только цитата
# чужого, плюс настоящее замечание без исхода.
cat > docs/tasks/XR-003.md <<'EOF'
# XR-003: третья

## Ход работы

Вывод merge на синтетической доске:

```
## Сценарий проверки

Агентский: `shipctl status`, ждём свободную очередь.
```

## Ревью

- настоящее замечание без исхода
EOF
git add docs
git commit -qm "docs(tasks): файлы задач"

echo "--- 1. процитированная чужая запись выката не держит очередь"
shipctl status
shipctl status | grep -q "очередь занята" && { echo "ПРОВАЛ: очередь держит цитата чужой записи"; exit 1; }

echo "--- 2. оборванный ограждённый блок виден вслух и в дереве задачи"
# Типовой путь по RULES.board.md: задача берётся в работу в своём worktree,
# файл задачи живёт там же на ветке, основной чекаут стоит на main.
shipctl start XR-004 --slug fix | tail -1
WT="$(dirname "$W")/$(basename "$W")-xr-004"
printf '# XR-004: четвёртая\n\n## Ход работы\n\nВывод merge:\n\n```\nдоска: XR-004 в Check\n' > "$WT/docs/tasks/XR-004.md"
git -C "$WT" add docs/tasks/XR-004.md
git -C "$WT" commit -qm "docs(tasks): XR-004 вывод с оборванным ограждением"
shipctl status | grep "не закрыт ограждённый блок"
shipctl merge XR-004 --test true 2>&1 | grep "не закрыт ограждённый блок"
printf '```\n' >> "$WT/docs/tasks/XR-004.md"
git -C "$WT" commit -qam "docs(tasks): XR-004 ограждение закрыто"
shipctl status | grep -q "не закрыт ограждённый блок" && { echo "ПРОВАЛ: закрытое ограждение всё ещё считается обрывом"; exit 1; }

echo "--- 3. настоящее замечание без исхода слияние по-прежнему отбивает"
git checkout -qb xr-003-fix
printf 'ещё\n' > b.txt
git add b.txt
git commit -qm "feat: XR-003 правка"
shipctl merge XR-003 --train --test true 2>&1 | grep "замечания без исхода"

echo "--- 4. процитированный заголовок за раздел «Сценарий проверки» не сходит"
taskctl -C "$W" review resolve XR-003 1 fixed --reason "стиль проекта" -m "docs(tasks): XR-003 исход замечания"
shipctl merge XR-003 --train --test true | grep "нет раздела «Сценарий проверки»"

echo "--- 5. процитированное замечание слияние пропускает, запись встаёт в свой раздел"
git checkout -qb xr-001-fix main
printf 'правка\n' > a.txt
git add a.txt
git commit -qm "feat: XR-001 правка"
OUT=$(shipctl merge XR-001 --train --test true)
echo "$OUT"
echo "$OUT" | grep -q "нет раздела «Сценарий проверки»" && { echo "ПРОВАЛ: подсказка при настоящем разделе"; exit 1; }
sed -n '/^## Выкат/,$p' docs/tasks/XR-001.md
shipctl ship | tail -2
shipctl status | grep "очередь занята"
echo "--- готово, дерево: $W"
