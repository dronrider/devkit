#!/usr/bin/env python3
"""Критерий доски для калитки пуша без shipctl в PATH (DK-796).

Коммит доски пушится сразу и без просьбы (RULES.board.md, «Трекинг задач»
п. 9). Диапазон пуша судит shipctl push --check-only, а без установленной
утилиты его зовёт хук pre-push и разбирает пути сам, через этот файл.
Критерий общий с go-копиями: путь сверяется от корня доски
(docs/TASKS.md, docs/TASKS-archive.md, docs/tasks/), доска боковой
директории корп-контура узнаётся подъёмом от каталога файла к ближайшему
docs/TASKS.md, и послабление включает привязка .devkit/tracker.local у
этого корня. Вложенный docs/TASKS.md стенда без привязки остаётся кодом.

Копии критерия правятся вместе: BoardOnlyAt в internal/merged, boardOnly в
tools/shipctl, boardOnlyFiles в tools/taskctl и этот файл.

На вход идут пути git diff --name-only по одному в строке; код выхода ноль,
если каждый путь это доска. Разбор идёт от текущего каталога: git зовёт хуки
из корня рабочего дерева.
"""
import os
import sys

BOARD_FILES = ("docs/TASKS.md", "docs/TASKS-archive.md")
TASKS_DIR = "docs/tasks/"
TRACKER = os.path.join(".devkit", "tracker.local")


def is_board_rel(f):
    """Путь от корня доски: сама доска, архив и файлы задач."""
    return f in BOARD_FILES or f.startswith(TASKS_DIR)


def is_corp_board_rel(root, f):
    """Корп-раскладка: подъём от каталога файла к ближайшему корню доски.

    Ближайший корень без привязки tracker.local гасит обход, и стенд с
    доской внутри обычного проекта остаётся кодом.
    """
    d = os.path.dirname(f)
    while d not in (".", "/", ""):
        absd = os.path.join(root, d)
        if os.path.isfile(os.path.join(absd, "docs", "TASKS.md")):
            if not os.path.isfile(os.path.join(absd, TRACKER)):
                return False
            return is_board_rel(f[len(d) + 1:])
        parent = os.path.dirname(d)
        if parent == d:
            return False
        d = parent
    return False


def is_board_file(root, f):
    """Верно, когда путь это доска домашнего проекта либо боковой директории."""
    return is_board_rel(f) or (bool(root) and is_corp_board_rel(root, f))


def main():
    for line in sys.stdin:
        f = line.strip()
        if f and not is_board_file(".", f):
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
