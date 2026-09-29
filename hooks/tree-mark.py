#!/usr/bin/env python3
"""Привязка «субагент -> дерево задачи» (DK-1072): PostToolUse-хук на Bash
запоминает, над какой задачей работает субагент и где лежит её боковое дерево.
Читает привязку сторож записи hooks/check-tree-write.py.

Рабочий каталог у субагента между вызовами Bash сбрасывается в каталог
позвавшей его сессии, и у исполнителя задачи это основной чекаут. Оттуда
правка уезжает в дерево, которое ему трогать запрещено, а прогон тестов идёт
против чужой ветки и даёт ложный зелёный. По хвосту каталога дерево задачи
тут не выводится ровно в том случае, ради которого рубеж и ставится: каталог
называет чужое дерево, а не своё.

Задачу хук берёт из команды самого субагента. Первым ходом исполнитель кладёт
план работ (`agentctl plan set --label DK-1072-exec`), и дальше каждая команда
доски называет ID задачи. Событие спавна (SubagentStart) для этого не годится:
задания субагента оно не несёт, а в транскрипте родителя ход инструмента
`Agent` в этот момент ещё не записан (замер 2026-09-29 на клиенте 2.1.263).

Ход самой сессии хук не метит: привязку получает только ход с полем agent_id,
которое харнес кладёт в событие под субагентом и не кладёт у хода сессии
(DK-608, hooks/check-reread.py). Иначе рубеж отбивал бы диспетчеру запись в его
же чекауте.

Дерево задачи ищется по ветке среди рабочих деревьев репозитория: имя ветки это
ID задачи прописными буквами вниз, с необязательным хвостом через дефис
(`dk-1072`, `dk-470-lld-link`). Вместе с деревом задачи в привязку ложится
перечень остальных деревьев репозитория: сторожу тогда не нужен подпроцесс на
каждую запись. Дерева нет, значит привязки нет, и сторож молчит: лучше
отсутствие рубежа, чем отказ по угаданному пути.

Файл привязки лежит по контексту хода, тем же порядком, что у
hooks/prose-mark.py: ~/.devkit/trees/<контекст>.json, контекст это session_id с
приписанным через точку agent_id.

Режимы:
  tree-mark.py --hook [протокол]
                   PostToolUse на Bash кладёт привязку по команде субагента,
                   назвавшей ID задачи; голый --hook это claude-code
  tree-mark.py --state <каталог>
                   каталог привязки для тестов (умолчание ~/.devkit/trees)
"""
import json
import os
import re
import subprocess
import sys
import time

import hookio

STATE_NAME = "trees"
# Брошенная привязка старше порога убирается тем же проходом, что и метит,
# порядок тот же, что у prose-mark.py.
STALE = 7 * 24 * 3600
# Утилиты, чей вызов считается работой над задачей. Свободная строка с ID тут
# не годится: ID задачи попадается в чужом тексте, в имени файла и в сообщении
# коммита, и привязка по нему уехала бы на первое же упоминание.
TOOLS = re.compile(r"\b(taskctl|agentctl|shipctl|trackctl|obeycheck|regcheck)\b")
# Форма ID задачи: префикс доски прописными и номер до шести знаков. Хвост
# метки плана (`DK-1072-exec`) в ID не входит, поэтому граница справа не
# требует пробела.
TASK = re.compile(r"\b([A-Z][A-Z0-9]{1,9}-[0-9]{1,6})\b")


def text_of(value):
    return value if isinstance(value, str) else ""


def bash_command(event):
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return ""
    return text_of(ti.get("command"))


def task_of(command):
    """ID задачи из команды доски. Пустая строка значит, что команда не про
    задачу: либо утилиты доски в ней нет, либо ID не назван."""
    if not TOOLS.search(command):
        return ""
    found = TASK.search(command)
    return found.group(1) if found else ""


def branches(task):
    """Имена ветки задачи: ровно ID вниз и он же с хвостом через дефис."""
    low = task.lower()
    return low, low + "-"


def worktrees(cwd):
    """Рабочие деревья репозитория парами (путь, ветка). Пустой список значит,
    что спросить git не вышло: хук стоит на каждом ходе Bash любой сессии
    машины, и ронять ход из-за этого нельзя."""
    root = hookio.tree_root(cwd) or cwd
    if not root:
        return []
    try:
        out = subprocess.run(["git", "-C", root, "worktree", "list", "--porcelain"],
                             capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return []
    if out.returncode != 0:
        return []
    found, path = [], ""
    for line in out.stdout.splitlines():
        if line.startswith("worktree "):
            path = line[len("worktree "):].strip()
        elif line.startswith("branch ") and path:
            branch = line[len("branch "):].strip()
            found.append((path, branch.rsplit("/", 1)[-1]))
            path = ""
        elif not line.strip() and path:
            found.append((path, ""))
            path = ""
    if path:
        found.append((path, ""))
    return found


def binding(task, cwd):
    """Дерево задачи и остальные деревья репозитория. None значит, что дерева
    задачи среди них нет."""
    exact, prefixed = branches(task)
    trees = worktrees(cwd)
    own = ""
    for path, branch in trees:
        if branch == exact or branch.startswith(prefixed):
            own = path
            break
    if not own:
        return None
    others = [p for p, _ in trees if p != own]
    return {"task": task, "tree": own, "others": others}


def mark(context, data, override=None, now=None):
    path = hookio.state_path(STATE_NAME, context, override)
    tmp = path + ".tmp"
    row = dict(data)
    row["ts"] = now if now is not None else time.time()
    try:
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(row, f)
        os.replace(tmp, path)
    except OSError:
        pass


def sweep(directory, now):
    try:
        names = os.listdir(directory)
    except OSError:
        return
    for name in names:
        if not name.endswith(".json"):
            continue
        f = os.path.join(directory, name)
        try:
            if now - os.path.getmtime(f) > STALE:
                os.remove(f)
        except OSError:
            pass


def run_hook(protocol, override=None, now=None):
    # Протокол сверяется до разбора события: незнакомое имя не должно молчать
    # неотличимо от «команда не про задачу».
    hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    if text_of(event.get("hook_event_name")) != "PostToolUse":
        return 0
    if text_of(event.get("tool_name")) != "Bash":
        return 0
    session = text_of(event.get("session_id"))
    agent = text_of(event.get("agent_id"))
    if not session or not agent:
        # Привязку получает только ход субагента: у хода самой сессии поля
        # agent_id нет вовсе, и метить его значило бы отбивать диспетчеру
        # запись в его же чекауте.
        return 0
    task = task_of(bash_command(event))
    if not task:
        return 0
    data = binding(task, text_of(event.get("cwd")))
    if data is None:
        return 0
    context = hookio.context_id(session, agent)
    mark(context, data, override, now)
    sweep(hookio.state_dir(STATE_NAME, override), now if now is not None else time.time())
    return 0


def main(argv):
    override = None
    args = list(argv)
    # Каталог привязки для тестов: --state <путь>, одиночным флагом, чтобы не
    # путать разбор протокола в hookio.protocol.
    if "--state" in args:
        i = args.index("--state")
        if i + 1 < len(args):
            override = args[i + 1]
            del args[i:i + 2]
    if args[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(args[1:]), override)
        except hookio.Unknown as e:
            sys.stderr.write("tree-mark: %s\n" % e)
            return 2
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
