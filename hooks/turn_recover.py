#!/usr/bin/env python3
"""Продолжение живого хода после обрыва, с отдельным отправителем."""
import contextlib
import fcntl
import glob
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import uuid

LIMIT = 3
PAUSE = 3
MESSAGE = "Продолжай работу с места обрыва связи. Учти уже выполненные действия."
ERROR = "Connection lost mid-response. The response above may be incomplete."


def state_path(session):
    return Path.home() / ".devkit" / "turn-recover" / (session + ".json")


@contextlib.contextmanager
def locked(session):
    path = state_path(session)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(str(path) + ".lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            data = json.loads(path.read_text())
        except (OSError, ValueError):
            data = {}
        yield data
        temp = path.with_suffix(".tmp")
        temp.write_text(json.dumps(data))
        temp.replace(path)


def peer(session):
    dirs = [str(Path.home() / ".claude" / "sessions")]
    cfg = os.environ.get("CLAUDE_CONFIG_DIR")
    if cfg:
        dirs.append(os.path.join(cfg, "sessions"))
    dirs += glob.glob(str(Path.home() / ".devkit" / "claude-*" / "sessions"))
    found = {}
    for directory in dirs:
        for name in glob.glob(os.path.join(directory, "*.json")):
            try:
                row = json.loads(Path(name).read_text())
                sid = row.get("sessionId", "")
                if not sid.startswith(session) or row.get("kind") != "interactive":
                    continue
                os.kill(int(row["pid"]), 0)
                row["config"] = str(Path(directory).parent)
                if row.get("updatedAt", 0) >= found.get(sid, {}).get("updatedAt", 0):
                    found[sid] = row
            except (OSError, ValueError, KeyError, TypeError):
                continue
    return next(iter(found.values())) if len(found) == 1 else None


def eligible(sess):
    if sess.agent or any(os.environ.get(k) for k in (
            "DEVKIT_RUN_DEPTH", "DEVKIT_NOTIFY_OFF", "DEVKIT_SILENT",
            "DEVKIT_GOAL_TMUX", "DEVKIT_GOAL_SHELL")):
        return None
    if not isinstance(sess.message, str):
        return None
    # Белый список относится ко всему сообщению, а не к цитате внутри ошибки.
    if not (re.fullmatch(r"(?:API Error: )?" + re.escape(ERROR), sess.message.strip())
            or re.fullmatch(r"API Error: Connection to the API was lost \(StreamTruncated\).*",
                            sess.message.strip())):
        return None
    return peer(sess.session)


def changed(sess):
    import hookio
    # Завершение StopFailure ещё дописывает служебные записи и ошибку ответа.
    # Новую работу доказывает реплика пользователя, а не размер транскрипта.
    last = None
    for line in hookio.tail_lines(sess.transcript):
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if row.get("type") == "user":
            last = row.get("uuid") or row.get("message")
    return last


def reset(sess):
    path = state_path(sess.session)
    if not path.exists():
        return
    with locked(sess.session) as state:
        state.pop("nonce", None)
        if sess.kind == "turn-done":
            state["tries"] = 0


def schedule(sess):
    row = eligible(sess)
    if row is None:
        return False
    with locked(sess.session) as state:
        if state.get("nonce"):
            if time.time() - state.get("pending_at", 0) < 30:
                return True
            state.pop("nonce", None)
            return False
        if state.get("tries", 0) >= LIMIT:
            return False
        nonce = uuid.uuid4().hex
        state.update(nonce=nonce, managed=True, pending_at=time.time())
        data = dict(sess=sess._asdict(), peer=row, nonce=nonce,
                    transcript=changed(sess), hook_pid=os.getpid())
        try:
            subprocess.Popen([sys.executable, __file__, json.dumps(data)],
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                             stderr=subprocess.DEVNULL, start_new_session=True,
                             close_fds=True)
        except OSError:
            state.pop("nonce", None)
            return False
    return True


def socket_send(row):
    try:
        cfg = json.loads((Path(row["config"]) / "settings.json").read_text())
    except (OSError, ValueError):
        return "unavailable"
    if cfg.get("crossSessionInbound") != "accept":
        return "unavailable"
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(5)
    try:
        sock.connect(row.get("messagingSocketPath") or "/tmp/cc-socks/%s.sock" % row["pid"])
    except OSError:
        sock.close()
        return "unavailable"
    try:
        sender = "uds:/tmp/devkit-turn-recover.sock"
        body = ('<cross-session-message from="%s" from-name="devkit" '
                'from-mode="prompting">\n%s\n</cross-session-message>') % (sender, MESSAGE)
        frame = dict(msgV=1, msg_id=str(uuid.uuid4()), type="user", priority="next",
                     message=dict(role="user", content=body), **{"from": sender})
        sock.sendall((json.dumps(frame) + "\n").encode())
        sock.shutdown(socket.SHUT_WR)
        while sock.recv(4096):
            pass
        return "socket"
    except OSError:
        # После connect даже частичная запись могла попасть в очередь.
        return "ambiguous"
    finally:
        sock.close()


def tmux_send(row):
    target = row.get("tmux", "").rsplit(".", 1)[-1]
    if not target:
        return "unavailable"
    # Непустой ввод человека не стирается и не дополняется.
    try:
        check = subprocess.run(["tmux", "capture-pane", "-p", "-t", target],
                               capture_output=True, text=True, timeout=3)
        lines = [s.strip() for s in check.stdout.splitlines() if s.strip()]
        prompts = [s for s in lines if s.startswith((">", "\u276f"))]
        if check.returncode or not prompts or prompts[-1] not in (">", "\u276f"):
            return "unavailable"
        put = subprocess.run(["tmux", "send-keys", "-t", target, "-l", MESSAGE], timeout=3)
        if put.returncode:
            return "ambiguous"
        subprocess.run(["tmux", "send-keys", "-t", target, "Enter"], timeout=3, check=True)
        return "tmux"
    except (OSError, subprocess.SubprocessError):
        return "ambiguous"


def work(data):
    import hookio
    import notify
    sess = hookio.Session(**data["sess"])
    # Отправитель не ждёт клиента, пока клиент ждёт завершения этого хука.
    for _ in range(100):
        try:
            os.kill(data["hook_pid"], 0)
        except OSError:
            break
        time.sleep(.1)
    else:
        raise RuntimeError("хук не завершился за 10 секунд")
    time.sleep(PAUSE)
    with locked(sess.session) as state:
        if state.get("nonce") != data["nonce"]:
            return
        state.pop("nonce", None)
        row = peer(sess.session)
        if row is None or row["pid"] != data["peer"]["pid"] or changed(sess) != data["transcript"]:
            notify.log(sess.session, "turn_recover", None, "пропуск: разговор уже изменился")
            return
        state["tries"] = state.get("tries", 0) + 1
        result = socket_send(row)
        if result == "unavailable":
            result = tmux_send(row)
        tries = state["tries"]
    if result in ("socket", "tmux"):
        notify.log(sess.session, "turn_recover", result,
                   "обрыв связи: продолжение %d/%d" % (tries, LIMIT))
    else:
        notify.deliver("Ход упал: продолжение не доставлено",
                       "Попытка %d/%d: %s. Продолжите в чате вручную." % (tries, LIMIT, result),
                       sess.session, "turn_failed")


if __name__ == "__main__":
    data = json.loads(sys.argv[1])
    try:
        work(data)
    except Exception as error:
        with locked(data["sess"]["session"]) as state:
            if state.get("nonce") == data["nonce"]:
                state.pop("nonce", None)
        import notify
        notify.deliver("Ход упал: ошибка продолжения", str(error),
                       data["sess"]["session"], "turn_failed")
