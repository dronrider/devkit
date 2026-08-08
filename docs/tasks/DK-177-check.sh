#!/bin/sh
# Сценарий проверки DK-177: назначение яруса в машинном слое, строка via
# вердикта и запись про подписку. Живой ~/.devkit не трогается: HOME временный,
# профили и машинный конфиг синтетические, доска берётся из репозитория.
# Аргумент это чекаут devkit (по умолчанию текущий репозиторий).
set -e
SRC=${1:-$(git rev-parse --show-toplevel)}
W=$(mktemp -d)
HOME="$W/home"
export HOME
mkdir -p "$HOME/.devkit" "$W/devkit/kit/harness" "$W/proj/docs/tasks"

echo "--- 0. бинарь из ветки и синтетический контур"
(cd "$SRC/tools/agentctl" && GOWORK=off go build -o "$W/agentctl" .)
cp "$SRC/kit/harness/claude-code.toml" "$W/devkit/kit/harness/claude-code.toml"
cat > "$W/devkit/kit/harness/glm-code.toml" <<'EOF'
[detect]

[rules]
mode = "embed"

[delegate]
mode = "cli"
command = ["claude", "-p", "{prompt}"]

[hooks]

[quota]
EOF
cat > "$HOME/.devkit/harness.local" <<'EOF'
default = "claude-code"
enabled = ["claude-code", "glm-code"]

[claude-code]
mini = "haiku"
base = "glm-code:glm-5.2"
pro = "opus"
max = "fable"

[glm-code]
bin = "/opt/claude"
EOF
cp "$SRC/docs/TASKS.md" "$W/proj/docs/TASKS.md"
cp "$SRC/docs/tasks/DK-177.md" "$W/proj/docs/tasks/DK-177.md"
DEVKIT_HOME="$W/devkit"
export DEVKIT_HOME
DEVKIT_HARNESS=""
export DEVKIT_HARNESS

echo "--- 1. harness называет уехавшую ступень и секцию без ярусов"
"$W/agentctl" harness -C "$W/proj"

echo "--- 2. вердикт на домашней ступени: via свой же харнес, модель без префикса"
"$W/agentctl" pick DK-177 -C "$W/proj"

echo "--- 3. вердикт на уехавшей ступени: via чужой харнес, корректор молчит с причиной"
"$W/agentctl" pick DK-177 -C "$W/proj" --role review

echo "--- 4. дефицит домашней подписки не двигает вердикт через её границу"
mkdir -p "$HOME/.devkit/quota"
NOW=$(date +%Y-%m-%dT%H:%M)
RESET=$(date -v+3d +%Y-%m-%dT%H:%M 2>/dev/null || date -d '+3 days' +%Y-%m-%dT%H:%M)
printf 'taken = %s\nweek_all = 95%% сброс %s\nweek_max = 50%% сброс %s\n' "$NOW" "$RESET" "$RESET" \
	> "$HOME/.devkit/quota/claude-code.local"
"$W/agentctl" pick DK-177 -C "$W/proj"

echo "--- 5. запись --record несёт харнес:модель и слово по режиму делегирования"
"$W/agentctl" pick DK-177 -C "$W/proj" --role review --record
# Свежая запись это последняя строка раздела «Ход работы», а не конец файла:
# ниже в файле задачи лежит этот же прогон, да и записей в разделе к моменту
# прогона накопится сколько угодно.
sed -n '/^## Ход работы/,/^## /p' "$W/proj/docs/tasks/DK-177.md" | grep '^- ' | tail -1

echo "--- 6. битое назначение: прочерки в обеих машинных строках, pick не падает"
sed 's/glm-code:glm-5.2/nowhere:m/' "$HOME/.devkit/harness.local" > "$W/broken"
cp "$W/broken" "$HOME/.devkit/harness.local"
"$W/agentctl" pick DK-177 -C "$W/proj" --role review
"$W/agentctl" harness -C "$W/proj" | grep "назначение битое"

echo "--- 7. пустая половина назначения это отказ чтения слоя"
sed 's/base = "nowhere:m"/base = ":opus"/' "$HOME/.devkit/harness.local" > "$W/bad"
cp "$W/bad" "$HOME/.devkit/harness.local"
if "$W/agentctl" harness -C "$W/proj"; then echo "ОШИБКА: опечатка принята"; exit 1; fi

echo "--- всё"
