#!/bin/sh
# Сценарий проверки DK-1124: живой замер отзывчивости taskctl и дашборда под
# нагрузкой второго полного прогона tools/devkitctl/parallel.py, рядом с ним.
# Десять замеров подряд, каждый не дольше порога первым аргументом (по
# умолчанию 5 с), потолок 30 с подпроцессов дашборда (tools/dashboard/board.go)
# не должен быть задет ни разу. Порт и токен читаются из живого конфига
# дашборда, ~/.devkit/dashboard.local: сценарий рассчитан на уже запущенный
# сервис, как он живёт на машине агента.
#
# DEVKIT_HOME, DEVKIT_LOAD_CMD и DEVKIT_PROBE_INTERVAL это точки подмены для
# check_test.py рядом: тест глушит настоящий прогон и настоящий HOME
# стендом, а логику порога и разбора конфига гоняет по-настоящему.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
LIMIT=${1:-5}
HOME_DIR=${DEVKIT_HOME:-$HOME}
CFG="$HOME_DIR/.devkit/dashboard.local"
LOG="$HOME_DIR/.devkit/dashboard.log"
LOAD_CMD=${DEVKIT_LOAD_CMD:-"cd '$ROOT' && python3 tools/devkitctl/parallel.py"}
PROBE_INTERVAL=${DEVKIT_PROBE_INTERVAL:-120}

port=$(awk -F'= *' '/^[[:space:]]*port[[:space:]]*=/{print $2}' "$CFG" 2>/dev/null | tail -n 1)
port=${port:-7112}
token=$(awk -F'= *' '/^[[:space:]]*token[[:space:]]*=/{print $2}' "$CFG" 2>/dev/null | tail -n 1)
base="http://127.0.0.1:$port"

start=$(date -u +%Y-%m-%dT%H:%M:%S)
cookies=$(mktemp)
trap 'rm -f "$cookies"' EXIT

curl -sS -c "$cookies" -H 'Content-Type: application/json' \
	-d "{\"token\": \"$token\"}" "$base/api/login" >/dev/null

# Нагрузка: второй полный прогон рядом с замером, как при реальном слиянии.
sh -c "$LOAD_CMD" >/dev/null 2>&1 &
load_pid=$!

over=0
i=1
while [ "$i" -le 10 ]; do
	t0=$(date +%s.%N)
	taskctl list --json >/dev/null
	t1=$(date +%s.%N)
	tk=$(echo "$t1 - $t0" | bc)

	t0=$(date +%s.%N)
	curl -sS -b "$cookies" "$base/api/projects" >/dev/null
	t1=$(date +%s.%N)
	ap=$(echo "$t1 - $t0" | bc)

	printf 'замер %s: taskctl=%ss api/projects=%ss\n' "$i" "$tk" "$ap"
	if [ "$(echo "$tk > $LIMIT" | bc)" = 1 ] || [ "$(echo "$ap > $LIMIT" | bc)" = 1 ]; then
		over=$((over + 1))
	fi
	i=$((i + 1))
	[ "$i" -le 10 ] && sleep "$PROBE_INTERVAL"
done

wait "$load_pid" || true

echo "--- потолок 30с из board.go, окно с $start ---"
if [ -f "$LOG" ] && awk -v s="$start" '$0 >= s' "$LOG" | grep -q "не ответил за 30s"; then
	echo "потолок задет: строки ниже"
	awk -v s="$start" '$0 >= s' "$LOG" | grep "не ответил за 30s"
else
	echo "потолок не задет"
fi

if [ "$over" -gt 0 ]; then
	echo "FAILED: $over замер(ов) дольше ${LIMIT}с"
	exit 1
fi
echo "OK"
