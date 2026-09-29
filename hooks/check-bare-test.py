#!/usr/bin/env python3
"""Рубеж прямого прогона тестов (DK-1219): PreToolUse-хук на Bash отбивает
голый `go test` и `python3 -m unittest` в дереве проекта devkit и печатает
готовую замену обёрткой `devkitctl test`.

Потолок числа одновременных прогонов на машине держит замок
`~/.devkit/parallel-slots` (DK-1162), и берут его только обёртки: полный
прогон `parallel.py` и частичный `devkitctl test`. Прямая команда идёт мимо
потолка. 2026-09-28 в 19:45 под замком шло слияние, а рядом третья сессия
гоняла `go test` по дереву соседней задачи, и load average держался на 76-89
при десяти ядрах.

Отказ, а не подсказка. Подсказка потолка не держит: агент прочитает её и
повторит ту же команду, потому что своя работа у него идёт, а полку нагрузки
он не видит. Замена собирается из самой команды: каталог берётся у ключа
`-C`, а без него у рабочего каталога хода, и хвост ключей прогона
(`-run TestX`, `-v`) переезжает в замену как есть.

Ловится голая команда, а не любая. Команда под обёрткой (`regcheck -- go test
./...`, `devkitctl test tools/taskctl`, `python3 parallel.py`) проходит: имя
первого слова там не `go` и не питон, и слот такая команда берёт сама либо
берёт её обёртка. Дерево вне devkit проходит тоже: потолок это файл
`~/.devkit/parallel-slots`, а смысл он имеет там, где прогоны сессий сходятся
на одной машине, то есть в проекте с каталогом `.devkit`.

Режимы:
  check-bare-test.py <команда>     проверить команду из аргументов, выход 1 если
                                   в ней нашёлся прямой прогон, иначе 0
  ... | check-bare-test.py --stdin
  check-bare-test.py --hook [протокол]
                                   хук на PreToolUse Bash: JSON события на stdin,
                                   разбирается tool_input.command. Разбор входа и
                                   канал ответа по имени протокола из hookio.py,
                                   голый --hook это claude-code, ответ exit 2
"""
import os
import shlex
import sys

import hookio

SEPARATOR_CHARS = set(";&|\n")
PUNCTUATION = "();<>|&\n"
OPENERS = ("(", "{")
CLOSERS = (")", "}")
# Обёртки, которые ставятся перед командой прогона и сами прогоном не являются:
# их первое слово рубеж пропускает дальше по сегменту, а прогон под ними уже
# взял слот либо возьмёт его обёрткой.
PREFIXES = {"nice", "env", "time", "sudo"}
# Питоновые интерпретаторы, у которых `-m unittest` это прогон сюиты.
PYTHONS = {"python", "python2", "python3"}
# Ключи go test, которые обёртка ставит сама: в замену они не переезжают,
# иначе агент задал бы долю бюджета и потолок времени поверх её расчёта.
GO_OWN_FLAGS = {"-count", "-timeout", "-p", "-C"}
# Аргументы `unittest discover`, которые обёртка ставит сама.
DISCOVER_OWN = {"discover", "-p", "--pattern"}


def tokens(command):
    """Токены команды, разделители отдельными кусками. None значит, что shell
    не разобрался: такой ход рубеж пропускает."""
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


def split_segments(parts):
    """Команда как список сегментов: каждый это список токенов одной команды."""
    segments = []
    cur = []
    for token in parts:
        if is_separator(token) or token in OPENERS or token in CLOSERS:
            if cur:
                segments.append(cur)
            cur = []
        else:
            cur.append(token)
    if cur:
        segments.append(cur)
    return segments


def head(seg):
    """Сегмент без присваиваний окружения и обёрток вида nice: имя команды
    первым элементом. Пустой список значит, что имени в сегменте нет."""
    i = 0
    while i < len(seg):
        tok = seg[i]
        if "=" in tok and not tok.startswith("-") and tok.split("=", 1)[0].isidentifier():
            i += 1
            continue
        if os.path.basename(tok) in PREFIXES:
            i += 1
            continue
        break
    return seg[i:]


def go_target(args):
    """Каталог из ключа `-C` команды go, пустая строка без него."""
    for i, tok in enumerate(args):
        if tok == "-C" and i + 1 < len(args):
            return args[i + 1]
        if tok.startswith("-C="):
            return tok[3:]
    return ""


def keep_go_flags(args):
    """Хвост ключей go test, который переезжает в замену.

    Отбрасываются свои ключи обёртки и списки пакетов: область прогона в
    замене называет каталог, а не шаблон `./...`.
    """
    out = []
    i = 0
    while i < len(args):
        tok = args[i]
        name = tok.split("=", 1)[0]
        if name in GO_OWN_FLAGS:
            i += 1 if "=" in tok else 2
            continue
        if not tok.startswith("-"):
            i += 1
            continue
        out.append(tok)
        i += 1
        # Значение ключа отдельным словом (`-run TestX`) переезжает вместе с
        # ключом, а список пакетов остаётся: область прогона в замене названа
        # каталогом.
        if i < len(args) and "=" not in tok and not is_package(args[i]):
            out.append(args[i])
            i += 1
    return out


def is_package(token):
    """Токен похож на список пакетов go, а не на значение ключа."""
    return (token == "." or token.startswith("-") or "/" in token
            or token.endswith("..."))


def keep_discover_args(args):
    """Хвост аргументов unittest, который переезжает в замену."""
    out = []
    i = 0
    while i < len(args):
        tok = args[i]
        if tok in DISCOVER_OWN:
            i += 2 if tok in ("-p", "--pattern") else 1
            continue
        out.append(tok)
        i += 1
    return out


def literal_dir(target):
    """Каталог `cd`, годный в путь: без подстановок и глоббинга."""
    if not target or any(ch in target for ch in "$`*?"):
        return ""
    return target


def find_bare(command, cwd):
    """Находка прямого прогона: (вид, каталог, хвост ключей) либо None.

    Связка `cd <дерево> && go test ./...` разбирается наравне с голой
    командой: каталог у прогона тогда не рабочий каталог хода, а тот, куда
    ушёл `cd`. Каталог с подстановкой (`cd $ROOT && ...`) не считается, и
    прогон в нём судится по рабочему каталогу.
    """
    parts = tokens(command)
    if parts is None:
        return None
    for seg in split_segments(parts):
        seg = head(seg)
        if not seg:
            continue
        name = os.path.basename(seg[0])
        if name == "cd":
            target = literal_dir(seg[1] if len(seg) > 1 else "")
            if target:
                cwd = os.path.join(cwd or ".", os.path.expanduser(target))
            continue
        if name == "go" and seg[1:2] == ["test"]:
            args = seg[2:]
            target = go_target(args) or cwd
            return "go", target, keep_go_flags(args)
        if name in PYTHONS and "-m" in seg[1:]:
            rest = seg[1:]
            at = rest.index("-m")
            if rest[at + 1:at + 2] == ["unittest"]:
                return "unittest", cwd, keep_discover_args(rest[at + 2:])
    return None


def devkit_tree(path):
    """Каталог `.devkit` выше пути, пустая строка вне дерева проекта devkit."""
    if not path:
        return ""
    path = os.path.abspath(os.path.expanduser(path))
    while True:
        if os.path.isdir(os.path.join(path, ".devkit")):
            return path
        parent = os.path.dirname(path)
        if parent == path:
            return ""
        path = parent


def found_in(command, cwd):
    """Находка с каталогом в дереве проекта devkit либо None."""
    found = find_bare(command, cwd)
    if not found:
        return None
    how, target, extra = found
    target = os.path.abspath(os.path.join(cwd or ".", os.path.expanduser(target)))
    if not devkit_tree(target):
        return None
    return how, target, extra


def report(found):
    how, target, extra = found
    replacement = " ".join(["devkitctl", "test", target] + extra)
    what = "go test" if how == "go" else "python3 -m unittest"
    return ("Прямой %s отбит рубежом DK-1219, повтори ход этой командой: %s\n"
            "Причина: потолок одновременных прогонов на машине держит замок "
            "~/.devkit/parallel-slots, и берут его обёртки. Прямая команда идёт "
            "мимо потолка, и рядом с чужим прогоном машина получает вторую "
            "заявку на все ядра. Обёртка берёт слот, при занятом слоте печатает "
            "строку ожидания и гонит те же тесты с долей бюджета и пониженным "
            "приоритетом.\n" % (what, replacement))


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
    cwd = event.get("cwd")
    found = found_in(command, cwd if isinstance(cwd, str) else "")
    if not found:
        return 0
    return hookio.reply(protocol).found(report(found))


def run_command(command):
    found = found_in(command, os.getcwd())
    if not found:
        return 0
    sys.stdout.write(report(found))
    return 1


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-bare-test: %s\n" % e)
            return 2
    if argv[:1] == ["--stdin"]:
        return run_command(sys.stdin.read())
    if not argv:
        sys.stderr.write(__doc__)
        return 2
    return run_command(" ".join(argv))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
