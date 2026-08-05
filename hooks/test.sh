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
# История длиннее порога check-commit (HISTORY_MIN): перечень префиксов
# становится нормой проекта только на ней, короткую проверка не судит.
hist='feat(core): раз\nfix: два\ndocs: три\nfix: четыре\nfeat: пять\ndocs: шесть\nfix: семь\nfeat: восемь\ndocs: девять\nfix: десять'
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

# check-commit.py: след не одного ассистента. Подписи стоят прямо в subject,
# без родовых «co-authored-by» и «generated with»: те ловятся и без перечня, а
# перечень тут и проверяется.
for trace in noreply@anthropic.com codex@openai.com noreply@openai.com \
    cursoragent@cursor.com aider@aider.chat openhands@all-hands.dev \
    devin-ai-integration copilot-swe-agent google-labs-jules; do
    printf 'fix: правка от %s\n' "$trace" > "$tmp/msg"
    out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
    [ $? -eq 1 ] || fail "след ассистента $trace не пойман"
    echo "$out" | grep -q 'след ассистента' || fail "нет находки про след $trace: $out"
done
printf 'fix: правка от noreply@example.com про генерацию отчёта\n' > "$tmp/msg"
printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null ||
    fail "check-commit принял чужой адрес за след ассистента"

# check-commit.py: тип не из истории ловится, revert и свежий репозиторий проходят.
printf 'perf: чужой тип\n' > "$tmp/msg"
out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
[ $? -eq 1 ] || fail "чужой тип префикса не пойман"
echo "$out" | grep -q 'не встречается в истории' || fail "нет находки про тип"
printf 'revert: XR-1 откат правки\n' > "$tmp/msg"
printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null || fail "revert должен проходить всегда"
printf 'perf: первый типизированный\n' > "$tmp/msg"
printf '\n' | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null || fail "пустая история не должна включать проверку префикса"
# Свежий проект (DK-125): в истории один-два коммита подключения, и нормой
# проекта их префиксы ещё не стали. Иначе первый же «docs(tasks)» доски вставал
# бы находкой на проекте, который только что завела devkitctl new.
printf 'docs(tasks): MP-001 в работу\n' > "$tmp/msg"
printf 'chore: подключение devkit\n' | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null ||
    fail "история из одного коммита включила проверку префикса"
printf 'perf: чужой тип\n' > "$tmp/msg"
printf 'feat: раз\nfix: два\ndocs: три\n' | python3 "$here/check-commit.py" "$tmp/msg" >/dev/null ||
    fail "короткая история включила проверку префикса"

# commit-msg целиком: типизированная история включает проверку префикса.
repoc="$tmp/repoc"
git init -q "$repoc"
git -C "$repoc" config user.name t
git -C "$repoc" config user.email t@t
# Историю сеют глубже порога HISTORY_MIN: на паре коммитов проверка префикса
# молчит намеренно, и «чужой тип» тут не поймался бы.
i=1
while [ $i -le 12 ]; do
    printf 'x%s\n' "$i" > "$repoc/f.txt"
    git -C "$repoc" add f.txt
    git -C "$repoc" commit -qm "feat: сид истории $i"
    i=$((i + 1))
done
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

# Голый --hook это claude-code: команды в settings.json на машинах прописаны без
# имени протокола, и обновление devkit их ломать не должно.
printf '{"tool_input":{"file_path":"x.md","new_string":"плохо %s"}}' "$dash" |
    python3 "$here/check-symbols.py" --hook claude-code 2>/dev/null
[ $? -eq 2 ] || fail "--hook claude-code пропустил тире"
printf '{"tool_input":{"file_path":"x.md","new_string":"чисто"}}' |
    python3 "$here/check-symbols.py" --hook claude-code 2>/dev/null ||
    fail "--hook claude-code ругается на чистое"

# Незнакомый протокол это отказ с внятной строкой, а не молчаливый пропуск:
# иначе опечатка в settings.json выключила бы проверку насовсем.
for tool in check-symbols check-memory check-sensitive; do
    err=$(printf '{"tool_input":{"file_path":"x.md","new_string":"текст"}}' |
        python3 "$here/$tool.py" --hook кодекс 2>&1 >/dev/null)
    [ $? -eq 2 ] || fail "$tool промолчал про незнакомый протокол"
    echo "$err" | grep -q 'не заведён' || fail "$tool не назвал причину отказа: $err"
done

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

# Где лежит индекс, знает профиль харнеса, а не сам хук: свой хвост пути ловится
# по профилю, а без ключа memory_index проверка молчит вовсе (у инструмента без
# памяти находки про индекс это шум).
hown="$tmp/harness-own"
hnone="$tmp/harness-none"
mkdir -p "$hown" "$hnone"
printf '[hooks]\nprotocol = "claude-code"\nmemory_index = "/заметки/ИНДЕКС.md"\n' > "$hown/claude-code.toml"
printf '[hooks]\nprotocol = "claude-code"\n' > "$hnone/claude-code.toml"
printf '{"tool_input":{"file_path":"/a/заметки/ИНДЕКС.md","new_string":"проза без указателя"}}' |
    DEVKIT_HARNESS_DIR="$hown" python3 "$here/check-memory.py" --hook 2>/dev/null
[ $? -eq 2 ] || fail "хук памяти не взял хвост пути из профиля"
printf '{"tool_input":{"file_path":"/a/memory/MEMORY.md","new_string":"проза без указателя"}}' |
    DEVKIT_HARNESS_DIR="$hown" python3 "$here/check-memory.py" --hook 2>/dev/null ||
    fail "хук памяти смотрит мимо профиля, по зашитому пути"
printf '{"tool_input":{"file_path":"/a/memory/MEMORY.md","new_string":"проза без указателя"}}' |
    DEVKIT_HARNESS_DIR="$hnone" python3 "$here/check-memory.py" --hook 2>/dev/null ||
    fail "хук памяти сработал у инструмента без индекса памяти"

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

# check-exec-bit.py: test.sh без бита x в индексе ловится (в корне и во
# вложенной директории), исполняемый и посторонний .sh рядом молчат.
repo3="$tmp/repo3"
git init -q "$repo3"
git -C "$repo3" config user.name t
git -C "$repo3" config user.email t@t
mkdir -p "$repo3/pkg"
printf '#!/bin/sh\necho ok\n' > "$repo3/pkg/test.sh"
git -C "$repo3" add pkg/test.sh
git -C "$repo3" commit -qm seed >/dev/null
out=$(python3 "$here/check-exec-bit.py" -C "$repo3")
[ $? -eq 1 ] || fail "test.sh без бита x не поймано"
case $out in "pkg/test.sh: режим 100644"*) ;; *) fail "находка без пути и режима: $out" ;; esac

chmod +x "$repo3/pkg/test.sh"
git -C "$repo3" add pkg/test.sh
git -C "$repo3" commit -qm chmod >/dev/null
python3 "$here/check-exec-bit.py" -C "$repo3" >/dev/null || fail "исполняемый test.sh ложно поймался"

printf 'echo other\n' > "$repo3/pkg/other.sh"
git -C "$repo3" add pkg/other.sh
git -C "$repo3" commit -qm other >/dev/null
python3 "$here/check-exec-bit.py" -C "$repo3" >/dev/null || fail "посторонний .sh без бита x ложно поймался"

printf '#!/bin/sh\necho ok\n' > "$repo3/test.sh"
git -C "$repo3" add test.sh
git -C "$repo3" commit -qm 'root test.sh' >/dev/null
python3 "$here/check-exec-bit.py" -C "$repo3" >/dev/null
[ $? -eq 1 ] || fail "test.sh в корне без бита x не поймано"

# check-exec-bit.py: не git-репозиторий и несуществующий -C дают чистую
# диагностику одной строкой, а не traceback (DK-072, замечание ревью).
notrepo="$tmp/notrepo"
mkdir -p "$notrepo"
out=$(python3 "$here/check-exec-bit.py" -C "$notrepo" 2>&1)
[ $? -eq 2 ] || fail "не git-репозиторий должен вернуть код 2"
case $out in
    *raceback*) fail "не git-репозиторий уронил traceback: $out" ;;
    "check-exec-bit: "*) ;;
    *) fail "не git-репозиторий без внятной диагностики: $out" ;;
esac
out=$(python3 "$here/check-exec-bit.py" -C "$notrepo/nope" 2>&1)
[ $? -eq 2 ] || fail "несуществующий -C DIR должен вернуть код 2"
case $out in
    *raceback*) fail "несуществующий -C DIR уронил traceback: $out" ;;
    "check-exec-bit: "*) ;;
    *) fail "несуществующий -C DIR без внятной диагностики: $out" ;;
esac

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

# hookio.py: таблица разборщиков. Что снимается с живых образцов и откуда
# берётся путь индекса памяти, держат юниты; прогон образцов через сами
# проверки идёт ниже, когда готовы стабы уведомителя.
python3 "$here/hookio_test.py" >/dev/null 2>&1 || fail "юниты разбора входа не прошли"

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
grep -q '^devkit-dk-034: ход закончен|первая строка$' "$nmark" || fail "конец хода не дошёл до бэкенда: $(cat "$nmark")"
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
grep -q '^devkit-dk-034: ход закончен|первая строка$' "$nmark" ||
    fail "конец хода из чужого окна промолчал: $(cat "$nmark")"

# Опрос не ответил (нет разрешения на управление компьютером, не macOS): зовём,
# и отказ виден в журнале, иначе «звонит всегда» разбирать нечем.
: > "$nmark"
: > "$ftitle"
event Stop "" sess-blind | notify_focus || fail "хук вернул не 0 на молчащем опросе"
grep -q '^devkit-dk-034: ход закончен|первая строка$' "$nmark" ||
    fail "конец хода с неотвеченным опросом промолчал: $(cat "$nmark")"
grep -q 'фокус не определился, зовём' "$nlog" || fail "отказ опроса не попал в журнал: $(cat "$nlog")"

# Выключатель гасит проверку целиком: зовём, не спрашивая никого.
: > "$nmark"
: > "$fmark"
printf 'Правка notify.py - devkit-dk-034\n' > "$ftitle"
event Stop "" sess-nofocus | HOME="$nhome" PATH="$nfoc:$nsys" \
    DEVKIT_NOTIFY_BACKEND="$nstub" DEVKIT_NOTIFY_FOCUS=off \
    python3 "$here/notify.py" --hook claude-code || fail "хук с выключенным фокусом вернул не 0"
grep -q '^devkit-dk-034: ход закончен|первая строка$' "$nmark" ||
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

# Аргументный вызов из временной директории молчит: это песочница вроде
# синтетической доски из обкатки сценария, живой баннер про неё ложный
# (DK-069). Пропуск при этом виден и в stdout, и в журнале.
: > "$nmark"
out=$(cd "$tmp" && HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$nstub" \
    python3 "$here/notify.py" "tmp-доска" "XR-001 в Check") ||
    fail "вызов из песочницы вернул не 0"
[ -s "$nmark" ] && fail "баннер ушёл из песочницы: $(cat "$nmark")"
echo "$out" | grep -q 'уведомление пропущено' || fail "пропуск песочницы молчит в stdout: $out"
grep -q 'пропуск: песочница' "$nlog" || fail "пропуск песочницы не попал в журнал: $(cat "$nlog")"

# Самопроверка говорит, чем именно послано, и краснеет, когда слать нечем.
out=$(HOME="$nhome" DEVKIT_NOTIFY_BACKEND="$nstub" python3 "$here/notify.py" --self-test) ||
    fail "самопроверка со стабом вернула не 0"
echo "$out" | grep -q "послано через $nstub" || fail "самопроверка молчит про бэкенд: $out"
out=$(HOME="$nhome" PATH="$nsys" python3 "$here/notify.py" --self-test)
[ $? -eq 1 ] || fail "самопроверка без бэкенда вернула 0"
echo "$out" | grep -q 'бэкенда уведомлений нет' || fail "самопроверка молчит про отсутствие бэкенда: $out"

# Живой запрос разрешения доходит до бэкенда, а живая запись файла уведомлением
# не считается: обе формы сняты с работающего Claude Code, а не сочинены.
: > "$nmark"
notify_hook < "$here/testdata/claude-code/notify-permission.json" ||
    fail "уведомитель вернул не 0 на живом запросе разрешения"
grep -q '^work: нужно разрешение|Claude needs your permission$' "$nmark" ||
    fail "живой запрос разрешения не дошёл до бэкенда: $(cat "$nmark")"
: > "$nmark"
notify_hook < "$here/testdata/claude-code/write-memory-index.json" ||
    fail "уведомитель вернул не 0 на записи файла"
[ -s "$nmark" ] && fail "запись файла ушла уведомлением: $(cat "$nmark")"

# Незнакомый протокол уведомитель называет, а не пропускает молча.
err=$(HOME="$nhome" python3 "$here/notify.py" --hook кодекс < "$here/testdata/claude-code/turn-done.json" 2>&1)
[ $? -eq 2 ] || fail "уведомитель промолчал про незнакомый протокол"
echo "$err" | grep -q 'не заведён' || fail "уведомитель не назвал причину отказа: $err"

# Образцы событий, снятые живьём: каждый прогоняется через каждую проверку и
# через уведомитель. Разбор у них общий, поэтому непонятая форма события должна
# всплыть тут целиком, а не в одной проверке из четырёх.
for sample in "$here"/testdata/claude-code/*.json; do
    name=$(basename "$sample")
    for tool in check-symbols check-memory check-sensitive; do
        err=$(python3 "$here/$tool.py" --hook claude-code < "$sample" 2>&1 >/dev/null)
        status=$?
        case $status in 0|2) ;; *) fail "$tool на образце $name вернул $status: $err" ;; esac
        echo "$err" | grep -q Traceback && fail "$tool упал на образце $name: $err"
    done
    err=$(notify_hook < "$sample" 2>&1 >/dev/null)
    [ $? -eq 0 ] || fail "уведомитель на образце $name вернул не 0: $err"
    [ -n "$err" ] && fail "уведомитель ругался на образце $name: $err"
done

# Разбор в тексте находки: находка не только запрещает символ, но и говорит,
# чем его переписать, и этот разбор виден и в CLI, и в режиме --hook.
arrow=$(printf '\342\206\222')   # стрелка U+2192
printf 'тире %s и стрелка %s\n' "$dash" "$arrow" > "$tmp/advice.txt"
out=$(python3 "$here/check-symbols.py" "$tmp/advice.txt")
echo "$out" | grep -q 'как переписать' || fail "находка без разбора: $out"
echo "$out" | grep -q 'перестроить предложение' || fail "нет разбора про тире: $out"
echo "$out" | grep -q 'ASCII' || fail "нет разбора про стрелку: $out"
out=$(printf 'кавычки %s\n' "$(printf '\342\200\234')" | python3 "$here/check-symbols.py" --stdin)
echo "$out" | grep -q 'ёлочки' || fail "нет разбора про кавычки: $out"
out=$(printf '{"tool_input":{"file_path":"x.md","new_string":"тире %s"}}' "$dash" |
    python3 "$here/check-symbols.py" --hook 2>&1 >/dev/null)
echo "$out" | grep -q 'перестроить предложение' || fail "режим --hook отдал находку без разбора: $out"

# Символ, для которого отдельного разбора нет, разбор всё равно получает.
out=$(printf 'буква %s\n' "$(printf '\303\251')" | python3 "$here/check-symbols.py" --stdin)
echo "$out" | grep -q 'клавиатурный аналог' || fail "нет разбора для прочего символа: $out"

# check-commit: у каждой находки свой разбор, а не только запрет.
printf 'fix: правка\n\nCo-authored-by: X <noreply@anthropic.com>\n' > "$tmp/msg"
out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
echo "$out" | grep -q 'как переписать' || fail "check-commit без разбора: $out"
echo "$out" | grep -q 'amend' || fail "нет разбора про след ассистента: $out"
printf 'perf: чужой тип\n' > "$tmp/msg"
out=$(printf "$hist\n" | python3 "$here/check-commit.py" "$tmp/msg")
echo "$out" | grep -q 'git log -n 100' || fail "нет разбора про префикс из истории: $out"

# check-tests.py: код без теста это находка, код с тестом нет, дока сама по
# себе тоже нет. Тестом считается и фикстура в testdata.
out=$(python3 "$here/check-tests.py" pkg/ops.go)
[ $? -eq 1 ] || fail "код без теста прошёл"
echo "$out" | grep -q 'regcheck' || fail "находка про тесты без разбора: $out"
python3 "$here/check-tests.py" pkg/ops.go pkg/ops_test.go >/dev/null || fail "код с тестом не прошёл"
python3 "$here/check-tests.py" pkg/ops.py tests/test_ops.py >/dev/null || fail "тест в tests/ не засчитан"
python3 "$here/check-tests.py" hooks/check-x.py hooks/testdata/sample.json >/dev/null ||
    fail "фикстура в testdata не засчитана"
python3 "$here/check-tests.py" docs/README.md docs/TASKS.md >/dev/null || fail "дока без кода не прошла"
python3 "$here/check-tests.py" --stdin < /dev/null >/dev/null || fail "пустой список не прошёл"
printf 'pkg/ops.go\n' | python3 "$here/check-tests.py" --stdin >/dev/null
[ $? -eq 1 ] || fail "режим --stdin пропустил код без теста"

# Файл без расширения читается по шебангу: утилиты devkit и сами хуки живут
# так, и без этого правка хука за код не считалась бы.
mkdir -p "$tmp/wt"
printf '#!/bin/sh\necho x\n' > "$tmp/wt/tool"
printf 'просто заметка\n' > "$tmp/wt/NOTES"
(cd "$tmp" && python3 "$here/check-tests.py" wt/tool >/dev/null)
[ $? -eq 1 ] || fail "скрипт с шебангом не сошёл за код"
(cd "$tmp" && python3 "$here/check-tests.py" wt/NOTES >/dev/null) || fail "текстовый файл сошёл за код"

# pre-commit: правка кода без теста краснеет, с тестом зеленеет, обход через
# DEVKIT_NO_TESTS пропускает только эту проверку.
repot="$tmp/repot"
git init -q "$repot"
git -C "$repot" config user.name t
git -C "$repot" config user.email t@t
printf 'def f():\n    return 1\n' > "$repot/lib.py"
git -C "$repot" add lib.py
(cd "$repot" && "$here/pre-commit" >/dev/null 2>&1)
[ $? -eq 1 ] || fail "pre-commit пропустил правку кода без теста"
(cd "$repot" && DEVKIT_NO_TESTS=1 "$here/pre-commit" >/dev/null 2>&1) ||
    fail "DEVKIT_NO_TESTS не обошёл проверку тестов"
printf 'def test_f():\n    assert True\n' > "$repot/test_lib.py"
git -C "$repot" add test_lib.py
(cd "$repot" && "$here/pre-commit" >/dev/null 2>&1) || fail "pre-commit ругается на код с тестом"
printf 'текст доки\n' > "$repot/README.md"
git -C "$repot" rm -q --cached lib.py test_lib.py
git -C "$repot" add README.md
(cd "$repot" && "$here/pre-commit" >/dev/null 2>&1) || fail "pre-commit ругается на коммит одной доки"
# Обход тестов не выключает остальные рубежи.
printf 'текст с тире %s\n' "$dash" > "$repot/README.md"
git -C "$repot" add README.md
(cd "$repot" && DEVKIT_NO_TESTS=1 "$here/pre-commit" >/dev/null 2>&1)
[ $? -eq 1 ] || fail "DEVKIT_NO_TESTS выключил проверку символов"

# pre-push: пуш пользователя рубеж не видит, пуш из сессии агента отбивается,
# а разрешение от утилит devkit пропускает.
(CLAUDECODE= CLAUDE_CODE_ENTRYPOINT= CURSOR_AGENT= "$here/pre-push" origin git@example.com:x.git </dev/null >/dev/null 2>&1) ||
    fail "pre-push отбил ручной пуш пользователя"
out=$(CLAUDECODE=1 "$here/pre-push" origin git@example.com:x.git </dev/null 2>&1 >/dev/null)
[ $? -eq 1 ] || fail "pre-push пропустил пуш из сессии агента"
echo "$out" | grep -q 'taskctl' || fail "отказ pre-push без разбора про доску: $out"
echo "$out" | grep -q 'DEVKIT_PUSH_OK' || fail "отказ pre-push без обхода: $out"
(CLAUDECODE=1 DEVKIT_PUSH_OK=1 "$here/pre-push" origin git@example.com:x.git </dev/null >/dev/null 2>&1) ||
    fail "pre-push отбил пуш с разрешением утилиты devkit"

# check-sensitive: local-docs в коммите это находка сама по себе, а на записи
# находка ровно тогда, когда он не заигнорен.
out=$(printf 'local-docs/hosts.md:1:строка\n' | python3 "$here/check-sensitive.py" --diff)
[ $? -eq 1 ] || fail "local-docs проехал в коммит"
echo "$out" | grep -q 'gitignore' || fail "находка про local-docs без разбора: $out"
repol="$tmp/repol"
git init -q "$repol"
mkdir -p "$repol/local-docs"
printf '{"tool_input":{"file_path":"local-docs/hosts.md","new_string":"роутер"}}' > "$tmp/ev.json"
(cd "$repol" && python3 "$here/check-sensitive.py" --hook < "$tmp/ev.json" 2>/dev/null)
[ $? -eq 2 ] || fail "запись в незаигноренный local-docs прошла"
printf 'local-docs/\n' > "$repol/.gitignore"
(cd "$repol" && python3 "$here/check-sensitive.py" --hook < "$tmp/ev.json" 2>/dev/null) ||
    fail "запись в заигноренный local-docs дала находку"

# --- корп-контур: рубеж следов devkit и обёртка-цепочка ---

# Боковая директория с доской и корп-клон с редиректом на неё. Путь редиректа
# относительный, как его кладёт подключение: заодно проверяется, что он
# считается от чекаута. Пометка про префикс стоит и ниже секции, в строке
# задачи: шапка кончается на первой секции, и такая пометка за префикс доски
# сойти не должна.
side="$tmp/proj-local"
mkdir -p "$side/docs" "$side/.devkit"
printf '# проба: задачи (префикс XR)\n\n## In progress\n\n| XR-1 | задача (префикс QQ) |\n' \
    > "$side/docs/TASKS.md"
corp="$tmp/corp"
git init -q "$corp"
git -C "$corp" config user.name t
git -C "$corp" config user.email t@t

stage() { printf '%s\n' "$1" > "$corp/note.txt"; git -C "$corp" add note.txt; }

# Без редиректа проверка молчит: домашние проекты рубежа не замечают.
stage 'правка по XR-007'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null) ||
    fail "рубеж следов сработал без редиректа devkit.local"
git -C "$corp" config devkit.local ../proj-local
out=$(cd "$corp" && python3 "$here/check-traces.py" --staged 2>&1)
[ $? -eq 1 ] || fail "локальный ID в диффе не пойман"
case $out in *"локальный ID"*) ;; *) fail "находка без разбора вида: $out" ;; esac

# Префикс берётся из шапки боковой доски, а не из головы: чужой ID проходит,
# пометка ниже секции за префикс не считается, а смена шапки меняет улов.
stage 'правка по DK-007 и QQ-1'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null) ||
    fail "чужой префикс сошёл за префикс боковой доски"
printf '# проба: задачи (префикс ZZ)\n\n## In progress\n' > "$side/docs/TASKS.md"
stage 'правка по ZZ-9'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "префикс из шапки не перечитан"
printf '# проба: задачи (префикс XR)\n\n## In progress\n' > "$side/docs/TASKS.md"

# Путь боковой директории и её имя это такой же след, как ID.
stage "смотри $side/docs/TASKS.md"
out=$(cd "$corp" && python3 "$here/check-traces.py" --staged 2>&1)
[ $? -eq 1 ] || fail "путь боковой директории в диффе не пойман"
case $out in *"путь боковой"*) ;; *) fail "находка про путь без разбора вида: $out" ;; esac
stage 'лежит в proj-local рядом'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "имя боковой директории в диффе не поймано"

# Перечень расширяется словами контура из привязки.
printf 'repo = %s\ntraces = ковчег\n' "$corp" > "$side/.devkit/tracker.local"
stage 'внутренний Ковчег проекта'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "слово контура из привязки не поймано"

# Префикс доски, совпавший с ключом проекта в привязке: локальный ID и ключ
# тикета там одна и та же строка, отличить их нечем, и правило про ID снимается
# целиком, иначе рубеж валил бы каждый коммит по конвенции компании (DK-124).
# Путь боковой директории и слова контура при этом стерегутся по-прежнему.
printf 'repo = %s\ntraces = ковчег\nkey = XR\n' "$corp" > "$side/.devkit/tracker.local"
stage 'правка по XR-007'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null) ||
    fail "рубеж бьёт по ключу тикета, неотличимому от локального ID"
stage "смотри $side/docs/TASKS.md"
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "на совпавших префиксах потерян рубеж по пути боковой директории"
stage 'внутренний Ковчег проекта'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "на совпавших префиксах потеряно слово контура"
# Ключ проекта, разведённый с префиксом доски: правило про ID работает как
# работало, а ключ тикета проходит.
printf 'repo = %s\ntraces = ковчег\nkey = TR\n' "$corp" > "$side/.devkit/tracker.local"
stage 'правка по XR-007'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "разведённый ключ проекта снял правило про локальный ID"
stage 'правка по TR-007'
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null) ||
    fail "ключ тикета чужого префикса принят за локальный ID"
printf 'repo = %s\ntraces = ковчег\n' "$corp" > "$side/.devkit/tracker.local"

# Имя добавленного файла смотрится наравне с его строками.
printf 'обычная строка\n' > "$corp/XR-012.md"
git -C "$corp" add XR-012.md
(cd "$corp" && python3 "$here/check-traces.py" --staged 2>/dev/null)
[ $? -eq 1 ] || fail "локальный ID в имени файла не пойман"
rm "$corp/XR-012.md"
git -C "$corp" rm -q --cached XR-012.md

# Сообщение коммита смотрится отдельно от диффа: чистый дифф от следа в тексте
# не спасает. Комментарии git и хвост после scissors не в счёт.
stage 'обычная правка'
printf 'feat: правка по XR-007\n' > "$tmp/cmsg"
(cd "$corp" && python3 "$here/check-traces.py" --msg "$tmp/cmsg" 2>/dev/null)
[ $? -eq 1 ] || fail "локальный ID в сообщении коммита не пойман"
printf 'feat: обычная правка\n' > "$tmp/cmsg"
(cd "$corp" && python3 "$here/check-traces.py" --msg "$tmp/cmsg" 2>/dev/null) ||
    fail "чистое сообщение не прошло рубеж следов"
printf 'feat: чисто\n# ветка dk-086 в XR-007\n' > "$tmp/cmsg"
(cd "$corp" && python3 "$here/check-traces.py" --msg "$tmp/cmsg" 2>/dev/null) ||
    fail "рубеж следов смотрит в комментарии git"
printf 'feat: чисто\n# ------------------------ >8 ------------------------\ndiff по XR-007\n' > "$tmp/cmsg"
(cd "$corp" && python3 "$here/check-traces.py" --msg "$tmp/cmsg" 2>/dev/null) ||
    fail "рубеж следов смотрит за scissors-строку"

# Обёртка-цепочка: чужой хук переезжает в <хук>.chained, на его место встаёт
# файл devkit с маркером в шапке.
chain() {
    python3 -c 'import sys
sys.path.insert(0, sys.argv[1])
import corp
print(corp.install_chain(sys.argv[2], sys.argv[3], sys.argv[1])[0])' "$here" "$1" "$2"
}
hooksdir="$corp/.git/hooks"
mkdir -p "$hooksdir"
alien="$tmp/alien.log"
: > "$alien"
printf '#!/bin/sh\necho %s >> "%s"\nexit ${ALIEN_CODE:-0}\n' 'чужой' "$alien" > "$hooksdir/pre-commit"
chmod +x "$hooksdir/pre-commit"
[ "$(chain "$hooksdir" pre-commit)" = "установлена" ] || fail "первая раскладка обёртки не установлена"
[ -x "$hooksdir/pre-commit.chained" ] || fail "чужой хук не переехал в .chained"
head -3 "$hooksdir/pre-commit" | grep -q 'devkit-corp-chain' || fail "в обёртке нет маркера"
[ "$(chain "$hooksdir" commit-msg)" = "установлена" ] || fail "обёртка commit-msg не установлена"

# Повторный прогон ничего не меняет и чужой хук вторым переездом не теряет.
[ "$(chain "$hooksdir" pre-commit)" = "на месте" ] || fail "повторная раскладка переписала обёртку"
[ -e "$hooksdir/pre-commit.chained.chained" ] && fail "повторный прогон переехал собственной обёрткой"
grep -q "$alien" "$hooksdir/pre-commit.chained" || fail "чужой хук потерян повторным прогоном"

# Чистый коммит проходит, и чужой хук в нём отрабатывает.
stage 'первая строка проекта'
git -C "$corp" commit -qm 'feat: чистая правка' >/dev/null 2>&1 ||
    fail "чистый коммит не прошёл через цепочку"
[ "$(wc -l < "$alien")" -eq 1 ] || fail "чужой pre-commit не позван цепочкой"

# След в сообщении валит коммит, чужой хук при этом всё равно отработал.
stage 'вторая строка проекта'
git -C "$corp" commit -qm 'feat: правка по XR-007' >/dev/null 2>&1 &&
    fail "коммит со следом devkit в сообщении прошёл"
[ "$(wc -l < "$alien")" -eq 2 ] || fail "чужой pre-commit не позван на падающем коммите"

# Ненулевой код чужой стороны валит коммит.
ALIEN_CODE=1 git -C "$corp" commit -qm 'feat: чистая правка' >/dev/null 2>&1 &&
    fail "отказ чужого хука проглочен цепочкой"

# Ненулевой код проверок devkit тоже валит коммит.
stage "строка с тире $dash тут"
git -C "$corp" commit -qm 'feat: правка с тире' >/dev/null 2>&1 &&
    fail "отказ проверок devkit проглочен цепочкой"

# Перезаписанная чужим инсталлером обёртка теряет маркер, и повторная раскладка
# разворачивает цепочку заново.
printf '#!/bin/sh\nexit 0\n' > "$hooksdir/pre-commit"
python3 -c 'import sys
sys.path.insert(0, sys.argv[1])
import corp
sys.exit(0 if corp.is_chain(sys.argv[2]) else 1)' "$here" "$hooksdir/pre-commit" &&
    fail "перезаписанный чужим инсталлером файл сошёл за обёртку"
[ "$(chain "$hooksdir" pre-commit)" = "установлена" ] || fail "цепочка не развёрнута заново"
head -3 "$hooksdir/pre-commit" | grep -q 'devkit-corp-chain' || fail "после починки нет маркера"

# Домашний проект с той же обёрткой корп-проверок не замечает: без редиректа
# рубеж следов молчит.
home="$tmp/homerepo"
git init -q "$home"
git -C "$home" config user.name t
git -C "$home" config user.email t@t
mkdir -p "$home/.git/hooks"
chain "$home/.git/hooks" pre-commit >/dev/null
chain "$home/.git/hooks" commit-msg >/dev/null
printf 'правка по XR-007 в %s\n' "$side" > "$home/note.txt"
git -C "$home" add note.txt
git -C "$home" commit -qm 'feat: правка по XR-007' >/dev/null 2>&1 ||
    fail "рубеж следов сработал в домашнем проекте"

if [ $fails -eq 0 ]; then
    echo "хуки в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
