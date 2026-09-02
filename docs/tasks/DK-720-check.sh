#!/bin/sh
# Сценарий проверки DK-720 после слияния в main: реальный бинарь shipctl
# против синтетического репозитория с доской, отдельно от прогона тестов
# ветки (ACCEPTANCE.md, «Самодостаточность агентской части»). Живой
# ~/.devkit, живая доска devkit и PATH не трогаются. Аргумент это чекаут
# devkit со слитой правкой (по умолчанию текущий репозиторий).
set -eu
SRC=${1:-$(git rev-parse --show-toplevel)}
W=$(mktemp -d)
BIN=$W/bin
STUB=$W/stub
mkdir -p "$BIN" "$STUB"
(cd "$SRC/tools/shipctl" && GOWORK=off go build -o "$BIN/shipctl" .)

CALLS=$STUB/calls.log
cat > "$STUB/taskctl" <<EOF
#!/bin/sh
echo "\$@" >> "$CALLS"
printf '<!-- move -->\n' >> "\$2/docs/TASKS.md"
EOF
chmod +x "$STUB/taskctl"
PATH="$BIN:$STUB:$PATH"
export PATH

ROOT=$W/proj
mkdir -p "$ROOT/docs/tasks"
cd "$ROOT"
git init -q -b main .
git config user.email t@t
git config user.name t

sect() {
	if [ "$1" = "Нет." ]; then
		echo "Нет."
	else
		printf '| ID | Задача | Тип | P | R | Ссылка |\n|--------|--------|-----|---|---|--------|\n%s' "$1"
	fi
}

board() {
	{
		echo "# Тест: задачи (префикс XR)"
		echo
		echo "## In progress"
		echo
		sect "$1"
		echo
		echo "## Check"
		echo
		sect "$2"
		echo
		echo "## Backlog"
		echo
		echo "Нет."
		echo
		echo "## Blocked"
		echo
		echo "Нет."
	} > docs/TASKS.md
}

board "| XR-001 | Починка бага | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |
" "Нет."
printf '# XR-001: починка бага\n\n## Сценарий проверки\n\nАгентский: `git log -1`.\n' > docs/tasks/XR-001.md
printf 'old\n' > code.txt
printf '.devkit/cmdout/\n' > .gitignore
git add .
git commit -qm seed

printf '# XR-777: чужая задача\n' > docs/tasks/XR-777.md
git add .
git commit -qm "docs(tasks): XR-777 заведена"

git checkout -qb xr-001-fix
printf 'new\n' > code.txt
printf 'package main\n' > fix_test.go
git add .
git commit -qm "fix: XR-001 правка"

echo "--- 1. merge: чужая незакоммиченная правка мимо диапазона слияния не отбивает"
printf '# XR-777: чужая задача\n\nправка соседней сессии\n' > docs/tasks/XR-777.md
OUT=$(shipctl merge XR-001 --test true)
echo "$OUT" | grep -q "слита в main fast-forward" || { echo "ПРОВАЛ шаг 1:"; echo "$OUT"; exit 1; }
grep -q "правка соседней сессии" docs/tasks/XR-777.md || { echo "ПРОВАЛ шаг 1: чужая незакоммиченная правка потерялась"; exit 1; }
echo ok

echo "--- 2. merge: незакоммиченное по своему файлу отбивает и называет только его"
board "| XR-002 | Вторая задача | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-002.md](tasks/XR-002.md) |
" "Нет."
printf '# XR-002: вторая задача\n\n## Сценарий проверки\n\nАгентский: `git log -1`.\n' > docs/tasks/XR-002.md
git add .
git commit -qm "docs(tasks): XR-002 заведена"
git checkout -qb xr-002-fix
printf 'newer\n' > code.txt
printf 'package main\n' > fix2_test.go
git add .
git commit -qm "fix: XR-002 правка"
printf 'незакоммиченное\n' > code.txt
printf 'ещё чужая грязь\n' >> docs/tasks/XR-777.md
set +e
OUT=$(shipctl merge XR-002 --test true 2>&1)
CODE=$?
set -e
[ "$CODE" -ne 0 ] || { echo "ПРОВАЛ шаг 2: merge должен был отбиться на своём файле"; exit 1; }
echo "$OUT" | grep -q "code.txt" || { echo "ПРОВАЛ шаг 2: отказ не назвал code.txt"; echo "$OUT"; exit 1; }
echo "$OUT" | grep -q "XR-777" && { echo "ПРОВАЛ шаг 2: отказ помянул чужую правку мимо задачи"; exit 1; }
git checkout -q -- code.txt docs/tasks/XR-777.md
echo ok

echo "--- 3. smoke: чужая незакоммиченная правка мимо файлов задачи не отбивает"
git checkout -q main
board "Нет." "| XR-003 | Ждёт проверки | task | P2 | 30 (25+5+0+0+0) | [tasks/XR-003.md](tasks/XR-003.md) |
"
printf 'v1\n' > XR-003.txt
git add .
git commit -qm "fix: XR-003 файл"
SHA=$(git rev-parse --short HEAD)
printf '# XR-003: ждёт проверки\n\n## Выкат\n\n- 2026-08-01 слито: %s\n' "$SHA" > docs/tasks/XR-003.md
git add docs/tasks/XR-003.md
git commit -qm "docs(tasks): XR-003 файл задачи"
printf 'чужая правка мимо XR-003\n' >> docs/tasks/XR-777.md
OUT=$(shipctl smoke XR-003)
echo "$OUT" | grep -q "smoke за XR-003 отмечен" || { echo "ПРОВАЛ шаг 3:"; echo "$OUT"; exit 1; }
grep -q "smoke прогнан" docs/tasks/XR-003.md || { echo "ПРОВАЛ шаг 3: отметка не легла в файл"; exit 1; }
grep -q "чужая правка мимо XR-003" docs/tasks/XR-777.md || { echo "ПРОВАЛ шаг 3: чужая незакоммиченная правка потерялась"; exit 1; }
git checkout -q -- docs/tasks/XR-777.md
echo ok

echo "--- 4. smoke: незакоммиченное по своему файлу отбивает и называет только его"
board "Нет." "| XR-004 | Ждёт проверки | task | P2 | 30 (25+5+0+0+0) | [tasks/XR-004.md](tasks/XR-004.md) |
"
printf 'v1\n' > XR-004.txt
git add .
git commit -qm "fix: XR-004 файл"
SHA=$(git rev-parse --short HEAD)
printf '# XR-004: ждёт проверки\n\n## Выкат\n\n- 2026-08-01 слито: %s\n' "$SHA" > docs/tasks/XR-004.md
git add docs/tasks/XR-004.md
git commit -qm "docs(tasks): XR-004 файл задачи"
printf 'v2\n' > XR-004.txt
printf 'ещё чужая правка\n' >> docs/tasks/XR-777.md
set +e
OUT=$(shipctl smoke XR-004 2>&1)
CODE=$?
set -e
[ "$CODE" -ne 0 ] || { echo "ПРОВАЛ шаг 4: smoke должен был отбиться на своём файле"; exit 1; }
echo "$OUT" | grep -q "XR-004.txt" || { echo "ПРОВАЛ шаг 4: отказ не назвал XR-004.txt"; echo "$OUT"; exit 1; }
echo "$OUT" | grep -q "XR-777" && { echo "ПРОВАЛ шаг 4: отказ помянул чужую правку мимо задачи"; exit 1; }
echo ok

echo "--- готово, дерево: $W"
