#!/usr/bin/env python3
"""Рубеж связки cd с командой (DK-770): PreToolUse-хук отбивает «cd <каталог> &&
<команда>» до того, как ход дойдёт до классификатора прав.

Классификатор авто-режима не вычисляет, куда указывает относительный путь после
cd, и говорит про «a directory that cannot be determined here». Поверх стоят
deny-правила на Read (четвёрка SECRET_DENY из perms.py, задача DK-228, рубеж
цели DK-207), а с версии 2.1.259 харнес распространил их и на связки с cd. При
запрете на чтение и неизвестном адресате разрешение спрашивается у человека, и
автономная сессия встаёт до его прихода: 3 сентября так разом встали и
исполнители задач, и ревьюверы. Снять deny нельзя, зато можно не писать связку
вовсе. Хук отвечает отказом с подсказкой, агент переписывает команду сам, и
вопрос до человека не доходит.

Одинокий `cd <путь>` проходит: сменить каталог отдельным вызовом законно, cwd у
Bash между вызовами сохраняется. Рубится ровно случай, когда за cd в позиции
команды идёт вторая команда через `&&`, `||`, `;`, `|`, `&` или перевод строки,
в том числе в подшелле `(cd X && ...)` и в подстановке `$(cd X && ...)`.

Команда разбирается токенами, а не поиском подстроки: слово cd внутри кавычек
(текст промпта у `claude -p '... cd ~/x && grep ...'`) командой не считается, и
тело heredoc'а снимается до разбора, иначе записанный в файл пример ловил бы сам
себя. Разбор берёт надёжный минимум: cd за словом другой конструкции (`do cd $d
&& ls` в цикле) рубеж пропускает, а нераспознанный shell проходит молча. Это
граница рубежа, а не пробел в нём: хук стоит на каждом ходе Bash и ложным
отказом сорвал бы работу дороже, чем пропущенной связкой.

Режимы:
  check-cd-compound.py <команда>   проверить команду из аргументов, выход 1 если
                                   в ней нашлась связка с cd, иначе 0
  ... | check-cd-compound.py --stdin
  check-cd-compound.py --hook [протокол]
                                   хук на PreToolUse Bash: JSON события на stdin,
                                   разбирается tool_input.command. Разбор входа и
                                   канал ответа по имени протокола из hookio.py,
                                   голый --hook это claude-code, ответ exit 2
"""
import re
import shlex
import sys

import hookio

CD = "cd"
# Знаки, из которых состоит разделитель команд. Токен, набранный только из них,
# открывает новую команду; `&&\n` тоже приходит одним токеном, поэтому судится
# состав, а не точное совпадение со списком.
SEPARATOR_CHARS = set(";&|\n")
# Знаки, которые лексер выделяет в отдельные токены. Перевод строки добавлен к
# набору shlex: он такой же разделитель команд, как `;`, и связку в две строки
# рубеж обязан видеть.
PUNCTUATION = "();<>|&\n"
# Имя разделителя, у которого нет печатного вида: голый перевод строки в отказе
# надо назвать словами, иначе подсказка выйдет с дырой посередине.
NEWLINE = "перевод строки"
# Открывающие скобки: после них снова стоит команда, поэтому `(cd X && ls)` и
# `$(cd X && ls)` разбираются наравне с началом строки.
OPENERS = ("(", "{")
# Заголовок heredoc'а: тело до строки-терминатора это данные, а не команды.
HEREDOC = re.compile(r"<<-?\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\1")


def strip_heredocs(command):
    """Команда без тел heredoc'ов. Записанный в файл пример со связкой это
    текст, и ловить в нём команды рубежу нечего."""
    out = []
    rest = command
    while True:
        m = HEREDOC.search(rest)
        if not m:
            out.append(rest)
            return "".join(out)
        delim = m.group(2)
        head, sep, tail = rest.partition("\n")
        if not sep:
            out.append(rest)
            return "".join(out)
        out.append(head + "\n")
        end = re.search(r"^\s*%s\s*$" % re.escape(delim), tail, re.MULTILINE)
        if not end:
            return "".join(out)
        rest = tail[end.end():]


def tokens(command):
    """Токены команды, разделители отдельными кусками. None значит, что shell
    не разобрался (незакрытая кавычка и подобное): такой ход рубеж пропускает."""
    lex = shlex.shlex(command, posix=True, punctuation_chars=PUNCTUATION)
    lex.whitespace_split = True
    lex.whitespace = " \t\r"
    try:
        return list(lex)
    except ValueError:
        return None


def is_separator(token):
    return bool(token) and set(token) <= SEPARATOR_CHARS


def compound_after_cd(command):
    """Разделитель, которым за cd открывается вторая команда, либо пустая строка.
    Разделитель возвращается текстом: отказ называет его, иначе спорить с
    рубежом нечем."""
    parts = tokens(strip_heredocs(command))
    if parts is None:
        return ""
    at_command = True
    in_cd = False
    for i, token in enumerate(parts):
        if is_separator(token):
            if in_cd:
                # Разделитель после cd открывает вторую команду только тогда,
                # когда за ним что-то стоит: хвостовой `cd /x;` это одинокий cd.
                if any(not is_separator(t) for t in parts[i + 1:]):
                    return token.strip() or NEWLINE
                return ""
            at_command = True
            continue
        if token in OPENERS:
            at_command = True
            in_cd = False
            continue
        if token == ")" or token == "}":
            in_cd = False
            at_command = False
            continue
        if at_command:
            in_cd = token == CD
        at_command = False
    return ""


def report(separator):
    named = separator if separator == NEWLINE else "`%s`" % separator
    return "\n".join([
        "связка cd со второй командой через %s отбита рубежом DK-770." % named,
        "Классификатор авто-режима не вычисляет каталог после cd, поверх стоит "
        "запрет на чтение секретов (DK-228), и такой ход уходит вопросом к "
        "человеку: автономная сессия встаёт до его прихода.",
        "Пиши без cd: абсолютный путь в аргументе, `git -C <путь>`, "
        "`cargo --manifest-path <путь>`, `go test <путь>`.",
        "Каталог, когда он всё-таки нужен, меняется одиноким `cd <путь>` "
        "отдельным вызовом Bash: cwd между вызовами сохраняется.",
    ]) + "\n"


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
    separator = compound_after_cd(command)
    if not separator:
        return 0
    return hookio.reply(protocol).found(report(separator))


def run_command(command):
    separator = compound_after_cd(command)
    if not separator:
        return 0
    sys.stdout.write(report(separator))
    return 1


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-cd-compound: %s\n" % e)
            return 2
    if argv[:1] == ["--stdin"]:
        return run_command(sys.stdin.read())
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    return run_command(" ".join(argv))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
