#!/usr/bin/env python3
"""Сторожок цикла цели: цель в работе, а движения нет дольше порога, значит
зовём человека громким уведомлением.

  python3 watch.py [--idle <минуты>]
      обойти реестр целей и позвать по вставшим. Та же работа стоит за
      `devkitctl watch`, и её же будит launchd. Выход 0 всё движется, 1
      нашёлся вставший цикл, 2 ошибка запуска.

Цикл цели ведут двое, оболочка goal-run и сессия живого чата, и встают они
одинаково незаметно: зависший фон, обрыв соединения, убитая сессия. Изнутри
такой стоп не поймать (ждущая сессия жива и молчит), поэтому сторожок живёт
вне сессии, отдельным запуском по расписанию. Носитель расписания на macOS это
launchd-агент, его кладёт `devkitctl doctor --fix`.

Что цикл должен идти, сторожок узнаёт из реестра `~/.devkit/goals`: запись туда
кладёт гейт бюджета `agentctl spend --goal`, который стоит в начале каждого
витка и у оболочки, и в чате. Движение меряется по `.devkit/log` проекта: туда
пишет строку каждая утилита devkit при каждом вызове, а виток без вызова
утилит не обходится. Цель, ушедшая с доски или из In progress, снимается с
надзора вместе со своей записью.

Позвав, сторожок ставит в запись отметку `stopped`: второй раз по тому же стопу
он молчит. Движение свежее отметки её снимает, снимает её и гейт следующего
витка. Сам цикл сторожок не поднимает, а называет в зове готовую команду
продолжения: стоп, доживший до порога, оболочка своими попытками уже не
пережила, а цикл в чате оболочкой не заменяется.

Порог простоя берётся из `~/.devkit/watch.local` (строка `idle = <минуты>`), а
без файла считается умолчанием в 45 минут: виток режет работу вызовами утилит
чаще, чем раз в час, а короче десятка минут порог ловил бы долгую сборку.

Тем же тиком сторожок будит припаркованные вопросом задачи (LLD DK-400,
решение 2): строка в Blocked с причиной «вопрос:» и лежащим ответом в разговоре
задачи возвращается в In progress вызовом `taskctl -C <корень> move <ID>
in-progress`. Будит сторожок и только он, а будить значит вернуть строку в
кандидаты планировщика, сессию поверх доски он не поднимает.

Тем же тиком идёт страховка ожидания (LLD DK-430, решение 3). Инструмент
`taskctl ask` паркует задачу сам, не дождавшись ответа, но SIGKILL от харнеса
не перехватывается ничем: убитый ход оставляет признак ожидания со своим сроком
и не паркует ничего, и строка молча стоит в In progress. Протухший признак
сторожок паркует сам, причиной из того же признака, где вопрос лежит текстом.
Живость решает реестр чатов `~/.devkit/sessions.log`: убитый ход ожидания не
значит убитой сессии, окно в vscode работает дальше, и парковка встала бы под
руками исполнителя. Живая сессия значит страховка молчит.

Тем же тиком сторожок льёт поезд (LLD DK-306, решение 4): по каждому корню
обхода он зовёт `shipctl -C <корень> ship --drain`, и поезд, оставшийся без
получателя события «очередь освободилась», доезжает до прода сам. Разлив
идемпотентен и молчит нулём на пустом поезде, занятой очереди, сломанном
проде и занятом чужим заходом конвейере, а провал деплоя не поднимает код
тика: уведомление шлёт сам shipctl через признак провала и taskctl fail.

Тем же тиком свежеет снимок квоты обеих подписок (DK-633): тик зовёт
`agentctl quota refresh --all --if-stale`, и протухший снимок переснимается и
тогда, когда на машине не идёт ни одной сессии, а дашборд выключен. Съём,
оставленный без свежего снимка исход и отказ видны строкой со счётом в журнале
сторожка, разбор по харнесам идёт в отчёт тика, а свежие снимки журнал не
трогают.

Тем же тиком закрываются агентские задачи из Check (DK-516): строка вида agent
с прогнанным smoke и непустым разделом «Проверка» доходит до Done без живой
сессии, а тех, кто из Check ждёт человека, тик не трогает вовсе. Отбор идёт
вердиктом `taskctl closable`, закрытие командой `taskctl close`.
"""
import importlib.util
import os
import re
import shutil
import say
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path

DEVKIT = Path(__file__).resolve().parent.parent.parent
NOTIFIER = DEVKIT / "hooks" / "notify.py"

GOALS_DIR = "~/.devkit/goals"
# Записи этапов работы: ~/.devkit/runs/<ID>-<slug>.run, поле «root» называет
# основной чекаут задачи. Реестр целей знает только корни живых циклов, а
# разговор с задачей идёт и вне цикла, поэтому корни на обход берутся из обоих
# мест (internal/stage пишет запись на каждый вердикт и на каждый вопрос).
RUNS_DIR = "~/.devkit/runs"
CONF = "~/.devkit/watch.local"
LOG = "~/.devkit/goal-watch.log"
LOG_LIMIT = 100 * 1024
LOG_KEEP = 500

IDLE = 45 * 60       # порог простоя по умолчанию, секунды
EVERY = 5 * 60       # как часто launchd будит сторожок, секунды
# Сколько прогонов сторожка можно пропустить, прежде чем это находка доктора:
# один пропуск бывает и от спящей машины, три подряд значит носитель не работает.
HEARTBEAT_MISS = 3

STAMP = "%Y-%m-%dT%H:%M:%S"
STAMP_FORMATS = (STAMP, "%Y-%m-%dT%H:%M", "%Y-%m-%d %H:%M:%S")
RUN_LOG = ".devkit/log"
BOARD = "docs/TASKS.md"
IN_PROGRESS = "In progress"
CHECK = "Check"
BLOCKED = "Blocked"
# Строка ответа `taskctl closable`: голый ID значит «закрывать можно», проза и
# перечень отказов идут ниже и под этот вид не подходят.
CLOSABLE_ID = re.compile(r"^[A-Z][A-Z0-9]*-\d+$")
# Причина блока с машинным префиксом «вопрос:» паркует задачу вопросом человека
# (LLD DK-400, решение 2): только такую строку будит лежащий в разговоре ответ,
# «окружение:» и проза ждут своего молча.
PARKED = "[блок: вопрос:"
# Вход разговора задачи и признак ожидания рядом с ним. Прежняя пара DK-440
# (.devkit/mail, task-<ID>.inbox) читается наравне с новой один выпуск: ответ,
# написанный до выката, обязан разбудить задачу так же, как написанный после.
CHAT_DIRS = (("chat", "task-%s.in"), ("mail", "task-%s.inbox"))
CHAT_ASK = "task-%s.ask"
# Реестр чатов задачи (LLD DK-430, решение 1): по нему страховка узнаёт сессию,
# ведущую задачу, и путь её транскрипта.
SESSIONS_LOG = "~/.devkit/sessions.log"
# Порог живости сессии, тот же, что на экране дашборда: молчащий дольше
# транскрипт считается неидущей сессией.
SESSION_LIVE = 12 * 60
# Ключевые слова полей строки реестра, те же, что у писателя hooks/session-task.py
# и у го-читателя internal/sessions: значение поля идёт до следующего слова,
# поэтому пробел в пути транскрипта строку не рассыпает.
REG_KEYS = ("сессия", "задача", "проект", "дерево", "транскрипт", "источник", "повод", "tmux",
            "родитель")
# Хвост журнала запусков, из которого берётся последняя метка времени: файл
# растёт всю жизнь проекта, и читать его целиком каждые пять минут незачем.
TAIL = 4096
# Порядок ключей записи реестра: сначала то, что пишет гейт, потом отметки
# сторожка. Незнакомые ключи не теряются, они дописываются в хвост.
KEYS = ("goal", "root", "file", "seen", "stopped")

LABEL = "ru.devkit.goal-watch"
PLIST = "~/Library/LaunchAgents/%s.plist" % LABEL


def home_path(home, path):
    """Путь из шапки модуля с подставленным домом: тесты гоняют сторожок на
    своём доме, живой прогон на настоящем."""
    tail = path[2:] if path.startswith("~/") else path
    return Path(home) / tail


def default_home():
    return Path(os.path.expanduser("~"))


def stamp_of(text):
    """Метка времени строки, None если не разобрана."""
    text = (text or "").strip()
    for fmt in STAMP_FORMATS:
        try:
            return datetime.strptime(text, fmt)
        except ValueError:
            pass
    return None


def read_entry(path):
    """Запись реестра словарём. Формат тот же, что у остальных локальных файлов
    devkit: строки `ключ = значение`, решётка комментарий."""
    data = {}
    try:
        text = Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return data
    for ln in text.splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#"):
            continue
        key, sep, val = ln.partition("=")
        if sep:
            data[key.strip()] = val.strip()
    return data


def write_entry(path, data):
    lines = ["%s = %s" % (k, data[k]) for k in KEYS if data.get(k)]
    lines += ["%s = %s" % (k, v) for k, v in sorted(data.items()) if k not in KEYS]
    try:
        Path(path).write_text("\n".join(lines) + "\n", encoding="utf-8")
    except OSError:
        pass


def conf_idle(home=None):
    """Порог простоя в секундах: строка `idle = <минуты>` в ~/.devkit/watch.local,
    без файла или с непонятной строкой умолчание."""
    home = default_home() if home is None else home
    for key, val in read_entry(home_path(home, CONF)).items():
        if key != "idle":
            continue
        try:
            minutes = int(val)
        except ValueError:
            return IDLE
        return minutes * 60 if minutes > 0 else IDLE
    return IDLE


def last_run_stamp(root):
    """Момент последнего вызова утилиты devkit в проекте, None если журнала нет
    или он пуст. Читается хвост файла: строки идут по возрастанию времени."""
    try:
        with open(os.path.join(root, RUN_LOG), "rb") as f:
            f.seek(0, os.SEEK_END)
            size = f.tell()
            f.seek(max(0, size - TAIL))
            tail = f.read().decode("utf-8", "replace")
    except OSError:
        return None
    for ln in reversed(tail.splitlines()):
        st = stamp_of(ln.split("\t")[0])
        if st is not None:
            return st
    return None


def board_section(root, goal_id):
    """Раздел доски, в котором стоит строка цели: «In progress», «Check» и так
    далее. Пустая строка значит, что цели на доске нет либо доски нет вовсе.

    Доска читается тут напрямую, а не через `taskctl show`: сторожок обязан
    работать и тогда, когда на машине сломано всё остальное, включая бинари
    devkit и их место в PATH."""
    try:
        text = Path(root, BOARD).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""
    section = ""
    for ln in text.splitlines():
        if ln.startswith("## "):
            section = ln[3:].strip()
            continue
        if not ln.startswith("|"):
            continue
        cell = ln.split("|")[1].strip() if ln.count("|") > 1 else ""
        if cell == goal_id:
            return section
    return ""


def board_present(root):
    return Path(root, BOARD).is_file()


# -- пробуждение припаркованных вопросом --------------------------------------

def chat_hook():
    """Модуль подхвата реплики hooks/chat-in.py либо None. Признак ожидания .ask
    у разговора задачи читается тем же разбором, каким его читает подхват, иначе
    вторая копия формата разъехалась бы с первой на первой же правке. Дефис в
    имени файла не годится для import, поэтому модуль грузится по пути."""
    sys.path.insert(0, str(DEVKIT / "hooks"))
    try:
        spec = importlib.util.spec_from_file_location(
            "devkit_chat_in", str(DEVKIT / "hooks" / "chat-in.py"))
        if spec is None or spec.loader is None:
            return None
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        return mod
    except (OSError, ImportError, SyntaxError):
        return None
    finally:
        sys.path.pop(0)


def task_tree(root, task_id):
    """Дерево задачи по правилу shipctl (treePath): ../<проект>-<id нижним
    регистром>. Дерево переживает парковку, и разговор задачи с ответом лежит
    в нём, а спрашивающий из основного чекаута оставляет его там же."""
    top = os.path.normpath(root.rstrip(os.sep))
    return os.path.join(os.path.dirname(top), os.path.basename(top) + "-" + task_id.lower())


def section_rows(root, section, needle=""):
    """ID задач корня, стоящих в названной секции доски, при непустом needle
    только строки с этой подстрокой. Доска читается напрямую, а не через
    `taskctl list`: сторожок работает и там, где бинари devkit сломаны.

    Имя секции сверяется началом заголовка: у Check в заголовке стоит пояснение
    («## Check (готово, ждёт проверки пользователем)»), и точное равенство
    прошло бы мимо всей секции молча."""
    try:
        text = Path(root, BOARD).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return []
    rows, here = [], False
    for ln in text.splitlines():
        if ln.startswith("## "):
            here = ln[3:].strip().startswith(section)
            continue
        if not here or not ln.startswith("|") or (needle and needle not in ln):
            continue
        cell = ln.split("|")[1].strip() if ln.count("|") > 1 else ""
        if cell and cell != "ID" and not set(cell) <= set("-: "):
            rows.append(cell)
    return rows


def parked_rows(root):
    """Задачи корня, припаркованные вопросом: список ID."""
    return section_rows(root, BLOCKED, PARKED)


def lying_answer(root, task_id, now, hook=None):
    """Ответ задаче, лежащий в её разговоре, и счёт пропущенных адресных строк.
    Ответом задаче считается только безадресная строка: адрес у реплики это
    адресат разговора, и строка «..., сессии <ID>: ...» написана живому окну, а
    не задаче (LLD DK-430, решение 2). Разбор адресата берётся у подхвата тем же
    импортом, каким берётся срок ожидания: вторая копия формата разъехалась бы с
    первой на первой же правке.

    Разговор ищется в двух деревьях, основном и дереве задачи: исполнитель
    спрашивает из своего дерева, диспетчер из основного. Свежий признак ожидания
    .ask отдаёт разговор инструменту ожидания, и будить рано: ответ заберёт сам
    ждущий заход, паркуется только вопрос, оставшийся без ответа."""
    hook = chat_hook() if hook is None else hook
    if hook is None:
        return None, 0
    until_now = time.mktime(now.timetuple())
    lying = []
    for base in (root, task_tree(root, task_id)):
        for sub, pattern in CHAT_DIRS:
            d = os.path.join(base, ".devkit", sub)
            until = hook.ask_stamp(os.path.join(d, CHAT_ASK % task_id))
            if until is not None and until > until_now:
                return None, 0
            lying.append(os.path.join(d, pattern % task_id))
    addressed = 0
    for src in lying:
        try:
            with open(src, encoding="utf-8", errors="replace") as f:
                lines = [l.strip() for l in f.read().split("\n") if l.strip()]
        except OSError:
            continue
        for line in lines:
            if not hook.addressee(line):
                return line, addressed
            addressed += 1
    return None, addressed


def devkit_bin(name, which=None):
    """Путь бинаря devkit для вызовов тика. launchd даёт сторожку системный
    PATH без каталога бинарей, поэтому за PATH стоят каталоги установки
    релиза, те же, что перебирает update."""
    which = shutil.which if which is None else which
    found = which(name)
    if found:
        return found
    import update
    for d in update.BIN_DIRS:
        cand = os.path.expanduser(os.path.join(d, name))
        if os.access(cand, os.X_OK):
            return cand
    return ""


def taskctl_bin(which=None):
    return devkit_bin("taskctl", which)


# Умолчание ключа hook у wake: подхват грузится самим тиком. Отдельная метка
# вместо None тут потому, что None это законное значение «подхват не
# загрузился», и стенд обязан уметь его передать.
LOAD_HOOK = object()


def progress_rows(root):
    """ID задач корня, стоящих в In progress."""
    return section_rows(root, IN_PROGRESS)


def session_alive(sid, now, home=None):
    """Идёт ли сессия sid прямо сейчас. Мера одна на дашборд и на сторожок:
    свежесть транскрипта, путь к которому лежит готовым полем в реестре чатов.
    Незнакомая сессия и пустое поле это «не идёт»: страховке нужен живой
    собеседник, а не запись о нём."""
    if not sid:
        return False
    home = default_home() if home is None else home
    try:
        text = home_path(home, SESSIONS_LOG).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    path = ""
    for ln in text.splitlines():
        f = ln.split()
        if len(f) < 3 or f[1] != "сессия":
            continue
        vals, key = {}, ""
        for tok in f[1:]:
            if tok in REG_KEYS and vals.get(key):
                key = tok
                continue
            if not key:
                key = tok
                continue
            vals[key] = (vals[key] + " " + tok).strip() if vals.get(key) else tok
        if vals.get("сессия") == sid:
            path = vals.get("транскрипт", "")
    if not path or path == "-":
        return False
    try:
        mod = os.path.getmtime(path)
    except OSError:
        return False
    return time.mktime(now.timetuple()) - mod < SESSION_LIVE


def stale_ask(root, task_id, now, hook):
    """Протухший признак ожидания задачи: путь, ждущая сессия и суть вопроса.
    Свежий признак значит, что заход ждёт ответа сам, и трогать его нечем.
    Ищется в обоих деревьях, основном и дереве задачи: инструмент пишет признак
    в чекаут, а брошенный ход бывает и в дереве задачи."""
    until_now = time.mktime(now.timetuple())
    for base in (root, task_tree(root, task_id)):
        for sub, _ in CHAT_DIRS:
            path = os.path.join(base, ".devkit", sub, CHAT_ASK % task_id)
            ask = hook.ask_fields(path)
            if ask is None:
                continue
            if ask["until"] > until_now:
                return None
            return {"path": path, "session": ask["session"],
                    "question": "; ".join(q for q in ask["questions"] if q)}
    return None


def park_stale(root, now, call=None, taskctl=None, hook=LOAD_HOOK, home=None):
    """Страховка ожидания: паркует строку, за которой остался протухший признак
    и не осталось живой сессии. Возврат это строки отчёта, как у пробуждения.

    Норма это парковка самим инструментом, страховка идёт тем же тиком, что и
    пробуждение, и повторный проход ничего не меняет: припаркованная строка из
    In progress уже ушла. Признак снимается вместе с парковкой, иначе следующий
    тик считал бы его брошенным заново."""
    call = subprocess.run if call is None else call
    bin = taskctl_bin() if taskctl is None else taskctl
    lines = []
    rows = progress_rows(root)
    if hook is LOAD_HOOK:
        hook = chat_hook() if rows else None
    if rows and hook is None:
        lines.append("корень %s: подхват реплики hooks/chat-in.py не загрузился, "
                     "признак ожидания разбирать нечем: брошенные вопросы стоят" % root)
        return lines
    for tid in rows:
        ask = stale_ask(root, tid, now, hook)
        if ask is None:
            continue
        if session_alive(ask["session"], now, home):
            lines.append("задача %s в %s: ожидание брошено, но сессия %s жива: "
                         "с вопросом разбирается сам заход" % (tid, root, ask["session"]))
            continue
        if not bin:
            lines.append("задача %s в %s: ожидание брошено, а бинаря taskctl нет ни в "
                         "PATH, ни в каталогах релиза: строка стоит в In progress" % (tid, root))
            continue
        reason = "вопрос: " + (ask["question"] or "заход спросил человека и не дождался ответа")
        try:
            p = call([bin, "-C", root, "move", tid, "blocked", "--reason", reason,
                      "-m", "docs(tasks): %s припаркована брошенным вопросом" % tid, "--push"],
                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        except OSError as e:
            lines.append("задача %s в %s: припарковать не вышло, %s" % (tid, root, e))
            continue
        if p.returncode != 0 and tid in progress_rows(root):
            lines.append("задача %s в %s: taskctl отказал с кодом %d: %s"
                         % (tid, root, p.returncode, (p.stdout or "").strip()))
            continue
        try:
            os.remove(ask["path"])
        except OSError:
            pass
        if p.returncode != 0:
            # Строка из In progress ушла, значит парковка отработала, а
            # споткнулся её хвост: доска не уехала в origin. Признак снимается
            # всё равно, иначе он лежал бы вечно, а неуехавшая доска называется
            # в отчёте: чинит её человек; следующий тик к ней не возвращается.
            lines.append("задача %s в %s припаркована страховкой, но доска не уехала в origin: %s"
                         % (tid, root, (p.stdout or "").strip()))
            continue
        lines.append("задача %s в %s припаркована страховкой: ход ожидания убит, живой сессии за строкой нет"
                     % (tid, root))
    return lines


# -- закрытие агентской задачи из Check ---------------------------------------

def close_agent(root, call=None, taskctl=None):
    """Закрывает строки Check, которые вправе закрыть автоматика. Возврат это
    строки отчёта, как у пробуждения и страховки.

    Задачу вида agent прогоняет и закрывает сам агент, но сессия кончается
    раньше закрытия чаще, чем доживает до него: слитая, выкаченная и
    прогнанная строка стоит в Check до следующей живой сессии, и человек видит
    вставший конвейер там, где делать ему нечего. Тик доводит такую строку до
    Done сам.

    Кого закрывать, отвечает `taskctl closable`, а не разбор доски тут:
    вид приёмки, форма файла задачи и рубеж непустой «Проверки» живут у
    taskctl, и вторая копия этих правил в питоне разошлась бы с первой на
    первой же правке. Без бинаря тик молчит: спросить некого, а закрывать
    вслепую нельзя.

    Закрытие идёт `taskctl -C <корень> close <ID> -m ... --push`, как парковка
    идёт своим move: за тиком никого нет, и правка доски, оставленная грязной
    в основном чекауте, отбила бы следующий merge предполётом. Громкого зова
    на закрытие нет намеренно: смысл тика в том, чтобы человека не дёргать.
    """
    call = subprocess.run if call is None else call
    bin = taskctl_bin() if taskctl is None else taskctl
    if not bin or not section_rows(root, CHECK):
        return []
    try:
        p = call([bin, "-C", root, "closable"], stdout=subprocess.PIPE,
                 stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return ["корень %s: спросить taskctl closable не вышло, %s" % (root, e)]
    if p.returncode != 0:
        return ["корень %s: taskctl closable отказал с кодом %d: %s"
                % (root, p.returncode, (p.stdout or "").strip())]
    lines = []
    for ln in (p.stdout or "").splitlines():
        tid = ln.strip()
        if not CLOSABLE_ID.match(tid):
            break
        try:
            r = call([bin, "-C", root, "close", tid,
                      "-m", "docs(tasks): %s закрыта тиком сторожка" % tid, "--push"],
                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        except OSError as e:
            lines.append("задача %s в %s: закрыть не вышло, %s" % (tid, root, e))
            continue
        if r.returncode != 0 and tid in section_rows(root, CHECK):
            lines.append("задача %s в %s: taskctl отказал с кодом %d: %s"
                         % (tid, root, r.returncode, (r.stdout or "").strip()))
            continue
        if r.returncode != 0:
            # Строка из Check ушла, значит закрытие отработало, а споткнулся
            # его хвост: доска не уехала в origin. Чинит её человек, следующий
            # тик к закрытой строке не возвращается.
            lines.append("задача %s в %s закрыта тиком, но доска не уехала в origin: %s"
                         % (tid, root, (r.stdout or "").strip()))
            continue
        lines.append("задача %s в %s закрыта тиком: вид agent, smoke прогнан, "
                     "раздел «Проверка» непуст" % (tid, root))
    return lines


def wake(root, now, call=None, taskctl=None, hook=LOAD_HOOK):
    """Будит припаркованные вопросом строки корня с лежащим ответом. Возврат
    это строки отчёта, по одной на будимость и итог на корень: тик молчит о
    корне только там, где парковок нет вовсе.

    Возврат строки идёт тем же `taskctl -C <корень> move <ID> in-progress`,
    каким её парковали: причина блока снимается им же, а строка становится
    кандидатом планировщика на общих основаниях. Move идёт с -m и --push:
    за будящим никого нет, и правка доски, оставленная грязной в основном
    чекауте, отбила бы следующий merge предполётом и подмелась бы чужим
    коммитом, поэтому она коммитится и пушится тут же, по правилу доски
    (ровно то, что делает shipctl своим commitBoard). Отказ taskctl не
    поднимает сторожок: строка стоит в Blocked, и следующий тик повторит.

    Не загрузившийся подхват останавливает пробуждение всего корня, и тик
    говорит об этом строкой отчёта: разбирать адресата своей копией формата
    нельзя, а будить не разбирая значит гнать в origin правку доски по чужой
    реплике."""
    call = subprocess.run if call is None else call
    bin = taskctl_bin() if taskctl is None else taskctl
    lines, woke = [], 0
    parked = parked_rows(root)
    if hook is LOAD_HOOK:
        hook = chat_hook() if parked else None
    if parked and hook is None:
        lines.append("корень %s: подхват реплики hooks/chat-in.py не загрузился, "
                     "разбирать адресата нечем: припаркованные строки стоят" % root)
        return lines
    for tid in parked:
        answer, addressed = lying_answer(root, tid, now, hook)
        if not answer:
            if addressed:
                lines.append("задача %s в %s: во входе лежат реплики с адресатом сессии (%d), "
                             "ответом задаче они не считаются: строка стоит в Blocked"
                             % (tid, root, addressed))
            continue
        if not bin:
            lines.append("задача %s в %s: ответ лежит, а бинаря taskctl нет ни в "
                         "PATH, ни в каталогах релиза: строка стоит в Blocked" % (tid, root))
            continue
        try:
            p = call([bin, "-C", root, "move", tid, "in-progress",
                      "-m", "docs(tasks): %s разбуждена ответом" % tid, "--push"],
                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        except OSError as e:
            lines.append("задача %s в %s: разбудить не вышло, %s" % (tid, root, e))
            continue
        if p.returncode != 0:
            lines.append("задача %s в %s: taskctl отказал с кодом %d: %s"
                         % (tid, root, p.returncode, (p.stdout or "").strip()))
            continue
        woke += 1
        lines.append("задача %s в %s разбужена: ответ в разговоре, строка вернулась в In progress" % (tid, root))
    if parked:
        lines.append("корень %s: припаркованных вопросом %d, разбужено %d"
                     % (os.path.basename(root.rstrip("/")), len(parked), woke))
    return lines


def quota_snap(call=None, agentctl=None):
    """Съём остатка подписок тем же тиком (DK-633): снимок квоты обязан свежеть
    и без живых сессий, иначе между заходами он стоит часами, а корректор
    вердикта слепнет ровно к началу следующей задачи. Снимает
    `agentctl quota refresh --all --if-stale`: перечень подписок, порог
    свежести и замок живут в agentctl, тик его только будит, а панель
    /usage и кабинет z.ai дёргаются не чаще протухания снимка. PATH
    собирается как у launchd-агента дашборда: системное умолчание launchd не
    знает ни tmux, ни claude, которыми снимается панель первой подписки.

    Возврат это отчёт тика целиком и строка для журнала сторожка либо None.
    В отчёт идёт разбор по харнесам как есть: живой случай 15:59 из DK-633
    разбирался вслепую ровно потому, что деталь резалась до счёта. В журнал
    идёт счёт исходов, и только когда что-то снялось, оставлено или отказало,
    а «всё свежо» капало бы туда каждые пять минут."""
    call = subprocess.run if call is None else call
    bin = devkit_bin("agentctl") if agentctl is None else agentctl
    if not bin:
        line = ("снимок квоты: бинаря agentctl нет ни в PATH, ни в каталогах "
                "релиза, снимки стареют до ручного refresh")
        return line, line
    import dashboard
    env = dict(os.environ)
    env["PATH"] = dashboard.agent_path(bin)
    try:
        p = call([bin, "quota", "refresh", "--all", "--if-stale"],
                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, env=env)
    except OSError as e:
        line = "снимок квоты: съём не запустился, %s" % e
        return line, line
    text = (p.stdout or "").strip()
    if p.returncode != 0:
        flat = " ".join(text.split())
        return ("снимок квоты: съём кончился кодом %d\n%s" % (p.returncode, text),
                "снимок квоты: съём кончился кодом %d: %s" % (p.returncode, flat[:500]))
    first = text.splitlines()[0] if text else ""
    note = None
    if re.search(r"(снято|оставлено) [1-9]", first):
        note = "снимок квоты: %s" % first
    return "снимок квоты: %s" % text, note


NO_DRAIN = "разлив не нужен"


def drain(root, call=None, shipctl=None):
    """Разлив поезда корня (LLD DK-306, решения 2 и 4): тик зовёт
    `shipctl -C <корень> ship --drain`, чтобы поезд, оставшийся без получателя
    после перезагрузки или падения сессий, не стоял до ручного ship. Разлив
    идемпотентен: пустой поезд, занятая очередь, сломанный прод и занятый чужим
    заходом конвейер выходят нулём со строкой «разлив не нужен», и такой исход
    значимым не считается. Возврат это строка отчёта и признак значимости:
    значимое (выкат или провал) идёт в журнал сторожка, остальное только в
    отчёт тика, иначе журнал тонул бы в пустом поезде каждые пять минут.

    Провал вызова не поднимает код тика и не глушит остальные корни:
    уведомление на провал деплоя шлёт сам shipctl через признак провала и
    taskctl fail, и повторять его из сторожка значило бы звонить дважды."""
    call = subprocess.run if call is None else call
    bin = devkit_bin("shipctl") if shipctl is None else shipctl
    name = os.path.basename(root.rstrip("/"))
    if not bin:
        return ("корень %s: бинаря shipctl нет ни в PATH, ни в каталогах релиза: "
                "поезд стоит до ручного ship" % name), True
    try:
        p = call([bin, "-C", root, "ship", "--drain"],
                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return ("корень %s: разлив не вышел, %s" % (name, e)), True
    text = " ".join((p.stdout or "").split())
    if p.returncode != 0:
        return ("корень %s: разлив упал с кодом %d: %s" % (name, p.returncode, text)), True
    return ("корень %s: %s" % (name, text)), NO_DRAIN not in (p.stdout or "")


def shout(title, body, root, call=None, task=None):
    """Громкий зов уведомителем. Зовётся он из корня проекта, как из оболочки
    goal-run: по рабочему дереву уведомитель собирает заголовок баннера и цель
    перехода по клику. Цель едет ключом `--task`: по полю события лента
    дашборда ведёт от вставшего цикла к строке цели (DK-323)."""
    call = subprocess.run if call is None else call
    argv = [sys.executable, str(NOTIFIER)]
    if task:
        argv += ["--task", task]
    try:
        p = call(argv + [title, body], cwd=root,
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return str(e)
    out = (p.stdout or "").strip()
    return out or "отправлено"


def resume_command(goal, root, devkit=None):
    """Команда продолжения цикла, готовая к запуску: путь до оболочки абсолютный,
    цель и корень названы. Сам сторожок цикл не поднимает (разбор в
    tools/devkitctl/README.md), поэтому в зове человеку остаётся ровно эта
    строка, а не поиск по доке."""
    devkit = DEVKIT if devkit is None else Path(devkit)
    return "python3 %s %s -C %s" % (
        devkit / "kit" / "skills" / "goal-loop" / "goal-run.py", goal, root)


def moved_at(entry, root, path):
    """Когда цикл двигался последний раз. Первый источник это журнал запусков
    проекта, второй строка гейта в самой записи: без `.devkit` журнала нет, а
    гейт витка есть всегда. Не разобрано ни то, ни другое, значит берётся время
    самой записи, иначе цель осталась бы без надзора."""
    marks = [m for m in (last_run_stamp(root), stamp_of(entry.get("seen"))) if m]
    if marks:
        return max(marks)
    try:
        return datetime.fromtimestamp(os.path.getmtime(path))
    except OSError:
        return None


def look(path, now, idle, call=None):
    """Что сторожок сделал с одной записью реестра: (позвали ли, строка отчёта).

    Тут же чинится сам реестр: цель, ушедшая с доски или из In progress, надзора
    больше не просит, и её запись снимается."""
    entry = read_entry(path)
    goal, root = entry.get("goal"), entry.get("root")
    if not goal or not root or not os.path.isdir(root):
        drop(path)
        return False, "запись %s снята: %s" % (
            path.name, "корня %s нет" % root if root else "в ней нет цели или корня")
    if board_present(root):
        section = board_section(root, goal)
        if section != IN_PROGRESS:
            drop(path)
            where = "она стоит в разделе «%s»" % section if section else "её нет на доске"
            return False, "цель %s в %s: %s, запись снята" % (goal, root, where)
    moved = moved_at(entry, root, path)
    if moved is None:
        return False, "цель %s в %s: движения не измерить, записи нет времени" % (goal, root)
    gap = (now - moved).total_seconds()
    if gap < idle:
        if entry.get("stopped"):
            entry.pop("stopped")
            write_entry(path, entry)
        return False, "цель %s в %s: движение %s назад, тихо" % (goal, root, say.human_age(gap))
    if entry.get("stopped"):
        return False, "цель %s в %s: простой %s, по этому стопу уже позвали в %s" % (
            goal, root, say.human_age(gap), entry["stopped"])
    title = "цель %s: цикл стоит %s" % (goal, say.human_age(gap))
    body = "движения в %s нет с %s при пороге %s; продолжить цикл: %s" % (
        os.path.basename(root.rstrip("/")), moved.strftime(STAMP), say.human_age(idle),
        resume_command(goal, root))
    said = shout(title, body, root, call, goal)
    entry["stopped"] = now.strftime(STAMP)
    write_entry(path, entry)
    return True, "цель %s в %s: простой %s при пороге %s, зову; %s" % (
        goal, root, say.human_age(gap), say.human_age(idle), said)


def drop(path):
    try:
        os.remove(path)
    except OSError:
        pass


def run_roots(home=None):
    """Корни задач из записей этапов. Реестр целей заводит только гейт бюджета
    внутри цикла цели, а `taskctl ask` зовут и по задаче, взятой напрямую: без
    второго источника страховка молчала бы ровно там, где вопрос задан вне
    цикла. Каждый вердикт pick и каждый вопрос заводят запись этапа, и поле
    «root» в ней это уже основной чекаут.

    Корень без доски пропускается: она читается напрямую, и её отсутствие
    значит, что запись пережила сам проект."""
    home = default_home() if home is None else home
    d = home_path(home, RUNS_DIR)
    if not d.is_dir():
        return []
    roots = []
    for path in sorted(d.glob("*.run")):
        root = read_entry(path).get("root", "")
        if root and root not in roots and os.path.isdir(root) and board_present(root):
            roots.append(root)
    return roots


def entries(home=None):
    home = default_home() if home is None else home
    d = home_path(home, GOALS_DIR)
    return sorted(d.glob("*.watch")) if d.is_dir() else []


def log_line(text, home=None):
    """Строка в журнал сторожка. Он же ответ на вопрос «а сторожок вообще
    работает»: по свежести последней строки это видит доктор."""
    home = default_home() if home is None else home
    path = home_path(home, LOG)
    line = "%s %s\n" % (datetime.now().strftime(STAMP), text)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.exists() and path.stat().st_size > LOG_LIMIT:
            tail = path.read_text(encoding="utf-8", errors="replace").splitlines(True)
            path.write_text("".join(tail[-LOG_KEEP:]), encoding="utf-8")
        with open(path, "a", encoding="utf-8") as f:
            f.write(line)
    except OSError:
        pass


def heartbeat(home=None):
    """Момент последнего прогона сторожка по его журналу, None если журнала нет."""
    home = default_home() if home is None else home
    try:
        lines = home_path(home, LOG).read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError:
        return None
    for ln in reversed(lines):
        st = stamp_of(ln.split(" ")[0])
        if st is not None:
            return st
    return None


def run(now=None, idle=None, home=None, out=None, call=None, taskctl=None, shipctl=None):
    """Обход реестра. Возврат 0 всё движется, 1 нашёлся вставший цикл."""
    now = datetime.now() if now is None else now
    home = default_home() if home is None else home
    idle = conf_idle(home) if idle is None else idle
    out = sys.stdout if out is None else out
    # Съём квоты идёт до обхода реестра и не зависит от него: снимок лежит на
    # уровне машины, и свежеть он обязан и там, где целей под надзором нет.
    qreport, qnote = quota_snap(call)
    out.write(qreport + "\n")
    if qnote:
        log_line(qnote, home)
    found, watched = 0, 0
    swept = set()
    for path in entries(home):
        watched += 1
        root = read_entry(path).get("root")
        called, line = look(path, now, idle, call)
        found += 1 if called else 0
        out.write(line + "\n")
        if called:
            log_line(line, home)
        # Пробуждение идёт по корню, а не по записи: целей на одной доске
        # бывает несколько, и разговор припаркованной задачи один на всех.
        if root and root not in swept and os.path.isdir(root):
            swept.add(root)
            for wline in wake(root, now, call, taskctl):
                out.write(wline + "\n")
                log_line(wline, home)
            for pline in park_stale(root, now, call, taskctl, home=home):
                out.write(pline + "\n")
                log_line(pline, home)
            for cline in close_agent(root, call, taskctl):
                out.write(cline + "\n")
                log_line(cline, home)
    # Разговор задачи живёт и вне цикла цели, поэтому корни задач обходятся
    # тем же порядком: пробуждение ответом и страховка брошенного ожидания не
    # спрашивают, ведут ли задачу целью.
    for root in run_roots(home):
        if root in swept:
            continue
        swept.add(root)
        for line in (wake(root, now, call, taskctl)
                     + park_stale(root, now, call, taskctl, home=home)
                     + close_agent(root, call, taskctl)):
            out.write(line + "\n")
            log_line(line, home)
    # Разлив идёт тем же множеством корней отдельным проходом: событие
    # «очередь освободилась» некому доставить после перезагрузки или падения
    # сессий, и без сторожка поезд стоял бы до ручного ship (LLD DK-306,
    # решение 4). Корень без доски пропускается: разливать там нечего, и
    # строка об отказе только плодила бы шум.
    for root in sorted(swept):
        if not board_present(root):
            continue
        line, notable = drain(root, call, shipctl)
        out.write(line + "\n")
        if notable:
            log_line(line, home)
    log_line("целей под надзором %d, вставших %d" % (watched, found), home)
    if not watched:
        out.write("целей под надзором нет: реестр %s пуст\n" % home_path(home, GOALS_DIR))
    return 1 if found else 0


# -- носитель расписания -----------------------------------------------------

def plist_text(python, script, log):
    """Тело launchd-агента. Интервал тут короче порога простоя намеренно: порог
    решает, когда звать, а агент только просыпается достаточно часто, чтобы
    любой порог сработал вовремя."""
    return "\n".join([
        '<?xml version="1.0" encoding="UTF-8"?>',
        '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" '
        '"http://www.apple.com/DTDs/PropertyList-1.0.dtd">',
        '<plist version="1.0">',
        '<dict>',
        '  <key>Label</key><string>%s</string>' % LABEL,
        '  <key>ProgramArguments</key>',
        '  <array>',
        '    <string>%s</string>' % python,
        '    <string>%s</string>' % script,
        '    <string>watch</string>',
        '  </array>',
        '  <key>StartInterval</key><integer>%d</integer>' % EVERY,
        '  <key>RunAtLoad</key><true/>',
        '  <key>StandardOutPath</key><string>%s</string>' % log,
        '  <key>StandardErrorPath</key><string>%s</string>' % log,
        '</dict>',
        '</plist>',
        '',
    ])


def script_of(text):
    """Путь сторожка, прописанный в готовом plist: по нему видно, на какой
    чекаут смотрит агент."""
    for ln in text.splitlines():
        ln = ln.strip()
        if ln.startswith("<string>") and ln.endswith("devkitctl.py</string>"):
            return ln[len("<string>"):-len("</string>")]
    return ""


def launchctl(args, call=None):
    """(код возврата, вывод) launchctl; код 127 значит, что позвать не вышло."""
    call = subprocess.run if call is None else call
    try:
        p = call(["launchctl"] + list(args), stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        return 127, str(e)
    return p.returncode, (p.stdout or "").strip()


def loaded(call=None):
    return launchctl(["list", LABEL], call)[0] == 0


def reload_agent(plist, call=None):
    """Перевзвести агента: снять прежнего и поднять заново. Домен gui/<uid> это
    сессия пользователя, в ней агент и живёт."""
    target = "gui/%d" % os.getuid()
    launchctl(["bootout", "%s/%s" % (target, LABEL)], call)
    code, out = launchctl(["bootstrap", target, str(plist)], call)
    if code == 0:
        return ""
    # Старый launchctl bootstrap не знает, а load умеет то же самое.
    code, out = launchctl(["load", "-w", str(plist)], call)
    return "" if code == 0 else out


def check(fix=False, main=None, from_main=True, home=None, platform=None, call=None):
    """Носитель сторожка в машинном контуре доктора: агент стоит, смотрит на
    основной чекаут и отрабатывает по расписанию."""
    home = default_home() if home is None else home
    platform = sys.platform if platform is None else platform
    main = DEVKIT if main is None else Path(main)
    script = main / "tools" / "devkitctl" / "devkitctl.py"
    watched = entries(home)
    if platform != "darwin":
        if watched:
            return ["носителя сторожка цикла цели на платформе %s пока нет, а целей под "
                    "надзором %d: звать python3 %s watch по своему расписанию (cron, systemd)"
                    % (platform, len(watched), script)], []
        return [], []
    plist = home_path(home, PLIST)
    log = home_path(home, LOG)
    want = plist_text(sys.executable, script, log)
    have = plist.read_text(encoding="utf-8", errors="replace") if plist.exists() else ""
    if have != want:
        why = ("сторожок цикла цели не подключён" if not have else
               "launchd-агент сторожка показывает на %s" % (script_of(have) or "невесть что"))
        if not fix:
            return ["%s: вставший цикл цели останется незамеченным, поднять: devkitctl doctor --fix"
                    % why], []
        if not from_main:
            return ["%s, а devkit тут выложен worktree ветки задачи: чинить из основного чекаута %s"
                    % (why, main)], []
        plist.parent.mkdir(parents=True, exist_ok=True)
        plist.write_text(want, encoding="utf-8")
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента сторожка %s: %s" % (plist, err)], []
        return [], ["сторожок цикла цели подключён launchd-агентом %s (раз в %s)"
                    % (LABEL, say.human_age(EVERY))]
    if not loaded(call):
        if not fix:
            return ["launchd-агент сторожка %s положен, но не поднят: вставший цикл цели "
                    "останется незамеченным, поднять: devkitctl doctor --fix" % LABEL], []
        err = reload_agent(plist, call)
        if err:
            return ["launchd не взял агента сторожка %s: %s" % (plist, err)], []
        return [], ["сторожок цикла цели поднят launchd-агентом %s" % LABEL]
    last = heartbeat(home)
    if last is None:
        return ["сторожок цикла цели ни разу не отработал: журнала %s нет; проверить руками: "
                "python3 %s watch" % (log, script)], []
    age = (datetime.now() - last).total_seconds()
    if age > HEARTBEAT_MISS * EVERY:
        return ["сторожок цикла цели не отрабатывал %s при расписании раз в %s: агент %s поднят, "
                "но не будится; проверить руками: python3 %s watch"
                % (say.human_age(age), say.human_age(EVERY), LABEL, script)], []
    return [], []


def main(argv):
    idle = None
    i = 0
    while i < len(argv):
        if argv[i] == "--idle":
            if i + 1 >= len(argv):
                sys.stderr.write("флагу --idle нужно значение в минутах\n")
                return 2
            try:
                idle = int(argv[i + 1]) * 60
            except ValueError:
                sys.stderr.write("порог --idle задаётся целым числом минут\n")
                return 2
            i += 2
            continue
        if argv[i] in ("-h", "--help"):
            sys.stdout.write(__doc__)
            return 0
        sys.stderr.write("неизвестный флаг %s\n" % argv[i])
        return 2
    return run(idle=idle)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
