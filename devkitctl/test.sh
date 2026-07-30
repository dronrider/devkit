#!/bin/sh
# Самопроверка devkitctl: подключение проекта и диагностика обвязки гоняются
# на временном репозитории с подставным HOME.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
fail() { echo "FAIL: $1" >&2; fails=$((fails + 1)); }
# Сборка бинарей идёт в GOBIN либо GOPATH, а они на машине выставлены на
# настоящий ~/go/bin: без сброса самопроверка положила бы туда заглушки.
unset GOBIN GOPATH

home="$tmp/home"
mkdir -p "$home/.claude"
cat > "$home/.claude/settings.json" <<'EOF'
{"hooks": {"PostToolUse": [{"matcher": "Edit|Write|NotebookEdit", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-symbols.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-memory.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-sensitive.py --hook"}
]}]}}
EOF

# Машинный контур подставной: бинари devkit и tmux заглушками в своём PATH,
# определения исполнителей и свежий снимок квоты в подставном HOME. Иначе
# проверки цеплялись бы за настоящую машину и в CI шли бы находками.
bin="$tmp/bin"
mkdir -p "$bin"
for t in taskctl shipctl agentctl regcheck tmux; do
    printf '#!/bin/sh\nexit 0\n' > "$bin/$t"
    chmod +x "$bin/$t"
done
# Системная часть PATH тоже подставная: в ней только то, без чего не обойтись.
# Проверки вида «tmux не в PATH» иначе держались бы на том, чего нет в
# /usr/bin на этой машине, и на другой краснели бы при исправном коде.
sys="$tmp/sys"
mkdir -p "$sys"
for t in git python3 dirname mkdir chmod rm; do
    p=$(command -v "$t") || fail "в PATH нет $t, самопроверке не на чем стоять"
    ln -sf "$p" "$sys/$t"
done
cleanpath="$bin:$sys"
mkdir -p "$home/.claude/agents" "$home/.devkit"
cp "$here/../agents/"exec-*.md "$home/.claude/agents/"
printf 'taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n' "$(date '+%Y-%m-%dT%H:%M')" \
    > "$home/.devkit/quota.local"

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
out=$(HOME="$home" PATH="$cleanpath" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
[ $? -eq 0 ] || fail "doctor нашёл находки на чистом проекте: $out"

# doctor: битый импорт, битая ссылка и пропавший PostToolUse-хук ловятся.
printf '@../devkit/NOPE.md\n' >> "$proj/CLAUDE.md"
mkdir -p "$proj/docs"
printf 'смотри [детали](nope.md)\n' > "$proj/docs/note.md"
out=$(HOME="$home" PATH="$cleanpath" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
[ $? -eq 1 ] || fail "doctor не увидел поломок"
echo "$out" | grep -q 'импорт' || fail "нет находки про битый импорт"
echo "$out" | grep -q 'битая ссылка' || fail "нет находки про битую ссылку"
sed -e '/check-memory/d' "$home/.claude/settings.json" > "$home/.claude/settings.json.new" &&
    mv "$home/.claude/settings.json.new" "$home/.claude/settings.json"
out=$(HOME="$home" PATH="$cleanpath" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'check-memory' || fail "нет находки про пропавший хук памяти"

# doctor: доска без taskctl в PATH это находка (PATH обрезан до системного).
printf '# Задачи\n' > "$proj/docs/TASKS.md"
out=$(HOME="$home" PATH="$sys" python3 "$here/devkitctl.py" doctor -C "$proj" 2>&1)
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
out=$(HOME="$home" PATH="$sys" python3 "$here/devkitctl.py" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'пустой deploy=' || fail "нет находки про пустую команду выката: $out"
printf 'deploy = make deploy\nautonomous = false\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$here/devkitctl.py" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'deploy' && fail "заполненная обвязка выката всё ещё в находках: $out"

# doctor: autonomous=true с пустым deploy= это специальная находка.
printf 'autonomous = true\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$here/devkitctl.py" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'autonomous = true при пустом deploy=' || fail "нет находки про autonomous=true без команды: $out"
echo "$out" | grep -q 'shipctl нечего выкатывать' && fail "старая находка дублирует новую при autonomous=true: $out"
# autonomous=false с пустым deploy= это старая находка, без новой.
printf 'autonomous = false\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$here/devkitctl.py" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'autonomous = true при пустом deploy=' && fail "новая находка не должна быть для autonomous=false: $out"
echo "$out" | grep -q 'пустой deploy=' || fail "старая находка про пустой deploy должна остаться: $out"

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
out=$(HOME="$home" PATH="$sys" python3 "$here/devkitctl.py" doctor -C "$nproj" 2>&1)
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
out=$(HOME="$home" PATH="$cleanpath" python3 "$here/devkitctl.py" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'починено' || fail "doctor --fix ничего не починил: $out"
[ -f "$fproj/.devkit/deploy.local" ] || fail "doctor --fix не завёл deploy.local"
git -C "$fproj" check-ignore -q .devkit/deploy.local || fail "doctor --fix не гитигнорил deploy.local"
[ -n "$(git -C "$fproj" config core.hooksPath)" ] || fail "doctor --fix не подключил хуки"
# Пустой deploy= остаётся находкой: команду выката --fix не выдумывает.
echo "$out" | grep -q 'пустой deploy=' || fail "doctor --fix должен просить вписать команду: $out"

# Повторный --fix уже ничего не меняет (идемпотентность).
out=$(HOME="$home" PATH="$cleanpath" python3 "$here/devkitctl.py" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный doctor --fix не должен ничего менять: $out"

# Заполненная команда: находки по выкату уходят.
printf 'deploy = make deploy\nautonomous = false\n' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$here/devkitctl.py" doctor -C "$fproj" 2>&1)
echo "$out" | grep -q 'deploy' && fail "заполненная обвязка выката всё ещё в находках: $out"

# Машинный контур гоняется на копии devkit во временной директории: свежесть
# бинарей меряется по mtime исходников, и настоящий чекаут для этого трогать
# нельзя. Копия не под git, так что для доктора это основной чекаут.
dk="$tmp/devkit"
mkdir -p "$dk"
for d in devkitctl agents hooks taskctl shipctl agentctl regcheck; do
    cp -R "$here/../$d" "$dk/"
done
dkctl="$dk/devkitctl/devkitctl.py"
mproj="$tmp/mproj"
mkdir -p "$mproj"
git init -q "$mproj"
git -C "$mproj" config user.name t
git -C "$mproj" config user.email t@t

# Машинный контур на пустой машине: бинарей нет, определений исполнителей нет,
# tmux нет, снимка квоты нет. Сборку изображает заглушка go, гонять настоящую
# в самопроверке незачем.
mhome="$tmp/mhome"
mkdir -p "$mhome/go/bin"
gostub="$tmp/gostub"
mkdir -p "$gostub"
cat > "$gostub/go" <<'EOF'
#!/bin/sh
# go build -o <путь> . кладёт по пути пустой исполняемый файл. За пределы
# временной директории заглушка не пишет: промах настройки в самопроверке
# иначе затёр бы настоящие бинари в ~/go/bin.
out=""
while [ $# -gt 0 ]; do
    if [ "$1" = "-o" ]; then shift; out=$1; fi
    shift
done
[ -n "$out" ] || exit 1
case "$out" in "$SANDBOX"/*) ;; *) echo "заглушка go пишет только в $SANDBOX: $out" >&2; exit 1;; esac
mkdir -p "$(dirname "$out")"
printf '#!/bin/sh\nexit 0\n' > "$out"
chmod +x "$out"
EOF
chmod +x "$gostub/go"
SANDBOX="$tmp"
export SANDBOX
mpath="$gostub:$mhome/go/bin:$sys"

out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'agentctl не в PATH' || fail "нет находки про бинарь agentctl: $out"
echo "$out" | grep -q 'exec-medium.md' || fail "нет находки про определения исполнителей: $out"
echo "$out" | grep -q 'tmux не в PATH' || fail "нет находки про tmux: $out"
echo "$out" | grep -q 'нет снимка квоты' || fail "нет находки про снимок квоты: $out"
[ -f "$mhome/go/bin/agentctl" ] && fail "doctor без --fix собрал бинарь"

# --fix собирает бинари и раскладывает определения, а неоднозначное (tmux,
# снимок квоты) оставляет находкой с командой.
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
for t in taskctl shipctl agentctl regcheck; do
    [ -x "$mhome/go/bin/$t" ] || fail "doctor --fix не собрал $t: $out"
done
[ -f "$mhome/.claude/agents/exec-medium.md" ] || fail "doctor --fix не разложил определения исполнителей: $out"
echo "$out" | grep -q 'tmux не в PATH' || fail "--fix не ставит tmux, находка должна остаться: $out"
echo "$out" | grep -q 'agentctl quota refresh' || fail "--fix не снимает квоту, находка должна остаться: $out"

# Повторный --fix по машинному контуру уже ничего не чинит.
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный --fix по машинному контуру не должен ничего менять: $out"

# Правленое руками определение исполнителя это находка, --fix его не затирает.
printf '\nсвоя строка\n' >> "$mhome/.claude/agents/exec-low.md"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'exec-low.md разошлось' || fail "нет находки про разошедшееся определение: $out"
grep -q 'своя строка' "$mhome/.claude/agents/exec-low.md" || fail "--fix затёр правленое определение"

# Снимок квоты: протухший это находка, свежий нет.
mkdir -p "$mhome/.devkit"
printf 'taken = 2020-01-01T00:00\nweek_all = 40%% сброс 2030-01-01T00:00\n' > "$mhome/.devkit/quota.local"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'старее суток' || fail "нет находки про протухший снимок квоты: $out"
printf 'taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n' "$(date '+%Y-%m-%dT%H:%M')" \
    > "$mhome/.devkit/quota.local"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'снимок квоты' && fail "свежий снимок квоты не должен быть находкой: $out"
# Снимок без разобранного момента снятия это тоже находка.
printf 'week_all = 40%% сброс 2030-01-01T00:00\n' > "$mhome/.devkit/quota.local"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'не разобран момент снятия' || fail "нет находки про снимок без taken: $out"

# Бинарь старее исходников devkit (так выходит после git pull): находка, а
# --fix пересобирает.
touch -t 200001010000 "$mhome/go/bin/agentctl"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'agentctl старее исходников devkit' || fail "нет находки про устаревший бинарь: $out"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено: agentctl собран' || fail "--fix не пересобрал устаревший бинарь: $out"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'старее исходников devkit' && fail "после пересборки находка про устаревший бинарь осталась: $out"

# Затенённый бинарь: свежая сборка на месте, а в PATH выигрывает чужая копия.
# Это находка, и пересобирать на каждом прогоне нечего.
shadow="$tmp/shadow"
mkdir -p "$shadow"
printf '#!/bin/sh\nexit 0\n' > "$shadow/agentctl"
chmod +x "$shadow/agentctl"
touch -t 200001010000 "$shadow/agentctl"
spath="$gostub:$shadow:$mhome/go/bin:$sys"
out=$(HOME="$mhome" PATH="$spath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'в PATH выигрывает' || fail "нет находки про затенённый бинарь: $out"
echo "$out" | grep -q 'починено: agentctl собран' && fail "затенённый бинарь пересобирается впустую: $out"
out=$(HOME="$mhome" PATH="$spath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный --fix при затенённом бинаре не должен ничего менять: $out"
echo "$out" | grep -q 'в PATH выигрывает' || fail "находка про затенённый бинарь должна остаться: $out"

# Каталог сборки: GOBIN сильнее умолчания, GOPATH задаёт свой bin.
gbhome="$tmp/gbhome"
gb="$tmp/gobin2"
out=$(HOME="$gbhome" GOBIN="$gb" PATH="$gostub:$gb:$sys" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
[ -x "$gb/agentctl" ] || fail "--fix собрал не в GOBIN: $out"
[ -f "$gbhome/go/bin/agentctl" ] && fail "--fix при заданном GOBIN лезет в ~/go/bin"
gphome="$tmp/gphome"
gp="$tmp/gopath2"
out=$(HOME="$gphome" GOPATH="$gp" PATH="$gostub:$gp/bin:$sys" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
[ -x "$gp/bin/agentctl" ] || fail "--fix собрал не в GOPATH/bin: $out"
[ -f "$gphome/go/bin/agentctl" ] && fail "--fix при заданном GOPATH лезет в ~/go/bin"

# Нет go: --fix не молчит, а называет находку с командой установки.
nghome="$tmp/nghome"
out=$(HOME="$nghome" PATH="$sys" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'go в PATH нет' || fail "нет находки про отсутствующий go: $out"

# devkit, выложенный worktree ветки задачи: mtime исходников там ничего не
# значит, сборка не запускается, находка отправляет в основной чекаут.
git -C "$dk" init -q .
git -C "$dk" config user.name t
git -C "$dk" config user.email t@t
git -C "$dk" add -A
git -C "$dk" commit -qm init
git -C "$dk" worktree add -q -b probe "$tmp/devkit-wt"
wthome="$tmp/wthome"
out=$(HOME="$wthome" PATH="$gostub:$sys" python3 "$tmp/devkit-wt/devkitctl/devkitctl.py" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'worktree ветки задачи' || fail "нет находки про worktree devkit: $out"
echo "$out" | grep -q "$dk/agentctl" || fail "находка не отправляет в основной чекаут: $out"
[ -f "$wthome/go/bin/agentctl" ] && fail "--fix собрал машинный бинарь с фичеветки"

# stats: вывод сводки по журналу запусков, сортировка по частоте.
sproj="$tmp/sproj"
mkdir -p "$sproj/.devkit"
git init -q "$sproj"
git -C "$sproj" config user.name t
git -C "$sproj" config user.email t@t
# Подложить журнал с известными строками, частую команду второй.
cat > "$sproj/.devkit/log" <<'EOF'
2026-07-29T01:02:41	shipctl	merge	0
2026-07-29T01:02:41	shipctl	merge	0
2026-07-29T01:02:40	taskctl	move	0
2026-07-29T01:02:40	taskctl	move	0
2026-07-29T01:02:40	taskctl	move	0
2026-07-29T01:02:40	taskctl	move	1
2026-07-29T01:02:41	regcheck	run	1
2026-07-29T01:02:41	broken	line	broken
2026-07-29T01:02:41	broken	code	bad
EOF
out=$(python3 "$here/devkitctl.py" stats -C "$sproj" 2>&1)
[ $? -eq 0 ] || fail "stats упал с ошибкой: $out"
echo "$out" | grep -q "taskctl move" || fail "в выводе stats нет taskctl move: $out"
echo "$out" | grep -q "shipctl merge" || fail "в выводе stats нет shipctl merge: $out"
echo "$out" | grep -q "итого" || fail "в выводе stats нет итоговой строки: $out"
# Проверить числа: taskctl move должно быть первым (4 запуска) несмотря на порядок в журнале.
first_line=$(echo "$out" | grep -v "итого" | head -1)
echo "$first_line" | grep -q "taskctl move.*4" || fail "taskctl move должно быть первым с 4 запусками: $first_line"
# Проверить ошибки: taskctl move должно иметь 1 ошибку из 4.
echo "$out" | grep "taskctl move" | head -1 | grep -q "ошибок 1 (25%)" || fail "taskctl move должно иметь 1 ошибку (25%)"
# Проверить что поломанные строки пропущены (всего должно быть 3 команды).
cmd_count=$(echo "$out" | grep -v "итого" | wc -l)
[ "$cmd_count" -eq 3 ] || fail "должно быть 3 команды, найдено $cmd_count"

# stats без журнала: код выхода 2.
empty_proj="$tmp/empty"
mkdir -p "$empty_proj"
out=$(python3 "$here/devkitctl.py" stats -C "$empty_proj" 2>&1)
[ $? -eq 2 ] || fail "stats без журнала должен вернуть код 2"

# stats с журналом из одних битых строк: код выхода 2, сообщение про пустой журнал.
bad_proj="$tmp/bad"
mkdir -p "$bad_proj/.devkit"
printf 'broken\tline\tno\tcode\nthis\tis\tbroken\ttoo\n' > "$bad_proj/.devkit/log"
out=$(python3 "$here/devkitctl.py" stats -C "$bad_proj" 2>&1)
[ $? -eq 2 ] || fail "stats с одними битыми строками должен вернуть код 2"

if [ $fails -eq 0 ]; then
    echo "devkitctl в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
