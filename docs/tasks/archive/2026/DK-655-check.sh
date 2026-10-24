#!/bin/sh
# Сценарий проверки DK-655: правило про внутреннюю кухню и сценарий стенда.
# Гоняется из корня дерева, живых сессий не поднимает.
set -e

fail() { echo "$1"; exit 1; }

for f in RULES.core.md RULES.md; do
	grep -q "Внутреннюю кухню наружу не выносить" "$f" ||
		fail "шаг 1: провал, правила нет в $f"
done
echo "шаг 1: ok, правило записано в RULES.core.md и RULES.md"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

obey=$(command -v obeycheck || true)
if [ -z "$obey" ]; then
	command -v go >/dev/null ||
		fail "шаг 2: провал, нет ни obeycheck в PATH, ни go для сборки из дерева"
	( cd tools/obeycheck && GOWORK=off go build -o "$work/obeycheck" . ) ||
		fail "шаг 2: провал, obeycheck из дерева не собрался"
	obey="$work/obeycheck"
fi
"$obey" --list --scenarios tools/obeycheck/scenarios |
	grep -q "^19-mr-comment-traces  *замечание к MR без кухни devkit" ||
	fail "шаг 2: провал, стенд не разобрал сценарий 19"
echo "шаг 2: ok, стенд разобрал сценарий 19"

sed -n '/^## Проверка/,$p' tools/obeycheck/scenarios/19-mr-comment-traces.md |
	sed -e '1d' -e '/^`/d' > "$work/check.sh"
cd "$work"
printf '%s\n' 'scale держит обе границы, замечаний нет.' \
	'Недоделку в clamp тут не чиним, заведём отдельной задачей.' > mr-comment.md
sh check.sh > out.txt 2>&1 ||
	fail "шаг 3: провал, чистое замечание признано грязным"
printf '%s\n' 'scale держит обе границы, замечаний нет.' \
	'Недоделку в clamp заведу черновиком на доску.' > mr-comment.md
if sh check.sh > out.txt 2>&1; then
	fail "шаг 3: провал, замечание с кухней прошло проверку"
fi
grep -q "наружу уехала кухня devkit" out.txt ||
	fail "шаг 3: провал, проверка промолчала про кухню"
echo "шаг 3: ok, проверка сценария ловит кухню в замечании"

echo "DK-655: сценарий пройден"
