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

if [ $fails -eq 0 ]; then
    echo "devkitctl в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
