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

План сессии это файл ~/.devkit/plans/<ID сессии>.json. Нет его, а файлы с
меткой <ID сессии>-sub-<метка>.json есть, значит план с меткой ведёт сама
сессия, и берётся свежайший из них. Так лежит план окна конвейера в tmux и
сессии стенда obeycheck: переменная CLAUDE_CODE_CHILD_SESSION достаётся им от
чужого процесса, и agentctl plan в таком окружении требует метку. Есть файл без
метки, значит план сессии это он, а файлы с меткой это планы её субагентов, и
брошенный идущим план субагента сторожа не будит.

Ходы сторож не считает сам: их считает отметка хода (hooks/turn-mark.py) в
журнале ~/.devkit/turns.log, и своего журнала рядом с планом тут не заводится.
Ход, кончившийся позже последней правки плана, это ход, который план не тронул.

Четвёртый признак другой природы: плана у сессии нет вовсе. У чата доски без
привязки к задаче нет своего шага board-task, который зовёт agentctl plan, и
решение положить план держится только на памяти самой сессии (находка
DK-978). Свой файл плана это только `<ID сессии>.json`, файл с меткой
субагента за него не считается, даже свежий: план положил не тот, у кого
сторож спрашивает. Второй ход подряд той же сессии, между отметками которого в
транскрипте видны вызовы инструментов, и есть порог DK-881 «длиннее одного
хода». Разбор на один ответ, пусть и с десятком вызовов внутри, сторож не
трогает. Сессия, чьё окружение само требует метку у плана
(`CLAUDE_CODE_CHILD_SESSION=1`: субагент, окно конвейера, стенд obeycheck), в
этот признак не попадает: её собственный план и так вынужденно ложится с
меткой, а расхождение с делом у него смотрят три признака выше.

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
# Признак сессии, чьё окружение само требует метку у плана (agentctl plan,
# tools/agentctl/plan.go): субагент, окно конвейера в tmux, стенд obeycheck.
CHILD_ENV = "CLAUDE_CODE_CHILD_SESSION"

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


def label_plans(session, env=None):
    """Планы, писанные с меткой: файлы <ID сессии>-sub-<метка>.json, свежайший
    первым. Маска та же, что у дашборда (subPlanFiles в
    tools/dashboard/sessions.go), и соседа вроде <ID>-submit.json она не
    захватывает."""
    if not session:
        return []
    directory = plan_dir(env)
    try:
        names = os.listdir(directory)
    except OSError:
        return []
    pre = session + "-sub"
    out = []
    for name in names:
        if not name.startswith(pre) or not name.endswith(".json"):
            continue
        rest = name[len(pre):-len(".json")]
        if rest and not rest.startswith("-"):
            continue
        path = os.path.join(directory, name)
        try:
            out.append((os.path.getmtime(path), path))
        except OSError:
            continue
    out.sort(reverse=True)
    return [path for _, path in out]


def own_plan(session, env=None):
    """Файл плана самой сессии. Первым спрашивается адрес без метки, как у
    кольца дашборда. Его нет, а файлы с меткой есть, значит план с меткой ведёт
    сама сессия: переменная CLAUDE_CODE_CHILD_SESSION достаётся от tmux-сервера
    и стенду obeycheck, и agentctl plan в таком окружении требует метку
    (DK-609). Берётся свежайший файл, остальные это планы субагентов."""
    path = plan_path(session, env)
    if os.path.exists(path):
        return path
    labelled = label_plans(session, env)
    return labelled[0] if labelled else path


def no_plan_applies(session, env=None):
    """Признак «плана нет вовсе» (DK-978) смотрит только файл без метки: план
    сессии это `<ID>.json`, а свежий файл субагента за него не считается,
    как и у own_plan(). В отличие от own_plan(), сюда не заглядывают: сессия,
    чьё окружение само требует метку (CLAUDE_CODE_CHILD_SESSION=1), тут вообще
    не смотрится, её собственный план и так ложится с меткой."""
    env = os.environ if env is None else env
    if (env.get(CHILD_ENV) or "").strip() == "1":
        return False
    return not os.path.exists(plan_path(session, env))


def is_human_turn(event):
    """Начало хода человека в транскрипте: настоящая реплика, а не
    механическое эхо харнеса. Тип user с результатом инструмента харнес кладёт
    сам после каждого вызова, обычно блоком tool_result, а у инструмента Skill
    тем же блоком текста, что и у живой реплики. Обе разновидности эха, и
    сдача фоновой работы, харнес помечает isMeta, и по этому признаку они
    отсекаются раньше разбора самого содержимого."""
    if event.get("type") != "user" or event.get("isMeta"):
        return False
    message = event.get("message")
    content = message.get("content") if isinstance(message, dict) else None
    if isinstance(content, str):
        return True
    if isinstance(content, list):
        return not all(isinstance(b, dict) and b.get("type") == "tool_result" for b in content)
    return False


def turn_used_tools(events):
    """В ходе виден хотя бы один вызов инструмента: у события ассистента
    среди блоков контента есть блок tool_use."""
    for event in events:
        message = event.get("message")
        content = message.get("content") if isinstance(message, dict) else None
        if not isinstance(content, list):
            continue
        for block in content:
            if isinstance(block, dict) and block.get("type") == "tool_use":
                return True
    return False


def transcript_turns(path):
    """Транскрипт сессии, порезанный на ходы человека: список списков событий
    ассистента между соседними настоящими репликами. Файла нет или строка не
    разбирается, и такая строка просто пропускается: транскрипт живого
    прогона хрупок форматом, а не обязан быть безупречным JSON построчно."""
    turns = []
    current = None
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                if not isinstance(event, dict):
                    continue
                if is_human_turn(event):
                    current = []
                    turns.append(current)
                    continue
                if current is not None and event.get("type") == "assistant":
                    current.append(event)
    except OSError:
        return []
    return turns


def two_turns_in_a_row_used_tools(path):
    """Второй ход подряд той же сессии с вызовами инструментов между
    отметками журнала turn-mark: буквально порог DK-881 «длиннее одного
    хода». Разбор на один ответ, пусть и с десятком вызовов внутри, здесь не
    считается, ходов для этого нужно минимум два."""
    turns = transcript_turns(path)
    if len(turns) < 2:
        return False
    return turn_used_tools(turns[-1]) and turn_used_tools(turns[-2])


def no_plan_handover():
    """Текст сдачи для признака «плана нет вовсе»: своей записи о плане
    сессия ещё не оставила, и просить «переложить» его, как у расхождения,
    здесь неверно по смыслу."""
    return ("Сторож плана devkit: второй ход подряд идёт с вызовами инструментов, "
            "а план работ сессия не положила.\n"
            "Работа явно не на один ход, порядок в скилле work-plan: положи план "
            "командой agentctl plan set, шаг начинай agentctl plan step и закрывай "
            "agentctl plan done.")


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
    if no_plan_applies(event.session, env) and two_turns_in_a_row_used_tools(event.transcript):
        blocked(no_plan_handover(), stream)
        log(event.session, "сдача: своего плана нет, а второй ход подряд идёт с инструментами", env)
        return 0
    items, changed = read_plan(own_plan(event.session, env))
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
