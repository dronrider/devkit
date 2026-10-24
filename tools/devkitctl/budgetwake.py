"""Пробуждение автономной цели, припаркованной исчерпанным бюджетом.

Бюджет цели накопительный: смена окна подписки его не обнуляет. Решение
остаётся за agentctl spend, а не за часами сторожка.
"""
import json
import re
import subprocess
import sys
from pathlib import Path

import lift


def parked_rows(doc):
    return [row for sec in doc.get("sections", []) if sec.get("key") == "blocked"
            for row in sec.get("rows", [])
            if (row.get("title") or "").startswith(lift.GOAL_TITLE)
            and row.get("block") == "бюджет исчерпан"
            and re.fullmatch(r"[A-Z][A-Z0-9]*-\d+", row.get("id") or "")]


def live_lock(root, tid, home):
    paths = [Path(root, ".devkit", "goal-%s.lock" % tid, "pid"),
             Path(home, ".devkit", "task-%s.lock" % tid, "pid")]
    for path in paths:
        try:
            pid = int(path.read_text(encoding="utf-8").strip())
        except FileNotFoundError:
            continue
        except (OSError, ValueError):
            return True
        if pid <= 0 or not lift.dead_pid(pid):
            return True
    return False


def fresh_gate(text):
    """Зелёный гейт при неизвестном или старом снимке не разрешает подъём."""
    lines = text.strip().splitlines()
    if not lines or lines[0] != "gate: ok":
        return False
    if any(mark in text for mark in ("протух", "по журналу", "неизвестен",
                                      "из будущего", "нет снимка", "снимка квоты нет")):
        return False
    return bool(re.search(r"снимок [^;\n]+ назад", text))


def goal_sessions(root, tid, entries, sessions):
    sids = {e.get("session") for e in entries
            if e.get("root") == root and e.get("goal") == tid}
    return [s for s in sessions if s.get("дерево") == root and s.get("задача") == tid
            and (s.get("сессия") in sids or s.get("tmux") == "goal-" + tid)]


def original_harness(root, tid, entries, sessions, layout):
    """Подписка последнего носителя цели по реестру чатов и раскладке машины."""
    owners = goal_sessions(root, tid, entries, sessions)
    owner = owners[-1] if owners else None
    if not owner or not owner.get("транскрипт") or owner["транскрипт"] == "-":
        return ""
    path = Path(owner["транскрипт"]).expanduser().resolve()
    names = []
    for h in layout.get("harnesses", []):
        if not h.get("home"):
            continue
        try:
            path.relative_to(Path(h["home"]).expanduser().resolve())
        except ValueError:
            continue
        names.append(h)
    if len(names) != 1 or not names[0].get("enabled"):
        return ""
    return names[0].get("name") or ""


def wake(root, now, call=None, taskctl=None, agentctl=None, home=None,
         left=None, act=True):
    """Обход доски, включая цели, ушедшие из реестра после штатного стопа.

    Возвращает строки отчёта и число попыток подъёма. Лимит и выдержка от lift; left
    позволяет вызывающему сократить потолок машины на текущем обходе.
    """
    import watch
    call = subprocess.run if call is None else call
    home = Path(watch.default_home() if home is None else home)
    taskctl = lift.which("taskctl") if taskctl is None else taskctl
    agentctl = lift.which("agentctl") if agentctl is None else agentctl
    if not taskctl:
        return ["корень %s: бюджетные цели не проверены, бинаря taskctl нет" % root], 0

    def invoke(argv):
        return call(argv, cwd=root, stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT, text=True)

    try:
        p = invoke([taskctl, "-C", root, "list", "--json"])
        if p.returncode:
            return ["корень %s: бюджетные цели не проверены, taskctl list отказал" % root], 0
        doc = json.loads(p.stdout or "{}")
    except (OSError, ValueError) as exc:
        return ["корень %s: бюджетные цели не проверены, %s" % (root, exc)], 0
    rows = parked_rows(doc)
    if not rows:
        return [], 0
    if not agentctl:
        return ["корень %s: бюджетные цели ждут, бинаря agentctl нет" % root], 0
    try:
        deploy = Path(root, ".devkit", "deploy.local").read_text(encoding="utf-8")
    except OSError:
        deploy = ""
    if not any(re.match(r"^\s*autonomous\s*=\s*true\s*(?:#.*)?$", ln)
               for ln in deploy.splitlines()):
        return ["корень %s: бюджетные цели ждут, автономия не включена" % root], 0
    board_rows = [row for sec in doc.get("sections", [])
                  for row in sec.get("rows", [])]
    free = lift.MACHINE_LIMIT if left is None else max(0, left)
    state_home = str(home / ".devkit")
    stamp = now.timestamp()
    tried = lift.marks(state_home, stamp)
    entries = [watch.read_entry(path) for path in watch.entries(home)]
    sessions = watch.session_rows(home)
    try:
        p = invoke([agentctl, "harness", "--json"])
        if p.returncode:
            return ["корень %s: бюджетные цели ждут, раскладка подписок не прочитана" % root], 0
        layout = json.loads(p.stdout or "{}")
    except (OSError, ValueError) as exc:
        return ["корень %s: бюджетные цели ждут, подписки не определены: %s" % (root, exc)], 0
    harness_used = {}
    lines, attempted = [], 0
    for row in rows:
        tid = row["id"]
        prefix = "цель %s в %s" % (tid, root)
        if (live_lock(root, tid, home)
                or row.get("stage_head") == "голова жива"
                or (row.get("stage_session") or "").startswith(lift.ALIVE)
                or any(e.get("root") == root and e.get("goal") == tid
                       and watch.session_alive(e.get("session"), now, home)
                       for e in entries)
                or any(watch.session_alive(s.get("сессия"), now, home)
                       for s in goal_sessions(root, tid, entries, sessions))):
            lines.append(prefix + ": носитель жив или замок занят, подъём не нужен")
            continue
        when, tries = tried.get(root + "\t" + tid, (0, 0))
        if tries >= lift.LIFT_TRIES or (when and stamp - when < lift.SETTLE):
            lines.append(prefix + ": бюджетный подъём ждёт, предел попыток или выдержка")
            continue
        harness = original_harness(root, tid, entries, sessions, layout)
        if not harness:
            lines.append(prefix + ": парковка сохранена, подписка прежнего носителя неизвестна или выключена")
            continue

        def scoped_call(argv, **kw):
            return call([agentctl, "exec", "--harness", harness, "--"] + argv, **kw)

        try:
            p = invoke([agentctl, "exec", "--harness", harness, "--",
                        agentctl, "-C", root, "spend", "--goal",
                        "docs/tasks/%s.md" % tid, "--peek"])
            text = " ".join((p.stdout or "").split())
            if p.returncode or not fresh_gate(p.stdout or ""):
                lines.append(prefix + ": парковка по бюджету сохранена; " +
                             (text or "гейт не дал проверенного разрешения"))
                continue
            slots, _ = lift.capacity(root, board_rows, call=scoped_call, agentctl=agentctl)
            if free < 1 or slots <= harness_used.get(harness, 0):
                lines.append(prefix + ": бюджет разрешает продолжение, свободной ёмкости нет")
                continue
            if not act:
                lines.append(prefix + ": бюджет разрешает продолжение, нужен подъём цикла")
                continue
            # Оболочка должна принять заказ до снятия парковки: иначе отказ
            # запуска убрал бы единственный повод следующего бюджетного обхода.
            Path(state_home).mkdir(parents=True, exist_ok=True)
            lift.mark_lifted(root, tid, state_home, stamp)
            free -= 1
            attempted += 1
            harness_used[harness] = harness_used.get(harness, 0) + 1
            script = watch.DEVKIT / "kit" / "skills" / "goal-loop" / "goal-run.py"
            p = invoke([sys.executable, str(script), tid, "-C", root, "--harness", harness])
            if p.returncode:
                lines.append(prefix + ": цикл не поднят, парковка сохранена; " +
                             " ".join((p.stdout or "").split()))
                continue
            p = invoke([taskctl, "-C", root, "wake", tid, "--push"])
            if p.returncode:
                lines.append(prefix + ": цикл поднят, снятие парковки отказало; " +
                             " ".join((p.stdout or "").split()))
            else:
                lines.append(prefix + ": бюджет разрешил продолжение, цикл поднят")
        except OSError as exc:
            lines.append(prefix + ": бюджетный подъём не завершён, %s" % exc)
    return lines, attempted
