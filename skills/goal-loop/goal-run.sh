#!/bin/sh
# Оболочка режима цели: поднимает витки скилла goal-loop одноразовыми
# headless-сессиями, пока виток отвечает маркером continue. Живучесть цикла от
# живучести сессии тут не зависит: состояние цели целиком лежит на диске (файл
# цели, доска, git, деревья задач), а оболочка только зовёт следующий виток и
# решает, когда перестать звать.
#
#   goal-run.sh <ID> [-C <корень проекта>] [--foreground]
#
# Без --foreground цикл уходит в свою tmux-сессию goal-<ID>, как съёмщик панели
# /usage поднимает клиента в одноразовой сессии, и оболочка возвращается сразу.
# С --foreground витки идут в этом же процессе: так цикл смотрят вживую и так
# его гоняют тесты.
#
# Коды возврата: 0 штатный стоп по маркеру витка, 1 цикл остановила сама
# оболочка (виток без маркера, авария, воронка), 2 ошибка вызова или
# окружения, 3 цикл этой цели уже идёт.
set -u

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
notifier="$here/../../hooks/notify.py"
funnel_limit=3 # витков подряд без записи в «Журнале», после которых стоп

usage() {
    cat <<'EOF'
goal-run.sh <ID> [-C <корень проекта>] [--foreground]

Витки цели <ID> одноразовыми сессиями, пока виток отвечает marker: continue.
Любой другой маркер завершает цикл и зовёт уведомитель громким поводом.

  -C <корень>    проект с доской, по умолчанию текущая директория
  --foreground   держать цикл в этом процессе, а не в tmux-сессии goal-<ID>

Ход цикла пишется в <корень>/.devkit/goal-<ID>.log: время, номер витка,
маркер, код выхода.
EOF
}

die() { # текст, код
    printf '%s\n' "$1" >&2
    exit "${2:-2}"
}

id=""
proj=$(pwd)
fg=0
while [ $# -gt 0 ]; do
    case $1 in
        -C)
            [ $# -ge 2 ] || die "у -C нет значения"
            proj=$2
            shift 2
            ;;
        --foreground)
            fg=1
            shift
            ;;
        -h | --help)
            usage
            exit 0
            ;;
        -*)
            usage >&2
            die "неизвестный флаг $1"
            ;;
        *)
            [ -z "$id" ] || die "лишний аргумент $1: цель у оболочки одна"
            id=$1
            shift
            ;;
    esac
done
[ -n "$id" ] || {
    usage >&2
    die "не назван ID цели"
}

proj=$(CDPATH= cd -- "$proj" 2>/dev/null && pwd) || die "корня проекта $proj нет"
goal="$proj/docs/tasks/$id.md"
devdir="$proj/.devkit"
deploy="$devdir/deploy.local"
log="$devdir/goal-$id.log"
lock="$devdir/goal-$id.lock"
sess="goal-$id"
prompt="продолжай цель $id по скиллу goal-loop"

[ -f "$proj/docs/TASKS.md" ] || die "доски $proj/docs/TASKS.md нет, режим цели живёт только в проекте с доской"
[ -f "$goal" ] || die "файла цели $goal нет: цель ставит скилл goal-start, оболочка её не заводит"
# Флаг автономии оболочка читает, но не поднимает: при autonomous = false
# слияние не пушит и не катит, и цикл проверял бы агентские сценарии против
# старого прода, считая задачи выкаченными.
grep -Eq '^[[:space:]]*autonomous[[:space:]]*=[[:space:]]*true' "$deploy" 2>/dev/null ||
    die "в $deploy нет autonomous = true, цикл без выката проверял бы сценарии против старого прода"
command -v claude >/dev/null 2>&1 || die "claude в PATH нет, поднимать витки нечем"
# Предполётная проверка прав: одобрять запросы харнеса в headless-сессии
# некому, и виток без прав отвечает continue, не сделав ничего. Без проверки
# такой цикл жёг бы бюджет до самой воронки, а стоп был бы неотличим от
# молчания.
perms=$(python3 "$here/../../devkitctl/perms.py" 2>&1)
case $? in
    0) ;;
    1) die "$perms" ;;
    *) die "прав машинного контура не проверить: $perms" ;;
esac

lock_busy() { # замок держит живой владелец
    pid=$(cat "$lock/pid" 2>/dev/null) || return 1
    [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

take_lock() {
    mkdir -p "$devdir" 2>/dev/null
    if mkdir "$lock" 2>/dev/null; then
        printf '%s\n' "$$" > "$lock/pid"
        return 0
    fi
    lock_busy && return 1
    # Замок без живого владельца остался от убитой оболочки: перезагрузка,
    # kill -9, закрытое окно tmux. Снимаем и берём себе, иначе цикл не поднять
    # до ручной уборки, а обрыв оболочки чинится повторным запуском.
    rm -f "$lock/pid"
    rmdir "$lock" 2>/dev/null
    mkdir "$lock" 2>/dev/null || return 1
    printf '%s\n' "$$" > "$lock/pid"
}

release() {
    rm -f "$lock/pid"
    rmdir "$lock" 2>/dev/null
}

say() { # строка в журнал оболочки и в вывод: панель tmux показывает то же
    line="$(date '+%Y-%m-%dT%H:%M:%S') $*"
    mkdir -p "$devdir" 2>/dev/null
    printf '%s\n' "$line" >> "$log" 2>/dev/null
    printf '%s\n' "$line"
}

shout() { # заголовок, текст: стоп цикла молчать не должен
    if [ ! -f "$notifier" ]; then
        say "уведомителя $notifier нет, стоп остался без голоса"
        return
    fi
    out=$(cd "$proj" && python3 "$notifier" "$1" "$2" 2>&1)
    say "уведомление: $1; ${out:-отправлено}"
}

stop() { # повод, текст, код
    say "цикл цели $id остановлен: $1, витков $turn"
    shout "цель $id: $1" "$2"
    exit "$3"
}

# Содержательных записей в «Журнале» цели столько. Строки снимков квоты их
# дописывает сам гейт бюджета, и продвижением витка они не считаются: снимок
# появляется и у витка, который не сделал ничего.
journal_entries() {
    awk '
        /^## / {
            if (inj) exit
            inj = ($0 ~ /^## Журнал/)
            next
        }
        inj {
            line = $0
            sub(/^[[:space:]]+/, "", line)
            sub(/^- /, "", line)
            if (line == "") next
            if (line ~ /^снимок /) next
            n++
        }
        END { print n + 0 }
    ' "$goal" 2>/dev/null
}

if [ "$fg" -eq 0 ]; then
    command -v tmux >/dev/null 2>&1 ||
        die "tmux в PATH нет, поднять цикл нечем; свой цикл в этом же процессе даёт --foreground"
    lock_busy && die "цикл цели $id уже идёт, замок $lock" 3
    tmux has-session -t "=$sess" 2>/dev/null &&
        die "tmux-сессия $sess уже поднята, смотреть её: tmux attach -t $sess" 3
    quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
    cmd="$(quote "$here/goal-run.sh") $(quote "$id") -C $(quote "$proj") --foreground"
    tmux new-session -d -s "$sess" "$cmd" || die "tmux не поднял сессию $sess"
    printf 'цикл цели %s поднят в tmux-сессии %s, журнал %s\n' "$id" "$sess" "$log"
    exit 0
fi

turn=0
take_lock || die "цикл цели $id уже идёт, замок $lock, владелец $(cat "$lock/pid" 2>/dev/null)" 3
trap 'release' EXIT
trap 'exit 1' INT TERM

idle=0
say "цикл цели $id начат в $proj, pid $$"
while :; do
    turn=$((turn + 1))
    before=$(journal_entries)
    out=$(cd "$proj" && claude -p "$prompt" 2>&1)
    code=$?
    after=$(journal_entries)
    # Маркер это последняя непустая строка ответа, и сравнивается она целиком:
    # ограда примера или «маркер: continue» прозой за маркер не считаются, так
    # обещает раздел «Маркеры выхода» скилла.
    last=$(printf '%s\n' "$out" | sed -e 's/[[:space:]]*$//' -e '/^$/d' | tail -n 1)
    marker=""
    case $last in
        "marker: continue" | "marker: done" | "marker: over" | "marker: wait-human" | "marker: stuck")
            marker=${last#marker: }
            ;;
    esac
    say "виток $turn маркер ${marker:-нет} код $code записей в журнале $before -> $after"
    [ "$code" -eq 0 ] ||
        stop "авария витка" "сессия витка вышла с кодом $code, последняя строка: ${last:-пусто}" 1
    [ -n "$marker" ] ||
        stop "виток без маркера" "последняя строка ответа: ${last:-пусто}, а маркер идёт голой строкой marker: <имя>" 1
    [ "$marker" = continue ] ||
        stop "$marker" "виток кончился маркером $marker, разбор в файле цели docs/tasks/$id.md" 0
    if [ "$after" -gt "$before" ]; then
        idle=0
    else
        idle=$((idle + 1))
        say "виток $turn не записал в «Журнал» ничего, подряд таких $idle из $funnel_limit"
    fi
    # Воронка это витки, которые говорят «работа осталась» и не делают ничего.
    # Маркером stuck виток останавливает цикл сам, тут ловится молчащий
    # continue: без счётчика он жёг бы бюджет цели до потолка.
    [ "$idle" -lt "$funnel_limit" ] ||
        stop "воронка" "$idle витка подряд без записи в «Журнале», цикл жжёт бюджет вхолостую" 1
done
