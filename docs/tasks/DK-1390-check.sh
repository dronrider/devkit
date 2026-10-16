#!/bin/sh
# Сценарий проверки DK-1390, шаги сухого прогона и замера объёма. Дом
# строится этим же скриптом. Строка сценария на обкатке идёт своим sh -c,
# переменных и файлов с других строк до неё не доезжает. Обкатка
# (taskctl rehearse) даёт шагам собственный временный HOME, поэтому дом
# кладётся под $HOME. Журнал сессий, журнал отметок и стоп-кран читаются
# из него же. Поток записан дублями без requestId, как пишут ответы
# glm-code и mimo, и часы простоя со склейкой проверяются на форме живых
# журналов. Рассчитан на чистый HOME. На живом доме он тронул бы журнал
# отметок и стоп-кран. Указатель харнеса DEVKIT_HARNESS снимается с прогонов
# нарочно: с ним обход сужается до одного дома харнеса, и сток-дом со
# синтетическим потоком выпадает из разбора.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
PROJ="$HOME/ckproj"
JOURNAL="$HOME/.devkit/turns.log"

# Дом из двух настоящих ходов (запись префикса и ход в кеш) и
# служебного пинга после них, всё дублями без requestId. Настоящая работа
# кончилась 690 секунд назад. Простой за пятиминутным TTL, но до порога
# выгоды далеко, и решение сухого прогона «продлевать». Служебная реплика
# берётся из context самого дерева. Разбор журналов узнаёт дословный текст.
python3 - "$ROOT" "$PROJ" <<'PY'
import json
import os
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

root, proj = sys.argv[1], Path(sys.argv[2])
proj.mkdir(parents=True, exist_ok=True)
sys.path.insert(0, os.path.join(root, "tools", "devkitctl"))
import context

logs = Path.home() / ".claude" / "projects" / context.slug(str(proj.resolve()))
logs.mkdir(parents=True, exist_ok=True)
now = datetime.now(timezone.utc)


def stamp(offset, ms):
    return (now + timedelta(seconds=offset)).strftime(
        "%Y-%m-%dT%H:%M:%S.") + "%03dZ" % ms


def answer(write, read, inp, out, ts, uuid):
    return {"type": "assistant", "uuid": uuid, "timestamp": ts,
            "message": {"model": "m1", "usage": {
                "cache_creation_input_tokens": write,
                "cache_read_input_tokens": read,
                "input_tokens": inp, "output_tokens": out}}}


def dup(write, read, inp, out, offset):
    """Две копии одного ответа: uuid разные, время в долях секунды."""
    return [answer(write, read, inp, out, stamp(offset, 100), "a%d" % offset),
            answer(write, read, inp, out, stamp(offset, 300), "b%d" % offset)]


def human(offset, text):
    return {"type": "user", "uuid": "u%d" % offset, "timestamp": stamp(offset, 0),
            "message": {"role": "user", "content": text}}


records = (
    [human(-700, "настоящая реплика")]
    + dup(200000, 0, 50, 10, -700)
    + [human(-690, "вторая настоящая реплика")]
    + dup(500, 199000, 30, 10, -690)
    + [human(-300, context.CACHEKEEP_PROMPT)]
    + dup(0, 199500, 20, 5, -300))

name = logs / "дом.jsonl"
with open(name, "w", encoding="utf-8") as f:
    for r in records:
        f.write(json.dumps(r, ensure_ascii=False) + "\n")
print("дом построен: %s" % name)
PY

echo "--- сухой прогон без стоп-крана"
out=$(env -u DEVKIT_HARNESS DEVKIT_TURN_MARK_LOG="$JOURNAL" python3 "$ROOT/tools/devkitctl/devkitctl.py" cachekeep -C "$PROJ" --dry-run 2>&1)
echo "$out"
echo "$out" | grep -q "продлено 1" || {
	echo "FAILED: решение продлевать не найдено, дубли мешают часам простоя"
	exit 1
}
if [ -s "$JOURNAL" ]; then
	echo "FAILED: сухой прогон пишет журнал отметок"
	exit 1
fi

echo "--- сухой прогон со стоп-краном"
mkdir -p "$HOME/.devkit"
printf 'cachekeep = off\n' > "$HOME/.devkit/watch.local"
out=$(env -u DEVKIT_HARNESS DEVKIT_TURN_MARK_LOG="$JOURNAL" python3 "$ROOT/tools/devkitctl/devkitctl.py" cachekeep -C "$PROJ" --dry-run 2>&1)
echo "$out"
echo "$out" | grep -q "продлено 0" || {
	echo "FAILED: стоп-кран не погасил продление"
	exit 1
}
if [ -s "$JOURNAL" ]; then
	echo "FAILED: журнал отметок не пуст при выключенном продлении"
	exit 1
fi

echo "--- объём потока с дублями по drain"
out=$(python3 "$ROOT/tools/devkitctl/devkitctl.py" drain -C "$PROJ" 2>&1)
echo "$out"
# Склеенные запросы читают кеш на 398500 токенов, задвоение дало бы 797K.
echo "$out" | grep -q "398.5K" || {
	echo "FAILED: чтение кеша посчитано не по склеенным запросам"
	exit 1
}
if echo "$out" | grep -q "797"; then
	echo "FAILED: дубли задвоены в объёме"
	exit 1
fi
echo "OK"
