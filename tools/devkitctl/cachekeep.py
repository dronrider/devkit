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
находит простаивающие потоки и принимает решение по каждому. Реальная отправка
продлевающего запроса идёт через клиент харнеса (CLI), на стенде она
подменяется заглушкой: синтетический поток держит механику, падение перезаписи
видно на настоящем заходе.
"""
import os
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
    }


def journal_line(stream, keep, cost, idle, home=None):
    """Строка журнала о решении механики: видна снаружи, молчание не считается.

    Формат именованными полями как у turn-mark.py, слово хода «кэш».
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
    except OSError:
        pass
    return line


def scan_project(root, now=None, home=None):
    """Обход журналов сессий проекта: решения по простаивающим потокам.

    Возвращает список решений (decide_stream). Строки журнала пишутся
    по каждому потоку, у которого есть простой (idle > 0).
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
        if d["idle"] > 0:
            journal_line(d["stream"], d["keep"], d["cost_keep"], d["idle"], home=home)
    return out
