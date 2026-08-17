#!/usr/bin/env python3
"""Вход в доке утилиты: первый раздел README это запуск, а не устройство.

  python3 entry.py <директория проекта>
      напечатать README, чей первый раздел не вход, построчно. Выход 0 чисто,
      1 находки, 2 ошибка запуска.

Правило про вход лежит в `RULES.md`, раздел «Документация изменений»: читатель,
впервые видящий фичу, начинает с первого запуска, а разбор устройства и доводы
решений остаются в LLD. Агент, пишущий README на закрытии задачи, кладёт первым
то, что у него в контексте, а там решение и обоснование, и дефект этот молчит:
сторож полноты доки (DK-071) смотрит наличие README, а не порядок разделов.

Проверяется тут грамматика файла, а не смысл: имя первого заголовка `##`
сверяется с коротким списком имён входа. Понятность текста новичку так не
ловится вовсе, это барьер «глаза» из `ACCEPTANCE.md`, и место ему в ревью и
приёмке. Эвристики глубже (есть ли в разделе блок кода, назван ли наблюдаемый
результат) не заводятся: ложных срабатываний от них больше пользы. `--fix`
находку не чинит, вход пишется руками.
"""
import re
import subprocess
import sys
from pathlib import Path

from describe import CODE_SUFFIXES, FENCE_RE, read_lines

# Имена, с которых начинается вход. Сверка идёт по началу заголовка, а не по
# точному совпадению: у README живут и «Команда names» (secretctl, где команда
# одна), и «Запуск и флаги». Форма единственного числа стоит рядом с
# множественной по той же причине. Список короткий намеренно: чем он длиннее,
# тем ближе он к «любой заголовок сойдёт», а находка тогда не срабатывает
# никогда.
ENTRY_NAMES = ("Первый запуск", "Запуск", "Установка", "Команды", "Команда")
# Каталоги, куда сторож не заходит: чужое, генерённое и стенды тестов. У
# стенда свой README, он материал прогона, а не дока утилиты.
SKIP_DIRS = {".git", ".github", ".devkit", ".idea", ".vscode", ".venv", "venv",
             "__pycache__", "node_modules", "vendor", "target", "build",
             "dist", "out", "obj", "docs", "local-docs", "testdata",
             "test_data", "examples"}
HEADING_RE = re.compile(r"^##\s+(?P<name>.+?)\s*$")
HOW_TO = ("первым разделом идёт вход (%s): что запустить с нуля, с предусловием "
          "и наблюдаемым результатом, а устройство и доводы ниже либо в LLD"
          % ", ".join("«%s»" % n for n in ("Первый запуск", "Запуск", "Команды",
                                           "Установка")))


def has_code(base):
    """Лежит ли код прямо в каталоге: README считается докой утилиты по нему."""
    for item in base.iterdir():
        if item.is_file() and item.suffix in CODE_SUFFIXES:
            return True
    return False


def first_section(text):
    """Имя первого раздела `##` либо None. Заборы кода пропускаются."""
    fence = None
    for ln in text.splitlines():
        m = FENCE_RE.match(ln)
        if m:
            mark = m.group(1)
            if fence is None:
                fence = mark[0]
            elif ln.lstrip().startswith(fence):
                fence = None
            continue
        if fence is not None:
            continue
        m = HEADING_RE.match(ln)
        if m:
            return m.group("name")
    return None


def is_entry(name):
    """Вход ли это: заголовок начинается с одного из имён входа."""
    plain = name.strip().strip("`").lstrip("#").strip()
    return any(plain == n or plain.startswith(n + " ") or plain.startswith(n + ",")
               for n in ENTRY_NAMES)


def readmes(root):
    """README доки утилит: рядом с кодом, не в корне и не в стенде."""
    found = []
    stack = [root]
    while stack:
        base = stack.pop()
        for item in sorted(base.iterdir()):
            if item.is_dir() and item.name not in SKIP_DIRS and not item.is_symlink():
                stack.append(item)
        if base == root:
            continue
        readme = base / "README.md"
        if readme.is_file() and has_code(base):
            found.append(readme)
    return sorted(found)


def check(root):
    """Находки doctor: README утилиты, чей первый раздел не вход."""
    root = Path(root)
    if subprocess.run(["git", "-C", str(root), "rev-parse", "--is-inside-work-tree"],
                      capture_output=True).returncode != 0:
        return []
    findings = []
    for readme in readmes(root):
        text = "\n".join(read_lines(readme))
        name = first_section(text)
        # README без разделов вовсе это короткая дока библиотеки, а не
        # перевёрнутый порядок: судить там нечего.
        if name is None or is_entry(name):
            continue
        findings.append("%s: первый раздел «%s» не вход для читателя, впервые "
                        "видящего утилиту; %s"
                        % (readme.relative_to(root), name, HOW_TO))
    return findings


def main(argv):
    if len(argv) != 2:
        sys.stderr.write(__doc__)
        return 2
    findings = check(argv[1])
    for f in findings:
        print(f)
    if findings:
        sys.stderr.write("находок: %d\n" % len(findings))
        return 1
    print("вход в доке на месте")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
