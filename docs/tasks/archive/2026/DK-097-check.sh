#!/bin/sh
# Сценарий проверки DK-097: машинные шаги скилла live-core на синтетическом
# окружении. Живой ~/.claude, живая доска devkit и бинари в PATH не трогаются:
# HOME временный, devkit клонируется во временную директорию, проект пустой.
# Аргумент это чекаут devkit со скиллом (по умолчанию текущий репозиторий).
set -e
SRC=${1:-$(git rev-parse --show-toplevel)}
W=$(mktemp -d)
REAL_HOME=$HOME
HOME="$W/home"
export HOME
mkdir -p "$HOME" "$W/gobin"

echo "--- 1. скилл на месте, символы клавиатурные"
python3 "$SRC/hooks/check-symbols.py" "$SRC/skills/live-core/SKILL.md"
head -3 "$SRC/skills/live-core/SKILL.md"

echo "--- 2. правка скилла доезжает до машины копией (шаг 5 процедуры)"
git clone -q "$SRC" "$W/devkit"
mkdir -p "$W/proj"
git -C "$W/proj" init -q -b main .
python3 "$W/devkit/devkitctl/devkitctl.py" doctor --fix -C "$W/proj" 2>&1 | grep live-core
test -f "$HOME/.claude/skills/live-core/SKILL.md"

echo "--- 3. из worktree ветки задачи копия на машину не кладётся"
git -C "$W/devkit" worktree add -q "$W/devkit-wt" -b dk-000-probe
rm -rf "$HOME/.claude/skills/live-core"
python3 "$W/devkit-wt/devkitctl/devkitctl.py" doctor --fix -C "$W/proj" 2>&1 | grep "класть на машину скилл с непроверенной ветки нельзя" | head -1
test ! -e "$HOME/.claude/skills/live-core/SKILL.md"

echo "--- 4. черновик крупной находки уходит на доску devkit, а не проекта"
taskctl -C "$W/proj" init --prefix PR > /dev/null
taskctl -C "$W/devkit" draft "shipctl merge не печатает, какие файлы задел поезд"
ls "$W/devkit/docs/tasks/drafts"
test ! -d "$W/proj/docs/tasks/drafts"

echo "--- 5. пересборка бинаря в PATH (шаг 5 процедуры)"
cd "$W/devkit/taskctl"
go build -o "$W/gobin/taskctl" .
PATH="$W/gobin:$PATH"
export PATH
command -v taskctl
taskctl -C "$W/proj" list | head -2

echo "--- 6. полный прогон тестов devkit (шаг 4 процедуры)"
# Гонится прогон в самом чекауте и с настоящим HOME, как в живой сессии, где
# скилл этот шаг и делает: тесты хуков смотрят на настройки харнеса, а
# уведомитель нарочно молчит из временной директории, и во временном клоне они
# упали бы не по делу.
HOME=$REAL_HOME
export HOME
cd "$SRC"
for d in taskctl shipctl agentctl regcheck; do (cd $d && go test ./... | tail -1); done
sh hooks/test.sh > /dev/null
echo "hooks ok"
sh devkitctl/test.sh > /dev/null
echo "devkitctl ok"
python3 hooks/check-exec-bit.py
echo "exec-bit ok"
echo "--- готово, дерево: $W"
