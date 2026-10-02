#!/bin/sh
# Съёмщик остатка routerai для agentctl quota refresh. Контракт съёмщика в
# docs/lld/DK-033-universal-kit.md, раздел «Контракт съёмщика»; чем подписка
# с балансом отличается от квотных окон, разобрано в LLD DK-090, раздел
# «Остаток второй подписки».
#
# Провайдер типа «оплата с баланса за токены»: квотных окон нет, деньги
# списываются с пополняемого баланса за каждый токен. Снимок у типа один на
# всех таких провайдеров: траты за пятичасовое окно и остаток баланса в
# рублях (строки со значением «N руб»), а процент бюджета отдельной строкой
# balance_all достаётся корректору и гейтам. routerai здесь первый
# представитель типа, следующему агрегатору достаётся тот же формат.
#
# Токен и base URL берутся из settings.json каталога конфигурации харнеса
# (переменная DEVKIT_HARNESS_HOME от agentctl): это тот же токен, которым
# работает клиент. Бюджет передаёт agentctl переменной DEVKIT_QUOTA_BUDGET:
# съёмщик сам делит потраченное на бюджет. Окно трат считается по истории
# сэмплов баланса, которую съёмщик ведёт рядом со снимком: эндпоинт помнит
# только текущий баланс. На stdout попадает только текст снимка, токен не
# печатается.
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
if [ -z "${DEVKIT_QUOTA_BUDGET:-}" ]; then
	echo "переменная DEVKIT_QUOTA_BUDGET пуста: бюджет подписки передаёт agentctl quota refresh из машинного слоя" >&2
	exit 1
fi

# Две строки: адрес запроса и токен. Эндпоинт баланса живёт под тем же base
# URL, что и API сообщений, поэтому путь склеивается с ним целиком, а не
# строится от голого хоста.
pair=$(python3 - "$settings" <<'PY'
import json, sys

try:
    env = json.load(open(sys.argv[1]))["env"]
    base, token = env["ANTHROPIC_BASE_URL"].rstrip("/"), env["ANTHROPIC_AUTH_TOKEN"]
except (OSError, ValueError, KeyError) as e:
    sys.exit("в %s не нашлось пары ANTHROPIC_BASE_URL и ANTHROPIC_AUTH_TOKEN: %s"
             % (sys.argv[1], e))
if not base.startswith(("http://", "https://")):
    sys.exit("в ANTHROPIC_BASE_URL нет хоста: %r" % env["ANTHROPIC_BASE_URL"])
if not token:
    sys.exit("ANTHROPIC_AUTH_TOKEN пуст")
print(base + "/v1/credits")
print(token)
PY
)
url=$(printf '%s\n' "$pair" | sed -n 1p)
token=$(printf '%s\n' "$pair" | sed -n 2p)

# Токен уезжает конфигом по пайпу, а не флагом: argv процесса виден в ps.
resp=$(printf 'header = "Authorization: Bearer %s"\n' "$token" | \
	curl -fsS --max-time 30 --config - "$url") || {
	echo "запрос баланса к $url не прошёл: токен протух либо эндпоинт сменился" >&2
	exit 1
}

history="${HOME}/.devkit/quota/${DEVKIT_HARNESS}.history.local"
python3 - "$resp" "$DEVKIT_QUOTA_BUDGET" "$history" <<'PY'
import datetime, json, os, sys

try:
    credits = json.loads(sys.argv[1])["data"]["credits"]
    budget = int(sys.argv[2])
except (ValueError, KeyError, TypeError):
    sys.exit("ответ не похож на баланс: %s" % sys.argv[1][:200])
if not isinstance(credits, (int, float)) or isinstance(credits, bool):
    sys.exit("баланс пришёл не числом: %r" % (credits,))
if budget <= 0:
    sys.exit("бюджет %s не положителен, процент не собрать" % budget)
now = datetime.datetime.now().replace(second=0, microsecond=0)

# История сэмплов, по ней считается окно трат: эндпоинт отдаёт только текущий
# баланс, а окно это разница с балансом пять часов назад. Ломаная строка
# историю обнуляет: окно набирается заново, врать пять часов вместо честного
# нуля смысла нет. Сэмплов старше окна держится ровно один, базовый: без него
# окно начиналось бы заново после каждого обновления.
samples = []
try:
    with open(sys.argv[3], encoding="utf-8") as f:
        for ln in f:
            ts, val = ln.split()
            samples.append((datetime.datetime.strptime(ts, "%Y-%m-%dT%H:%M"), float(val)))
except (OSError, ValueError):
    samples = []
# Сэмпл этой минуты заменяет свой же прошлый: обновления приходят чаще
# минуты, и копия не несла бы ничего нового, а строку в истории занимала.
samples = [s for s in samples if s[0] != now]
samples.append((now, float(credits)))
samples.sort()
edge = now - datetime.timedelta(hours=5)
base = [s for s in samples if s[0] <= edge]
kept = ([base[-1]] if base else []) + [s for s in samples if s[0] > edge]
os.makedirs(os.path.dirname(sys.argv[3]), exist_ok=True)
tmp = sys.argv[3] + ".tmp"
with open(tmp, "w", encoding="utf-8") as f:
    for ts, val in kept:
        f.write("%s %s\n" % (ts.strftime("%Y-%m-%dT%H:%M"), val))
os.replace(tmp, sys.argv[3])

# Траты окна идут от базового сэмпла до текущего баланса. Базы нет, значит
# история моложе окна, и окно считается от старейшего сэмпла: короче пяти
# часов, но честно. Пополнение баланса тратой не считается: отрицательная
# дельта это принесённые деньги, окно ждёт следующего расхода. Процент
# бюджета это доля потраченного от предоплаченной суммы, клэмп нуля и сотни:
# сервис держит небольшой минус за гранью нуля, а формат снимка рубли и доли
# принимает только неотрицательные.
anchor = base[-1][1] if base else kept[0][1]
window = max(0.0, anchor - float(credits))
spent = min(budget, max(0, budget - credits))
print("taken = " + now.strftime("%Y-%m-%dT%H:%M"))
print("balance_all = %d%%" % min(100, int(spent * 100 / budget + 0.5)))
print("window5h_rub = %d руб" % int(window + 0.5))
print("balance_rub = %d руб" % int(max(0.0, float(credits)) + 0.5))
PY
