"""Стенд самопроверки devkitctl: копия devkit во временной директории и
подставной машинный контур вокруг неё.

На живом чекауте гонять нельзя: доктор сверяет коммит бинарей с HEAD основного
чекаута, сверяет определения агентов с ним же и лезет в HOME, так что
незакоммиченная правка в ~/projects/devkit красила бы самопроверку на исправном
коде. Копия заводится своим git-репозиторием и для доктора она и есть основной
чекаут.
"""
import atexit
import contextlib
import hashlib
import json
import os
import pty
import re
import select
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from datetime import datetime, timedelta
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEVKIT_SRC = HERE.parent.parent
PY = sys.executable

# Настоящий HOME из-под тестов не виден вовсе: и модули devkitctl, и сам доктор
# ходят в него через expanduser, и тест, забывший подставить свой, читал бы
# раскладку машины, на которой запущен. Такой тест зелен у того, у кого devkit
# уже разложен, и красен на голой машине, а увидеть это можно только там.
# Поэтому HOME процесса подменяется на пустую директорию: забытая подстановка
# краснеет сразу и у всех, а не в чужом CI.
REAL_HOME = os.environ["HOME"]
GUARD_HOME = Path(tempfile.mkdtemp(prefix="devkitctl-guard-home-"))
os.environ["HOME"] = str(GUARD_HOME)
atexit.register(shutil.rmtree, str(GUARD_HOME), True)
_GO_ENV = {}

# Системная часть PATH подставная: в ней только то, без чего не обойтись.
# Проверки вида «tmux не в PATH» иначе держались бы на том, чего нет в /usr/bin
# на этой машине, и на другой краснели бы при исправном коде. sh нужен утилитам
# devkit: тесты и выкат слияния гоняются через sh -c, и без него краснел бы
# сам конвейер, а не проверяемая раскладка.
SYS_TOOLS = ("git", "python3", "sh", "dirname", "mkdir", "chmod", "rm")
# launchctl заглушкой, а не с машины: доктор спрашивает им, поднят ли агент
# сторожка цикла цели, а доводка сама зовёт bootstrap, и настоящему launchctl
# в самопроверке делать нечего.
STUB_TOOLS = ("launchctl",)
STUB = "#!/bin/sh\nexit 0\n"
# Заглушка бинаря devkit: доктор судит о нём по строке --version, и заглушка,
# отвечающая «exit 0», числилась бы у него собранной до релизной схемы.
BINARY_STUB = '#!/bin/sh\n[ "$1" = "--version" ] && echo "%s"\nexit 0\n'
# Момент сборки релиза в тарболле: 2001 год.
RELEASE_MTIME = 1000000000

# Первый exec свежего скрипта на macOS платит проверку подписи кода, повторный
# того же файла уже почти ничего. Стенд кладёт десяток заглушек отдельными
# файлами, и первый доктор класса платил бы по трети секунды на каждую.
# Поэтому заглушки бинарей это симлинки на один диспетчер: у него разбор по
# собственному имени (basename $0), и каждое имя отвечает своей строкой
# --version. Диспетчер прогревается одним холостым запуском при создании
# стенда, а симлинки исполняются уже без проверки подписи. Заглушки, чьё тело
# это целый скрипт (GO_STUB, BREW_STUB, обёртка devkitctl, taskctl с доской),
# остаются отдельными файлами: их запускают считанные разы за прогон.
DISPATCHER_HEAD = "#!/bin/sh\ncase \"$0\" in\n"
DISPATCHER_CASE = "*%s) [ \"$1\" = \"--version\" ] && echo \"%s\"; exit 0 ;;\n"
DISPATCHER_TAIL = "esac\nexit 0\n"


def dispatcher_script(bodies):
    """Скрипт диспетчера заглушек: имя -> строка --version."""
    lines = [DISPATCHER_HEAD]
    for name, line in bodies.items():
        lines.append(DISPATCHER_CASE % (name, line))
    lines.append(DISPATCHER_TAIL)
    return "".join(lines)

# Хуки харнеса в фикстуре машинного контура: чистый проект должен быть чист.
# Каждый хук стоит своей строкой, потому что проверки режут этот файл построчно,
# и после реза он обязан оставаться разбираемым.
NOTIFY = "python3 ~/projects/devkit/hooks/notify.py --hook claude-code"
WATCH = "python3 ~/projects/devkit/hooks/agent-watch.py --hook claude-code"
SETTINGS = """{"permissions": {"allow": %s, "deny": %s},
 "hooks": {"PostToolUse": [{"matcher": "Edit|Write|NotebookEdit", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-symbols.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-memory.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-sensitive.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-prose.py --hook"}
]}, {"hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/chat-in.py --hook claude-code"}
]}, {"matcher": "Agent", "hooks": [
  {"type": "command", "command": "%s"}
]}], "PreToolUse": [{"matcher": "Bash", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-read-secret.py --hook"}
]}, {"matcher": "Read", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-reread.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-longfile.py --hook"}
]}], "SessionStart": [{"hooks": [
  {"type": "command", "command": "sh ~/projects/devkit/hooks/quota-refresh.sh"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/session-task.py --hook claude-code"},
  {"type": "command", "command": "sh ~/projects/devkit/hooks/board-catchup.sh"}
]}], "Notification": [{"hooks": [
  {"type": "command", "command": "%s"}
]}], "Stop": [{"hooks": [
  {"type": "command", "command": "%s"},
  {"type": "command", "command": "%s"}
]}], "StopFailure": [{"hooks": [
  {"type": "command", "command": "%s"}
]}], "SubagentStop": [{"hooks": [
  {"type": "command", "command": "%s"},
  {"type": "command", "command": "%s"}
]}], "UserPromptSubmit": [{"hooks": [
  {"type": "command", "command": "%s"}
]}]},
 "env": {"CLAUDE_CODE_RETRY_WATCHDOG": "1"}}
"""

# Заглушка go: настоящая сборка шести модулей на четырёх парах стоила бы минуты
# на каждый прогон самопроверки. Разбирает она то же, что передаёт сборка (-o и
# -ldflags -X), и кладёт по пути скрипт, печатающий строку версии по --version:
# без подстановки не проверить ни самопроверку запуском, ни байтовую. За пределы
# временной директории заглушка не пишет: промах настройки иначе затёр бы
# настоящие бинари в ~/go/bin.
#
# Две порчи нарочные, ими и проверяются обе самопроверки сборки:
#   GO_STUB_NO_FLAG=<утилита>          собранный бинарь не знает --version,
#                                      хотя версия в нём лежит
#   GO_STUB_NO_STAMP=<goos>/<goarch>   на эту пару сборка идёт без подстановки
# Запуск собранного пишется в файл GO_STUB_RUNLOG: по нему видно, какие пары
# релизная сборка исполняла живьём, а какие проверяла байтами.
GO_STUB = '''#!%s
import os
import stat
import sys

argv, out, ld = sys.argv[1:], None, ""
while argv:
    if argv[0] == "-o" and len(argv) > 1:
        out = argv[1]
    if argv[0] == "-ldflags" and len(argv) > 1:
        ld = argv[1]
    argv = argv[1:]
if not out:
    sys.exit(1)
sandbox = os.environ["SANDBOX"]
if not out.startswith(sandbox + os.sep):
    sys.stderr.write("заглушка go пишет только в %%s: %%s\\n" %% (sandbox, out))
    sys.exit(1)
name = os.path.basename(out)
# Своё имя настоящая утилита несёт в коде, а не берёт из пути сборки, поэтому
# суффикс промежуточного файла (сборка идёт рядом и переезжает переименованием)
# в строку версии не попадает.
if name.endswith(".new"):
    name = name[:-4]
stamp = dict(p.split("=", 1) for p in ld.split() if "=" in p)
pair = "%%s/%%s" %% (os.environ.get("GOOS", ""), os.environ.get("GOARCH", ""))
if os.environ.get("GO_STUB_NO_STAMP") == pair:
    stamp = {}
line = "%%s %%s (%%s)" %% (name, stamp.get("main.version", "dev"),
                       stamp.get("main.commit", "unknown"))
body = 'echo "%%s"' %% line
if os.environ.get("GO_STUB_NO_FLAG") == name:
    body = 'echo "%%s: справка"' %% name
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    f.write("#!/bin/sh\\n# %%s\\n"
            '[ -n "$GO_STUB_RUNLOG" ] && echo "$0" >> "$GO_STUB_RUNLOG"\\n'
            '[ "$1" = "--version" ] && %%s\\n'
            "exit 0\\n" %% (line, body))
os.chmod(out, os.stat(out).st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
''' % PY

# Заглушка пакетного менеджера: настоящий brew в самопроверке не участвует, а
# проверяется то, что доводка зовёт именно его и именно с тем пакетом. Пакет она
# кладёт исполняемым файлом в BREW_STUB_BIN (это каталог в подставном PATH),
# позванное пишет в BREW_STUB_LOG. Две порчи нарочные, и они разные:
#   BREW_STUB_FAIL     менеджер отвечает ошибкой и говорит, чем недоволен
#   BREW_STUB_SILENT   менеджер отрабатывает нулём, а пакета не появляется
# Второе это сломанный менеджер, и на живом прогоне DK-157 доктор описал его
# враньём («не прошёл ()»), хотя команда как раз прошла.
BREW_STUB = '''#!%s
import os
import stat
import sys

argv = sys.argv[1:]
log = os.environ.get("BREW_STUB_LOG")
if log:
    with open(log, "a", encoding="utf-8") as f:
        f.write(" ".join(argv) + "\\n")
if argv[:1] != ["install"] or len(argv) != 2:
    sys.stderr.write("заглушка brew ждала install <пакет>: %%s\\n" %% " ".join(argv))
    sys.exit(2)
if os.environ.get("BREW_STUB_FAIL"):
    sys.stderr.write("Error: No available formula with the name %%s\\n" %% argv[1])
    sys.exit(1)
if os.environ.get("BREW_STUB_SILENT"):
    sys.exit(0)
path = os.path.join(os.environ["BREW_STUB_BIN"], argv[1])
with open(path, "w", encoding="utf-8") as f:
    f.write("#!/bin/sh\\nexit 0\\n")
os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
''' % PY

# Заглушка taskctl, которая доску всё-таки заводит: настоящий бинарь в PATH
# самопроверки заменён на печать версии, а без доски проверять нечего. Строку
# версии она печатает наравне с остальными заглушками: заглушка, промолчавшая на
# --version, для доктора собрана до релизной схемы.
BOARD_TASKCTL = '''#!%s
import os
import sys

argv, root, prefix = sys.argv[1:], ".", "TP"
if argv[:1] == ["--version"]:
    print("%s")
    sys.exit(0)
if argv[:1] == ["-C"]:
    root, argv = argv[1], argv[2:]
if "--prefix" in argv:
    prefix = argv[argv.index("--prefix") + 1]
if argv[:1] == ["init"]:
    # Место доски заглушка выбирает как настоящий taskctl: вершина репозитория,
    # а с --here названная директория. Иначе подключение корп-проекта без флага
    # зеленело бы на стенде и клало доску одну на весь контур (DK-583).
    if "--here" not in argv:
        import subprocess
        got = subprocess.run(["git", "-C", root, "rev-parse", "--show-toplevel"],
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        if got.returncode == 0 and got.stdout.strip():
            root = got.stdout.decode("utf-8").strip()
    os.makedirs(os.path.join(root, "docs", "tasks"), exist_ok=True)
    with open(os.path.join(root, "docs", "TASKS.md"), "w", encoding="utf-8") as f:
        f.write("# Задачи проекта (префикс %%s)\\n" %% prefix)
    print("доска создана")
'''


def sandbox_env(env=None, path=None, home=None):
    e = dict(os.environ)
    # Сборка бинарей идёт в GOBIN либо GOPATH, а они на машине выставлены на
    # настоящий ~/go/bin: без сброса самопроверка положила бы туда заглушки.
    for k in ("GOBIN", "GOPATH"):
        e.pop(k, None)
    if home is not None:
        e["HOME"] = str(home)
    if path is not None:
        e["PATH"] = str(path)
    if env:
        for k, v in env.items():
            if v is None:
                e.pop(k, None)
            else:
                e[k] = str(v)
    return e


def run(args, cwd=None, env=None, path=None, home=None):
    """Прогон команды с подставным окружением. Отдаёт код и слитый вывод.

    Ввод закрыт наглухо: подключение спрашивает недостающее, когда видит tty, и
    прогон, унаследовавший терминал раннера, вставал бы на вопросе. Диалог
    проверяется отдельно, через run_tty.
    """
    p = subprocess.run([str(a) for a in args], cwd=cwd and str(cwd),
                       env=sandbox_env(env, path, home), stdin=subprocess.DEVNULL,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    return p.returncode, p.stdout.decode("utf-8", "replace")


def run_tty(args, answers=(), cwd=None, env=None, path=None, home=None, timeout=120):
    """Тот же прогон, но под псевдотерминалом: команда видит tty и задаёт свои
    вопросы, а ответы подаются строками по порядку. Отдаёт код и вывод вместе с
    эхом ответов, как его видит человек в терминале."""
    master, slave = pty.openpty()
    p = subprocess.Popen([str(a) for a in args], cwd=cwd and str(cwd),
                         env=sandbox_env(env, path, home),
                         stdin=slave, stdout=slave, stderr=slave, close_fds=True)
    os.close(slave)
    os.write(master, "".join(a + "\n" for a in answers).encode("utf-8"))
    out, deadline = [], time.time() + timeout
    while time.time() < deadline:
        r, _, _ = select.select([master], [], [], deadline - time.time())
        if not r:
            break
        try:
            chunk = os.read(master, 4096)
        except OSError:
            # Ребёнок закрыл свой конец: на linux это EIO, на macOS пустое чтение.
            break
        if not chunk:
            break
        out.append(chunk)
    os.close(master)
    try:
        rc = p.wait(timeout=10)
    except subprocess.TimeoutExpired:
        p.kill()
        rc = p.wait()
    return rc, b"".join(out).decode("utf-8", "replace")


def go_cache_env():
    """Кеш сборки go с машины: без него сборка под подставным HOME гоняла бы
    компиляцию стандартной библиотеки заново на каждый прогон. Это единственное,
    за чем тесты ходят в настоящий HOME, и берётся оно одними путями кеша.
    """
    if not _GO_ENV:
        for key in ("GOCACHE", "GOMODCACHE"):
            rc, out = run(["go", "env", key], home=REAL_HOME)
            _GO_ENV[key] = out.strip() if rc == 0 else None
    return dict(_GO_ENV)


@contextlib.contextmanager
def fake_home(home):
    """Подставной HOME для вызовов модулей прямо в процессе теста.

    Модули devkitctl читают раскладку машины через expanduser, и юнит без такой
    подстановки судил бы по HOME того, кто запустил прогон.
    """
    old = os.environ["HOME"]
    os.environ["HOME"] = str(home)
    try:
        yield Path(home)
    finally:
        os.environ["HOME"] = old


def git(root, *args):
    return run(["git", "-C", str(root)] + list(args))


def git_init(root):
    Path(root).mkdir(parents=True, exist_ok=True)
    git(root, "init", "-q")
    git(root, "config", "user.name", "t")
    git(root, "config", "user.email", "t@t")
    return Path(root)


def write(path, text):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def read(path):
    return Path(path).read_text(encoding="utf-8")


def executable(path, text=STUB):
    write(path, text)
    os.chmod(str(path), 0o755)
    return Path(path)


def taken_at(hours_ago, fmt="%Y-%m-%dT%H:%M"):
    return (datetime.now() - timedelta(hours=hours_ago)).strftime(fmt)


def without_pockets(out):
    """Вывод доктора без таблицы карманов резидента: она информационная, а не находка."""
    left, table = [], False
    for ln in out.split("\n"):
        if re.match(r"^вес резидента .* по карманам ", ln):
            table = True
            continue
        if table and (ln.startswith("  ") or not ln.strip()):
            continue
        table = False
        left.append(ln)
    return "\n".join(left)


def stub_release(where, tag, commit, names):
    """Ассеты релиза: тарболл на пару этой машины и SHA256SUMS рядом.

    Отдают их тестам либо локальный сервер, либо file://: гитхаб в самопроверке
    не участвует, а бинари в тарболле это скрипты со своей строкой версии,
    проверяется тут не компилятор, а то, что вокруг него.
    """
    where = Path(where) / tag
    where.mkdir(parents=True, exist_ok=True)
    stage = where / "stage"
    stage.mkdir(exist_ok=True)
    for name in names:
        executable(stage / name, BINARY_STUB % build.version_line(name, tag, commit))
        # Время правки в тарболле это момент сборки релиза, и оно нарочно
        # старое: установка обязана поставить своё, иначе доктор посчитает
        # свежепоставленный бинарь несобранной правкой.
        os.utime(str(stage / name), (RELEASE_MTIME, RELEASE_MTIME))
    asset, bad = update.asset_name(tag)
    assert bad is None, bad
    with tarfile.open(str(where / asset), "w:gz") as tf:
        for name in names:
            tf.add(str(stage / name), arcname=name)
    shutil.rmtree(str(stage))
    write(where / build.SUMS, "%s  %s\n" % (build.sha256(where / asset), asset))
    return where / asset


if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))
import build  # noqa: E402
import context  # noqa: E402
import drain  # noqa: E402
import harness  # noqa: E402
import leak  # noqa: E402
import perms  # noqa: E402
import rules  # noqa: E402
import sessions  # noqa: E402
import update  # noqa: E402
import dashboard  # noqa: E402
import watch  # noqa: E402
import weigh  # noqa: E402


class Sandbox:
    """Копия devkit, подставной HOME и подставной PATH вокруг них."""

    def __init__(self, home=True):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-test-"))
        self.dk = self.root / "devkit"
        self.dk.mkdir()
        # internal копируется за tools: с DK-237 это общий go-модуль, на который
        # утилиты ссылаются relative replace ../../internal в go.mod, и без
        # каталога рядом сборка потребителя (shipctl с DK-266) в Sandbox падает.
        for d in ("tools", "kit", "hooks", "internal"):
            shutil.copytree(str(DEVKIT_SRC / d), str(self.dk / d))
        for f in ("RULES.md", "RULES.board.md"):
            shutil.copy(str(DEVKIT_SRC / f), str(self.dk / f))
        self.dkctl = self.dk / "tools" / "devkitctl" / "devkitctl.py"
        # Копия под git, и это не украшение: доктор сверяет коммит, зашитый в
        # бинарь, с HEAD основного чекаута, а стенд без git оставил бы сверку
        # без обеих сторон. Ходить за тегами копии некуда, поэтому FETCH_HEAD ей
        # проставляется руками: иначе на каждом прогоне горела бы находка про
        # давний поход за релизами.
        git_init(self.dk)
        git(self.dk, "add", "-A")
        git(self.dk, "commit", "-qm", "стенд")
        write(self.dk / ".git" / "FETCH_HEAD", "")
        self.version, self.commit = build.stamp(self.dk)

        # Машинный контур подставной: бинари devkit и tmux заглушками в своём
        # PATH. Иначе проверки цеплялись бы за настоящую машину. Заглушки
        # называют версию копии devkit: без неё доктор считает их разошедшимися.
        # Лежат они симлинками на один диспетчер (см. DISPATCHER_HEAD): первый
        # exec свежего скрипта платит проверку подписи кода, и десяток
        # отдельных файлов собирал бы секунды на каждом первом докторе класса.
        self.bin = self.root / "bin"
        self.bin.mkdir()
        bodies = {t: self.version_line(t) for t in build.tools(self.dk)}
        bodies["tmux"] = ""
        dispatcher = executable(self.bin / ".stub-dispatcher",
                                dispatcher_script(bodies))
        subprocess.run([str(dispatcher)], env=sandbox_env(), capture_output=True)
        for t in list(bodies):
            os.symlink(".stub-dispatcher", str(self.bin / t))
        self.sys = self.root / "sys"
        self.sys.mkdir()
        self.missing_tools = []
        for t in SYS_TOOLS:
            found = shutil.which(t)
            if not found:
                self.missing_tools.append(t)
                continue
            os.symlink(found, str(self.sys / t))
        for t in STUB_TOOLS:
            executable(self.sys / t)
        # Каталог назначения devkit: туда ставит бинари update и туда же доктор
        # кладёт обёртку devkitctl. В подставном PATH он стоит своей записью,
        # иначе на чистом проекте горела бы находка про каталог мимо PATH.
        self.dkbin = self.root / "dkbin"
        self.dkbin.mkdir()
        # Путь в обёртке разрешённый: доктор берёт чекаут через resolve(), а
        # mktemp на macOS отдаёт /var, который на деле симлинк на /private/var.
        executable(self.dkbin / update.WRAPPER,
                   update.wrapper_text(os.path.realpath(str(self.dk))))
        self.cleanpath = "%s:%s:%s" % (self.bin, self.dkbin, self.sys)
        # Чем слать уведомления, доктор спрашивает у самого уведомителя, а тот
        # смотрит платформу и PATH. Без подставного бэкенда проверка краснела бы
        # на машине без osascript или notify-send.
        self.notify_stub = self.root / "notify-stub"

        self.home = self.root / "home"
        if home:
            self.make_home(self.home)

    def version_line(self, name):
        """Строка --version, которую доктор ждёт от бинаря этой копии devkit."""
        return build.version_line(name, self.version, self.commit)

    def board_taskctl(self):
        """Заглушка taskctl, заводящая доску, со своей строкой версии."""
        return BOARD_TASKCTL % (PY, self.version_line("taskctl"))

    def make_home(self, home):
        """Машина с актуальной раскладкой devkit: на чистом проекте доктор молчит."""
        home = Path(home)
        (home / ".claude" / "agents").mkdir(parents=True)
        (home / ".claude" / "skills").mkdir(parents=True)
        (home / ".devkit" / "quota").mkdir(parents=True)
        allow = json.dumps(list(perms.MACHINE_ALLOW), ensure_ascii=False)
        deny = json.dumps(list(perms.SECRET_DENY), ensure_ascii=False)
        write(home / ".claude" / "settings.json",
              SETTINGS % (allow, deny, WATCH, NOTIFY, NOTIFY, WATCH,
                          NOTIFY, NOTIFY, WATCH, NOTIFY))
        for f in (self.dk / "kit" / "agents").glob("*.md"):
            shutil.copy(str(f), str(home / ".claude" / "agents" / f.name))
        for d in (self.dk / "kit" / "skills").iterdir():
            if d.is_dir():
                shutil.copytree(str(d), str(home / ".claude" / "skills" / d.name))
        write(home / ".devkit" / "quota" / "claude-code.local",
              "taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n" % taken_at(0))
        self.watch_agent(home)
        self.dashboard_agent(home)
        self.global_rules(home)
        self.alt_sub(home)
        return home

    def alt_sub(self, home):
        """Конфиг второй подписки в машинном слое: заполненный, как на машине,
        где подписка заведена. Без него на чистом проекте горела бы находка про
        неоткрывающееся окно, а проверяется он своими тестами."""
        home = Path(home)
        conf = home / ".devkit" / "claude-glm" / "settings.json"
        write(conf, json.dumps({"env": {
            "ANTHROPIC_BASE_URL": "https://endpoint.example/anthropic",
            "ANTHROPIC_AUTH_TOKEN": "токен-стенда",
            "ANTHROPIC_MODEL": "модель-стенда",
        }}, ensure_ascii=False, indent=2) + "\n")
        conf.parent.chmod(0o700)
        conf.chmod(0o600)
        return home

    def watch_agent(self, home):
        """Носитель сторожка цикла цели: launchd-агент на копию devkit и свежий
        след его прогона. Без него на чистом проекте горела бы находка про
        неподключённый сторожок, а проверяется он своими тестами."""
        home = Path(home)
        main = Path(os.path.realpath(str(self.dk)))
        log = home / ".devkit" / "goal-watch.log"
        write(home / watch.PLIST[2:],
              watch.plist_text(PY, main / "tools" / "devkitctl" / "devkitctl.py", log))
        write(log, "%s целей под надзором 0, вставших 0\n"
              % datetime.now().strftime(watch.STAMP))
        return home

    def dashboard_agent(self, home):
        """Носитель дашборда: launchd-агент на заглушку бинаря из подставного
        PATH, по той же причине, что и сторожок. Конфига ~/.devkit/dashboard.local
        стенд не кладёт, поэтому живость по /healthz доктор тут не меряет:
        сервер, который ни разу не стартовал, ещё не породил конфига."""
        home = Path(home)
        write(home / dashboard.PLIST[2:],
              dashboard.plist_text(self.bin / "dashboard",
                                   home / dashboard.LOG[2:]))
        return home

    def global_rules(self, home, dk=None):
        """Глобальная точка правил в подставном HOME, той же генерацией, что и --fix."""
        dk = Path(dk or self.dk)
        prof = harness.parse("p.toml", read(self.dk / "kit" / "harness" / "claude-code.toml"))
        with fake_home(home):
            text = rules.global_thin_text(prof, str(dk))
        return write(Path(home) / ".claude" / "CLAUDE.md", text)

    def dkctl_run(self, *args, **kw):
        kw.setdefault("home", self.home)
        kw.setdefault("path", self.cleanpath)
        env = dict(kw.pop("env", None) or {})
        env.setdefault("DEVKIT_NOTIFY_BACKEND", str(self.notify_stub))
        env.setdefault(update.BIN_ENV, str(self.dkbin))
        answers = kw.pop("answers", None)
        argv = [PY, str(kw.pop("dkctl", self.dkctl))] + list(args)
        if answers is None:
            return run(argv, env=env, **kw)
        return run_tty(argv, answers, env=env, **kw)

    def doctor(self, root, *args, **kw):
        return self.dkctl_run("doctor", *(list(args) + ["-C", str(root)]), **kw)

    def project(self, name, board=False):
        root = git_init(self.root / name)
        if board:
            write(root / "docs" / "TASKS.md", "# Задачи\n")
        return root

    def fingerprint(self):
        """Слепок копии devkit по содержимому: чем стенд был до теста.

        Считается по содержимому, а не по mtime: тест, вернувший файл на место
        перезаписью, стенд не менял. `.git` пропускается, его трогает не тест, а
        сам git (индекс, логи ссылок). `__pycache__` пропускается по той же
        причине: байткод пишет python при первом же импорте из подпроцесса, и на
        чекауте, где кеша ещё нет, сторож утечки краснел бы на исправном коде.
        """
        h = hashlib.sha1()
        skip = (".git", "__pycache__")
        for p in sorted(self.dk.rglob("*")):
            if set(p.relative_to(self.dk).parts) & set(skip):
                continue
            h.update(str(p.relative_to(self.dk)).encode("utf-8"))
            if p.is_symlink():
                h.update(b"->" + os.readlink(str(p)).encode("utf-8"))
            elif p.is_file():
                h.update(p.read_bytes())
        return h.hexdigest()

    def close(self):
        shutil.rmtree(str(self.root), ignore_errors=True)


class SandboxCase(unittest.TestCase):
    """Общий стенд на класс: копия devkit заводится один раз, а не на тест.

    Стенд дорогой, поэтому общий, и цена этому утечка состояния между тестами:
    один тест правит копию devkit, следующий видит чужую раскладку и краснеет
    через раз. Поэтому слепок копии сверяется до и после каждого теста, и
    невосстановленная правка называется сразу, а не всплывает в соседнем тесте.

    Класс, где шаги нарочно стоят друг на друге (цепочка вклейки правил), это
    объявляет полем CHAIN и за состояние отвечает сам.
    """

    CHAIN = False

    @classmethod
    def setUpClass(cls):
        cls.box = Sandbox()

    @classmethod
    def tearDownClass(cls):
        cls.box.close()

    def setUp(self):
        if not self.CHAIN:
            self._devkit_before = self.box.fingerprint()

    def tearDown(self):
        if not self.CHAIN:
            self.assertEqual(self.box.fingerprint(), self._devkit_before,
                             "тест оставил копию devkit изменённой, стенд течёт в соседние тесты")

    def assertIn_(self, needle, out, why):
        self.assertIn(needle, out, "%s: %s" % (why, out))

    def assertNotIn_(self, needle, out, why):
        self.assertNotIn(needle, out, "%s: %s" % (why, out))
