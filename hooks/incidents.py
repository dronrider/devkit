#!/usr/bin/env python3
"""Журнал инцидентов конвейера задачи (DK-1415).

Нештатная остановка конвейера это событие с записью: устойчивый код причины,
диагностика и ссылки на журналы переживают перезапуск, и по ним считается
сводка остановок. Запись дополняет повторное обнаружение той же остановки, а
закрывает её исход разбора дежурного: вопрос человеку, черновик, возобновление
либо потолок попыток. Штатные завершения, ожидания предусмотренных событий и
остановки по команде человека записей не дают.

Модуль общий для четверых: оболочка конвейера (kit/skills/board-task/task-run.py)
пишет остановку, которую увидела изнутри, тик (tools/devkitctl/lift.py)
находит погибший процесс по мёртвому замку, сторожок цикла
(tools/devkitctl/watch.py) с DK-1460 кладёт сюда же оперативные эскалации
дежурному, дежурный (tools/devkitctl/duty.py)
читает открытые записи и закрывает их исходом. Формат один, и грузится модуль
по пути, как hooks/chat-in.py.

Журнал это строки JSON в ~/.devkit/incidents.jsonl. Перезапись строчная:
запись находит себя по ID, каталог подменяется ключом DEVKIT_INCIDENTS_LOG,
поэтому стенд разводит журнал мимо машинного. Пара чтение-запись идёт под
файловым замком: писатели трое и из разных процессов, и одновременная
перезапись от своего снимка теряла чужую строку. Чувствительного в записи
нет: только ID, коды, пути журналов и слова диагностики, без адресов
и доступов.
"""
import fcntl
import json
import os
import tempfile
import time
from contextlib import contextmanager
from datetime import datetime

# Полный путь журнала для стенда. Настоящий общий на машину, и самопроверке
# в нём не место.
LOG_ENV = "DEVKIT_INCIDENTS_LOG"
LOG_NAME = "incidents.jsonl"
STAMP = "%Y-%m-%dT%H:%M:%S"

# Сколько строк о попытках дежурного держит запись. Потолок пробок держит
# duty.py, а тут режется только хвост, чтобы запись не росла без меры.
NOTES_CAP = 12

# Устойчивые коды причин. Список тот же, что перечень путей выхода в задаче
# DK-1415: воронка, исчерпание проходов, неожиданный выход головы, недошедший
# заказ, тишина головы, ошибка конвейера и исчезновение процесса.
IDLE_RUNS = "воронка"
PASSES_OUT = "проходы исчерпаны"
HEAD_EXIT = "голова вышла"
ORDER_LOST = "заказ не дошёл"
HEAD_SILENT = "тишина головы"
BROKEN = "ошибка конвейера"
PROC_GONE = "процесс исчез"

# Исходы разбора дежурного. Пустой исход значит открытый инцидент: работа
# приостановлена, дежурного ждёт или поднимает тик.
ASK_HUMAN = "вопрос человеку"
DRAFT_MADE = "черновик"
RESUMED = "возобновлено"
TRIES_OUT = "потолок попыток"

# Повтор той же остановки (та же задача и код причины) дополняет последнюю
# её запись, а не заводит новую: счёт обнаружений растёт, пока остановка
# стоит, и дежурного повтор не поднимает. Новая запись заводится лишь после
# исхода «возобновлено»: возобновлённый конвейер упал снова, и это уже другая
# остановка, какая бы причина у неё ни была.
RESUMED_OUTCOME = RESUMED


def log_path(home=None):
    """Путь журнала: ключ стенда, иначе <дом>/.devkit/incidents.jsonl."""
    given = (os.environ.get(LOG_ENV) or "").strip()
    if given:
        return given
    base = os.path.expanduser("~") if home is None else str(home)
    return os.path.join(base, ".devkit", LOG_NAME)


def read(home=None):
    """Записи журнала, старые раньше. Битые строки пропускаются: журнал
    переживает перезапуск писателя, и недописанный хвост не должен ронять
    сводку целиком."""
    try:
        with open(log_path(home), encoding="utf-8", errors="replace") as f:
            text = f.read()
    except OSError:
        return []
    out = []
    for ln in text.splitlines():
        try:
            rec = json.loads(ln)
        except ValueError:
            continue
        if isinstance(rec, dict) and rec.get("id"):
            out.append(rec)
    return out


def _write(records, home=None):
    path = log_path(home)
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            for rec in records:
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
    except OSError:
        return False
    return True


@contextmanager
def _guard(home=None):
    """Замок на пару чтение-запись журнала. Держится отдельным файлом рядом:
    сам журнал перезаписывается целиком, и замок на нём жил бы на дескрипторе,
    который писатели открывают заново. Недоступный замок не блокирует запись:
    её исход и так виден возвратом report."""
    path = log_path(home) + ".lock"
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        f = open(path, "a", encoding="utf-8")
    except OSError:
        yield
        return
    try:
        fcntl.flock(f.fileno(), fcntl.LOCK_EX)
        yield
    finally:
        f.close()


def _stamp(now=None):
    return (now or datetime.now()).strftime(STAMP)


def report(task, reason, why, run="", root="", logs=(), home=None, now=None, identity=None):
    """Записать остановку и вернуть ID инцидента. Повтор той же остановки
    (та же задача, тот же код причины, тот же корень) дополняет последнюю её
    запись счётчиком и возвращает её ID, каким бы исходом она ни кончилась,
    кроме возобновления: после него повтор значит новую остановку. Корень
    входит в ключ наравне с задачей: один и тот же ID живёт в основном
    чекауте и в дереве ветки, и склейка писала бы чужому корню. Пустой
    возврат значит, что журнал не записался: остановка названа вслух
    журналами утилит, и молча терять её повтором нельзя."""
    when = _stamp(now)
    with _guard(home):
        records = read(home)
        for rec in reversed(records):
            if rec.get("task") != task or rec.get("reason") != reason:
                continue
            if str(rec.get("root") or "") != str(root or ""):
                continue
            if identity and (rec.get("identity") or {}).get("id") != identity.get("id"):
                break
            if rec.get("outcome") == RESUMED_OUTCOME:
                break
            rec["seen"] = int(rec.get("seen") or 1) + 1
            return rec["id"] if _write(records, home) else ""
        day = when[:10].replace("-", "")
        alike = sum(1 for rec in records
                    if str(rec.get("id", "")).startswith("INC-%s" % day))
        ident = "INC-%s-%d" % (day, alike + 1)
        records.append({
            "id": ident, "task": task, "run": run, "when": when,
            "reason": reason, "why": why, "root": root, "logs": list(logs),
            "seen": 1, "notes": [], "outcome": "",
            **({"identity": dict(identity)} if identity else {}),
        })
        return ident if _write(records, home) else ""


def open_for(task, home=None):
    """Открытая запись задачи либо None. Открытой считается запись без
    исхода: работа по ней приостановлена, и автоматический подъём строки
    стоит до разбора дежурным."""
    for rec in reversed(read(home)):
        if rec.get("task") == task and not rec.get("outcome"):
            return rec
    return None


def open_list(home=None):
    """Открытые записи журнала, старые раньше."""
    return [rec for rec in read(home) if not rec.get("outcome")]


def _touch(ident, home, change):
    """Найти запись по ID и переписать её change(rec). Возврат это найденная
    запись либо None."""
    with _guard(home):
        records = read(home)
        for rec in records:
            if rec.get("id") != ident:
                continue
            change(rec)
            _write(records, home)
            return rec
    return None


def note(ident, line, home=None):
    """Дополнить запись строкой о попытке дежурного. Хвост режется до
    NOTES_CAP строк: потолок попыток держит duty.py, а не размер записи."""
    def change(rec):
        notes = list(rec.get("notes") or [])
        notes.append("%s %s" % (_stamp(), line))
        rec["notes"] = notes[-NOTES_CAP:]
    return _touch(ident, home, change)


def settle(ident, outcome, home=None):
    """Закрыть запись исходом разбора. Повторное закрытие не затирает
    первый исход: черновик после вопроса это продолжение того же разбора,
    а не новый исход."""
    def change(rec):
        if not rec.get("outcome"):
            rec["outcome"] = outcome
            rec["outcome_when"] = _stamp()
    return _touch(ident, home, change)


def summary(home=None):
    """Сводка остановок по причинам и исходам, строка на причину."""
    rows = {}
    for rec in read(home):
        reason = rec.get("reason") or "без кода"
        row = rows.setdefault(reason, {})
        outcome = rec.get("outcome") or "открыт"
        row[outcome] = row.get(outcome, 0) + 1
    if not rows:
        return ["инцидентов нет"]
    out = []
    for reason in sorted(rows):
        parts = ", ".join("%s %d" % (name, rows[reason][name])
                          for name in sorted(rows[reason]))
        total = sum(rows[reason].values())
        out.append("%s: %d (%s)" % (reason, total, parts))
    return out


def read_recovery(home=None, _locked=False, timeout=1):
    path = log_path(home)
    if not _locked:
        fd = os.open(path + ".lock", os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, "r") as lock:
            deadline = time.monotonic() + max(0, timeout)
            while True:
                try:
                    fcntl.flock(lock, fcntl.LOCK_SH | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    if time.monotonic() >= deadline:
                        raise TimeoutError("замок журнала инцидентов занят")
                    time.sleep(min(0.01, max(0, deadline-time.monotonic())))
            return read_recovery(home, _locked=True)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, encoding="utf-8") as stream:
        raw = stream.read()
    if raw.strip() and not raw.endswith("\n"):
        raise ValueError("незавершён журнал инцидентов")
    records, seen = [], set()
    for row in raw.splitlines():
        if not row.strip():
            continue
        rec = json.loads(row)
        if (not isinstance(rec, dict) or not isinstance(rec.get("id"), str)
                or not rec["id"] or rec["id"] in seen
                or not isinstance(rec.get("task", ""), str)
                or not isinstance(rec.get("root", ""), str)
                or not isinstance(rec.get("notes", []), list)):
            raise ValueError("повреждён журнал инцидентов")
        seen.add(rec["id"])
        records.append(rec)
    return records


def finish_recovery(ident, root, task, line, resumed=False, home=None, timeout=1):
    """Сохранить результат восстановления под ограниченным замком.

    В этом пути ошибка замка либо повреждение журнала запрещают запись.
    Запись вместе с исходом заменяется атомарно и синхронизируется на диск.
    """
    path = log_path(home)
    fd = os.open(path + ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "r+") as lock:
        deadline = time.monotonic() + max(0, timeout)
        while True:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise TimeoutError("замок журнала инцидентов занят")
                time.sleep(min(0.01, max(0, deadline - time.monotonic())))
        records = read_recovery(home, _locked=True)
        found = [rec for rec in records if rec["id"] == ident and rec.get("task") == task
                 and rec.get("root") and os.path.realpath(rec["root"]) == os.path.realpath(root)]
        if len(found) != 1 or found[0].get("outcome"):
            return None
        rec = found[0]
        notes = list(rec.get("notes") or [])
        notes.append("%s %s" % (_stamp(), line))
        rec["notes"] = notes[-NOTES_CAP:]
        if resumed:
            rec["outcome"], rec["outcome_when"] = RESUMED, _stamp()
        fd, tmp = tempfile.mkstemp(prefix=".recovery-incident-", dir=os.path.dirname(path))
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as stream:
                for item in records:
                    stream.write(json.dumps(item, ensure_ascii=False, allow_nan=False) + "\n")
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(tmp, path)
            directory = os.open(os.path.dirname(path), os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            try:
                os.unlink(tmp)
            except FileNotFoundError:
                pass
        return rec
