#!/usr/bin/env python3
"""Рубеж связки cd с командой (DK-770): PreToolUse-хук отбивает «cd <каталог> &&
<команда>» до того, как ход дойдёт до классификатора прав, и подсказывает
готовую замену.
Классификатор авто-режима не вычисляет, куда указывает относительный путь после
cd, и говорит про «a directory that cannot be determined here». Поверх стоят
deny-правила на Read (четвёрка SECRET_DENY из perms.py, задача DK-228, рубеж
цели DK-207), а с версии 2.1.259 харнес распространил их и на связки с cd. При
запрете на чтение и неизвестном адресате разрешение спрашивается у человека, и
автономная сессия встаёт до его прихода: 3 сентября так разом встали и
исполнители задач, и ревьюверы. Снять deny нельзя, зато можно не писать связку
вовсе. Хук отвечает отказом с готовой заменой, агент повторяет ход ею, и вопрос
до человека не доходит.
Рубится не любая связка, а три вида, которые уходят человеку (замер
headless-прогонами 2026-09-03): за cd идёт команда чтения или листинга, которую
Claude Code узнаёт в Bash (cat, grep, sed, ls, find и соседи), с относительным
путём в операнде либо листинг без операнда (голый `ls` читает cwd); читатель
stdin в трубе (`make | head -1`) проходит; за cd идёт git (в новом каталоге он может
выполнить чужие хуки); за cd идёт редирект вывода в файл, кроме /dev/null.
Связка с командой, которую классификатор пропускает сам (`cd X && cargo test`,
`cd X && go test ./...`, `cd X && make`, `cd X && python3 t.py`), проходит:
ложный отказ на каждом ходу дороже пропущенной связки.
Одинокий `cd <путь>` проходит: сменить каталог отдельным вызовом законно, cwd у
Bash между вызовами сохраняется. Подшелл `(cd X && ...)` и подстановка
`$(cd X && ...)` разбираются наравне с началом строки.
Команда разбирается токенами, а не поиском подстроки: слово cd внутри кавычек
(текст промпта у `claude -p '... cd ~/x && grep ...'`) командой не считается, и
тело heredoc'а снимается до разбора, иначе записанный в файл пример ловил бы сам
себя. Разбор берёт надёжный минимум: cd за словом другой конструкции (`do cd $d
&& ls` в цикле) рубеж пропускает, а нераспознанный shell проходит молча.
Замена в подсказке собирается из самой команды: cd убирается, относительные
операнды команд чтения и цель редиректа получают префикс каталога, git получает
`-C <каталог>`. Каталог с подстановкой (`cd $ROOT && ...`) в путь не
подставляется, тогда подсказка советует одинокий cd отдельным вызовом.
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
SEPARATOR_CHARS = set(";&|\n")
PUNCTUATION = "();<>|&\n"
OPENERS = ("(", "{")
CLOSERS = (")", "}")
HEREDOC = re.compile(r"<<-?\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\1")
# Команды чтения файла, которые Claude Code узнаёт в Bash и судит по правилам
# Read: относительный путь в их операнде после cd и есть случай «каталог не
# определить». grep-семейство, sed и awk первым операндом несут шаблон, у них
# файлы идут со второго.
READERS = {
    "cat", "head", "tail", "tac", "less", "more", "nl", "wc", "cut", "sort",
    "uniq", "strings", "xxd", "od", "diff", "cmp", "cp", "stat", "file",
    "grep", "egrep", "fgrep", "rg", "ugrep", "ag", "sed", "awk",
    "ls", "find", "tree", "du",
}
# Команды, которым без операнда каталог даёт сам cd: в замене он встаёт явно.
DIR_DEFAULT = {"ls", "find", "tree", "du"}
PATTERN_FIRST = {"grep", "egrep", "fgrep", "rg", "ugrep", "ag", "sed", "awk"}
DEVNULL = "/dev/null"


def strip_heredocs(command):
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
    # Экранированный перевод строки это продолжение той же строки: shlex иначе
    # склеил бы его со следующим словом в один токен, и `cd X && \\<newline>cat f`
    # прошёл бы мимо рубежа.
    command = command.replace("\\\n", " ")
    lex = shlex.shlex(command, posix=True, punctuation_chars=PUNCTUATION)
    lex.whitespace_split = True
    lex.whitespace = " \t\r"
    try:
        return list(lex)
    except ValueError:
        return None


def is_separator(token):
    return bool(token) and set(token) <= SEPARATOR_CHARS


def is_redirect(token):
    return bool(token) and ">" in token and "&" not in token and set(token) <= set("<>0123456789")


def split_segments(parts):
    """Команда как список сегментов: каждый это (список токенов, разделитель
    после него). Скобки в сегменты не входят, глубина не отслеживается: рубежу
    хватает знать, где начинается очередная команда."""
    segments = []
    cur = []
    for token in parts:
        if is_separator(token):
            segments.append((cur, token))
            cur = []
        elif token in OPENERS or token in CLOSERS:
            if cur:
                segments.append((cur, ""))
                cur = []
        else:
            cur.append(token)
    if cur:
        segments.append((cur, ""))
    return segments


NUMERIC = re.compile(r"^[+]?[0-9][0-9,]*[a-z]?$")


def is_relative(path):
    """Операнд похож на относительный путь: не абсолютный, не с тильдой, не
    подстановка, не ключ и не число (значение `-n 5` у tail это не файл)."""
    if not path or path[0] in "/~$-" or path.startswith("${"):
        return False
    if "/" not in path and any(ch in path for ch in "*?["):
        return False
    return not NUMERIC.match(path)


def literal_dir(target):
    """Каталог cd, годный в префикс пути: без подстановок и глоббинга."""
    if not target or any(ch in target for ch in "$`*?"):
        return ""
    return target.rstrip("/") or "/"


def join_path(base, rel):
    if rel == ".":
        return base
    if rel.startswith("./"):
        rel = rel[2:]
    return base + "/" + rel


def rewrite_reader(seg, base):
    """Сегмент команды чтения с абсолютными операндами: относительные получают
    префикс каталога, а листингу без операнда каталог дописывается явно."""
    name = seg[0]
    skip_pattern = name in PATTERN_FIRST
    out = list(seg)
    changed = False
    operands = 0
    i = 1
    while i < len(seg):
        tok = seg[i]
        if is_redirect(tok):
            i += 2
            continue
        if name == "find" and tok.startswith("-"):
            # У find пути стоят до первого ключа, дальше идут условия и их
            # значения (`-name '*.py'`), путями они не являются.
            break
        if tok.startswith("-") and tok != "-":
            if tok in ("-e", "-f", "--file", "--regexp") and i + 1 < len(seg):
                i += 2
                continue
            i += 1
            continue
        if skip_pattern:
            skip_pattern = False
            i += 1
            continue
        operands += 1
        if is_relative(tok):
            changed = True
            if base:
                out[i] = join_path(base, tok)
        i += 1
    if not operands and name in DIR_DEFAULT:
        # Листинг без операнда читает cwd, который классификатору не виден.
        changed = True
        if base:
            at = next((k for k, t in enumerate(out) if is_redirect(t)), len(out))
            out.insert(at, base)
    return out if changed else None


def rewrite_redirect(seg, base):
    """Сегмент с абсолютной целью редиректа, либо None, если файлового
    редиректа в нём нет."""
    out = list(seg)
    changed = False
    for i, tok in enumerate(seg[:-1]):
        target = seg[i + 1]
        if is_redirect(tok) and target != DEVNULL and not is_separator(target):
            changed = True
            if base and is_relative(target):
                out[i + 1] = join_path(base, target)
    return out if changed else None


def offending(segments, start, base):
    """Причина отказа и переписанные сегменты после cd, либо (None, None)."""
    reason = None
    rewritten = []
    for seg, sep in segments[start:]:
        new = seg
        if seg:
            if seg[0] == "git":
                reason = reason or "git после cd выполняется в чужом каталоге"
                new = ["git", "-C", base] + seg[1:] if base else seg
            elif seg[0] in READERS:
                r = rewrite_reader(seg, base)
                if r is not None:
                    reason = reason or "команда чтения после cd с путём, который классификатору не виден"
                    new = r
            r = rewrite_redirect(new, base)
            if r is not None:
                reason = reason or "редирект вывода в файл после cd"
                new = r
        rewritten.append((new, sep))
    if reason is None:
        return None, None
    return reason, rewritten


def needs_quote(token):
    """Токен в замене берётся в кавычки, если shell раскрыл бы его иначе:
    пробел, кавычка или глоб. Подстановка `$VAR` остаётся как есть."""
    return any(ch in token for ch in " \t\"'*?[")


def render(segments):
    out = []
    for seg, sep in segments:
        out.append(" ".join(shlex.quote(t) if needs_quote(t) else t for t in seg))
        if sep:
            out.append(" " + ("\n" if "\n" in sep else sep) + " " if "\n" not in sep else "\n")
    return "".join(out).strip()


def find_compound(command):
    """Находка по связке с cd: (причина, каталог cd, замена) либо None."""
    parts = tokens(strip_heredocs(command))
    if parts is None:
        return None
    segments = split_segments(parts)
    for idx, (seg, sep) in enumerate(segments):
        if not seg or seg[0] != CD:
            continue
        if not sep or not any(s for s, _ in segments[idx + 1:]):
            continue
        target = seg[1] if len(seg) > 1 else ""
        base = literal_dir(target)
        reason, rewritten = offending(segments, idx + 1, base)
        if reason is None:
            return None
        return reason, target, (render(rewritten) if base else "")
    return None


def report(found):
    reason, target, replacement = found
    lines = []
    if replacement:
        lines.append("Связка с cd отбита рубежом DK-770, повтори ход этой командой: %s" % replacement)
    else:
        lines.append("Связка с cd отбита рубежом DK-770, смени каталог одиноким `cd %s` "
                     "отдельным вызовом Bash и повтори остальное следующим вызовом: "
                     "cwd между вызовами сохраняется." % (target or "<путь>"))
    lines.append("Причина: %s. Классификатор авто-режима не вычисляет каталог после cd, "
                 "поверх стоит запрет на чтение секретов (DK-228), и такой ход уходит "
                 "вопросом к человеку, а автономная сессия встаёт до его прихода." % reason)
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
    found = find_compound(command)
    if not found:
        return 0
    return hookio.reply(protocol).found(report(found))


def run_command(command):
    found = find_compound(command)
    if not found:
        return 0
    sys.stdout.write(report(found))
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
