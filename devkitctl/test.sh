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

# Всё гоняется на копии devkit во временной директории, а не на живом чекауте:
# доктор судит по mtime исходников и сверяет определения агентов с
# основным чекаутом, так что незакоммиченная правка в ~/projects/devkit красила
# бы самопроверку на исправном коде. Копия не под git, поэтому для доктора она
# и есть основной чекаут.
dk="$tmp/devkit"
mkdir -p "$dk"
for d in devkitctl agents skills harness hooks templates taskctl shipctl agentctl regcheck; do
    cp -R "$here/../$d" "$dk/"
done
cp "$here/../RULES.md" "$here/../RULES.board.md" "$dk/"
dkctl="$dk/devkitctl/devkitctl.py"

home="$tmp/home"
mkdir -p "$home/.claude"
cat > "$home/.claude/settings.json" <<'EOF'
{"hooks": {"PostToolUse": [{"matcher": "Edit|Write|NotebookEdit", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-symbols.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-memory.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-sensitive.py --hook"}
]}], "SessionStart": [{"hooks": [
  {"type": "command", "command": "sh ~/projects/devkit/hooks/quota-refresh.sh"}
]}], "Notification": [{"hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/notify.py --hook claude-code"}
]}], "Stop": [{"hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/notify.py --hook claude-code"}
]}], "SubagentStop": [{"hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/notify.py --hook claude-code"}
]}], "UserPromptSubmit": [{"hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/notify.py --hook claude-code"}
]}]}}
EOF

# Чем слать уведомления, доктор спрашивает у самого уведомителя, а тот смотрит
# платформу и PATH. Без подставного бэкенда проверка краснела бы на любой машине
# без osascript или notify-send, поэтому он выставлен на весь прогон; отсутствие
# бэкенда проверяется отдельным шагом со снятой переменной.
DEVKIT_NOTIFY_BACKEND="$tmp/notify-stub"
export DEVKIT_NOTIFY_BACKEND

# Машинный контур подставной: бинари devkit и tmux заглушками в своём PATH,
# определения агентов и свежий снимок квоты в подставном HOME. Иначе
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
    if ! p=$(command -v "$t"); then
        fail "в PATH нет $t, самопроверке не на чем стоять"
        continue
    fi
    ln -sf "$p" "$sys/$t"
done
cleanpath="$bin:$sys"
mkdir -p "$home/.claude/agents" "$home/.claude/skills" "$home/.devkit/quota"
cp "$dk/agents/"*.md "$home/.claude/agents/"
cp -R "$dk/skills/"* "$home/.claude/skills/"
printf 'taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n' "$(date '+%Y-%m-%dT%H:%M')" \
    > "$home/.devkit/quota/claude-code.local"

proj="$tmp/proj"
mkdir -p "$proj"
git init -q "$proj"
git -C "$proj" config user.name t
git -C "$proj" config user.email t@t

# new: AGENTS.md из шаблона, тонкий CLAUDE.md генератором, hooksPath на хуки
# devkit, повторный запуск отбит.
HOME="$home" python3 "$dkctl" new --no-board -C "$proj" >/dev/null || fail "new не прошёл"
[ -f "$proj/AGENTS.md" ] || fail "new не создал AGENTS.md"
grep -q '<название проекта>' "$proj/AGENTS.md" && fail "плейсхолдер названия не заменён"
grep -q '^@' "$proj/AGENTS.md" && fail "в AGENTS.md из шаблона есть строка импорта"
[ -f "$proj/CLAUDE.md" ] || fail "new не сгенерил тонкий CLAUDE.md"
head -1 "$proj/CLAUDE.md" | grep -q '^<!-- devkit:generated body=[0-9a-f]\{12\} -->$' ||
    fail "у тонкого CLAUDE.md нет маркера с хешем: $(head -1 "$proj/CLAUDE.md")"
grep -q '^@AGENTS.md$' "$proj/CLAUDE.md" || fail "тонкий CLAUDE.md не ссылается на AGENTS.md"
grep -q '^@../devkit/RULES.md$' "$proj/CLAUDE.md" || fail "тонкий CLAUDE.md не ссылается на правила devkit"
grep -q 'RULES.board.md' "$proj/CLAUDE.md" && fail "проекту без доски выписаны правила доски"
cp "$proj/CLAUDE.md" "$tmp/thin.gen"
hp=$(git -C "$proj" config core.hooksPath)
[ -n "$hp" ] || fail "hooksPath не выставлен"
[ -f "$proj/$hp/pre-commit" ] || fail "hooksPath смотрит мимо хуков devkit: $hp"
HOME="$home" python3 "$dkctl" new --no-board -C "$proj" >/dev/null 2>&1
[ $? -eq 2 ] || fail "повторный new не отбит"

# doctor: свежеподключённый проект чист.
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
[ $? -eq 0 ] || fail "doctor нашёл находки на чистом проекте: $out"

# doctor: битый импорт, битая ссылка и пропавший PostToolUse-хук ловятся.
printf '@../devkit/NOPE.md\n' >> "$proj/CLAUDE.md"
mkdir -p "$proj/docs"
printf 'смотри [детали](nope.md)\n' > "$proj/docs/note.md"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
[ $? -eq 1 ] || fail "doctor не увидел поломок"
echo "$out" | grep -q 'импорт @../devkit/NOPE.md не разворачивается' || fail "нет находки про битый импорт: $out"
cp "$tmp/thin.gen" "$proj/CLAUDE.md"
echo "$out" | grep -q 'битая ссылка' || fail "нет находки про битую ссылку"
# Пример ссылки внутри блока кода это не ссылка: документированная команда с
# [текст](путь) в теле не должна красить доктор.
printf 'пример:\n\n````\n```\nсмотри [детали](nope.md)\n```\n````\n' > "$proj/docs/note.md"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'битая ссылка' && fail "ссылка в блоке кода принята за настоящую"
printf 'пример команды `смотри [детали](nope.md)` в строке\n' > "$proj/docs/note.md"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'битая ссылка' && fail "ссылка в инлайн-коде принята за настоящую"
# Отступ в четыре пробела забор уже не открывает (CommonMark), ссылка под ним
# остаётся настоящей.
printf 'пример:\n\n    ```\nсмотри [детали](nope.md)\n' > "$proj/docs/note.md"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'битая ссылка' || fail "четыре пробела приняты за забор блока кода"
printf 'смотри [детали](nope.md)\n' > "$proj/docs/note.md"
sed -e '/check-memory/d' "$home/.claude/settings.json" > "$home/.claude/settings.json.new" &&
    mv "$home/.claude/settings.json.new" "$home/.claude/settings.json"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'check-memory' || fail "нет находки про пропавший хук памяти"
# Хук освежения квоты живёт в другом событии, и проверяться должен своей строкой.
sed -e '/quota-refresh/d' "$home/.claude/settings.json" > "$home/.claude/settings.json.new" &&
    mv "$home/.claude/settings.json.new" "$home/.claude/settings.json"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'SessionStart-хук quota-refresh.sh' || fail "нет находки про хук освежения квоты: $out"

# Уведомитель висит на четырёх событиях сразу, и пропажа любого это находка:
# без SubagentStop сессия молчит про отработавшего субагента, а без
# UserPromptSubmit не снимается отметка ожидания.
nset="$home/.claude/settings.json"
cp "$nset" "$tmp/settings.full"
python3 - "$nset" <<'EOF'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
del d["hooks"]["SubagentStop"]
del d["hooks"]["UserPromptSubmit"]
json.dump(d, open(p, "w"))
EOF
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'notify.py не подключён на события SubagentStop, UserPromptSubmit' ||
    fail "нет находки про неподключённый хук субагента: $out"
echo "$out" | grep -q 'события Notification' && fail "подключённое событие попало в находку: $out"
sed -e '/notify.py/d' "$tmp/settings.full" > "$nset"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'notify.py не подключён на события Notification, Stop, SubagentStop, UserPromptSubmit' ||
    fail "нет находки про неподключённый уведомитель: $out"
cp "$tmp/settings.full" "$nset"

# Слать нечем: бэкенда на платформе нет (PATH подставной, переменная снята),
# и доктор называет это находкой с командой самопроверки.
out=$( (unset DEVKIT_NOTIFY_BACKEND; HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1) )
echo "$out" | grep -q 'уведомлять нечем' || fail "нет находки про отсутствие бэкенда уведомлений: $out"
echo "$out" | grep -q 'notify.py --self-test' || fail "в находке про бэкенд нет команды проверки: $out"

# Слать есть чем, но клик по баннеру уводит в Finder: доктор предлагает
# отправителя с переходом. Случай ровно macOS-ный, на другой платформе клик не
# поддержан ни одним бэкендом, и предлагать там нечего.
if [ "$(uname)" = Darwin ]; then
    printf '#!/bin/sh\nexit 0\n' > "$tmp/osascript"
    chmod +x "$tmp/osascript"
    out=$(DEVKIT_NOTIFY_BACKEND="$tmp/osascript" HOME="$home" PATH="$cleanpath" \
        python3 "$dkctl" doctor -C "$proj" 2>&1)
    echo "$out" | grep -q 'клик по баннеру ведёт не в окно сессии' ||
        fail "нет находки про клик мимо окна сессии: $out"
    echo "$out" | grep -q 'brew install terminal-notifier' ||
        fail "в находке про клик нет команды установки: $out"
    printf '#!/bin/sh\nexit 0\n' > "$tmp/terminal-notifier"
    chmod +x "$tmp/terminal-notifier"
    out=$(DEVKIT_NOTIFY_BACKEND="$tmp/terminal-notifier" HOME="$home" PATH="$cleanpath" \
        python3 "$dkctl" doctor -C "$proj" 2>&1)
    echo "$out" | grep -q 'клик по баннеру' &&
        fail "находка про клик осталась при отправителе, который клик умеет: $out"
fi

# Профили харнесов: общие с agentctl фикстуры. На каждый вход отчёт парсера
# сверяется побайтно с .expected, тот же файл сверяет Go-реализация
# (agentctl/harness_test.go). Разъедься парсеры, и один и тот же профиль
# читался бы двумя утилитами по-разному.
seen=0
for f in "$dk"/harness/testdata/*.toml; do
    seen=$((seen + 1))
    base=$(basename "$f" .toml)
    if ! python3 "$dk/devkitctl/harness.py" "$f" > "$tmp/report.out" 2>"$tmp/report.err"; then
        fail "парсер профилей упал на $base: $(cat "$tmp/report.err")"
        continue
    fi
    diff -u "$dk/harness/testdata/$base.expected" "$tmp/report.out" > "$tmp/report.diff" ||
        fail "отчёт по $base разошёлся с ожидаемым: $(cat "$tmp/report.diff")"
done
[ "$seen" -ge 10 ] || fail "фикстур парсера найдено $seen, набор потерялся"

# Профиль сегодняшнего харнеса валиден, и доктор это проверяет на каждом
# прогоне: битый профиль молча выключил бы ось, а находится он тут.
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'профиль харнеса' && fail "находка по исправным профилям: $out"
cp "$dk/harness/claude-code.toml" "$tmp/claude-code.toml.bak"
printf '[detect]\n\n[rules]\nmode = "import"\n\n[delegate]\nmode = "none"\n\n[hooks]\n\n[quota]\n' \
    > "$dk/harness/claude-code.toml"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'профиль харнеса битый: claude-code.toml: \[rules\] нет ключа file' ||
    fail "нет находки про битый профиль харнеса: $out"
# Следствие битого профиля называется отдельно: генерировать по нему нечего, и
# молчать об этом нельзя, иначе правила просто перестали бы доезжать.
echo "$out" | grep -q 'харнес claude-code включён, а его профиль не проходит валидацию' ||
    fail "нет находки про генерацию по битому профилю: $out"
cp "$tmp/claude-code.toml.bak" "$dk/harness/claude-code.toml"

# Юниты генератора правил: хеш маркера, поломанные маркеры, забор кода. Гоняются
# прямо по модулю, доктор до них не нужен.
python3 - "$dk/devkitctl" <<'EOF' || fail "юниты генератора правил не прошли"
import sys
sys.path.insert(0, sys.argv[1])
import harness
import rules

prof = harness.parse("p.toml", '[rules]\nmode = "import"\nfile = "CLAUDE.md"\nimport_line = "@{path}"\n')
thin = rules.thin_text(prof, "/proj", "/proj/../devkit", board=False, embed=True)
stamp, body = rules.generated_parts(thin)
assert body == "@AGENTS.md\n", body
assert stamp == rules.digest(body), thin
assert rules.generated_parts("# рукописный\n") == (None, None)

block = rules.block_text("aabbccddeeff", "правила\n")
text = rules.put_block("# проект\n", block)
assert rules.find_block(text)[1] == "aabbccddeeff", text
assert rules.find_block(text)[3] == "правила\n", text
assert rules.drop_block(text) == "# проект\n", repr(rules.drop_block(text))
try:
    rules.find_block("# проект\n<!-- devkit:rules end -->\n")
    raise AssertionError("конец без начала принят за целую вклейку")
except rules.BrokenMarkers:
    pass
try:
    rules.find_block(block.split("\n")[0] + "\nтекст\n")
    raise AssertionError("начало без конца принято за целую вклейку")
except rules.BrokenMarkers:
    pass

# Пример в блоке кода это текст, а не вклейка и не импорт: и то и другое
# показано в дизайне DK-033 и в доке, скан по ним не должен спотыкаться.
sample = "пример:\n\n```\n" + block + "@../devkit/RULES.md\n```\n"
assert rules.find_block(sample) is None, sample
assert rules.handwritten_imports(sample) == [], rules.handwritten_imports(sample)
assert rules.handwritten_imports("# проект\n@../devkit/RULES.md\n") == [(2, "@../devkit/RULES.md")]
EOF

# Глубина правил: три ветки оси скиллов, признак глубины в тонком файле и
# побайтная сверка сегодняшней раскладки с эталоном. Гоняется на выдуманном
# devkit во временной директории, чтобы нарезанное ядро можно было изобразить
# файлами, которых в настоящем devkit пока нет.
python3 - "$dk/devkitctl" "$tmp/depth" "$dk" <<'EOF' || fail "юниты глубины правил не прошли"
import os
import sys

sys.path.insert(0, sys.argv[1])
work, dkroot = sys.argv[2], sys.argv[3]
import harness
import rules


def prof(skills):
    return harness.parse("p.toml", '[rules]\nmode = "import"\nfile = "T.md"\n'
                         'import_line = "@{path}"\n' + skills)


def put(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w", encoding="utf-8").write(text)


none = prof("")
empty = prof("\n[skills]\n")
auto = prof('\n[skills]\ndir = "~/.t/skills"\ndiscovery = "auto"\n')
manual = prof('\n[skills]\ndiscovery = "manual"\n')
assert rules.declared_depth(none)[0] == rules.DEPTH_FULL, rules.declared_depth(none)
assert rules.declared_depth(empty)[0] == rules.DEPTH_FULL, rules.declared_depth(empty)
# Пустая секция и её отсутствие дают одну глубину, а значат разное: у первого
# ось разобрана, у второго до неё не дошли, и сказать об этом надо по-разному.
assert rules.declared_depth(none)[1] != rules.declared_depth(empty)[1]
assert rules.declared_depth(auto)[0] == rules.DEPTH_CORE, rules.declared_depth(auto)
assert rules.declared_depth(manual)[0] == rules.DEPTH_POINTERS, rules.declared_depth(manual)

dk, proj = os.path.join(work, "devkit"), os.path.join(work, "proj")
os.makedirs(proj)
put(os.path.join(dk, "RULES.md"), "# правила\n")
put(os.path.join(dk, "RULES.board.md"), "# правила доски\n")
put(os.path.join(dk, "skills", "board-test", "SKILL.md"),
    "---\nname: board-test\ndescription: Процедура доски. Звать, когда трогают задачу.\n---\n\n# Доска\n")

# Ядра ещё нет: объявленная глубина остаётся обещанием, доезжает полный текст,
# и признака в маркере нет. Указателям ядро нужно не меньше, чем ядерной ветке:
# рядом с полным текстом таблица указателей стала бы второй копией тех же
# процедур, а признак назвал бы полный текст ядром с указателями.
assert rules.actual_depth(dk, proj, True, rules.DEPTH_CORE) == rules.DEPTH_FULL
assert rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS) == rules.DEPTH_FULL
early = rules.thin_text(manual, proj, dk, True, False,
                        rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS))
assert early.startswith("<!-- devkit:generated body="), early
assert "Процедуры devkit" not in early, early
assert "@../devkit/RULES.md\n" in early, early
put(os.path.join(dk, "RULES.core.md"), "# ядро\n")
# Ядро нарезано наполовину: пока полон хоть один текст, глубины нет ни у одной
# из двух неполных веток.
assert rules.actual_depth(dk, proj, True, rules.DEPTH_CORE) == rules.DEPTH_FULL
assert rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS) == rules.DEPTH_FULL
put(os.path.join(dk, "RULES.board.core.md"), "# ядро доски\n")
assert rules.actual_depth(dk, proj, True, rules.DEPTH_CORE) == rules.DEPTH_CORE
assert rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS) == rules.DEPTH_POINTERS
# При вклейке тонкий файл правил не везёт вовсе, и глубина не про него.
assert rules.thin_text(auto, proj, dk, True, True, rules.DEPTH_CORE).startswith(
    "<!-- devkit:generated body="), rules.thin_text(auto, proj, dk, True, True, rules.DEPTH_CORE)

thin = rules.thin_text(auto, proj, dk, True, False, rules.DEPTH_CORE)
assert thin.startswith("<!-- devkit:generated depth=core body="), thin
assert "@../devkit/RULES.core.md\n" in thin, thin
assert "@../devkit/RULES.board.core.md\n" in thin, thin
assert "@../devkit/RULES.md\n" not in thin, thin

# Указатели: строка на скилл с описанием и путём, по которому его читать.
ptr = rules.pointers_text(dk, proj)
assert "board-test" in ptr, ptr
assert "`../devkit/skills/board-test/SKILL.md`" in ptr, ptr
assert "Звать, когда трогают задачу." in ptr, ptr
thin = rules.thin_text(manual, proj, dk, True, False, rules.DEPTH_POINTERS)
assert thin.startswith("<!-- devkit:generated depth=pointers body="), thin
assert "board-test" in thin, thin
stamp, body = rules.generated_parts(thin)
assert stamp == rules.digest(body), thin
# Хеш маркера с глубиной сходится с телом, а тронутый руками файл расходится:
# без этого правку молча перетёрли бы при первой же перегенерации.
stamp, body = rules.generated_parts(thin + "своя строка\n")
assert stamp is not None and stamp != rules.digest(body)

# Вклейка одна на всех, и глубина у неё самая полная из запрошенных.
assert rules.embed_depth([rules.DEPTH_CORE, rules.DEPTH_FULL]) == rules.DEPTH_FULL
assert rules.embed_depth([rules.DEPTH_CORE, rules.DEPTH_POINTERS]) == rules.DEPTH_POINTERS
assert rules.embed_depth([]) == rules.DEPTH_FULL

# Эталон сегодняшней раскладки, снятый с генератора до этой задачи: пока ядро
# не названо, активный профиль обязан давать те же байты.
cc = harness.parse("claude-code.toml",
                   open(os.path.join(dkroot, "harness", "claude-code.toml"), encoding="utf-8").read())
declared = rules.declared_depth(cc)[0]
assert declared == rules.DEPTH_CORE, declared
fact = rules.actual_depth(dkroot, os.path.join(work, "nowhere"), True, declared)
assert fact == rules.DEPTH_FULL, fact
for name, board in (("thin-board.expected", True), ("thin-noboard.expected", False)):
    want = open(os.path.join(dkroot, "devkitctl", "testdata", name), encoding="utf-8").read()
    got = rules.thin_text(cc, "/nowhere/proj", "/nowhere/devkit", board, False, fact)
    assert got == want, "%s: жду %r, вижу %r" % (name, want, got)
EOF

# Разрез ядра: за резидентную часть правил платит каждый запрос каждой сессии,
# поэтому бюджет тут не пожелание, а проверка. Гоняется на настоящих файлах
# devkit, а не на копии: мерить надо тот текст, который реально доедет.
dkreal=$(CDPATH= cd -- "$here/.." && pwd)
plen() { python3 -c 'import sys;print(len(open(sys.argv[1],encoding="utf-8").read()))' "$1"; }
for f in RULES.core.md RULES.board.core.md; do
    [ -f "$dkreal/$f" ] || fail "нет $f: резидентного ядра правил не нарезано"
done
[ "$(plen "$dkreal/RULES.core.md")" -le 5500 ] ||
    fail "ядро правил длиннее бюджета 5500 символов: $(plen "$dkreal/RULES.core.md")"
[ "$(plen "$dkreal/RULES.board.core.md")" -le 1500 ] ||
    fail "ядро правил доски длиннее бюджета 1500 символов: $(plen "$dkreal/RULES.board.core.md")"

python3 - "$dkreal" <<'EOF' || fail "ядро дублирует полный текст правил"
import sys
from pathlib import Path

# Ядро и полный текст никогда не лежат в контексте вместе, но переписанный под
# одну строку пункт легко подменить копипастой из полного текста, и тогда ядро
# растёт молча. Сравниваются предложения, а не строки: перенос строки у этих
# файлов разный, и построчное сравнение пропустило бы копию.
dk = Path(sys.argv[1])
bad = []
for core, full in (("RULES.core.md", "RULES.md"),
                   ("RULES.board.core.md", "RULES.board.md")):
    whole = " ".join((dk / full).read_text(encoding="utf-8").split())
    for sent in " ".join((dk / core).read_text(encoding="utf-8").split()).split(". "):
        if len(sent) >= 60 and sent in whole:
            bad.append("%s -> %s: %s" % (core, full, sent[:80]))
    if "`%s`" % full not in (dk / core).read_text(encoding="utf-8"):
        bad.append("%s не называет %s: разбор пункта искать негде" % (core, full))
if bad:
    print("\n".join(bad))
    sys.exit(1)
EOF

python3 - "$dkreal/devkitctl" "$tmp/cut" "$dkreal" <<'EOF' || fail "тонкий файл собран не под нарезанное ядро"
import os
import sys

sys.path.insert(0, sys.argv[1])
work, dkroot = sys.argv[2], sys.argv[3]
import harness
import rules

cc = harness.parse("claude-code.toml",
                   open(os.path.join(dkroot, "harness", "claude-code.toml"), encoding="utf-8").read())
proj = os.path.join(work, "proj")
os.makedirs(os.path.join(proj, "docs"))
for board in (True, False):
    tasks = os.path.join(proj, "docs", "TASKS.md")
    if board:
        open(tasks, "w").close()
    elif os.path.exists(tasks):
        os.remove(tasks)
    fact = rules.actual_depth(dkroot, proj, board, rules.declared_depth(cc)[0])
    assert fact == rules.DEPTH_CORE, "ядро нарезано, а доехала глубина %s" % fact
    thin = rules.thin_text(cc, proj, dkroot, board, False, fact)
    assert "RULES.core.md\n" in thin, thin
    assert "RULES.md\n" not in thin, "в тонкий файл уехал полный текст правил: %s" % thin
    assert ("RULES.board.core.md\n" in thin) == board, thin
    assert "RULES.board.md\n" not in thin, "в тонкий файл уехал полный текст правил доски: %s" % thin
EOF

# Файлы правил: рукописный AGENTS.md источник, тонкие файлы харнесов генерятся.
# Гоняется на своём проекте, чтобы правки --fix не мешали прежним шагам.
rproj="$tmp/rproj"
mkdir -p "$rproj"
git init -q "$rproj"
git -C "$rproj" config user.name t
git -C "$rproj" config user.email t@t
HOME="$home" PATH="$cleanpath" python3 "$dkctl" new --prefix RP -C "$rproj" >/dev/null 2>&1
docr() { HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor "$@" -C "$rproj" 2>&1; }
# taskctl в PATH заглушка, доски она не заводит: кладём её руками, и тонкий файл
# от этого устаревает, ему не хватает правил доски.
mkdir -p "$rproj/docs"
printf '# Задачи\n' > "$rproj/docs/TASKS.md"
out=$(docr)
echo "$out" | grep -q 'CLAUDE.md устарел' || fail "появление доски не сделало тонкий файл устаревшим: $out"
grep -q 'RULES.board.md' "$rproj/CLAUDE.md" && fail "doctor без --fix сам перегенерил тонкий файл"
out=$(docr --fix)
echo "$out" | grep -q 'починено: CLAUDE.md перегенерирован' || fail "--fix не перегенерил устаревший файл: $out"
grep -q '^@../devkit/RULES.board.md$' "$rproj/CLAUDE.md" || fail "в перегенерённом файле нет правил доски"
out=$(docr)
echo "$out" | grep -qE 'CLAUDE.md|AGENTS.md' && fail "после перегенерации остались находки по правилам: $out"

# Правка руками: генератор файл не перетирает даже под --fix, а зовёт перенести
# её в AGENTS.md. Иначе чужой текст пропал бы молча.
printf 'своя строка\n' >> "$rproj/CLAUDE.md"
out=$(docr --fix)
echo "$out" | grep -q 'CLAUDE.md правлен руками' || fail "нет находки про правленый руками тонкий файл: $out"
grep -q 'своя строка' "$rproj/CLAUDE.md" || fail "--fix затёр правку в тонком файле"
rm -f "$rproj/CLAUDE.md"
out=$(docr --fix)
echo "$out" | grep -q 'починено: CLAUDE.md сгенерирован' || fail "--fix не сгенерил пропавший тонкий файл: $out"

# Проект, подключённый до переезда на AGENTS.md: рукописный CLAUDE.md остаётся
# нетронутым, а доктор подсказывает переезд. Автоматом его не делают: в живых
# CLAUDE.md бывают локальные импорты, автоматике их не разобрать.
hproj="$tmp/hproj"
mkdir -p "$hproj"
git init -q "$hproj"
git -C "$hproj" config user.name t
git -C "$hproj" config user.email t@t
printf '# Старый проект\n\n@../devkit/RULES.md\n' > "$hproj/CLAUDE.md"
doch() { HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor "$@" -C "$hproj" 2>&1; }
out=$(doch --fix)
echo "$out" | grep -q 'git mv CLAUDE.md AGENTS.md' || fail "нет подсказки про переезд на AGENTS.md: $out"
[ -f "$hproj/AGENTS.md" ] && fail "--fix перенёс рукописный файл сам"
grep -q '@../devkit/RULES.md' "$hproj/CLAUDE.md" || fail "--fix тронул рукописный CLAUDE.md"
mv "$hproj/CLAUDE.md" "$hproj/AGENTS.md"
out=$(doch)
echo "$out" | grep -q 'AGENTS.md:3: строка импорта @../devkit/RULES.md' ||
    fail "импорт, переехавший в AGENTS.md, не назван находкой: $out"
out=$(doch --fix)
echo "$out" | grep -q 'починено: CLAUDE.md сгенерирован' || fail "--fix не сгенерил тонкий файл после переезда: $out"
printf '# Старый проект\n' > "$hproj/AGENTS.md"
out=$(doch)
echo "$out" | grep -q 'строка импорта' && fail "находка про импорт осталась после его удаления: $out"
echo "$out" | grep -qE 'CLAUDE.md|AGENTS.md' && fail "после переезда остались находки по правилам: $out"

# embed-инструмент: правила вклеиваются в AGENTS.md единственной копией, а
# тонкие файлы на devkit больше не ходят, иначе правила приехали бы дважды.
cat > "$dk/harness/embed-tool.toml" <<'EOF'
# Подставной профиль для самопроверки: импортов инструмент не понимает, правила
# доезжают до него только вклейкой, остальных осей у него нет.
[detect]

[rules]
mode = "embed"

[delegate]
mode = "none"

[hooks]

[quota]
EOF
mkdir -p "$home/.devkit"
printf 'enabled = ["claude-code", "embed-tool"]\n' > "$home/.devkit/harness.local"
out=$(docr)
echo "$out" | grep -q 'нет вклейки правил' || fail "нет находки про недостающую вклейку: $out"
out=$(docr --fix)
echo "$out" | grep -q 'починено: вклейка правил добавлена в AGENTS.md' || fail "--fix не вклеил правила: $out"
echo "$out" | grep -q 'починено: CLAUDE.md перегенерирован' || fail "тонкий файл не перегенерён под вклейку: $out"
grep -q '^<!-- devkit:rules begin src=[0-9a-f]\{12\} body=[0-9a-f]\{12\} -->$' "$rproj/AGENTS.md" ||
    fail "у вклейки нет маркера начала с двумя хешами"
grep -q '^<!-- devkit:rules end -->$' "$rproj/AGENTS.md" || fail "у вклейки нет маркера конца"
grep -q 'Правила работы с ассистентом' "$rproj/AGENTS.md" || fail "во вклейке нет текста RULES.md"
grep -q 'Правила проектов с доской' "$rproj/AGENTS.md" || fail "во вклейке нет правил доски"
[ "$(grep -c '^@' "$rproj/CLAUDE.md")" -eq 1 ] || fail "при вклейке тонкий файл всё ещё ходит в devkit"
out=$(docr)
echo "$out" | grep -qE 'вклейка|CLAUDE.md' && fail "после вклейки остались находки по правилам: $out"
cp "$rproj/AGENTS.md" "$tmp/agents.embed"

# Тронутая руками вклейка: находка, текст не перетирается.
python3 - "$rproj/AGENTS.md" <<'EOF'
import sys
p = sys.argv[1]
t = open(p, encoding="utf-8").read().replace("<!-- devkit:rules end -->",
                                             "своя правка\n<!-- devkit:rules end -->")
open(p, "w", encoding="utf-8").write(t)
EOF
out=$(docr --fix)
echo "$out" | grep -q 'вклейку правил в AGENTS.md правили руками' || fail "тронутая вклейка не названа: $out"
grep -q 'своя правка' "$rproj/AGENTS.md" || fail "--fix затёр правку внутри маркеров"
cp "$tmp/agents.embed" "$rproj/AGENTS.md"

# Правила devkit обновились: src разошёлся, body цел, вклейка перегенерируется.
printf '\nновая строка правил доски\n' >> "$dk/RULES.board.md"
out=$(docr)
echo "$out" | grep -q 'вклейка правил в AGENTS.md протухла против devkit' || fail "протухшая вклейка не названа: $out"
out=$(docr --fix)
echo "$out" | grep -q 'починено: вклейка правил в AGENTS.md обновлена под devkit' ||
    fail "--fix не обновил протухшую вклейку: $out"
grep -q 'новая строка правил доски' "$rproj/AGENTS.md" || fail "обновлённая вклейка без новой строки правил"

# Ось скиллов у embed-инструмента: discovery = "manual" значит, что к ядру
# правил прикладывается таблица указателей на скиллы devkit, а глубина стоит
# признаком в маркере вклейки.
sed -e '$a\
\
[skills]\
discovery = "manual"' "$dk/harness/embed-tool.toml" > "$tmp/embed-tool.manual" &&
    cp "$tmp/embed-tool.manual" "$dk/harness/embed-tool.toml"
# Ядра ещё нет: указателям заменять нечего, рядом с полным текстом они были бы
# второй копией тех же процедур. Вклейка остаётся сегодняшней, до байта, и
# доктору чинить нечего.
cp "$rproj/AGENTS.md" "$tmp/agents.beforecore"
out=$(docr)
echo "$out" | grep -qE 'вклейка|CLAUDE.md' &&
    fail "manual без нарезанного ядра сделал вклейку устаревшей: $out"
diff -q "$tmp/agents.beforecore" "$rproj/AGENTS.md" >/dev/null ||
    fail "manual без нарезанного ядра тронул вклейку"
grep -q 'Процедуры devkit отдельными файлами' "$rproj/AGENTS.md" &&
    fail "указатели приехали раньше ядра, рядом с полным текстом правил"
grep -q 'devkit:rules begin depth=' "$rproj/AGENTS.md" &&
    fail "признак глубины уехал в маркер, хотя доехал полный текст: $(head -1 "$rproj/AGENTS.md")"

# Ядро нарезано: та же объявленная глубина теперь доезжает целиком, и смена
# источников видна как протухание.
printf '# Ядро правил\n\nстрока ядра\n' > "$dk/RULES.core.md"
printf '# Ядро правил доски\n\nстрока ядра доски\n' > "$dk/RULES.board.core.md"
out=$(docr)
echo "$out" | grep -q 'вклейка правил в AGENTS.md протухла против devkit' ||
    fail "нарезанное ядро не сделало вклейку протухшей: $out"
out=$(docr --fix)
echo "$out" | grep -q 'починено: вклейка правил в AGENTS.md обновлена под devkit' ||
    fail "--fix не переложил вклейку под указатели: $out"
grep -q '^<!-- devkit:rules begin depth=pointers src=[0-9a-f]\{12\} body=[0-9a-f]\{12\} -->$' "$rproj/AGENTS.md" ||
    fail "в маркере вклейки нет признака глубины: $(head -1 "$rproj/AGENTS.md")"
grep -q '^## Процедуры devkit отдельными файлами$' "$rproj/AGENTS.md" ||
    fail "во вклейке нет таблицы указателей на скиллы"
grep -q '`../devkit/skills/board-batch/SKILL.md`' "$rproj/AGENTS.md" ||
    fail "в таблице указателей нет пути до скилла: $(grep -n 'board-batch' "$rproj/AGENTS.md")"
grep -q 'строка ядра доски' "$rproj/AGENTS.md" ||
    fail "указатели приехали вместо текста ядра, а не вместе с ним"
grep -q 'Правила работы с ассистентом' "$rproj/AGENTS.md" &&
    fail "под указателями остался полный текст правил вместо ядра"
out=$(docr)
echo "$out" | grep -qE 'вклейка|CLAUDE.md' && fail "после перекладки под указатели остались находки: $out"
# Ось убрана обратно, ядро тоже: указатели уходят, вклейка возвращается к
# полному тексту.
rm -f "$dk/RULES.core.md" "$dk/RULES.board.core.md"
grep -v 'discovery = "manual"' "$tmp/embed-tool.manual" | grep -v '^\[skills\]$' \
    > "$dk/harness/embed-tool.toml"
out=$(docr --fix)
echo "$out" | grep -q 'починено: вклейка правил в AGENTS.md обновлена под devkit' ||
    fail "--fix не вернул вклейку к полному тексту: $out"
grep -q 'Процедуры devkit отдельными файлами' "$rproj/AGENTS.md" &&
    fail "таблица указателей осталась после снятия оси скиллов"
grep -q '^<!-- devkit:rules begin src=[0-9a-f]\{12\} body=[0-9a-f]\{12\} -->$' "$rproj/AGENTS.md" ||
    fail "признак глубины остался в маркере при полной вклейке: $(head -1 "$rproj/AGENTS.md")"

# Копия текста правил в дереве должна быть одна.
cp "$rproj/AGENTS.md" "$rproj/docs/copy.md"
out=$(docr)
echo "$out" | grep -q 'вклейка правил лежит не в одном файле' || fail "две вклейки в дереве не названы: $out"
rm -f "$rproj/docs/copy.md"
# Маркеры в примере кода это документация, а не вторая копия.
printf 'пример:\n\n```\n<!-- devkit:rules begin src=aaaaaaaaaaaa body=bbbbbbbbbbbb -->\nтекст\n<!-- devkit:rules end -->\n```\n' \
    > "$rproj/docs/sample.md"
out=$(docr)
echo "$out" | grep -q 'вклейка правил лежит не в одном файле' && fail "пример в блоке кода принят за вклейку: $out"
rm -f "$rproj/docs/sample.md"

# Последний embed-инструмент выключен: вклейка убирается, импорты возвращаются.
# Это единственный случай, когда --fix удаляет, и только целый блок со своим body.
printf 'enabled = ["claude-code"]\n' > "$home/.devkit/harness.local"
out=$(docr --fix)
echo "$out" | grep -q 'починено: вклейка правил убрана из AGENTS.md' || fail "--fix не убрал ненужную вклейку: $out"
echo "$out" | grep -q 'починено: CLAUDE.md перегенерирован' || fail "тонкий файл не вернул импорты devkit: $out"
grep -q 'devkit:rules' "$rproj/AGENTS.md" && fail "в AGENTS.md остались маркеры вклейки"
grep -q '^@../devkit/RULES.md$' "$rproj/CLAUDE.md" || fail "в тонком файле не вернулись правила devkit"
rm -f "$home/.devkit/harness.local"

# Проектный слой только сужает машинный список: профиль embed-tool на месте, но
# в машинном слое его нет, и сужением его не включить. Остаться совсем без
# харнесов это находка, а не тишина: правила иначе молча перестали бы доезжать.
mkdir -p "$rproj/.devkit"
printf 'enabled = ["embed-tool"]\ndefault = "embed-tool"\n\n[embed-tool]\npro = "x"\n' \
    > "$rproj/.devkit/harness.local"
out=$(docr)
echo "$out" | grep -q 'включённых харнесов нет' || fail "сужение до невключённого харнеса прошло молча: $out"
# Тексты те же, что у Go-стороны (agentctl/harness.go, narrowByProject): один и
# тот же проектный конфиг обе реализации разбирают одинаково и говорят о нём
# одно и то же, иначе сужение чинилось бы по разным подсказкам.
echo "$out" | grep -q 'embed-tool сужением не включить, в машинном слое его нет, пропущен' ||
    fail "имя вне машинного слоя отброшено молча: $out"
echo "$out" | grep -q 'claude-code сужен проектным слоем' || fail "про суженный харнес доктор молчит: $out"
echo "$out" | grep -q 'ключ default проектному слою не положен, понимается только enabled' ||
    fail "лишний ключ проектного слоя прошёл молча: $out"
echo "$out" | grep -q 'секция \[embed-tool\] проектному слою не положена, маппинг ярусов машинный' ||
    fail "секция проектного слоя прошла молча: $out"
rm -f "$rproj/.devkit/harness.local"
rm -f "$dk/harness/embed-tool.toml"

# doctor: доска без taskctl в PATH это находка (PATH обрезан до системного).
printf '# Задачи\n' > "$proj/docs/TASKS.md"
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$proj" 2>&1)
echo "$out" | grep -q 'taskctl не в PATH' || fail "нет находки про taskctl: $out"

# Отдельный проект с доской: new заводит болванку выката и гитигнорит её.
bproj="$tmp/bproj"
mkdir -p "$bproj"
git init -q "$bproj"
git -C "$bproj" config user.name t
git -C "$bproj" config user.email t@t
HOME="$home" python3 "$dkctl" new --prefix BP -C "$bproj" >/dev/null 2>&1
[ -f "$bproj/.devkit/deploy.local" ] || fail "new не завёл .devkit/deploy.local"
grep -q '^autonomous = false' "$bproj/.devkit/deploy.local" || fail "в болванке нет autonomous"
grep -q '^test =$' "$bproj/.devkit/deploy.local" || fail "в болванке нет пустого ключа test"
git -C "$bproj" check-ignore -q .devkit/deploy.local || fail ".devkit/deploy.local не гитигнорнут"

# Журнал запусков: new записал свою строку в .devkit/log, файл гитигнорнут.
tab=$(printf '\t')
[ -f "$bproj/.devkit/log" ] || fail "new не записал журнал запусков"
grep -q "devkitctl${tab}new${tab}0" "$bproj/.devkit/log" || fail "в журнале нет строки про new"
git -C "$bproj" check-ignore -q .devkit/log || fail ".devkit/log не гитигнорнут"

# doctor: пустые deploy= и test= это находки, заполненный и гитигнорнутый файл чист.
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'пустой deploy=' || fail "нет находки про пустую команду выката: $out"
echo "$out" | grep -q 'пустой test=' || fail "нет находки про пустую команду тестов: $out"
# Команда выката вписана, а тестов нет: находка про test остаётся одна.
printf 'deploy = make deploy\nautonomous = false\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'пустой deploy=' && fail "находка про пустой deploy осталась при вписанной команде: $out"
echo "$out" | grep -q 'пустой test=' || fail "находка про пустой test пропала вместе с deploy: $out"
printf 'deploy = make deploy\ntest = go test ./...\nautonomous = false\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'deploy' && fail "заполненная обвязка выката всё ещё в находках: $out"

# doctor: autonomous=true с пустым deploy= это специальная находка.
printf 'autonomous = true\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'autonomous = true при пустом deploy=' || fail "нет находки про autonomous=true без команды: $out"
echo "$out" | grep -q 'shipctl нечего выкатывать' && fail "старая находка дублирует новую при autonomous=true: $out"
# autonomous=false с пустым deploy= это старая находка, без новой.
printf 'autonomous = false\n' > "$bproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$bproj" 2>&1)
echo "$out" | grep -q 'autonomous = true при пустом deploy=' && fail "новая находка не должна быть для autonomous=false: $out"
echo "$out" | grep -q 'пустой deploy=' || fail "старая находка про пустой deploy должна остаться: $out"

# doctor: команда есть, но файл не гитигнорнут это находка.
nproj="$tmp/nproj"
mkdir -p "$nproj/.devkit"
git init -q "$nproj"
git -C "$nproj" config user.name t
git -C "$nproj" config user.email t@t
HOME="$home" python3 "$dkctl" new --no-board -C "$nproj" >/dev/null 2>&1
mkdir -p "$nproj/docs"
printf '# Задачи\n' > "$nproj/docs/TASKS.md"
printf 'deploy = make deploy\nautonomous = false\n' > "$nproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$sys" python3 "$dkctl" doctor -C "$nproj" 2>&1)
echo "$out" | grep -q 'не гитигнорнут' || fail "нет находки про негитигнорнутый конфиг: $out"

# doctor --fix доводит обвязку проекта, подключённого до появления выката:
# заводит deploy.local с гитигнором и возвращает отвязанные хуки.
fproj="$tmp/fproj"
mkdir -p "$fproj"
git init -q "$fproj"
git -C "$fproj" config user.name t
git -C "$fproj" config user.email t@t
HOME="$home" python3 "$dkctl" new --prefix FP -C "$fproj" >/dev/null 2>&1
rm -f "$fproj/.devkit/deploy.local"
git -C "$fproj" config --unset core.hooksPath
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'починено' || fail "doctor --fix ничего не починил: $out"
[ -f "$fproj/.devkit/deploy.local" ] || fail "doctor --fix не завёл deploy.local"
git -C "$fproj" check-ignore -q .devkit/deploy.local || fail "doctor --fix не гитигнорил deploy.local"
[ -n "$(git -C "$fproj" config core.hooksPath)" ] || fail "doctor --fix не подключил хуки"
# Пустой deploy= остаётся находкой: команду выката --fix не выдумывает.
echo "$out" | grep -q 'пустой deploy=' || fail "doctor --fix должен просить вписать команду: $out"

# Повторный --fix уже ничего не меняет (идемпотентность).
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный doctor --fix не должен ничего менять: $out"

# Заполненные команды: находки по выкату уходят.
printf 'deploy = make deploy\ntest = go test ./...\nautonomous = false\n' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$fproj" 2>&1)
echo "$out" | grep -q 'deploy' && fail "заполненная обвязка выката всё ещё в находках: $out"

# doctor --fix дописывает недостающий ключ в уже готовый deploy.local (DK-075):
# файл проекта, подключённого до появления test= (DK-053), содержит только
# deploy и autonomous. --fix дописывает test= в конец, своя шапка, значения
# deploy и autonomous и их порядок не тронуты.
printf '# моя шапка, не трогать\ndeploy = ssh prod deploy.sh\nautonomous = true\n' \
    > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан недостающий ключ test' || fail "doctor --fix не отчитался о дописанном test: $out"
depfile=$(cat "$fproj/.devkit/deploy.local")
echo "$depfile" | grep -q '^# моя шапка, не трогать$' || fail "doctor --fix стёр шапку файла: $depfile"
echo "$depfile" | grep -q '^deploy = ssh prod deploy.sh$' || fail "doctor --fix затронул значение deploy: $depfile"
echo "$depfile" | grep -q '^autonomous = true$' || fail "doctor --fix затронул значение autonomous: $depfile"
echo "$depfile" | grep -q '^test =$' || fail "дописанный test не пустой: $depfile"
echo "$depfile" | grep -q '# test это команда тестов' || fail "дописанный test без своего комментария: $depfile"
# test дописан в хвост, после deploy и autonomous, не между ними.
last_key=$(echo "$depfile" | grep -E '^[a-z]+ ?=' | tail -1 | cut -d= -f1 | tr -d ' ')
[ "$last_key" = test ] || fail "test дописан не в конец файла: $depfile"
# Находка про пустой test= остаётся: --fix дописывает место под ключ, а не
# выдумывает команду.
echo "$out" | grep -q 'пустой test=' || fail "находка про пустой test после дописывания пропала: $out"

# Повторный --fix ключ, который уже дописан, второй раз не трогает.
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан' && fail "повторный doctor --fix переписал уже дописанный ключ: $out"

# Отсутствующий ключ и присутствующий пустой это разные случаи: пустой test=
# --fix не трогает, дописывать там уже нечего.
printf '# шапка\ndeploy = ssh prod deploy.sh\ntest = \nautonomous = true\n' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан' && fail "doctor --fix дописал ключ, который уже есть пустым: $out"
echo "$out" | grep -q 'пустой test=' || fail "находка про пустой test пропала при уже существующем пустом ключе: $out"

# Несколько недостающих ключей сразу: дописываются оба (deploy и test),
# найденный autonomous не трогается, а связанная с ним находка остаётся.
printf 'autonomous = true\n' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
depfile=$(cat "$fproj/.devkit/deploy.local")
echo "$depfile" | grep -q '^deploy =$' || fail "doctor --fix не дописал ключ deploy: $depfile"
echo "$depfile" | grep -q '^test =$' || fail "doctor --fix не дописал ключ test: $depfile"
echo "$depfile" | grep -q '^autonomous = true$' || fail "doctor --fix затронул autonomous: $depfile"
echo "$out" | grep -q 'autonomous = true при пустом deploy=' ||
    fail "находка про autonomous=true при пустом deploy пропала после дописывания: $out"

# Закомментированный ключ это не имеющийся ключ: "# test = ..." выключен
# руками и для --fix значит то же, что полное отсутствие test.
printf 'deploy = x\n# test = отключено руками, до времени\nautonomous = true\n' \
    > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан недостающий ключ test' ||
    fail "doctor --fix не считает закомментированный test отсутствующим: $out"
grep -q '^test =$' "$fproj/.devkit/deploy.local" ||
    fail "doctor --fix не дописал настоящий test поверх закомментированного: $(cat "$fproj/.devkit/deploy.local")"

# Обрубленная строка ключа (без "=" вообще) это тоже не имеющийся ключ, как и
# закомментированная: без "=" partition отдал бы всю строку в ключ, и мусор
# засчитался бы present'ом. Мусорная строка не удаляется (--fix только
# дописывает, чужой текст не трогает), а рабочая строка появляется рядом.
printf 'deploy = x\ntest\nautonomous = true\n' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан недостающий ключ test' ||
    fail "doctor --fix не считает обрубленную строку test отсутствующим ключом: $out"
depfile=$(cat "$fproj/.devkit/deploy.local")
echo "$depfile" | grep -qx 'test' ||
    fail "doctor --fix стёр обрубленную строку test, хотя обязан только дописывать: $depfile"
echo "$depfile" | grep -q '^test =$' ||
    fail "doctor --fix не дописал настоящий test рядом с обрубленной строкой: $depfile"

# Уже полный файл без завершающего перевода строки --fix не трогает: нечего
# дописывать значит нечего и писать, даже перевод строки в хвост.
printf 'deploy = x\ntest = y\nautonomous = true' > "$fproj/.devkit/deploy.local"
before=$(od -c "$fproj/.devkit/deploy.local")
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан' && fail "doctor --fix дописал в уже полный файл: $out"
after=$(od -c "$fproj/.devkit/deploy.local")
[ "$before" = "$after" ] || fail "doctor --fix изменил байты уже полного файла: было [$before] стало [$after]"

# Файл без завершающего перевода строки, в котором ключа не хватает: перед
# дописанным комментарием обязан появиться перенос строки, а не склейка с
# последней имеющейся строкой.
printf 'deploy = x\nautonomous = true' > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан недостающий ключ test' ||
    fail "doctor --fix не дописал test в файл без завершающего перевода строки: $out"
depfile=$(cat "$fproj/.devkit/deploy.local")
echo "$depfile" | grep -q '^autonomous = true$' ||
    fail "дописывание в файл без \\n на конце склеило последнюю строку с комментарием: $depfile"
echo "$depfile" | grep -q '^test =$' || fail "test не дописан отдельной строкой: $depfile"

# Действительно пустой (0 байт) deploy.local: дописываются сразу все три
# ключа, а результат начинается прямо с комментария, без лишнего переноса
# строки перед ним (в пустом файле text.endswith("\n") тоже ложно, разделитель
# должен ставиться по text, а не по одному этому условию).
: > "$fproj/.devkit/deploy.local"
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor --fix -C "$fproj" 2>&1)
echo "$out" | grep -q 'дописан недостающий ключ deploy' || fail "doctor --fix не дописал deploy в пустой файл: $out"
echo "$out" | grep -q 'дописан недостающий ключ test' || fail "doctor --fix не дописал test в пустой файл: $out"
echo "$out" | grep -q 'дописан недостающий ключ autonomous' || fail "doctor --fix не дописал autonomous в пустой файл: $out"
first_bytes=$(head -c1 "$fproj/.devkit/deploy.local" | od -An -c | tr -d ' ')
[ "$first_bytes" = "#" ] || fail "дописывание в пустой файл начинается не с комментария, а с [$first_bytes]"
# Дописанный файл читается read_deploy как обычная пустая болванка.
out=$(HOME="$home" PATH="$cleanpath" python3 "$dkctl" doctor -C "$fproj" 2>&1)
echo "$out" | grep -q 'пустой deploy=' || fail "после дописывания в пустой файл нет находки про пустой deploy=: $out"
echo "$out" | grep -q 'пустой test=' || fail "после дописывания в пустой файл нет находки про пустой test=: $out"

# Машинный контур гоняется на отдельном проекте, чтобы правки --fix не мешали
# прежним шагам.
mproj="$tmp/mproj"
mkdir -p "$mproj"
git init -q "$mproj"
git -C "$mproj" config user.name t
git -C "$mproj" config user.email t@t

# Машинный контур на пустой машине: бинарей нет, определений исполнителей нет,
# tmux нет, снимка квоты нет. Сборку изображает заглушка go, гонять настоящую
# в самопроверке незачем. Соседний markdown без frontmatter в agents/ имитирует
# README: раскладка берёт директорию целиком и обязана отличать определение от
# постороннего файла, иначе тот уедет на машину как агент.
printf '# agents\n\nПроза, не определение.\n' > "$dk/agents/README.md"
# То же со скиллами: скилл это директория с SKILL.md, соседняя проза в skills/
# на машину уезжать не должна.
printf '# skills\n\nПроза, не скилл.\n' > "$dk/skills/README.md"
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
echo "$out" | grep -q 'нет скилла .*board-batch/SKILL.md' || fail "нет находки про скилл: $out"
echo "$out" | grep -q 'tmux не в PATH' || fail "нет находки про tmux: $out"
echo "$out" | grep -q 'нет снимка квоты в' || fail "нет находки про снимок квоты: $out"
echo "$out" | grep -q 'SessionStart-хук' || fail "нет находки про хук освежения квоты на пустой машине: $out"
[ -f "$mhome/go/bin/agentctl" ] && fail "doctor без --fix собрал бинарь"
# Помета «машина» отделяет машинные находки от проектных, на неё опирается и
# дока, и сценарий проверки.
echo "$out" | grep -q '^машина: tmux не в PATH' || fail "у машинной находки нет пометы «машина»: $out"
echo "$out" | grep -q '^нет AGENTS.md' || fail "проектная находка получила помету «машина»: $out"

# --fix собирает бинари и раскладывает определения, а неоднозначное (tmux,
# снимок квоты) оставляет находкой с командой.
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
for t in taskctl shipctl agentctl regcheck; do
    [ -x "$mhome/go/bin/$t" ] || fail "doctor --fix не собрал $t: $out"
done
[ -f "$mhome/.claude/agents/exec-medium.md" ] || fail "doctor --fix не разложил определения исполнителей: $out"
# Роли ревьювера в наборе появились позже исполнителей, и раскладка обязана
# брать директорию целиком, а не один префикс.
[ -f "$mhome/.claude/agents/review-high.md" ] || fail "doctor --fix не разложил определения ревьюверов: $out"
[ -f "$mhome/.claude/agents/README.md" ] && fail "--fix положил на машину markdown без frontmatter: $out"
echo "$out" | grep -q 'README.md положено' && fail "--fix отчитался о README как об определении агента: $out"
# Скиллы едут тем же каналом: директория с SKILL.md раскладывается, соседняя
# проза остаётся в devkit.
[ -f "$mhome/.claude/skills/board-batch/SKILL.md" ] || fail "doctor --fix не разложил скилл: $out"
[ -f "$mhome/.claude/skills/README.md" ] && fail "--fix положил на машину markdown из skills/ как скилл: $out"
echo "$out" | grep -q 'tmux не в PATH' || fail "--fix не ставит tmux, находка должна остаться: $out"
echo "$out" | grep -q 'agentctl quota refresh' || fail "--fix не снимает квоту, находка должна остаться: $out"

# Повторный --fix по машинному контуру уже ничего не чинит.
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный --fix по машинному контуру не должен ничего менять: $out"

# Определение, разошедшееся на машине (правка руками или отставшая копия):
# plain doctor называет находку с командой cp, файл не трогает.
printf '\nсвоя строка\n' >> "$mhome/.claude/agents/exec-low.md"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'exec-low.md разошлось' || fail "нет находки про разошедшееся определение: $out"
grep -q 'своя строка' "$mhome/.claude/agents/exec-low.md" || fail "doctor без --fix тронул определение"

# devkit источник правды для промптов: --fix перекладывает разошедшееся
# определение и называет в отчёте, что переложил, а не затирает молча.
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено:.*exec-low.md разошлось' || fail "--fix не отчитался о перекладке разошедшегося определения: $out"
grep -q 'своя строка' "$mhome/.claude/agents/exec-low.md" && fail "--fix не переложил разошедшееся определение: $out"

# Повторный --fix уже не находит расхождения: переложенное совпало с devkit.
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный --fix после перекладки не должен ничего менять: $out"

# Скилл, разошедшийся на машине: без --fix находка и файл не тронут, с --fix
# перекладка из devkit с отчётом, дальше опять тихо.
printf '\nсвоя строка\n' >> "$mhome/.claude/skills/board-batch/SKILL.md"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'скилл .*board-batch/SKILL.md разошёлся' || fail "нет находки про разошедшийся скилл: $out"
grep -q 'своя строка' "$mhome/.claude/skills/board-batch/SKILL.md" || fail "doctor без --fix тронул скилл"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено:.*board-batch/SKILL.md разошёлся' || fail "--fix не отчитался о перекладке скилла: $out"
grep -q 'своя строка' "$mhome/.claude/skills/board-batch/SKILL.md" && fail "--fix не переложил разошедшийся скилл: $out"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено' && fail "повторный --fix после перекладки скилла не должен ничего менять: $out"

# Снимок квоты. Возраст берётся из строки taken, порог 45 минут (тот же, что у
# корректора pick), поэтому проверка идёт по обе стороны границы, а не «2020 год
# против сейчас».
mkdir -p "$mhome/.devkit/quota"
snap() { printf 'taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n' "$1" > "$mhome/.devkit/quota/claude-code.local"; }
taken_at() {
    python3 -c 'import datetime,sys;print((datetime.datetime.now()-datetime.timedelta(hours=float(sys.argv[1]))).strftime(sys.argv[2]))' "$1" "$2"
}
docm() { HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor -C "$mproj" 2>&1; }

snap "$(taken_at 1 '%Y-%m-%dT%H:%M')"
out=$(docm)
# Ищется именно строка про этот снимок: слово «протух» есть и в находке про
# неподключённый хук освежения, и по нему проверка проходила бы всегда.
echo "$out" | grep -q 'claude-code.local протух (возраст' || fail "снимок возрастом час не признан протухшим: $out"
snap "$(taken_at 0.5 '%Y-%m-%dT%H:%M')"
out=$(docm)
echo "$out" | grep -q 'claude-code.local' && fail "снимок возрастом полчаса попал в находки: $out"
# Остальные форматы момента снятия разбираются наравне с основным.
snap "$(taken_at 0.5 '%Y-%m-%dT%H:%M:%S')"
out=$(docm)
echo "$out" | grep -q 'claude-code.local' && fail "момент снятия с секундами не разобран: $out"
snap "$(taken_at 0.5 '%Y-%m-%d %H:%M')"
out=$(docm)
echo "$out" | grep -q 'claude-code.local' && fail "момент снятия через пробел не разобран: $out"
# Строка taken есть, но не разобрана (снимок заполняют и руками): возрасту
# верить нельзя, это находка.
snap вчера
out=$(docm)
echo "$out" | grep -q 'не разобран момент снятия' || fail "нет находки про неразобранный taken: $out"
# Строки taken нет вовсе: та же находка.
printf 'week_all = 40%% сброс 2030-01-01T00:00\n' > "$mhome/.devkit/quota/claude-code.local"
out=$(docm)
echo "$out" | grep -q 'не разобран момент снятия' || fail "нет находки про снимок без taken: $out"

# Переезд снимка в директорию. Одиночный quota.local это как было до DK-038, и
# читатель его ещё понимает, но чинит расхождение --fix, а не пользователь.
rm -f "$mhome/.devkit/quota/claude-code.local"
printf 'taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n' "$(taken_at 0.5 '%Y-%m-%dT%H:%M')" \
    > "$mhome/.devkit/quota.local"
out=$(docm)
echo "$out" | grep -q 'снимок квоты лежит по старому пути' || fail "нет находки про старый путь снимка: $out"
echo "$out" | grep -q 'нет снимка квоты' && fail "старый снимок посчитан отсутствующим: $out"
[ -f "$mhome/.devkit/quota.local" ] || fail "doctor без --fix тронул старый снимок"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено: снимок квоты переехал' || fail "--fix не переложил снимок: $out"
[ -f "$mhome/.devkit/quota/claude-code.local" ] || fail "--fix не положил снимок в директорию"
[ -f "$mhome/.devkit/quota.local" ] && fail "--fix оставил снимок и по старому пути"
out=$(docm)
echo "$out" | grep -q '\.devkit/quota' && fail "после переезда по снимку остались находки: $out"

# Оба файла сразу: читается новый, а про старый доктор говорит, но не удаляет
# его сам (правки --fix строго additive).
printf 'taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n' "$(taken_at 0.5 '%Y-%m-%dT%H:%M')" \
    > "$mhome/.devkit/quota.local"
out=$(HOME="$mhome" PATH="$mpath" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'лежит рядом с новым' || fail "нет находки про два снимка сразу: $out"
[ -f "$mhome/.devkit/quota.local" ] || fail "--fix удалил старый снимок, хотя правки additive"
rm -f "$mhome/.devkit/quota.local"

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

# Симлинк на тот же бинарь впереди в PATH (~/.local/bin поверх ~/go/bin) это
# не затенение: сверяется realpath, а не строка пути. Бинарь в GOBIN нарочно
# устаревший, иначе до сверки дело не дойдёт: свежий отсекается раньше, и
# строковое сравнение прошло бы незамеченным.
localbin="$tmp/localbin"
mkdir -p "$localbin"
ln -sf "$mhome/go/bin/agentctl" "$localbin/agentctl"
touch -t 200001010000 "$mhome/go/bin/agentctl"
out=$(HOME="$mhome" PATH="$gostub:$localbin:$mhome/go/bin:$sys" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'починено: agentctl собран' || fail "устаревший бинарь за симлинком не пересобран: $out"
echo "$out" | grep -q 'в PATH выигрывает' && fail "симлинк на тот же бинарь принят за чужую копию: $out"

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

# Нет go: находка с командой установки идёт в обоих режимах, а не только под
# --fix, иначе doctor советовал бы сборку командой, которой нет.
nghome="$tmp/nghome"
out=$(HOME="$nghome" PATH="$sys" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'go в PATH нет' || fail "нет находки про отсутствующий go под --fix: $out"
out=$(HOME="$nghome" PATH="$sys" python3 "$dkctl" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q 'go в PATH нет' || fail "нет находки про отсутствующий go без --fix: $out"

# Каталог сборки не в PATH: --fix соберёт, а пользоваться собранным нечем, и
# эта находка единственный сигнал. Ради такого случая задача и затевалась.
pphome="$tmp/pphome"
out=$(HOME="$pphome" PATH="$gostub:$sys" python3 "$dkctl" doctor --fix -C "$mproj" 2>&1)
[ -x "$pphome/go/bin/agentctl" ] || fail "--fix не собрал бинарь: $out"
echo "$out" | grep -q 'не в PATH: добавить директорию' || fail "нет находки про каталог сборки вне PATH: $out"

# weigh: живой замер резидента. Настоящих сессий самопроверка не поднимает,
# вместо claude в PATH лежит скрипт, который отдаёт usage по той раскладке,
# какую видит в HOME и в рабочей директории: ровно за неё платит и настоящий
# клиент. Пол 30 000, глобальная точка 620, определения агентов 830, скиллы 540,
# цепочка правил проекта 12 300, вместе целевой прогон это 44 290 из сценария.
wtmp="$tmp/weigh"
whome="$wtmp/home"
mkdir -p "$whome/.claude/agents" "$whome/.claude/skills"
cp "$dk/agents/"*.md "$whome/.claude/agents/"
cp -R "$dk/skills/"* "$whome/.claude/skills/"
printf '# Глобальные правила\n\n@~/.claude/CLAUDE_RULES.md\n' > "$whome/.claude/CLAUDE.md"
ln -sf "$dk/RULES.md" "$whome/.claude/CLAUDE_RULES.md"
printf '{"hooks": {"Stop": []}}\n' > "$whome/.claude/settings.json"
wproj="$wtmp/proj"
mkdir -p "$wproj"
git init -q "$wproj"
git -C "$wproj" config user.name t
git -C "$wproj" config user.email t@t
HOME="$whome" python3 "$dkctl" new --no-board -C "$wproj" >/dev/null || fail "weigh: проект не подключился"
cp "$wproj/CLAUDE.md" "$wtmp/thin.gen"
calls="$wtmp/calls"
: > "$calls"
wbin="$wtmp/bin"
mkdir -p "$wbin"
cat > "$wbin/claude" <<'EOF'
#!/bin/sh
echo "$PWD" >> "$WEIGH_CALLS"
t=30000
[ -f "$HOME/.claude/CLAUDE.md" ] && t=$((t + 620))
[ -f "$HOME/.claude/agents/exec-high.md" ] && t=$((t + 830))
[ -d "$HOME/.claude/skills/board-batch" ] && t=$((t + 540))
# Цепочка правил проекта стоит по каждому файлу, который тонкий импортирует, и
# только пока файл лежит в рабочей директории: импорт наружу настоящий клиент не
# разворачивает, и платить за него не за что. Нарезанное ядро дешевле полного
# текста, как дешевле оно и в жизни.
if [ -f "$PWD/CLAUDE.md" ]; then
    while IFS= read -r ln; do
        case "$ln" in @*) name=${ln#@} ;; *) continue ;; esac
        [ -n "$WEIGH_IMPORTS" ] && printf '%s\n' "$name" >> "$WEIGH_IMPORTS"
        case "$name" in */*) continue ;; esac
        [ -f "$PWD/$name" ] || continue
        case "$name" in
            AGENTS.md) t=$((t + 1400)) ;;
            RULES.core.md) t=$((t + 300)) ;;
            RULES.md) t=$((t + 10900)) ;;
            RULES.board.md) t=$((t + 7000)) ;;
        esac
    done < "$PWD/CLAUDE.md"
fi
# Вход гуляет от прогона к прогону, и разброс в выводе должен браться из
# настоящих чисел: каждый следующий вызов дороже предыдущего на свой квадрат.
# Считается вызов встроенным read: в PATH замера лежит только то, без чего не
# обойтись, и wc там нет.
n=0
while read -r _; do n=$((n + 1)); done < "$WEIGH_CALLS"
t=$((t + n * n * 10))
printf '{"type":"result","usage":{"input_tokens":4,"cache_creation_input_tokens":20000,"cache_read_input_tokens":%d}}\n' $((t - 20004))
EOF
chmod +x "$wbin/claude"
wpath="$wbin:$sys"

# Ожидаемые числа считаются в самой самопроверке: длина каждого кармана и сумма
# по ним. Разъедься список карманов с дизайном, и сумма разойдётся тоже.
listing() {
    python3 - "$@" <<'EOF'
import re, sys
n = 0
for p in sys.argv[1:]:
    parts = open(p, encoding="utf-8").read().split("---\n")
    head = parts[1] if len(parts) > 1 else ""
    for key in ("name", "description"):
        m = re.search(r"^%s: ?(.*)$" % key, head, re.M)
        n += len(m.group(1)) if m else 0
print(n)
EOF
}
fmt() { printf '%d' "$1" | sed -e :a -e 's/\(.*[0-9]\)\([0-9]\{3\}\)/\1 \2/;ta'; }
wagents=$(listing "$dk/agents/"*.md)
wskills=$(listing "$dk/skills/"*/SKILL.md)
wtotal=$(( $(plen "$whome/.claude/CLAUDE.md") + $(plen "$dk/RULES.md") \
    + $(plen "$wproj/AGENTS.md") + $(plen "$wproj/CLAUDE.md") + wagents + wskills ))

imports="$wtmp/imports"
: > "$imports"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" WEIGH_IMPORTS="$imports" \
    python3 "$dkctl" weigh -C "$wproj" --limit 20000 2>&1)
[ $? -eq 0 ] || fail "weigh не прошёл при потолке выше замера: $out"
# Файлы правил лежат в самой директории замера, и тонкий файл зовёт их голым
# именем: импорт наружу клиент не разворачивает, и правила до целевого прогона
# не доезжают вовсе.
grep -q '^RULES.md$' "$imports" || fail "тонкий файл замера зовёт правила не голым именем: $(cat "$imports")"
grep -q '^AGENTS.md$' "$imports" || fail "тонкий файл замера не зовёт AGENTS.md: $(cat "$imports")"
grep -q '/' "$imports" && fail "тонкий файл замера импортирует наружу: $(cat "$imports")"
# Базовый прогон идёт под слепком без раскладки devkit: пол и ничего сверх него.
# Пропусти сборщик слепка любую из трёх частей раскладки, и число вырастет.
echo "$out" | grep -q 'прогон 1: без раскладки 30 010, с раскладкой 44 330, разница 14 320' ||
    fail "первая пара прогонов посчитана не так: $out"
echo "$out" | grep -q 'прогон 3: .*разница 14 400' || fail "третья пара прогонов посчитана не так: $out"
echo "$out" | grep -q 'замер: 14 360 токенов, разброс 80 (0,56%)' || fail "замер или разброс не те: $out"
[ "$(wc -l < "$calls" | tr -d ' ')" -eq 6 ] || fail "три повтора это шесть прогонов, а вышло $(wc -l < "$calls")"
grep -qF "$wproj" "$calls" && fail "замер гонял прогон прямо в чекауте проекта: $(cat "$calls")"
# Карманы: файл считается один раз, даже когда приезжает двумя дорогами
# (глобальной точкой и импортом тонкого файла).
[ "$(echo "$out" | grep -c 'RULES.md')" -eq 1 ] || fail "RULES.md посчитан не одним карманом: $out"
echo "$out" | grep -q "глобальная точка ~/.claude/CLAUDE.md .*$(fmt "$(plen "$whome/.claude/CLAUDE.md")")" ||
    fail "карман глобальной точки посчитан не так: $out"
echo "$out" | grep -q "итого .*$(fmt "$wtotal")" || fail "итог по карманам не сошёлся ($wtotal): $out"
echo "$out" | grep -q "листинг определений agents/ .*$(fmt "$wagents")" || fail "листинг агентов не сошёлся: $out"
echo "$out" | grep -q "листинг скиллов .*$(fmt "$wskills")" || fail "листинг скиллов не сошёлся: $out"
# Коэффициент этого прогона и расхождение расчёта с замером считаются от тех же
# чисел: символы карманов делятся на замер, расчёт идёт по коэффициенту дизайна.
wcoef=$(python3 -c "print(('%.2f' % ($wtotal / 14360.0)).replace('.', ','))")
echo "$out" | grep -q "коэффициент этого прогона: $wcoef символа на токен" || fail "коэффициент не сошёлся: $out"
wcalc=$(python3 -c "print(int(round($wtotal / 2.45 - 14360)))")
echo "$out" | grep -q "расчёт против замера: .*$(fmt "$wcalc") токенов" || fail "расхождение расчёта с замером не сошлось: $out"

# Потолок: равный замеру не превышен, на токен ниже уже превышен, и это код 1.
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 14360 2>&1)
[ $? -eq 0 ] || fail "потолок вровень с замером принят за превышение: $out"
echo "$out" | grep -q 'потолок резидента 14 360 токенов, запас 0' || fail "нет строки про запас до потолка: $out"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 14359 2>&1)
[ $? -eq 1 ] || fail "замер выше потолка не дал кода 1: $out"
echo "$out" | grep -q 'потолок резидента 14 359 токенов превышен на 1' || fail "нет строки про превышение потолка: $out"

# Потолок по умолчанию: без --limit берётся тот, что в бюджете дизайна, и
# сегодняшний резидент выше него. Дефолт тут не украшение, этим же порогом
# доктор будет красить находку.
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" 2>&1)
[ $? -eq 1 ] || fail "замер выше потолка по умолчанию не дал кода 1: $out"
echo "$out" | grep -q 'потолок резидента 6 500 токенов превышен на 7 860' ||
    fail "потолок по умолчанию не 6 500 токенов из бюджета дизайна: $out"

# Повторов один: разброса нет, а прогонов ровно два.
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --runs 1 --limit 20000 2>&1)
[ $? -eq 0 ] || fail "weigh с одним повтором не прошёл: $out"
echo "$out" | grep -q 'замер: 14 320 токенов, разброс 0' || fail "одиночный повтор посчитан не так: $out"
[ "$(wc -l < "$calls" | tr -d ' ')" -eq 2 ] || fail "один повтор это два прогона, а вышло $(wc -l < "$calls")"

# Девятое определение агента: листинг растёт ровно на имя и описание, и вместе с
# ним растёт итог. Иначе экономия перетекала бы в карман, который никто не мерит.
probe_desc=$(python3 -c 'print("описание" * 25)')
printf -- '---\nname: probe-agent\ndescription: %s\neffort: low\n---\n\nтело определения\n' "$probe_desc" \
    > "$dk/agents/probe-agent.md"
cp "$dk/agents/probe-agent.md" "$whome/.claude/agents/"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 20000 2>&1)
grown=$((wagents + 11 + ${#probe_desc}))
echo "$out" | grep -q "листинг определений agents/ .*$(fmt "$grown")" ||
    fail "листинг агентов не вырос на новое определение ($grown): $out"
echo "$out" | grep -q "итого .*$(fmt "$((wtotal + 11 + ${#probe_desc}))")" ||
    fail "итог по карманам не вырос на новое определение: $out"
rm -f "$dk/agents/probe-agent.md" "$whome/.claude/agents/probe-agent.md"

# Несвежая раскладка на машине: замер отказан, находка названа, ни одного
# прогона не заказано. Мерить по вчерашней раскладке значит соврать молча.
mv "$whome/.claude/skills/board-groom" "$wtmp/board-groom"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 20000 2>&1)
[ $? -eq 2 ] || fail "weigh мерил по несвежей раскладке: $out"
echo "$out" | grep -q 'нет скилла .*board-groom' || fail "отказ не называет находку раскладки: $out"
echo "$out" | grep -q 'замер отменён' || fail "отказ не объяснён: $out"
[ -s "$calls" ] && fail "при отказе всё же гонялись прогоны: $(cat "$calls")"
mv "$wtmp/board-groom" "$whome/.claude/skills/board-groom"
# Тот же отказ по файлам правил проекта: тронутый руками тонкий файл это чужая
# раскладка, и замер по ней тоже не о чем.
printf '@../nope.md\n' >> "$wproj/CLAUDE.md"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 20000 2>&1)
[ $? -eq 2 ] || fail "weigh мерил по правленому руками тонкому файлу: $out"
[ -s "$calls" ] && fail "при отказе по файлам правил всё же гонялись прогоны: $(cat "$calls")"
cp "$wtmp/thin.gen" "$wproj/CLAUDE.md"

# Нет claude в PATH: гнать замер нечем, и это не находка веса, а отказ.
: > "$calls"
out=$(HOME="$whome" PATH="$sys" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" 2>&1)
[ $? -eq 2 ] || fail "weigh без claude в PATH не отказался: $out"
echo "$out" | grep -q 'claude не в PATH' || fail "отказ без claude не объяснён: $out"

# Повторов ноль: мерить нечего, и это отказ, а не деление на ноль.
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --runs 0 2>&1)
[ $? -eq 2 ] || fail "weigh с нулём повторов не отказался: $out"

# Карманы embed-харнеса: правила вклеены в сам AGENTS.md, и он посчитан целиком,
# а тонкого файла и отдельных файлов правил у такого харнеса нет. Сложи их
# сверху вклейки, и вес правил уехал бы в сумму дважды.
cat > "$dk/harness/embed-tool.toml" <<'EOF'
[detect]

[rules]
mode = "embed"

[delegate]
mode = "none"

[hooks]

[quota]
EOF
mkdir -p "$whome/.devkit"
printf 'enabled = ["embed-tool"]\n' > "$whome/.devkit/harness.local"
cp "$wproj/AGENTS.md" "$wtmp/agents.plain"
HOME="$whome" PATH="$wpath" python3 "$dkctl" doctor --fix -C "$wproj" >/dev/null 2>&1
grep -q '^<!-- devkit:rules begin' "$wproj/AGENTS.md" || fail "правила не вклеились в AGENTS.md embed-харнеса"
out=$(HOME="$whome" python3 - "$dk" "$wproj" <<'EOF'
import sys
sys.path.insert(0, sys.argv[1] + "/devkitctl")
import weigh
name, profile = weigh.active_profile(sys.argv[2], sys.argv[1])
for label, chars in weigh.pockets(sys.argv[2], sys.argv[1], profile):
    print("%s\t%d" % (label, chars))
EOF
)
echo "$out" | grep -q "^AGENTS.md проекта	$(plen "$wproj/AGENTS.md")$" || fail "AGENTS.md с вклейкой посчитан не целиком: $out"
echo "$out" | grep -q 'RULES' && fail "файлы правил посчитаны сверх вклейки: $out"
echo "$out" | grep -q 'тонкий' && fail "у embed-харнеса нашёлся тонкий файл: $out"
echo "$out" | grep -q 'листинг определений' && fail "харнесу без своих субагентов посчитаны определения агентов: $out"
echo "$out" | grep -q "^листинг скиллов	$wskills$" || fail "листинг скиллов у embed-харнеса не сошёлся: $out"
rm -f "$dk/harness/embed-tool.toml" "$whome/.devkit/harness.local"
cp "$wtmp/agents.plain" "$wproj/AGENTS.md"
cp "$wtmp/thin.gen" "$wproj/CLAUDE.md"

# Связка ключей в слепке: без неё клиент под подменённым HOME отвечает «Not
# logged in», и замер не начинается вовсе. Гоняется на выдуманных HOME, чтобы
# обе ветки проверялись на любой платформе, а не только там, где связка есть.
python3 - "$dk" "$wtmp/keys" <<'EOF' || fail "юниты связки ключей в слепке не прошли"
import os
import sys

sys.path.insert(0, sys.argv[1] + "/devkitctl")
work, dkroot = sys.argv[2], sys.argv[1]
import harness
import weigh

profile = harness.parse("cc.toml", open(os.path.join(dkroot, "harness", "claude-code.toml"),
                                        encoding="utf-8").read())
src = os.path.join(work, "src")
keys = os.path.join(src, weigh.KEYCHAIN_DIR)
os.makedirs(os.path.join(src, ".claude"))
os.makedirs(keys)
open(os.path.join(keys, "login.keychain-db"), "w").write("не связка, а её место")

# Связка на месте: в слепке симлинк на неё, и авторизация читается с машины.
# Копия тут была бы и лишней, и опасной: связка живая.
link = os.path.join(weigh.build_home(src, os.path.join(work, "full"), dkroot, profile, True),
                    weigh.KEYCHAIN_DIR)
assert os.path.islink(link), link
assert os.path.realpath(link) == os.path.realpath(keys), os.path.realpath(link)
assert os.path.isfile(os.path.join(link, "login.keychain-db")), os.listdir(link)

# Связки нет (не macOS): слепок остаётся прежним, пустого Library в нём не
# заводится.
bare = os.path.join(work, "bare")
os.makedirs(os.path.join(bare, ".claude"))
dst = weigh.build_home(bare, os.path.join(work, "nokeys"), dkroot, profile, True)
assert not os.path.lexists(os.path.join(dst, "Library")), os.listdir(dst)
EOF

# Проект с доской: в замер едут оба файла правил, и второй это тот самый
# RULES.board.md, из-за которого замер занижался. Пропусти его копию в
# директорию замера, и правила доски снова не доедут до целевого прогона.
bproj="$wtmp/bproj"
mkdir -p "$bproj/docs"
git init -q "$bproj"
git -C "$bproj" config user.name t
git -C "$bproj" config user.email t@t
HOME="$whome" python3 "$dkctl" new --no-board -C "$bproj" >/dev/null || fail "weigh: проект с доской не подключился"
printf '# Задачи\n\nПрефикс: WG\n' > "$bproj/docs/TASKS.md"
HOME="$whome" PATH="$wpath" python3 "$dkctl" doctor --fix -C "$bproj" >/dev/null 2>&1
grep -q '^@.*RULES.board.md$' "$bproj/CLAUDE.md" ||
    fail "тонкий файл проекта с доской не зовёт правила доски: $(cat "$bproj/CLAUDE.md")"
: > "$calls"
: > "$imports"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" WEIGH_IMPORTS="$imports" \
    python3 "$dkctl" weigh -C "$bproj" --runs 1 --limit 30000 2>&1)
[ $? -eq 0 ] || fail "weigh не прошёл на проекте с доской: $out"
grep -q '^RULES.board.md$' "$imports" ||
    fail "правила доски не доехали до директории замера: $(cat "$imports")"
grep -q '/' "$imports" && fail "проект с доской импортирует наружу: $(cat "$imports")"
echo "$out" | grep -q 'замер: 21 320 токенов' || fail "правила доски не попали в замер: $out"
echo "$out" | grep -q 'RULES.board.md (импорт тонкого файла)' ||
    fail "правила доски не посчитаны карманом: $out"

# Ядро правил нарезано: резидентно то, что импортирует тонкий файл, а не полный
# текст. Глубину claude-code объявляет ядром, и как только RULES.core.md
# появляется, за импортом переезжает и карман. Считай карман по-старому, и порог
# резидента мерил бы файл, которого в контексте уже нет.
printf 'ядро правил, короткий текст\n' > "$dk/RULES.core.md"
HOME="$whome" PATH="$wpath" python3 "$dkctl" doctor --fix -C "$wproj" >/dev/null 2>&1
grep -q 'RULES.core.md' "$wproj/CLAUDE.md" || fail "тонкий файл не переехал на нарезанное ядро"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --runs 1 --limit 20000 2>&1)
[ $? -eq 0 ] || fail "weigh не прошёл на нарезанном ядре: $out"
echo "$out" | grep -q "RULES.core.md (импорт тонкого файла) .*$(fmt "$(plen "$dk/RULES.core.md")")" ||
    fail "карман считает не нарезанное ядро: $out"
echo "$out" | grep -q '^  RULES.md (импорт' && fail "в карманах остался полный текст правил: $out"
# Под ту же глубину собирается и тонкий файл директории замера, иначе целевой
# прогон платил бы за полный текст, а расчёт считал бы ядро. Видно это по
# замеру: подложный клиент берёт цену цепочки из того, что тонкий файл
# импортирует, и на ядре целевой прогон дешевле.
echo "$out" | grep -q 'замер: 3 720 токенов' ||
    fail "директория замера собрана не под глубину проекта, целевой прогон платит за полный текст: $out"
rm -f "$dk/RULES.core.md"
HOME="$whome" PATH="$wpath" python3 "$dkctl" doctor --fix -C "$wproj" >/dev/null 2>&1
grep -q 'RULES.core.md' "$wproj/CLAUDE.md" && fail "тонкий файл не вернулся на полный текст правил"

# Оба прогона пары стоят одинаково: разница нулевая, мерить было нечего (прогоны
# ушли по одной раскладке либо усилия харнеса разъехались). Печатать по такой
# разнице карманы и коэффициент значило бы выдать шум за замер.
cat > "$wbin/claude" <<'EOF'
#!/bin/sh
echo "$PWD" >> "$WEIGH_CALLS"
printf '{"type":"result","usage":{"input_tokens":4,"cache_creation_input_tokens":20000,"cache_read_input_tokens":9996}}\n'
EOF
chmod +x "$wbin/claude"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 20000 2>&1)
[ $? -eq 2 ] || fail "нулевая разница принята за замер: $out"
echo "$out" | grep -q 'разница вышла неположительной' || fail "нулевая разница не объяснена: $out"
echo "$out" | grep -q 'коэффициент этого прогона' && fail "по нулевой разнице всё же посчитан коэффициент: $out"

# Битый ответ клиента: замер падает вслух, а не выдаёт разницу из нулей.
cat > "$wbin/claude" <<'EOF'
#!/bin/sh
echo "$PWD" >> "$WEIGH_CALLS"
echo 'ничего похожего на json'
EOF
chmod +x "$wbin/claude"
: > "$calls"
out=$(HOME="$whome" PATH="$wpath" WEIGH_CALLS="$calls" python3 "$dkctl" weigh -C "$wproj" --limit 20000 2>&1)
[ $? -eq 2 ] || fail "weigh проглотил ответ клиента без usage: $out"
echo "$out" | grep -q 'не JSON' || fail "поломка прогона не названа: $out"

# devkit, выложенный worktree ветки задачи: mtime исходников там ничего не
# значит, сборка не запускается, находка отправляет в основной чекаут.
git -C "$dk" init -q .
git -C "$dk" config user.name t
git -C "$dk" config user.email t@t
git -C "$dk" add -A
git -C "$dk" commit -qm init
git -C "$dk" worktree add -q -b probe "$tmp/devkit-wt"
# Пути в находках доктор печатает разрешёнными, а mktemp на macOS отдаёт /var,
# который на деле симлинк на /private/var.
dkreal=$(cd "$dk" && pwd -P)
wthome="$tmp/wthome"
out=$(HOME="$wthome" PATH="$gostub:$sys" python3 "$tmp/devkit-wt/devkitctl/devkitctl.py" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'worktree ветки задачи' || fail "нет находки про worktree devkit: $out"
echo "$out" | grep -q "$dkreal/agentctl" || fail "находка не отправляет в основной чекаут: $out"
[ -f "$wthome/go/bin/agentctl" ] && fail "--fix собрал машинный бинарь с фичеветки"
# Определения агентов из worktree так же не раскладываются, а находка
# зовёт копировать из основного чекаута.
[ -f "$wthome/.claude/agents/exec-medium.md" ] && fail "--fix разложил определения агентов с фичеветки"
echo "$out" | grep -q "cp $dkreal/agents/review-high.md" || fail "находка про определения зовёт не в основной чекаут: $out"
# Скиллы держатся того же рубежа.
[ -f "$wthome/.claude/skills/board-batch/SKILL.md" ] && fail "--fix разложил скилл с фичеветки"
echo "$out" | grep -q "cp $dkreal/skills/board-batch/SKILL.md" || fail "находка про скилл зовёт не в основной чекаут: $out"
# Разошедшееся определение из worktree сверяется с основным чекаутом.
mkdir -p "$wthome/.claude/agents"
cp "$dk/agents/"*.md "$wthome/.claude/agents/"
printf '\nсвоя строка\n' >> "$wthome/.claude/agents/exec-high.md"
out=$(HOME="$wthome" PATH="$gostub:$sys" python3 "$tmp/devkit-wt/devkitctl/devkitctl.py" doctor -C "$mproj" 2>&1)
echo "$out" | grep -q "cp $dkreal/agents/exec-high.md" || fail "сверка определения идёт не с основным чекаутом: $out"
# Защита from_main действует и для перезаписи: --fix с worktree ветки задачи
# разошедшееся определение не перекладывает, находка остаётся.
out=$(HOME="$wthome" PATH="$gostub:$sys" python3 "$tmp/devkit-wt/devkitctl/devkitctl.py" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q "cp $dkreal/agents/exec-high.md" || fail "с worktree --fix потерял находку про разошедшееся определение: $out"
grep -q 'своя строка' "$wthome/.claude/agents/exec-high.md" || fail "с worktree --fix переложил определение с непроверенной ветки: $out"
# Бинари на месте, но старее выложенного worktree: свежесть по mtime там ничего
# не значит, и доктор про них молчит, а не печатает четыре ложные находки.
wtbin="$tmp/wtbin"
mkdir -p "$wtbin"
for t in taskctl shipctl agentctl regcheck; do
    printf '#!/bin/sh\nexit 0\n' > "$wtbin/$t"
    chmod +x "$wtbin/$t"
    touch -t 200001010000 "$wtbin/$t"
done
out=$(HOME="$wthome" PATH="$gostub:$wtbin:$sys" python3 "$tmp/devkit-wt/devkitctl/devkitctl.py" doctor --fix -C "$mproj" 2>&1)
echo "$out" | grep -q 'старее исходников devkit' && fail "из worktree бинари объявлены устаревшими: $out"
echo "$out" | grep -q 'починено' && fail "из worktree --fix что-то пересобрал: $out"

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
out=$(python3 "$dkctl" stats -C "$sproj" 2>&1)
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
out=$(python3 "$dkctl" stats -C "$empty_proj" 2>&1)
[ $? -eq 2 ] || fail "stats без журнала должен вернуть код 2"

# stats с журналом из одних битых строк: код выхода 2, сообщение про пустой журнал.
bad_proj="$tmp/bad"
mkdir -p "$bad_proj/.devkit"
printf 'broken\tline\tno\tcode\nthis\tis\tbroken\ttoo\n' > "$bad_proj/.devkit/log"
out=$(python3 "$dkctl" stats -C "$bad_proj" 2>&1)
[ $? -eq 2 ] || fail "stats с одними битыми строками должен вернуть код 2"

if [ $fails -eq 0 ]; then
    echo "devkitctl в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
