#!/usr/bin/env python3
"""Уведомитель devkit: сказать наружу, что сессия ждёт действия или что
субагент отработал. Параллельных сессий на машине несколько, и та, что упёрлась
в запрос разрешения, иначе стоит, пока на её окно не посмотрят.

Поводы двух сортов. Громкий зовёт подключиться (сессия упёрлась в вопрос,
закончила ход) и приходит со звуком и своим баннером. Фоновый рассказывает о
ходе работы (субагент отработал), приходит молча и схлопывается по сессии,
чтобы не копить ленту.

Режимы:
  notify.py [--quiet] [--reason <повод>] [--task <ID>] [--project <имя>]
            <заголовок> [<текст>]
                                    позвать уведомитель из чего угодно: скрипт
                                    выката, другой харнес, проверка расписания;
                                    --quiet понижает повод до фонового,
                                    --reason называет повод словом для журнала
                                    (goal_stop, wait_human, task_check), по
                                    которому лента дашборда отличает стоп цикла
                                    от завершённой задачи, а --task и --project
                                    называют задачу и проект события, по
                                    которым лента ведёт к строке доски
  notify.py --hook [протокол]       хук харнеса: событие читается со stdin,
                                    разбирается по имени протокола таблицей
                                    hookio.py (голый --hook это claude-code), а
                                    заголовок и тело собираются из разобранного.
                                    Оси notify (по поводу), turn-done,
                                    subagent-done и prompt-submit (та только
                                    снимает отметку ожидания), остальное молча
                                    пропускается
  notify.py --self-test             послать пробное уведомление и напечатать,
                                    чем именно послано

Переменные окружения:
  DEVKIT_NOTIFY_OFF=1        не слать ничего (headless-прогоны, уведомлять некого)
  DEVKIT_RUN_DEPTH=...       хуки молчат: это сессия подпроцесса делегирования,
                             её никто не ждёт у окна, а звонок на каждый её
                             конец хода глушит настоящий
  DEVKIT_NOTIFY_BACKEND=...  перебить выбор бэкенда: имя (terminal-notifier,
                             osascript, notify-send) либо своя команда, её зовут
                             как «команда заголовок тело»
  DEVKIT_NOTIFY_OPEN=...     своя цель перехода по клику, `{cwd}` подставляется
                             рабочим деревом сессии; пустое значение гасит клик
  DEVKIT_NOTIFY_FOCUS=off    не смотреть, какое окно впереди: конец хода тогда
                             зовёт всегда

Аргументный вызов и хук-путь из проекта во временной директории (TMPDIR,
/tmp, /var/folders) ничего не шлют: такой корень это песочница вроде
синтетической доски из обкатки сценария или вложенной headless-сессии из
scratchpad, и живой баннер про неё ложный. Пропуск виден строкой в журнале,
а у аргументного вызова и в stdout, откуда её доносит до пользователя
зовущая утилита.

Журнал последних отправок лежит в ~/.devkit/notify.log: время, сессия, повод,
уровень, бэкенд, цель перехода, задача, проект, код возврата и хвостом текст
баннера заголовком и телом в ёлочках. Жалоба «уведомления не приходят»
разбирается по нему, как и «важное не отличается от фонового»; текстом хвоста
живёт лента дашборда, которой строка без слов ничего не говорит.

Задача и проект стоят в строке своими полями, а не угадываются из текста
баннера (DK-323): по ним лента дашборда ведёт от события к строке доски и к
журналу агента, а «Поднять виток» бьёт в тот проект, где событие случилось.
В хук-режиме поля собираются из рабочего дерева (у дерева задачи имя собрано
как «проект-ID», у обычного чекаута ID лежит на ветке), в аргументном их
называют ключи `--task` и `--project`, а без ключей они собираются так же по
рабочей директории. Задачи у события может честно не быть (самопроверка,
авария контура), и тогда в поле стоит прочерк.
"""
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.parse

import hookio

WINDOW = 30          # окно троттлинга: один повод одной сессии не чаще, секунды
# Признак сессии, поднятой подпроцессом делегирования: её ставит agentctl run
# (docs/lld/DK-033-universal-kit.md, «Делегирование»), и хуки такой сессии
# молчат. Окна редактора на второй подписке правило не касается, там переменной
# нет, а звать к такому окну надо.
RUN_DEPTH = "DEVKIT_RUN_DEPTH"
TRANSCRIPT_SCAN = 20 # сколько записей транскрипта смотрим ради cwd сессии
STALE = 24 * 3600    # брошенное состояние троттлинга старше суток убирается
BODY_LIMIT = 200
LOG_LIMIT = 100 * 1024
LOG_KEEP = 500

# Уровень повода. Он же слово журнала: жалоба «важное не отличается от
# фонового» разбирается по строке, а не на глаз.
LOUD = "громкий"
QUIET = "фоновый"

# Поводы события Notification, на которые шлём, и подпись в заголовке. Остальные
# (auth_success, elicitation_complete, elicitation_response) пользователя ни к
# чему не зовут и молчат. Отбор дублируется матчерами в settings.json, но
# держится и тут: хук зовут и без матчера.
NOTIFICATION_REASONS = {
    "permission_prompt": "нужно разрешение",
    "agent_needs_input": "нужен ответ",
    "elicitation_dialog": "диалог MCP",
    "idle_prompt": "ждёт ввода",
}
SUBAGENT_REASON = "субагент отработал"
TURN_DONE = "turn_done"
TURN_REASON = "ход закончен"
# Звать ли о конце хода, решает фокус: смотришь на окно этой сессии значит
# молчим, всё остальное значит зовём. Куда смотрит человек, System Events
# отвечает заголовком переднего окна, а имя рабочего дерева стоит там хвостом.
FOCUS_TOOL = "osascript"
FOCUS_SCRIPT = ('tell application "System Events" to tell '
                '(first application process whose frontmost is true) '
                'to get name of front window')
# Опрос стоит около 180 мс, но упереться он может и в диалог разрешения на
# управление компьютером: висеть на нём хук не должен.
FOCUS_TIMEOUT = 5
FOCUS_SESSION = "окно сессии"
FOCUS_OTHER = "чужое окно"
FOCUS_UNKNOWN = "не определился"
# Конец хода и idle_prompt это один и тот же повод «сессия ждёт тебя», поэтому
# троттлится он общим ключом. Окно шире минуты: idle_prompt харнес присылает
# примерно через минуту после конца хода, и своим окном он продублировал бы
# баннер.
WAIT_KEY = "session_waiting"
WAIT_REASONS = ("idle_prompt", TURN_DONE)
WAIT_WINDOW = 180

# Аргументный вызов из песочницы (mktemp -d, синтетическая доска обкатки
# сценария) молчит: баннер собрался бы про репозиторий, которого через минуту
# нет, а ложный сигнал дороже пропущенного. Признак песочницы это корень
# проекта под временной директорией: TMPDIR плюс её обычные места на случай,
# когда переменная перебита; варианты с /private стоят своими строками, чтобы
# не зависеть от того, разрешит ли realpath симлинки macOS на этой машине.
TMP_ROOTS = ("/tmp", "/private/tmp", "/var/folders", "/private/var/folders")

# Чего ждёт ключ аргументного вызова: слово идёт в отказ, чтобы «ключ без
# значения» читался прямо в терминале, а не сверялся с докой.
FLAG_WORD = {"--reason": "повод", "--task": "задачу", "--project": "проект"}

# Куда переключает клик по баннеру. Умеет это только terminal-notifier: она шлёт
# от своего имени, а display notification постит от имени Script Editor, и клик
# по такому баннеру открывает Finder с папкой скриптов.
CLICK_BACKENDS = ("terminal-notifier",)
VSCODE_URL = "vscode://file"
# Без этого параметра редактор открывает дерево в активном окне, а не в
# отдельном: у сессии из worktree своего окна обычно нет, и под замену уходит
# то окно, в котором сейчас работают, вместе со всеми его агентами. С
# параметром уже открытое дерево по-прежнему поднимается, второго окна на него
# не заводится.
VSCODE_NEW_WINDOW = "?windowId=_blank"
VSCODE_BUNDLE = "com.microsoft.VSCode"
TERMINAL_BUNDLES = {
    "Apple_Terminal": "com.apple.Terminal",
    "iTerm.app": "com.googlecode.iterm2",
}


def short(text, limit=BODY_LIMIT):
    # Тело уведомления это одна строка: у баннера две строки места, а
    # last_assistant_message субагента бывает на экран.
    if not isinstance(text, str):
        # Поле события пришло числом или объектом. Уведомление из-за этого не
        # теряется: повод известен, а тело собирается из того, что дали.
        text = "" if text is None else str(text)
    line = ""
    for raw in text.splitlines():
        if raw.strip():
            line = " ".join(raw.split())
            break
    if len(line) > limit:
        line = line[:limit - 3] + "..."
    return line


def tree_name(path):
    return os.path.basename((path if isinstance(path, str) else "").rstrip("/"))


BRANCH_HEAD_PREFIX = "ref: refs/heads/"
BRANCH_NOT_A_TASK = ("main", "master")


def tree_branch(tree):
    """Ветка репозитория в `tree` по файлу HEAD, пустая строка если ветки нет,
    HEAD не смотрит на ветку (detached) или дерево не репозиторий. Живой `git`
    тут не зовётся: хук стоит на каждом конце хода, и лишний подпроцесс ни к
    чему. `.git` обычно каталог, а у linked worktree это файл со строкой
    `gitdir: <путь>`, ведущей на каталог с собственным HEAD."""
    if not isinstance(tree, str) or not tree:
        return ""
    git = os.path.join(tree, ".git")
    try:
        if os.path.isfile(git):
            with open(git, encoding="utf-8") as f:
                line = f.readline().strip()
            if not line.startswith("gitdir:"):
                return ""
            git = line[len("gitdir:"):].strip()
            if not os.path.isabs(git):
                git = os.path.join(tree, git)
        with open(os.path.join(git, "HEAD"), encoding="utf-8") as f:
            ref = f.readline().strip()
    except OSError:
        return ""
    return ref[len(BRANCH_HEAD_PREFIX):] if ref.startswith(BRANCH_HEAD_PREFIX) else ""


def task_label(tree):
    """Метка задачи с ветки `tree`, пустая строка если ветки нет или это
    `main`/`master`: их веткой задачи не считаем."""
    branch = tree_branch(tree)
    return branch if branch and branch not in BRANCH_NOT_A_TASK else ""


# ID доски: буква, дальше буквы с цифрами, дефис и номер (DK-323, XR-001).
# Регистр свободный: на ветке и в имени дерева ID лежит строчными, а в доске и
# в ленте он ходит заглавными.
TASK_ID_RE = re.compile(r"^[A-Za-z][A-Za-z0-9]*-[0-9]+$")
# Имя дерева задачи собрано как «проект-ID» (`shipctl start`), и из него
# читаются оба поля сразу: и задача, и проект, чьё это дерево.
TREE_TASK_RE = re.compile(r"^(?P<home>.+)-(?P<task>[A-Za-z][A-Za-z0-9]*-[0-9]+)$")


def task_id(label):
    """Метка, приведённая к виду ID доски, пустая строка если это не ID.
    Веткой задачи бывает и `glm`, и `feature/x`, и такое событие честно едет
    без задачи, а не с выдуманной."""
    label = label.strip() if isinstance(label, str) else ""
    return label.upper() if TASK_ID_RE.match(label) else ""


def event_target(cwd, root=None):
    """Задача и проект события парой. Дерево задачи названо «проект-ID», и
    оттуда читаются оба поля; у обычного чекаута задача лежит на ветке, а
    проектом зовётся само дерево. Ничего не нашлось значит пустая строка:
    поле с прочерком честнее угаданного."""
    name, home = tree_name(cwd), tree_name(root)
    task, project = "", ""
    for label in (name, home):
        m = TREE_TASK_RE.match(label) if label else None
        if m:
            task, project = m.group("task").upper(), m.group("home")
            break
    if not project:
        project = home or name
    if not task:
        task = task_id(task_label(root or cwd))
    return task, project


def session_label(cwd, root=None):
    # Какая из сессий позвала, видно по имени рабочего дерева: у worktree задачи
    # в имени лежит её ID. Без этого при пяти агентах баннер бесполезен.
    # Работал субагент в дереве задачи, значит в заголовке стоит и окно, где его
    # искать, и сама задача: «it-road-course (irc-75)».
    name, home = tree_name(cwd), tree_name(root)
    if not home or home == name:
        # Имя дерева задачу не назвало (это либо основной чекаут, либо дерево
        # без пары root/cwd вовсе): вместо неё пробуем ветку того же дерева,
        # сессия в основном чекауте обычно на ней и работает.
        label = name or "сессия"
        task = task_label(root or cwd)
        return "%s (%s)" % (label, task) if task else label
    if not name:
        return home
    task = name[len(home) + 1:] if name.startswith(home + "-") else name
    return "%s (%s)" % (home, task)


def session_tree(sess, read=None):
    """Дерево, на котором стоит окно позвавшей сессии.

    У субагента в worktree задачи свой `cwd`, и цель, собранная из него, ведёт
    мимо: своего окна у такого дерева нет, и клик открывает лишнее окно вместо
    того, где идёт работа. Само дерево сессии лежит в её транскрипте: первые
    записи там служебные, а дальше идёт `cwd` самой сессии."""
    read = transcript_cwd if read is None else read
    return read(sess.transcript) or sess.cwd


def transcript_cwd(path):
    """`cwd` из первых записей транскрипта, пустая строка если не нашёлся."""
    if not isinstance(path, str) or not path:
        return ""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for _ in range(TRANSCRIPT_SCAN):
                line = f.readline()
                if not line:
                    break
                try:
                    record = json.loads(line)
                except ValueError:
                    continue
                cwd = record.get("cwd") if isinstance(record, dict) else None
                if isinstance(cwd, str) and cwd:
                    return cwd
    except OSError:
        pass
    return ""


def parse_event(sess, root=None):
    """Разобранное событие сессии в (ключ повода, заголовок, тело, уровень).
    None значит не шлём."""
    if sess is None:
        return None
    level = LOUD
    if sess.kind == hookio.NOTIFY:
        key = sess.reason
        label = NOTIFICATION_REASONS.get(key)
        if not label:
            return None
        body = short(sess.message)
    elif sess.kind == hookio.TURN_DONE:
        # Конец хода главной сессии. Текст события это последняя реплика
        # (hookio кладёт её в message из last_assistant_message), тело собираем
        # так же, как у субагента; пустая реплика оставляет прежний вид баннера.
        key, label, body = TURN_DONE, TURN_REASON, short(sess.message)
    elif sess.kind == hookio.SUBAGENT_DONE:
        level = QUIET
        # Обычный субагент приходит сюда, а не поводом agent_completed события
        # Notification: тот повод про фоновые сессии, не про субагентов.
        key = "subagent_stop"
        label = SUBAGENT_REASON
        # Роль субагента идёт в тело уже после того, как от ответа осталась
        # первая строка, иначе первой строкой стала бы сама роль.
        body = short(sess.message)
        if sess.agent:
            body = short("%s: %s" % (sess.agent, body) if body else sess.agent)
    else:
        return None
    return key, "%s: %s" % (session_label(sess.cwd, root), label), body, level


def in_vscode(env):
    # У сессии из расширения VS Code своего терминала нет, поэтому нет и
    # TERM_PROGRAM; зато видно сам редактор, который её поднял.
    return bool(env.get("TERM_PROGRAM") == "vscode" or env.get("VSCODE_PID")
                or env.get("CLAUDE_CODE_ENTRYPOINT") == "claude-vscode")


def click_target(env=None, cwd=None):
    """Куда переключить по клику: пара флага и значения для terminal-notifier.
    None значит переключать некуда, и уведомление уходит без цели."""
    env = os.environ if env is None else env
    cwd = cwd if isinstance(cwd, str) else ""
    template = env.get("DEVKIT_NOTIFY_OPEN")
    if template is not None:
        template = template.strip()
        if not template:
            return None
        return "-open", template.replace("{cwd}", urllib.parse.quote(cwd))
    if in_vscode(env):
        # Ссылка ведёт в окно с этим рабочим деревом, а у worktree задачи в
        # имени лежит её ID, так что клик попадает в позвавшую сессию, а не в
        # первое попавшееся окно. Уже открытое окно поднимается, второго
        # VS Code не заводит.
        if cwd:
            return "-open", VSCODE_URL + urllib.parse.quote(cwd) + VSCODE_NEW_WINDOW
        return "-activate", VSCODE_BUNDLE
    bundle = TERMINAL_BUNDLES.get(env.get("TERM_PROGRAM") or "")
    # Окно терминала по рабочему дереву не найти, поднимаем сам терминал.
    return ("-activate", bundle) if bundle else None


def supports_click(backend):
    return os.path.basename(backend or "") in CLICK_BACKENDS


def pick_backend(env=None, platform=None, which=None):
    """Чем слать на этой машине. None значит нечем."""
    env = os.environ if env is None else env
    platform = sys.platform if platform is None else platform
    which = shutil.which if which is None else which
    override = (env.get("DEVKIT_NOTIFY_BACKEND") or "").strip()
    if override:
        return override
    if platform == "darwin":
        # terminal-notifier идёт первой ради перехода по клику; её нет, значит
        # остаётся osascript, и баннер просто не кликается.
        for name in ("terminal-notifier", "osascript"):
            if which(name):
                return name
        return None
    if platform.startswith("linux"):
        return "notify-send" if which("notify-send") else None
    # Windows и WSL: место под свой бэкенд (powershell-тост, wsl-notify-send),
    # пока нечем.
    return None


def group_id(session):
    # Схлопывание идёт по сессии, а не по поводу: лента копится именно из
    # фоновых баннеров одной сессии, и новый должен вытеснять её же прошлый.
    session = str(session or "").strip()
    return "devkit-%s" % session if session and session != "-" else "devkit"


def backend_argv(backend, title, body, target=None, level=LOUD, session=None):
    """Как уровень выглядит на баннере, решает бэкенд: со звуком и своей
    строкой громкий, молча и схлопываясь по сессии фоновый."""
    name = os.path.basename(backend)
    if name == "terminal-notifier":
        # Пустое тело она показывает пустой строкой, поэтому в теле хотя бы
        # заголовок: баннер без второй строки выглядит обрезанным.
        argv = [backend, "-title", title, "-message", body or title]
        argv += ["-sound", "default"] if level == LOUD else ["-group", group_id(session)]
        return argv + list(target) if target else argv
    if name == "osascript":
        # Группировки у display notification нет вовсе, остаётся звук.
        sound = ' sound name "default"' if level == LOUD else ""
        script = 'display notification "%s" with title "%s"%s' % (
            applescript_quote(body), applescript_quote(title), sound)
        return [backend, "-e", script]
    if name == "notify-send":
        # Схлопывание тут держит подсказка synchronous: баннер с той же меткой
        # заменяет предыдущий, а не ложится под него.
        if level == LOUD:
            return [backend, "-u", "critical", title, body]
        return [backend, "-u", "low",
                "-h", "string:x-canonical-private-synchronous:%s" % group_id(session),
                title, body]
    # Своя команда зовётся как «команда заголовок тело»: ни уровня, ни цели
    # перехода она не понимает, и уведомление уходит без них.
    return [backend, title, body]


def applescript_quote(text):
    return text.replace("\\", "\\\\").replace('"', '\\"')


def send(backend, title, body, target=None, level=LOUD, session=None):
    """Код возврата бэкенда, None если запустить не удалось."""
    try:
        p = subprocess.run(backend_argv(backend, title, body, target, level, session),
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except OSError:
        return None
    return p.returncode


def state_dir():
    return os.path.join(os.path.expanduser("~"), ".devkit", "notify")


def sweep(path, now):
    # Сессии кончаются, их файлы состояния остаются; брошенное старше суток
    # убирается тем же проходом, отдельной уборки заводить незачем.
    try:
        names = os.listdir(path)
    except OSError:
        return
    for name in names:
        f = os.path.join(path, name)
        try:
            if now - os.path.getmtime(f) > STALE:
                os.remove(f)
        except OSError:
            pass


def throttle(key):
    """Ключ и окно троттлинга повода. Конец хода и idle_prompt считаются одним
    поводом «сессия ждёт тебя»: иначе пришедший следом idle_prompt повторил бы
    баннер конца хода."""
    return (WAIT_KEY, WAIT_WINDOW) if key in WAIT_REASONS else (key, WINDOW)


def stamp(mark, now):
    try:
        os.makedirs(os.path.dirname(mark), exist_ok=True)
        with open(mark, "w", encoding="utf-8") as f:
            f.write("%.3f" % now)
        os.utime(mark, (now, now))
    except OSError:
        pass


def read_stamp(mark):
    try:
        with open(mark, encoding="utf-8") as f:
            return float(f.read().strip())
    except (OSError, ValueError):
        return None


def throttle_mark(session, key, path=None):
    path = state_dir() if path is None else path
    return os.path.join(path, hashlib.sha1(
        ("%s|%s" % (session, key)).encode("utf-8")).hexdigest()[:16])


def clear_wait(session, path=None):
    """Забыть, что этой сессии уже говорили «ждёт тебя». Зовётся на вводе
    пользователя: он к сессии вернулся, и следующий конец хода это новый повод
    позвать, а не повтор прошлого."""
    try:
        os.remove(throttle_mark(session, WAIT_KEY, path))
    except OSError:
        pass


def allow(session, key, now=None, path=None):
    """Можно ли слать этот повод этой сессии: прошлая отправка старше окна.
    Разрешив, отметку сдвигает."""
    now = time.time() if now is None else now
    path = state_dir() if path is None else path
    key, window = throttle(key)
    mark = throttle_mark(session, key, path)
    last = read_stamp(mark)
    if last is not None and 0 <= now - last < window:
        return False
    stamp(mark, now)
    sweep(path, now)
    return True


def front_window(which=None, run=None):
    """Заголовок переднего окна системы, пустая строка значит не спросили.

    Живой опрос держится за этой границей: тестам не нужны ни macOS, ни
    выданное разрешение на управление компьютером. Пусто отвечают все отказы
    сразу (не macOS и `osascript` не нашёлся, разрешения нет, окна у переднего
    приложения нет вовсе), и разбирать их порознь незачем: зовём мы в каждом."""
    which = shutil.which if which is None else which
    run = subprocess.run if run is None else run
    tool = which(FOCUS_TOOL)
    if not tool:
        return ""
    try:
        p = run([tool, "-e", FOCUS_SCRIPT], stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL, timeout=FOCUS_TIMEOUT)
    except (OSError, subprocess.SubprocessError):
        return ""
    if p.returncode != 0:
        return ""
    out = p.stdout
    if isinstance(out, bytes):
        out = out.decode("utf-8", "replace")
    return (out or "").strip()


def window_is_session(title, tree):
    """Переднее окно стоит на рабочем дереве сессии: у VS Code имя дерева идёт
    хвостом заголовка после разделителя. Хвост берётся целым словом, иначе
    `devkit` сошёл бы за `devkit-dk-064`, а это разные деревья и разные окна."""
    name = tree_name(tree)
    title = title.strip() if isinstance(title, str) else ""
    if not name or not title or not title.endswith(name):
        return False
    head = title[:-len(name)]
    return not head or not (head[-1].isalnum() or head[-1] in "-_.")


def focus_state(tree, env=None, ask=None):
    """Куда смотрит человек: FOCUS_SESSION значит на окно этой сессии, и звать
    его некуда; FOCUS_OTHER значит куда-то ещё; FOCUS_UNKNOWN значит спросить
    не удалось, и тогда зовём тоже. Выключенная проверка это то же самое, что
    чужое окно."""
    env = os.environ if env is None else env
    if (env.get("DEVKIT_NOTIFY_FOCUS") or "").strip().lower() == "off":
        return FOCUS_OTHER
    # Дерева сессии нет, сравнивать заголовок не с чем: опрос тут только тратил
    # бы свои 180 мс.
    if not tree_name(tree):
        return FOCUS_UNKNOWN
    ask = front_window if ask is None else ask
    title = ask()
    if not title:
        return FOCUS_UNKNOWN
    return FOCUS_SESSION if window_is_session(title, tree) else FOCUS_OTHER


def sandbox_reason(cwd, env=None):
    """Причина молчать про этот корень, None если корень настоящий и слать
    можно. Сравнение идёт по realpath с обеих сторон: на macOS /tmp и
    /var/folders это симлинки в /private, а TMPDIR приходит то так, то так."""
    env = os.environ if env is None else env
    if not isinstance(cwd, str) or not cwd:
        return None
    real = os.path.realpath(cwd)
    for root in ((env.get("TMPDIR") or "").strip(),) + TMP_ROOTS:
        root = os.path.realpath(root).rstrip("/") if root else ""
        if not root:
            continue
        if real == root or real.startswith(root + os.sep):
            return "корень %s лежит под %s" % (cwd, root)
    return None


def one_line(text):
    """Текст одной строкой без ёлочек: ими в журнале обрамлён сам текст, и
    своя ёлочка внутри развалила бы разбор хвоста."""
    if not isinstance(text, str):
        return ""
    return " ".join(text.split()).replace("«", "<").replace("»", ">")


def log_text(title, body):
    """Хвост строки журнала с текстом баннера. Пусто, когда текста нет: строка
    тогда выглядит как раньше, и разбор хвоста ищет его по метке «текст»."""
    title, body = one_line(title), one_line(body)
    if not title and not body:
        return ""
    return " текст «%s» «%s»" % (title, body)


def log(session, key, backend, result, target=None, level=None,
        title=None, body=None, task=None, project=None):
    d = os.path.join(os.path.expanduser("~"), ".devkit")
    path = os.path.join(d, "notify.log")
    line = ("%s сессия %s повод %s уровень %s бэкенд %s цель %s "
            "задача %s проект %s %s%s\n") % (
        time.strftime("%Y-%m-%dT%H:%M:%S"), session or "-", key or "-",
        level or "-", backend or "-", target[1] if target else "-",
        task or "-", project or "-", result, log_text(title, body))
    try:
        os.makedirs(d, exist_ok=True)
        if os.path.exists(path) and os.path.getsize(path) > LOG_LIMIT:
            with open(path, encoding="utf-8", errors="replace") as f:
                tail = f.readlines()[-LOG_KEEP:]
            with open(path, "w", encoding="utf-8") as f:
                f.writelines(tail)
        with open(path, "a", encoding="utf-8") as f:
            f.write(line)
    except OSError:
        pass


def terminal_sequence(title, body):
    # Запасной путь там, где системного бэкенда нет (ssh, голый Linux): харнес
    # сам выдаёт OSC-последовательность в терминал. Тело OSC 9 не должно
    # начинаться с цифры, иначе харнес его отбросит.
    text = "%s. %s" % (title, body) if body else title
    if text[:1].isdigit():
        text = " " + text
    return "\033]9;%s\007" % text


def deliver(title, body, session="-", key="-", target=None, level=LOUD,
            task=None, project=None):
    """Отправить и записать в журнал. Возврат (бэкенд, код возврата)."""
    backend = pick_backend()
    if not backend:
        log(session, key, None, "бэкенда нет: слать нечем", level=level,
            title=title, body=body, task=task, project=project)
        return None, None
    # В журнал идёт цель, которая реально уехала: бэкенд без клика её не берёт,
    # и жалоба «клик не работает» тогда разбирается по строке, а не на глаз.
    sent = target if supports_click(backend) else None
    code = send(backend, title, body, sent, level, session)
    log(session, key, backend,
        "код возврата: %s" % ("не запустился" if code is None else code), sent, level,
        title, body, task, project)
    return backend, code


def run_hook(protocol):
    # Вход кривой во всех видах (не json, json не объектом, объект с полями не
    # той формы) заканчивается одинаково: код 0 и строка в журнале. Хук стоит в
    # каждой сессии, и падать traceback'ом ему нельзя, а молчать про непонятое
    # значит держать эту дыру незаметной.
    try:
        sess = hookio.session_event(protocol)
    except hookio.BadEvent as e:
        log("-", "-", None, "событие не разобрано: %s" % e)
        return 0
    if sess is None:
        return 0
    session = sess.session
    if sess.kind == hookio.PROMPT_SUBMIT:
        # Ввод пользователя ничего не шлёт, он снимает отметку ожидания: иначе
        # общее окно с idle_prompt глушило бы и следующий конец хода той же
        # сессии, а это уже новый повод позвать.
        clear_wait(session)
        return 0
    try:
        root = session_tree(sess)
        parsed = parse_event(sess, root)
    except (AttributeError, TypeError, ValueError):
        log(session, "-", None, "событие не разобрано: поля не той формы")
        return 0
    if not parsed:
        return 0
    key, title, body, level = parsed
    # Задача и проект собираются из деревьев события: рабочего дерева самого
    # повода и окна сессии, которое его подняло. Дальше они едут в каждую
    # строку журнала, включая строки пропусков: событие, до баннера не
    # доехавшее, лента всё равно показывает и вести от него должна туда же.
    task, project = event_target(sess.cwd, root)
    sandbox = sandbox_reason(root)
    if sandbox:
        # Симметрично аргументному пути: корень под TMPDIR это песочница вроде
        # синтетической доски из обкатки сценария или вложенная headless-сессия
        # из scratchpad, и баннер про неё ложный. Пропуск виден в журнале той же
        # строкой, что и у аргументного вызова, а опрос фокуса и троттлинг
        # переводить на такой корень незачем.
        log(session, key, None, "пропуск: песочница, %s" % sandbox, level=level,
            title=title, body=body, task=task, project=project)
        return 0
    if key == TURN_DONE:
        # Фокус спрашивается только тут. Запрос разрешения и вопрос агента зовут
        # всегда (сессия на них стоит намертво), а фоновым поводам лишний опрос
        # на каждого субагента ни к чему.
        state = focus_state(root)
        if state == FOCUS_SESSION:
            log(session, key, None, "пропуск: окно сессии в фокусе", level=level,
                title=title, body=body, task=task, project=project)
            return 0
        if state == FOCUS_UNKNOWN:
            # Тишина тут хуже лишнего баннера: она неотличима от штатной работы,
            # а разрешение на управление компьютером выдают не на всякой машине.
            log(session, key, None, "фокус %s, зовём" % FOCUS_UNKNOWN, level=level,
                title=title, body=body, task=task, project=project)
    if not allow(session, key):
        log(session, key, None,
            "пропуск: повтор в окне %dс" % throttle(key)[1], level=level,
            title=title, body=body, task=task, project=project)
        return 0
    backend, _ = deliver(title, body, session, key, click_target(cwd=root), level,
                         task, project)
    if not backend:
        # Системного бэкенда нет, остаётся сам терминал: харнес выдаст
        # последовательность за нас.
        print(json.dumps({"terminalSequence": terminal_sequence(title, body)}))
    return 0


def self_test():
    if os.environ.get("DEVKIT_NOTIFY_OFF"):
        print("уведомления выключены переменной DEVKIT_NOTIFY_OFF, ничего не слал")
        return 1
    title = "%s: самопроверка" % session_label(os.getcwd())
    target = click_target(cwd=os.getcwd())
    task, project = event_target(os.getcwd())
    backend, code = deliver(title, "канал уведомлений devkit", "-", "self-test",
                            target, task=task, project=project)
    if not backend:
        print("бэкенда уведомлений нет: на macOS ждём terminal-notifier или "
              "osascript, на Linux notify-send")
        return 1
    print("послано через %s, код возврата %s" % (backend, code))
    if code != 0:
        return 1
    if supports_click(backend):
        print("клик по баннеру ведёт в %s"
              % (target[1] if target else "никуда: цель не собралась"))
    else:
        print("клик по баннеру никуда не ведёт: %s постит от имени Script Editor, "
              "переход даёт terminal-notifier (brew install terminal-notifier)"
              % os.path.basename(backend))
    print("баннера не видно? разрешение на уведомления даётся в системных "
          "настройках тому приложению, от чьего имени пришёл баннер")
    return 0


def main(argv):
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    if argv[0] == "--hook":
        if os.environ.get("DEVKIT_NOTIFY_OFF"):
            return 0
        if os.environ.get(RUN_DEPTH):
            # Сессия подпроцесса делегирования: её никто не ждёт у окна,
            # диспетчер стоит на agentctl run синхронно и заберёт результат
            # сам. Звонок на каждый её конец хода это шум, от которого потом
            # глохнет настоящий. Пропуск виден строкой журнала: молчание иначе
            # не отличить от сломанного канала.
            log("-", "-", None, "пропуск: подпроцесс делегирования (%s)" % RUN_DEPTH)
            return 0
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("notify: %s\n" % e)
            return 2
    if argv[0] == "--self-test":
        return self_test()
    # Без флага зовущий скрипт считается громким: он зовёт человека к делу, а
    # не рассказывает о ходе работы. Повода у аргументного вызова по умолчанию
    # нет: строка журнала тогда встаёт с прочерком, как раньше, а назвавший
    # повод словом получает его в журнале и в ленте дашборда.
    level, key = LOUD, "-"
    # Задачу и проект зовущий называет сам: taskctl знает строку доски, дашборд
    # знает проект, а по рабочей директории видно не всё (доску двигают из
    # основного чекаута, где ветка это main). Не назвал, значит поля собираются
    # по рабочей директории, как в хук-режиме.
    task, project = None, None
    while argv and argv[0] in ("--quiet", "--reason", "--task", "--project"):
        if argv[0] == "--quiet":
            level, argv = QUIET, argv[1:]
            continue
        flag = argv[0]
        if len(argv) < 2 or not argv[1].strip():
            sys.stderr.write("notify: %s ждёт %s словом\n" % (flag, FLAG_WORD[flag]))
            return 2
        value, argv = argv[1].strip(), argv[2:]
        if flag == "--reason":
            key = value
        elif flag == "--task":
            task = value
        else:
            project = value
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    if os.environ.get("DEVKIT_NOTIFY_OFF"):
        return 0
    title = short(argv[0])
    body = short(argv[1]) if len(argv) > 1 else ""
    guess_task, guess_project = event_target(os.getcwd())
    task = task_id(task) if task else guess_task
    project = project or guess_project
    sandbox = sandbox_reason(os.getcwd())
    if sandbox:
        # Молчать про пропуск нельзя: строка уходит и в журнал, и в stdout,
        # откуда зовущая утилита доносит её до своего вывода. Повод и текст в
        # строке остаются: пропущенный баннер это про доставку, а не про то,
        # что события не было.
        log("-", key, None, "пропуск: песочница, %s" % sandbox, level=level,
            title=title, body=body, task=task, project=project)
        print("уведомление пропущено: %s, звать некого" % sandbox)
        return 0
    # Троттлинга тут нет: позвал скрипт, значит шлём. Окно держит поток событий
    # харнеса, а не осознанный вызов.
    backend, code = deliver(title, body, key=key,
                            target=click_target(cwd=os.getcwd()), level=level,
                            task=task, project=project)
    return 0 if backend and code == 0 else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
