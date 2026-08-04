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
