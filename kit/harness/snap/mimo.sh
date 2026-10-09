#!/bin/sh
# Съёмщик использования Token Plan для agentctl quota refresh. Контракт съёмщика
# в docs/lld/DK-033-universal-kit.md, раздел «Контракт съёмщика».
#
# Провайдер типа «пакет кредитов на месяц». У подписки есть лимит кредитов и
# израсходованное число. Ответ эндпоинта использования отдаёт их списком строк
# с месячным срезом. Проценты в ответе грубые. Поэтому снимок несёт сами числа
# строками «N кред» (окно трат, остаток, израсходованное), а месячный бакет
# month_plan с грубым процентом достаётся корректору. Точный процент и темп
# считает показ блока квоты из чисел снимка.
#
# Эндпоинт использования открывается куками сессии учётной записи кабинета,
# ключ модели его не открывает. Куки лежат в хранилище секретов под собственным
# именем mimo-cabinet-cookie, а ключ модели остаётся под именем mimo-token.
# У этой подписки ключ и куки разные, и имя на двоих затирало бы единственную
# копию ключа. Скрипт перезапускает себя через secretctl exec и получает куки
# переменной окружения, значение не печатается. Адрес кабинета лежит в машинном
# конфиге квоты (~/.devkit/quota.local, ключ mimo-cabinet). Адрес в коммитируемые
# тексты не едет.
#
# Сессия обновляется самоходом, ручных обновлений куки не остаётся. Началом
# продления служит сам эндпоинт использования. Его 401-ответ несёт loginUrl
# с подписью платформы. По этому адресу цепочка с пасспортными куками из
# секрета mimo-passport-cookie переиздаёт serviceToken. Обратно командой
# secretctl set пишется только кабинетный секрет. Пасспортный остаётся
# исходным. Переизданный по дороге состав при обратной записи не принимается,
# а предъявленная кука ротациями жива. Если продление не вышло, съёмщик
# входит заново по логину и паролю из секретов mimo-cabinet-account и
# mimo-cabinet-password. Адрес аккаунта для входа лежит в машинном конфиге
# под ключом mimo-passport. Без пасспортных кук продление выключено, без
# адреса выключен вход по паролю. Без них съёмщик работает по-старому.
# Мёртвая кука это отказ. Когда сессия обновлялась в последний раз, это
# записано в файле <харнес>.renew.local в каталоге квоты. Плановое продление
# идёт раз в двенадцать часов и не чаще попытки в час. Отказ входа печатает
# свою причину (капча, подтверждение, неверный пароль), и во всех случаях
# снимок не тронут.
#
# Окно трат считается по истории сэмплов израсходованного, которую съёмщик
# ведёт рядом со снимком. Эндпоинт отдаёт только текущее число. За обычный
# запуск уходит один запрос использования. Платформа ловит серию запросов
# подряд ответом 429, и съёмщик не долбит. Продление добавляет к нему пару
# своих, добычу loginUrl и хвост цепочки, но идёт оно раз в двенадцать
# часов. Запросы аккаунта к этому не относятся, они уходят на другой хост
# и только когда сессия требует обновления.
set -eu

if [ -z "${DEVKIT_HARNESS:-}" ]; then
	echo "переменная DEVKIT_HARNESS пуста: имя харнеса передаёт agentctl quota refresh" >&2
	exit 1
fi

# Куки приезжают переменной окружения с дефисом в имени. Поэтому наличие
# проверяется printenv, а не подстановкой оболочки. Перезапуск через secretctl
# идёт один раз. После него переменная уже стоит.
if ! printenv mimo-cabinet-cookie >/dev/null 2>&1; then
	exec secretctl exec mimo-cabinet-cookie -- /bin/sh "$0"
fi

python3 - <<'PY'
import datetime
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from http.cookies import SimpleCookie

harness = os.environ["DEVKIT_HARNESS"]

def fail(why):
    sys.exit(why)

# Адреса кабинета и аккаунта берутся из машинного конфига квоты. Они
# машинные, как и куки. Ключ mimo-passport необязателен. Он нужен для входа
# по паролю, а продление идёт по адресу из ответа использования и включается
# пасспортными куками. Без того и другого съёмщик работает как прежде, до
# самопродления.
conf = pathlib.Path.home() / ".devkit" / "quota.local"
cfg = {}
try:
    for ln in conf.read_text(encoding="utf-8").splitlines():
        key, _, val = ln.partition("=")
        key = key.strip()
        if key.startswith("mimo-"):
            cfg[key] = val.strip()
except OSError:
    pass

cabinet = cfg.get("mimo-cabinet", "")
passport = cfg.get("mimo-passport", "")
# Схема не сужена до https сознательно. Тестовый стенд agentctl гоняет съёмщик
# на локальном http-сервере, живой кабинет стоит адресом с https.
if not (cabinet.startswith("https://") or cabinet.startswith("http://")):
    fail("в %s нет строки mimo-cabinet = <адрес кабинета>: адрес эндпоинта "
         "использования живёт в машинном конфиге, не в репозитории" % conf)
if passport and not (passport.startswith("https://") or passport.startswith("http://")):
    fail("ключ mimo-passport в %s не похож на адрес: жду http-адрес аккаунта "
         "либо пустой ключ без обновления сессии" % conf)

cookie = os.environ.get("mimo-cabinet-cookie", "").strip()
if not cookie:
    fail("секрет mimo-cabinet-cookie пуст: положите в него куки сессии кабинета целиком")
if "serviceToken=" not in cookie:
    fail("в mimo-cabinet-cookie не куки кабинета: значения без serviceToken= "
         "эндпоинт не открываются, положите заголовок Cookie из кабинета")

now = datetime.datetime.now().replace(second=0, microsecond=0)

UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)"
SID = "api-platform"
TOKEN_KEY = SID + "_serviceToken"

def pairs_of(header):
    out = {}
    for chunk in (header or "").split(";"):
        chunk = chunk.strip()
        if "=" in chunk:
            k, _, v = chunk.partition("=")
            out[k.strip()] = v.strip()
    return out

def header_of(pairs):
    return "; ".join("%s=%s" % kv for kv in pairs.items())

def secret_value(name):
    # Значение секрета живёт в окружении или приходит от secretctl exec,
    # наружу оно не попадает ни отсюда, ни в ошибки ниже.
    v = os.environ.get(name, "").strip()
    if v:
        return v
    try:
        p = subprocess.run(["secretctl", "exec", name, "--", "printenv", name],
                           capture_output=True)
    except OSError:
        return None
    if p.returncode != 0:
        return None
    return p.stdout.decode("utf-8", "replace").strip() or None

# Пасспортные куки читаются тем же ленивым способом. Секрета может не быть,
# тогда продление выключено, а съём идёт по действующей куке кабинета до её
# истечения.
passport_cookie = secret_value("mimo-passport-cookie") or ""

def store_secret(name, value):
    # Обратная кладка в хранилище. Съёмщик это та обвязка, которая умеет
    # записать, и отказ здесь равен отказу съёмщика целиком. Иначе свежая
    # сессия жила бы один запуск, а поломка записи была бы неотличима от
    # штатной работы.
    try:
        p = subprocess.run(["secretctl", "set", name],
                           input=value.encode("utf-8"), capture_output=True)
    except OSError as e:
        fail("свежие куки записать не удалось (нет secretctl): %s" % e)
    if p.returncode != 0:
        why = p.stderr.decode("utf-8", "replace").strip() or "причины в stderr нет"
        fail("свежие куки записать в секрет %s не удалось: %s" % (name, why))

def domain_match(domain, host):
    """Кука принадлежит хосту, когда это он сам или его поддомен. Порядок
    тот же, что в браузере. Цепочка продления несёт пасспортные куки
    мимо их домена."""
    domain = (domain or "").lower().lstrip(".")
    host = (host or "").lower()
    return bool(domain) and (host == domain or host.endswith("." + domain))

def http_call(method, url, cookie_header, hops=6):
    """Один переход по цепочке без доверия urllib-редиректам. Свой цикл нужен,
    чтобы собирать Set-Cookie по дороге, не теряя обновление сессии. У каждой
    куки записан домен, в котором её поставили, и на хост уходит только своя
    доля. Отдаёт код, тело, словарь кук, домены кук и отказ."""
    state = pairs_of(cookie_header)
    seed_host = urllib.parse.urlparse(url).netloc
    domains = dict.fromkeys(state, seed_host)
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    opener = urllib.request.build_opener(NoRedirect)
    for _ in range(hops):
        host = urllib.parse.urlparse(url).netloc
        outgoing = header_of({k: v for k, v in state.items()
                              if domain_match(domains.get(k, ""), host)})
        req = urllib.request.Request(url, method=method)
        req.add_header("User-Agent", UA)
        req.add_header("Accept", "application/json, text/plain, */*")
        if outgoing:
            req.add_header("Cookie", outgoing)
        headers, body, code = None, b"", 0
        try:
            with opener.open(req, timeout=30) as resp:
                headers, body, code = resp.headers, resp.read(), resp.status
        except urllib.error.HTTPError as e:
            headers, body, code = e.headers, e.read(), e.code
        except OSError as e:
            return 0, b"", state, domains, "запрос к %s не прошёл: %s" % (url, e)
        for sc in (headers.get_all("Set-Cookie") or []):
            jar = SimpleCookie()
            try:
                jar.load(sc)
            except Exception:
                continue
            for k, morsel in jar.items():
                state[k] = morsel.value
                d = (morsel.get("domain") or "").strip().lower().lstrip(".")
                domains[k] = d or host
        newurl = headers.get("Location")
        if code in (301, 302, 303, 307, 308) and newurl:
            url = urllib.parse.urljoin(url, newurl)
            if code == 303:
                method = "GET"
            continue
        return code, body, state, domains, None
    return 0, b"", state, domains, "цепочка редиректов к %s не замкнулась за %d шагов" % (url, hops)

def jsonp(body):
    text = body.decode("utf-8", "replace").strip()
    if text.startswith("&&&START&&&"):
        text = text[len("&&&START&&&"):]
    try:
        doc = json.loads(text)
    except ValueError:
        return None
    return doc if isinstance(doc, dict) else None

def usage_login_url(cookie_header):
    """Мёртвый токен в запросе использования отвечает 401 с loginUrl. loginUrl
    это адрес аккаунта, подписанный платформой своим callback на sts. Дорога
    продления берётся оттуда целиком, подпись платформы со стороны не
    повторяется."""
    pairs = pairs_of(cookie_header)
    pairs[TOKEN_KEY] = "renew-probe"
    code, body, _state, _domains, err = http_call(
        "GET", cabinet.rstrip("/") + "/tokenPlan/usage", header_of(pairs))
    if err:
        return None, err
    doc = jsonp(body)
    login_url = (doc or {}).get("loginUrl")
    if code != 401 or not login_url:
        return None, "жду 401 с loginUrl от использования, а пришло код %s" % code
    return login_url, None

def renew_session(cookie_header, passport_cookie):
    """Продление идёт по дороге loginUrl. Цепочка по подписанному адресу
    аккаунта с пасспортными куками переиздаёт serviceToken. Успех это свежий
    токен в собранных куках и ответ использования 200 в хвосте цепочки.
    Тот же запрос использования съёмщик получает даром, тело возвращается
    вместе с куками. Переизданные по дороге куки аккаунта в ответ не берутся,
    обратно идёт только кабинетная доля."""
    login_url, why = usage_login_url(cookie_header)
    if not login_url:
        return None, None, why
    if not passport_cookie:
        return None, None, "нет пасспортных кук в секрете mimo-passport-cookie"
    code, body, state, domains, err = http_call("GET", login_url, passport_cookie)
    if err:
        return None, None, err
    if not state.get(TOKEN_KEY):
        return None, None, "после продления нет %s" % TOKEN_KEY
    if code != 200:
        return None, None, "цепочка продления кончилась кодом %s" % code
    login_host = urllib.parse.urlparse(login_url).netloc
    cab_host = urllib.parse.urlparse(cabinet).netloc
    fresh_cab = {k: v for k, v in state.items()
                 if domain_match(domains.get(k, ""), cab_host)}
    if TOKEN_KEY not in fresh_cab:
        # Подпись callback ведёт на хост, отличный от кабинетного. Куки
        # платформы тогда берутся всем, что не пасспортное.
        fresh_cab = {k: v for k, v in state.items()
                     if not domain_match(domains.get(k, ""), login_host)}
    if TOKEN_KEY not in fresh_cab:
        return None, None, "собранные куки кабинета без %s" % TOKEN_KEY
    usage_body = body.decode("utf-8", "replace")
    return header_of(fresh_cab), usage_body, None

def login(passport_base, account, password):
    """Вход по паролю. serviceLogin отдаёт подпись, Auth2 её принимает с
    md5-хешем пароля, цепочка по location сажает куки кабинета."""
    base = passport_base.rstrip("/")
    code, body, state, _domains, err = http_call("GET",
        base + "/pass/serviceLogin?sid=%s&_json=true" % SID, None)
    if err:
        return None, err
    doc = jsonp(body)
    if not doc:
        return None, "serviceLogin не отдал JSON"
    qs, sign = doc.get("qs"), doc.get("_sign")
    callback = doc.get("callback") or "https://platform.xiaomimimo.com/sts"
    if not qs or not sign:
        return None, "в serviceLogin нет qs и _sign"
    params = urllib.parse.urlencode({
        "sid": SID,
        "hash": hashlib.md5(password.encode("utf-8")).hexdigest().upper(),
        "callback": callback,
        "qs": qs,
        "user": account,
        "_sign": sign,
        "_json": "true",
    })
    code, body, state, _domains, err = http_call("POST",
        base + "/pass/serviceLoginAuth2?" + params, None)
    if err:
        return None, err
    doc = jsonp(body)
    if not doc:
        return None, "Auth2 не отдал JSON"
    if doc.get("notificationUrl") or doc.get("captchaUrl"):
        return None, "вход требует подтверждение на стороне аккаунта"
    if doc.get("code") != 0:
        why = doc.get("description") or doc.get("desc") or "код %s" % doc.get("code")
        return None, "вход по паролю не прошёл: %s" % why
    location = doc.get("location")
    if not location:
        return None, "в Auth2 нет location"
    code, body, state, _domains, err = http_call("GET", location, None)
    if err:
        return None, err
    if not state.get(TOKEN_KEY):
        return None, "после входа нет %s" % TOKEN_KEY
    return header_of(state), None

# В файле обновлений сессии две метки, обе минутные, как и снимок. renewed
# пишется только при удаче, attempted при любой попытке, плановое продление
# стоит между ними. Оно идёт раз в двенадцать часов, но не чаще попытки в
# час. Без порога съём каждые десять минут долбил бы аккаунт.
renew_file = pathlib.Path.home() / ".devkit" / "quota" / (harness + ".renew.local")
RENEW_AFTER = datetime.timedelta(hours=12)
RETRY_AFTER = datetime.timedelta(hours=1)

def renew_marks():
    marks = {}
    try:
        for ln in renew_file.read_text(encoding="utf-8").splitlines():
            key, _, val = ln.partition("=")
            if key.strip() in ("renewed", "attempted"):
                try:
                    marks[key.strip()] = datetime.datetime.strptime(
                        val.strip(), "%Y-%m-%dT%H:%M")
                except ValueError:
                    pass
    except OSError:
        pass
    return marks

def renew_mark(key, when):
    marks = renew_marks()
    marks[key] = when
    renew_file.parent.mkdir(parents=True, exist_ok=True)
    tmp = renew_file.with_suffix(".local.tmp")
    tmp.write_text("".join("%s=%s\n" % (k, v.strftime("%Y-%m-%dT%H:%M"))
                           for k, v in sorted(marks.items())), encoding="utf-8")
    os.replace(tmp, renew_file)

def renew_due(marks):
    if not passport_cookie:
        return False
    renewed = marks.get("renewed")
    attempted = marks.get("attempted")
    if renewed is None or now - renewed > RENEW_AFTER:
        if attempted is None or now - attempted >= RETRY_AFTER:
            return True
    return False

# Плановое продление идёт раньше запроса использования. Кука, пролежавшая
# больше половины суток, переиздаётся заранее, и съём дальше идёт по свежей.
# Отказ продления не валит запуск. Кука ещё может быть жива, это покажет
# сам usage. Цепочка продления кончается запросом использования, и его тело
# съёмщик берёт даром, без второго похода к эндпоинту.
status, payload = None, None
if renew_due(renew_marks()):
    renew_mark("attempted", now)
    fresh, used_body, _why = renew_session(cookie, passport_cookie)
    if fresh:
        store_secret("mimo-cabinet-cookie", fresh)
        cookie = fresh
        renew_mark("renewed", now)
        if used_body:
            status, payload = "ok", used_body

def usage_once(cookie_header):
    """Один запрос использования. Исправ («ok», тело), отказ сессии
    («auth», None), ответ платформы («http», код) или сеть («net», текст)."""
    req = urllib.request.Request(cabinet.rstrip("/") + "/tokenPlan/usage", method="GET")
    req.add_header("User-Agent", UA)
    req.add_header("Accept", "application/json, text/plain, */*")
    req.add_header("Cookie", cookie_header)

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    opener = urllib.request.build_opener(NoRedirect)
    try:
        with opener.open(req, timeout=30) as resp:
            return "ok", resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        if e.code in (301, 302, 303, 307, 401):
            return "auth", None
        return "http", e.code
    except OSError as e:
        return "net", str(e)

# Один запрос использования за запуск, без повторов по пустяку. Платформа
# ловит серию 429. Если плановое продление уже принесло ответ использования
# в хвосте цепочки, запрос не повторяется; после смены кук в ходе запуска
# уходит ровно одна попытка.
if status is None:
    status, payload = usage_once(cookie)
why = None
if status == "auth" and passport_cookie:
    fresh, used_body, why = renew_session(cookie, passport_cookie)
    if fresh:
        store_secret("mimo-cabinet-cookie", fresh)
        cookie = fresh
        renew_mark("attempted", now)
        renew_mark("renewed", now)
        if used_body:
            status, payload = "ok", used_body
        else:
            status, payload = usage_once(cookie)
if status == "auth" and passport:
    account = secret_value("mimo-cabinet-account")
    login_pass = secret_value("mimo-cabinet-password")
    if account and login_pass:
        fresh, login_why = login(passport, account, login_pass)
        why = login_why or why
        if fresh:
            store_secret("mimo-cabinet-cookie", fresh)
            cookie = fresh
            renew_mark("attempted", now)
            renew_mark("renewed", now)
            status, payload = usage_once(cookie)
    else:
        why = "нет секретов mimo-cabinet-account и mimo-cabinet-password"
if status == "auth":
    tail = ": %s" % why if why else ""
    fail("сессия кабинета истекла%s; снимок не тронут" % tail)
if status == "http":
    fail("эндпоинт использования ответил %s" % payload)
if status == "net":
    fail("запрос использования не прошёл: %s" % payload)
body = payload

try:
    doc = json.loads(body)
    item = doc["data"]["monthUsage"]["items"][0]
    used, limit = int(item["used"]), int(item["limit"])
except (ValueError, KeyError, TypeError, IndexError):
    fail("ответ не похож на использование Token Plan: %s" % body[:200])
if limit <= 0 or used < 0:
    fail("ответ принёс лимит %s и израсходованное %s, из них не собрать процент"
         % (limit, used))

# По истории сэмплов израсходованного считается окно трат. Эндпоинт отдаёт
# только текущее число. Правила те же, что у съёмщика routerai. Сэмпл этой
# минуты заменяет прошлый, старше окна остаётся один базовый, ломаная строка
# историю обнуляет и окно набирается заново.
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
# история моложе окна, и окно считается от старейшего сэмпла. Оно короче
# пяти часов, но размер окна печатается в самой строке. Пополнение кредитов
# тратой не считается. Отрицательная дельта это принесённые кредиты, окно
# считается от следующего расхода.
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

# Сброс месячного окна это конец календарного месяца (разбор «окно темпа»
# задачи DK-1304). Ровный расход до него считается из остатка, отдельного
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
