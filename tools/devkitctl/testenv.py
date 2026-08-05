"""Стенд самопроверки devkitctl: копия devkit во временной директории и
подставной машинный контур вокруг неё.

На живом чекауте гонять нельзя: доктор судит по mtime исходников, сверяет
определения агентов с основным чекаутом и лезет в HOME, так что незакоммиченная
правка в ~/projects/devkit красила бы самопроверку на исправном коде. Копия не
под git, поэтому для доктора она и есть основной чекаут.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEVKIT_SRC = HERE.parent.parent
PY = sys.executable

# Системная часть PATH подставная: в ней только то, без чего не обойтись.
# Проверки вида «tmux не в PATH» иначе держались бы на том, чего нет в /usr/bin
# на этой машине, и на другой краснели бы при исправном коде.
SYS_TOOLS = ("git", "python3", "dirname", "mkdir", "chmod", "rm")
STUB_TOOLS = ("taskctl", "shipctl", "agentctl", "regcheck", "tmux")
STUB = "#!/bin/sh\nexit 0\n"

# Хуки харнеса в фикстуре машинного контура: чистый проект должен быть чист.
# Каждый хук стоит своей строкой, потому что проверки режут этот файл построчно,
# и после реза он обязан оставаться разбираемым.
NOTIFY = "python3 ~/projects/devkit/hooks/notify.py --hook claude-code"
SETTINGS = """{"permissions": {"allow": %s},
 "hooks": {"PostToolUse": [{"matcher": "Edit|Write|NotebookEdit", "hooks": [
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-symbols.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-memory.py --hook"},
  {"type": "command", "command": "python3 ~/projects/devkit/hooks/check-sensitive.py --hook"}
]}], "SessionStart": [{"hooks": [
  {"type": "command", "command": "sh ~/projects/devkit/hooks/quota-refresh.sh"}
]}], "Notification": [{"hooks": [
  {"type": "command", "command": "%s"}
]}], "Stop": [{"hooks": [
  {"type": "command", "command": "%s"}
]}], "SubagentStop": [{"hooks": [
  {"type": "command", "command": "%s"}
]}], "UserPromptSubmit": [{"hooks": [
  {"type": "command", "command": "%s"}
]}]}}
"""

# Заглушка taskctl, которая доску всё-таки заводит: настоящий бинарь в PATH
# самопроверки заменён на «exit 0», а без доски проверять нечего.
BOARD_TASKCTL = '''#!%s
import os
import sys

argv, root, prefix = sys.argv[1:], ".", "TP"
if argv[:1] == ["-C"]:
    root, argv = argv[1], argv[2:]
if "--prefix" in argv:
    prefix = argv[argv.index("--prefix") + 1]
if argv[:1] == ["init"]:
    os.makedirs(os.path.join(root, "docs", "tasks"), exist_ok=True)
    with open(os.path.join(root, "docs", "TASKS.md"), "w", encoding="utf-8") as f:
        f.write("# Задачи проекта (префикс %%s)\\n" %% prefix)
    print("доска создана")
''' % sys.executable


def run(args, cwd=None, env=None, path=None, home=None):
    """Прогон команды с подставным окружением. Отдаёт код и слитый вывод."""
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
    p = subprocess.run([str(a) for a in args], cwd=cwd and str(cwd), env=e,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    return p.returncode, p.stdout.decode("utf-8", "replace")


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


if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))
import context  # noqa: E402
import corp  # noqa: E402
import harness  # noqa: E402
import perms  # noqa: E402
import rules  # noqa: E402
import weigh  # noqa: E402


class Sandbox:
    """Копия devkit, подставной HOME и подставной PATH вокруг них."""

    def __init__(self, home=True):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-test-"))
        self.dk = self.root / "devkit"
        self.dk.mkdir()
        for d in ("tools", "kit", "hooks"):
            shutil.copytree(str(DEVKIT_SRC / d), str(self.dk / d))
        for f in ("RULES.md", "RULES.board.md"):
            shutil.copy(str(DEVKIT_SRC / f), str(self.dk / f))
        self.dkctl = self.dk / "tools" / "devkitctl" / "devkitctl.py"

        # Машинный контур подставной: бинари devkit и tmux заглушками в своём
        # PATH. Иначе проверки цеплялись бы за настоящую машину.
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for t in STUB_TOOLS:
            executable(self.bin / t)
        self.sys = self.root / "sys"
        self.sys.mkdir()
        self.missing_tools = []
        for t in SYS_TOOLS:
            found = shutil.which(t)
            if not found:
                self.missing_tools.append(t)
                continue
            os.symlink(found, str(self.sys / t))
        self.cleanpath = "%s:%s" % (self.bin, self.sys)
        # Чем слать уведомления, доктор спрашивает у самого уведомителя, а тот
        # смотрит платформу и PATH. Без подставного бэкенда проверка краснела бы
        # на машине без osascript или notify-send.
        self.notify_stub = self.root / "notify-stub"

        self.home = self.root / "home"
        if home:
            self.make_home(self.home)

    def make_home(self, home):
        """Машина с актуальной раскладкой devkit: на чистом проекте доктор молчит."""
        home = Path(home)
        (home / ".claude" / "agents").mkdir(parents=True)
        (home / ".claude" / "skills").mkdir(parents=True)
        (home / ".devkit" / "quota").mkdir(parents=True)
        allow = json.dumps(list(perms.MACHINE_ALLOW), ensure_ascii=False)
        write(home / ".claude" / "settings.json",
              SETTINGS % (allow, NOTIFY, NOTIFY, NOTIFY, NOTIFY))
        for f in (self.dk / "kit" / "agents").glob("*.md"):
            shutil.copy(str(f), str(home / ".claude" / "agents" / f.name))
        for d in (self.dk / "kit" / "skills").iterdir():
            if d.is_dir():
                shutil.copytree(str(d), str(home / ".claude" / "skills" / d.name))
        write(home / ".devkit" / "quota" / "claude-code.local",
              "taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n" % taken_at(0))
        self.global_rules(home)
        return home

    def global_rules(self, home, dk=None):
        """Глобальная точка правил в подставном HOME, той же генерацией, что и --fix."""
        dk = Path(dk or self.dk)
        prof = harness.parse("p.toml", read(self.dk / "kit" / "harness" / "claude-code.toml"))
        old = os.environ.get("HOME")
        os.environ["HOME"] = str(home)
        try:
            text = rules.global_thin_text(prof, str(dk))
        finally:
            if old is None:
                os.environ.pop("HOME", None)
            else:
                os.environ["HOME"] = old
        return write(Path(home) / ".claude" / "CLAUDE.md", text)

    def dkctl_run(self, *args, **kw):
        kw.setdefault("home", self.home)
        kw.setdefault("path", self.cleanpath)
        env = dict(kw.pop("env", None) or {})
        env.setdefault("DEVKIT_NOTIFY_BACKEND", str(self.notify_stub))
        return run([PY, str(kw.pop("dkctl", self.dkctl))] + list(args), env=env, **kw)

    def doctor(self, root, *args, **kw):
        return self.dkctl_run("doctor", *(list(args) + ["-C", str(root)]), **kw)

    def project(self, name, board=False):
        root = git_init(self.root / name)
        if board:
            write(root / "docs" / "TASKS.md", "# Задачи\n")
        return root

    def close(self):
        shutil.rmtree(str(self.root), ignore_errors=True)


class SandboxCase(unittest.TestCase):
    """Общий стенд на класс: копия devkit заводится один раз, а не на тест."""

    @classmethod
    def setUpClass(cls):
        cls.box = Sandbox()

    @classmethod
    def tearDownClass(cls):
        cls.box.close()

    def assertIn_(self, needle, out, why):
        self.assertIn(needle, out, "%s: %s" % (why, out))

    def assertNotIn_(self, needle, out, why):
        self.assertNotIn(needle, out, "%s: %s" % (why, out))
