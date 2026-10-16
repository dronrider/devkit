"""Поддержание кеша длинного потока в простое: дешёвое чтение против перезаписи.

Простой с истечением TTL кеша это главная причина перезаписи префикса (DK-1312):
1449 из 1613 событий перезаписи идут после простоя больше пяти минут и пишут
320.9M из 361M токенов. Прямой обход причины это дешёвое чтение кеша в простое:
пока поток простаивает, кеш продлевается коротким запросом, и следующий
настоящий ход читает его по ставке чтения, а не платит за перезапись.

Мера порога ротации это токены (развилка «мера порога» DK-1312, решена
исполнителем): ходы не отражают объём кеша, а готовая механика agentctl rotate
меряет токенами. Здесь та же мера: цена простоя считается по объёму префикса.

Цена поддержания не должна превышать цену перезаписи (кейс 5). Пока простой
укладывается в N интервалов TTL, каждый продлевающий запрос стоит 0.1x от
объёма префикса (ставка чтения кеша), а перезапись после истечения стоит 1.25x
(пятиминутный TTL) или 2x (часовой). Порог пересечения: 12.5 продлений для
пятиминутного TTL (простой до ~50 минут) и 20 для часового (до ~16 часов).
Дальше перезапись дешевле, и поддержание отключается.

Работа механики видна строкой журнала сессий (turns.log): молчание за работу
не считается (DoD DK-1312). Строка несёт сессию, решение («продлил» или
«простой дешевле перезаписи») и цену.

Интеграция: команда `devkitctl cachekeep` обходит журналы сессий проекта,
находит простаивающие потоки и принимает решение по каждому. Журналы лежат
не в одном дереве: сессии конвейера пишут в дом своего харнеса (машинный
слой `~/.devkit/harness.local`, ключ home секции), и обход видит их все,
сток-дом `~/.claude/projects` плюс дома включённых харнесов. У дома харнеса
журналы лежат сразу в `<home>/projects/<слепок>`, а не в `<home>/.claude`:
ключ home это каталог конфигурации клиента (CLAUDE_CONFIG_DIR). Продлевающий
запрос уходит через клиент того харнеса, чей дом нашли: resume читается из
профиля `kit/harness/<имя>.toml`, и обёртка `agentctl exec` кладёт пары
окружения подписки сама. Журнал отметок при этом один на машину
(`~/.devkit/turns.log`) и по домам подписок не расползается.

Продлевающий запрос несёт тот же resume и модель, что у потока, и хвост `-p`
со служебной репликой: тот же префикс читается по ставке чтения, TTL кеша
продлевается. Реплика запрещает отвечать и реагировать, ключ `--tools ""`
хвоста гасит инструменты клиента, а `DEVKIT_SILENT=1` в окружении глушит
хуки. Ход не поднимает диалог: в ленте остаётся одна короткая служебная
помета «продление кеша», она обновляется и не плодит строк, ответ модели
в разговор не идёт.
Строка журнала «продлил» пишется только о запросе, который реально ушёл;
отказ отправки виден строкой stderr, молчание за работу не считается.
На стенде отправка подменяется заглушкой: синтетический поток держит
механику, падение перезаписи видно на настоящем заходе.

Мёртвое дерево не разбирается и не продлевается тысячами запросов. Решение
по цене уже гасит продление само: простой длиннее порога пересечения делает
перезапись дешевле, и запрос не уходит. Часы простоя идут от последнего
настоящего хода: продлевающий ход тоже ложится в журнал запросом с usage,
и без этого исключения каждый пинг сбрасывал часы, не давая простою дойти
до порога (замечание 3 приёмки DK-1349). Строка «истёк» пишется только пока
решение свежее (простой в пределах окна истечения), а не о каждом обходе
давно остывшего потока. Тик сторожка зовёт обход с горизонтом разбора
(`--max-idle`): потоки, чей журнал не менялся дольше горизонта, пропускаются
до разбора файла.

Продление привязано к TTL, а не к тику сторожка (DK-1390): следующий пинг
уходит не раньше, чем через `ttl * PING_MARGIN` после предыдущего, и тик сам
по себе пинг не поднимает. Момент прошлого пинга читается из журнала
«продлил», запасом служит служебная реплика транскрипта; пинга не было
значит отсчёт от последнего настоящего хода, и первый пинг успевает на 80%
TTL, пока кеш жив. Дубли записей без `requestId` (glm-code, mimo)
склеиваются в разборе журналов по числам usage, поэтому часы простоя, объём
префикса и TTL считаются по настоящим ходам и на этих харнесах. Субагентские
потоки не продлеваются вовсе: возврата к ним не бывает, пинг чистый убыток.
Стоп-кран `cachekeep = off` в `~/.devkit/watch.local` выключает продление
целиком, не трогая остальные дела сторожка.
"""
import math
import os
import subprocess
import sys
import time
from pathlib import Path

import context
import harness
import rules
import sessions

# Ставки провайдера относительно базовой цены входа. Чтение кеша это 0.1x,
# запись на пятиминутном TTL 1.25x, на часовом 2x. Числа из замера DK-100.
CACHE_READ_RATE = 0.1
CACHE_WRITE_RATE = 1.25
CACHE_WRITE_RATE_LONG = 2.0
# Выход модели дороже входа: ответ на продление входит в цену хода (DoD
# DK-1349), хотя сам и сводится к молчаливому минимуму. Ставка выхода
# относительно базовой цены входа, как и ставки выше.
OUTPUT_RATE = 5.0
# Молчаливый минимум ответа модели на служебный пинг, токены: без
# инструментов и без слов ход закрывается сразу, счёт держит запас на
# служебный хвост клиента.
KEEPALIVE_REPLY_TOKENS = 16
# Пятиминутный TTL (секунды), умолчание провайдера.
TTL_SHORT = 300
# Часовой TTL (extended cache), встречается на длинных потоках.
TTL_LONG = 3600
# Запас до истечения TTL, когда уходит продлевающий запрос: 80% от TTL.
# Пинг на 80% оставляет запас на задержку сети и обработку.
PING_MARGIN = 0.8
# Журнал отметок механики: тот же файл, что у turn-mark.py.
TURNS_LOG = os.path.join(os.path.expanduser("~"), ".devkit", "turns.log")
TURNS_ENV = "DEVKIT_TURN_MARK_LOG"
# Продлевающий запрос: служебная реплика, ответ модели не нужен. Запрос
# читает тот же префикс по ставке чтения и продлевает TTL кеша. Текст реплики
# дословный, формулировка пользователя: он запрещает отвечать и реагировать,
# и живой агент узнаёт служебное продление без диалога (DK-1349). Формы
# реплики живут в context (одно место хранения на разбор журналов и на сам
# продлевающий запрос): лента дашборда узнаёт те же слова и старую рамку
# devkit-cachekeep из прошлых транскриптов, в чате встаёт одна короткая
# служебная помета.
KEEPALIVE_PROMPT = context.CACHEKEEP_PROMPT
# Resume умолчания, когда профиль харнеса не прочитан: то же, что head.resume
# профиля claude-code. Клиент читает его снаружи и передаёт сюда.
DEFAULT_RESUME = ["claude", "--resume", "{session}", "--model", "{model}"]
# Харнес сток-дома журналов: его home это сам ~, поэтому его журналы лежат
# в ~/.claude/projects. Дом идёт обходом первым, включён он в машинном слое
# или нет.
STOCK_HARNESS = "claude-code"


def _profiles_dir():
    """Каталог профилей харнесов того чекаута, из которого модуль работает."""
    return Path(__file__).resolve().parent.parent.parent / "kit" / "harness"


def harness_resume(name, profiles_dir=None):
    """head.resume профиля харнеса, DEFAULT_RESUME при его отсутствии.

    Продлевающий запрос обязан уходить клиентом того харнеса, чей дом нашли:
    resume несёт обёртку agentctl exec с парами окружения подписки.
    """
    path = Path(profiles_dir or _profiles_dir()) / ("%s.toml" % name)
    try:
        doc = harness.parse(path.name, path.read_text(encoding="utf-8"))
        got = doc.arr_of("head", "resume")
        if got:
            return got
    except (OSError, harness.TomlError):
        pass
    return list(DEFAULT_RESUME)


def project_homes(root, machine_path=None, profiles_dir=None):
    """Дома журналов проекта по харнесам машины.

    Возврат это список записей {harness, dir, resume}: сток-дом
    ~/.claude/projects/<слепок> первым, дальше дома включённых харнесов
    с ключом home в машинном слое, у каждого журналы лежат в
    <home>/projects/<слепок>. Слепок корня один и тот же в каждом доме.
    """
    stock = context.logs_dir(root)
    entries = [{
        "harness": STOCK_HARNESS,
        "dir": stock,
        "resume": harness_resume(STOCK_HARNESS, profiles_dir),
    }]
    homes = rules.machine_homes(machine_path)
    enabled, _findings = rules.enabled_harnesses(None, profiles_dir or _profiles_dir(),
                                                 machine_path)
    seen = {str(stock)}
    for name, _profile in enabled:
        home = homes.get(name)
        if not home:
            continue
        d = Path(home) / "projects" / context.slug(root)
        if str(d) in seen:
            continue
        seen.add(str(d))
        entries.append({
            "harness": name,
            "dir": d,
            "resume": harness_resume(name, profiles_dir),
        })
    return entries


def corpus_dirs(machine_path=None, profiles_dir=None):
    """Корни журналов всех домов машины: drain --all ходит по ним целиком."""
    dirs = [context.projects_dir()]
    homes = rules.machine_homes(machine_path)
    enabled, _findings = rules.enabled_harnesses(None, profiles_dir or _profiles_dir(),
                                                 machine_path)
    seen = {str(dirs[0])}
    for name, _profile in enabled:
        home = homes.get(name)
        if not home:
            continue
        d = Path(home) / "projects"
        if str(d) in seen:
            continue
        seen.add(str(d))
        dirs.append(d)
    return dirs


def pings_needed(idle_seconds, ttl):
    """Число продлевающих запросов за простой: по одному на каждый интервал TTL.

    Простой меньше одного TTL не требует продления: кеш ещё жив. Дальше
    продление уходит на 80% каждого интервала, чтобы пережить задержку сети.
    """
    if idle_seconds <= ttl:
        return 0
    interval = ttl * PING_MARGIN
    return max(1, math.ceil((idle_seconds - ttl) / interval))


def keepalive_cost(prefix_tokens, idle_seconds, ttl):
    """Цена поддержания кеша за простой: чтение префикса и ответ модели.

    Ответ входит в цену хода (DoD DK-1349): пинг не поднимает ход
    с инструментами, и ответ сводится к молчаливому минимуму.
    """
    return pings_needed(idle_seconds, ttl) * (
        prefix_tokens * CACHE_READ_RATE + KEEPALIVE_REPLY_TOKENS * OUTPUT_RATE)


def rewrite_cost(prefix_tokens, ttl):
    """Цена перезаписи префикса после истечения TTL."""
    rate = CACHE_WRITE_RATE if ttl <= TTL_SHORT else CACHE_WRITE_RATE_LONG
    return prefix_tokens * rate


def should_keepalive(prefix_tokens, idle_seconds, ttl):
    """Дешевле ли поддержание кеша, чем перезапись (кейс 5).

    Простой меньше TTL не требует решения: кеш жив, перезаписи не будет.
    """
    if idle_seconds <= ttl:
        return True
    return keepalive_cost(prefix_tokens, idle_seconds, ttl) < rewrite_cost(prefix_tokens, ttl)


def stream_prefix_tokens(reqs):
    """Объём префикса потока: первый запрос пишет его целиком."""
    if not reqs:
        return 0
    return reqs[0]["write"] + reqs[0]["read"] + reqs[0]["input"]


def _resend(cur, prev):
    """Переплачен ли вход: префикс уехал обычным входом при пустом чтении кеша.

    Провайдеры без `cache_creation` (mimo) пишут write нулём, и правило
    перезаписи в context.rewrites их подпись не видит. Мёртвый пинг узнаётся
    иначе: чтение пустое, а вход объёмом с прежний префикс, целиком
    оплаченный по базовой ставке (DK-1390).
    """
    prefix = prev["read"] + prev["write"] + prev["input"]
    return cur["read"] == 0 and prefix > 0 and cur["input"] * 2 > prefix


def detect_ttl(reqs):
    """Какой TTL у кеша потока: часовой, если среди перезаписей есть пауза
    длиннее пятиминутного порога но меньше двух часов (подпись часового TTL).

    Перезапись узнаётся обоими правилами: подпись записи из context.rewrites
    и переплаченный вход без записи кеша (_resend). Провайдер, не отдающий
    `cache_creation`, подписывает часовой кеш только второй формой.
    """
    gap_map = dict(context.gaps(reqs))
    known = set(context.rewrites(reqs))
    for i in range(1, len(reqs)):
        if i not in known and not _resend(reqs[i], reqs[i - 1]):
            continue
        g = gap_map.get(i, 0)
        if TTL_SHORT < g <= TTL_LONG * 1.5:
            return TTL_LONG
    return TTL_SHORT


def keepalive_argv(session, model, resume=None):
    """Команда продлевающего запроса: resume профиля плюс служебная реплика.

    {session} и {model} подставляются, как в ResumeCommand; модель обязана
    быть той же, что у потока, иначе смена модели перепишет префикс (кейс 6).
    Ключи едут хвостом: за `--` обёртки agentctl exec они попадают клиенту.
    `--tools ""` гасит инструменты: пинг не поднимает ход с инструментами,
    ответ модели сводится к молчаливому минимуму (DoD DK-1349).
    """
    base = resume if resume else DEFAULT_RESUME
    out = [s.replace("{session}", session).replace("{model}", model) for s in base]
    return out + ["--tools", "", "-p", KEEPALIVE_PROMPT]


def send_keepalive(session, model, root, resume=None, home=None, timeout=120):
    """Продлевающий запрос через клиент харнеса. True, если запрос ушёл.

    resume это head.resume профиля харнеса; без него берётся DEFAULT_RESUME.
    Отказ запуска и ненулевой код клиента это False: строка «продлил» после
    этого не пишется. На стенде функция подменяется заглушкой.
    """
    cmd = keepalive_argv(session, model, resume=resume)
    env = dict(os.environ)
    # Хуки devkit молчат на служебном вызове клиента: без флага каждый пинг
    # писал бы ложные отметки «ход начат/кончен» в реестр чатов (DoD DK-1349).
    # Тот же маркер ставит silentEnv в peers.go.
    env["DEVKIT_SILENT"] = "1"
    if home:
        env["HOME"] = str(home)
    try:
        p = subprocess.run(cmd, cwd=str(root), env=env, capture_output=True,
                           timeout=timeout)
        return p.returncode == 0
    except (OSError, subprocess.TimeoutExpired) as e:
        sys.stderr.write("cachekeep: продлевающий запрос %s не ушёл: %s\n"
                         % (session, e))
        return False


def decide_stream(path, now=None):
    """Решение по одному потоку: продлевать кеш или дать ему истечь.

    now это момент счёта, на стенде подставляется вместо живого. Простой
    считается от последнего настоящего хода потока до now: продлевающий ход
    сам ложится в журнал запросом с usage, и без фильтра каждый пинг
    сбрасывал часы простоя, держа решение вечно на «продлить» (замечание 3
    приёмки DK-1349). Служебные ходы из разбора потока исключены целиком:
    часы, detect_ttl и объём префикса считают по настоящим ходам.
    """
    reqs = context.real_requests(path)
    if not reqs:
        return None
    prefix = stream_prefix_tokens(reqs)
    ttl = detect_ttl(reqs)
    last_ts = context.parse_ts(reqs[-1].get("ts"))
    if last_ts is None:
        return None
    now = now or time.time()
    from datetime import datetime, timezone
    if isinstance(now, (int, float)):
        now_dt = datetime.fromtimestamp(now, tz=timezone.utc)
    else:
        now_dt = now
    idle = (now_dt - last_ts).total_seconds()
    if idle < 0:
        idle = 0
    keep = should_keepalive(prefix, idle, ttl)
    cost_k = keepalive_cost(prefix, idle, ttl)
    cost_r = rewrite_cost(prefix, ttl)
    if last_ts.tzinfo is None:
        from datetime import timezone
        last_ts = last_ts.replace(tzinfo=timezone.utc)
    return {
        "stream": path.stem,
        "prefix": prefix,
        "idle": idle,
        "ttl": ttl,
        "keep": keep,
        "cost_keep": cost_k,
        "cost_rewrite": cost_r,
        "pings": pings_needed(idle, ttl),
        "model": reqs[-1].get("model") or "",
        "last_ts": last_ts.timestamp(),
    }


def expiry_window(ttl):
    """Простой, за которым строка «истёк» уже не пишется, секунды.

    Кеш пущен под перезапись в момент пересечения порога выгоды, и строка
    отмечает этот момент, а не каждый обход давно остывшего потока: иначе
    мёртвое дерево в тысячу потоков писало бы строку на каждый тик. Окно это
    порог пересечения плюс один интервал продления, чтобы тик успел попасть
    внутрь.
    """
    rate = CACHE_WRITE_RATE if ttl <= TTL_SHORT else CACHE_WRITE_RATE_LONG
    spans = math.ceil(rate / CACHE_READ_RATE) + 1
    return ttl + spans * ttl * PING_MARGIN


def keep_horizon():
    """Горизонт разбора тика, секунды: окно истечения часового TTL.

    Тик режет мёртвые деревья, а не живое окно продления: для часового TTL
    продление дешевле перезаписи примерно до 16 часов простоя (порог
    пересечения), и окно истечения его покрывает. Дальше решать нечего,
    строка «истёк» не пишется.
    """
    return int(expiry_window(TTL_LONG))


def journal_line(stream, keep, cost, idle, harness_name=None, now=None):
    """Строка журнала о решении механики: видна снаружи, молчание не считается.

    Формат именованными полями как у turn-mark.py, слово хода «кэш». Журнал
    один на машину (~/.devkit/turns.log) и по домам подписок не расползается;
    нестоковый харнес назван в строке, чтобы дома различались. Отказ записи
    не глотается: уходит строкой stderr, и след механики теряется громко,
    а не молча (замечание 7 ревью DK-1312). now это момент решения: строка
    журнала служит памятью о пинге, и живое время вместо него разъезжалось
    бы с моментом, по которому решение принято.
    """
    log = os.environ.get(TURNS_ENV) or TURNS_LOG
    from datetime import datetime, timezone
    ts = datetime.fromtimestamp(_epoch(now), tz=timezone.utc).strftime(
        "%Y-%m-%dT%H:%M:%S")
    decision = "продлил" if keep else "истёк"
    whose = "" if harness_name in (None, STOCK_HARNESS) else " харнес %s" % harness_name
    line = "%s сессия %s ход кэш повод %s%s дерево %s\n" % (
        ts, stream, decision, whose, "простой %.0fs" % idle)
    try:
        os.makedirs(os.path.dirname(log), exist_ok=True)
        with open(log, "a", encoding="utf-8") as f:
            f.write(line)
    except OSError as e:
        sys.stderr.write("cachekeep: строка журнала не записана в %s: %s\n" % (log, e))
    return line


def _epoch(now):
    """Момент счёта секундами: now бывает числом, datetime или None (живое время)."""
    from datetime import datetime
    if now is None:
        return time.time()
    if isinstance(now, datetime):
        return now.timestamp()
    return now


def journal_pings(log=None):
    """Моменты последних продлевающих запросов по потокам из журнала отметок.

    Память о пинге (DK-1390): строка «продлил» с именем потока уже пишется
    журналом, и по ней следующий пинг уходит не раньше, чем через интервал
    продления. Журнал может резаться: после обрезки пинги учащаются не чаще
    тика, такой деградационный режим приемлем (развилка «память о пинге»).
    """
    path = log or os.environ.get(TURNS_ENV) or TURNS_LOG
    from datetime import datetime, timezone
    out = {}
    try:
        with open(path, encoding="utf-8") as f:
            text = f.read()
    except OSError:
        return out
    for ln in text.splitlines():
        if "повод продлил" not in ln:
            continue
        head, sep, tail = ln.partition(" сессия ")
        if not sep:
            continue
        name, sep2, _rest = tail.partition(" ")
        if not sep2:
            continue
        try:
            ts = datetime.strptime(head, "%Y-%m-%dT%H:%M:%S").replace(
                tzinfo=timezone.utc)
        except ValueError:
            continue
        got = out.get(name)
        if got is None or ts.timestamp() > got:
            out[name] = ts.timestamp()
    return out


def last_ping_epoch(path, decision, pings):
    """Момент, от которого тикает интервал продления потока.

    Это последний пинг: журнал отметок либо служебная реплика транскрипта,
    что позже. Пинга не было значит отсчёт от последнего настоящего хода:
    первый пинг уходит на 80% TTL, пока кеш ещё жив.
    """
    got = pings.get(decision["stream"])
    in_file = context.last_service_ts(path)
    if in_file is not None and (got is None or in_file > got):
        got = in_file
    if got is None:
        got = decision["last_ts"]
    return got


def extension_off(conf=None):
    """Выключено ли продление строкой `cachekeep = off` в ~/.devkit/watch.local.

    Стоп-кран (DK-1390): глушит продление целиком, ни один запрос не уходит
    и журнал отметок молчит, а сам сторожок продолжает тикать со всеми его
    остальными делами. Формат строки тот же, что у порогов сторожка.
    """
    path = Path(conf or os.path.join(os.path.expanduser("~"),
                                     ".devkit", "watch.local"))
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    for ln in text.splitlines():
        key, sep, val = ln.strip().partition("=")
        if sep and key.strip() == "cachekeep" and val.strip() == "off":
            return True
    return False


def scan_home(entry, root, now=None, sender=None, dry_run=False, max_idle=None):
    """Обход одного дома журналов: решения по потокам и продлевающие запросы.

    entry это запись из project_homes: дом, харнес и его resume. Продлевающий
    запрос уходит по каждому потоку с решением «продлевать», когда с прошлого
    пинга прошло не меньше ttl * PING_MARGIN: тик сторожка сам по себе пинг
    не поднимает, интервал считается от журнала «продлил» либо от последнего
    настоящего хода (DK-1390). Субагентские потоки не продлеваются вовсе:
    возврата к ним не бывает, пинг чистый убыток. Строка «продлил» пишется
    только о реально ушедшем запросе; строка «истёк» пишется, пока решение
    свежее (простой в окне истечения). Стоп-кран `cachekeep = off` в
    ~/.devkit/watch.local гасит и то и другое. max_idle это горизонт разбора
    в секундах: потоки, чей журнал не менялся дольше, пропускаются до чтения
    файла, чтобы тик не разбирал чужие мёртвые деревья. sender это подмена
    отправки на стенде; dry_run принимает решения, ничего не отправляя.
    """
    directory = Path(entry["dir"])
    if not directory.is_dir():
        return []
    stamp = _epoch(now)
    off = extension_off()
    pings = {} if off else journal_pings()
    out = []
    for path in context.streams(directory):
        if path.parent.name == "subagents":
            # К потоку субагента возврата не бывает: ожидаемая польза продления
            # ноль по построению, и любой пинг туда чистый убыток (DK-1390).
            continue
        if max_idle is not None:
            try:
                fresh = stamp - path.stat().st_mtime
            except OSError:
                fresh = 0.0
            if fresh > max_idle:
                continue
        d = decide_stream(path, now=now)
        if d is None:
            continue
        d["harness"] = entry["harness"]
        out.append(d)
        if d["idle"] <= 0:
            continue
        sent = False
        if d["keep"] and not dry_run and not off:
            ref = last_ping_epoch(path, d, pings)
            if stamp - ref >= d["ttl"] * PING_MARGIN:
                send = sender if sender is not None else send_keepalive
                sent = bool(send(d["stream"], d.get("model") or "", root,
                                 resume=entry["resume"]))
        d["sent"] = sent
        d["expired"] = False
        if off:
            continue
        if sent:
            journal_line(d["stream"], True, d["cost_keep"], d["idle"],
                         harness_name=entry["harness"], now=now)
        elif not d["keep"] and d["idle"] <= expiry_window(d["ttl"]):
            d["expired"] = True
            journal_line(d["stream"], False, d["cost_rewrite"], d["idle"],
                         harness_name=entry["harness"], now=now)
    return out


def scan_all(root, now=None, sender=None, dry_run=False, max_idle=None,
             machine_path=None, profiles_dir=None):
    """Обход всех домов журналов проекта: сток и дома включённых харнесов."""
    out = []
    for entry in project_homes(root, machine_path=machine_path, profiles_dir=profiles_dir):
        out += scan_home(entry, root, now=now, sender=sender, dry_run=dry_run,
                         max_idle=max_idle)
    return out


def scan_project(root, now=None, home=None, sender=None, resume=None, dry_run=False):
    """Обход сток-дома журналов проекта: разовый прогон и вход тестов.

    Дом нестандартного ~ задаётся home, остальное то же, что у scan_home.
    """
    entry = {
        "harness": STOCK_HARNESS,
        "dir": context.logs_dir(root, home=home),
        "resume": resume if resume is not None else harness_resume(STOCK_HARNESS),
    }
    return scan_home(entry, root, now=now, sender=sender, dry_run=dry_run)
