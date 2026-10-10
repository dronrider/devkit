#!/usr/bin/env python3
"""Рубеж ревью: PreToolUse-хук отбивает пуш и создание MR там, где у кода нет
следа ревью.

Правило «ревью по скиллу review» держалось воротами слияния на доске: без строки
уровня в файле задачи `shipctl merge` не сливает. Дорога кода мимо доски этих
ворот не знает: правка уезжает в origin своей веткой, становится MR в чужом
трекере, и ревью там либо шло, либо не шло, а отличить одно от другого нечем.
Рубеж закрывает именно эту дорогу: команду, которая выносит код наружу, он до
запуска отдаёт на суд `shipctl push --check-only`, тому же разбору, каким
пользуется git-хук pre-push, и отбивает её текстом отказа.

След ревью бывает двух видов, и оба ставятся одной командой: git-заметка на
HEAD (`taskctl review level <ярлык> <0-3> "причина"` в репозитории без доски) и
строка уровня в файле задачи ветки доски. Разбор обоих живёт в shipctl, тут его
копии нет.

Хук стоит в каждой сессии на машине, в том числе в чужих проектах, поэтому
молчит везде, где судить не берётся: нет shipctl в PATH, ход идёт вне
git-дерева, диапазон не с чем сравнить. Прямая команда пользователя
(DEVKIT_PUSH_OK=1 в той же команде) снимает рубеж, как снимает и рубеж пуша.

Режимы:
  check-review.py --hook [протокол]
                          хук на PreToolUse: JSON события на stdin, смотрится
                          tool_input.command. Разбор входа и канал ответа по
                          имени протокола из hookio.py, голый --hook это
                          claude-code, ответ exit 2
  check-review.py --why [дерево]
                          что рубеж скажет про это дерево прямо сейчас: чем
                          судит, есть ли след ревью и что было бы с пушем
"""
import os
import shlex
import shutil
import subprocess
import sys

import hookio

TOOL = "Bash"
# Разрешение пользователя: та же переменная, которой снимается рубеж пуша
# (hooks/pre-push). Стоит она в самой команде, и разбирать её отдельно не надо.
PERMIT = "DEVKIT_PUSH_OK"
# Вызов самой проверки: рубеж не судит команду, которая его же и зовёт.
SELF = "--check-only"
SHIPCTL = "shipctl"
# Разделители команд в строке Bash: судится каждый кусок отдельно, иначе
# «cd x && git push» проехало бы мимо.
SEPARATORS = ("&&", "||", ";", "|", "\n")
# Создание MR и PR по трекерам: команда выносит код на чужие глаза так же, как
# пуш, и следа ревью требует того же.
REQUESTS = {"glab": ("mr", "create"), "gh": ("pr", "create"), "tea": ("pr", "create")}
# Глобальные ключи git со значением следующим словом: подкоманда стоит за ними,
# и без этого списка «git -C dir push» разобралось бы как «git -C».
GIT_VALUE_FLAGS = ("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path")
# Ключи git push со значением следующим слова: за ними идут позиционные.
PUSH_VALUE_FLAGS = ("-o", "-x", "--push-option", "--receive-pack", "--exec", "--repo")
# Чем ищется коммит, от которого считается диапазон. Первый разрешившийся и
# берётся: у ветки с отслеживанием это её ремоут, у новой ветки основная ветка
# origin.
BASES = ("@{upstream}", "origin/HEAD", "origin/main", "origin/master")


def segments(command):
    """Куски команды по разделителям оболочки."""
    out = [command]
    for sep in SEPARATORS:
        out = [p for chunk in out for p in chunk.split(sep)]
    return [s.strip() for s in out if s.strip()]


def words(segment):
    """Слова куска. Кривые кавычки это не повод падать: хук стоит на каждом
    ходе Bash, и разбор тут сходит на грубое деление по пробелам."""
    try:
        return shlex.split(segment)
    except ValueError:
        return segment.split()


def git_subcommand(tokens):
    """Подкоманда git, пустая строка у чужой команды. Глобальные ключи и
    присваивания окружения перед именем пропускаются."""
    rest = list(tokens)
    while rest and ("=" in rest[0] and not rest[0].startswith("-")):
        rest.pop(0)
    if not rest or os.path.basename(rest[0]) != "git":
        return ""
    rest.pop(0)
    while rest:
        if rest[0] in GIT_VALUE_FLAGS:
            rest = rest[2:]
            continue
        if rest[0].startswith("-"):
            rest.pop(0)
            continue
        return rest[0]
    return ""


def request_of(tokens):
    """Создание MR или PR: имя утилиты и два её слова. Пустая строка у чужой
    команды."""
    rest = [t for t in tokens if "=" not in t or t.startswith("-")]
    if not rest:
        return ""
    name = os.path.basename(rest[0])
    want = REQUESTS.get(name)
    if not want:
        return ""
    plain = [t for t in rest[1:] if not t.startswith("-")]
    if plain[:2] == list(want):
        return "%s %s %s" % (name, want[0], want[1])
    return ""


def push_positionals(tokens):
    """Позиционные слова git push: repository и refspec. Ключи со значением
    пропускаются парами, голые ключи одним словом. None у команды, где git
    push не разобрать."""
    rest = list(tokens)
    while rest and ("=" in rest[0] and not rest[0].startswith("-")):
        rest.pop(0)
    if not rest or os.path.basename(rest[0]) != "git":
        return None
    rest.pop(0)
    found = False
    while rest:
        if rest[0] in GIT_VALUE_FLAGS:
            rest = rest[2:]
            continue
        if rest[0].startswith("-"):
            rest.pop(0)
            continue
        if rest[0] == "push":
            rest = rest[1:]
            found = True
        break
    if not found:
        return None
    while rest:
        if rest[0] in PUSH_VALUE_FLAGS:
            rest = rest[2:]
            continue
        if rest[0].startswith("-"):
            rest.pop(0)
            continue
        return rest
    return []


def heads_ref(name):
    """Ссылка в виде refs/heads/ветка, пусто у всего, что не ветка."""
    if name.startswith("refs/heads/"):
        return name
    if name.startswith("refs/"):
        return ""
    return "refs/heads/" + name


def branch_ref(tree):
    """refs/heads/текущая ветка, пусто при detached HEAD и без дерева."""
    if not tree:
        return ""
    branch = git_out(tree, "rev-parse", "--abbrev-ref", "HEAD")
    if not branch or branch == "HEAD":
        return ""
    return "refs/heads/" + branch


def ref_exists(tree, ref):
    """Есть ли ref в дереве: по нему одиночное имя refspec отличается от
    ветки (refs/heads/<имя>) от тега или прочего, что судить как ветку
    нельзя."""
    if not tree:
        return False
    try:
        r = subprocess.run(["git", "-C", tree, "show-ref", "--verify", "--quiet", ref],
                           capture_output=True, text=True)
    except OSError:
        return False
    return r.returncode == 0


def push_ref(tree, tokens):
    """Пушимая ссылка в виде refs/heads/ветка, пустая строка когда судить не
    чем: неоднозначный refspec, тег или detached HEAD. По этой ссылке shipctl
    в автономном режиме DK-1324 пускает только ветку задачи, пустая строка
    идёт калитке без аргумента и обход не открывает."""
    pos = push_positionals(tokens)
    if pos is None:
        return ""
    if len(pos) < 2:
        # git push [repository]: пушится текущая ветка.
        return branch_ref(tree)
    if len(pos) > 2:
        # Несколько refspec: одна ссылка тут не судья всего пуша.
        return ""
    spec = pos[1]
    if ":" in spec:
        _, _, dst = spec.partition(":")
        return heads_ref(dst)
    if spec.startswith("refs/"):
        return heads_ref(spec)
    # Одиночное имя: ветка, если локально есть refs/heads/<имя>; иначе это
    # может быть тег, и судить его как ветку нельзя.
    if ref_exists(tree, "refs/heads/"+spec):
        return "refs/heads/" + spec
    return ""


def judge(command, tree):
    """Чем команда выносит код наружу и какую ссылку она пушит. Разрешение
    пользователя и вызов самой проверки снимают суд."""
    for segment in segments(command):
        if PERMIT in segment or SELF in segment:
            continue
        tokens = words(segment)
        if git_subcommand(tokens) == "push":
            return "git push", push_ref(tree, tokens)
        name = request_of(tokens)
        if name:
            # MR уходит из текущей ветки: её пушит и команда, и её судит
            # калитка DK-1324.
            return name, branch_ref(tree)
    return "", ""


def judged(command):
    """Чем команда выносит код наружу, либо пустая строка. Дерева тут нет,
    судить ссылку нечем: это дешёвая обрезка команд, которые рубеж вовсе не
    трогает."""
    what, _ = judge(command, "")
    return what


def git_out(tree, *args):
    """Вывод git в дереве, пустая строка при любом отказе."""
    try:
        r = subprocess.run(["git", "-C", tree] + list(args),
                           capture_output=True, text=True)
    except OSError:
        return ""
    return r.stdout.strip() if r.returncode == 0 else ""


def push_range(tree, target=""):
    """Пара sha для проверки: от известного коммита ремоута до HEAD. None
    значит, что сравнивать не с чем, и рубеж молчит. target это ветка
    назначения пуша: её origin главнее текущей ветки работы, так диапазон
    совпадает с тем, что судит pre-push по строке remote_sha."""
    head = git_out(tree, "rev-parse", "HEAD")
    if not head:
        return None
    first = ()
    if target:
        first = ("origin/" + target,)
    else:
        branch = git_out(tree, "rev-parse", "--abbrev-ref", "HEAD")
        first = (("origin/" + branch,) if branch and branch != "HEAD" else ())
    for name in first + BASES:
        base = git_out(tree, "rev-parse", "--verify", "--quiet", name + "^{commit}")
        if base:
            return base, head
    return None


def verdict(tree, shipctl, ref=""):
    """Приговор shipctl по диапазону: пустая строка значит «пропустить».
    Зовётся тем же способом, что из git-хука pre-push, и разбора диапазона тут
    своего нет. ref это пушимая ссылка (refs/heads/ветка): её shipctl судит в
    автономном режиме DK-1324, пустая строка идёт без аргумента и обход не
    открывает."""
    target = ref[len("refs/heads/"):] if ref.startswith("refs/heads/") else ""
    pair = push_range(tree, target)
    if not pair:
        return ""
    cmd = [shipctl, "push", "--check-only", pair[0], pair[1]]
    if ref:
        cmd.append(ref)
    try:
        r = subprocess.run(cmd, cwd=tree, capture_output=True, text=True)
    except OSError:
        return ""
    if r.returncode == 0:
        return ""
    return (r.stderr + r.stdout).strip() or "shipctl отказал без текста"


def report(what, text):
    """Отказ: чем команда судима и что ответил shipctl. Своего разбора причины
    тут нет, поэтому и пересказывать её нечем: приговор едет как есть."""
    return "\n".join([
        "%s отбит рубежом ревью: код агента не уходит в origin и не становится "
        "MR без ревью по скиллу review, свежим контекстом, а не тем, который "
        "писал правку." % what,
        text,
    ]) + "\n"


def run_hook(protocol):
    try:
        event = hookio.load()
    except hookio.BadEvent:
        return 0
    ti = event.get("tool_input")
    if not isinstance(ti, dict) or hookio.text_of(event.get("tool_name")) != TOOL:
        return 0
    command = hookio.text_of(ti.get("command"))
    # Дешёвая обрезка без дерева: судима ли команда вообще.
    if not judged(command):
        return 0
    shipctl = shutil.which(SHIPCTL)
    if not shipctl:
        return 0
    tree = hookio.tree_root(hookio.text_of(event.get("cwd")) or os.getcwd())
    if not tree:
        return 0
    what, ref = judge(command, tree)
    if not what:
        return 0
    text = verdict(tree, shipctl, ref)
    if not text:
        return 0
    return hookio.reply(protocol).found(report(what, text))


def run_why(argv, out):
    """Что рубеж скажет про дерево прямо сейчас. Без этого режима человеку
    нечем отличить «след ревью на месте» от «рубеж молчит, потому что судить
    нечем»."""
    tree = hookio.tree_root(argv[0] if argv else os.getcwd())
    if not tree:
        out.write("тут не git-дерево: рубеж молчит\n")
        return 0
    shipctl = shutil.which(SHIPCTL)
    if not shipctl:
        out.write("shipctl в PATH нет: рубеж молчит, судить пуш нечем\n")
        return 0
    pair = push_range(tree)
    if not pair:
        out.write("в %s не с чем сравнить HEAD (нет коммита ремоута): рубеж молчит\n" % tree)
        return 0
    text = verdict(tree, shipctl, branch_ref(tree))
    if not text:
        out.write("диапазон %s..%s проходит: пуш и MR рубеж пропустит\n"
                  % (pair[0][:7], pair[1][:7]))
        return 0
    out.write("диапазон %s..%s отбивается:\n%s\n" % (pair[0][:7], pair[1][:7], text))
    return 0


def main(argv):
    if argv[:1] == ["--hook"]:
        try:
            return run_hook(hookio.protocol(argv[1:]))
        except hookio.Unknown as e:
            sys.stderr.write("check-review: %s\n" % e)
            return 2
    if argv[:1] == ["--why"]:
        return run_why(argv[1:], sys.stdout)
    sys.stderr.write(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
