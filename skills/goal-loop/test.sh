#!/bin/sh
# Самопроверка оболочки цели goal-run.sh. Витки играет стаб вместо claude, как
# парсер панели /usage гоняется по образцам: настоящих сессий тут не
# поднимается ни одной. Каждый стенд это свой синтетический проект с доской,
# файлом цели и временным HOME, чтобы уведомитель писал свой журнал в него.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
run="$here/goal-run.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
fail() { echo "FAIL: $1" >&2; fails=$((fails + 1)); }

n=0

claude_stub() { # стаб клиента стенда: играет очередную строку сценария
    root=$1
    cat > "$root/bin/claude" <<EOF
#!/bin/sh
# Строка сценария это «маркер что-делает-с-журналом»: «запись» дописывает
# содержательную строку в «Журнал» цели, «снимок» строку снимка квоты, всё
# остальное молчит. Особые маркеры: none (ответ без маркера), fenced (маркер в
# ограде), crash (ненулевой код возврата). Слово «замок» в строке поднимает
# вторую оболочку той же цели. Сценарий кончился, значит повторяется последняя
# строка: цикл, который не остановился, так и крутится.
root="$root"
goal="\$root/proj/docs/tasks/DK-100.md"
printf '%s\n' "\$*" >> "\$root/calls"
turn=\$(wc -l < "\$root/calls" | tr -d ' ')
spec=\$(sed -n "\${turn}p" "\$root/turns")
[ -n "\$spec" ] || spec=\$(tail -n 1 "\$root/turns")
# Потолок витков: сломанная оболочка, которая не умеет останавливаться, иначе
# крутила бы стенд вечно, и провал выглядел бы зависшим прогоном.
if [ "\$turn" -gt 10 ]; then
    printf 'стаб: витков больше десяти, оболочка не остановилась\n'
    printf 'marker: done\n'
    exit 0
fi
marker=\${spec%% *}
entry=""
case \$spec in
    *" запись"*) entry="- виток \$turn, задача цели закрыта; \$marker" ;;
    *" снимок"*) entry="- снимок 2026-08-0\$turn: week_all \$turn%" ;;
esac
if [ -n "\$entry" ]; then
    awk -v line="\$entry" '{ print } /^## Журнал/ { print ""; print line }' "\$goal" > "\$goal.new" &&
        mv "\$goal.new" "\$goal"
fi
case \$spec in
    *замок*)
        [ -d "\$root/proj/.devkit/goal-DK-100.lock" ] && printf 'замок на месте\n' >> "\$root/lock-probe"
        "$run" DK-100 -C "\$root/proj" --foreground > "\$root/second.out" 2>&1
        printf 'код %s\n' "\$?" >> "\$root/second.out"
        ;;
esac
printf 'виток стаба %s\n' "\$turn"
case \$marker in
    none) printf 'работа осталась, но маркер сказать забыли\n'; exit 0 ;;
    fenced) printf 'marker: \`continue\`\n'; exit 0 ;;
    crash) printf 'клиент упал\n'; exit 7 ;;
esac
printf 'marker: %s\n' "\$marker"
EOF
    chmod +x "$root/bin/claude"
}

stand() { # новый стенд, аргументы это строки сценария стаба; корень в $root
    n=$((n + 1))
    root="$tmp/s$n"
    mkdir -p "$root/proj/.devkit" "$root/proj/docs/tasks" "$root/bin" "$root/home"
    printf '# Доска\n\nПрефикс: DK\n\n## In progress\n' > "$root/proj/docs/TASKS.md"
    cat > "$root/proj/docs/tasks/DK-100.md" <<'EOF'
# DK-100: Цель: синтетическая цель обкатки

## Цель

DoD: стенд отработал.

## Бюджет

бюджет: week_all <= 10

## Задачи цели

Заводит нарезка первым витком.

## Журнал

## Итог

Пишет последний виток.
EOF
    printf 'deploy = true\nautonomous = true\n' > "$root/proj/.devkit/deploy.local"
    : > "$root/turns"
    for spec in "$@"; do printf '%s\n' "$spec" >> "$root/turns"; done
    claude_stub "$root"
}

goal_run() { # стенд, дальше аргументы оболочки
    root=$1
    shift
    (
        PATH="$root/bin:$PATH"
        HOME="$root/home"
        export PATH HOME
        cd "$root/proj" && "$run" "$@"
    )
}

turns_done() { wc -l < "$1/calls" | tr -d ' '; }
shell_log() { printf '%s\n' "$1/proj/.devkit/goal-DK-100.log"; }

# Цикл до маркера done: continue поднимает следующий виток, done завершает.
# Заодно тут проверяются промпт витка, журнал оболочки, громкое уведомление на
# стопе и снятый замок.
stand "continue запись" "continue запись" "done запись"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1 || fail "цикл до done вернул не 0"
got=$(turns_done "$root")
[ "$got" -eq 3 ] || fail "маркер continue не поднял следующий виток: витков $got вместо трёх"
grep -q -- '-p продолжай цель DK-100 по скиллу goal-loop' "$root/calls" ||
    fail "виток поднят не тем вызовом: $(head -n 1 "$root/calls")"
log=$(shell_log "$root")
grep -q 'виток 1 маркер continue код 0' "$log" || fail "в журнале оболочки нет витка с маркером и кодом"
grep -q 'виток 3 маркер done код 0' "$log" || fail "в журнале оболочки нет последнего витка"
grep -q 'остановлен: done' "$log" || fail "журнал оболочки не назвал, чем цикл кончился"
grep -q 'уровень громкий' "$root/home/.devkit/notify.log" || fail "стоп прошёл без громкого уведомления"
[ -d "$root/proj/.devkit/goal-DK-100.lock" ] && fail "замок остался после цикла"

# Каждый маркер, кроме continue, завершает цикл первым же витком.
for m in done over wait-human stuck; do
    stand "$m запись"
    goal_run "$root" DK-100 --foreground > "$root/out" 2>&1 || fail "маркер $m вернул не 0"
    got=$(turns_done "$root")
    [ "$got" -eq 1 ] || fail "маркер $m не завершил цикл: витков $got"
    grep -q "остановлен: $m" "$(shell_log "$root")" || fail "маркер $m не назван в журнале оболочки"
    grep -q 'уровень громкий' "$root/home/.devkit/notify.log" || fail "маркер $m прошёл без уведомления"
done

# Воронка: витки говорят continue и в «Журнал» ничего не пишут. Стоп ровно на
# третьем, а не на втором и не на четвёртом.
stand "continue молчок"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 1 ] || fail "воронка вернула не 1"
got=$(turns_done "$root")
[ "$got" -eq 3 ] || fail "воронка остановила цикл на витке $got, а порог три"
grep -q 'остановлен: воронка' "$(shell_log "$root")" || fail "воронка не названа в журнале оболочки"
grep -q 'уровень громкий' "$root/home/.devkit/notify.log" || fail "воронка прошла без громкого уведомления"

# Счётчик воронки сбрасывается витком, который в «Журнал» записал.
stand "continue молчок" "continue молчок" "continue запись" "continue молчок" "continue молчок" "done запись"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1 || fail "цикл со сбросом счётчика вернул не 0"
got=$(turns_done "$root")
[ "$got" -eq 6 ] || fail "счётчик воронки не сбросился на продвинувшемся витке: витков $got вместо шести"

# Строка снимка квоты продвижением не считается: её дописывает гейт бюджета и
# у витка, который не сделал ничего.
stand "continue снимок"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 1 ] || fail "цикл со снимками вместо записей не остановился воронкой"
got=$(turns_done "$root")
[ "$got" -eq 3 ] || fail "снимок квоты сошёл за продвижение витка: витков $got"

# Замок на цель: пока цикл идёт, вторая оболочка той же цели отказывает.
stand "continue запись замок" "done запись"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1 || fail "цикл с замком вернул не 0"
grep -q 'замок на месте' "$root/lock-probe" 2>/dev/null || fail "идущий цикл замка не поставил"
grep -q 'уже идёт' "$root/second.out" 2>/dev/null || fail "вторая оболочка не отказала: $(cat "$root/second.out" 2>/dev/null)"
grep -q 'код 3' "$root/second.out" 2>/dev/null || fail "отказ второй оболочки вернул не 3"
[ "$(turns_done "$root")" -eq 2 ] || fail "вторая оболочка успела поднять свои витки"

# Чужой замок с живым владельцем уважается, а брошенный снимается: оболочка,
# убитая вместе с окном tmux, не должна запирать цель до ручной уборки.
stand "done запись"
mkdir -p "$root/proj/.devkit/goal-DK-100.lock"
sleep 30 &
alive=$!
printf '%s\n' "$alive" > "$root/proj/.devkit/goal-DK-100.lock/pid"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 3 ] || fail "оболочка полезла в цель, которую ведёт живой владелец замка"
kill "$alive" 2>/dev/null
wait "$alive" 2>/dev/null
printf '%s\n' "$alive" > "$root/proj/.devkit/goal-DK-100.lock/pid"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1 || fail "брошенный замок не снялся"
[ "$(turns_done "$root")" -eq 1 ] || fail "цикл после брошенного замка не пошёл"

# Ответ без маркера, маркер в ограде и упавший клиент это авария витка.
stand "none запись"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 1 ] || fail "ответ без маркера не остановил цикл"
grep -q 'остановлен: виток без маркера' "$(shell_log "$root")" || fail "авария без маркера не названа"

stand "fenced запись"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 1 ] || fail "маркер в ограде принят за маркер"
[ "$(turns_done "$root")" -eq 1 ] || fail "маркер в ограде поднял следующий виток"

stand "crash запись"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 1 ] || fail "упавший клиент не остановил цикл"
grep -q 'остановлен: авария витка' "$(shell_log "$root")" || fail "падение клиента не названо аварией"

# Отказы на старте: без ID, без доски, без файла цели и при autonomous = false
# цикл не поднимается вовсе.
stand "done запись"
goal_run "$root" --foreground > "$root/out" 2>&1
[ $? -eq 2 ] || fail "оболочка пошла без ID цели"
goal_run "$root" DK-101 --foreground > "$root/out" 2>&1
[ $? -eq 2 ] || fail "оболочка пошла по цели, файла которой нет"
grep -q 'файла цели' "$root/out" || fail "отказ не назвал причину: $(cat "$root/out")"
printf 'deploy = true\nautonomous = false\n' > "$root/proj/.devkit/deploy.local"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 2 ] || fail "оболочка пошла при autonomous = false"
grep -q 'autonomous = true' "$root/out" || fail "отказ по автономии не назвал флаг: $(cat "$root/out")"
printf 'deploy = true\nautonomous = true\n' > "$root/proj/.devkit/deploy.local"
mv "$root/proj/docs/TASKS.md" "$root/proj/docs/BOARD.md"
goal_run "$root" DK-100 --foreground > "$root/out" 2>&1
[ $? -eq 2 ] || fail "оболочка пошла в проекте без доски"
[ -f "$root/calls" ] && fail "отказавшая оболочка успела поднять виток"

# Без --foreground цикл уходит в свою tmux-сессию, а поднятую сессию той же
# цели оболочка второй раз не заводит.
stand "done запись"
cat > "$root/bin/tmux" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$root/tmux-calls"
case \$1 in has-session) [ -f "$root/tmux-has" ] && exit 0; exit 1 ;; esac
exit 0
EOF
chmod +x "$root/bin/tmux"
goal_run "$root" DK-100 > "$root/out" 2>&1 || fail "запуск в tmux вернул не 0"
grep -q 'new-session -d -s goal-DK-100' "$root/tmux-calls" || fail "tmux позван не тем: $(cat "$root/tmux-calls")"
grep -q -- '--foreground' "$root/tmux-calls" || fail "в tmux уехала команда без --foreground"
grep -q 'goal-DK-100' "$root/out" || fail "оболочка не назвала tmux-сессию цикла"
[ -f "$root/calls" ] && fail "оболочка подняла виток мимо tmux"
: > "$root/tmux-has"
goal_run "$root" DK-100 > "$root/out" 2>&1
[ $? -eq 3 ] || fail "оболочка полезла в цель с уже поднятой tmux-сессией"

if [ $fails -eq 0 ]; then
    echo "оболочка цели в порядке"
else
    echo "провалов: $fails" >&2
    exit 1
fi
