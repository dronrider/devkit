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
"""
import os
import say
import subprocess
import sys
from datetime import datetime
from pathlib import Path

DEVKIT = Path(__file__).resolve().parent.parent.parent
NOTIFIER = DEVKIT / "hooks" / "notify.py"

GOALS_DIR = "~/.devkit/goals"
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


def shout(title, body, root, call=None):
    """Громкий зов уведомителем. Зовётся он из корня проекта, как из оболочки
    goal-run: по рабочему дереву уведомитель собирает заголовок баннера и цель
    перехода по клику."""
    call = subprocess.run if call is None else call
    try:
        p = call([sys.executable, str(NOTIFIER), title, body], cwd=root,
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
    said = shout(title, body, root, call)
    entry["stopped"] = now.strftime(STAMP)
    write_entry(path, entry)
    return True, "цель %s в %s: простой %s при пороге %s, зову; %s" % (
        goal, root, say.human_age(gap), say.human_age(idle), said)


def drop(path):
    try:
        os.remove(path)
    except OSError:
        pass


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


def run(now=None, idle=None, home=None, out=None, call=None):
    """Обход реестра. Возврат 0 всё движется, 1 нашёлся вставший цикл."""
    now = datetime.now() if now is None else now
    home = default_home() if home is None else home
    idle = conf_idle(home) if idle is None else idle
    out = sys.stdout if out is None else out
    found, watched = 0, 0
    for path in entries(home):
        watched += 1
        called, line = look(path, now, idle, call)
        found += 1 if called else 0
        out.write(line + "\n")
        if called:
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
