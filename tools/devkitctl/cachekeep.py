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

Продлевающий запрос несёт тот же resume и модель, что у потока, и короткий
хвост `-p`: тот же префикс читается по ставке чтения, TTL кеша продлевается.
Строка журнала «продлил» пишется только о запросе, который реально ушёл;
отказ отправки виден строкой stderr, молчание за работу не считается.
На стенде отправка подменяется заглушкой: синтетический поток держит
механику, падение перезаписи видно на настоящем заходе.

Мёртвое дерево не разбирается и не продлевается тысячами запросов. Решение
по цене уже гасит продление само: простой длиннее порога пересечения делает
перезапись дешевле, и запрос не уходит. Строка «истёк» пишется только пока
решение свежее (простой в пределах окна истечения), а не о каждом обходе
давно остывшего потока. Тик сторожка зовёт обход с горизонтом разбора
(`--max-idle`): потоки, чей журнал не менялся дольше горизонта, пропускаются
до разбора файла.
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
# Короткий запрос продления: один символ, ответ модели не нужен. Запрос читает
# тот же префикс по ставке чтения и продлевает TTL кеша.
KEEPALIVE_PROMPT = "кэш"
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
    """Цена поддержания кеша за простой: продления по ставке чтения."""
    return pings_needed(idle_seconds, ttl) * prefix_tokens * CACHE_READ_RATE


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


def detect_ttl(reqs):
    """Какой TTL у кеша потока: часовой, если среди перезаписей есть пауза
    длиннее пятиминутного порога но меньше двух часов (подпись часового TTL).
    """
    gap_map = dict(context.gaps(reqs))
    for i in context.rewrites(reqs):
        g = gap_map.get(i, 0)
        if TTL_SHORT < g <= TTL_LONG * 1.5:
            return TTL_LONG
    return TTL_SHORT


def keepalive_argv(session, model, resume=None):
    """Команда продлевающего запроса: resume профиля плюс короткий запрос.

    {session} и {model} подставляются, как в ResumeCommand; модель обязана
    быть той же, что у потока, иначе смена модели перепишет префикс (кейс 6).
    Ключ -p едет хвостом: за `--` обёртки agentctl exec он попадает клиенту.
    """
    base = resume if resume else DEFAULT_RESUME
    out = [s.replace("{session}", session).replace("{model}", model) for s in base]
    return out + ["-p", KEEPALIVE_PROMPT]


def send_keepalive(session, model, root, resume=None, home=None, timeout=120):
    """Продлевающий запрос через клиент харнеса. True, если запрос ушёл.

    resume это head.resume профиля харнеса; без него берётся DEFAULT_RESUME.
    Отказ запуска и ненулевой код клиента это False: строка «продлил» после
    этого не пишется. На стенде функция подменяется заглушкой.
    """
    cmd = keepalive_argv(session, model, resume=resume)
    env = dict(os.environ)
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

    now это момент счёта, на стенде подставляется вместо живого. Простой считается
    от последнего запроса потока до now.
    """
    reqs = context.requests(path)
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


def journal_line(stream, keep, cost, idle, harness_name=None):
    """Строка журнала о решении механики: видна снаружи, молчание не считается.

    Формат именованными полями как у turn-mark.py, слово хода «кэш». Журнал
    один на машину (~/.devkit/turns.log) и по домам подписок не расползается;
    нестоковый харнес назван в строке, чтобы дома различались. Отказ записи
    не глотается: уходит строкой stderr, и след механики теряется громко,
    а не молча (замечание 7 ревью DK-1312).
    """
    log = os.environ.get(TURNS_ENV) or TURNS_LOG
    from datetime import datetime, timezone
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
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


def scan_home(entry, root, now=None, sender=None, dry_run=False, max_idle=None):
    """Обход одного дома журналов: решения по потокам и продлевающие запросы.

    entry это запись из project_homes: дом, харнес и его resume. Продлевающий
    запрос уходит по каждому потоку, где решение «продлевать» и простой
    длиннее TTL (pings > 0), клиентом харнеса этого дома. Строка «продлил»
    пишется только о реально ушедшем запросе; строка «истёк» пишется, пока
    решение свежее (простой в окне истечения). max_idle это горизонт разбора
    в секундах: потоки, чей журнал не менялся дольше, пропускаются до чтения
    файла, чтобы тик не разбирал чужие мёртвые деревья. sender это подмена
    отправки на стенде; dry_run принимает решения, ничего не отправляя.
    """
    directory = Path(entry["dir"])
    if not directory.is_dir():
        return []
    stamp = _epoch(now)
    out = []
    for path in context.streams(directory):
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
        if d["keep"] and d["pings"] > 0 and not dry_run:
            send = sender if sender is not None else send_keepalive
            sent = bool(send(d["stream"], d.get("model") or "", root,
                             resume=entry["resume"]))
        d["sent"] = sent
        d["expired"] = False
        if sent:
            journal_line(d["stream"], True, d["cost_keep"], d["idle"],
                         harness_name=entry["harness"])
        elif not d["keep"] and d["idle"] <= expiry_window(d["ttl"]):
            d["expired"] = True
            journal_line(d["stream"], False, d["cost_rewrite"], d["idle"],
                         harness_name=entry["harness"])
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
