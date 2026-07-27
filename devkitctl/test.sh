#!/bin/sh
# Самопроверка devkitctl: подключение проекта и диагностика обвязки гоняются
# на временном репозитории с подставным HOME.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
fail() { echo "FAIL: $1" >&2; fails=$((fails + 1)); }

home="$tmp/home"
mkdir -p "$home/.claude"
cat > "$home/.claude/settings.json" <<'EOF'
{"hooks": {"PostToolUse": [{"matcher": "Edit|Write|NotebookEdit", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-symbols.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-memory.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-sensitive.py --hook"}
]}]}}
EOF

proj="$tmp/proj"
mkdir -p "$proj"
git init -q "$proj"
git -C "$proj" config user.name t
git -C "$proj" config user.email t@t

# new: CLAUDE.md из шаблона, hooksPath на хуки devkit, повторный запуск отбит.
HOME="$home" python3 "$here/devkitctl.py" new --no-board -C "$proj" >/dev/null || fail "new не прошёл"
[ -f "$proj/CLAUDE.md" ] || fail "new не создал CLAUDE.md"
grep -q '<название проекта>' "$proj/CLAUDE.md" && fail "плейсхолдер названия не заменён"
hp=$(git -C "$proj" config core.hooksPath)
[ -n "$hp" ] || fail "hooksPath не выставлен"
[ -f "$proj/$hp/pre-commit" ] || fail "hooksPath смотрит мимо хуков devkit: $hp"
HOME="$home" python3 "$here/devkitctl.py" new --no-board -C "$proj" >/dev/null 2>&1
[ $? -eq 2 ] || fail "повторный new не отбит"

# doctor: свежеподключённый проект чист.
out=$(HOME="$home" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
[ $? -eq 0 ] || fail "doctor нашёл находки на чистом проекте: $out"

# doctor: битый импорт, битая ссылка и пропавший PostToolUse-хук ловятся.
printf '@../devkit/NOPE.md\n' >> "$proj/CLAUDE.md"
mkdir -p "$proj/docs"
printf 'смотри [детали](nope.md)\n' > "$proj/docs/note.md"
out=$(HOME="$home" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
[ $? -eq 1 ] || fail "doctor не увидел поломок"
echo "$out" | grep -q 'импорт' || fail "нет находки про битый импорт"
echo "$out" | grep -q 'битая ссылка' || fail "нет находки про битую ссылку"
sed -e '/check-memory/d' "$home/.claude/settings.json" > "$home/.claude/settings.json.new" &&
    mv "$home/.claude/settings.json.new" "$home/.claude/settings.json"
out=$(HOME="$home" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'check-memory' || fail "нет находки про пропавший хук памяти"

# doctor: доска без taskctl в PATH это находка (PATH обрезан до системного).
printf '# Задачи\n' > "$proj/docs/TASKS.md"
out=$(HOME="$home" PATH="/usr/bin:/bin" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'taskctl не в PATH' || fail "нет находки про taskctl: $out"

# Отдельный проект с доской: new заводит болванку выката и гитигнорит её.
bproj="$tmp/bproj"
mkdir -p "$bproj"
git init -q "$bproj"
git -C "$bproj" config user.name t
git -C "$bproj" config user.email t@t
HOME="$home" python3 "$here/devkitctl.py" new --prefix BP -C "$bproj" >/dev/null 2>&1
[ -f "$bproj/.devkit/deploy.local" ] || fail "new не завёл .devkit/deploy.local"
grep -q '^autonomous = false' "$bproj/.devkit/deploy.local" || fail "в болванке нет autonomous"
git -C "$bproj" check-ignore -q .devkit/deploy.local || fail ".devkit/deploy.local не гитигнорнут"

# Журнал запусков: new записал свою строку в .devkit/log, файл гитигнорнут.
tab=$(printf '\t')
[ -f "$bproj/.devkit/log" ] || fail "new не записал журнал запусков"
grep -q "devkitctl${tab}new${tab}0" "$bproj/.devkit/log" || fail "в журнале нет строки про new"
git -C "$bproj" check-ignore -q .devkit/log || fail ".devkit/log не гитигнорнут"

# doctor: пустой deploy= это находка, заполненный и гитигнорнутый чист.
out=$(HOME="$home" PATH="/usr/bin:/bin" python3 "$here/devkitctl.py" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'пустой deploy=' || fail "нет находки про пустую команду выката: $out"
printf 'deploy = make deploy\nautonomous = false\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="/usr/bin:/bin" python3 "$here/devkitctl.py" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'deploy' && fail "заполненная обвязка выката всё ещё в находках: $out"

# doctor: команда есть, но файл не гитигнорнут это находка.
nproj="$tmp/nproj"
mkdir -p "$nproj/.devkit"
git init -q "$nproj"
git -C "$nproj" config user.name t
git -C "$nproj" config user.email t@t
HOME="$home" python3 "$here/devkitctl.py" new --no-board -C "$nproj" >/dev/null 2>&1
mkdir -p "$nproj/docs"
printf '# Задачи\n' > "$nproj/docs/TASKS.md"
printf 'deploy = make deploy\nautonomous = false\n' > "$nproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="/usr/bin:/bin" python3 "$here/devkitctl.py" doctor -C "$nproj" 2>&1)
echo "$out" | grep -q 'не гитигнорнут' || fail "нет находки про негитигнорнутый конфиг: $out"

# doctor --fix доводит обвязку проекта, подключённого до появления выката:
# заводит deploy.local с гитигнором и возвращает отвязанные хуки.
fproj="$tmp/fproj"
mkdir -p "$fproj"
git init -q "$fproj"
git -C "$fproj" config user.name t
git -C "$fproj" config user.email t@t
HOME="$home" python3 "$here/devkitctl.py" new --prefix FP -C "$fproj" >/dev/null 2>&1
rm -f "$fproj/.devkit/deploy.local"
git -C "$fproj" config --unset core.hooksPath
out=$(HOME="$home" python3 "$here/devkitctl.py" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'починено' || fail "doctor --fix ничего не починил: $out"
[ -f "$fproj/.devkit/deploy.local" ] || fail "doctor --fix не завёл deploy.local"
git -C "$fproj" check-ignore -q .devkit/deploy.local || fail "doctor --fix не гитигнорил deploy.local"
[ -n "$(git -C "$fproj" config core.hooksPath)" ] || fail "doctor --fix не подключил хуки"
# Пустой deploy= остаётся находкой: команду выката --fix не выдумывает.
echo "$out" | grep -q 'пустой deploy=' || fail "doctor --fix должен просить вписать команду: $out"

# Повторный --fix уже ничего не меняет (идемпотентность).
out=$(HOME="$home" python3 "$here/devkitctl.py" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный doctor --fix не должен ничего менять: $out"

# Заполненная команда: находки по выкату уходят.
printf 'deploy = make deploy\nautonomous = false\n' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" python3 "$here/devkitctl.py" doctor -C "$fproj" 2>&1)
echo "$out" | grep -q 'deploy' && fail "заполненная обвязка выката всё ещё в находках: $out"

if [ $fails -eq 0 ]; then
    echo "devkitctl в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
