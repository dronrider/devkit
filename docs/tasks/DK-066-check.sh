#!/bin/sh
# Сценарий проверки DK-066: два исхода проверки на синтетическом стенде.
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
taskctl init --prefix XR --name "Стенд DK-066" >/dev/null
taskctl add --id XR-001 --title "Первая" --type task --rank "25+3+0+0+0" >/dev/null
taskctl add --id XR-002 --title "Вторая" --type task --rank "25+2+0+0+0" >/dev/null
taskctl file XR-001 >/dev/null
taskctl file XR-002 >/dev/null
git add . && git commit -qm "chore: стенд"

# XR-001 выкачена и ждёт проверки, XR-002 пока в бэклоге.
echo one > one.txt
git add . && git commit -qm "feat: XR-001 правка"
taskctl move XR-001 check -m "docs(tasks): XR-001 в Check" >/dev/null

# 1. Выкаченная задача в Check держит очередь, как и раньше.
shipctl status | grep -q "очередь занята: XR-001" || die 1 "$(shipctl status)"
ok 1

# 2. Провал проверки: задача возвращается в работу с пометкой в строке.
taskctl fail XR-001 --reason "прод отдаёт 500 на входе" -m "docs(tasks): XR-001 провал проверки" >/dev/null
taskctl show XR-001 | grep -q "XR-001 в in-progress" || die 2 "$(taskctl show XR-001)"
taskctl show XR-001 | grep -q "\[провал: прод отдаёт 500 на входе\]" || die 2 "$(taskctl show XR-001)"
ok 2

# 3. Status называет сломанный прод и вердикта про свободную очередь не даёт.
shipctl status | grep -q "провал проверки за XR-001 (прод отдаёт 500 на входе)" || die 3 "$(shipctl status)"
shipctl status | grep -q "shipctl revert XR-001" || die 3 "$(shipctl status)"
! shipctl status | grep -q "очередь свободна" || die 3 "$(shipctl status)"
ok 3

# 4. Чужая задача не сливается, пока прод сломан.
shipctl start XR-002 >/dev/null
(cd "$TMP/proj-xr-002" && echo two > two.txt && git add . && git commit -qm "feat: XR-002 правка")
if shipctl merge XR-002 --test true >"$TMP/out" 2>&1; then die 4 "$(cat "$TMP/out")"; fi
grep -q "провал проверки за XR-001" "$TMP/out" || die 4 "$(cat "$TMP/out")"
ok 4

# 5. Форвард-фикс самой проваленной задачи проходит и гасит признак.
shipctl start XR-001 >/dev/null
(cd "$TMP/proj-xr-001" && echo fixed > one.txt && git add . && git commit -qm "fix: XR-001 починка прода")
shipctl merge XR-001 --test true >"$TMP/out" 2>&1 || die 5 "$(cat "$TMP/out")"
grep -q "признак провала (прод отдаёт 500 на входе) снят переводом в Check" "$TMP/out" || die 5 "$(cat "$TMP/out")"
taskctl show XR-001 | grep -q "XR-001 в check" || die 5 "$(taskctl show XR-001)"
! taskctl show XR-001 | grep -q "провал:" || die 5 "$(taskctl show XR-001)"
ok 5

# 6. Приёмка с замечаниями: обычный возврат в работу очередь не держит,
#    но выкат за задачей status называет.
taskctl move XR-001 in-progress -m "docs(tasks): XR-001 доработка после проверки" >/dev/null
shipctl status | grep -q "выкат на проде за ушедшими из Check: XR-001" || die 6 "$(shipctl status)"
shipctl status | grep -q "очередь свободна" || die 6 "$(shipctl status)"
ok 6

# 7. Следующая задача при этом сливается штатно.
shipctl merge XR-002 --test true >"$TMP/out" 2>&1 || die 7 "$(cat "$TMP/out")"
grep -q "XR-002 слита в main" "$TMP/out" || die 7 "$(cat "$TMP/out")"
ok 7

# 8. Откат тоже гасит признак: прод вернулся к прежнему состоянию.
taskctl fail XR-001 --reason "снова 500" -m "docs(tasks): XR-001 снова провал" >/dev/null
shipctl revert XR-001 --test true >"$TMP/out" 2>&1 || die 8 "$(cat "$TMP/out")"
grep -q "признак провала XR-001 снят откатом" "$TMP/out" || die 8 "$(cat "$TMP/out")"
! taskctl show XR-001 | grep -q "провал:" || die 8 "$(taskctl show XR-001)"
ok 8

# 9. Доска после всех переходов остаётся здоровой.
taskctl lint >"$TMP/out" 2>&1 || die 9 "$(cat "$TMP/out")"
ok 9

echo "СЦЕНАРИЙ ПРОШЁЛ"
rm -rf "$TMP"
