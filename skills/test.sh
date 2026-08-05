#!/bin/sh
# Самопроверка скиллов devkit: у каждого разбираемый frontmatter с именем по
# директории и описанием, по которому видно, когда скилл звать, а у процедурной
# части правил (board-task, board-ship, test-standard) ещё и вход из полного
# текста правил. Инструмент без скиллов зовёт процедуру по указателю, и путь в
# указателе берётся из тех же файлов, поэтому пропавший скилл это не косметика,
# а потерянная процедура.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(dirname "$here")
fails=0
fail() { echo "FAIL: $1" >&2; fails=$((fails + 1)); }

meta() { # значение ключа frontmatter: meta <файл> <ключ>
    awk -v key="$2" '
        NR == 1 { if ($0 != "---") exit; next }
        $0 == "---" { exit }
        index($0, key ":") == 1 { sub("^" key ": *", ""); print; exit }
    ' "$1"
}

n=0
for skill in "$here"/*/; do
    name=$(basename "$skill")
    f="$skill/SKILL.md"
    n=$((n + 1))
    [ -f "$f" ] || { fail "$name: нет SKILL.md, скилл сессия не подхватит"; continue; }
    [ "$(head -n 1 "$f")" = "---" ] || fail "$name: нет frontmatter, описание в промпт не уедет"
    got=$(meta "$f" name)
    [ "$got" = "$name" ] ||
        fail "$name: во frontmatter имя «$got», а скилл зовётся по директории"
    desc=$(meta "$f" description)
    [ -n "$desc" ] ||
        fail "$name: пустое описание, звать скилл сессии будет не по чему"
    case $desc in
        *Звать,*|*Use\ this\ skill*) ;;
        *) fail "$name: описание не говорит, когда скилл звать: $desc" ;;
    esac
    [ "$(wc -l < "$f")" -gt 10 ] || fail "$name: тело скилла пустое"
done
[ "$n" -gt 0 ] || fail "скиллов не нашлось вовсе"

# Процедурная часть правил: разрез резидента (docs/lld/DK-100-context-tree.md,
# задача D) вынес шаги отсюда в скиллы, и вход к ним обязан быть назван в полном
# тексте. Инструмент со скиллами зовёт их по описанию, а тот, кому текст
# вклеивается целиком, только по этой строке: без неё процедура не уехала, а
# пропала.
for pair in "board-task:RULES.board.md" "board-ship:RULES.board.md" \
            "test-standard:RULES.md"; do
    skill=${pair%%:*}
    src=${pair#*:}
    [ -f "$here/$skill/SKILL.md" ] ||
        fail "$skill: процедура правил скиллом не заведена"
    grep -q "skills/$skill/SKILL.md" "$root/$src" ||
        fail "$src не называет путь до скилла $skill: процедуру по правилам не найти"
done

# Режим цели разрезан по шву между постановкой и витком (DK-118): постановка
# зовётся один раз на цель, а виток десятки раз, и до разреза каждый виток
# тащил в контекст текст постановки. Разрез держится только текстом, поэтому
# проверяется, что половины не срослись обратно и что вход из одной в другую
# назван: потерянный вход это цель, которую некому продолжить.
for pair in "goal-start:goal-loop" "goal-loop:goal-start"; do
    skill=${pair%%:*}
    other=${pair#*:}
    f="$here/$skill/SKILL.md"
    [ -f "$f" ] || { fail "$skill: скилл режима цели не заведён"; continue; }
    grep -q "$other" "$f" || fail "$skill: не называет соседа $other, вторая половина режима потеряна"
done
grep -q '^## Разделы файла цели' "$here/goal-start/SKILL.md" ||
    fail "goal-start: нет разделов файла цели, постановке нечем заводить цель"
grep -q '^## Маркеры выхода' "$here/goal-loop/SKILL.md" ||
    fail "goal-loop: нет маркеров выхода, витку нечем кончиться"
grep -q '^## Виток' "$here/goal-start/SKILL.md" &&
    fail "goal-start: тащит процедуру витка, разрез сросся обратно"
grep -q '^## Разделы файла цели' "$here/goal-loop/SKILL.md" &&
    fail "goal-loop: тащит постановку, разрез сросся обратно"

# Грумминг разрезан на фазу разбора и фазу оформления (DK-129): у захода три
# входа, а у разобранного черновика четыре исхода, и под каждый исход своя
# команда taskctl. Выпавший из текста вход или исход это ветка разбора, которую
# сессия молча не пройдёт, и заметить пропажу на живом заходе некому.
groom="$here/board-groom/SKILL.md"
if [ -f "$groom" ]; then
    grep -q '^## Входы' "$groom" ||
        fail "board-groom: нет раздела про входы, откуда берётся список черновиков не сказано"
    n_in=$(grep -c '^- `/board-groom' "$groom")
    [ "$n_in" -eq 3 ] ||
        fail "board-groom: входов названо $n_in, а их три (один ID, список ID, весь накопитель)"
    grep -q '^## Исходы разбора' "$groom" ||
        fail "board-groom: нет раздела про исходы, разобранный черновик остаётся без следа"
    for cmd in "add --id" "draft attach" "draft defer" "draft drop"; do
        grep -q -- "$cmd" "$groom" ||
            fail "board-groom: исход без команды, в процедуре нет taskctl $cmd"
    done
else
    fail "board-groom: скилл грумминга не заведён"
fi

# Скилл ссылается на правило, из которого выведен: расхождение процедуры с
# правилом иначе замечается только чтением обоих подряд.
for skill in board-task board-ship test-standard; do
    grep -q 'RULES\.\(board\.\)\?md' "$here/$skill/SKILL.md" ||
        fail "$skill: скилл не называет правило, из которого выведен"
done

if [ "$fails" -eq 0 ]; then
    echo "ok: скиллы, проверено $n"
else
    echo "провалов: $fails" >&2
    exit 1
fi
