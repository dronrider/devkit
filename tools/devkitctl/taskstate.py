"""Устойчивое состояние задачи, общий формат с internal/taskstate."""
import contextlib
import fcntl
import hashlib
import json
import os
import re
import tempfile
import time
from pathlib import Path


def path(home, root, task):
    task = task.upper().strip()
    if not home or not re.fullmatch(r"[A-Z][A-Z0-9]*-[0-9]+", task):
        raise ValueError("нет дома либо неверный ID задачи")
    canonical = os.path.realpath(root)
    key = hashlib.sha256(canonical.encode()).hexdigest()
    return Path(home) / ".devkit" / "task-state" / key / (task + ".json")


def read(home, root, task):
    name = path(home, root, task)
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, encoding="utf-8") as stream:
        raw = stream.read(65537)
    if len(raw) > 65536:
        raise ValueError("слишком большое состояние задачи")
    data = json.loads(raw)
    if (not isinstance(data, dict) or data.get("version") != 1 or
            data.get("root") != os.path.realpath(root) or
            data.get("task") != task.upper() or
            type(data.get("stopped")) is not bool):
        raise ValueError("чужое либо неизвестное состояние задачи")
    return data


@contextlib.contextmanager
def locked(home, root, task, timeout=30):
    name = path(home, root, task)
    fd = os.open(str(name) + ".lock", os.O_RDWR | os.O_NOFOLLOW)
    with os.fdopen(fd, "r+") as lock:
        deadline = time.monotonic() + max(0, timeout)
        while True:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise TimeoutError("замок состояния задачи занят")
                time.sleep(min(0.01, max(0, deadline - time.monotonic())))
        yield read(home, root, task)


def save(home, root, task, data):
    name = path(home, root, task)
    fd, tmp = tempfile.mkstemp(prefix=".state-", dir=name.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(data, stream, ensure_ascii=False, allow_nan=False)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(tmp, name)
        directory = os.open(name.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        try:
            os.unlink(tmp)
        except FileNotFoundError:
            pass


def acknowledge(home, root, task, run_id, pid):
    with locked(home, root, task) as data:
        run = data.get("run") or {}
        if data["stopped"] or not run_id or run.get("id") != run_id:
            raise ValueError("остановка либо другой исходный запуск")
        run["pid"] = pid
        run["group"] = os.getpgid(pid)
        save(home, root, task, data)


def identity(home, root, task, pid):
    data = read(home, root, task)
    run = data.get("run") or {}
    if data["stopped"] or run.get("pid") != pid or not run.get("id"):
        return None
    return dict(run)


def progress(home, root, task, run_id, attempt, pid, session):
    if not attempt:
        return
    if not re.fullmatch(r"RV-[0-9a-f]{32}", attempt):
        raise ValueError("неверная метка восстановления")
    with locked(home, root, task) as data:
        run = data.get("run") or {}
        if (data["stopped"] or run.get("id") != run_id or
                run.get("pid") != pid or run.get("attempt") != attempt or not session):
            raise ValueError("ход другого либо остановленного запуска")
        from datetime import datetime
        journal = os.environ.get("DEVKIT_RECOVERY_LOG") or str(Path(home) / ".devkit" / "recovery.jsonl")
        directory = Path(journal + ".proofs")
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        target = directory / (attempt + ".json")
        proof = {"attempt": attempt, "root": os.path.realpath(root), "task": task,
                 "pid": pid, "run_id": run_id, "session": session,
                 "stage": "turn", "when": datetime.now().strftime("%Y-%m-%dT%H:%M:%S")}
        fd, tmp = tempfile.mkstemp(prefix=".proof-", dir=directory)
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as stream:
                json.dump(proof, stream)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(tmp, target)
            dfd = os.open(directory, os.O_RDONLY)
            try:
                os.fsync(dfd)
            finally:
                os.close(dfd)
        finally:
            try:
                os.unlink(tmp)
            except FileNotFoundError:
                pass
