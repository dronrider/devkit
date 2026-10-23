#!/usr/bin/env python3
"""Онбординг деревьев в профиле подписки: доверие, разложенное заранее.

  python3 onboard.py [--fix]
      сверить .claude.json включённых подписок семейства claude-code с
      деревьями проектов и назвать деревья без отметок; --fix дописывает
      их. Выход 0 доверие на месте, 1 находки, 2 ошибка запуска.

Первую сессию в дереве клиент встречает вопросами онбординга и доверия
папке, и живут они только на экране tmux. Лента панели пуста, и человек
смотрит в неё, а не в окно. Чат задачи DK-536 на свежей подписке routerai
молчал сорок минут, пока человек не заглянул в терминал сам. По решению
02.10 доктор раскладывает отметки заранее, по образцу trust_dirs у замера
квоты. Отметки hasCompletedProjectOnboarding и hasTrustDialogAccepted
пишутся всем деревьям проектов devkit, где дашборд может поднять сессию.
Остальное клиент спрашивает сам.
"""
import json
import say
import sys
from pathlib import Path

import dashboard
import rules

CLIENT_STATE = ".claude.json"
CLAUDE_PROTOCOL = "claude-code"
ONBOARDING = "hasCompletedProjectOnboarding"
TRUST = "hasTrustDialogAccepted"
BOARD = "docs/TASKS.md"
# Три формы слова для счёта в отчёте: одно дерево, два дерева, десять деревьев.
TREE = ("дерево проектов", "дерева проектов", "деревьев проектов")
LIST_HEAD = 6
DEVKIT = Path(__file__).resolve().parent.parent.parent


def has_board(d):
    """Каталог проекта: доска лежит в нём файлом, а не подкаталогом."""
    return (d / BOARD).is_file()


def project_trees(roots):
    """Каталоги, где дашборд поднимает сессии. Проект это корень из конфига
    дашборда либо его прямой подкаталог с доской. Дерево задачи shipctl
    заводит сиблингом `<имя проекта>-<id>` с той же доской. Всех их
    собирает один перечень каталогов с доской."""
    out, seen = [], set()

    def take(d):
        if d not in seen and has_board(d):
            seen.add(d)
            out.append(d)

    for root in roots:
        root = Path(root)
        if not root.is_dir():
            continue
        if has_board(root):
            take(root)
        for child in sorted(root.iterdir()):
            if child.is_dir():
                take(child)
    for d in list(out):
        # Дерево задачи корневого проекта лежит сиблингом корня, вне самого
        # корня, и первый обход его не видит.
        for sib in sorted(d.parent.glob(d.name + "-*")):
            take(sib)
    return out


def untrusted(config, trees):
    """Деревья без записи проекта или без отметки в ней, на них клиент
    спросит человека. Возврат None значит, что файл состояния не
    разбирается. Это беда самого клиента, и доктор обходит её молча, как и
    trust_dirs у замера."""
    if not config.is_file():
        return list(trees)
    try:
        data = json.loads(config.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    if not isinstance(data, dict):
        return None
    projects = data.get("projects")
    if not isinstance(projects, dict):
        return list(trees)
    out = []
    for d in trees:
        entry = projects.get(str(d))
        # Обе отметки в записи. Доверие и онбординг закрывают разные вопросы
        # клиента, и половинный набор подъём всё равно останавливает.
        if not isinstance(entry, dict) or not entry.get(TRUST) or not entry.get(ONBOARDING):
            out.append(d)
    return out


def mark_trusted(config, dirs):
    """Дописать деревьям обе отметки, по образцу trust_dirs. Запись проекта
    заводится при нужде, остальной файл остаётся как был. Возврат это
    записанные деревья, пустой список значит, что файл не разбирается и его
    оставили клиенту."""
    data = {}
    if config.is_file():
        try:
            data = json.loads(config.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return []
        if not isinstance(data, dict):
            return []
    projects = data.get("projects")
    if not isinstance(projects, dict):
        projects = {}
    data["projects"] = projects
    fresh = not config.is_file()
    for d in dirs:
        entry = projects.get(str(d))
        if not isinstance(entry, dict):
            entry = {}
        entry[TRUST] = True
        entry[ONBOARDING] = True
        projects[str(d)] = entry
    config.parent.mkdir(parents=True, exist_ok=True)
    config.write_text(json.dumps(data, ensure_ascii=False), encoding="utf-8")
    if fresh:
        config.chmod(0o600)
    return list(dirs)


def dir_list(dirs, head=LIST_HEAD):
    dirs = list(dirs)
    if len(dirs) <= head:
        return ", ".join(str(d) for d in dirs)
    return ", ".join(str(d) for d in dirs[:head]) + " и ещё %d" % (len(dirs) - head)


def check(fix, profiles, homes, repair, home=None):
    """Профили включённых подписок семейства claude-code против деревьев
    проектов; подписки других протоколов (codex) и вовсе не включённые
    обходятся молча. repair это строка починки, её строит вызывающий по
    своему чекауту. Возврат как у остальных проверок машинного контура,
    пара (находки, починенное)."""
    home = dashboard.default_home() if home is None else Path(home)
    findings, fixed = [], []
    trees = project_trees(dashboard.conf_roots(home))
    if not trees:
        # Корней у дашборда нет, значит и деревьев. Закрывать нечего, и
        # находка про пустоту была бы шумом.
        return findings, fixed
    for name, profile in profiles:
        if profile.str_of("hooks", "protocol") != CLAUDE_PROTOCOL:
            continue
        config = Path(homes.get(name) or home) / CLIENT_STATE
        missing = untrusted(config, trees)
        if not missing:
            continue
        if fix:
            wrote = mark_trusted(config, missing)
            if wrote:
                fixed.append("в профиль подписки %s записано доверие на %s (%s)"
                             % (name, say.counted(len(wrote), TREE), dir_list(wrote)))
            continue
        findings.append("в профиле подписки %s нет онбординга и доверия для деревьев "
                        "проектов (%s): первый подъём задачи встанет на вопросах клиента, "
                        "которых лента панели не видит (DK-1282); %s"
                        % (name, dir_list(missing), repair))
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
    profiles, warns = rules.enabled_harnesses(None, DEVKIT / "kit" / "harness")
    findings, fixed = check(fix, profiles, rules.machine_homes(), "devkitctl doctor --fix")
    for w in warns:
        print(w)
    for m in fixed:
        print("починено: %s" % m)
    for f in findings:
        print(f)
    if findings:
        return 1
    if not fixed:
        print("онбординг деревьев проектов у подписок на месте")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
