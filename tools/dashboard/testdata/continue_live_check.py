#!/usr/bin/env python3
"""Изолированный installed-сценарий проверки DK-1605.

Поднимает одноразовый дом: установленные dashboard, taskctl и agentctl,
копию kit и hooks из проверяемого дерева, синтетический проект demo с bare
origin, детерминированный носитель и настоящий tmux под личным сокетом.
Прогоняет четыре проверки терминальной дороги continue из c5faab6e5: живое
окно без peer, сломанный send-keys, мёртвый носитель и живая цель. Успех
это код выхода 0 без пропущенных проверок, при ошибке артефакты остаются
в каталоге, который сценарий печатает перед выходом.

Бинари берутся из DASHBOARD_ACCEPT_BIN, TASKCTL_ACCEPT_BIN и
AGENTCTL_ACCEPT_BIN, без переменных подходит command -v. Корень с kit и
hooks задаёт DEVKIT_ACCEPT_ROOT, по умолчанию это чекаут, где лежит сам
сценарий. Сборки внутри сценария нет.
"""
import datetime
import http.cookiejar
import json
import os
import re
import shutil
import shlex
import socket
import subprocess
import sys
import tempfile
import time
import traceback
import urllib.error
import urllib.request
import uuid

POLL_STEP = 0.3
CARRIER_ROLE = "носитель"

CARRIER_SRC = '''#!/usr/bin/env python3
"""Детерминированный носитель стенда DK-1605.

Роль идёт первым argv, дальше разбор как у клиента обвязки: --name, --model,
--resume, --permission-mode, --session-id и позиционный текст заказа.
Каждый вход оседает квитанцией в ~/receipts/<роль>.jsonl и репликой в
собственном транскрипте ~/.claude/projects/<имя-дерева>/<sid>.jsonl. Строку
реестра чатов носитель кладёт один раз на сессию. Без заказа носитель ждёт
строки со stdin: в tmux-сессии его кормит send-keys, каждая строка даёт
квитанцию и ответ в истории. Сетевых моделей нет.
"""
import datetime
import fcntl
import json
import os
import re
import select
import sys
import uuid

ROLE = sys.argv[1] if len(sys.argv) > 1 else "носитель"
ARGS = sys.argv[2:]
HOME = os.environ.get("HOME") or os.path.expanduser("~")
VALUE_FLAGS = ("--name", "--model", "--resume", "--permission-mode",
               "--session-id", "--effort")


def stamp():
    return datetime.datetime.now().strftime("%Y-%m-%dT%H:%M:%S")


def parse_args(args):
    vals = {"--name": "", "--model": "", "--resume": "", "--permission-mode": "",
            "--session-id": "", "--effort": ""}
    text = ""
    i = 0
    while i < len(args):
        a = args[i]
        if a in VALUE_FLAGS:
            vals[a] = args[i + 1] if i + 1 < len(args) else ""
            i += 2
        elif a.startswith("-") and len(a) > 1:
            i += 1
        else:
            text = a
            i += 1
    return vals, text


def sid_of(vals):
    return vals["--resume"] or vals["--session-id"] or vals["--name"] or "carrier"


def claude_dir_name(p):
    return re.sub(r"[^A-Za-z0-9-]", "-", p)


def append_jsonl(path, obj):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    obj = dict(obj, timestamp=datetime.datetime.now(datetime.timezone.utc)
               .strftime("%Y-%m-%dT%H:%M:%S.000Z"))
    with open(path, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj, ensure_ascii=False) + "\\n")


def receipt(vals, text, how):
    os.makedirs(HOME + "/receipts", exist_ok=True)
    rec = {
        "ts": stamp(),
        "role": ROLE,
        "sid": sid_of(vals),
        "task": os.environ.get("DEVKIT_TASK", "-"),
        "tmux": os.environ.get("DEVKIT_TMUX", "-"),
        "model": vals["--model"],
        "how": how,
        "nonce": text,
        "argv": ARGS,
    }
    with open(HOME + "/receipts/%s.jsonl" % ROLE, "a", encoding="utf-8") as f:
        f.write(json.dumps(rec, ensure_ascii=False) + "\\n")
    return rec


def say_into_transcript(vals, text):
    sid = sid_of(vals)
    tpath = HOME + "/.claude/projects/" + claude_dir_name(os.getcwd()) + "/" + sid + ".jsonl"
    append_jsonl(tpath, {"type": "user",
                         "message": {"role": "user", "content": text}})
    append_jsonl(tpath, {"type": "assistant",
                         "message": {"role": "assistant",
                                     "content": [{"type": "text",
                                                  "text": "ответ носителя: " + text}]}})
    return tpath


def write_registry(vals):
    sid = sid_of(vals)
    reg = HOME + "/.devkit/sessions.log"
    os.makedirs(os.path.dirname(reg), exist_ok=True)
    tpath = HOME + "/.claude/projects/" + claude_dir_name(os.getcwd()) + "/" + sid + ".jsonl"
    task = os.environ.get("DEVKIT_TASK") or "-"
    tmux = os.environ.get("DEVKIT_TMUX") or "-"
    line = ("%s сессия %s задача %s проект demo дерево %s транскрипт %s "
            "источник заказ повод %s tmux %s панель - носитель -\\n"
            % (stamp(), sid, task, os.getcwd(), tpath,
               "резюм чата" if vals["--resume"] else "startup", tmux))
    with open(reg, "a+", encoding="utf-8") as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        try:
            f.seek(0, 2)
            f.write(line)
        finally:
            fcntl.flock(f, fcntl.LOCK_UN)


def main():
    vals, text = parse_args(ARGS)
    if not vals["--resume"] and not vals["--session-id"]:
        vals["--session-id"] = str(uuid.uuid4())
    write_registry(vals)
    receipt(vals, "synthetic carrier ready", "startup")
    say_into_transcript(vals, "synthetic carrier ready")
    if text:
        receipt(vals, text, "argv")
        say_into_transcript(vals, text)
    if os.environ.get("DEVKIT_CARRIER_HEADLESS") == "1" or "-p" in ARGS:
        return
    while True:
        readable, _, _ = select.select([sys.stdin], [], [], 0.25)
        if readable:
            line = sys.stdin.readline()
            if not line:
                return
            if line.strip():
                receipt(vals, line.strip(), "stdin")
                say_into_transcript(vals, line.strip())


if __name__ == "__main__":
    main()
'''

# Профиль подписки стенда: каркас claude-code, клиент заменён носителем.
# Секция [hooks] без config закрывает hooks_gap пустым значением, личный
# settings.json и doctor реальной машины не нужны.
PROFILE_SRC = '''# Профиль стенда DK-1605: детерминированный носитель вместо клиента.
[detect]
env = "CLAUDECODE"
value = "1"
bin = "dk1605-carrier"

[delegate]
mode = "native"
command = ["dk1605-carrier", "-p", "{prompt}", "--model", "{model}", "--effort", "{effort}", "--session-id", "{session}"]
agents_dir = "~/.claude/agents"
agents_format = "claude-code"
map_mini = "детерминированный"
map_base = "детерминированный"
map_pro = "детерминированный"
map_max = "детерминированный"

[hooks]
protocol = "claude-code"
events = ["tool-done"]

[head]
client = ["dk1605-carrier", "--permission-mode", "auto"]
model = ["--model", "{model}"]
session = ["--session-id", "{session}"]
name = ["--name", "{name}"]
resume = ["dk1605-carrier", "--resume", "{session}", "--model", "{model}"]
turn_end = "turn-mark"

[quota]

[rules]
mode = "import"
file = "CLAUDE.md"
import_line = "@{path}"
'''


def run(cmd, cwd=None, env=None, timeout=30, check=True):
    r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True,
                       timeout=timeout)
    if check and r.returncode != 0:
        raise AssertionError("команда не прошла: %s\n%s\n%s"
                             % (cmd, r.stdout[-1500:], r.stderr[-1500:]))
    return r


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def nonce(label):
    return "dk1605-%s-%s" % (label, uuid.uuid4().hex[:10])


def resolve_bin(env_name, name):
    p = os.environ.get(env_name)
    if p:
        p = os.path.abspath(p)
        if not (os.path.isfile(p) and os.access(p, os.X_OK)):
            raise AssertionError("%s указывает на неисполняемый файл: %s"
                                 % (env_name, p))
        return p
    w = shutil.which(name)
    if not w:
        raise AssertionError("в PATH нет %s, задайте %s" % (name, env_name))
    return w


def accept_root():
    root = os.environ.get("DEVKIT_ACCEPT_ROOT") or os.path.abspath(
        os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
    for part in ("kit", "hooks"):
        if not os.path.isdir(os.path.join(root, part)):
            raise AssertionError("в корне %s нет каталога %s, задайте DEVKIT_ACCEPT_ROOT"
                                 % (root, part))
    return root


def write_text(path, text, mode=None):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    if mode is not None:
        os.chmod(path, mode)


class Stand:
    def __init__(self):
        self.work = os.path.realpath(tempfile.mkdtemp(prefix="dk1605-"))
        self.home = os.path.join(self.work, "home")
        self.bin = os.path.join(self.work, "bin")
        self.devkit = os.path.join(self.work, "devkit")
        self.config_dir = os.path.join(self.work, "config-dir")
        self.proj = os.path.join(self.home, "projects", "demo")
        self.origin = os.path.join(self.home, "projects", "demo-origin")
        self.sock = "dk1605-" + uuid.uuid4().hex[:8]
        self.shim_log = os.path.join(self.work, "tmux-shim.log")
        self.shim_break = os.path.join(self.work, "tmux-break.flag")
        self.server_log = os.path.join(self.work, "server.log")
        self.run_log = []
        self.server = None
        self.server_fd = None
        self.port = free_port()
        self.base = "http://127.0.0.1:%d" % self.port
        self.dashboard_bin = resolve_bin("DASHBOARD_ACCEPT_BIN", "dashboard")
        self.taskctl_bin = resolve_bin("TASKCTL_ACCEPT_BIN", "taskctl")
        self.agentctl_bin = resolve_bin("AGENTCTL_ACCEPT_BIN", "agentctl")
        self.tmux_bin = shutil.which("tmux")
        if not self.tmux_bin:
            raise AssertionError("в PATH нет tmux, стенду нужен настоящий tmux")
        self.root = accept_root()
        jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(jar))

    def env_base(self):
        e = {k: v for k, v in os.environ.items()
             if not k.startswith(("DEVKIT_", "CLAUDE_", "CODEX_", "ANTHROPIC_"))
             and k not in ("CLAUDECODE", "AI_AGENT", "TMUX", "TMUX_PANE", "GIT_DIR", "GIT_WORK_TREE",
                           "GIT_INDEX_FILE", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM")}
        e["HOME"] = self.home
        e["DEVKIT_HOME"] = self.devkit
        e["PATH"] = self.bin + os.pathsep + e.get("PATH", "")
        e["GOWORK"] = "off"
        e["DEVKIT_NOTIFY_OFF"] = "1"
        return e

    def log(self, entry):
        self.run_log.append(entry)
        with open(os.path.join(self.work, "run-log.json"), "w",
                  encoding="utf-8") as f:
            json.dump(self.run_log, f, ensure_ascii=False, indent=1)

    def setup(self):
        for d in (self.home, self.bin, self.config_dir):
            os.makedirs(d, exist_ok=True)
        self.log({"step": "дом", "work": self.work, "port": self.port,
                  "sock": self.sock})
        for tool in (self.dashboard_bin, self.taskctl_bin, self.agentctl_bin):
            r = run([tool, "--version"], timeout=20, check=False)
            self.log({"tool": tool, "version": r.stdout.strip()})

        # DEVKIT_HOME: копия kit и hooks из проверяемого дерева.
        shutil.copytree(os.path.join(self.root, "kit"),
                        os.path.join(self.devkit, "kit"))
        shutil.copytree(os.path.join(self.root, "hooks"),
                        os.path.join(self.devkit, "hooks"))
        write_text(os.path.join(self.devkit, "kit", "harness", "stand-1605.toml"),
                   PROFILE_SRC)

        # Обвязка стенда: только синтетический профиль.
        harness = (
            'default = "stand-1605"\n'
            'enabled = ["stand-1605"]\n'
            'auto = ["stand-1605"]\n\n'
            '[stand-1605]\n'
            'env = ["CONFIG_DIR=%s"]\n'
            'mini = "детерминированный"\n'
            'base = "детерминированный"\n'
            'pro = "детерминированный"\n'
            'max = "детерминированный"\n'
            'bin = "%s"\n' % (self.config_dir, os.path.join(self.bin, "dk1605-carrier")))
        write_text(os.path.join(self.home, ".devkit", "harness.local"), harness)

        # Ключ принадлежит только одноразовому серверу стенда.
        login_key = uuid.uuid4().hex + uuid.uuid4().hex
        write_text(os.path.join(self.home, ".devkit", "dashboard.local"),
                   "root = %s\nport = %d\ntoken = %s\n"
                   % (os.path.join(self.home, "projects"), self.port, login_key), mode=0o600)
        write_text(os.path.join(self.home, ".devkit", "goals", "XR-100.watch"),
                   "goal = XR-100\nroot = %s\n" % self.proj)

        # Носитель и обёртки в PATH стенда.
        write_text(os.path.join(self.bin, "carrier-src.py"), CARRIER_SRC)
        write_text(os.path.join(self.bin, "dk1605-carrier"),
                   '#!/bin/sh\nexec python3 "%s" %s "$@"\n'
                   % (os.path.join(self.bin, "carrier-src.py"), CARRIER_ROLE),
                   mode=0o755)
        shim = (
            '#!/bin/sh\n'
            '# Обёртка стенда DK-1605: журнал вызовов, кнопка ломает send-keys,\n'
            '# остальное уходит в настоящий tmux под личным сокетом.\n'
            'echo "$@" >> "%s"\n'
            'if [ -f "%s" ] && [ "$1" = "send-keys" ]; then\n'
            '  echo "dk1605 shim: send-keys broken by stand button" >&2\n'
            '  exit 1\n'
            'fi\n'
            'exec "%s" -L "%s" -f /dev/null "$@"\n'
            % (self.shim_log, self.shim_break, self.tmux_bin, self.sock))
        write_text(os.path.join(self.bin, "tmux"), shim, mode=0o755)
        # Точные байты dashboard помещаются рядом с адаптерами утилит:
        # запуск дашборда берёт машинный HOME из uid, адаптер возвращает
        # временный HOME перед вызовом настоящей утилиты без подмены ответа.
        shutil.copy2(self.dashboard_bin, os.path.join(self.bin, "dashboard"))
        for name, target in (("taskctl", self.taskctl_bin),
                             ("agentctl", self.agentctl_bin)):
            adapter = '#!/bin/sh\nexport HOME=%s\nexport DEVKIT_HOME=%s\nif [ "$1" = exec ] && [ "$3" != stand-1605 ]; then exit 91; fi\nexec %s "$@"\n' % (
                shlex.quote(self.home), shlex.quote(self.devkit), shlex.quote(target))
            write_text(os.path.join(self.bin, name), adapter, mode=0o755)

        # Синтетический проект demo: bare origin и доска XR.
        env = self.env_base()
        for args in (("user.name", "dk1605"),
                     ("user.email", "dk1605@stand.invalid"),
                     ("init.defaultBranch", "main")):
            run(["git", "config", "--global", args[0], args[1]], env=env,
                timeout=15)
        run(["git", "init", "--bare", "-b", "main", self.origin], env=env,
            timeout=20)
        run(["git", "init", "-b", "main", self.proj], env=env, timeout=20)
        run(["git", "-C", self.proj, "remote", "add", "origin", self.origin],
            env=env, timeout=15)
        run([self.taskctl_bin, "init", "--prefix", "XR", "--name", "demo"],
            cwd=self.proj, env=env, timeout=40)
        body_task = os.path.join(self.work, "body-task.md")
        body_goal = os.path.join(self.work, "body-goal.md")
        task_body = '## Что происходит\n\nСинтетическая задача изолированного стенда.\n\n## Чего хотим\n\nПроверить продолжение единственного носителя.\n\n## DoD\n\n- Продолжение сохраняет единственный процесс разговора.\n\n## Ранг\n\n- Серьёзность 0: синтетический стенд.\n- Ценность 1: проверка воспроизведения.\n- Неопределённость 1: фиксированный сценарий.\n- Поправка на баг 0: синтетическая задача.\n- Рычаг 0: изолированная проверка.\n'
        write_text(body_task, task_body)
        write_text(body_goal, task_body)
        run([self.taskctl_bin, "add", "--id", "XR-010", "--type", "task",
             "--title", "Проверка продолжения", "--rank", "0+1+1+0+0",
             "--cost", "S", "--accept", "agent", "--body-file", body_task],
            cwd=self.proj, env=env, timeout=40)
        run([self.taskctl_bin, "add", "--id", "XR-100",
             "--title", "Цель: стенд продолжения", "--rank", "0+1+1+0+0",
             "--accept", "agent", "--body-file", body_goal],
            cwd=self.proj, env=env, timeout=40)
        run(["git", "-C", self.proj, "add", "-A"], env=env, timeout=20)
        run(["git", "-C", self.proj, "commit", "-m", "стенд: доска demo"],
            env=env, timeout=20)
        run(["git", "-C", self.proj, "push", "-u", "origin", "main"], env=env,
            timeout=30)

        self.server_fd = open(self.server_log, "wb")
        self.server = subprocess.Popen(
            [os.path.join(self.bin, "dashboard"), "serve"],
            env=self.env_base(), cwd=os.path.join(self.home, "projects"),
            stdout=self.server_fd, stderr=subprocess.STDOUT,
            start_new_session=True)
        self.wait_http()
        code, _ = self.api("POST", "/api/login", {"token": login_key})
        login_key = None
        _, view = self.api("GET", "/api/harnesses")
        assert [h["name"] for h in view.get("harnesses", [])] == ["stand-1605"], view

    def wait_http(self, seconds=40):
        end = time.time() + seconds
        while time.time() < end:
            if self.server.poll() is not None:
                raise AssertionError("dashboard serve упал, лог: %s"
                                     % self.server_log)
            try:
                socket.create_connection(("127.0.0.1", self.port),
                                         timeout=1).close()
                return
            except OSError:
                time.sleep(POLL_STEP)
        raise AssertionError("dashboard serve не открыл порт %d, лог: %s"
                             % (self.port, self.server_log))

    def api(self, method, path, data=None, expect=200):
        body = json.dumps(data, ensure_ascii=False).encode() if data is not None else None
        req = urllib.request.Request(
            self.base + path, data=body, method=method,
            headers={"Content-Type": "application/json", "Origin": self.base})
        try:
            r = self.opener.open(req, timeout=30)
            code, raw = r.status, r.read().decode()
        except urllib.error.HTTPError as e:
            code, raw = e.code, e.read().decode()
        try:
            v = json.loads(raw)
        except ValueError:
            v = raw
        self.log({"method": method, "path": path, "status": code,
                  "body": "<login>" if path == "/api/login" else data, "response": str(v)[:2000]})
        if expect is not None and code != expect:
            raise AssertionError("%s %s: ждали %s, получили %s %s"
                                 % (method, path, expect, code, raw[:800]))
        return code, v

    def tmux(self, *args, check=True):
        return run([self.tmux_bin, "-L", self.sock, "-f", "/dev/null"] + list(args),
                   env=self.env_base(), check=check, timeout=15)

    def start_carrier(self, task):
        sid = str(uuid.uuid4())
        window = "chat-" + task + "-1"
        command = ["env", "DEVKIT_TASK=" + task, "DEVKIT_TMUX=" + window,
                   sys.executable, os.path.join(self.bin, "carrier-src.py"),
                   CARRIER_ROLE, "--resume", sid]
        self.tmux("new-session", "-d", "-s", window, "-c", self.proj,
                  shlex.join(command))
        self.wait_for(lambda: self.starts(sid) == 1, 20,
                      "носитель не создал стартовую квитанцию")
        self.api("POST", "/api/projects/demo/tasks/%s/chat/bind" % task,
                 {"chat": sid})
        _, current = self.api("GET", "/api/projects/demo/tasks/%s/chat" % task)
        assert current["chat"] == sid, current
        return sid

    def has_session(self, name):
        return self.tmux("has-session", "-t", "=" + name, check=False).returncode == 0

    def sessions(self):
        r = self.tmux("list-sessions", "-F", "#{session_name}", check=False)
        if r.returncode != 0:
            return []
        return [x for x in r.stdout.splitlines() if x.strip()]

    def receipts(self):
        out = []
        rd = os.path.join(self.home, "receipts")
        if not os.path.isdir(rd):
            return out
        for name in sorted(os.listdir(rd)):
            if not name.endswith(".jsonl"):
                continue
            with open(os.path.join(rd, name), encoding="utf-8") as f:
                for line in f:
                    try:
                        out.append(json.loads(line))
                    except ValueError:
                        pass
        return out

    def starts(self, sid=None):
        return len([r for r in self.receipts()
                    if r.get("how") == "startup"
                    and (sid is None or r.get("sid") == sid)])

    def journal_new_sessions(self):
        if not os.path.isfile(self.shim_log):
            return 0
        with open(self.shim_log, encoding="utf-8") as f:
            return len([x for x in f.read().splitlines() if "new-session" in x])

    def seed_transcript(self, sid):
        d = re.sub(r"[^A-Za-z0-9-]", "-", self.proj)
        path = os.path.join(self.home, ".claude", "projects", d, sid + ".jsonl")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime(
            "%Y-%m-%dT%H:%M:%S.000Z")
        rows = [
            {"type": "user", "timestamp": stamp,
             "message": {"role": "user", "content": "посев транскрипта стенда"}},
            {"type": "assistant", "timestamp": stamp,
             "message": {"role": "assistant", "content": [{"type": "text",
                                                          "text": "посев стенда ok"}]}},
        ]
        with open(path, "a", encoding="utf-8") as f:
            for row in rows:
                f.write(json.dumps(row, ensure_ascii=False) + "\n")

    def wait_for(self, pred, seconds, what):
        end = time.time() + seconds
        while time.time() < end:
            if pred():
                return
            time.sleep(POLL_STEP)
        raise AssertionError("истёк срок ожидания: %s" % what)

    def teardown(self, ok):
        if self.server is not None:
            try:
                os.killpg(os.getpgid(self.server.pid), 15)
                self.server.wait(timeout=5)
            except Exception:
                try:
                    os.killpg(os.getpgid(self.server.pid), 9)
                except Exception:
                    pass
        if self.server_fd is not None:
            self.server_fd.close()
        if self.tmux_bin:
            run([self.tmux_bin, "-L", self.sock, "kill-server"],
                check=False, timeout=15)
        if ok:
            shutil.rmtree(self.work, ignore_errors=True)


def phase_live_task(s):
    """Живое окно без peer получает continue в тот же SID без второго процесса."""
    sid = s.start_carrier("XR-010")
    s.wait_for(lambda: s.starts(sid) >= 1, 40,
               "живое окно задачи не поднялось, лог: %s" % s.server_log)
    s.wait_for(lambda: s.has_session("chat-XR-010-1"), 15,
               "нет окна chat-XR-010-1")
    before = s.starts()
    _, v = s.api("POST", "/api/projects/demo/tasks/XR-010/continue")
    assert v.get("way") == "send-keys", v
    s.wait_for(lambda: any("Продолжай работу по XR-010" in r.get("nonce", "")
                           for r in s.receipts() if r.get("how") == "stdin"),
               25, "реплика continue не дошла по send-keys")
    assert s.starts() == before, "поднялся второй носитель: %d было, %d стало" \
        % (before, s.starts())
    assert not s.has_session("chat-XR-010-2"), "появилось второе окно задачи"
    return sid


def phase_break_send_keys(s, sid):
    """Shim ломает send-keys при живом окне, сервер отвечает 502 без нового носителя."""
    before_starts = s.starts()
    before_new = s.journal_new_sessions()
    open(s.shim_break, "w").close()
    try:
        code, v = s.api("POST", "/api/projects/demo/tasks/XR-010/continue",
                        expect=None)
        assert code == 502, (code, v)
        assert "не подалось" in str(v), v
        assert s.starts() == before_starts, "при отказе поднялся новый носитель"
        assert s.journal_new_sessions() == before_new, \
            "при отказе появился новый tmux-процесс"
    finally:
        os.remove(s.shim_break)


def phase_dead_carrier(s, sid):
    """Уничтоженный носитель поднимается одним resume того же SID."""
    s.tmux("kill-session", "-t", "=chat-XR-010-1")
    s.wait_for(lambda: not s.has_session("chat-XR-010-1"), 15,
               "окно задачи не умерло")
    before_starts = s.starts(sid)
    before_new = s.journal_new_sessions()
    _, v = s.api("POST", "/api/projects/demo/tasks/XR-010/continue")
    assert v.get("way") == "resume", v
    s.wait_for(lambda: s.starts(sid) == before_starts + 1, 40,
               "носитель не поднялся повторно, лог: %s" % s.server_log)
    assert s.journal_new_sessions() == before_new + 1, \
        "подъём пошёл больше одного раза"
    assert s.starts(sid) == before_starts + 1, "появился лишний носитель SID"


def phase_live_goal(s):
    """Живая цель отвечает way=live без подгоняющей реплики."""
    sid = s.start_carrier("XR-100")
    s.wait_for(lambda: s.starts(sid) >= 1, 40,
               "живое окно цели не поднялось, лог: %s" % s.server_log)
    before_starts = s.starts()
    before_new = s.journal_new_sessions()
    _, v = s.api("POST", "/api/projects/demo/tasks/XR-100/continue")
    assert v.get("way") == "live", v
    end = time.time() + 3
    while time.time() < end:
        assert s.starts() == before_starts, "цель подняла нового носителя"
        assert s.journal_new_sessions() == before_new, "цель открыла новое окно"
        assert not any("Продолжай цель" in r.get("nonce", "")
                       for r in s.receipts()), "ушла подгоняющая реплика"
        time.sleep(POLL_STEP)


def step(name, fn):
    v = fn()
    print(name + ": PASS", flush=True)
    return v


def main():
    s = Stand()
    ok = False
    try:
        s.setup()
        sid = step("живое окно без peer", lambda: phase_live_task(s))
        step("сломанный send-keys", lambda: phase_break_send_keys(s, sid))
        step("мёртвый носитель", lambda: phase_dead_carrier(s, sid))
        step("живая цель", lambda: phase_live_goal(s))
        ok = True
        print("OK: DK-1605, проверок 4, отказов и пропусков нет")
        return 0
    except Exception:
        traceback.print_exc()
        print("FAIL: артефакты стенда в %s" % s.work)
        return 1
    finally:
        s.teardown(ok)


if __name__ == "__main__":
    sys.exit(main())
