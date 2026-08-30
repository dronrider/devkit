#!/usr/bin/env python3
"""Рубеж подстановки в свободном тексте (DK-452): PreToolUse-хук на Bash ловит
вызов утилиты devkit, у которой в аргументе рядом с текстом стоят обратные
кавычки или $(...). Bash разбирает подстановку до запуска утилиты, и команда из
текста замечания выполняется молча; на ревью DK-438 так запустился devkitctl
update. Опасна ли подставленная команда, рубеж не разбирает: любая подстановка
в свободном тексте это отказ, а отказ называет безопасный вход.

Что проходит без отказа:
  - утилиты вне devkit: рубеж смотрит только на taskctl, shipctl и остальные
    из своего списка;
  - подстановка в одинарных кавычках и в heredoc с одинарными кавычками у
    делимитера (<<'EOF'): bash её не разворачивает;
  - служебное значение целым аргументом: --commit "$(git rev-parse HEAD)",
    -C "$(pwd)". Машинная подстановка отличается от свободного текста тем,
    что кроме неё в аргументе ничего нет.

Разбор команды берёт надёжный минимум, как у check-read-secret.py: слова,
кавычки, подстановки и heredoc, без интерпретации shell целиком. eval, запись
команды в переменную и подобные обходы рубеж на себя не берёт.

Режимы:
  check-subst.py <команда>    проверить команду из аргументов, выход 1
                              если нашлась подстановка в свободном тексте
  ... | check-subst.py --stdin
  check-subst.py --hook [протокол]
                              хук на PreToolUse Bash: JSON события на stdin,
                              разбирается tool_input.command. Разбор входа и
                              канал ответа по имени протокола из hookio.py,
                              голый --hook это claude-code, ответ exit 2
"""
import os
import re
import sys

import hookio

# Утилиты devkit из карты проекта: рубеж накрывает их все разом, а не одну
# review add. Матчится базовое имя первого слова простой команды, поэтому
# вызов по полному пути (~/bin/taskctl) ловится так же.
DEVKIT_TOOLS = frozenset((
    "agentctl", "cmdout", "dashboard", "devkitctl", "obeycheck", "regcheck",
    "secretctl", "shipctl", "taskctl", "trackctl",
))

# Обёртки, сквозь которые видно имя утилиты: env GOWORK=off taskctl ... это
# всё ещё вызов taskctl. Присвоения VAR=x перед командой пропускаются там же,
# как и ключи с длительностью после обёртки (timeout 60 taskctl ...).
WRAPPERS = frozenset(("env", "command", "exec", "nohup", "timeout"))

# Служебные слова bash перед именем утилиты: if taskctl ...; then это тот же
# вызов taskctl, и слово if разбор не выключает.
KEYWORDS = frozenset(("if", "then", "elif", "else", "fi", "for", "in", "do",
                      "done", "while", "until", "case", "esac", "time",
                      "!", "{", "}"))

# Длительность или число после обёртки: второй аргумент timeout.
DURATION = re.compile(r"[0-9]+[smhd]?")

# Ключ вида --flag=, -f= или присвоение VAR= перед подстановкой:
# --commit=$(git rev-parse HEAD) и FOO=$(date) это то же служебное значение,
# что и --commit "$(...)".
FLAG_PREFIX = re.compile(r"(--?[A-Za-z][A-Za-z0-9_-]*|[A-Za-z_][A-Za-z0-9_]*)=")


class Word(object):
    """Одно слово простой команды: литеральный текст до первой подстановки,
    литеральный текст после неё и число подстановок. Кавычки это синтаксис, в
    литеральный текст они не идут, поэтому "$(pwd)" остаётся чистой
    подстановкой без соседей."""

    def __init__(self):
        self.pre = ""
        self.post = ""
        self.substs = 0
        self.started = False

    def literal(self, text):
        if text:
            self.started = True
            if self.substs:
                self.post += text
            else:
                self.pre += text

    def subst(self):
        self.started = True
        self.substs += 1

    def free_subst(self):
        """Подстановка в свободном тексте: рядом с ней в слове есть другой
        текст. Целый аргумент из одной подстановки, в том числе за ключом
        --flag=, это служебное значение, оно проходит."""
        if not self.substs:
            return False
        if self.post:
            return True
        if self.substs > 1:
            return True
        return bool(self.pre) and not FLAG_PREFIX.fullmatch(self.pre)


class Command(object):
    """Простая команда: слова, признак подстановки в теле heredoc без
    одинарных кавычек у делимитера и номер конвейера, которым команда связана
    с соседями через одиночный |."""

    def __init__(self, pipeline=0):
        self.words = []
        self.heredoc_subst = False
        self.pipeline = pipeline

    def tool(self):
        """Имя утилиты devkit, которой команда принадлежит, либо пустая
        строка. Присвоения, служебные слова bash и обёртки перед именем
        пропускаются, у обёртки пропускаются её ключи и длительность."""
        after_wrapper = False
        for w in self.words:
            name = w.pre + w.post
            if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", name):
                continue
            if name in KEYWORDS:
                continue
            if after_wrapper and (name.startswith("-") or DURATION.fullmatch(name)):
                continue
            if w.substs:
                return ""
            base = os.path.basename(name)
            if base in WRAPPERS:
                after_wrapper = True
                continue
            return base if base in DEVKIT_TOOLS else ""
        return ""

    def has_free(self):
        """Подстановка в свободном тексте где-то в команде: слово со смешанной
        подстановкой либо тело heredoc без кавычек у делимитера."""
        return self.heredoc_subst or any(w.free_subst() for w in self.words)


def _skip_subst(text, i):
    """Конец подстановки $( ... ) с учётом вложенных скобок и кавычек внутри.
    Возврат это индекс за закрывающей скобкой; у оборванной подстановки это
    конец строки."""
    depth = 1
    i += 2
    n = len(text)
    while i < n and depth:
        c = text[i]
        if c == "\\":
            i += 2
            continue
        if c == "'":
            j = text.find("'", i + 1)
            i = n if j < 0 else j + 1
            continue
        if c == '"':
            i += 1
            while i < n and text[i] != '"':
                i += 2 if text[i] == "\\" else 1
            i += 1
            continue
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
        i += 1
    return i


def _skip_backtick(text, i):
    """Конец подстановки в обратных кавычках, индекс за закрывающей."""
    i += 1
    n = len(text)
    while i < n:
        if text[i] == "\\":
            i += 2
            continue
        if text[i] == "`":
            return i + 1
        i += 1
    return n


def _heredoc_delim(text, i):
    """Делимитер heredoc после <<: (индекс за ним, слово, в кавычках ли).
    Кавычки у делимитера значат, что bash тело не разворачивает."""
    if i < len(text) and text[i] == "-":
        i += 1
    while i < len(text) and text[i] in " \t":
        i += 1
    if i < len(text) and text[i] in "'\"":
        q = text[i]
        j = text.find(q, i + 1)
        if j < 0:
            return len(text), "", True
        return j + 1, text[i + 1:j], True
    j = i
    quoted = False
    while j < len(text) and text[j] not in " \t\n;|&<>":
        if text[j] == "\\":
            quoted = True
        j += 1
    return j, text[i:j].replace("\\", ""), quoted


def parse(text):
    """Простые команды строки Bash. Разделители это ;, |, &, перевод строки и
    скобки подоболочки; тела heredoc в слова не идут, а подстановка в теле без
    кавычек у делимитера помечает команду. Одиночный | связывает соседние
    команды одним номером конвейера: по нему found_tools судит, чей вывод
    течёт в утилиту devkit. Символы редиректа < и > режут слово: цель
    редиректа это своё слово, и >"$(mktemp)" остаётся чистой подстановкой."""
    pipeline = 0
    cmds = [Command(pipeline)]
    word = Word()
    heredocs = []
    i, n = 0, len(text)

    def flush_word():
        nonlocal word
        if word.started:
            cmds[-1].words.append(word)
            word = Word()

    def flush_cmd(same_pipe=False):
        nonlocal pipeline
        flush_word()
        if not same_pipe:
            pipeline += 1
        if cmds[-1].words:
            cmds.append(Command(pipeline))
        else:
            cmds[-1].pipeline = pipeline

    while i < n:
        c = text[i]
        if c == "\\":
            if i + 1 < n and text[i + 1] == "\n":
                i += 2
                continue
            word.literal(text[i + 1:i + 2])
            i += 2
        elif c == "'":
            j = text.find("'", i + 1)
            j = n if j < 0 else j
            word.literal(text[i + 1:j])
            i = j + 1
        elif c == '"':
            i += 1
            word.started = True
            while i < n and text[i] != '"':
                d = text[i]
                if d == "\\" and i + 1 < n and text[i + 1] in '"\\$`':
                    word.literal(text[i + 1])
                    i += 2
                elif d == "`":
                    word.subst()
                    i = _skip_backtick(text, i)
                elif d == "$" and i + 1 < n and text[i + 1] == "(":
                    word.subst()
                    i = _skip_subst(text, i)
                else:
                    word.literal(d)
                    i += 1
            i += 1
        elif c == "`":
            word.subst()
            i = _skip_backtick(text, i)
        elif c == "$" and i + 1 < n and text[i + 1] == "(":
            word.subst()
            i = _skip_subst(text, i)
        elif c == "<" and text[i + 1:i + 2] == "<" and text[i + 2:i + 3] != "<":
            flush_word()
            i, delim, quoted = _heredoc_delim(text, i + 2)
            if delim:
                heredocs.append((delim, quoted, cmds[-1]))
        elif c in "<>":
            flush_word()
            i += 1
        elif c in " \t":
            flush_word()
            i += 1
        elif c == "\n":
            for delim, quoted, cmd in heredocs:
                end = text.find("\n" + delim + "\n", i)
                stop = text.find("\n" + delim, i) if end < 0 else end
                body = text[i + 1:] if stop < 0 else text[i + 1:stop]
                if not quoted and ("`" in body or "$(" in body):
                    cmd.heredoc_subst = True
                i = n if stop < 0 else stop + 1 + len(delim)
            heredocs = []
            flush_cmd()
            i += 1
        elif c == "|":
            # || это разделитель команд, одиночный | и |& связка конвейера.
            if text[i + 1:i + 2] == "|":
                flush_cmd()
                i += 2
            else:
                flush_cmd(same_pipe=True)
                i += 2 if text[i + 1:i + 2] == "&" else 1
        elif c in ";&()":
            flush_cmd()
            i += 1 + (1 if c == "&" and text[i + 1:i + 2] == "&" else 0)
        else:
            word.literal(c)
            i += 1
    flush_word()
    return [c for c in cmds if c.words]


def found_tools(command):
    """Утилиты devkit, до которых в этой строке дотягивается подстановка в
    свободном тексте: она стоит в самой команде утилиты либо выше по
    конвейеру, чей вывод утилита читает. Ниже по конвейеру подстановка до
    утилиты не течёт и её не трогает. Порядок как в команде, без повторов."""
    tools = []
    upstream = {}
    for cmd in parse(command):
        dirty = upstream.get(cmd.pipeline, False) or cmd.has_free()
        t = cmd.tool()
        if t and dirty and t not in tools:
            tools.append(t)
        upstream[cmd.pipeline] = dirty
    return tools


def report(tools):
    return (
        "подстановка в свободном тексте рубится рубежом DK-452:\n"
        "в аргументе %s стоят обратные кавычки или $(...), bash выполнит их\n"
        "до вызова утилиты. Передай текст на stdin через heredoc с одинарными\n"
        "кавычками (taskctl review add DK-1 <<'EOF' ... EOF) либо аргументом\n"
        "в одинарных кавычках. Служебная подстановка целым аргументом\n"
        "(--commit \"$(git rev-parse HEAD)\") проходит.\n" % ", ".join(tools)
    )


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
    tools = found_tools(command)
    if not tools:
        return 0
    return hookio.reply(protocol).found(report(tools))


def run_command(command):
    tools = found_tools(command)
    if not tools:
        return 0
    sys.stdout.write(report(tools))
    return 1


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-subst: %s\n" % e)
            return 2
    if argv[:1] == ["--stdin"]:
        return run_command(sys.stdin.read())
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    return run_command(" ".join(argv))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
