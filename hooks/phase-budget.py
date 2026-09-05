#!/usr/bin/env python3
"""Сторожок стыка фаз: остаток окна сессии против оценки следующей фазы.

Стык фазы это переход задачи по доске: `taskctl move <ID> in-progress` открывает
разработку, `taskctl move <ID> check` открывает проверку, `taskctl close <ID>`
её закрывает. Что делать на стыке, сессия до этого хука решала по памяти, и
дефолтом выходило дотянуть до автосжатия: хвост задачи попадал на самый занятый
контекст, и там же терялись шаги (DK-803, черновики DK-568 и DK-559).

Хук стоит на PostToolUse Bash. Команду перехода он узнаёт по тексту вызова,
занятость окна берёт из журнала сессии: у последнего хода ассистента в
транскрипте лежит usage, и сумма свежего входа с записью и чтением кеша это
размер контекста на этот момент. Размер окна выводится из имени модели в той же
записи. Оценку следующей фазы хук берёт из своего журнала ~/.devkit/phase.log:
на каждом переходе туда ложится строка с занятостью окна, а разница двух
строк одной сессии по одной задаче это рост контекста за фазу. Прошлые задачи
той же цены дают медиану (оценка) и третью четверть (порог): остаток меньше
порога значит фаза не влезает, и в ленту сессии едет подсказка с остатком и с
рядом действий из ядра правил. Пока записей по цене нет, порог берётся долей
окна, и подсказка называет его умолчанием.

Режимы:
  phase-budget.py --hook [протокол]   ход инструмента со stdin, разбор по
                                      таблице hookio.py
  phase-budget.py --show <ID> [транскрипт]
                                      записи журнала по задаче, оценки фаз по
                                      её цене и, если дан транскрипт, остаток
                                      окна по нему

Стенду свои пути задаются переменными: DEVKIT_PHASE_LOG (журнал) и
DEVKIT_PHASE_BOARD (файл доски вместо docs/TASKS.md от дерева хода).

Молчит хук на любом входе, который не про переход: чужой инструмент, другая
команда, транскрипт без usage, доска без такой строки. Сессий на машине много,
ронять их из-за сторожка нельзя, и любая порча входа это тихий ноль.
"""
import datetime
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio  # noqa: E402

LOG = os.path.expanduser("~/.devkit/phase.log")
# Окно модели в токенах. Харнес называет модель с миллионным окном суффиксом
# [1m] в имени, остальные идут с обычным окном.
WINDOW = 200000
WINDOW_1M = 1000000
# Доля окна, которую хук оставляет харнесу: ответ хода и автосжатие начинаются
# раньше, чем окно заполнится до последнего токена, и остаток считается до этой
# черты, а не до края окна.
RESERVE_SHARE = 0.1
# Умолчание порога, пока журнал пуст: доля окна на фазу. Грубая заготовка, а не
# замер, и в подсказке она так и называется; первая же закрытая фаза той же цены
# её вытесняет.
DEFAULT_SHARE = {"разработка": 0.35, "проверка": 0.15}
# Фаза, которую открывает переход. Blocked и Backlog фазы не открывают, а
# закрытие задачи только закрывает проверку.
PHASE_OF = {"in-progress": "разработка", "check": "проверка"}
# Секция доски по заголовку, как в taskctl (board.go, sectByPrefix).
SECTIONS = (("## In progress", "in-progress"), ("## Check", "check"),
            ("## Backlog", "backlog"), ("## Blocked", "blocked"))
# Ряд действий на стыке, в порядке ядра правил (RULES.core.md, «Стык фаз»).
ACTIONS = ("продолжить в том же окне", "поднять субагента со свежим окном",
           "передать работу файлом задачи", "сжать контекст")

TRANSITION = re.compile(
    r"(?:^|[;&|(])\s*taskctl\s+(?:-C\s+\S+\s+)?(move|close)\s+([A-Z][A-Z0-9]*-\d+)(?:\s+(\S+))?")
USAGE_KEYS = ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")


def transition(command):
    """(ID, статус) из команды перехода; у close статус так и зовётся close.
    None значит, что команда не про переход."""
    m = TRANSITION.search(command or "")
    if not m:
        return None
    verb, task, status = m.group(1), m.group(2), m.group(3)
    if verb == "close":
        return task, "close"
    if not status or status.startswith("-"):
        return None
    return task, status


def window_of(model):
    return WINDOW_1M if "1m" in (model or "").lower() else WINDOW


def usage_of(path):
    """(занято, окно, модель) по последнему ходу ассистента в транскрипте.
    None значит, что ходов с usage в файле нет: пустой журнал, чужой формат,
    нет файла."""
    last = None
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                if '"usage"' not in line:
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                if not isinstance(rec, dict) or rec.get("type") != "assistant":
                    continue
                msg = rec.get("message")
                usage = msg.get("usage") if isinstance(msg, dict) else None
                if not isinstance(usage, dict):
                    continue
                try:
                    used = sum(int(usage.get(k) or 0) for k in USAGE_KEYS)
                except (TypeError, ValueError):
                    continue
                last = (used, window_of(hookio.text_of(msg.get("model"))),
                        hookio.text_of(msg.get("model")))
    except (OSError, TypeError):
        return None
    return last


def board_row(path, task):
    """(секция, цена) строки задачи на доске или в архиве. Секция у архива
    зовётся close: строка туда попадает закрытием. Колонку цены называет шапка
    таблицы: у архива её нет вовсе, и цена там «-». None значит, строки нет."""
    try:
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
    except OSError:
        return None
    section = "close" if os.path.basename(path).startswith("TASKS-archive") else ""
    price_col = None
    for line in lines:
        for prefix, key in SECTIONS:
            if line.startswith(prefix):
                section = key
        cells = [c.strip() for c in line.split("|")]
        if len(cells) > 2 and cells[1] == "ID":
            price_col = cells.index("Цена") if "Цена" in cells else None
        if len(cells) > 2 and cells[1] == task:
            price = cells[price_col] if price_col and price_col < len(cells) else "-"
            return section, price or "-"
    return None


def board_files(cwd):
    env = os.environ.get("DEVKIT_PHASE_BOARD")
    if env:
        return [env, os.path.join(os.path.dirname(env), "TASKS-archive.md")]
    root = hookio.tree_root(cwd)
    if not root:
        return []
    docs = os.path.join(root, "docs")
    return [os.path.join(docs, "TASKS.md"), os.path.join(docs, "TASKS-archive.md")]


def find_row(cwd, task, status):
    """Строка задачи там, куда её вёл переход. Переход, которого доска не
    подтвердила (taskctl отказал воротами), стыком не считается."""
    for path in board_files(cwd):
        row = board_row(path, task)
        if row and row[0] == status:
            return row
    return None


def log_path():
    return os.environ.get("DEVKIT_PHASE_LOG") or LOG


def parse_record(line):
    words = line.split()
    if len(words) < 3 or words[1] != "сессия":
        return None
    rec = {"time": words[0]}
    for i in range(1, len(words) - 1, 2):
        rec[words[i]] = words[i + 1]
    return rec


def records(path=None):
    try:
        with open(path or log_path(), encoding="utf-8", errors="replace") as f:
            return [r for r in (parse_record(l) for l in f) if r]
    except OSError:
        return []


def num_of(value):
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def growth(prev, session, used):
    """Рост контекста за фазу, которую закрыл переход: разница с прошлой
    записью той же задачи, и только внутри одной сессии. Фаза, прошедшая
    через другую сессию, роста не даёт: у той сессии своё окно. Окно меньше
    прошлой записи значит, что между переходами было сжатие, и замера роста
    нет: фаза закрывается без числа, а ноль потянул бы оценку вниз."""
    if not prev or prev.get("сессия") != session:
        return None, None
    before = num_of(prev.get("окно"))
    phase = prev.get("фаза") or ""
    if before is None or phase == "-" or not phase:
        return None, None
    return phase, (used - before if used >= before else None)


def estimate(recs, price, phase):
    """(медиана, третья четверть, число записей) роста за фазу у задач той же
    цены. None значит, записей нет."""
    values = sorted(num_of(r.get("рост")) for r in recs
                    if r.get("закрыл") == phase and r.get("цена") == price
                    and num_of(r.get("рост")) is not None)
    if not values:
        return None
    n = len(values)
    median = values[n // 2] if n % 2 else (values[n // 2 - 1] + values[n // 2]) // 2
    upper = values[min(n - 1, (3 * n) // 4)]
    return median, upper, n


def thousands(n):
    return "%d тыс" % round(n / 1000.0)


def threshold(recs, price, phase, window):
    """(порог, строка про источник порога)."""
    est = estimate(recs, price, phase)
    if est:
        median, upper, n = est
        return upper, ("фаза «%s» у задач цены %s брала до %s токенов (медиана %s, записей %s)"
                       % (phase, price, thousands(upper), thousands(median), n))
    share = DEFAULT_SHARE.get(phase, 0)
    return int(window * share), ("записей по фазе «%s» у задач цены %s нет, порог по умолчанию "
                                 "%s токенов" % (phase, price, thousands(window * share)))


def remaining_of(used, window):
    return int(window * (1 - RESERVE_SHARE)) - used


def hint(task, status, used, window, remaining, source):
    return ("Стык фаз %s -> %s: окно сессии занято %s из %s токенов, остаток до черты "
            "харнеса %s; %s. Следующая фаза в остаток не влезает. Ряд действий по ядру "
            "правил, берётся первый подходящий: %s. Записи: python3 %s --show %s."
            % (task, status, thousands(used), thousands(window), thousands(max(remaining, 0)),
               source, ", ".join(ACTIONS), os.path.abspath(__file__), task))


def record_line(now, session, task, price, status, phase, used, window, closed, grown):
    return ("%s сессия %s задача %s цена %s статус %s фаза %s окно %s из %s закрыл %s рост %s\n"
            % (now.strftime("%Y-%m-%dT%H:%M:%S"), session or "-", task, price, status,
               phase or "-", used, window, closed or "-", "-" if grown is None else grown))


def run_hook(protocol, now=None):
    hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    if hookio.text_of(event.get("hook_event_name")) != "PostToolUse":
        return 0
    if hookio.text_of(event.get("tool_name")) != "Bash":
        return 0
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return 0
    move = transition(hookio.text_of(ti.get("command")))
    if not move:
        return 0
    task, status = move
    usage = usage_of(hookio.text_of(event.get("transcript_path")))
    if not usage:
        return 0
    used, window, _ = usage
    row = find_row(hookio.text_of(event.get("cwd")), task, status)
    if not row:
        return 0
    session = hookio.text_of(event.get("session_id"))
    recs = records()
    prev = [r for r in recs if r.get("задача") == task]
    # У архива колонки цены нет, и закрытие берёт цену из прошлой записи той же
    # задачи: без неё запись «закрыл проверка» по цене не нашлась бы никогда.
    price = row[1]
    if price == "-" and prev:
        price = prev[-1].get("цена") or "-"
    closed, grown = growth(prev[-1] if prev else None, session, used)
    phase = PHASE_OF.get(status, "")
    hookio.append_capped(log_path(), record_line(
        now or datetime.datetime.now(), session, task, price, status, phase, used, window,
        closed, grown))
    if not phase:
        return 0
    limit, source = threshold(recs, price, phase, window)
    remaining = remaining_of(used, window)
    if remaining >= limit:
        return 0
    return hookio.context(protocol).say(hint(task, status, used, window, remaining, source))


def run_show(task, transcript, out):
    recs = records()
    mine = [r for r in recs if r.get("задача") == task]
    if not mine:
        out.write("записей по %s в %s нет: переходов доски при этом хуке не было\n"
                  % (task, log_path()))
    for r in mine:
        out.write("%s статус %s окно %s из %s закрыл %s рост %s\n"
                  % (r["time"], r.get("статус"), r.get("окно"), r.get("из"),
                     r.get("закрыл"), r.get("рост")))
    price = mine[-1].get("цена") if mine else "-"
    window = num_of(mine[-1].get("из")) if mine else WINDOW
    usage = usage_of(transcript) if transcript else None
    if usage:
        window = usage[1]
        out.write("транскрипт: занято %s из %s токенов (%s), остаток до черты харнеса %s\n"
                  % (thousands(usage[0]), thousands(window), usage[2],
                     thousands(max(remaining_of(usage[0], window), 0))))
    elif transcript:
        out.write("транскрипт %s без ходов ассистента с usage\n" % transcript)
    for phase in sorted(set(PHASE_OF.values())):
        _, source = threshold(recs, price, phase, window or WINDOW)
        out.write("%s\n" % source)
    return 0


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("phase-budget: %s\n" % e)
            return 2
    if argv[:1] == ["--show"] and len(argv) in (2, 3):
        return run_show(argv[1], argv[2] if len(argv) == 3 else "", sys.stdout)
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
