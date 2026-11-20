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


def _read_file(name):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, encoding="utf-8") as stream:
        raw = stream.read(65537)
    if len(raw) > 65536:
        raise ValueError("слишком большое состояние задачи")
    return json.loads(raw)


def read(home, root, task):
    data = _read_file(path(home, root, task))
    if (not isinstance(data, dict) or data.get("version") != 1 or
            data.get("root") != os.path.realpath(root) or
            data.get("task") != task.upper() or
            type(data.get("stopped")) is not bool):
        raise ValueError("чужое либо неизвестное состояние задачи")
    return data


def app_runs(home, root, tasks):
    """Возвращает рабочие запуски Codex для ограждения общих подъёмов."""
    home = Path.home() if home is None else Path(home)
    confirmed, legacy = {}, {}
    root = os.path.realpath(root)
    for task in set(tasks):
        try:
            data = read(home, root, task)
        except (OSError, ValueError):
            continue
        run = data.get("run")
        if not isinstance(run, dict) or not run.get("id"):
            continue
        if isinstance(run.get("appChat"), str) and run["appChat"]:
            confirmed[task] = dict(run, stopped=data["stopped"])
        elif type(run.get("pid")) is int and run["pid"] > 0:
            legacy[task] = dict(run, stopped=data["stopped"])
    if not legacy:
        return confirmed

    # После обновления дашборд перенесёт адрес чата в Run. До этого тик
    # узнаёт прежний запуск по обоим сохранённым экземплярам его реквизитов.
    for file in (home / ".devkit" / "chats").glob("as*.json"):
        try:
            with file.open(encoding="utf-8") as stream:
                chat = json.load(stream)
        except (OSError, ValueError):
            continue
        if not isinstance(chat, dict) or not isinstance(chat.get("task"), str):
            continue
        task = chat["task"]
        run = legacy.get(task)
        if (run and chat.get("protocol") == "codex" and chat.get("thread")
                and chat.get("taskRunSession") == file.stem
                and chat.get("taskRunRoot") == root
                and chat.get("taskRunID") == run["id"]
                and chat.get("taskRunPID") == run["pid"]
                and chat.get("harness") == run.get("harness")
                and chat.get("model") == run.get("model")):
            confirmed[task] = dict(run, appChat=file.stem)
    return confirmed


def terminal_app_roots(home):
    """Находит корни для передачи исчерпанных восстановлений дежурному."""
    roots = set()
    for name in (Path(home) / ".devkit" / "task-state").glob("*/*.json"):
        try:
            data = _read_file(name)
            if not isinstance(data, dict):
                continue
            root, task = data.get("root"), data.get("task")
            run = data.get("run")
            if (data.get("version") == 1 and data.get("stopped") is False
                    and isinstance(root, str) and os.path.isabs(root)
                    and isinstance(task, str) and path(home, root, task) == name
                    and isinstance(run, dict) and run.get("appChat")
                    and run.get("appRecoveryTerminal") is True):
                roots.add(root)
        except (OSError, ValueError):
            continue
    return roots


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
