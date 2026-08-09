#!/usr/bin/env python3
"""Проверка текста коммита по разделу «Git» RULES.md: без следов ассистента
(п. 4), одной строкой без body (п. 2), тип префикса из истории проекта (п. 1).
След ищется не одного ассистента: рядом с родовыми «co-authored-by» и
«generated with» стоят подписи ходовых остальных (см. ASSISTANTS). Пятый
рубеж, ID задачи в subject (RULES.board.md, «Ветки, ревью и деплой» п. 7),
стоит только в репозитории с доской (docs/TASKS.md у корня) на ветке main и
только у коммита, который трогает файлы вне доски: доска пушится по ID уже
самим устройством taskctl, а фичеветкам и репозиториям без доски рубеж не
касается.

  git log -n 100 --format=%s | check-commit.py <файл сообщения>

История приходит на stdin. Короткая история (свежий проект, меньше HISTORY_MIN
коммитов) и subject без conventional-префикса проверку префикса выключают: не
всякий проект живёт по conventional commits, а перечень префиксов первых
коммитов нормой проекта ещё не стал. Тип revert разрешён всегда, его пишет shipctl revert на
аварийном пути. Комментарии git и хвост после scissors-строки не смотрятся.
Ветку, доску и задетые файлы проверка спрашивает у git сама (staged-дифф на
момент commit-msg это и есть состав будущего коммита); вне git-репозитория
или без ответа команды рубеж про ID молчит, спросить было негде.
Выход 0 чисто, 1 находки, осознанный обход: git commit --no-verify.
"""
import os
import re
import subprocess
import sys

# Подписи ходовых ассистентов. Родовые «co-authored-by» и «generated with»
# ловят почти всех, но подпись доезжает и без них (автор коммита, строка
# «paired with»), поэтому рядом стоят адреса и боты, снятые с их реальных
# коммитов на github.
ASSISTANTS = (
    "noreply@anthropic",        # Claude Code
    "codex@openai.com",         # Codex
    "noreply@openai.com",       # он же, вторым адресом
    "cursoragent@cursor.com",   # Cursor
    "aider@aider.chat",         # aider
    "openhands@all-hands.dev",  # OpenHands
    "devin-ai-integration",     # Devin
    "copilot-swe-agent",        # GitHub Copilot
    "google-labs-jules",        # Jules
)

TRACES = re.compile("|".join(("co-authored-by", "generated with")
                             + tuple(re.escape(a) for a in ASSISTANTS)), re.I)
PREFIX = re.compile(r"^([a-z]+)(\([^)]+\))?!?: ")
SCISSORS = re.compile(r"^# -+ >8 -+$")
# Ниже этого числа коммитов перечень префиксов истории ещё не норма проекта:
# у свежего проекта там один-два коммита подключения, и любой другой тип
# выглядел бы чужим.
HISTORY_MIN = 10

# ID задачи в subject: перечень префиксов доски (кириллица допущена, у
# board.go idRe тот же класс символов) через дефис и номер.
TASK_ID = re.compile(r"\b[A-ZА-Я]+-[0-9]+\b")

# Три места доски (RULES.board.md, «Трекинг задач» п. 1): открытая работа,
# архив и файлы задач. Коммит, который держится в этих путях целиком, доску
# и правит, а ID ему не нужен, его ставит сама taskctl.
BOARD_FILES = ("docs/TASKS.md", "docs/TASKS-archive.md")
BOARD_DIR = "docs/tasks/"


def is_board_path(path):
    return path in BOARD_FILES or path.startswith(BOARD_DIR)


def touches_non_board(paths):
    return any(not is_board_path(p) for p in paths)


def git(*args):
    """Ответ git одной строкой, либо None, когда спросить было негде: вне
    репозитория, без git в PATH или на ошибке команды. Пустой ответ (например
    git diff без застейдженных файлов) None не считается."""
    try:
        out = subprocess.run(["git"] + list(args), stdout=subprocess.PIPE,
                             stderr=subprocess.DEVNULL, text=True)
    except OSError:
        return None
    if out.returncode != 0:
        return None
    return out.stdout.strip()


def repo_context():
    """Board-репозиторий, ветка main и список задетых файлов, снятые с самого
    git: staged-дифф на момент commit-msg это и есть состав будущего коммита.
    Не-репозиторий, любая ошибка команды и репозиторий без docs/TASKS.md у
    корня отвечают board=False, рубеж про ID тогда молчит."""
    root = git("rev-parse", "--show-toplevel")
    if root is None:
        return False, False, []
    board = os.path.isfile(os.path.join(root, "docs", "TASKS.md"))
    branch = git("symbolic-ref", "--short", "HEAD") or ""
    files = git("diff", "--cached", "--name-only") or ""
    paths = [ln for ln in files.splitlines() if ln.strip()]
    return board, branch == "main", paths


def message_lines(raw):
    lines = []
    for ln in raw.splitlines():
        if SCISSORS.match(ln):
            break
        if ln.startswith("#"):
            continue
        lines.append(ln)
    while lines and not lines[-1].strip():
        lines.pop()
    return lines


def check(lines, history, board=False, on_main=False, paths=()):
    findings = []
    for i, ln in enumerate(lines, 1):
        if TRACES.search(ln):
            findings.append("%d: след ассистента в тексте коммита, строгий запрет (RULES.md, «Git» п. 4): %s\n"
                            "  как переписать: строку убрать целиком, соавторства и упоминания модели\n"
                            "  в истории не бывает; уже закоммиченное правится git commit --amend"
                            % (i, ln.strip()))
    if body := [ln for ln in lines[1:] if ln.strip()]:
        findings.append("коммит пишется одной строкой, body не добавляется (RULES.md, «Git» п. 2), лишних строк: %d\n"
                        "  как переписать: главное из body внести в subject, остальное выбросить;\n"
                        "  что не влезло в строку, обычно просится в отдельный коммит"
                        % len(body))
    # Короткая история это не норма проекта, а случайность первых коммитов: у
    # свежего проекта в ней стоит один «chore» подключения, и первый же
    # «docs(tasks)» доски валился бы с предложением сменить префикс.
    types = set()
    if len(history) >= HISTORY_MIN:
        types = {m.group(1) for s in history if (m := PREFIX.match(s))}
    if lines and types and (m := PREFIX.match(lines[0])):
        if m.group(1) not in types and m.group(1) != "revert":
            findings.append("тип %r не встречается в истории проекта (RULES.md, «Git» п. 1), там: %s\n"
                            "  как переписать: взять префикс из этого перечня, он снят с последней\n"
                            "  сотни коммитов (git log -n 100 --pretty=format:%%s); новый тип заводится\n"
                            "  осознанно, через git commit --no-verify"
                            % (m.group(1), ", ".join(sorted(types))))
    if board and on_main and lines and touches_non_board(paths) and not TASK_ID.search(lines[0]):
        findings.append("нет ID задачи в subject на main проекта с доской (RULES.board.md, «Ветки, ревью и деплой» п. 7): %s\n"
                        "  как переписать: добавить ID вида ABC-123 в subject; коммиту без задачи\n"
                        "  (мелочь вне доски) обход осознанный, git commit --no-verify"
                        % lines[0].strip())
    return findings


def main(argv):
    if len(argv) != 1:
        sys.stderr.write(__doc__)
        return 2
    try:
        with open(argv[0], encoding="utf-8", errors="replace") as f:
            lines = message_lines(f.read())
    except OSError as e:
        sys.stderr.write("check-commit: %s\n" % e)
        return 2
    history = [] if sys.stdin.isatty() else sys.stdin.read().splitlines()
    board, on_main, paths = repo_context()
    findings = check(lines, history, board=board, on_main=on_main, paths=paths)
    for f in findings:
        print(f)
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
