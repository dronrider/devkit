#!/bin/sh
# Сценарий проверки DK-1168: сколько подпроцессов git поднимает выкаченный
# `taskctl list --json` на живой доске. Считается факт состава хода, а не стенное
# время: число одно и то же под любой нагрузкой машины, а время под соседним
# полным прогоном плывёт втрое-впятеро (замер в docs/tasks/DK-1168.md).
#
# Первый аргумент это потолок числа запусков (по умолчанию 12), второй корень
# проекта (по умолчанию тот, откуда лежит сам сценарий). На PATH кладётся
# подставной git, который дописывает свои аргументы в журнал и отдаёт работу
# настоящему.
set -eu
LIMIT=${1:-12}
ROOT=${2:-$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)}
REAL=$(command -v git)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

cat > "$WORK/git" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$WORK/calls.log"
exec "$REAL" "\$@"
EOF
chmod +x "$WORK/git"
: > "$WORK/calls.log"

PATH="$WORK:$PATH" taskctl -C "$ROOT" list --json > "$WORK/board.json"

n=$(wc -l < "$WORK/calls.log" | tr -d ' ')
rows=$(grep -o '"id"' "$WORK/board.json" | wc -l | tr -d ' ')
echo "строк доски: $rows, подпроцессов git: $n, потолок: $LIMIT"
sed 's/^/  git /' "$WORK/calls.log" | cut -c1-100

if [ "$n" -gt "$LIMIT" ]; then
	echo "FAILED: $n подпроцессов git при потолке $LIMIT"
	exit 1
fi
echo OK
