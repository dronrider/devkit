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
находит простаивающие потоки и принимает решение по каждому. Продлевающий
запрос уходит через клиент харнеса (CLI) с тем же resume и моделью, что у
потока, и коротким запросом `-p`: тот же префикс читается по ставке чтения,
TTL кеша продлевается. Строка журнала «продлил» пишется только о запросе,
который реально ушёл; отказ отправки виден строкой stderr, молчание за
работу не считается. На стенде отправка подменяется заглушкой: синтетический
поток держит механику, падение перезаписи видно на настоящем заходе.
"""
import os
import subprocess
import sys
import time
from pathlib import Path

import context
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


def pings_needed(idle_seconds, ttl):
    """Число продлевающих запросов за простой: по одному на каждый интервал TTL.

    Простой меньше одного TTL не требует продления: кеш ещё жив. Дальше
    продление уходит на 80% каждого интервала, чтобы пережить задержку сети.
    """
    if idle_seconds <= ttl:
        return 0
    interval = ttl * PING_MARGIN
    import math
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


def journal_line(stream, keep, cost, idle, home=None):
    """Строка журнала о решении механики: видна снаружи, молчание не считается.

    Формат именованными полями как у turn-mark.py, слово хода «кэш».
    Отказ записи не глотается: уходит строкой stderr, и след механики
    теряется громко, а не молча (замечание 7 ревью DK-1312).
    """
    log = os.environ.get(TURNS_ENV) or TURNS_LOG
    if home:
        log = os.path.join(str(home), ".devkit", "turns.log")
    from datetime import datetime, timezone
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    decision = "продлил" if keep else "истёк"
    line = "%s сессия %s ход кэш повод %s дерево %s\n" % (
        ts, stream, decision, "простой %.0fs" % idle)
    try:
        os.makedirs(os.path.dirname(log), exist_ok=True)
        with open(log, "a", encoding="utf-8") as f:
            f.write(line)
    except OSError as e:
        sys.stderr.write("cachekeep: строка журнала не записана в %s: %s\n" % (log, e))
    return line


def scan_project(root, now=None, home=None, sender=None, resume=None, dry_run=False):
    """Обход журналов сессий проекта: решения по простаивающим потокам.

    Возвращает список решений (decide_stream). Продлевающий запрос уходит
    по каждому потоку, где решение «продлевать» и простой длиннее TTL
    (pings > 0). Строка «продлил» пишется только о реально ушедшем запросе;
    строка «истёк» пишется о решении не продлевать. sender это подмена
    отправки на стенде; dry_run принимает решения, ничего не отправляя.
    """
    directory = context.logs_dir(root, home=home)
    if not directory.is_dir():
        return []
    out = []
    for path in context.streams(directory):
        d = decide_stream(path, now=now)
        if d is None:
            continue
        out.append(d)
        if d["idle"] <= 0:
            continue
        sent = False
        if d["keep"] and d["pings"] > 0 and not dry_run:
            send = sender if sender is not None else send_keepalive
            sent = bool(send(d["stream"], d.get("model") or "", root,
                             resume=resume, home=home))
        d["sent"] = sent
        if sent:
            journal_line(d["stream"], True, d["cost_keep"], d["idle"], home=home)
        elif not d["keep"]:
            journal_line(d["stream"], False, d["cost_rewrite"], d["idle"], home=home)
    return out
