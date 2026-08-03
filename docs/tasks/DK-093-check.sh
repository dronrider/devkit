#!/bin/sh
# Сценарий проверки DK-093 на синтетической цели: живой проект, живая доска и
# настоящий снимок квоты не трогаются. С DEVKIT=<клон ветки> берутся бинари
# ветки (собираются во временный каталог и подменяют PATH), без него
# выкаченный agentctl из PATH.
set -e
T=$(mktemp -d)
DEVKIT_SRC=${DEVKIT:-$HOME/projects/devkit}
if [ -n "${DEVKIT:-}" ]; then
	mkdir -p "$T/bin"
	(cd "$DEVKIT/agentctl" && go build -o "$T/bin/agentctl" .)
	PATH=$T/bin:$PATH
	export PATH
fi
export HOME="$T/home" DEVKIT_HOME="$DEVKIT_SRC" DEVKIT_HARNESS=claude-code
mkdir -p "$T/docs/tasks" "$HOME/.devkit/quota"
RESET=$(date -v+3d +%Y-%m-%dT%H:%M 2>/dev/null || date -d '+3 days' +%Y-%m-%dT%H:%M)

printf '# Тест: задачи (префикс T)\n\n## In progress\n\n| ID | Задача | Тип | P | R | Цена | Ссылка |\n|---|---|---|---|---|---|---|\n| T-001 | Цель: пример | task | P1 | 40 (25+7+2+0+6) | XL | [tasks/T-001.md](tasks/T-001.md) |\n| T-002 | задача цели | task | P2 | 34 (25+4+1+0+4) | M | [tasks/T-001.md](tasks/T-001.md) |\n' > "$T/docs/TASKS.md"
cat > "$T/docs/tasks/T-001.md" <<'EOF'
# T-001: Цель: пример

## Бюджет

бюджет: week_all <= 25
ярус: base

## Журнал

- снимок 2026-08-03T09:00: week_all 10%

## Итог

Пока пусто.
EOF
printf '# T-002\n\n## Ход работы\n' > "$T/docs/tasks/T-002.md"
{ echo "taken = $(date +%Y-%m-%dT%H:%M)"; echo "week_all = 22% сброс $RESET"; } > "$HOME/.devkit/quota/claude-code.local"

agentctl -C "$T" spend --goal docs/tasks/T-001.md > "$T/out1"
grep -q '^gate: ok$' "$T/out1"
grep -q 'week_all: потрачено 12 из 25' "$T/out1"
echo "шаг 1: ok"

agentctl -C "$T" spend --goal docs/tasks/T-001.md --record > /dev/null
grep -q '^- снимок .*: week_all 22%$' "$T/docs/tasks/T-001.md"
grep -q '^Пока пусто.$' "$T/docs/tasks/T-001.md"
agentctl -C "$T" spend --goal docs/tasks/T-001.md | grep -q 'потрачено 12 из 25'
echo "шаг 2: ok"

agentctl -C "$T" pick T-002 --goal docs/tasks/T-001.md --record > "$T/out3"
grep -q '^tier: base$' "$T/out3"
grep -q 'потолок цели: base, pro -> base' "$T/out3"
grep -q 'маппинг opus, потолок цели: base, pro -> base' "$T/docs/tasks/T-002.md"
agentctl -C "$T" pick T-002 | grep -q '^tier: pro$'
echo "шаг 3: ok"

sed -e 's/^бюджет: week_all <= 25/бюджет: week_all <= 10/' "$T/docs/tasks/T-001.md" > "$T/tmp" && mv "$T/tmp" "$T/docs/tasks/T-001.md"
agentctl -C "$T" spend --goal docs/tasks/T-001.md > "$T/out4"
grep -q '^gate: over$' "$T/out4"
grep -q 'бюджет цели исчерпан по week_all' "$T/out4"
echo "шаг 4: ok"

sed -e 's/^бюджет: week_all <= 10/бюджет: week_gpt <= 10/' "$T/docs/tasks/T-001.md" > "$T/tmp" && mv "$T/tmp" "$T/docs/tasks/T-001.md"
if agentctl -C "$T" spend --goal docs/tasks/T-001.md > "$T/out5" 2>&1; then
	echo "шаг 5: провал, незнакомый бакет прошёл молча"
	cat "$T/out5"
	exit 1
fi
grep -q 'week_gpt' "$T/out5"
echo "шаг 5: ok"

rm -rf "$T"
echo "СЦЕНАРИЙ ПРОШЁЛ"
