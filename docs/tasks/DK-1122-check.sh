#!/bin/sh
# Сценарий проверки DK-1122: один зелёный прогон tools/devkitctl/parallel.py
# на заданных -j с load average до и после. Первый аргумент это список -j
# через пробел (по умолчанию "3 4 5 8"), второй число повторов на каждое
# значение (по умолчанию 1). Стена меряется снаружи прогона (date +%s), а не
# строкой самого раннера, чтобы захватить и обвязку python3.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
JOBS=${1:-"3 4 5 8"}
REPS=${2:-1}
# Носитель сессии текущего конвейера (DEVKIT_GOAL_SHELL) не должен просачиваться
# в дерево прогона: TestWatchRegisterNamesSessionAndCarrier читает его напрямую
# из окружения процесса, и ambient-значение красит замер вне зависимости от -j.
unset DEVKIT_GOAL_SHELL 2>/dev/null || true

load_now() {
	uptime | sed 's/.*load average[s]*: *//'
}

run_one() {
	j=$1
	n=$2
	before=$(load_now)
	start=$(date +%s)
	if out=$(cd "$ROOT" && python3 tools/devkitctl/parallel.py -j "$j" 2>&1); then
		code=0
	else
		code=$?
	fi
	end=$(date +%s)
	after=$(load_now)
	wall=$((end - start))
	printf -- '-j %s run %s: exit=%s wall=%ss load_before=%s load_after=%s\n' \
		"$j" "$n" "$code" "$wall" "$before" "$after"
	if [ "$code" != 0 ]; then
		printf '%s\n' "$out"
	else
		printf '%s\n' "$out" | tail -n 3
	fi
}

for j in $JOBS; do
	i=1
	while [ "$i" -le "$REPS" ]; do
		run_one "$j" "$i"
		i=$((i + 1))
	done
done
