#!/usr/bin/env python3
"""Сторож плана devkit: сказать сессии, что её план разошёлся с работой.

План работ сессии рисует дашборд: деления кольца в шапке разговора и блок
«План агента» на экране задачи. Врёт он молча. В кольце диспетчерской сессии
висели два шага многочасовой давности, оба отменённые сменой работы, и заметил
это человек глазами (находка DK-609). Сравнить план с делом машине нечем, но
простые признаки расхождения считаются.

Признаков три, пороги к ним лежат в конфиге (kit/plan.toml), своих чисел код не
держит.

  идущий пункт старше порога   план не менялся дольше step_hours часов, а пункт
                               в нём всё ещё помечен идущим
  план закрыт целиком          все пункты completed, а сессия работает дальше
                               уже done_turns ходов
  план стоит без правки        план не менялся still_turns ходов подряд, а
                               незакрытые пункты в нём есть

Ходы сторож не считает сам: их считает отметка хода (hooks/turn-mark.py) в
журнале ~/.devkit/turns.log, и своего журнала рядом с планом тут не заводится.
Ход, кончившийся позже последней правки плана, это ход, который план не тронул.

Находка сдаётся сессии решением block на конце хода, как сдача фоновых работ:
строка без блокировки теряется среди необязательных, а расхождение чинит та же
сессия, которой его завели.

Режим один:
  plan-watch.py --hook [протокол]   событие читается со stdin и разбирается по
                                    имени протокола таблицей hookio.py (голый
                                    --hook это claude-code)

Переменные окружения:
  DEVKIT_PLAN_WATCH_OFF=1   сторож молчит (стенд, прогон проверки)
  DEVKIT_PLAN_CONFIG=..     свой конфиг порогов вместо kit/plan.toml
  DEVKIT_PLAN_DIR=..        свой каталог планов вместо ~/.devkit/plans
  DEVKIT_TURN_MARK_LOG=..   свой журнал отметок хода, тот же ключ, что у
                            hooks/turn-mark.py
  DEVKIT_PLAN_WATCH_LOG=..  свой журнал сторожа (стенд, прогон проверки)
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

HOME_DIR = os.path.join(os.path.expanduser("~"), ".devkit")
PLAN_DIR = os.path.join(HOME_DIR, "plans")
TURNS_LOG = os.path.join(HOME_DIR, "turns.log")
LOG = os.path.join(HOME_DIR, "plan-watch.log")

OFF_ENV = "DEVKIT_PLAN_WATCH_OFF"
CONFIG_ENV = "DEVKIT_PLAN_CONFIG"
DIR_ENV = "DEVKIT_PLAN_DIR"
TURNS_ENV = "DEVKIT_TURN_MARK_LOG"
LOG_ENV = "DEVKIT_PLAN_WATCH_LOG"

DEFAULT_CONFIG = os.path.join(hookio.ROOT, "kit", "plan.toml")

# Состояния пункта, те же три, что держит agentctl plan.
PENDING, RUNNING, DONE = "pending", "in_progress", "completed"
# Слово хода в журнале отметок, значащее доработанный ход.
TURN_DONE_WORD = "кончен"
# Время в журнале отметок: местное, секундной точности.
TURN_TIME = "%Y-%m-%dT%H:%M:%S"
# Пороги, которых ждём от конфига: ключ и что он значит.
KEYS = (
    ("step_hours", "сколько часов идущему пункту, прежде чем счесть его брошенным"),
    ("done_turns", "сколько ходов сессия работает поверх плана, закрытого целиком"),
    ("still_turns", "сколько ходов подряд план стоит без правки"),
)


def off(env=None):
    env = os.environ if env is None else env
    return bool((env.get(OFF_ENV) or "").strip())


def plan_dir(env=None):
    env = os.environ if env is None else env
    return (env.get(DIR_ENV) or "").strip() or PLAN_DIR


def turns_log(env=None):
    env = os.environ if env is None else env
    return (env.get(TURNS_ENV) or "").strip() or TURNS_LOG


def config_path(env=None):
    env = os.environ if env is None else env
    return (env.get(CONFIG_ENV) or "").strip() or DEFAULT_CONFIG


def log(session, text, env=None):
    """Журнал сторожа: чем кончился его заход. Без такой строки молчание
    сторожа неотличимо от его отсутствия."""
    env = os.environ if env is None else env
    line = "%s сессия %s %s\n" % (time.strftime("%Y-%m-%dT%H:%M:%S"), session, text)
    try:
        hookio.append_capped((env.get(LOG_ENV) or "").strip() or LOG, line)
    except OSError:
        pass


def thresholds(path=None, env=None):
    """Пороги из конфига парой (словарь, причина). Пустой словарь значит, что
    порогов нет и считать нечем: своих чисел код не держит."""
    path = path or config_path(env)
    doc, why = hookio.toml_at(path)
    if doc is None:
        return {}, "порогов не прочесть, %s" % why
    out = {}
    for key, what in KEYS:
        v = doc.get("plan", key)
        if v is None or not isinstance(v.val, int) or isinstance(v.val, bool) or v.val < 0:
            return {}, "[plan] %s: жду целое, %s" % (key, what)
        out[key] = v.val
    return out, ""


def plan_path(session, env=None):
    return os.path.join(plan_dir(env), "%s.json" % session)


def read_plan(path):
    """План файлом: (пункты, время правки). Нет файла или он битый, и пунктов
    нет: сессию без плана сторожит не эта проверка (DK-880)."""
    try:
        with open(path, encoding="utf-8") as f:
            items = json.load(f)
        changed = os.path.getmtime(path)
    except (OSError, ValueError):
        return [], 0.0
    if not isinstance(items, list):
        return [], 0.0
    out = []
    for it in items:
        if isinstance(it, dict) and it.get("text"):
            out.append((hookio.text_of(it.get("text")), hookio.text_of(it.get("state")) or PENDING))
    return out, changed


def turn_time(word):
    try:
        return time.mktime(time.strptime(word, TURN_TIME))
    except ValueError:
        return 0.0


def turns_after(session, since, env=None):
    """Сколько ходов сессия доработала после правки плана. Журнал отметок зовёт
    сессию первыми восемью знаками, а план целым ID, поэтому сводятся они
    началом строки, а не равенством."""
    if since <= 0:
        return 0
    path = turns_log(env)
    try:
        with open(path, encoding="utf-8") as f:
            lines = f.read().split("\n")
    except OSError:
        return 0
    count = 0
    for line in lines:
        words = line.split()
        if len(words) < 4 or words[1] != "сессия" or words[3] != "ход":
            continue
        if not session.startswith(words[2]):
            continue
        if words[4:5] != [TURN_DONE_WORD]:
            continue
        if turn_time(words[0]) > since:
            count += 1
    return count


def hours(seconds):
    """Часы словами: сторож говорит про возраст пункта человеческой мерой."""
    n = int(seconds // 3600)
    tail = n % 10
    if 10 <= n % 100 <= 20 or tail == 0 or tail >= 5:
        return "%d часов" % n
    return "%d %s" % (n, "час" if tail == 1 else "часа")


def findings(items, changed, turns, limits, now):
    """Чем план разошёлся с работой. Пустой список значит, что план делу
    отвечает."""
    if not items or not limits:
        return []
    out = []
    running = [text for text, state in items if state == RUNNING]
    age = max(0.0, now - changed)
    if running and age >= limits["step_hours"] * 3600:
        out.append("пункт «%s» помечен идущим, а план не менялся %s"
                   % (running[0], hours(age)))
    closed = all(state == DONE for _, state in items)
    if closed and turns >= limits["done_turns"]:
        out.append("все пункты плана закрыты, а сессия работает дальше: ходов "
                   "после правки плана %d" % turns)
    elif not closed and turns >= limits["still_turns"]:
        out.append("план не менялся %d ходов подряд, а незакрытые пункты в нём есть" % turns)
    return out


def handover(lines):
    """Текст сдачи. Сессия читает его вместо сна, поэтому в нём сказано и что
    случилось, и что с этим делать."""
    body = "\n".join("- %s" % line for line in lines)
    return ("Сторож плана devkit: план работ разошёлся с делом.\n"
            "%s\n"
            "Переложи план: сними отменённые шаги и положи набор заново командой "
            "agentctl plan set, состояния совпавших пунктов она переносит сама. Если план "
            "верен, отметь идущий шаг (agentctl plan step) или закрой сделанное "
            "(agentctl plan done)." % body)


def blocked(text, stream=None):
    """Канал сдачи: решение block на конце хода. Харнес отдаёт текст модели и
    продолжает ход вместо того, чтобы уснуть."""
    out = sys.stdout if stream is None else stream
    json.dump({"decision": "block", "reason": text}, out, ensure_ascii=False)
    out.write("\n")


def handle(event, env=None, now=None, stream=None):
    """Одно событие: план сверен с делом, находки сданы сессии."""
    now = time.time() if now is None else now
    if event.kind != hookio.TURN_DONE or event.active:
        # Ход, продолженный стоп-хуком, сторож пропускает: второй заход закрутил
        # бы сессию в цикле, а сказанное в первый раз уже сказано.
        return 0
    items, changed = read_plan(plan_path(event.session, env))
    if not items:
        return 0
    limits, why = thresholds(None, env)
    if not limits:
        log(event.session, "пропуск: %s" % why, env)
        return 0
    turns = turns_after(event.session, changed, env)
    lines = findings(items, changed, turns, limits, now)
    if not lines:
        log(event.session, "план отвечает делу: пунктов %d, ходов после правки %d"
            % (len(items), turns), env)
        return 0
    blocked(handover(lines), stream)
    log(event.session, "сдача: %s" % "; ".join(lines), env)
    return 0


def run_hook(protocol, env=None, now=None, stream=None):
    if off(env):
        return 0
    event = hookio.agent_event(protocol)
    if event is None or not event.session:
        # Чужое событие и событие без ID сессии сторожу не годятся: план ведётся
        # по сессии, и без её имени файла не найти.
        return 0
    try:
        return handle(event, env, now, stream)
    except OSError:
        return 0


def main(argv):
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("plan-watch: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
