#!/bin/sh
# Сценарий проверки DK-679: профиль называет обе дороги делегирования, и
# уехавшая ступень уходит командой снаружи. Стенд синтетический, живая доска и
# живой ~/.devkit не трогаются. Без DEVKIT берётся agentctl из PATH (проверка
# после выката), с DEVKIT=<путь к клону> бинарь собирается оттуда (обкатка на
# ветке). Профили шага 4 читаются из клона devkit: без него шаг пропускается.
set -eu
TMP=$(mktemp -d)
BIN=$TMP/bin
HOME_DIR=$TMP/home
PROJ=$TMP/proj
KIT=${DEVKIT:-$HOME/projects/devkit}
mkdir -p "$BIN" "$HOME_DIR/kit/harness" "$HOME_DIR/kit/agents" "$HOME_DIR/.devkit" "$PROJ/docs"
if [ -n "${DEVKIT:-}" ]; then
	(cd "$DEVKIT/tools/agentctl" && GOWORK=off go build -o "$BIN/agentctl" .)
	export PATH="$BIN:$PATH"
fi
export HOME="$HOME_DIR" DEVKIT_HOME="$HOME_DIR"
unset DEVKIT_RUN_DEPTH DEVKIT_HARNESS 2>/dev/null || true

for e in low medium high xhigh; do
	printf '%s\n' '---' "name: exec-$e" '---' '' 'Тело определения.' \
		> "$HOME_DIR/kit/agents/exec-$e.md"
done
# Раздающий харнес: своя команда, чтобы домашняя ступень тоже отдавалась.
printf '%s\n' '[detect]' '' '[rules]' 'mode = "embed"' '' '[delegate]' 'mode = "cli"' \
	'command = ["/bin/sh", "-c", "echo раздающий отработал"]' '' '[hooks]' '' '[quota]' \
	> "$HOME_DIR/kit/harness/echocli.toml"
# Харнес назначения со спавном внутри сессии и командой наружу.
printf '%s\n' '[detect]' '' '[rules]' 'mode = "embed"' '' '[delegate]' 'mode = "native"' \
	'command = ["/bin/sh", "-c", "echo делегат отработал model={model}"]' '' '[hooks]' '' '[quota]' \
	> "$HOME_DIR/kit/harness/nativecmd.toml"
# Харнес назначения, назвавший один спавн: снаружи поднять его нечем.
printf '%s\n' '[detect]' '' '[rules]' 'mode = "embed"' '' '[delegate]' 'mode = "native"' '' \
	'[hooks]' '' '[quota]' > "$HOME_DIR/kit/harness/nativeonly.toml"
printf '%s\n' '# demo: задачи (префикс T)' '' 'Проза шапки, таблицей не является.' '' \
	'## In progress' '' '| ID | Задача | Тип | P | R | Цена | Ссылка |' \
	'|---|---|---|---|---|---|---|' \
	'| T-002 | ступень уезжает в обе стороны | task | P2 | 34 (25+4+1+0+4) | M | - |' '' \
	'## Check' '' 'Нет.' > "$PROJ/docs/TASKS.md"

machine() {
	printf '%s\n' 'enabled = ["echocli"]' 'default = "echocli"' '' '[echocli]' \
		'mini = "cheap"' 'base = "cheap"' "pro = \"$1\"" 'max = "strong"' \
		> "$HOME_DIR/.devkit/harness.local"
}

die() { printf 'шаг %s: ПРОВАЛ\n%s\n' "$1" "$2" >&2; exit 1; }

cd "$PROJ"

machine 'nativecmd:чужая'
away=$(agentctl run T-002 --workdir "$PROJ" 2>&1) || die 1 "$away"
echo "$away" | grep -q 'делегирование: команда снаружи (харнес nativecmd' || die 1 "$away"
echo "$away" | grep -q 'делегат отработал model=чужая' || die 1 "$away"
echo "$away" | grep -q 'делегат вернулся: задача T-002, харнес nativecmd, код выхода 0' ||
	die 1 "$away"
printf 'шаг 1: ok, уехавшая ступень поднята командой профиля\n'

machine "strong"
home=$(agentctl run T-002 --workdir "$PROJ" 2>&1) || die 2 "$home"
echo "$home" | grep -q 'раздающий отработал' || die 2 "$home"
if echo "$home" | grep -q 'делегат отработал'; then
	die 2 "$home"
fi
printf 'шаг 2: ok, домашняя ступень осталась у своего харнеса\n'

machine 'nativeonly:чужая'
code=0
deny=$(agentctl run T-002 --workdir "$PROJ" 2>&1) || code=$?
[ "$code" = 3 ] || die 3 "код возврата $code, жду 3
$deny"
echo "$deny" | grep -q 'делегировать нечем' || die 3 "$deny"
echo "$deny" | grep -q 'не назвал' || die 3 "$deny"
printf 'шаг 3: ok, профиль без команды отказывает кодом 3\n'

if [ -f "$KIT/kit/harness/claude-code.toml" ]; then
	printf '%s\n' 'enabled = ["claude-code", "glm-code"]' 'default = "glm-code"' '' \
		'[glm-code]' 'mini = "glm-5.3-flash"' 'base = "glm-5.3"' \
		'pro = "claude-code:opus"' 'max = "claude-code:opus"' \
		> "$HOME_DIR/.devkit/harness.local"
	shown=$(DEVKIT_HOME="$KIT" agentctl harness --harness glm-code 2>&1) || die 4 "$shown"
	echo "$shown" | grep -q 'назначение: ярус pro уезжает в claude-code, поднимается командой' ||
		die 4 "$shown"
	printf 'шаг 4: ok, достижимость первой подписки видна командой harness\n'
else
	printf 'шаг 4: пропущен, клона devkit нет в %s\n' "$KIT"
fi

printf 'DK-679: сценарий пройден\n'
