#!/usr/bin/env python3
"""Литеральные токены в allow-правилах settings.json и резервные копии файла.

  python3 leak.py [--fix]
      сверить настройки харнеса (~/.claude/settings.json) на остатки
      литеральных токенов в теле allow-правил и на резервные копии файла
      рядом; --fix заменяет литералы маской __TRACKED_VAR__ и стирает копии.
      Выход 0 чисто, 1 находки, 2 ошибка запуска.

Цель DK-207 исключает попадание секретов в контекст модели, а allow-правила
живут в открытом тексте settings.json: значение токена в теле правила ездит
в сессию той же строкой, что и любое разрешение. Маска __TRACKED_VAR__ уже
прижита в трёх правилах, и тот же шаблон разносится на остальные. Маска не
резолвится харнесом: правило перестаёт быть рабочим разрешением, но форму
команды хранит без литерала. Резервные копии .bak/.pre несут те же литералы,
что и основной файл, и чистота одного при грязной копии рядом ничего не стоит.
"""
import os
import re
import say
import sys
from pathlib import Path

MASK = "__TRACKED_VAR__"
SETTINGS = "~/.claude/settings.json"
DOCTOR = Path(__file__).resolve().parent / "devkitctl.py"

# Заголовок, за которым идёт значение, а не маска. Негативный lookahead
# пропускает уже прижитую маску, так что чистый файл пуст. Перечень взят из
# реальных правил харнеса: PAT Jira/Confluence и GitLab, токены релиз-конвейера,
# ключ подписи Android-сборки. Новому заголовку токена здесь место, и доктор
# подхватывает его без отдельной правки вызова.
LITERAL_PATTERNS = [
    (re.compile(r"Authorization: Bearer (?!%s)" % re.escape(MASK)),
     "Authorization: Bearer"),
    (re.compile(r"PRIVATE-TOKEN: (?!%s)" % re.escape(MASK)),
     "PRIVATE-TOKEN"),
    (re.compile(r"RELEASE_GITLAB_TOKEN=(?!%s)" % re.escape(MASK)),
     "RELEASE_GITLAB_TOKEN"),
    (re.compile(r"RELEASE_JIRA_TOKEN=(?!%s)" % re.escape(MASK)),
     "RELEASE_JIRA_TOKEN"),
    (re.compile(r"ORG_GRADLE_PROJECT_xrReleasePublicKey='(?!%s')" % re.escape(MASK)),
     "xrReleasePublicKey"),
]

# Подстановка для --fix: заголовок и значение до ближайшей кавычки, запятой или
# пробела сменяются на заголовок и маску. На прижитой маске идемпотентно.
_FIX = [
    (re.compile(r"Authorization: Bearer [^'\" ]+"), "Authorization: Bearer " + MASK),
    (re.compile(r"PRIVATE-TOKEN: [^'\" ]+"), "PRIVATE-TOKEN: " + MASK),
    (re.compile(r"RELEASE_GITLAB_TOKEN=[^ ,'\"}]+"), "RELEASE_GITLAB_TOKEN=" + MASK),
    (re.compile(r"RELEASE_JIRA_TOKEN=[^ ,'\"}]+"), "RELEASE_JIRA_TOKEN=" + MASK),
    (re.compile(r"ORG_GRADLE_PROJECT_xrReleasePublicKey='[^']+'"),
     "ORG_GRADLE_PROJECT_xrReleasePublicKey='" + MASK + "'"),
]

TOKEN_FORM = ("литерал в правиле", "литерала в правилах", "литералов в правилах")
COPY_FORM = ("копия settings.json", "копии settings.json", "копий settings.json")

# Резервные копии settings.json: .bak-* и .pre-* рядом с файлом несут те же
# литералы, что и основной файл, и подлежат удалению.
COPY_HEADS = ("bak", "pre")


def scan_literals(text):
    # Возврат это (заголовок, число) для каждого сработавшего шаблона.
    found = []
    for rx, name in LITERAL_PATTERNS:
        n = len(rx.findall(text))
        if n:
            found.append((name, n))
    return found


def find_copies(settings):
    # .bak-* и .pre-* рядом с settings.json: основное имя плюс суффикс через
    # дефис (settings.json.bak-dk062, settings.json.pre-dk113).
    copies = []
    parent = settings.parent
    base = settings.name
    if parent.is_dir():
        for entry in sorted(parent.iterdir()):
            if not entry.is_file() or entry.name == base:
                continue
            if not entry.name.startswith(base + "."):
                continue
            head = entry.name[len(base) + 1:].split("-", 1)[0]
            if head in COPY_HEADS:
                copies.append(entry)
    return copies


def check(settings, fix=False, worktree_main=None):
    # worktree_main тот же рубеж, что у perms: с ветки задачи на машину правила
    # не едут, только сверяются. Чинить зовут из основного чекаута.
    from_main = worktree_main is None
    doctor = DOCTOR if from_main else Path(worktree_main) / "tools" / "devkitctl" / "devkitctl.py"
    whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, править настройки "
                                   "машины с непроверенной ветки нельзя; из основного чекаута: ")
    findings, fixed = [], []
    settings = Path(os.path.expanduser(str(settings)))
    try:
        text = settings.read_text(encoding="utf-8")
    except FileNotFoundError:
        # Файла нет, и течь в нём нечему: perms считает так же.
        return findings, fixed
    except OSError as e:
        return ["настройки харнеса %s не читаются (%s): проверить остатки литералов "
                "в них руками" % (settings, e)], fixed
    literals = scan_literals(text)
    copies = find_copies(settings)
    if not literals and not copies:
        return findings, fixed
    detail = []
    if literals:
        detail.append(say.counted(sum(n for _, n in literals), TOKEN_FORM)
                      + " (" + ", ".join("%s: %d" % (name, n) for name, n in literals) + ")")
    if copies:
        detail.append(say.counted(len(copies), COPY_FORM))
    detail_text = ", ".join(detail)
    if fix and from_main:
        for rx, replacement in _FIX:
            text = rx.sub(replacement, text)
        settings.write_text(text, encoding="utf-8")
        fixed.append("в %s литералы заменены маской %s" % (settings, MASK))
        for copy in copies:
            copy.unlink()
            fixed.append("удалена %s" % copy)
        return findings, fixed
    findings.append("в %s остались литеральные токены и резервные копии (%s): значение "
                    "токена в теле allow-правила ездит в контекст модели той же строкой, "
                    "и цель DK-207 так не закрывается; починить: %spython3 %s doctor --fix"
                    % (settings, detail_text, whence, doctor))
    return findings, fixed


def main(argv):
    fix = False
    for a in argv:
        if a == "--fix":
            fix = True
        elif a in ("-h", "--help"):
            sys.stdout.write(__doc__)
            return 0
        else:
            sys.stderr.write("неизвестный аргумент %s\n" % a)
            return 2
    findings, fixed = check(SETTINGS, fix)
    for m in fixed:
        print("починено: %s" % m)
    for f in findings:
        print(f)
    if findings:
        return 1
    if not fixed:
        print("литеральных токенов и резервных копий нет")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
