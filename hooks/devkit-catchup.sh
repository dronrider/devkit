#!/bin/sh
# SessionStart-хук: догоняет сам чекаут devkit до origin/main, пока сессия не
# прочла из него устаревшие правила и не разложила устаревший kit. Гард и само
# действие живут в devkitctl catchup --hook: хук это тонкая обёртка, как
# board-catchup.sh вокруг taskctl. Молчит всё, кроме подтяга, расхождения с
# origin и отказа слияния.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cli="$here/../tools/devkitctl/devkitctl.py"
# Зовётся python-часть по пути, а не обёртка devkitctl из PATH: обёртка ведёт в
# тот чекаут, из которого её ставили, а догонять надо этот.
[ -f "$cli" ] || exit 0
command -v python3 >/dev/null 2>&1 || exit 0
# stderr подрезается: у старого чекаута нет подкоманды catchup, и «неизвестная
# команда» на каждом старте сессии это шум. Пустой вывод не печатается вовсе.
out=$(python3 "$cli" catchup --hook 2>/dev/null)
[ -n "$out" ] && printf '%s\n' "$out"
exit 0
