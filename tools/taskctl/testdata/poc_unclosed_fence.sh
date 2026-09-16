#!/bin/sh
# Живой отказ обкатки на незакрытом ограждении (DK-820): временный проект с
# доской, файл задачи с потерянным ``` во втором шаге, прогон taskctl rehearse.
# Ждём код возврата не ноль, разбор в тексте отказа и файл задачи байт в байт
# прежний.
set -eu

proj=$(mktemp -d)
trap 'rm -rf "$proj"' EXIT
mkdir -p "$proj/docs/tasks"
printf '# Доска\n\n## In progress\n\n| XR-005 | проба | bug | P2 | 10 | S | [tasks/XR-005.md](tasks/XR-005.md) |\n\n## Backlog\n' > "$proj/docs/TASKS.md"
printf '# XR-005\n\n## Сценарий проверки\n\nАгентский.\n\n```sh\ntrue\n```\n\n2. Второй шаг.\n\n```sh\ntrue\n\nОжидание: строка встала в Check.\n\n3. Третий шаг руками.\n\n## Проверка\n\n```console\nчужой вывод\n```\n' > "$proj/docs/tasks/XR-005.md"
before=$(cksum < "$proj/docs/tasks/XR-005.md")
git -C "$proj" init -q .
git -C "$proj" add -A
git -C "$proj" -c user.email=t@t -c user.name=t commit -qm init

out=$(taskctl rehearse -C "$proj" XR-005 2>&1) && code=0 || code=$?
if [ "$code" = 0 ]; then
	echo "poc_unclosed_fence: обкатка прошла, а должна была отказать" >&2
	exit 1
fi
for want in 'docs/tasks/XR-005.md' 'строкой 13' 'закрыть блок'; do
	case $out in
	*"$want"*) ;;
	*)
		echo "poc_unclosed_fence: в отказе нет «$want»: $out" >&2
		exit 1
		;;
	esac
done
if [ "$(cksum < "$proj/docs/tasks/XR-005.md")" != "$before" ]; then
	echo "poc_unclosed_fence: файл задачи после отказа изменился" >&2
	exit 1
fi
if grep -q 'Обкатка' "$proj/docs/tasks/XR-005.md"; then
	echo "poc_unclosed_fence: в файле задачи осталась отметка обкатки" >&2
	exit 1
fi
echo "poc_unclosed_fence: обкатка отказала до свежего дерева, назвала файл и строку 13, файл задачи не тронут"
