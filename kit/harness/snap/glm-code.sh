#!/bin/sh
# Съёмщик остатка GLM Coding Plan (z.ai) для agentctl quota refresh. Контракт
# съёмщика в docs/lld/DK-033-universal-kit.md, раздел «Контракт съёмщика»;
# какое окно ответа каким бакетом становится, разобрано в LLD DK-090, раздел
# «Остаток второй подписки».
#
# Токен и base URL берутся из settings.json каталога конфигурации харнеса
# (переменная DEVKIT_HARNESS_HOME от agentctl): это тот же токен, которым
# работает клиент, отдельного ключа у подписки нет. На stdout попадает только
# текст снимка, токен не печатается.
set -eu

if [ -z "${DEVKIT_HARNESS_HOME:-}" ]; then
	echo "переменная DEVKIT_HARNESS_HOME пуста: каталог конфигурации харнеса передаёт agentctl quota refresh" >&2
	exit 1
fi
settings="$DEVKIT_HARNESS_HOME/settings.json"
if [ ! -f "$settings" ]; then
	echo "нет $settings: токен подписки живёт в настройках клиента этого каталога" >&2
	exit 1
fi

# Две строки: origin для запроса и токен. Эндпоинт мониторинга чужой для API
# сообщений, поэтому путь не склеивается с base URL, а берётся от origin.
pair=$(python3 - "$settings" <<'PY'
import json, sys, urllib.parse

try:
    env = json.load(open(sys.argv[1]))["env"]
    split = urllib.parse.urlsplit(env["ANTHROPIC_BASE_URL"])
    token = env["ANTHROPIC_AUTH_TOKEN"]
except (OSError, ValueError, KeyError) as e:
    sys.exit("в %s не нашлось пары ANTHROPIC_BASE_URL и ANTHROPIC_AUTH_TOKEN: %s"
             % (sys.argv[1], e))
if not split.scheme or not split.netloc:
    sys.exit("в ANTHROPIC_BASE_URL нет хоста: %r" % env["ANTHROPIC_BASE_URL"])
if not token:
    sys.exit("ANTHROPIC_AUTH_TOKEN пуст")
print("%s://%s" % (split.scheme, split.netloc))
print(token)
PY
)
origin=$(printf '%s\n' "$pair" | sed -n 1p)
token=$(printf '%s\n' "$pair" | sed -n 2p)

# Токен уезжает конфигом по пайпу, а не флагом: argv процесса виден в ps.
resp=$(printf 'header = "Authorization: Bearer %s"\n' "$token" | \
	curl -fsS --max-time 30 --config - \
	"$origin/api/monitor/usage/quota/limit") || {
	echo "запрос остатка к $origin не прошёл: токен протух либо эндпоинт сменился" >&2
	exit 1
}

# Оба окна опознаются парой unit/number из ответа, а не расстоянием до сброса:
# недельное окно перед своим сбросом короче живого пятичасового. Незнакомая
# пара это отказ, а не догадка: снимок с перепутанными окнами двигал бы
# вердикты в обратную сторону. Повтор окна это тоже отказ, а не молчаливая
# вторая строка: парсер agentctl взял бы первую без предупреждения. Проценты
# считаются из сырых чисел, а не из обрезанного поля percentage: панель
# округляет вниз.
python3 - "$resp" <<'PY'
import datetime, json, sys

# Длина окна нужна нетронутому: пока по окну не потрачено ни кредита, z.ai
# времени сброса не присылает вовсе, и посчитать его больше не из чего.
windows = {(3, 5): ("window5h_all", datetime.timedelta(hours=5)),
           (6, 1): ("week_all", datetime.timedelta(days=7))}
try:
    limits = json.loads(sys.argv[1])["data"]["limits"]
except (ValueError, KeyError, TypeError):
    sys.exit("ответ не похож на квоту z.ai: %s" % sys.argv[1][:200])
now = datetime.datetime.now().replace(second=0, microsecond=0)
print("taken = " + now.strftime("%Y-%m-%dT%H:%M"))
seen = set()
for lim in limits:
    if lim.get("type") != "CREDIT_LIMIT":
        continue
    window = windows.get((lim.get("unit"), lim.get("number")))
    if window is None:
        sys.exit("в ответе незнакомое окно unit=%s number=%s, разбор отказан"
                 % (lim.get("unit"), lim.get("number")))
    name, span = window
    if name in seen:
        sys.exit("окно %s приехало в ответе дважды" % name)
    ceiling, used = lim.get("usage"), lim.get("currentValue")
    if not ceiling or used is None:
        sys.exit("у окна %s нет расходов (usage=%s currentValue=%s)" % (name, ceiling, used))
    pct = min(100, int(used * 100 / ceiling + 0.5))
    # Часы окна пускает первая трата, до неё сброс не назначен и в ответе его
    # нет. Отказывать тут нельзя: одно нетронутое окно уносило снимок целиком,
    # и остаток второй подписки застывал на сутки. Нетронутому окну сброс
    # считается от его длины, потраченному без даты сброса верить нечему.
    if "nextResetTime" not in lim:
        if used:
            sys.exit("у окна %s потрачено %s, а времени сброса нет" % (name, used))
        reset = now + span
    else:
        reset = datetime.datetime.fromtimestamp(lim["nextResetTime"] / 1000)
    print("%s = %d%% сброс %s" % (name, pct, reset.strftime("%Y-%m-%dT%H:%M")))
    seen.add(name)
if seen != {name for name, _ in windows.values()}:
    sys.exit("в ответе не оба окна подписки: %s" % ", ".join(sorted(seen)))
PY
