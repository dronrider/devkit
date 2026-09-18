#!/usr/bin/env python3
"""Указатель на скилл chat devkit: класть его по транскрипту, а не по правилу.

Строка старта сессии (`session-task.py`, константы `CHAT_RULE` и
`NO_TASK_CHAT_RULE`) раньше несла оговорку «на каждой реплике человека»: без
неё модель звала скилл chat один раз и на втором ходу шла к работе мимо
него. Цена оговорки это повторный вызов на каждом ходе подряд: тело скилла
приезжает целиком один раз, а дальше харнес сам отвечает «Skill /chat is
already loaded above», и повтор стоит около сотни токенов и одного круга
вывода модели (разбор docs/tasks/DK-1032.md).

Этот хук решает ту же задачу иначе. Стоит он на `UserPromptSubmit`, событии
каждой реплики человека, и кладёт указатель не всегда, а только когда
вызова скилла chat в транскрипте после последней границы сжатия ещё не
было: сильная модель зовёт скилл раз за окно контекста, а слабая по-прежнему
не пропускает его перед первым ответом.

Транскрипт может весить десятки мегабайт (43 МБ был самый большой дома на
день разбора), и читать его целиком на каждом ходе дорого. Хук читает не
файл целиком, а хвост фиксированного размера с конца. Если в хвосте
находится граница сжатия (запись `type: system, subtype: compact_boundary`)
раньше вызова скилла, ответ точный: «после неё не звался». Если в хвосте нет
ни границы, ни вызова, хук не отличает «звался раньше хвоста» от «не звался
вовсе» и кладёт указатель заново: цена ложного срабатывания это один лишний
вызов скилла, а не потерянное правило.

Признак `DEVKIT_HIDDEN` хук уважает тем же порядком, что `session-task.py`
(DK-880): у скрытой сессии (виток цели, тиковый прогон конвейера, делегат
`agentctl run`) собеседника-человека нет, и указатель ей класть некому. Текст
указателя, `CHAT_RULE` для сессии с заказом задачи и `NO_TASK_CHAT_RULE` без
него, хук берёт из `session-task.py`, а не заводит свой: это тот же текст,
что кладёт старт сессии на первом ходе и после сжатия, и расходиться им
незачем.

Режим один:
  chat-pointer.py --hook [протокол]   событие читается со stdin и разбирается
                                      по имени протокола таблицей hookio.py
                                      (голый --hook это claude-code)
"""
import importlib
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

# Строка старта сессии и признаки DEVKIT_HIDDEN/DEVKIT_TASK живут в
# session-task.py, и указатель этого хука обязан звучать тем же текстом:
# дефис в имени файла не даёт обычный import, тем же приёмом пользуются тесты
# хуков (session_task_test.py и соседи).
session_task = importlib.import_module("session-task")

# Повод строки транскрипта, которым харнес помечает границу сжатия контекста.
COMPACT_BOUNDARY = "compact_boundary"

# Сколько байт с конца транскрипта читает хук вместо файла целиком. Значение
# с большим запасом покрывает несколько ходов подряд: типичный ход весит
# заметно меньше, а цена промаха (лишний указатель) намного дешевле цены
# чтения десятков мегабайт на каждой реплике.
TAIL_BYTES = 256 * 1024


def tail_lines(path, size=TAIL_BYTES):
    """Строки хвоста файла: последние `size` байт, разрезанные по переводу
    строки. Нет файла или он не читается, и хвост пуст: пустой транскрипт (ещё
    не начатая сессия) значит «указатель нужен», то же, что и пустой хвост
    здесь."""
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            end = f.tell()
            start = max(0, end - size)
            f.seek(start)
            if start:
                # Первая строка после смещения могла быть разорвана серединой:
                # отбросить её дешевле, чем гадать про её начало.
                f.readline()
            data = f.read()
    except OSError:
        return []
    return data.decode("utf-8", errors="replace").splitlines()


def skill_chat_call(event):
    """Ход ассистента с вызовом Skill chat: блок `tool_use`, имя `Skill`,
    вход `{"skill": "chat"}`. Форма снята с живого транскрипта (разбор
    docs/tasks/DK-1032.md)."""
    if event.get("type") != "assistant":
        return False
    message = event.get("message")
    content = message.get("content") if isinstance(message, dict) else None
    if not isinstance(content, list):
        return False
    for block in content:
        if not isinstance(block, dict) or block.get("type") != "tool_use":
            continue
        if block.get("name") != "Skill":
            continue
        inp = block.get("input")
        if isinstance(inp, dict) and inp.get("skill") == "chat":
            return True
    return False


def called_since_compact(path):
    """Звался ли скилл chat после последней границы сжатия, глядя в хвост
    транскрипта в обратном порядке. Первая найденная граница сжатия кончает
    разбор отрицательным ответом: до неё смотреть незачем, всё, что было
    раньше, уже не в счёт. Первый найденный вызов раньше любой границы кончает
    его положительным: он застал момент до всякой более ранней границы, а
    значит и до последней. Хвост кончился без того и другого, и ответ тоже
    отрицательный: за его пределами хук предпочитает лишний указатель
    пропущенному правилу."""
    for line in reversed(tail_lines(path)):
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get("type") == "system" and event.get("subtype") == COMPACT_BOUNDARY:
            return False
        if skill_chat_call(event):
            return True
    return False


def pointer(env):
    """Текст указателя по признаку заказа задачи, тем же критерием, что
    session-task.py выбирает между CHAT_RULE и NO_TASK_CHAT_RULE."""
    if session_task.ordered_task(env):
        return session_task.CHAT_RULE
    return session_task.NO_TASK_CHAT_RULE


def run_hook(protocol, env=None):
    real_env = os.environ if env is None else env
    try:
        sess = hookio.session_event(protocol)
    except hookio.BadEvent:
        return 0
    if sess is None or sess.kind != hookio.PROMPT_SUBMIT:
        return 0
    if session_task.hidden(real_env):
        return 0
    if called_since_compact(sess.transcript):
        return 0
    return hookio.Context("UserPromptSubmit").say(pointer(real_env))


def main(argv):
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("chat-pointer: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
