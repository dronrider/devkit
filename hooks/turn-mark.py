#!/usr/bin/env python3
"""Отметка хода devkit: сказать оболочке конвейера, чем кончился ход живой
сессии.

Конвейер задачи ведёт одна интерактивная сессия в tmux (DK-724), а оболочка
task-run.py стоит рядом с нею в том же окне. Процесс клиента при этом не
выходит ни разу, а прежде именно выход и был концом прохода. Оболочка ждала
кода возврата и по нему решала, звать ли следующий. У живой сессии такого
события нет, и узнать про конец хода больше не у кого, кроме хуков самого
харнеса.

Строка ложится в машинный журнал ~/.devkit/turns.log именованными полями, как в
реестре чатов:

  <время> сессия <ID> ход <слово> повод <тип> дерево <путь>

Четыре события, четыре слова хода:

  Stop              кончен  ход доработан, оболочка решает, звать ли следующий
  StopFailure       упал    ход убит ошибкой API, сессия стоит до «продолжай»
  Notification      ждёт    сессия упёрлась в вопрос, повод несёт его тип
  UserPromptSubmit  начат   в сессию пришла реплика, ход пошёл

Слово «начат» нужно не меньше остальных. Реплику человека панель подаёт в то же
окно, и оболочка, не знающая про чужой заказ, послала бы свой поверх идущего
хода.

ID сессии харнес отдаёт этому событию первыми восемью знаками, а реестру чатов
целиком. Поэтому читатель журнала сводит их началом строки, а не равенством.

Сессия подпроцесса делегирования (`agentctl run`) отметок не пишет. Своё окно
она наследует от хозяина вместе с переменными, и её конец хода оболочка приняла
бы за конец своего.

Режим один:
  turn-mark.py --hook [протокол]   событие читается со stdin и разбирается по
                                   имени протокола таблицей hookio.py (голый
                                   --hook это claude-code)

Переменные окружения:
  DEVKIT_TURN_MARK_LOG=..  свой журнал отметок (стенд, прогон проверки)
  DEVKIT_RUN_DEPTH=..      сессия подпроцесса делегирования, отметок нет
  DEVKIT_SILENT=1          служебный вызов клиента, отметок нет
"""
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

# Журнал отметок лежит рядом с реестром чатов и журналом уведомителя. Сессии на
# машине общие, и доска у них не одна.
LOG = os.path.join(os.path.expanduser("~"), ".devkit", "turns.log")
LOG_ENV = "DEVKIT_TURN_MARK_LOG"
# Признак сессии подпроцесса делегирования, тот же, что уважает уведомитель.
RUN_DEPTH = "DEVKIT_RUN_DEPTH"
# Служебный вызов клиента, тот же маркер, что у реестра чатов.
SILENT_ENV = "DEVKIT_SILENT"

# Слово хода по оси события. Субагентская ось сюда не идёт. Конец субагента это
# не конец хода сессии, за ним смотрит свой сторож (agent-watch.py).
WORDS = {
    hookio.TURN_DONE: "кончен",
    hookio.TURN_FAILED: "упал",
    hookio.NOTIFY: "ждёт",
    hookio.PROMPT_SUBMIT: "начат",
}


def dashless(value):
    """Значение поля журнала. Пустое место это дефис, как у уведомителя."""
    value = " ".join(str(value or "").split())
    return value or "-"


def record(sess, now=None):
    """Строка журнала про ход сессии. Пусто значит, что событие не про ход или
    у него нет ID сессии. Отметку без сессии не свести ни с окном, ни с
    реестром чатов."""
    word = WORDS.get(sess.kind)
    if not word or not sess.session or sess.session == "-":
        return ""
    stamp = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(now))
    return "%s сессия %s ход %s повод %s дерево %s\n" % (
        stamp, dashless(sess.session), word, dashless(sess.reason),
        dashless(hookio.tree_root(sess.cwd) or sess.cwd))


def silent(env=None):
    env = os.environ if env is None else env
    return bool((env.get(SILENT_ENV) or "").strip()) or \
        bool((env.get(RUN_DEPTH) or "").strip())


def log_path(env=None):
    env = os.environ if env is None else env
    return (env.get(LOG_ENV) or "").strip() or LOG


def run_hook(protocol, path=None, now=None):
    # Кривой вход кончается кодом 0 и молчанием. Хук стоит в каждой сессии
    # машины, и ронять её ради отметки нельзя.
    try:
        sess = hookio.session_event(protocol)
    except hookio.BadEvent:
        return 0
    if sess is None:
        return 0
    line = record(sess, now)
    if line:
        hookio.append_capped(path or log_path(), line)
    return 0


def main(argv):
    if silent():
        return 0
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("turn-mark: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
