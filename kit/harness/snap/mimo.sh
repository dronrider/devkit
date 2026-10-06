#!/bin/sh
# Съёмщик использования Token Plan для agentctl quota refresh. Контракт съёмщика
# в docs/lld/DK-033-universal-kit.md, раздел «Контракт съёмщика».
#
# Провайдер типа «пакет кредитов на месяц»: у подписки есть лимит кредитов и
# израсходованное число, ответ эндпоинта использования отдаёт их списком строк
# с месячным срезом. Проценты в ответе грубые, поэтому снимок несёт сами числа
# строками «N кред» (окно трат, остаток, израсходованное), а месячный бакет
# month_plan с грубым процентом достаётся корректору. Точный процент и темп
# считает показ блока квоты из чисел снимка.
#
# Эндпоинт использования открывается куками сессии учётной записи кабинета,
# ключ модели его не открывает. Куки лежат в хранилище секретов под именем
# mimo-token: скрипт перезапускает себя через secretctl exec и получает их
# переменной окружения, значение не печатается. Адрес кабинета живёт в машинном
# конфиге квоты (~/.devkit/quota.local, ключ mimo-cabinet), потому что адрес в
# коммитируемые тексты не едет. Окно трат считается по истории сэмплов
# израсходованного, которую съёмщик ведёт рядом со снимком: эндпоинт помнит
# только текущее число. За запуск уходит ровно один запрос: платформа ловит
# серию запросов подряд ответом 429, и съёмщик не долбит.
set -eu

if [ -z "${DEVKIT_HARNESS:-}" ]; then
	echo "переменная DEVKIT_HARNESS пуста: имя харнеса передаёт agentctl quota refresh" >&2
	exit 1
fi

# Куки приезжают переменной окружения с дефисом в имени, поэтому наличие
# проверяется printenv, а не подстановкой оболочки. Перезапуск через secretctl
# идёт один раз: после него переменная уже стоит.
if ! printenv mimo-token >/dev/null 2>&1; then
	exec secretctl exec mimo-token -- /bin/sh "$0"
fi

python3 - <<'PY'
import datetime
import json
import os
import pathlib
import sys
import urllib.error
import urllib.request

harness = os.environ["DEVKIT_HARNESS"]

def fail(why):
    sys.exit(why)

# Адрес кабинета из машинного конфига квоты: он машинный, как и куки.
conf = pathlib.Path.home() / ".devkit" / "quota.local"
cabinet = ""
try:
    for ln in conf.read_text(encoding="utf-8").splitlines():
        key, _, val = ln.partition("=")
        if key.strip() == "mimo-cabinet":
            cabinet = val.strip()
except OSError:
    pass
# Схема не сужена до https сознательно: тестовый стенд agentctl гоняет съёмщик
# на локальном http-сервере, живой кабинет стоит адресом с https.
if not (cabinet.startswith("https://") or cabinet.startswith("http://")):
    fail("в %s нет строки mimo-cabinet = <адрес кабинета>: адрес эндпоинта "
         "использования живёт в машинном конфиге, не в репозитории" % conf)

cookie = os.environ.get("mimo-token", "").strip()
if not cookie:
    fail("секрет mimo-token пуст: положите в него куки сессии кабинета целиком")
if "serviceToken=" not in cookie:
    fail("в mimo-token не куки кабинета: значения без serviceToken= ключом "
         "модели эндпоинт не открываются, положите заголовок Cookie из кабинета")

now = datetime.datetime.now().replace(second=0, microsecond=0)

# Один запрос за запуск, без повторов. Куки уходят заголовком запроса, а не
# аргументом команды: argv виден в ps.
req = urllib.request.Request(cabinet.rstrip("/") + "/tokenPlan/usage", method="GET")
req.add_header("Accept", "application/json, text/plain, */*")
req.add_header("Cookie", cookie)

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

opener = urllib.request.build_opener(NoRedirect)
try:
    with opener.open(req, timeout=30) as resp:
        body = resp.read().decode("utf-8", "replace")
except urllib.error.HTTPError as e:
    if e.code in (301, 302, 303, 307, 401):
        fail("сессия кабинета истекла: войдите в кабинет заново и обновите "
             "секрет mimo-token (куки сессии)")
    fail("эндпоинт использования ответил %s" % e.code)
except OSError as e:
    fail("запрос использования не прошёл: %s" % e)

try:
    doc = json.loads(body)
    item = doc["data"]["monthUsage"]["items"][0]
    used, limit = int(item["used"]), int(item["limit"])
except (ValueError, KeyError, TypeError, IndexError):
    fail("ответ не похож на использование Token Plan: %s" % body[:200])
if limit <= 0 or used < 0:
    fail("ответ принёс лимит %s и израсходованное %s, из них не собрать процент"
         % (limit, used))

# История сэмплов израсходованного, по ней считается окно трат: эндпоинт помнит
# только текущее число. Правила те же, что у съёмщика routerai: сэмпл этой
# минуты заменяет свой прошёл, старше окна держится один базовый, ломаная
# строка историю обнуляет и окно набирается заново.
history = pathlib.Path.home() / ".devkit" / "quota" / (harness + ".history.local")
samples = []
try:
    for ln in history.read_text(encoding="utf-8").splitlines():
        ts, val = ln.split()
        samples.append((datetime.datetime.strptime(ts, "%Y-%m-%dT%H:%M"), int(val)))
except (OSError, ValueError):
    samples = []
samples = [s for s in samples if s[0] != now]
samples.append((now, used))
samples.sort()
edge = now - datetime.timedelta(hours=5)
base = [s for s in samples if s[0] <= edge]
kept = ([base[-1]] if base else []) + [s for s in samples if s[0] > edge]
history.parent.mkdir(parents=True, exist_ok=True)
tmp = history.parent / (history.name + ".tmp")
tmp.write_text("".join("%s %d\n" % (ts.strftime("%Y-%m-%dT%H:%M"), val) for ts, val in kept),
               encoding="utf-8")
os.replace(tmp, history)

# Траты окна идут от базового сэмпла до текущего числа. Базы нет, значит
# история моложе окна, и окно считается от старейшего сэмпла: короче пяти
# часов, но честно, и размер окна подписывается в самой строке. Пополнение
# кредитов тратой не считается: отрицательная дельта это принесённые кредиты,
# окно ждёт следующего расхода.
anchor_ts, anchor_val = base[-1] if base else kept[0]
window = max(0, used - anchor_val)
span = now - anchor_ts
hours, minutes = span.total_seconds() // 3600, (span.total_seconds() % 3600) // 60
if hours >= 5:
    window_word = "5ч"
elif hours > 0:
    window_word = "%dч %dм" % (hours, minutes) if minutes else "%dч" % hours
elif minutes > 0:
    window_word = "%dм" % minutes
else:
    window_word = "0м"

# Сброс месячного окна это конец календарного месяца (разворка «окно темпа»
# задачи DK-1304): ровный расход до него считается из остатка, отдельного
# запроса под дату сброса съёмщик не делает.
if now.month == 12:
    reset = datetime.datetime(now.year + 1, 1, 1, 0, 0) - datetime.timedelta(minutes=1)
else:
    reset = datetime.datetime(now.year, now.month + 1, 1, 0, 0) - datetime.timedelta(minutes=1)

print("taken = " + now.strftime("%Y-%m-%dT%H:%M"))
print("month_plan = %d%% сброс %s" % (min(100, max(0, round(used * 100 / limit))), reset.strftime("%Y-%m-%dT%H:%M")))
print("window5h_cred = %d кред %s" % (window, window_word))
print("balance_cred = %d кред" % max(0, limit - used))
print("spent_cred = %d кред" % used)
PY
