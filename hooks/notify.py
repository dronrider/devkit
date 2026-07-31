#!/usr/bin/env python3
"""Уведомитель devkit: сказать наружу, что сессия ждёт действия или что
субагент отработал. Параллельных сессий на машине несколько, и та, что упёрлась
в запрос разрешения, иначе стоит, пока на её окно не посмотрят.

Режимы:
  notify.py <заголовок> [<текст>]   позвать уведомитель из чего угодно: скрипт
                                    выката, другой харнес, проверка расписания
  notify.py --hook claude-code      хук Claude Code: событие читается со stdin,
                                    заголовок и тело собираются из него;
                                    события Notification (по поводу) и
                                    SubagentStop, остальное молча пропускается
  notify.py --self-test             послать пробное уведомление и напечатать,
                                    чем именно послано

Переменные окружения:
  DEVKIT_NOTIFY_OFF=1        не слать ничего (headless-прогоны, уведомлять некого)
  DEVKIT_NOTIFY_BACKEND=...  перебить выбор бэкенда: имя (osascript, notify-send)
                             либо своя команда, её зовут как «команда заголовок тело»

Журнал последних отправок лежит в ~/.devkit/notify.log: время, сессия, повод,
бэкенд и код возврата. Жалоба «уведомления не приходят» разбирается по нему.
"""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time

WINDOW = 30          # окно троттлинга: один повод одной сессии не чаще, секунды
STALE = 24 * 3600    # брошенное состояние троттлинга старше суток убирается
BODY_LIMIT = 200
LOG_LIMIT = 100 * 1024
LOG_KEEP = 500

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


def session_label(cwd):
    # Какая из сессий позвала, видно по имени рабочего дерева: у worktree задачи
    # в имени лежит её ID. Без этого при пяти агентах баннер бесполезен.
    name = os.path.basename((cwd if isinstance(cwd, str) else "").rstrip("/"))
    return name or "сессия"


def parse_event(event):
    """Событие харнеса в (ключ повода, заголовок, тело). None значит не шлём."""
    name = event.get("hook_event_name")
    if name == "Notification":
        key = event.get("notification_type") or ""
        label = NOTIFICATION_REASONS.get(key)
        if not label:
            return None
        body = short(event.get("message"))
    elif name == "SubagentStop":
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
    return key, "%s: %s" % (session_label(event.get("cwd")), label), body


def pick_backend(env=None, platform=None, which=None):
    """Чем слать на этой машине. None значит нечем."""
    env = os.environ if env is None else env
    platform = sys.platform if platform is None else platform
    which = shutil.which if which is None else which
    override = (env.get("DEVKIT_NOTIFY_BACKEND") or "").strip()
    if override:
        return override
    if platform == "darwin":
        return "osascript" if which("osascript") else None
    if platform.startswith("linux"):
        return "notify-send" if which("notify-send") else None
    # Windows и WSL: место под свой бэкенд (powershell-тост, wsl-notify-send),
    # пока нечем.
    return None


def backend_argv(backend, title, body):
    name = os.path.basename(backend)
    if name == "osascript":
        script = 'display notification "%s" with title "%s"' % (
            applescript_quote(body), applescript_quote(title))
        return [backend, "-e", script]
    # notify-send и своя команда зовутся одинаково: заголовок и тело аргументами.
    return [backend, title, body]


def applescript_quote(text):
    return text.replace("\\", "\\\\").replace('"', '\\"')


def send(backend, title, body):
    """Код возврата бэкенда, None если запустить не удалось."""
    try:
        p = subprocess.run(backend_argv(backend, title, body),
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


def allow(session, key, now=None, path=None):
    """Можно ли слать этот повод этой сессии: прошлая отправка старше окна.
    Разрешив, отметку сдвигает."""
    now = time.time() if now is None else now
    path = state_dir() if path is None else path
    mark = os.path.join(path, hashlib.sha1(
        ("%s|%s" % (session, key)).encode("utf-8")).hexdigest()[:16])
    try:
        with open(mark, encoding="utf-8") as f:
            last = float(f.read().strip())
    except (OSError, ValueError):
        last = None
    if last is not None and 0 <= now - last < WINDOW:
        return False
    try:
        os.makedirs(path, exist_ok=True)
        with open(mark, "w", encoding="utf-8") as f:
            f.write("%.3f" % now)
        os.utime(mark, (now, now))
    except OSError:
        pass
    sweep(path, now)
    return True


def log(session, key, backend, result):
    d = os.path.join(os.path.expanduser("~"), ".devkit")
    path = os.path.join(d, "notify.log")
    line = "%s сессия %s повод %s бэкенд %s %s\n" % (
        time.strftime("%Y-%m-%dT%H:%M:%S"), session or "-", key or "-",
        backend or "-", result)
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


def deliver(title, body, session="-", key="-"):
    """Отправить и записать в журнал. Возврат (бэкенд, код возврата)."""
    backend = pick_backend()
    if not backend:
        log(session, key, None, "бэкенда нет: слать нечем")
        return None, None
    code = send(backend, title, body)
    log(session, key, backend,
        "код возврата: %s" % ("не запустился" if code is None else code))
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
    try:
        parsed = parse_event(event)
    except (AttributeError, TypeError, ValueError):
        log(session, "-", None, "событие не разобрано: поля не той формы")
        return 0
    if not parsed:
        return 0
    key, title, body = parsed
    if not allow(session, key):
        log(session, key, None, "пропуск: повтор в окне %dс" % WINDOW)
        return 0
    backend, _ = deliver(title, body, session, key)
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
    backend, code = deliver(title, "канал уведомлений devkit", "-", "self-test")
    if not backend:
        print("бэкенда уведомлений нет: на macOS ждём osascript, на Linux notify-send")
        return 1
    print("послано через %s, код возврата %s" % (backend, code))
    if code != 0:
        return 1
    print("баннера не видно? на macOS уведомление приходит от имени Script Editor, "
          "разрешение ему даётся в системных настройках")
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
    if os.environ.get("DEVKIT_NOTIFY_OFF"):
        return 0
    title = short(argv[0])
    body = short(argv[1]) if len(argv) > 1 else ""
    # Троттлинга тут нет: позвал скрипт, значит шлём. Окно держит поток событий
    # харнеса, а не осознанный вызов.
    backend, code = deliver(title, body)
    return 0 if backend and code == 0 else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
