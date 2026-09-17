#!/usr/bin/env python3
"""Отметка взятой выборки prose (DK-1024): PostToolUse-хук на Bash ставит
след по команде `prose.py sample`, тем же порядком, что phase-budget.py
узнаёт `taskctl move` разбором tool_input.command регуляркой
(hooks/phase-budget.py:69-74). Тот же скрипт стоит SessionStart-хуком и гасит
след на поводе compact: сжатие уносит корпус из контекста, и подсказка
«выборка уже была» после него была бы неправдой (hooks/session-task.py,
различение source=compact).

Файл следа лежит по контексту сессии, тем же порядком, что у
hooks/check-reread.py: ~/.devkit/prose/<контекст>.json, контекст это
session_id, а у субагента с приписанным agent_id через точку (DK-608), и у
него своё, пустое окно: выборка диспетчера ему не засчитывается. Читает
след PreToolUse-хук hooks/check-prose-sample.py: пока следа нет, запись
долгоживущего текста отбивается подсказкой с командой выборки.

Режимы:
  prose-mark.py --hook [протокол]
                   PostToolUse на Bash ставит след по команде `prose.py
                   sample`, SessionStart с source=compact его гасит; голый
                   --hook это claude-code
  prose-mark.py --state <каталог>
                   каталог следа для тестов (умолчание ~/.devkit/prose)
"""
import json
import os
import re
import sys
import time

import hookio

STATE_NAME = "prose"
# Брошенное состояние старше порога убирается тем же проходом, что и метит:
# отдельной уборки заводить незачем, тем же порядком, что у check-reread.py.
STALE = 7 * 24 * 3600
# Команда выборки узнаётся по подстроке независимо от пути перед ней и
# аргументов после: `python3 ~/projects/devkit/kit/skills/prose/prose.py
# sample --genre task` и вызов из другого чекаута devkit совпадают одинаково.
SAMPLE = re.compile(r"prose\.py\s+sample\b")


def text_of(value):
    return value if isinstance(value, str) else ""


def bash_command(event):
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return ""
    return text_of(ti.get("command"))


def mark(context, override=None, now=None):
    path = hookio.state_path(STATE_NAME, context, override)
    tmp = path + ".tmp"
    try:
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump({"ts": now if now is not None else time.time()}, f)
        os.replace(tmp, path)
    except OSError:
        pass


def clear(context, override=None):
    try:
        os.remove(hookio.state_path(STATE_NAME, context, override))
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
    # неотличимо от «команда не про выборку».
    hookio.entry(protocol)
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    kind = text_of(event.get("hook_event_name"))
    session = text_of(event.get("session_id"))
    if not session:
        # Без session_id след ставить некуда: пусть ход идёт как есть.
        return 0
    context = hookio.context_id(session, text_of(event.get("agent_id")))
    if kind == "SessionStart":
        if text_of(event.get("source")) == "compact":
            clear(context, override)
        return 0
    if kind != "PostToolUse" or text_of(event.get("tool_name")) != "Bash":
        return 0
    if not SAMPLE.search(bash_command(event)):
        return 0
    mark(context, override, now)
    sweep(hookio.state_dir(STATE_NAME, override), now if now is not None else time.time())
    return 0


def main(argv):
    override = None
    args = list(argv)
    # Каталог состояния для тестов: --state <путь>, одиночным флагом, чтобы не
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
            sys.stderr.write("prose-mark: %s\n" % e)
            return 2
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
