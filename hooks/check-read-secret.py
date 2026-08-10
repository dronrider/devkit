#!/usr/bin/env python3
"""Рубеж чтения секретов через Bash: PreToolUse-хук ловит обращение команды
shell к файлу доступов, local-docs, приватным ключам и хранилищу secretctl.
Инструмент Read режут permissions.deny (SECRET_DENY в perms.py, та же четвёрка
путей), этот хук прикрывает обход через cat, grep, head, tail и подобное
(задача DK-228, цель DK-207). Значение секрета доступно через secretctl, поэтому
рубится только прямой доступ к значениям, не работа с секретом вообще.

Матчинг путей берёт надёжный минимум: разворот ~/ и сравнение с эталонными
абсолютными путями и их префиксами, без попыток интерпретировать shell целиком. Переменные ($HOME и подобные), кавычки и пайпы не разворачиваются,
поэтому обход через них хуком не ловится. Надёжно режется прямой вид путей, и
этого хватает для примеров из постановки; хитрый обход через подмену команд и
переменные остаётся за рубежом, хук его на себя не берёт.

Режимы:
  check-read-secret.py <команда>   проверить команду из аргументов, выход 1
                                   если в ней нашёлся секретный путь, иначе 0
  ... | check-read-secret.py --stdin
  check-read-secret.py --hook [протокол]
                                   хук на PreToolUse Bash: JSON события на stdin,
                                   разбирается tool_input.command. Разбор входа
                                   и канал ответа по имени протокола из hookio.py,
                                   голый --hook это claude-code, ответ exit 2
"""
import os
import re
import sys

import hookio

HOME = os.path.expanduser("~")

# Эталонные секретные пути, те же, что в SECRET_DENY у perms.py: deny режет
# инструмент Read, хук прикрывает Bash. Файл доступов и хранилище secretctl
# точные, ключи каталогом: адресоваться может любой файл под ~/.ssh. local-docs
# смотрится сегментом пути, каталог лежит в корне проекта и путь к нему от cwd.
ACCESS_FILE = "~/.claude/access.local.md"
SSH_DIR = "~/.ssh"
SECRET_STORE = "~/.devkit/secrets"
LOCAL_DOCS = "local-docs"


def expand(path):
    """Разворот ~/ в абсолютный путь во всей строке. $HOME и прочие переменные
    не трогаются: их раскрыть без shell нельзя, а гадать ненадёжно, и хук честно
    признаёт это ограничение. ~/ хватает, чтобы развернуть и одиночный путь, и
    путь внутри команды за утилитой; ~ без слеша и ~user остаются как есть."""
    return path.replace("~/", HOME + "/") if HOME else path


# Развёрнутые абсолютные эталоны: исходный вид для подсказки и развёрнутый для
# матчинга в команде. Без HOME матчить абсолютные пути нечем, и рубится только
# local-docs по сегменту.
ABSOLUTE_TARGETS = tuple(
    (raw, expand(raw)) for raw in (ACCESS_FILE, SSH_DIR, SECRET_STORE)
) if HOME else ()


def found_targets(command):
    """Секретные эталоны (исходный вид с ~), чей развёрнутый путь встретился в
    команде. local-docs смотрится сегментом пути в любом месте команды: каталог
    лежит в корне проекта, но обратиться к нему можно и относительным путём из
    подкаталога. Для остальных берётся вхождение развёрнутого абсолютного пути,
    без разбора, какая утилита и какой аргумент: любые cat, cp, vim или
    незнакомая утилита рубятся одним рубежом."""
    found = []
    expanded = expand(command)
    if re.search(r"(^|[/\s])local-docs(/|$)", expanded):
        found.append(LOCAL_DOCS)
    for raw, abs_t in ABSOLUTE_TARGETS:
        if abs_t in expanded:
            found.append(raw)
    seen = set()
    unique = []
    for t in found:
        if t not in seen:
            seen.add(t)
            unique.append(t)
    return unique


ADVICE = (
    "значение секрета доступно через secretctl (secretctl exec, secretctl get),\n"
    "и рубится только прямой доступ к значениям, не работа с секретом вообще\n"
    "(цель DK-207)."
)


def report(targets):
    lines = ["чтение секретов через Bash рубится рубежом DK-228 (цель DK-207):",
             "команда лезет к " + ", ".join(targets),
             ADVICE]
    return "\n".join(lines) + "\n"


def run_hook(protocol):
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    ti = event.get("tool_input")
    if not isinstance(ti, dict):
        return 0
    command = ti.get("command")
    if not isinstance(command, str) or not command:
        return 0
    targets = found_targets(command)
    if not targets:
        return 0
    return hookio.reply(protocol).found(report(targets))


def run_command(command):
    targets = found_targets(command)
    if not targets:
        return 0
    sys.stdout.write(report(targets))
    return 1


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-read-secret: %s\n" % e)
            return 2
    if argv[:1] == ["--stdin"]:
        return run_command(sys.stdin.read())
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    return run_command(" ".join(argv))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
