#!/bin/sh
# Сценарий проверки DK-307: раздача уехавшей ступени из живого окна и из сессии
# без окна. Стенд синтетический, живая доска и живой ~/.devkit не трогаются.
# Без DEVKIT берётся agentctl из PATH (проверка после выката), с DEVKIT=<путь к
# клону> бинарь собирается оттуда (обкатка на ветке).
set -eu
TMP=$(mktemp -d)
BIN=$TMP/bin
HOME_DIR=$TMP/home
PROJ=$TMP/proj
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
printf '%s\n' '[detect]' '' '[rules]' 'mode = "embed"' '' '[delegate]' 'mode = "cli"' \
	'command = ["/bin/sh", "-c", "echo делегат отработал"]' '' '[hooks]' '' '[quota]' \
	> "$HOME_DIR/kit/harness/echocli.toml"
printf '%s\n' 'enabled = ["echocli"]' 'default = "echocli"' '' '[echocli]' \
	'mini = "cheap"' 'base = "cheap"' 'pro = "strong"' 'max = "strong"' \
	> "$HOME_DIR/.devkit/harness.local"
printf '%s\n' '# demo: задачи (префикс T)' '' 'Проза шапки, таблицей не является.' '' \
	'## In progress' '' '| ID | Задача | Тип | P | R | Цена | Ссылка |' \
	'|---|---|---|---|---|---|---|' \
	'| T-002 | ступень второй подписки | task | P2 | 34 (25+4+1+0+4) | M | - |' '' \
	'## Check' '' 'Нет.' > "$PROJ/docs/TASKS.md"

die() { printf 'шаг %s: ПРОВАЛ\n%s\n' "$1" "$2" >&2; exit 1; }

cd "$PROJ"
blind=$(CLAUDE_CODE_ENTRYPOINT=sdk-cli agentctl run T-002 --workdir "$PROJ" 2>&1) ||
	die 1 "$blind"
echo "$blind" | grep -q 'делегирование из сессии без окна (CLAUDE_CODE_ENTRYPOINT=sdk-cli)' ||
	die 1 "$blind"
echo "$blind" | grep -q 'делегат отработал' || die 1 "$blind"
echo "$blind" | grep -q 'делегат вернулся: задача T-002, харнес echocli, код выхода 0' ||
	die 1 "$blind"
printf 'шаг 1: ok, сессия без окна предупреждена и работа отдана\n'

live=$(CLAUDE_CODE_ENTRYPOINT=cli agentctl run T-002 --workdir "$PROJ" 2>&1) || die 2 "$live"
if echo "$live" | grep -q 'сессии без окна'; then
	die 2 "$live"
fi
echo "$live" | grep -q 'делегат вернулся: задача T-002, харнес echocli, код выхода 0' ||
	die 2 "$live"
printf 'шаг 2: ok, из живого окна run про потолок хода молчит\n'

printf 'DK-307: сценарий пройден\n'
