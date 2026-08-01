#!/usr/bin/env python3
"""Уведомитель devkit: сказать наружу, что сессия ждёт действия или что
субагент отработал. Параллельных сессий на машине несколько, и та, что упёрлась
в запрос разрешения, иначе стоит, пока на её окно не посмотрят.

Поводы двух сортов. Громкий зовёт подключиться (сессия упёрлась в вопрос,
закончила ход) и приходит со звуком и своим баннером. Фоновый рассказывает о
ходе работы (субагент отработал), приходит молча и схлопывается по сессии,
чтобы не копить ленту.

Режимы:
  notify.py [--quiet] <заголовок> [<текст>]
                                    позвать уведомитель из чего угодно: скрипт
                                    выката, другой харнес, проверка расписания;
                                    --quiet понижает повод до фонового
  notify.py --hook claude-code      хук Claude Code: событие читается со stdin,
                                    заголовок и тело собираются из него;
                                    события Notification (по поводу), Stop,
                                    SubagentStop и UserPromptSubmit (та только
                                    отмечает начало хода), остальное молча
                                    пропускается
  notify.py --self-test             послать пробное уведомление и напечатать,
                                    чем именно послано

Переменные окружения:
  DEVKIT_NOTIFY_OFF=1        не слать ничего (headless-прогоны, уведомлять некого)
  DEVKIT_NOTIFY_BACKEND=...  перебить выбор бэкенда: имя (terminal-notifier,
                             osascript, notify-send) либо своя команда, её зовут
                             как «команда заголовок тело»
  DEVKIT_NOTIFY_OPEN=...     своя цель перехода по клику, `{cwd}` подставляется
                             рабочим деревом сессии; пустое значение гасит клик
  DEVKIT_NOTIFY_TURN_MIN=... порог длительности хода в секундах, короче него
                             конец хода уведомления не даёт (по умолчанию 60)

Журнал последних отправок лежит в ~/.devkit/notify.log: время, сессия, повод,
уровень, бэкенд, цель перехода и код возврата. Жалоба «уведомления не приходят»
разбирается по нему, как и «важное не отличается от фонового».
"""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.parse

WINDOW = 30          # окно троттлинга: один повод одной сессии не чаще, секунды
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
# Ход короче этого уведомления не даёт: пользователь эти секунды смотрит в то
# же окно, и звать его некуда. Порог перебивается DEVKIT_NOTIFY_TURN_MIN.
TURN_MIN = 60
# Конец хода и idle_prompt это один и тот же повод «сессия ждёт тебя», поэтому
# троттлится он общим ключом. Окно шире минуты: idle_prompt харнес присылает
# примерно через минуту после конца хода, и своим окном он продублировал бы
# баннер.
WAIT_KEY = "session_waiting"
WAIT_REASONS = ("idle_prompt", TURN_DONE)
WAIT_WINDOW = 180

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


def session_label(cwd, root=None):
    # Какая из сессий позвала, видно по имени рабочего дерева: у worktree задачи
    # в имени лежит её ID. Без этого при пяти агентах баннер бесполезен.
    # Работал субагент в дереве задачи, значит в заголовке стоит и окно, где его
    # искать, и сама задача: «it-road-course (irc-75)».
    name, home = tree_name(cwd), tree_name(root)
    if not home or home == name:
        return name or "сессия"
    if not name:
        return home
    task = name[len(home) + 1:] if name.startswith(home + "-") else name
    return "%s (%s)" % (home, task)


def session_tree(event, read=None):
    """Дерево, на котором стоит окно позвавшей сессии.

    У субагента в worktree задачи свой `cwd`, и цель, собранная из него, ведёт
    мимо: своего окна у такого дерева нет, и клик открывает лишнее окно вместо
    того, где идёт работа. Само дерево сессии лежит в её транскрипте: первые
    записи там служебные, а дальше идёт `cwd` самой сессии."""
    read = transcript_cwd if read is None else read
    root = read(event.get("transcript_path"))
    if root:
        return root
    cwd = event.get("cwd")
    return cwd if isinstance(cwd, str) else ""


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


def parse_event(event, root=None):
    """Событие харнеса в (ключ повода, заголовок, тело, уровень). None значит
    не шлём."""
    name = event.get("hook_event_name")
    level = LOUD
    if name == "Notification":
        key = event.get("notification_type") or ""
        label = NOTIFICATION_REASONS.get(key)
        if not label:
            return None
        body = short(event.get("message"))
    elif name == "Stop":
        # Конец хода главной сессии. Своего текста у события нет, тело собирать
        # не из чего, и баннеру хватает заголовка.
        key, label, body = TURN_DONE, TURN_REASON, ""
    elif name == "SubagentStop":
        level = QUIET
        # Обычный субагент приходит сюда, а не поводом agent_completed события
        # Notification: тот повод про фоновые сессии, не про субагентов.
        key = "subagent_stop"
        label = SUBAGENT_REASON
        # Роль субагента идёт в тело уже после того, как от ответа осталась
        # первая строка, иначе первой строкой стала бы сама роль.
        body = short(event.get("last_assistant_message"))
        agent = event.get("agent_type")
        if agent:
            body = short("%s: %s" % (agent, body) if body else agent)
    else:
        return None
    return key, "%s: %s" % (session_label(event.get("cwd"), root), label), body, level


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


def allow(session, key, now=None, path=None):
    """Можно ли слать этот повод этой сессии: прошлая отправка старше окна.
    Разрешив, отметку сдвигает."""
    now = time.time() if now is None else now
    path = state_dir() if path is None else path
    key, window = throttle(key)
    mark = os.path.join(path, hashlib.sha1(
        ("%s|%s" % (session, key)).encode("utf-8")).hexdigest()[:16])
    last = read_stamp(mark)
    if last is not None and 0 <= now - last < window:
        return False
    stamp(mark, now)
    sweep(path, now)
    return True


def turn_mark(session, path=None):
    path = state_dir() if path is None else path
    return os.path.join(path, "turn-" + hashlib.sha1(
        str(session).encode("utf-8")).hexdigest()[:16])


def mark_turn(session, now=None, path=None):
    """Отметить начало хода: с этой метки считается его длительность."""
    stamp(turn_mark(session, path), time.time() if now is None else now)


def turn_min(env=None):
    env = os.environ if env is None else env
    try:
        return max(0.0, float((env.get("DEVKIT_NOTIFY_TURN_MIN") or "").strip()))
    except ValueError:
        return float(TURN_MIN)


def short_turn(session, now=None, path=None, env=None):
    """Ход короче порога, звать некуда: пользователь эти секунды смотрит в то
    же окно. Метки нет (сессия поднялась до правки настроек, ход первый),
    значит порог не срабатывает и уведомление уходит."""
    now = time.time() if now is None else now
    started = read_stamp(turn_mark(session, path))
    return started is not None and 0 <= now - started < turn_min(env)


def log(session, key, backend, result, target=None, level=None):
    d = os.path.join(os.path.expanduser("~"), ".devkit")
    path = os.path.join(d, "notify.log")
    line = "%s сессия %s повод %s уровень %s бэкенд %s цель %s %s\n" % (
        time.strftime("%Y-%m-%dT%H:%M:%S"), session or "-", key or "-",
        level or "-", backend or "-", target[1] if target else "-", result)
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


def deliver(title, body, session="-", key="-", target=None, level=LOUD):
    """Отправить и записать в журнал. Возврат (бэкенд, код возврата)."""
    backend = pick_backend()
    if not backend:
        log(session, key, None, "бэкенда нет: слать нечем", level=level)
        return None, None
    # В журнал идёт цель, которая реально уехала: бэкенд без клика её не берёт,
    # и жалоба «клик не работает» тогда разбирается по строке, а не на глаз.
    sent = target if supports_click(backend) else None
    code = send(backend, title, body, sent, level, session)
    log(session, key, backend,
        "код возврата: %s" % ("не запустился" if code is None else code), sent, level)
    return backend, code


def run_hook(harness):
    if harness != "claude-code":
        sys.stderr.write("notify: разбор события %s не заведён\n" % harness)
        return 2
    # Вход кривой во всех видах (не json, json не объектом, объект с полями не
    # той формы) заканчивается одинаково: код 0 и строка в журнале. Хук стоит в
    # каждой сессии, и падать traceback'ом ему нельзя, а молчать про непонятое
    # значит держать эту дыру незаметной.
    try:
        event = json.load(sys.stdin)
    except (json.JSONDecodeError, UnicodeDecodeError):
        log("-", "-", None, "событие не разобрано: не json")
        return 0
    if not isinstance(event, dict):
        log("-", "-", None, "событие не разобрано: json не объектом")
        return 0
    session = str(event.get("session_id") or "-")[:8]
    if event.get("hook_event_name") == "UserPromptSubmit":
        # Ввод пользователя ничего не шлёт, он только отмечает начало хода: от
        # этой метки конец хода считает свою длительность.
        mark_turn(session)
        return 0
    try:
        root = session_tree(event)
        parsed = parse_event(event, root)
    except (AttributeError, TypeError, ValueError):
        log(session, "-", None, "событие не разобрано: поля не той формы")
        return 0
    if not parsed:
        return 0
    key, title, body, level = parsed
    if key == TURN_DONE and short_turn(session):
        log(session, key, None, "пропуск: ход короче %gс" % turn_min(), level=level)
        return 0
    if not allow(session, key):
        log(session, key, None,
            "пропуск: повтор в окне %dс" % throttle(key)[1], level=level)
        return 0
    backend, _ = deliver(title, body, session, key, click_target(cwd=root), level)
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
    backend, code = deliver(title, "канал уведомлений devkit", "-", "self-test", target)
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
        return run_hook(argv[1] if len(argv) > 1 else "claude-code")
    if argv[0] == "--self-test":
        return self_test()
    # Без флага зовущий скрипт считается громким: он зовёт человека к делу, а
    # не рассказывает о ходе работы. Старые вызовы от этого не меняются.
    level = LOUD
    if argv[0] == "--quiet":
        level, argv = QUIET, argv[1:]
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    if os.environ.get("DEVKIT_NOTIFY_OFF"):
        return 0
    title = short(argv[0])
    body = short(argv[1]) if len(argv) > 1 else ""
    # Троттлинга тут нет: позвал скрипт, значит шлём. Окно держит поток событий
    # харнеса, а не осознанный вызов.
    backend, code = deliver(title, body, target=click_target(cwd=os.getcwd()),
                            level=level)
    return 0 if backend and code == 0 else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
