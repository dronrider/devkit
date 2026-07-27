#!/bin/sh
# Самопроверка хуков. Запрещённые символы для тест-данных генерируются через
# printf, чтобы сами файлы devkit оставались чистыми и не спотыкались о
# собственные хуки.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
fail() { echo "FAIL: $1" >&2; fails=$((fails + 1)); }

dash=$(printf '\342\200\224') # длинное тире U+2014

# check-symbols.py: чистое проходит, тире ловится, и по файлу, и по stdin.
printf 'обычный текст, «ёлочки», № 5, ASCII -> стрелка\n' > "$tmp/clean.txt"
printf 'текст с тире %s вот\n' "$dash" > "$tmp/dirty.txt"
python3 "$here/check-symbols.py" "$tmp/clean.txt" >/dev/null || fail "чистый файл не прошёл"
out=$(python3 "$here/check-symbols.py" "$tmp/dirty.txt")
[ $? -eq 1 ] || fail "тире в файле не поймано"
case $out in "$tmp/dirty.txt:1:"*) ;; *) fail "находка без файл:строка: $out" ;; esac
printf 'строка с тире %s\n' "$dash" | python3 "$here/check-symbols.py" --stdin >/dev/null
[ $? -eq 1 ] || fail "тире в stdin не поймано"

# pre-commit: ловит тире в добавленных строках и молчит про уже закоммиченные.
repo="$tmp/repo"
git init -q "$repo"
git -C "$repo" config user.name t
git -C "$repo" config user.email t@t
printf 'первая строка\nстарое тире %s тут\nтретья строка\n' "$dash" > "$repo/f.txt"
git -C "$repo" add f.txt
(cd "$repo" && "$here/pre-commit" >/dev/null 2>&1)
[ $? -eq 1 ] || fail "pre-commit пропустил тире в новом файле"
git -C "$repo" commit -qm 'seed'
printf 'первая строка правлена\nстарое тире %s тут\nтретья строка\n' "$dash" > "$repo/f.txt"
git -C "$repo" add f.txt
(cd "$repo" && "$here/pre-commit" >/dev/null 2>&1) || fail "pre-commit ругается на нетронутую чужую строку"

# commit-msg: текст проверяется, комментарии git нет. Запуск из репозитория:
# хук читает историю для проверки префикса, снаружи она была бы чужой.
printf 'fix: обычный коммит\n' > "$tmp/msg"
(cd "$repo" && "$here/commit-msg" "$tmp/msg" >/dev/null 2>&1) || fail "чистый коммит не прошёл"
printf 'fix: коммит с тире %s\n' "$dash" > "$tmp/msg"
(cd "$repo" && "$here/commit-msg" "$tmp/msg" >/dev/null 2>&1)
[ $? -eq 1 ] || fail "тире в тексте коммита не поймано"
printf 'fix: чисто\n# комментарий с тире %s\n' "$dash" > "$tmp/msg"
(cd "$repo" && "$here/commit-msg" "$tmp/msg" >/dev/null 2>&1) || fail "commit-msg смотрит в комментарии git"

# check-commit.py: следы ассистента и body ловятся, чистое проходит.
hist='feat(core): раз\nfix: два\ndocs: три'
printf 'fix: чистая строка\n' > "$tmp/msg"
printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null || fail "чистый коммит не прошёл check-commit"
printf 'fix: правка\n\nCo-authored-by: Claude <noreply@anthropic.com>\n' > "$tmp/msg"
out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
[ $? -eq 1 ] || fail "след ассистента не пойман"
echo "$out" | grep -q 'след ассистента' || fail "нет находки про след ассистента"
printf 'fix: правка\n\nразвёрнутое body с деталями\n' > "$tmp/msg"
out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
[ $? -eq 1 ] || fail "body не пойман"
echo "$out" | grep -q 'одной строкой' || fail "нет находки про body"
printf 'fix: чисто\n# Co-authored-by в комментарии\n' > "$tmp/msg"
printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null || fail "check-commit смотрит в комментарии git"

# check-commit.py: тип не из истории ловится, revert и свежий репозиторий проходят.
printf 'perf: чужой тип\n' > "$tmp/msg"
out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
[ $? -eq 1 ] || fail "чужой тип префикса не пойман"
echo "$out" | grep -q 'не встречается в истории' || fail "нет находки про тип"
printf 'revert: XR-1 откат правки\n' > "$tmp/msg"
printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null || fail "revert должен проходить всегда"
printf 'perf: первый типизированный\n' > "$tmp/msg"
printf '\n' | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null || fail "пустая история не должна включать проверку префикса"

# commit-msg целиком: типизированная история включает проверку префикса.
repoc="$tmp/repoc"
git init -q "$repoc"
git -C "$repoc" config user.name t
git -C "$repoc" config user.email t@t
printf 'x\n' > "$repoc/f.txt"
git -C "$repoc" add f.txt
git -C "$repoc" commit -qm 'feat: сид истории'
printf 'perf: чужой тип\n' > "$tmp/msg"
(cd "$repoc" && "$here/commit-msg" "$tmp/msg" >/dev/null 2>&1)
[ $? -eq 1 ] || fail "commit-msg пропустил чужой тип"
printf 'feat: свой тип\n' > "$tmp/msg"
(cd "$repoc" && "$here/commit-msg" "$tmp/msg" >/dev/null 2>&1) || fail "commit-msg отбил тип из истории"

# Режим --hook: тире в new_string даёт выход 2, чистый текст 0.
printf '{"tool_input":{"file_path":"x.md","new_string":"плохо %s"}}' "$dash" |
    python3 "$here/check-symbols.py" --hook 2>/dev/null
[ $? -eq 2 ] || fail "режим --hook пропустил тире"
printf '{"tool_input":{"file_path":"x.md","new_string":"чисто, «ёлочки», № 5"}}' |
    python3 "$here/check-symbols.py" --hook 2>/dev/null || fail "режим --hook ругается на чистое"

# check-memory.py: короткие строки-указатели проходят, жир и прозу ловит.
long=$(printf 'x%.0s' $(seq 1 170))
printf -- '- [Запись](file.md) - крючок\n\n- [Вторая](f2.md) - тоже коротко\n' > "$tmp/mem_ok.md"
printf -- '- [Журнал](file.md) - %s\nпроза без указателя\n' "$long" > "$tmp/mem_bad.md"
python3 "$here/check-memory.py" "$tmp/mem_ok.md" >/dev/null || fail "чистый индекс памяти не прошёл"
out=$(python3 "$here/check-memory.py" "$tmp/mem_bad.md")
[ $? -eq 1 ] || fail "жирный индекс памяти не пойман"
echo "$out" | grep -q 'длина' || fail "нет находки про длину строки индекса"
echo "$out" | grep -q 'не строка-указатель' || fail "нет находки про прозу в индексе"
printf '{"tool_input":{"file_path":"/a/memory/MEMORY.md","new_string":"- [Журнал](f.md) - %s"}}' "$long" |
    python3 "$here/check-memory.py" --hook 2>/dev/null
[ $? -eq 2 ] || fail "хук памяти пропустил жирную строку"
printf '{"tool_input":{"file_path":"/a/b/notes.md","new_string":"%s"}}' "$long" |
    python3 "$here/check-memory.py" --hook 2>/dev/null || fail "хук памяти лезет в чужие файлы"

# check-sensitive.py: IP и токены в файлах доски ловятся, роли машин и
# loopback проходят, чужие пути не смотрятся.
printf 'хост роутер DE, локально 127.0.0.1, маска 255.255.255.0\n' > "$tmp/sens_ok.md"
printf 'сервер живёт на 10.1.2.3, пароль: hunter2secret\n' > "$tmp/sens_bad.md"
python3 "$here/check-sensitive.py" "$tmp/sens_ok.md" >/dev/null || fail "чистый текст не прошёл check-sensitive"
out=$(python3 "$here/check-sensitive.py" "$tmp/sens_bad.md")
[ $? -eq 1 ] || fail "IP и пароль в файле не пойманы"
echo "$out" | grep -q 'IP-адрес' || fail "нет находки про IP"
echo "$out" | grep -q 'секрет' || fail "нет находки про секрет"
printf 'docs/TASKS.md:3:| XR-1 | сервер 10.1.2.3 | task |\n' |
    python3 "$here/check-sensitive.py" --diff >/dev/null
[ $? -eq 1 ] || fail "IP в диффе доски не пойман"
printf 'src/config.go:1:addr := "10.1.2.3"\n' |
    python3 "$here/check-sensitive.py" --diff >/dev/null || fail "режим --diff лезет в файлы кода"
token="ghp_$(printf 'a%.0s' $(seq 1 36))"
printf '{"tool_input":{"file_path":"/a/docs/tasks/XR-1.md","new_string":"токен %s"}}' "$token" |
    python3 "$here/check-sensitive.py" --hook 2>/dev/null
[ $? -eq 2 ] || fail "хук пропустил токен в файле задачи"
printf '{"tool_input":{"file_path":"/a/src/main.go","new_string":"10.1.2.3"}}' |
    python3 "$here/check-sensitive.py" --hook 2>/dev/null || fail "хук чувствительного лезет вне доски"
printf '{"tool_input":{"file_path":"/a/docs/tasks/XR-1.md","new_string":"проверить на роутере DE"}}' |
    python3 "$here/check-sensitive.py" --hook 2>/dev/null || fail "хук ругается на роль машины"

# pre-commit: IP в staged-строках доски ловится вторым рубежом.
repo2="$tmp/repo2"
git init -q "$repo2"
git -C "$repo2" config user.name t
git -C "$repo2" config user.email t@t
mkdir -p "$repo2/docs"
printf '| XR-1 | сервер 10.1.2.3 | task |\n' > "$repo2/docs/TASKS.md"
git -C "$repo2" add docs/TASKS.md
(cd "$repo2" && "$here/pre-commit" >/dev/null 2>&1)
[ $? -eq 1 ] || fail "pre-commit пропустил IP в доске"
printf '| XR-1 | сервер уехал на роль VPS RU | task |\n' > "$repo2/docs/TASKS.md"
git -C "$repo2" add docs/TASKS.md
(cd "$repo2" && "$here/pre-commit" >/dev/null 2>&1) || fail "pre-commit ругается на чистую доску"

if [ $fails -eq 0 ]; then
    echo "хуки в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
