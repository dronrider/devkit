#!/usr/bin/env python3
"""Запись этапов задачи из хуков: тот же файл ~/.devkit/runs/<ID>-<slug>.run,
что пишет internal/stage на go.

Хук спавна субагента ставит ревью, вычитку, разработку и доработку (DK-911), хук
старта сессии ставит постановку. Звать go-утилиту из хука нельзя. Бинарь стоит
не на каждой машине и не в каждом PATH, а молчаливая пропажа этапа из-за него
неотличима от штатной работы. Формат записи из строк «ключ = значение» с
полями этапа через вертикальную черту, и писать его тут дешевле, чем
зависеть от сборки. Формат держится тем же, что у go-стороны. Проверка на
этот счёт лежит в stagerun_test.py и в internal/stage/stage_test.go, на одном
и том же тексте записи.

Поля этапа: вид, начало, текст записи, разговор, конец, номер работы. Конец
пуст у живого этапа, номер работы пуст у этапов без субагента.
"""
import os
import re
import subprocess
import time

STAMP = "%Y-%m-%dT%H:%M:%S"

# Словарь видов, тот же, что в internal/stage: восемь этапов работы и три
# ожидания. Слова прежнего словаря («снаружи», «уточнение») хук не пишет.
SETUP = "постановка"
DEV = "разработка"
PROOF = "вычитка"
REVIEW = "ревью"
REWORK = "доработка"
MERGE = "слияние"
DEPLOY = "выкат"
VERIFY = "проверка"
WAIT_HUMAN = "ждёт человека"
WAIT_EVENT = "ждёт события"
WAIT_QUEUE = "ждёт очереди"
WORKS = (SETUP, DEV, PROOF, REVIEW, REWORK, MERGE, DEPLOY, VERIFY)
WAITS = (WAIT_HUMAN, WAIT_EVENT, WAIT_QUEUE)
KINDS = WORKS + WAITS

# Работа исполнителя над кодом: по последнему такому этапу хук решает, идёт
# разработка или доработка после ревью.
EXECS = (DEV, REWORK)


def home_dir(env=None):
    env = os.environ if env is None else env
    return env.get("HOME") or os.path.expanduser("~")


def runs_dir(home):
    return os.path.join(home, ".devkit", "runs")


def slug(root):
    """Имя, годное в имя файла: всё, кроме латиницы и цифр, дефис, как у
    stage.Slug на go."""
    out = "".join(ch if ch.isascii() and ch.isalnum() else "-" for ch in root)
    return out.strip("-")


def main_root(root, run=subprocess.run):
    """Основной чекаут по git-common-dir, как stage.MainRoot: этап разработки
    открывают из дерева задачи, а пакет закрывает смена статуса из основного
    чекаута, и без приведения это были бы две разные записи. Вне git-дерева
    возвращается то, что дали."""
    if not root:
        return root
    try:
        out = run(["git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                  capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return root
    if out.returncode != 0:
        return root
    common = (out.stdout or "").strip()
    if not common:
        return root
    main = os.path.dirname(common)
    return main if main and main != "." else root


def path(home, root, task):
    return os.path.join(runs_dir(home), "%s-%s.run" % (task, slug(root)))


def clean(text):
    """Разделитель полей и перевод строки из текста уходят, как у clean на go."""
    return (text or "").replace("|", "/").replace("\n", " ").strip()


def load(file):
    """Запись целиком: id, root и список этапов словарями. Нет файла, значит
    пустая запись."""
    rec = {"id": "", "root": "", "stages": []}
    try:
        with open(file, encoding="utf-8") as f:
            lines = f.read().split("\n")
    except OSError:
        return rec
    for ln in lines:
        ln = ln.strip()
        if not ln or ln.startswith("#") or "=" not in ln:
            continue
        key, val = ln.split("=", 1)
        key, val = key.strip(), val.strip()
        if key == "id":
            rec["id"] = val
        elif key == "root":
            rec["root"] = val
        elif key == "этап":
            parts = [p.strip() for p in val.split("|")]
            if len(parts) < 2:
                continue
            parts += [""] * (6 - len(parts))
            rec["stages"].append({"kind": parts[0], "start": parts[1], "note": parts[2],
                                  "session": parts[3], "end": parts[4], "work": parts[5]})
    return rec


def dump(task, root, stages):
    out = ["# этапы задачи %s: пишет конвейер devkit, читает дашборд" % task,
           "id = %s" % task, "root = %s" % root]
    for s in stages:
        out.append("этап = %s | %s | %s | %s | %s | %s" % (
            s["kind"], s["start"], clean(s.get("note")), clean(s.get("session")),
            s.get("end") or "", clean(s.get("work"))))
    return "\n".join(out) + "\n"


def stamp(seconds):
    return time.strftime(STAMP, time.localtime(seconds))


def save(home, root, task, rec):
    os.makedirs(runs_dir(home), exist_ok=True)
    with open(path(home, root, task), "w", encoding="utf-8") as f:
        f.write(dump(task, root, rec["stages"]))


def put(home, root, task, kind, note, session, start, end=None, work=""):
    """Дописать этап. Закрытый этап (синхронный субагент, о котором хук узнаёт
    по его концу) кладётся сразу с концом. Возвращает путь записи."""
    if kind not in KINDS:
        raise ValueError("неизвестный вид деятельности %r" % kind)
    rec = load(path(home, root, task))
    rec["stages"].append({"kind": kind, "start": stamp(start), "note": note,
                          "session": session, "end": stamp(end) if end else "",
                          "work": work})
    save(home, root, task, rec)
    return path(home, root, task)


def live(home, root, task):
    """Живой этап записи либо None: последний, пока его не закрыл писатель."""
    stages = load(path(home, root, task))["stages"]
    if not stages or stages[-1]["end"]:
        return None
    return stages[-1]


def last_work(home, root, task):
    """Последний этап работы записи (не ожидание) либо None."""
    for s in reversed(load(path(home, root, task))["stages"]):
        if s["kind"] in WORKS:
            return s
    return None


def close(home, root, task, kind, end, extra="", work=""):
    """Закрыть живой этап названного вида: конец и хвост текста записи. Живой
    этап другого вида не трогается. Номер работы сверяется, когда назван:
    у одной задачи бывают два субагента подряд, и конец первого не должен
    закрывать второй. Возвращает True, когда этап закрыт."""
    rec = load(path(home, root, task))
    stages = rec["stages"]
    if not stages or stages[-1]["end"] or stages[-1]["kind"] != kind:
        return False
    if work and stages[-1]["work"] and stages[-1]["work"] != work:
        return False
    stages[-1]["end"] = stamp(end)
    if extra:
        stages[-1]["note"] = (stages[-1]["note"] + ", " + extra) if stages[-1]["note"] else extra
    save(home, root, task, rec)
    return True


TURNS_RE = re.compile(r"ходов (\d+)")


def work_note(text, seconds):
    """Хвост «ходов N, минут M» по отчёту субагента и его длительности: тот же
    канон, что у stage.WorkNote на go, его читает taskctl review stats. Без
    числа ходов в отчёте хвост несёт одни минуты."""
    minutes = max(0, int(round(seconds / 60.0)))
    m = TURNS_RE.search(text or "")
    if not m:
        return "минут %d" % minutes
    return "ходов %s, минут %d" % (m.group(1), minutes)
