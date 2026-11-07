#!/bin/sh
# SessionStart-хук: освежает снимок остатка лимитов активного харнеса
# (~/.devkit/quota/<харнес>.local), по которому корректор agentctl pick двигает
# вердикт. Руками снимок не переснимает никто, а протухший корректор просто
# выключает, поэтому обновление повешено на старт сессии. Работа уходит отцепленным процессом, хук возвращается сразу:
# съём панели /usage занимает секунды, и держать на нём старт сессии незачем.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
dir="$HOME/.devkit"
lock="$dir/quota-refresh.lock"
log="$dir/quota-refresh.log"
# Порог обрезки журнала: тот же размер и тот же запас строк, что у
# append_capped (hookio.py), которым режутся остальные машинные журналы хуков.
# Переопределимы переменной окружения ради теста, на живой машине не трогаются.
log_limit=${QUOTA_REFRESH_LOG_LIMIT:-102400}
log_keep=${QUOTA_REFRESH_LOG_KEEP:-500}

# Снимать нечем, значит молча уходим: хук стоит у всех сессий на машине, и
# ругаться в каждой на отсутствующий tmux он не должен. Про нехватку скажет
# devkitctl doctor, у которого это ровно его работа.
for tool in agentctl tmux claude; do
    command -v "$tool" >/dev/null 2>&1 || exit 0
done
mkdir -p "$dir" 2>/dev/null || exit 0

# Замок держит две вещи сразу. Первая и главная: refresh поднимает claude в
# tmux, старт того клиента дёргает этот же хук, и без замка получилась бы
# воронка из сессий. Вторая: параллельные сессии не снимают панель наперегонки.
# Замок это директория, mkdir атомарен; брошенный замок старше десяти минут
# снимается, весь цикл refresh укладывается в минуту.
if [ -d "$lock" ]; then
    [ -n "$(find "$lock" -maxdepth 0 -mmin +10 2>/dev/null)" ] || exit 0
    rmdir "$lock" 2>/dev/null
fi
mkdir "$lock" 2>/dev/null || exit 0

# Свежий снимок refresh не трогает сам (--if-stale), порог свежести живёт в
# agentctl, и второй его копии тут нет. Вывод последнего запуска остаётся в
# журнале: иначе про фоновую работу нельзя узнать вообще ничего, а отказ
# (клиент не залогинен, панель не узналась) выглядел бы как её отсутствие.
{
    out=$(agentctl quota refresh --if-stale 2>&1)
    code=$?
    # Харнес без объявленной квоты (пустая секция [quota] профиля): снимать у
    # него нечего по построению, и refresh честно отказывает. Журнал из одних
    # таких отказов перестал бы отвечать на вопрос, ради которого заведён,
    # поэтому тут хук уходит молча и файла не трогает. Про сам харнес без
    # квоты говорят agentctl harness и хвост вердикта pick.
    quiet=""
    case "$code:$out" in
    0:*) ;;
    *"[quota] пуста"*) quiet=1 ;;
    esac
    if [ -z "$quiet" ]; then
        # Запись копится хвостом: прежний запуск не затирается, отбивка
        # разделяет записи, и по журналу видно всю цепочку отказов, а не
        # только последний (DK-457, инцидент 19.08 разбирался три захода
        # именно потому, что журнал перезаписывался и след того дня стёрся).
        # Даже один printf может писать по строкам. Читатель получает
        # готовый снимок журнала только после замены соседнего файла.
        record=$({
            printf -- '---\n'
            date '+%Y-%m-%dT%H:%M:%S'
            echo "хук $here/quota-refresh.sh"
            printf '%s\n' "$out"
            echo "код возврата: $code"
        } 2>&1)
        publish_journal() (
            failed() {
                printf '%s\n' "ошибка записи журнала: $1" >"$dir/quota-refresh.error" 2>/dev/null
                exit 1
            }
            stage=$(mktemp -d "$dir/quota-refresh.XXXXXX") || failed подготовка
            trap 'rm -f "$stage/journal" "$stage/capped"; rmdir "$stage"' 0
            if [ -f "$log" ]; then
                tail -n +1 "$log" >"$stage/journal" || failed чтение
            else
                : >"$stage/journal" || failed подготовка
            fi
            printf '%s\n' "$record" >>"$stage/journal" || failed запись
            size=$(wc -c <"$stage/journal") || failed размер
            set -- $size
            case "${1:-}" in
            ''|*[!0-9]*) failed размер ;;
            esac
            if [ "$1" -gt "$log_limit" ]; then
                tail -n "$log_keep" "$stage/journal" >"$stage/capped" || failed обрезка
                mv "$stage/capped" "$stage/journal" || failed обрезка
            fi
            [ ! -d "$log" ] || failed публикация
            mv "$stage/journal" "$log" || failed публикация
            rm -f "$dir/quota-refresh.error"
        )
        publish_journal
    fi
    rmdir "$lock" 2>/dev/null
} </dev/null >/dev/null 2>&1 &
exit 0
