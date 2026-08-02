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

# check-symbols.py: снимки в testdata пропускаются и по путям, и в режиме
# --hook, а похожее имя директории пропуска не даёт.
mkdir -p "$tmp/testdata" "$tmp/mytestdata"
printf 'текст с тире %s в снимке\n' "$dash" > "$tmp/testdata/file.txt"
cp "$tmp/testdata/file.txt" "$tmp/mytestdata/file.txt"
python3 "$here/check-symbols.py" "$tmp/testdata/file.txt" >/dev/null || fail "снимок в testdata не пропущен"
python3 "$here/check-symbols.py" "$tmp/mytestdata/file.txt" >/dev/null
[ $? -eq 1 ] || fail "тире в mytestdata сошло за testdata"
printf '{"tool_input":{"file_path":"mylib/testdata/snapshot.txt","new_string":"тире %s в хуке"}}' "$dash" |
    python3 "$here/check-symbols.py" --hook 2>/dev/null || fail "режим --hook не пропустил testdata"
printf '{"tool_input":{"file_path":"mylib/mytestdata/snapshot.txt","new_string":"тире %s в хуке"}}' "$dash" |
    python3 "$here/check-symbols.py" --hook 2>/dev/null
[ $? -eq 2 ] || fail "режим --hook принял mytestdata за testdata"

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

# quota-refresh.sh: отцепленный съём снимка квоты на старте сессии. Настоящие
# tmux и claude тут не поднимаются, вместо них заглушки: проверяется обвязка
# хука (условия запуска, замок, журнал), а не съём панели, у него свои тесты.
qbin="$tmp/qbin"
mkdir -p "$qbin"
for t in tmux claude; do
    printf '#!/bin/sh\nexit 0\n' > "$qbin/$t"
    chmod +x "$qbin/$t"
done
mark="$tmp/refresh.mark"
cat > "$qbin/agentctl" <<EOF
#!/bin/sh
echo "\$*" >> "$mark"
EOF
chmod +x "$qbin/agentctl"
# Системная часть PATH подставная: проверка «инструментов нет» иначе держалась
# бы на том, чего нет в /usr/bin именно на этой машине.
qsys="$tmp/qsys"
mkdir -p "$qsys"
for t in sh dirname mkdir rmdir date find sleep; do
    p=$(command -v "$t") && ln -sf "$p" "$qsys/$t"
done
awaited() { # дождаться файла, который пишет фоновый процесс
    i=0
    while [ ! -s "$1" ] && [ "$i" -lt 100 ]; do sleep 0.1; i=$((i + 1)); done
}

qhome="$tmp/qhome"
mkdir -p "$qhome"
HOME="$qhome" PATH="$qbin:$qsys" sh "$here/quota-refresh.sh" || fail "хук вернул не 0"
awaited "$mark"
grep -q -- '--if-stale' "$mark" || fail "хук снимает панель мимо режима --if-stale: $(cat "$mark" 2>/dev/null)"
awaited "$qhome/.devkit/quota-refresh.log"
grep -q 'код возврата' "$qhome/.devkit/quota-refresh.log" || fail "хук не оставил журнала последнего запуска"

# Взятый замок останавливает второй запуск: иначе claude, поднятый в tmux, своим
# стартом дёрнул бы этот же хук и увёл бы сессии в воронку.
: > "$mark"
mkdir -p "$qhome/.devkit/quota-refresh.lock"
HOME="$qhome" PATH="$qbin:$qsys" sh "$here/quota-refresh.sh" || fail "хук при взятом замке вернул не 0"
sleep 1
[ -s "$mark" ] && fail "хук полез снимать панель при взятом замке"
rmdir "$qhome/.devkit/quota-refresh.lock"

# Нечем снимать: уходим молча и следов не оставляем, ругаться на это дело
# devkitctl doctor, а не каждой сессии.
nohome="$tmp/qnohome"
mkdir -p "$nohome"
HOME="$nohome" PATH="$qsys" sh "$here/quota-refresh.sh" || fail "хук без инструментов вернул не 0"
[ -d "$nohome/.devkit" ] && fail "хук без инструментов насорил в HOME"

# notify.py: уведомитель сессии. Разбор события, выбор бэкенда и окно
# троттлинга держат юниты, тут прогон целиком: временный HOME, стаб вместо
# osascript, каждый повод, повтор в окне, посторонние события и пустая машина.
python3 "$here/notify_test.py" >/dev/null 2>&1 || fail "юниты уведомителя не прошли"

nhome="$tmp/nhome"
mkdir -p "$nhome"
nmark="$tmp/notify.mark"
nstub="$tmp/notify-stub"
cat > "$nstub" <<EOF
#!/bin/sh
printf '%s|%s\n' "\$1" "\$2" >> "$nmark"
EOF
chmod +x "$nstub"
# Своя системная часть PATH: проверка «слать нечем» иначе держалась бы на том,
# есть ли на этой машине osascript или notify-send.
nsys="$tmp/nsys"
mkdir -p "$nsys"
for t in sh python3; do
    p=$(command -v "$t") && ln -sf "$p" "$nsys/$t"
done
# Стаб отправителя с кликом зовётся именно terminal-notifier: по имени бэкенда
# уведомитель и решает, брать ли цель перехода.
ntn="$tmp/tn"
mkdir -p "$ntn"
cat > "$ntn/terminal-notifier" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$nmark"
EOF
chmod +x "$ntn/terminal-notifier"
nlog="$nhome/.devkit/notify.log"
notify_hook() { # событие на stdin, стаб вместо бэкенда
    # Фокус окна тут выключен: живой System Events отвечал бы тем, что в этот
    # момент открыто на машине, и конец хода стал бы гадательным. Своя проверка
    # фокуса идёт ниже, со стабом опроса.
    HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$nstub" DEVKIT_NOTIFY_FOCUS=off \
        python3 "$here/notify.py" --hook claude-code
}
notify_click() { # то же, но отправителем стаб terminal-notifier
    HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$ntn/terminal-notifier" \
        CLAUDE_CODE_ENTRYPOINT=claude-vscode python3 "$here/notify.py" --hook claude-code
}
event() { # тип события, повод, сессия
    printf '{"hook_event_name":"%s","notification_type":"%s","session_id":"%s",' "$1" "$2" "$3"
    printf '"cwd":"/p/devkit-dk-034","message":"текст повода","agent_type":"exec-low",'
    printf '"last_assistant_message":"первая строка\\nвторая"}'
}

# Каждый согласованный повод доходит до бэкенда, и в заголовке видно, какая сессия.
for reason in permission_prompt agent_needs_input elicitation_dialog idle_prompt; do
    : > "$nmark"
    event Notification "$reason" "sess-$reason" | notify_hook || fail "хук уведомителя вернул не 0 на $reason"
    grep -q '^devkit-dk-034: ' "$nmark" || fail "повод $reason не дошёл до бэкенда: $(cat "$nmark")"
done
: > "$nmark"
event SubagentStop "" sess-sub | notify_hook || fail "хук уведомителя вернул не 0 на субагенте"
grep -q 'субагент отработал|exec-low: первая строка' "$nmark" || fail "субагент не дошёл до бэкенда: $(cat "$nmark")"

# Уровень доезжает до баннера: громкий со звуком, фоновый молча и с группой по
# сессии, чтобы новый баннер вытеснял её же предыдущий.
: > "$nmark"
event Notification permission_prompt sess-loud | notify_click || fail "громкий повод вернул не 0"
grep -q -- '-sound default' "$nmark" || fail "громкий повод ушёл без звука: $(cat "$nmark")"
grep -q -- '-group' "$nmark" && fail "громкий повод схлопнулся в группу: $(cat "$nmark")"
: > "$nmark"
event SubagentStop "" sess-bg | notify_click || fail "фоновый повод вернул не 0"
grep -q -- '-group devkit-sess-bg' "$nmark" || fail "фоновый повод ушёл без группы: $(cat "$nmark")"
grep -q -- '-sound' "$nmark" && fail "фоновый повод ушёл со звуком: $(cat "$nmark")"
grep -q 'повод subagent_stop уровень фоновый' "$nlog" ||
    fail "уровень не попал в журнал: $(cat "$nlog")"

# Конец хода уходит громким, а ввод пользователя не шлёт ничего.
: > "$nmark"
event Stop "" sess-turn | notify_hook || fail "хук вернул не 0 на конце хода"
grep -q '^devkit-dk-034: ход закончен|$' "$nmark" || fail "конец хода не дошёл до бэкенда: $(cat "$nmark")"
grep -q 'повод turn_done уровень громкий' "$nlog" || fail "конец хода в журнале без уровня: $(cat "$nlog")"
: > "$nmark"
event UserPromptSubmit "" sess-in | notify_hook || fail "хук вернул не 0 на вводе пользователя"
[ -s "$nmark" ] && fail "ввод пользователя послал уведомление: $(cat "$nmark")"

# Звать о конце хода или молчать, решает фокус окна. Живой System Events тут не
# спрашивается: стаб osascript в PATH отвечает тем заголовком, который лежит в
# файле, и он же показывает, спрашивали ли фокус вообще.
nfoc="$tmp/nfoc"
mkdir -p "$nfoc"
ftitle="$tmp/focus.title"
fmark="$tmp/focus.mark"
cat > "$nfoc/osascript" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$fmark"
[ -s "$ftitle" ] || exit 1
read -r title < "$ftitle" && printf '%s\n' "\$title"
EOF
chmod +x "$nfoc/osascript"
notify_focus() { # то же, что notify_hook, но фокус спрашивается через стаб
    HOME="$nhome" PATH="$nfoc:$nsys" DEVKIT_NOTIFY_BACKEND="$nstub" \
        python3 "$here/notify.py" --hook claude-code
}

# Смотришь в окно этой сессии, значит звать некуда.
: > "$nmark"
: > "$fmark"
printf 'Правка notify.py - devkit-dk-034\n' > "$ftitle"
event Stop "" sess-focus | notify_focus || fail "хук вернул не 0 на конце хода в фокусе"
[ -s "$nmark" ] && fail "конец хода при взгляде в окно сессии послал уведомление: $(cat "$nmark")"
[ -s "$fmark" ] || fail "фокус на конце хода не спрашивался вовсе"
grep -q 'повод turn_done уровень громкий бэкенд - цель - пропуск: окно сессии в фокусе' "$nlog" ||
    fail "пропуск по фокусу не попал в журнал: $(cat "$nlog")"

# Впереди чужое окно, значит зовём. Дерево проекта и дерево задачи при этом
# разные окна: сессия сидит в devkit-dk-034, а заголовок кончается на devkit.
: > "$nmark"
printf 'Разбор задачи - devkit\n' > "$ftitle"
event Stop "" sess-away | notify_focus || fail "хук вернул не 0 на конце хода из чужого окна"
grep -q '^devkit-dk-034: ход закончен|$' "$nmark" ||
    fail "конец хода из чужого окна промолчал: $(cat "$nmark")"

# Опрос не ответил (нет разрешения на управление компьютером, не macOS): зовём,
# и отказ виден в журнале, иначе «звонит всегда» разбирать нечем.
: > "$nmark"
: > "$ftitle"
event Stop "" sess-blind | notify_focus || fail "хук вернул не 0 на молчащем опросе"
grep -q '^devkit-dk-034: ход закончен|$' "$nmark" ||
    fail "конец хода с неотвеченным опросом промолчал: $(cat "$nmark")"
grep -q 'фокус не определился, зовём' "$nlog" || fail "отказ опроса не попал в журнал: $(cat "$nlog")"

# Выключатель гасит проверку целиком: зовём, не спрашивая никого.
: > "$nmark"
: > "$fmark"
printf 'Правка notify.py - devkit-dk-034\n' > "$ftitle"
event Stop "" sess-nofocus | HOME="$nhome" PATH="$nfoc:$nsys" \
    DEVKIT_NOTIFY_BACKEND="$nstub" DEVKIT_NOTIFY_FOCUS=off \
    python3 "$here/notify.py" --hook claude-code || fail "хук с выключенным фокусом вернул не 0"
grep -q '^devkit-dk-034: ход закончен|$' "$nmark" ||
    fail "выключенная проверка не пропустила конец хода: $(cat "$nmark")"
[ -s "$fmark" ] && fail "выключенная проверка всё равно спросила фокус: $(cat "$fmark")"

# Мимо конца хода фокус не спрашивается: лишний опрос на каждого субагента это
# его 180 мс на пустом месте, а запрос разрешения зовёт в любом случае.
: > "$nmark"
: > "$fmark"
event Notification permission_prompt sess-fp | notify_focus || fail "хук вернул не 0 на запросе разрешения"
event SubagentStop "" sess-fs | notify_focus || fail "хук вернул не 0 на субагенте с фокусом"
[ "$(wc -l < "$nmark")" -eq 2 ] || fail "поводы мимо конца хода не дошли до бэкенда: $(cat "$nmark")"
[ -s "$fmark" ] && fail "фокус спрашивался мимо конца хода: $(cat "$fmark")"

# Конец хода и idle_prompt это один повод, и второй не повторяет баннер первого.
: > "$nmark"
event Stop "" sess-wait | notify_hook || fail "хук вернул не 0 на конце хода сессии ожидания"
event Notification idle_prompt sess-wait | notify_hook || fail "хук вернул не 0 на idle_prompt следом"
[ "$(wc -l < "$nmark")" -eq 1 ] || fail "idle_prompt повторил баннер конца хода: $(cat "$nmark")"

# Ввод пользователя снимает отметку ожидания: второй ход подряд снова звучит,
# хотя окно повода «сессия ждёт тебя» ещё не вышло.
: > "$nmark"
event Stop "" sess-again | notify_hook || fail "хук вернул не 0 на первом конце хода"
event UserPromptSubmit "" sess-again | notify_hook || fail "хук вернул не 0 на вводе пользователя"
event Stop "" sess-again | notify_hook || fail "хук вернул не 0 на втором конце хода"
[ "$(wc -l < "$nmark")" -eq 2 ] ||
    fail "второй ход после ввода пользователя промолчал: $(cat "$nmark")"

# Повтор того же повода той же сессии в окне молчит, а соседняя сессия нет.
: > "$nmark"
event Notification idle_prompt sess-window | notify_hook
event Notification idle_prompt sess-window | notify_hook
[ "$(wc -l < "$nmark")" -eq 1 ] || fail "повтор повода в окне ушёл вторым баннером: $(cat "$nmark")"
grep -q 'пропуск: повтор в окне' "$nlog" || fail "пропуск по окну не попал в журнал"
event Notification idle_prompt sess-other | notify_hook
[ "$(wc -l < "$nmark")" -eq 2 ] || fail "соседняя сессия заглушена чужим окном"

# Посторонние события и молчаливые поводы не шлют ничего.
: > "$nmark"
event Notification auth_success sess-quiet | notify_hook || fail "хук вернул не 0 на молчаливом поводе"
event PreToolUse "" sess-quiet | notify_hook || fail "хук вернул не 0 на постороннем событии"
printf 'не json' | notify_hook || fail "хук вернул не 0 на мусоре вместо события"
[ -s "$nmark" ] && fail "уведомление ушло на том, на чём слать не должны: $(cat "$nmark")"

# Кривой вход любого вида это код 0 и строка в журнале, а не traceback: хук
# стоит в каждой сессии, и форма события целиком на стороне харнеса.
for bad in '42' 'null' '[1,2]' '"строка"'; do
    err=$(printf '%s' "$bad" | notify_hook 2>&1) ||
        fail "хук вернул не 0 на событии $bad"
    [ -n "$err" ] && fail "хук ругался в stderr на событии $bad: $err"
done
grep -q 'событие не разобрано: json не объектом' "$nlog" ||
    fail "непонятое событие не попало в журнал: $(cat "$nlog")"
err=$(printf '{"hook_event_name":"Notification","notification_type":{"a":1},"session_id":"sess-bad"}' |
    notify_hook 2>&1) || fail "хук вернул не 0 на поле не той формы"
[ -n "$err" ] && fail "хук ругался в stderr на поле не той формы: $err"
grep -q 'сессия sess-bad повод - уровень - бэкенд - цель - событие не разобрано: поля не той формы' "$nlog" ||
    fail "поле не той формы не попало в журнал: $(cat "$nlog")"
[ -s "$nmark" ] && fail "уведомление ушло на кривом событии: $(cat "$nmark")"
# Повод при этом не съедается: тело собирается из того, что дали.
printf '{"hook_event_name":"Notification","notification_type":"permission_prompt","session_id":"sess-num","cwd":"/p/devkit-dk-034","message":42}' |
    notify_hook || fail "хук вернул не 0 на числовом теле"
grep -q '^devkit-dk-034: нужно разрешение|42$' "$nmark" ||
    fail "числовое тело съело повод: $(cat "$nmark")"

# Клик по баннеру ведёт в рабочее дерево позвавшей сессии, а не в общее место,
# и открывает его отдельным окном: без windowId=_blank редактор подменил бы
# деревом задачи то окно, в котором сейчас работают.
: > "$nmark"
event Notification idle_prompt sess-click | notify_click || fail "хук с кликом вернул не 0"
grep -q '\-open vscode://file/p/devkit-dk-034?windowId=_blank$' "$nmark" ||
    fail "цель перехода не уехала отправителю: $(cat "$nmark")"
grep -q 'цель vscode://file/p/devkit-dk-034?windowId=_blank код возврата: 0' "$nlog" ||
    fail "цель перехода не попала в журнал: $(cat "$nlog")"

# Субагент работает в дереве задачи, а окно сессии стоит на своём: клик ведёт
# в окно, а не в дерево задачи, и заголовок показывает оба.
: > "$nmark"
ntr="$tmp/transcript.jsonl"
printf '{"type":"queue-operation","sessionId":"s1"}\n{"type":"user","cwd":"/p/devkit"}\n' > "$ntr"
printf '{"hook_event_name":"SubagentStop","session_id":"sess-wt","cwd":"/p/devkit-dk-059",' > "$tmp/wt.json"
printf '"transcript_path":"%s","agent_type":"exec-low","last_assistant_message":"готово"}' "$ntr" >> "$tmp/wt.json"
notify_click < "$tmp/wt.json" || fail "хук с деревом задачи вернул не 0"
grep -q '^-title devkit (dk-059): субагент отработал' "$nmark" ||
    fail "заголовок не показал окно и задачу разом: $(cat "$nmark")"
grep -q -- '-open vscode://file/p/devkit?windowId=_blank$' "$nmark" ||
    fail "клик ведёт не в окно сессии: $(cat "$nmark")"

# Отправитель без клика цель не получает, и журнал говорит об этом прямо.
: > "$nmark"
event Notification idle_prompt sess-noclick | notify_hook || fail "хук без клика вернул не 0"
grep -q 'open' "$nmark" && fail "цель ушла отправителю, который клик не умеет: $(cat "$nmark")"
grep -q 'сессия sess-noc повод idle_prompt уровень громкий бэкенд .* цель - код возврата: 0' "$nlog" ||
    fail "в журнале нет отправки без цели: $(cat "$nlog")"

# cwd не строкой цель не роняет: уведомление уходит, клик поднимает редактор.
: > "$nmark"
printf '{"hook_event_name":"Notification","notification_type":"idle_prompt","session_id":"sess-cwd","cwd":7,"message":"проба"}' |
    notify_click || fail "хук вернул не 0 на cwd числом"
grep -q -- '-activate com.microsoft.VSCode$' "$nmark" ||
    fail "на cwd числом цель не собралась запасным путём: $(cat "$nmark")"

# Своя цель перебивает нашу, пустая гасит клик совсем.
: > "$nmark"
event Notification idle_prompt sess-own |
    HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$ntn/terminal-notifier" \
    DEVKIT_NOTIFY_OPEN='x-devkit://{cwd}' python3 "$here/notify.py" --hook claude-code ||
    fail "хук со своей целью вернул не 0"
grep -q -- '-open x-devkit:///p/devkit-dk-034$' "$nmark" ||
    fail "своя цель не уехала отправителю: $(cat "$nmark")"
: > "$nmark"
event Notification idle_prompt sess-nogo |
    HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$ntn/terminal-notifier" \
    CLAUDE_CODE_ENTRYPOINT=claude-vscode DEVKIT_NOTIFY_OPEN= \
    python3 "$here/notify.py" --hook claude-code || fail "хук с погашенным кликом вернул не 0"
[ -s "$nmark" ] || fail "уведомление с погашенным кликом не ушло вовсе"
grep -q 'open' "$nmark" && fail "пустая DEVKIT_NOTIFY_OPEN клик не погасила: $(cat "$nmark")"

# Слать нечем: код 0, отказ в журнале и запасной путь через сам терминал.
: > "$nmark"
out=$(HOME="$nhome" PATH="$nsys" python3 "$here/notify.py" --hook claude-code <<EOF
{"hook_event_name":"Notification","notification_type":"idle_prompt","session_id":"sess-none","cwd":"/p/devkit-dk-034","message":"текст"}
EOF
) || fail "хук без бэкенда вернул не 0"
grep -q 'бэкенда нет' "$nlog" || fail "отказ бэкенда не попал в журнал"
echo "$out" | grep -q 'terminalSequence' || fail "без бэкенда нет запасного пути через терминал: $out"

# Журнал пишет сессию, повод, уровень и код возврата: жалоба «не приходят»
# разбирается по нему, а «важное не отличается от фонового» по уровню.
grep -q 'сессия sess-win повод idle_prompt уровень громкий бэкенд .*код возврата: 0' "$nlog" ||
    fail "в журнале нет строки отправки: $(cat "$nlog")"

# Выключатель гасит уведомитель целиком, в том числе аргументный режим.
: > "$nmark"
event Notification idle_prompt sess-off |
    HOME="$nhome" DEVKIT_NOTIFY_OFF=1 DEVKIT_NOTIFY_BACKEND="$nstub" python3 "$here/notify.py" --hook claude-code ||
    fail "выключенный хук вернул не 0"
HOME="$nhome" DEVKIT_NOTIFY_OFF=1 DEVKIT_NOTIFY_BACKEND="$nstub" python3 "$here/notify.py" "заголовок" "тело" ||
    fail "выключенный уведомитель вернул не 0"
[ -s "$nmark" ] && fail "выключатель не сработал: $(cat "$nmark")"

# Аргументный режим: зовётся не только хуком, поэтому заголовок и тело идут прямо.
: > "$nmark"
HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$nstub" python3 "$here/notify.py" "выкат" "прод обновлён" ||
    fail "аргументный режим вернул не 0"
grep -q '^выкат|прод обновлён$' "$nmark" || fail "аргументный режим не дошёл до бэкенда: $(cat "$nmark")"
grep -q 'уровень громкий' "$nlog" || fail "внешний вызов ушёл не громким: $(cat "$nlog")"

# Внешний вызов громкий по умолчанию, --quiet понижает его до фонового.
: > "$nmark"
HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$ntn/terminal-notifier" \
    python3 "$here/notify.py" "выкат" "прод обновлён" || fail "громкий вызов вернул не 0"
grep -q -- '-sound default' "$nmark" || fail "внешний вызов ушёл без звука: $(cat "$nmark")"
: > "$nmark"
HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$ntn/terminal-notifier" \
    python3 "$here/notify.py" --quiet "поезд собран" "три задачи" || fail "тихий вызов вернул не 0"
grep -q '^-title поезд собран -message три задачи -group devkit' "$nmark" ||
    fail "--quiet не понизил повод: $(cat "$nmark")"
HOME="$nhome" python3 "$here/notify.py" --quiet >/dev/null 2>&1
[ $? -eq 2 ] || fail "--quiet без заголовка не показал справку"

# Самопроверка говорит, чем именно послано, и краснеет, когда слать нечем.
out=$(HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$nstub" python3 "$here/notify.py" --self-test) ||
    fail "самопроверка со стабом вернула не 0"
echo "$out" | grep -q "послано через $nstub" || fail "самопроверка молчит про бэкенд: $out"
out=$(HOME="$nhome" PATH="$nsys" python3 "$here/notify.py" --self-test)
[ $? -eq 1 ] || fail "самопроверка без бэкенда вернула 0"
echo "$out" | grep -q 'бэкенда уведомлений нет' || fail "самопроверка молчит про отсутствие бэкенда: $out"

if [ $fails -eq 0 ]; then
    echo "хуки в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
