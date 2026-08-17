#!/usr/bin/env python3
"""Полнота карты переходов работы: docs/workflow.md (DK-407).

  python3 workflow.py <корень devkit>
      напечатать неупомянутое на карте переходов построчно. Выход 0 чисто,
      1 находки, 2 ошибка запуска.

Карта переходов пишется руками, и сторож проверяет полноту упоминаний,
а не истинность рёбер (LLD DK-400, решение 8): протухшей карте верят,
а без сторожа она протухает на первом же новом скилле. Список ожидаемого
не константа здесь, а сами носители: скиллы это каталоги kit/skills/*,
статусы доски это таблица секций taskctl, рубежи это константы F из
taskctl progress. Правка taskctl тогда доезжает до карты тем же прогоном
доктора, без второй точки правды.

Проверяется только чекаут самого devkit (признак тот же, что у раскладки):
в подключённом проекте ни карты, ни скиллов нет, и находка там звала бы
чинить чужое дерево. Источник, который перестал разбираться, сам становится
находкой: молчаливый сторож неотличим от согласного.
"""
import re
import sys
from pathlib import Path

import layout

MAP = Path("docs") / "workflow.md"
BOARD_GO = Path("tools") / "taskctl" / "board.go"
PROGRESS_GO = Path("tools") / "taskctl" / "progress.go"
SKILLS_DIR = Path("kit") / "skills"

# Таблица секций доски в taskctl: строки вида {"## Check", SectCheck} внутри
# sectByPrefix. Имя для человека это текст заголовка после "## ".
SECTION_RE = re.compile(r'\{"## ([^"#]+)"')
# Константы пяти рубежей в taskctl progress: progressBoard = 0.00 и собратья.
# Число ищется тем написанием, каким его печатает утилита, с двумя знаками.
MARK_RE = re.compile(r"\bprogress\w+\s*=\s*([0-9]+\.[0-9]+)\b")

NO_MAP = ("карты переходов %s нет; пишется руками по решению 8 LLD DK-400, "
          "doctor --fix её не генерит" % MAP)
NO_SKILLS = ("скиллы не читаются из %s: каталог скиллов уехал или SKILL.md "
             "переименован, обновить сторожа карты переходов" % SKILLS_DIR)
NO_STATUSES = ("статусы доски не читаются из %s: таблица секций уехала или "
               "переименована, обновить сторожа карты переходов" % BOARD_GO)
NO_MARKS = ("рубежи не читаются из %s: константы progress* уехали или "
            "переименованы, обновить сторожа карты переходов" % PROGRESS_GO)


def _read(path):
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return None


def skills(devkit):
    """Скиллы devkit: каталоги kit/skills/* со своим SKILL.md.

    Пустой список значит, что источник не разбирается.
    """
    return sorted(p.parent.name for p in
                  (Path(devkit) / SKILLS_DIR).glob("*/SKILL.md"))


def statuses(devkit):
    """Статусы доски из таблицы секций taskctl, именами для человека.

    Пустой список значит, что источник не разбирается.
    """
    text = _read(Path(devkit) / BOARD_GO)
    if text is None:
        return []
    return SECTION_RE.findall(text)


def marks(devkit):
    """Рубежи F из констант taskctl progress, написанием как в утилите.

    Пустой список значит, что источник не разбирается.
    """
    text = _read(Path(devkit) / PROGRESS_GO)
    if text is None:
        return []
    return MARK_RE.findall(text)


def check(root):
    """Находки doctor: неупомянутое на карте переходов. Не devkit это пустой
    список, отсутствие карты это находка."""
    root = Path(root)
    if not layout.is_devkit(root):
        return []
    text = _read(root / MAP)
    if text is None:
        return [NO_MAP]
    findings = []
    kit = skills(root)
    if not kit:
        findings.append(NO_SKILLS)
    for name in kit:
        if name not in text:
            findings.append("карта переходов: скилл %s не упомянут" % name)
    board = statuses(root)
    if not board:
        findings.append(NO_STATUSES)
    for name in board:
        if name not in text:
            findings.append("карта переходов: статус доски %s не упомянут" % name)
    progress = marks(root)
    if not progress:
        findings.append(NO_MARKS)
    for mark in progress:
        if mark not in text:
            findings.append("карта переходов: рубеж %s не упомянут" % mark)
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
    print("карта переходов полна")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
