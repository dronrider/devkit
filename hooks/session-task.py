#!/usr/bin/env python3
"""Реестр чатов devkit: записать, какую задачу ведёт родившаяся сессия.

До этого хука связь «сессия -> задача» была только угаданной: по хвосту
каталога бокового дерева, ID в первой реплике, имени ветки. Угадывание
ошибочно в обе стороны.
Разговор про задачу из главного дерева загорается её живой работой, а сессия, не
попавшая ни под один способ, остаётся карточкой без имени и без входа. Хук стоит
на рождении сессии, где ID уже есть, и кладёт строку в машинный журнал
~/.devkit/sessions.log. Решение целиком в docs/lld/DK-430-task-chat.md, решение 1.

Режимов два:
  session-task.py --hook [протокол]  рождение сессии: событие читается со stdin
                                     и разбирается по имени протокола таблицей
                                     hookio.py (голый --hook это claude-code)
  session-task.py --touch [протокол] ход инструмента: правка файла в боковом
                                     дереве задачи дописывает привязку по факту
                                     работы, повторы одной и той же привязки
                                     отсеиваются

Строка журнала именованная, как у уведомителя, и читается по ключевым словам:

  <время> сессия <ID> задача <DK-431> проект <devkit> дерево <путь>
  транскрипт <путь> источник <слово> повод <startup> tmux <имя>

Задачу хук берёт из двух мест. Переменная DEVKIT_TASK это заказ того, кто
поднял сессию: её ставит дашборд в начало команды сессии, и только так узнаётся
конвейерная сессия, стартующая в главном чекауте. Хвост имени рабочего дерева
(devkit-dk-431) это боковое дерево задачи, заведённое ровно под неё. Ни доски,
ни сети хуку не нужно: форма ID проверяется регуляркой, и чужой префикс тут
законен, доски на машине не одна.

Источник это слово, которым дашборд подписывает привязку на экране: «заказ» и
«дерево» пишет этот хук, «рука» и «снята» пишет ручка привязки. Сессия без
задачи тоже пишет строку, с пустой задачей и пустым источником: по ней разговор доски
виден сразу, без угадывания по транскрипту заново.

Имя tmux-сессии приезжает переменной DEVKIT_TMUX от того же, кто поднял сессию.
Вывести его из записи нечем: чат задачи зовётся chat-<ID>-<n>, и номер знает
только поднявший. Мера «разговор кончился» проверяется по тому, жива ли
tmux-сессия с этим именем, а без поля пришлось бы гадать (развилка DK-431).

Беда любого разбора это тихий ноль: хук стоит в каждой сессии на машине, и
ронять её ради журнала нельзя. Сессия вне git-дерева всё равно пишет строку с
пустым деревом: разговор доски идёт как раз из такой.
"""
import os
import re
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio

# Журнал реестра лежит рядом с журналом уведомителя в одном машинном каталоге,
# и формат с механизмом для третьего файла в нём уже готовы.
LOG = os.path.join(os.path.expanduser("~"), ".devkit", "sessions.log")

# Форма ID задачи в заказе: префикс доски прописными, номер до шести знаков.
TASK_RE = re.compile(r"^[A-Z]{2,10}-[0-9]{1,6}$")
# Хвост имени бокового дерева: devkit-dk-431 и голое dk-431. Регистр тут любой,
# каталоги называются строчными.
TREE_RE = re.compile(r"(?:^|-)([A-Za-z]{2,10}-[0-9]{1,6})$")

# Слова источника, которые пишет этот хук. «Рука» и «снята» относятся к ручке
# привязки дашборда, сюда они не приходят.
BY_ORDER = "заказ"
BY_TREE = "дерево"

TASK_ENV = "DEVKIT_TASK"
TMUX_ENV = "DEVKIT_TMUX"


def dashless(value):
    """Значение поля журнала: пустое место это дефис, как у уведомителя."""
    value = " ".join((value or "").split())
    return value or "-"


def ordered_task(env):
    """Задача из заказа поднявшего сессию. Пустая строка значит, что заказа не
    было или он не похож на ID: врать про чужую задачу дороже, чем промолчать."""
    task = (env.get(TASK_ENV) or "").strip().upper()
    return task if TASK_RE.match(task) else ""


def tree_task(root):
    """Задача и проект по имени рабочего дерева. Проект это то, что осталось от
    имени без хвоста задачи: боковое дерево зовётся <проект>-<id>."""
    if not root:
        return "", ""
    name = os.path.basename(root.rstrip(os.sep))
    m = TREE_RE.search(name)
    if not m:
        return "", name
    return m.group(1).upper(), name[:len(name) - len(m.group(1))].rstrip("-")


def record(start, env=None, now=None):
    """Строка журнала про родившуюся сессию."""
    env = os.environ if env is None else env
    root = hookio.tree_root(start.cwd)
    task, project = tree_task(root)
    source = BY_TREE if task else ""
    ordered = ordered_task(env)
    if ordered:
        # Заказ старше дерева: конвейер дашборда стартует в главном чекауте, и
        # по имени дерева там выходит не та задача либо никакая.
        task, source = ordered, BY_ORDER
    stamp = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(now))
    return ("%s сессия %s задача %s проект %s дерево %s транскрипт %s "
            "источник %s повод %s tmux %s\n") % (
        stamp, dashless(start.session), dashless(task), dashless(project),
        dashless(root), dashless(start.transcript), dashless(source),
        dashless(start.source), dashless(env.get(TMUX_ENV)))


# Ходы, которые считаются работой в дереве задачи: правка файла это работа, а
# чтение и поиск нет, иначе разговор привязывался бы к задаче за один взгляд в
# её дерево (POC ветки poc-chat).
WORK_TOOLS = ("Edit", "Write", "NotebookEdit", "MultiEdit")

BY_WORK = "работа"


def touch_record(tool, now=None):
    """Строка журнала про работу сессии в дереве задачи. Пусто значит, что ход
    работой не считается или дерево про задачу молчит."""
    if tool.tool not in WORK_TOOLS or not tool.session:
        return ""
    root = hookio.tree_root(tool.cwd)
    task, project = tree_task(root)
    if not task:
        return ""
    stamp = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(now))
    return ("%s сессия %s задача %s проект %s дерево %s транскрипт %s "
            "источник %s повод %s tmux %s\n") % (
        stamp, dashless(tool.session), dashless(task), dashless(project),
        dashless(root), "-", dashless(BY_WORK), "правка файла", "-")


def known_touch(path, session, task):
    """Такая привязка уже лежит в журнале: повторять строку на каждой правке
    незачем, журнал раздулся бы за один заход в сотню строк."""
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            data = fh.read()
    except OSError:
        return False
    mark = "сессия %s задача %s" % (session, task)
    return mark in data


def run_touch(protocol, path=None, now=None):
    tool = hookio.tool_event(protocol)
    if tool is None:
        return 0
    line = touch_record(tool, now)
    if not line:
        return 0
    log = path or LOG
    task = line.split(" задача ")[1].split(" ")[0]
    if known_touch(log, dashless(tool.session), task):
        return 0
    hookio.append_capped(log, line)
    return 0


def run_hook(protocol, path=None, env=None, now=None):
    start = hookio.start_event(protocol)
    if start is None or not start.session:
        # Чужое событие и событие без ID сессии в реестр не попадают: запись
        # без сессии не сводится ни с транскриптом, ни с ручкой привязки.
        return 0
    hookio.append_capped(path or LOG, record(start, env, now))
    return 0


def main(argv):
    if not argv or argv[0] not in ("--hook", "--touch"):
        sys.stderr.write(__doc__)
        return 2
    try:
        if argv[0] == "--touch":
            return run_touch(hookio.protocol(argv[1:]))
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("session-task: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
