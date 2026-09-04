#!/usr/bin/env python3
"""Подхват реплики devkit: донести сказанное человеком до идущей сессии.

Реплику с экрана чата дашборд кладёт строкой во вход разговора, и до этого хука
её читал только следующий виток цели. Между двумя витками проходят десятки
минут, а человек, написавший «стой, не туда», всё это время ждёт. Подхват стоит
на завершённом ходе инструмента, читает разговор сессии и вносит лежащие строки
прямо в контекст идущего захода каналом добавки. Решение целиком в
docs/lld/DK-136-live-chat.md.

Носителя два, и механика у них одна. Реплики цели лежат в разделе «Входящие»
файла цели, адрес ищется через реестр целей (LLD DK-136). Реплики любой другой
живой сессии лежат во входах .devkit/chat/<имя>.in дерева работы, и адресат
записан в самой строке: строка «..., сессии <ID>: ...» доезжает только этой
сессии, строка без адресата доезжает любой сессии дерева (DK-345). Имена
разговоров устойчивы к смене сессии: task-<ID> переживает парковку задачи, а
sess-<ID> это личный разговор сессии без задачи. Отметка доставки входу не
нужна: доставленная строка уходит из него тем же замком, что и пишется, поэтому
лежащая строка всегда непрочитанная. Признак ожидания .ask и замок .lock у
входа те же по формату, что у цели, и читаются теми же функциями.

Прежний каталог .devkit/mail с файлами <имя>.inbox читается наравне с новым
(DK-440): строка, написанная дашбордом за минуту до выката, доезжает тем же
ходом, а не пропадает молча.

Режим один:
  chat-in.py --hook [протокол]  событие читается со stdin и разбирается по имени
                                протокола таблицей hookio.py (голый --hook это
                                claude-code)

Адрес реплик цели ищется в три шага. Реестр целей ~/.devkit/goals/*.watch
называет цель, её файл и корень; дерево хода берётся из cwd события и
сверяется с корнями по границе пути; сессия сверяется с именем витка из файла
session в каталоге замка, пока замок держит живая оболочка. Адрес реплик
разговора проще: дерево это ближайший предок cwd с .git, а сессию называет сама
строка. Ход субагента не получает реплик вовсе: реплика адресована витку или
сессии, а не тому, кого они позвали.

Живость цели решает, доставлять ли, и признаки идут от точного к грубому. Сам
факт хода значит живую сессию, живой pid в замке значит ведомую цель, а в
режиме чата остаётся метка движения (seen в записи реестра и последняя чужая
строка журнала цикла) с порогом в три часа. Отметку stopped подхват не читает
ни в одном режиме: сторожок меряет ею движение дерева, а не цели, и у живого
витка, висящего на ожидании исполнителей, она стоит наравне с брошенным.

Отметки доставки лежат в .devkit/goal-<ID>.mail рядом с журналом цикла, а не в
файле цели: docs/tasks пушится коммитом на каждую правку, и хук, дописывающий
файл цели на ходе инструмента, наплодил бы коммитов посреди витка. Ключ отметки
это строка «Входящих» целиком, живёт отметка, пока лежит её строка, и пишется
до доставки: реплика, доставленная в тут же упавшую сессию, полежит во
«Входящих» до следующего витка, а реплика, доставляемая на каждом ходе по
кругу, сожгла бы бюджет цели. Вход цели общий с ключом --ask оболочки,
который спрашивает человека и читает те же «Входящие», поэтому запись идёт под
flock соседнего файла .devkit/goal-<ID>.mail.lock: не взявший замок подхват
уходит тихим нолём, лежащую строку доставит сосед.

Любая беда за развилкой цели (битая запись реестра, нечитаемый файл цели, не
пишется отметка) это тихий ноль и строка в свой журнал ~/.devkit/chat-in.log: хук
стоит на каждом ходе чужой работы, и ронять её ему нельзя, а молчать про
непонятое значит держать дыру незаметной.

Переменные окружения:
  DEVKIT_CHAT_TRACE=1   строка в журнал на каждый путь мимо корней целей и на
                         каждый промах имени сессии. Без ключа они молчат:
                         таких ходов на машине десятки за час, и поток строк
                         поверх обещанной дешевизны хука топит нужную строку.
                         Жалоба «реплика не доезжает вот из этой сессии»
                         разбирается нарочным прогоном с ключом
"""
import collections
import fcntl
import json
import os
import re
import subprocess
import sys
import time

import hookio

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GOAL_RUN = os.path.join(ROOT, "kit", "skills", "goal-loop", "goal-run.py")

STAMP = "%Y-%m-%dT%H:%M:%S"
# Порог метки движения цели, три часа. Судить «идёт ли виток прямо сейчас» порог
# не годится: самый длинный наблюдавшийся виток шёл час, а между двумя
# поворотами внутри витка проходит и сорок минут. Он ограничивает окно
# брошенной цели, и в этом вся его работа.
MOVED = 3 * 3600
GOAL_HEADER = "## Входящие"
# Подпись дашборда в строке разговора: по ней из строки достаётся текст реплики.
# Рукописная строка идёт в журнал цикла как есть.
FROM_DASHBOARD = ", из дашборда: "
# Адресат внутри строки разговора: «<стамп>, сессии <ID>, из дашборда: текст».
# Стоит перед подписью дашборда и кончается запятой, а ID сессии из цифр и
# дефисов, поэтому граница читается по первой запятой после адресата.
TO_SESSION = ", сессии "
# Приставки имён разговоров. Имя несёт того, кому разговор принадлежит:
# task-<ID> это разговор задачи, sess-<ID> личный разговор сессии. Безадресную
# строку такого разговора забирает только его хозяин, а не любой ход в дереве
# (DK-397 POC: реплика в снятый разговор задачи уезжала в чужую сессию того же
# чекаута).
TASK_CHAT = "task-"
SESS_CHAT = "sess-"
# ID задачи в имени разговора и в хвосте имени бокового дерева. Тот же вид, что
# у реестра чатов (hooks/session-task.py) и у разбора дашборда.
TASK_ID_RE = re.compile(r"^[A-Z]{2,10}-[0-9]{1,6}$")
TREE_TASK_RE = re.compile(r"(?:^|-)([A-Za-z]{2,10}-[0-9]{1,6})$")
# Реестр чатов: он говорит, какую задачу ведёт сессия. Пишет его хук старта
# hooks/session-task.py, читают дашборд (internal/sessions) и этот подхват.
SESS_LOG = os.path.join(".devkit", "sessions.log")
# Ключевые слова полей строки реестра, порядком записи.
BIND_KEYS = ("сессия", "задача", "проект", "дерево", "транскрипт",
             "источник", "повод", "tmux")
# Каталог разговоров любой сессии в дереве работы: .devkit/chat/<имя>.in.
# Поля признака ожидания ниже срока: кто ждёт и по какой задаче. Пишет их
# писатель taskctl ask (internal/chat), зовёт его хук ask-panel.py на вопросе
# AskUserQuestion (DK-715), читают подхват и сторожок.
ASK_SESSION = "сессия "
ASK_TASK = "задача "
# Метка «без срока» на месте штампа времени (internal/chat.AskForever,
# DK-715): признак без неё живёт до ответа, а не до часов.
ASK_FOREVER = "-"
CHAT_DIR = "chat"
CHAT_SUFFIX = ".in"
# Прежние имена того же носителя (DK-440). Каталог читается наравне с новым один
# выпуск: реплика, написанная дашбордом до выката, лежит в нём, и терять её
# из-за момента обновления нельзя.
OLD_CHAT_DIR = "mail"
OLD_CHAT_SUFFIX = ".inbox"
# Начало строки доставки в журнале цикла. Оно же признак своей строки: журнал
# это одна из двух меток живости, а свои строки подхват не должен считать
# движением цели.
SAY_WORD = "чат"
# Прежнее слово строки доставки: журналы целей его помнят, и свои старые строки
# подхват обязан узнавать по-прежнему, иначе они пойдут за движение цели.
SAY_WORD_OLD = "разговор"
TEXT_LIMIT = 200
TRACE_ENV = "DEVKIT_CHAT_TRACE"

# Отметка доставки: время, сессия и доставленная строка «Входящих» целиком.
# Ключ это строка, а не текст сообщения: тот же текст, посланный через час
# заново, иначе нашёл бы старую отметку и не доехал бы вовсе.
Mark = collections.namedtuple("Mark", "stamp session line")


def tracing(env=None):
    env = os.environ if env is None else env
    return bool((env.get(TRACE_ENV) or "").strip())


def log_path():
    return os.path.join(os.path.expanduser("~"), ".devkit", "chat-in.log")


def log(session, addr, reason):
    """Строка в свой журнал: время, сессия, адрес и повод. Адрес это ID цели
    либо имя разговора, поводы перечислены поимённо в README, и все они за
    развилкой «дерево сошлось с адресом реплики», так что строк тут ровно
    столько, сколько на машине живых целей и разговоров с лежащей строкой."""
    hookio.append_capped(log_path(), "%s сессия %s адрес %s %s\n"
                         % (time.strftime(STAMP), session or "-", addr or "-", reason))


def read_kv(path):
    """Запись реестра целей ключами и значениями. Формат тот же «ключ =
    значение», каким её пишет гейт бюджета (watchRegister, tools/agentctl/watch.go)."""
    data = {}
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            body = f.read()
    except OSError:
        return data
    for line in body.split("\n"):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if sep:
            data[key.strip()] = val.strip()
    return data


def goals():
    """Цели под надзором с их корнями. Корень считается от поля file, а не
    берётся из root: file гейт приводит к абсолютному пути при записи, а root
    пишет как пришёл, и относительный путь под сверку с cwd не годится."""
    d = os.path.join(os.path.expanduser("~"), ".devkit", "goals")
    try:
        names = sorted(os.listdir(d))
    except OSError:
        return []
    out = []
    for name in names:
        if not name.endswith(".watch"):
            continue
        entry = read_kv(os.path.join(d, name))
        goal, path = entry.get("goal", ""), entry.get("file", "")
        if not goal or not os.path.isabs(path):
            continue
        # Файл цели это <корень>/docs/tasks/<ID>.md, отсюда и три шага вверх.
        entry["root"] = os.path.dirname(os.path.dirname(os.path.dirname(path)))
        out.append(entry)
    return out


def border(cwd, root):
    """Путь лежит в корне по границе пути. Подстрокой сюда попало бы соседнее
    дерево задачи, чьё имя начинается с имени корня, а ход исполнителя под
    адрес цели попадать не должен."""
    root = root.rstrip(os.sep)
    return cwd == root or cwd.startswith(root + os.sep)


def under(cwd, root):
    """Дерево хода лежит в корне цели. Сверка сперва текстовая, обе стороны как
    есть, и на живой машине она сходится сразу: корень цели это дерево в
    ~/projects. За ней идёт вторая, по realpath, и нужна она стендам: на macOS
    /tmp и /var/folders это симлинки в /private, харнес приносит cwd уже
    развёрнутым, а запись реестра держит корень таким, каким его назвал вызов.
    Без второй сверки цель, чей корень лежит под временным каталогом, реплик не
    получает вовсе, и живой прогон это показал первым же ходом."""
    if not cwd or not root:
        return False
    if border(cwd, root):
        return True
    return border(os.path.realpath(cwd), os.path.realpath(root))


def goal_lines(doc):
    """Строки раздела «Входящие»: реплики, которых виток ещё не подхватил.
    Разбор тот же, что у дашборда (inboxLines, tools/dashboard/messages.go)."""
    out, inside = [], False
    for line in doc.split("\n"):
        line = line.rstrip(" ")
        if line == GOAL_HEADER:
            inside = True
            continue
        if not inside:
            continue
        if line.startswith("## "):
            break
        if line.startswith("- "):
            out.append(line[2:])
    return out


def read_marks(path):
    """Отметки доставки. Запись это две строки: «время сессия» и сама
    доставленная строка. Строка держится отдельной, потому что в ней живут
    пробелы, ёлочки и что угодно ещё, что написал человек."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            rows = f.read().split("\n")
    except OSError:
        return []
    out = []
    for i in range(0, len(rows) - 1, 2):
        head, line = rows[i].strip(), rows[i + 1]
        if not head or not line:
            continue
        stamp, _, session = head.partition(" ")
        out.append(Mark(stamp, session.strip() or "-", line))
    return out


def marks_body(marks):
    return "".join("%s %s\n%s\n" % (m.stamp, m.session or "-", m.line) for m in marks)


def write_marks(path, marks):
    """Файл отметок переписывается целиком: горизонт у отметки тот же, что у её
    строки «Входящих», значит записью решается и то, какие отметки остаются."""
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(marks_body(marks))
    os.replace(tmp, path)


def write_lines(path, lines):
    """Переписать вход разговора оставшимися строками. Временный файл с заменой ради
    того же, чего ради него у отметок: читатель на половине записи видит либо
    старое, либо новое, но не огрызок."""
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write("".join(line + "\n" for line in lines))
    os.replace(tmp, path)


def take_chat_lock(path):
    """Замок отметок на дескрипторе соседнего файла. Своё имя ему нужно ради
    сверки inode: файл отметок пересоздаётся на каждой записи, и замок на нём
    самом разъезжался бы с путём. None значит, что пишет сосед: ждать своей
    очереди ходу инструмента незачем, лежащую строку доставит он."""
    try:
        fd = os.open(path, os.O_CREAT | os.O_RDWR, 0o644)
    except OSError:
        return None
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if os.fstat(fd).st_ino == os.stat(path).st_ino:
            return fd
    except OSError:
        pass
    os.close(fd)
    return None


def shell_lock(root, goal):
    """Оболочка цели: жива ли она и какому витку выдано имя. Живость меряется
    тем же kill -0 по pid, каким меряет её сама оболочка (lock_busy,
    kit/skills/goal-loop/goal-run.py), так что замок убитой оболочки читается
    как её отсутствие, а не как чужая цель."""
    d = os.path.join(root, ".devkit", "goal-%s.lock" % goal)
    try:
        with open(os.path.join(d, "pid"), encoding="utf-8") as f:
            pid = f.read().strip()
        os.kill(int(pid), 0)
    except (OSError, ValueError):
        return False, ""
    try:
        with open(os.path.join(d, "session"), encoding="utf-8") as f:
            return True, f.read().strip()
    except OSError:
        return True, ""


def stamp_at(text):
    """Время из метки. None значит, что метки нет: отсутствующий файл и пустое
    поле это именно отсутствие, а не нулевое время, иначе цель, которую ведут
    первый раз, оказалась бы протухшей с рождения."""
    try:
        return time.mktime(time.strptime((text or "").strip(), STAMP))
    except (ValueError, OverflowError):
        return None


def journal_at(path):
    """Время последней чужой строки журнала цикла. Чужая тут значит не своя:
    строку доставки подхват кладёт в тот же журнал, и движением цели она не
    считается, иначе он держал бы цель живой сам за себя."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            rows = f.read().split("\n")
    except OSError:
        return None
    for line in reversed(rows):
        head, _, rest = line.strip().partition(" ")
        if rest.startswith(SAY_WORD) or rest.startswith(SAY_WORD_OLD):
            continue
        at = stamp_at(head)
        if at is not None:
            return at
    return None


def moved_at(entry, root, goal):
    """Когда цель двигалась в последний раз. Меток две, берётся свежая, и дыры
    у них разные: seen в записи реестра пишет гейт бюджета в начале каждого
    витка и закрывает холодный старт цели, которую ведут в чате впервые, а
    журнал цикла двигается всю дорогу и закрывает виток длиннее порога, где
    seen уже протух."""
    marks = [stamp_at(entry.get("seen", "")),
             journal_at(os.path.join(root, ".devkit", "goal-%s.log" % goal))]
    marks = [m for m in marks if m is not None]
    return max(marks) if marks else None


def ask_stamp(path):
    """Срок признака ожидания из файла. Формат один у цели и у разговоров
    .devkit/chat: строка со штампом, и лежит срок внутри файла затем, чтобы
    убитый ход ожидания не запер вход навсегда. None значит, что файла нет или
    срок не разобрался, то есть никто не ждёт."""
    try:
        with open(path, encoding="utf-8") as f:
            return stamp_at(f.readline())
    except OSError:
        return None


def ask_fields(path):
    """Признак ожидания разбором: срок, ждущая сессия, задача и пачка вопросов
    JSON. Первой строкой в файле стоит срок либо метка «без срока» (DK-715,
    internal/chat.AskForever), поэтому однострочный признак цели читается тем
    же разбором, а поля ниже приезжают от писателя ожидания (internal/chat,
    LLD DK-430, решение 2). Возврат словарём: читателей у признака трое, и
    каждому нужны свои поля. Поле `until` в возврате это `None` у признака без
    срока, что не то же самое, что возврат `None` целиком: тот значит «файла
    нет или он не разобрался», этот «признак есть, срока у него нет»."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            lines = f.read().split("\n")
    except OSError:
        return None
    first = (lines[0] if lines else "").strip()
    if first == ASK_FOREVER:
        until = None
    else:
        until = stamp_at(first)
        if until is None:
            return None
    out = {"until": until, "session": "", "task": "", "questions": []}
    body = []
    for ln in lines[1:]:
        ln = ln.strip()
        if ln.startswith(ASK_SESSION):
            out["session"] = ln[len(ASK_SESSION):].strip()
        elif ln.startswith(ASK_TASK):
            out["task"] = ln[len(ASK_TASK):].strip()
        elif ln:
            body.append(ln)
    if body:
        try:
            pack = json.loads("\n".join(body))
        except ValueError:
            pack = {}
        if isinstance(pack, dict):
            out["questions"] = [q.get("text", "") for q in pack.get("questions", [])
                                if isinstance(q, dict)]
    return out


def ask_until(root, goal):
    """Срок признака ожидания .devkit/goal-<ID>.ask. Пока он не вышел, вход
    читает ключ --ask оболочки: виток задал человеку прямой вопрос и ждёт
    ответа сам, вторым читателем подхвату там делать нечего."""
    return ask_stamp(os.path.join(root, ".devkit", "goal-%s.ask" % goal))


# Дерево работы ищет hookio: тем же предком с .git живёт реестр чатов, и две
# копии этого поиска разошлись бы на первой же особенности бокового дерева.
tree_root = hookio.tree_root


def chat_names(root):
    """Разговоры дерева тройками «каталог, имя, расширение»: файл <имя>.in в
    каталоге .devkit/chat. Вход без реплик убирается при доставке, поэтому
    живой файл в каталоге значит лежащую строку. Прежний каталог .devkit/mail
    с файлами <имя>.inbox перебирается следом (DK-440): пока он есть, лежащая
    в нём строка доезжает тем же ходом, а признак ожидания и замок у неё лежат
    там же, рядом с ней."""
    out = []
    for sub, suffix in ((CHAT_DIR, CHAT_SUFFIX), (OLD_CHAT_DIR, OLD_CHAT_SUFFIX)):
        d = os.path.join(root, ".devkit", sub)
        try:
            names = sorted(os.listdir(d))
        except OSError:
            continue
        out += [(d, n[:-len(suffix)], suffix) for n in names if n.endswith(suffix)]
    return out


def addressee(line):
    """Сессия, которой реплика адресована. Пустая строка значит «любой сессии
    дерева»: так ответ припаркованной задаче доезжает той, что её продолжит,
    не дожидаясь возвращения умершей сессии. Рукописная строка адресата не
    несёт и читается всеми так же."""
    _, sep, rest = line.partition(TO_SESSION)
    if not sep:
        return ""
    return rest.partition(",")[0].strip()


def bind_line(line):
    """Сессия и её задача из строки реестра чатов либо (None, None). Разбор тот
    же, что в internal/sessions: значение поля собирается до следующего
    ключевого слова, поэтому пробел в пути дерева строку не рассыпает, а
    непонятая строка пропускается, не роняя разбора."""
    f = line.strip().split()
    if len(f) < 3 or f[1] != "сессия":
        return None, None
    vals, key = {}, ""
    for tok in f[1:]:
        if tok in BIND_KEYS and vals.get(key):
            key = tok
            continue
        if not key:
            key = tok
            continue
        vals[key] = (vals.get(key, "") + " " + tok).strip()
    sid = vals.get("сессия", "").strip()
    if sid in ("", "-"):
        return None, None
    task = vals.get("задача", "").strip()
    return sid, "" if task == "-" else task.upper()


def session_task(session, home=None):
    """Задача, которую ведёт сессия по реестру чатов. Выигрывает последняя её
    запись: перепривязка и отвязка это обычные строки журнала, а не правка
    файла, и снятая привязка приезжает пустой задачей (LLD DK-430, решение 1).
    Реестра нет, значит сессия не ведёт ничего: молчание тут строже догадки."""
    home = os.path.expanduser("~") if home is None else home
    try:
        with open(os.path.join(home, SESS_LOG), encoding="utf-8", errors="replace") as f:
            rows = f.read().split("\n")
    except OSError:
        return ""
    task = ""
    for row in rows:
        sid, got = bind_line(row)
        if sid == session:
            task = got
    return task


def tree_task(root):
    """Задача бокового дерева: хвост имени каталога <проект>-<ID>. Дерево
    задачи принадлежит ей целиком, и разговор задачи там свой по месту."""
    m = TREE_TASK_RE.search(os.path.basename(root or ""))
    return m.group(1).upper() if m else ""


def owns_chat(name, session, root, home=None):
    """Хозяин ли сессия разговору name. Безадресную строку забирает только он:
    имя разговора несёт того, кому строка предназначена, и отдавать её первому
    ходу в дереве значит увозить реплику в чужую сессию.

    Разговор задачи принадлежит той сессии, что ведёт задачу по реестру чатов
    либо работает в её боковом дереве. Личный разговор сессии принадлежит ей
    одной, и её ID стоит прямо в имени. Имя без приставки хозяина не называет
    (рукописный разговор), и там всё остаётся по-прежнему."""
    if name.startswith(SESS_CHAT):
        return name[len(SESS_CHAT):] == session
    if name.startswith(TASK_CHAT):
        task = name[len(TASK_CHAT):].upper()
        if not TASK_ID_RE.match(task):
            return True
        return task in (session_task(session, home), tree_task(root))
    return True


def for_me(line, session, own=True):
    addressee_line = addressee(line)
    if addressee_line:
        return addressee_line == session
    return own


def chat_add(name, lines):
    """Текст добавки контекста из разговора. Отсылки к скиллу goal-loop тут
    нет: читатель разговора бывает исполнителем задачи и грумером черновика, и
    разряд реплики он выбирает по месту своей работы."""
    body = "\n".join("- %s" % said(line) for line in lines)
    return ("Живая реплика человека из разговора %s, доставлена посреди хода каналом чата devkit:\n"
            "%s\n"
            "Строка уходит из разговора в момент доставки, повторно она не приедет." % (name, body))


def serve_chat(d, name, suffix, turn, now, tree=None):
    """Один разговор: текст добавки либо None. Отличие от цели в том, что
    доставленная строка уходит из входа тут же, под тем же замком: читателя,
    который забирает строку своей записью, у входа нет, и отдельная механика
    отметок ему не нужна. Отметкой служит отсутствие строки, и любые читатели,
    подхват и будущий инструмент ожидания, расходят реплики одним замком."""
    src = os.path.join(d, name + suffix)
    # Живой признак ожидания запирает не весь вход, а только те строки, которые
    # заберёт ждущий: безадресные и адресованные ему самому (LLD DK-430,
    # решение 2). Реплики другим сессиям идут как обычно, и два живых чата по
    # одной задаче лежат в одном входе, не глуша друг друга.
    # Признак без срока (DK-715) тут не держит вовсе: живого процесса, который
    # читал бы ответ отдельно от подхвата, у него больше нет, --wait и опрос
    # входа ушли из taskctl ask вместе с этой правкой. Держит только признак с
    # настоящим, ещё не вышедшим сроком.
    ask = ask_fields(os.path.join(d, name + ".ask"))
    waiting = None
    if ask is not None and ask["until"] is not None and ask["until"] > now:
        waiting = ask["session"]
        log(turn.session, name, "частичный отказ: разговор держит вопрос до %s, ждёт инструмент ожидания"
            % time.strftime(STAMP, time.localtime(ask["until"])))
    fd = take_chat_lock(os.path.join(d, name + ".lock"))
    if fd is None:
        return None
    try:
        try:
            with open(src, encoding="utf-8", errors="replace") as f:
                lines = [l.strip() for l in f.read().split("\n") if l.strip()]
        except OSError:
            return None
        own = owns_chat(name, turn.session, tree)
        taken = [l for l in lines if for_me(l, turn.session, own)]
        if waiting is not None:
            taken = [l for l in taken if addressee(l) and addressee(l) != waiting]
        if not taken:
            # Отказ хозяина громкий, а не под ключом трассировки: безадресная
            # строка лежит в разговоре, чья сессия снята, и молчание тут
            # неотличимо от доставки. По этой строке видно, что реплика ждёт
            # хозяина, а не уехала в первый попавшийся ход.
            if not own and any(not addressee(l) for l in lines):
                log(turn.session, name,
                    "отказ: разговор принадлежит не этой сессии, безадресная строка ждёт хозяина")
            elif tracing() and lines:
                other = next((addressee(l) for l in lines if addressee(l)), "-")
                log(turn.session, name, "отказ: реплика адресована сессии %s, а ход идёт сессией %s"
                    % (other, turn.session or "-"))
            return None
        lying = set(taken)
        try:
            if len(taken) == len(lines):
                os.remove(src)
            else:
                write_lines(src, [l for l in lines if l not in lying])
        except OSError as e:
            log(turn.session, name, "отказ: доставленное из разговора не убрать: %s" % e)
            return None
    finally:
        os.close(fd)
    log(turn.session, name, "доставлено строк %d, первая «%s»"
        % (len(taken), short(said(taken[0]))))
    return chat_add(name, taken)


def short(text, limit=TEXT_LIMIT):
    text = " ".join((text or "").split())
    return text if len(text) <= limit else text[:limit] + "..."


def said(line):
    """Что человек написал: подпись дашборда из строки убирается, рукописная
    строка идёт как есть."""
    _, sep, text = line.partition(FROM_DASHBOARD)
    return text if sep else line


def voice(lines):
    """Строка доставки для журнала цикла. Начинается словом «чат»: журнал
    смотрят панелью tmux и tail'ом, и человек видит доставку там же, где смотрит
    ход витка."""
    said_all = ["«%s»" % short(said(line)) for line in lines]
    if len(said_all) == 1:
        return "%s: витку доставлена реплика %s" % (SAY_WORD, said_all[0])
    return "%s: витку доставлены реплики %s" % (SAY_WORD, "; ".join(said_all))


def goal_add(goal, lines):
    """Текст добавки контекста. Рамки провала у него нет: реплика человека это
    не претензия к ходу инструмента, и повторять ход витку незачем."""
    body = "\n".join("- %s" % line for line in lines)
    return ("Живая реплика человека по цели %s, доставлена посреди витка каналом чата devkit:\n"
            "%s\n"
            "Разряд реплики и реакцию на неё виток выбирает по скиллу goal-loop; во «Входящих» "
            "файла цели строка остаётся до записи витка." % (goal, body))


def say(root, goal, session, lines):
    """Строка доставки в журнал цикла тем же ключом --say, каким пишет ход сам
    виток: формат журнала остаётся в одном месте. Вывод оболочки забирается
    себе, и это не мелочь реализации: --say печатает записанную строку в свой
    stdout, а на stdout у подхвата лежит его единственный ответ, и приехавшая
    туда строка журнала встала бы перед JSON, который читает харнес."""
    try:
        p = subprocess.run(["python3", GOAL_RUN, goal, "-C", root, "--say", voice(lines)],
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        log(session, goal, "строку доставки не записать в журнал цикла: %s" % e)
        return
    if p.returncode != 0:
        log(session, goal, "оболочка не записала строку доставки, код %d: %s"
            % (p.returncode, short(p.stdout)))


def pending(entry, marks_path):
    """Сколько во «Входящих» лежит непомеченных строк. Читается это без замка:
    решается тут строка в журнал, а не доставка, и устаревшее чтение стоит
    лишней строки, а не потерянной реплики."""
    try:
        with open(entry["file"], encoding="utf-8", errors="replace") as f:
            lines = goal_lines(f.read())
    except OSError:
        return 0
    known = {m.line for m in read_marks(marks_path)}
    return len([line for line in lines if line not in known])


def miss(entry, turn, named, marks_path):
    """Промах имени сессии. Под живым замком это обычный ход соседнего окна
    корня, и таких за час десятки, поэтому сам по себе строки он не стоит.
    Строку он получает, когда во «Входящих» есть что доставлять: реплика есть,
    адресата нет, и это ровно та жалоба, с которой человек приходит. Названы в
    строке обе стороны, чтобы стык «ID из --session-id доезжает до события»
    разбирался по журналу, а не по догадкам."""
    n = pending(entry, marks_path)
    if not n and not tracing():
        return
    log(turn.session, entry["goal"],
        "отказ: реплика адресована витку %s, а ход идёт сессией %s, непомеченных строк %d"
        % (named or "-", turn.session or "-", n))


def serve(entry, turn, now):
    """Разговор одной цели: текст добавки либо None, когда доставлять нечего
    или некому."""
    goal, root = entry["goal"], entry["root"]
    marks_path = os.path.join(root, ".devkit", "goal-%s.mail" % goal)
    live, named = shell_lock(root, goal)
    if live:
        # Под оболочкой адресат один, виток из файла session. Тут не смотрится
        # никаких меток времени: мерка грубее его не должна перебивать точный
        # признак.
        if not named or turn.session != named:
            miss(entry, turn, named, marks_path)
            return None
    else:
        at = moved_at(entry, root, goal)
        if at is None:
            log(turn.session, goal, "отказ: в записи реестра нет ни одной метки движения, "
                                    "запись битая")
            return None
        if now - at > MOVED:
            # Цель брошена: работу по ней никто не ведёт, и реплика подождёт
            # человека, а не уедет постороннему окну корня. Строки такому
            # отказу не положено, за неё отвечает дашборд состоянием «ждёт
            # витка», но нарочный разбор её получает.
            if tracing():
                log(turn.session, goal, "отказ: цель не двигалась %d мин при пороге %d мин"
                    % ((now - at) / 60, MOVED / 60))
            return None
    until = ask_until(root, goal)
    if until is not None and until > now:
        log(turn.session, goal, "отказ: вход цели держит вопрос витка до %s, отвечает на него --ask"
            % time.strftime(STAMP, time.localtime(until)))
        return None
    fd = take_chat_lock(marks_path + ".lock")
    if fd is None:
        return None
    try:
        try:
            with open(entry["file"], encoding="utf-8", errors="replace") as f:
                lines = goal_lines(f.read())
        except OSError as e:
            log(turn.session, goal, "отказ: файла цели не прочитать: %s" % e)
            return None
        marks = read_marks(marks_path)
        # «Входящие» читаются раньше отметок: отметка, чьей строки там больше
        # нет, не считается вовсе и в новый файл не переносится, поэтому файл
        # не растёт и отдельная уборка ему не нужна.
        lying = set(lines)
        kept = [m for m in marks if m.line in lying]
        known = {m.line for m in kept}
        fresh = [line for line in lines if line not in known]
        if not fresh and len(kept) == len(marks):
            return None
        stamp = time.strftime(STAMP, time.localtime(now))
        # Отметка встаёт до доставки, а не после. Размен сознательный и в
        # пользу потери: доставленная в упавшую сессию реплика лежит во
        # «Входящих» до следующего витка, а доставляемая по кругу превратила бы
        # виток в мельницу.
        try:
            write_marks(marks_path, kept + [Mark(stamp, turn.session or "-", line) for line in fresh])
        except OSError as e:
            log(turn.session, goal, "отказ: отметку доставки не записать: %s" % e)
            return None
    finally:
        os.close(fd)
    if not fresh:
        return None
    say(root, goal, turn.session, fresh)
    log(turn.session, goal, "доставлено строк %d, первая «%s»"
        % (len(fresh), short(said(fresh[0]))))
    return goal_add(goal, fresh)


def run_hook(protocol, now=None):
    turn = hookio.tool_event(protocol)
    if turn is None or not turn.cwd:
        return 0
    if turn.agent:
        # Ход субагента: реплика адресована витку или сессии, а не тому, кого
        # они позвали. География закрывает не всех (Explore и proofread зовутся
        # по тому же дереву), а в событии хода субагента есть роль, и отсев ею
        # честнее.
        return 0
    now = time.time() if now is None else now
    adds, found = [], False
    entries = goals()
    for entry in entries:
        # Записей на одно дерево бывает и несколько, когда в работе две цели
        # разом: имя сессии разводит витки под оболочкой, а витку в чате
        # достаются реплики обеих, и это одно и то же окно человека.
        if not under(turn.cwd, entry["root"]):
            continue
        found = True
        text = serve(entry, turn, now)
        if text:
            adds.append(text)
    if not found and tracing():
        # Путь мимо корней целей это любая чужая сессия машины, включая
        # песочницу харнеса. Своего правила про песочницу тут нет по-прежнему:
        # песочный путь под корень цели не ложится никогда, а лишняя проверка
        # сломала бы стенд, чей корень лежит под временным каталогом.
        log(turn.session, "-", "путь %s мимо корней целей" % turn.cwd)
    root = tree_root(turn.cwd)
    if root is not None:
        for d, name, suffix in chat_names(root):
            text = serve_chat(d, name, suffix, turn, now, root)
            if text:
                adds.append(text)
    if not adds:
        return 0
    return hookio.context(protocol).say("\n\n".join(adds))


def main(argv):
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("chat-in: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
