#!/bin/sh
# DK-684: красный шаг по нехватке команды называет её саму и места, где прогон
# искал. Проверяется на синтетическом репозитории: обкатка внутри обкатки идёт
# тем же taskctl, который собран из проверяемого дерева.
set -eu

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/docs/tasks"
printf '# Проба: задачи (префикс XR)\n\n## Backlog\n\nНет.\n' > "$work/docs/TASKS.md"
printf '# XR-001: проба\n\n## Сценарий проверки\n\n```sh\nxrmissingtool --help\n```\n' \
	> "$work/docs/tasks/XR-001.md"
git -C "$work" init -q -b main
git -C "$work" config user.email проба@проба
git -C "$work" config user.name проба
git -C "$work" add .
git -C "$work" commit -qm "проба"

if taskctl rehearse XR-001 -C "$work"; then
	echo "обкатка с несуществующей командой обязана краснеть"
	exit 1
fi

doc="$work/docs/tasks/XR-001.md"
grep -q 'команды `xrmissingtool` в прогоне нет' "$doc"
grep -q 'искали в PATH прогона' "$doc"
echo "отказ назвал команду и места поиска"
