#!/bin/sh
# Сценарий проверки DK-067: пометки строки Check (держит очередь, кто
# проверяет) и возраст строки в выводе list/show, на синтетическом стенде.
# Живой проект и живая доска не трогаются, всё во временном дереве.
# Без DEVKIT берутся taskctl и shipctl из PATH (проверка после выката), с
# DEVKIT=<путь к клону> бинари собираются оттуда (обкатка на ветке).
set -eu
TMP=$(mktemp -d)
BIN=$TMP/bin
mkdir -p "$BIN" "$TMP/home"
if [ -n "${DEVKIT:-}" ]; then
	(cd "$DEVKIT/taskctl" && go build -o "$BIN/taskctl" .)
	(cd "$DEVKIT/shipctl" && go build -o "$BIN/shipctl" .)
fi
export PATH="$BIN:$PATH" HOME="$TMP/home" DEVKIT_NOTIFY_OFF=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@test
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@test

ok() { printf 'шаг %s: ok\n' "$1"; }
die() { printf 'шаг %s: ПРОВАЛ\n%s\n' "$1" "$2" >&2; exit 1; }

P=$TMP/proj
mkdir -p "$P"
cd "$P"
git init -q -b main
taskctl init --prefix XR --name "Стенд DK-067" >/dev/null

# XR-001 пройдёт полный цикл shipctl (выкат пишет раздел «Выкат» в файл
# задачи), XR-002 попадёт в Check без выката, как LLD или разбор, и со своим
# агентским сценарием проверки.
taskctl add --id XR-001 --title "Со слитым кодом" --type task --rank "0+3+0+0+0" >/dev/null
taskctl add --id XR-002 --title "Без выката, дока" --type task --rank "0+2+0+0+0" >/dev/null
taskctl file XR-001 >/dev/null
taskctl file XR-002 >/dev/null
cat >>docs/tasks/XR-002.md <<'MD'

## Сценарий проверки (агентский)

шаги
MD

# XR-003 заводим сразу в Check и больше не трогаем: строка демонстрирует
# возраст, а XR-001/XR-002 ниже ещё переедут (move, merge) и своей же правкой
# обнулят себе счётчик, это оговорено решением задачи (дата берётся с
# последней правки строки, а не с перевода в статус).
mkdir -p docs/tasks
printf '# XR-003: Старая, непотревоженная\n' >docs/tasks/XR-003.md
taskctl add --id XR-003 --title "Старая, непотревоженная" --type task --rank "0+1+0+0+0" \
	--link "[tasks/XR-003.md](tasks/XR-003.md)" --status check >/dev/null

# Начальный коммит датирован десятью годами раньше сегодняшнего дня, чтобы
# возраст строки XR-003 в выводе был однозначно виден, а не тонул в «0 дней»
# из-за скорости самого сценария.
git add .
GIT_AUTHOR_DATE=2016-01-01T00:00:00 GIT_COMMITTER_DATE=2016-01-01T00:00:00 \
	git commit -qm "chore: стенд"

shipctl start XR-001 >/dev/null
(cd "$TMP/proj-xr-001" && echo code >code.txt && git add . && git commit -qm "feat: XR-001 правка")
shipctl merge XR-001 --test true >/dev/null
taskctl move XR-002 check -m "docs(tasks): XR-002 в Check" >/dev/null

# 1. list check показывает обе оси разом: XR-001 держит очередь и ждёт
#    живого человека, XR-002 без выката и проверяется агентом.
OUT=$(taskctl list check)
echo "$OUT" | grep -q "код слит, сценарий пользовательский" || die 1 "$OUT"
echo "$OUT" | grep -q "без выката, сценарий агентский" || die 1 "$OUT"
ok 1

# 2. Возраст виден и однозначно большой у нетронутой строки XR-003:
#    начальный коммит датирован 2016 годом. У XR-001 и XR-002 та же строка
#    только что переехала (move, merge), и это честные «0 дней», а не баг.
echo "$OUT" | grep -A1 "XR-003" | grep -Eq "строка не двигалась [0-9]{4} дней" || die 2 "$OUT"
ok 2

# 3. show печатает то же самое для одной задачи.
taskctl show XR-001 | grep -q "код слит, сценарий пользовательский, строка не двигалась" ||
	die 3 "$(taskctl show XR-001)"
ok 3

# 4. Грязное дерево доски гасит возраст (номера строк рабочей копии и HEAD
#    разъехались бы), но не пометки очереди и сценария: они читаются не из
#    git, а из файла задачи.
printf '\n' >>docs/TASKS.md
DIRTY=$(taskctl list check)
if echo "$DIRTY" | grep -q "строка не двигалась"; then die 4 "$DIRTY"; fi
echo "$DIRTY" | grep -q "код слит, сценарий пользовательский" || die 4 "$DIRTY"
git checkout -- docs/TASKS.md
ok 4

# 5. Доска после всех переходов остаётся здоровой.
taskctl lint >"$TMP/out" 2>&1 || die 5 "$(cat "$TMP/out")"
ok 5

echo "СЦЕНАРИЙ ПРОШЁЛ"
rm -rf "$TMP"
