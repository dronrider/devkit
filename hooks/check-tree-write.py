#!/usr/bin/env python3
"""Сторож записи мимо дерева задачи (DK-1072): PreToolUse-хук на Write, Edit,
MultiEdit и NotebookEdit отбивает правку, которая уезжает из бокового дерева
задачи в чужое дерево того же репозитория. Отказ называет оба пути и путь той
же правки в своём дереве.

Рабочий каталог у субагента между вызовами Bash сбрасывается в каталог
позвавшей его сессии, и у исполнителя задачи это основной чекаут. Относительный
путь оттуда указывает в чужое дерево, правку находит только следующий
`shipctl`, а прогон тестов идёт против чужой ветки и даёт ложный зелёный. Ни
отказа, ни предупреждения при этом раньше не было.

Привязку «субагент -> дерево задачи» кладёт hooks/tree-mark.py по команде
самого исполнителя, назвавшей ID задачи. Нет привязки, нет и рубежа: сторож
молчит у любого хода, про который неизвестно, над какой задачей он идёт.
Отбивается только запись внутрь чужого рабочего дерева того же репозитория.
Файл вне деревьев (черновик в /tmp, чужой проект) рубеж не трогает: следом
задачи он не становится.

Правка через Bash сторожу не видна. `sed`, `taskctl` и прогон тестов идут
мимо события записи, а разбор чужой командной строки ошибается в обе стороны.
Эти случаи держат отказы самих утилит доски, и живут они своими строками
(DK-536, DK-949).

Режимы:
  check-tree-write.py --hook [протокол]
                   PreToolUse на записи сверяет путь правки с деревом задачи;
                   голый --hook это claude-code
  check-tree-write.py --state <каталог>
                   каталог привязки для тестов (умолчание ~/.devkit/trees)
"""
import json
import os
import sys

import hookio

STATE_NAME = "trees"


def text_of(value):
    return value if isinstance(value, str) else ""


def bound(context, override=None):
    """Привязка хода: пара (дерево задачи, ID задачи, чужие деревья). None
    значит, что привязки нет и сверять не с чем."""
    try:
        with open(hookio.state_path(STATE_NAME, context, override), "r",
                  encoding="utf-8") as f:
            row = json.load(f)
    except (OSError, ValueError):
        return None
    if not isinstance(row, dict):
        return None
    tree, task = text_of(row.get("tree")), text_of(row.get("task"))
    if not tree or not task:
        return None
    others = [text_of(p) for p in row.get("others") or [] if text_of(p)]
    return tree, task, others


def inside(path, root):
    """Путь лежит в дереве: сам корень или что-то под ним. Сравнение идёт по
    нормализованным путям без обращения к диску: файла правки может ещё не
    быть, и realpath тут вернул бы не то, что просили."""
    if not root:
        return False
    root = os.path.normpath(root)
    path = os.path.normpath(path)
    return path == root or path.startswith(root + os.sep)


def hint(path, tree, task, others):
    """Отказ: оба пути и та же правка в своём дереве."""
    stray = next((o for o in others if inside(path, o)), "")
    rel = os.path.relpath(os.path.normpath(path), os.path.normpath(stray))
    return ("Запись мимо дерева задачи %s отбита рубежом DK-1072.\n"
            "Правка ушла бы в %s, а дерево задачи это %s.\n"
            "Повтори ход путём в своём дереве: %s\n"
            "Рабочий каталог между вызовами Bash сбрасывается в каталог "
            "диспетчера, поэтому путь пишется целиком, а git зовётся с "
            "-C %s." % (task, stray, tree, os.path.join(tree, rel), tree))


def run_hook(protocol, override=None):
    entry = hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    write = entry.write(event)
    if write is None or not write.path:
        return 0
    session = text_of(event.get("session_id"))
    agent = text_of(event.get("agent_id"))
    if not session or not agent:
        # Ход самой сессии рубеж не трогает: привязку получает только субагент,
        # а диспетчеру запись в его чекауте законна.
        return 0
    data = bound(hookio.context_id(session, agent), override)
    if data is None:
        return 0
    tree, task, others = data
    path = write.path
    if not os.path.isabs(path):
        # Относительный путь харнес разрешает от рабочего каталога хода, и в
        # событие он приходит уже целым. Пришёл неразрешённым, значит считаем
        # его от того же каталога.
        path = os.path.join(text_of(event.get("cwd")), path)
    if inside(path, tree):
        return 0
    if not any(inside(path, o) for o in others):
        return 0
    return hookio.reply(protocol).found(hint(path, tree, task, others))


def main(argv):
    override = None
    args = list(argv)
    if "--state" in args:
        i = args.index("--state")
        if i + 1 < len(args):
            override = args[i + 1]
            del args[i:i + 2]
    if args[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(args[1:]), override)
        except hookio.Unknown as e:
            sys.stderr.write("check-tree-write: %s\n" % e)
            return 2
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
