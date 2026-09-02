#!/usr/bin/env python3
"""Подключение проекта и диагностика обвязки: new, doctor по проекту и по
машинному контуру, обвязка выката, рубеж основного чекаута и сводка запусков.
"""
import json
import os
import platform
import re
import shutil
import tempfile
import time
import unittest
from pathlib import Path

import devkitctl
from testenv import (BINARY_STUB, BREW_STUB, GO_STUB, SandboxCase, build, executable,
                     fake_home, git, git_init, go_cache_env, harness, read, rules, run,
                     stub_release, taken_at, update, write)

MARKER = re.compile(r"^<!-- devkit:generated body=[0-9a-f]{12} -->$")

LOG = ("2026-07-29T01:02:41\tshipctl\tmerge\t0\n"
       "2026-07-29T01:02:41\tshipctl\tmerge\t0\n"
       "2026-07-29T01:02:40\ttaskctl\tmove\t0\n"
       "2026-07-29T01:02:40\ttaskctl\tmove\t0\n"
       "2026-07-29T01:02:40\ttaskctl\tmove\t0\n"
       "2026-07-29T01:02:40\ttaskctl\tmove\t1\n"
       "2026-07-29T01:02:41\tregcheck\trun\t1\n"
       "2026-07-29T01:02:41\tbroken\tline\tbroken\n"
       "2026-07-29T01:02:41\tbroken\tcode\tbad\n")


def drop_lines(path, needle):
    write(path, "".join(ln + "\n" for ln in read(path).split("\n")
                        if ln and needle not in ln))


class EnvironmentTest(SandboxCase):

    def test_path_carries_the_tools(self):
        # Системная часть PATH подставная, и самопроверке не на чем стоять, если
        # чего-то из неё на машине нет.
        self.assertEqual(self.box.missing_tools, [], "в PATH нет инструментов самопроверки")


class NewProjectTest(SandboxCase):
    """new: AGENTS.md из шаблона, тонкий CLAUDE.md генератором, hooksPath на
    хуки devkit, повторный запуск отбит.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.rc, cls.out = cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))

    def test_new_lays_out_the_project(self):
        self.assertEqual(self.rc, 0, "new не прошёл: %s" % self.out)
        agents = read(self.proj / "AGENTS.md")
        self.assertNotIn("<название проекта>", agents, "плейсхолдер названия не заменён")
        self.assertFalse([ln for ln in agents.split("\n") if ln.startswith("@")],
                         "в AGENTS.md из шаблона есть строка импорта")
        thin = read(self.proj / "CLAUDE.md")
        self.assertRegex(thin.split("\n")[0], MARKER, "у тонкого CLAUDE.md нет маркера с хешем")
        self.assertIn("@AGENTS.md", thin.split("\n"), "тонкий CLAUDE.md не ссылается на AGENTS.md")
        self.assertIn("@.devkit/devkit/RULES.md", thin.split("\n"),
                      "тонкий CLAUDE.md не ссылается на правила devkit")
        self.assertNotIn("RULES.board.md", thin, "проекту без доски выписаны правила доски")
        # Правила зовутся через ссылку на дерево devkit: путь наружу клиент не
        # разворачивает и молчит об этом (DK-193).
        link = self.proj / rules.LINK_DIR / rules.DEVKIT_LINK
        self.assertTrue(link.is_symlink(), "подключение не положило ссылку на devkit")
        self.assertTrue((link / "RULES.md").is_file(),
                        "ссылка на devkit ведёт мимо дерева правил: %s" % os.readlink(str(link)))
        hooks = git(self.proj, "config", "core.hooksPath")[1].strip()
        self.assertTrue(hooks, "hooksPath не выставлен")
        self.assertTrue((self.proj / hooks / "pre-commit").is_file(),
                        "hooksPath смотрит мимо хуков devkit: %s" % hooks)

    def test_second_new_is_refused(self):
        rc, out = self.box.dkctl_run("new", "--no-board", "-C", str(self.proj))
        self.assertEqual(rc, 2, "повторный new не отбит: %s" % out)

    def test_fresh_project_is_clean(self):
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 0, "doctor нашёл находки на чистом проекте: %s" % out)


class ProjectFindingsTest(SandboxCase):
    """Находки по проекту и по хукам харнеса. Проверки идут цепочкой по одной
    раскладке, как их гонял sh-раннер: часть шагов режет настройки харнеса
    построчно, а следующий стоит на том, что осталось.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.thin_gen = read(cls.proj / "CLAUDE.md")
        # Заметка живой доки: ссылки проверяются в ней, а не в docs/, где текст
        # стареет по правилу (граница в check_links).
        cls.note = cls.proj / "note.md"
        cls.settings = cls.box.home / ".claude" / "settings.json"

    def test_1_broken_import_and_link(self):
        write(self.proj / "CLAUDE.md", self.thin_gen + "@../devkit/NOPE.md\n")
        write(self.note, "смотри [детали](nope.md)\n")
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 1, "doctor не увидел поломок")
        self.assertIn_("импорт @../devkit/NOPE.md не разворачивается", out,
                       "нет находки про битый импорт")
        write(self.proj / "CLAUDE.md", self.thin_gen)
        self.assertIn_("битая ссылка", out, "нет находки про битую ссылку")

    def test_2_link_inside_code_is_not_a_link(self):
        # Документированная команда с [текст](путь) в теле не должна красить
        # доктор ни забором, ни инлайн-кодом.
        write(self.note, "пример:\n\n````\n```\nсмотри [детали](nope.md)\n```\n````\n")
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("битая ссылка", out, "ссылка в блоке кода принята за настоящую")
        write(self.note, "пример команды `смотри [детали](nope.md)` в строке\n")
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("битая ссылка", out, "ссылка в инлайн-коде принята за настоящую")

    def test_3_four_spaces_and_the_memory_hook(self):
        # Отступ в четыре пробела забор уже не открывает (CommonMark), ссылка под
        # ним остаётся настоящей.
        write(self.note, "пример:\n\n    ```\nсмотри [детали](nope.md)\n")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("битая ссылка", out, "четыре пробела приняты за забор блока кода")
        write(self.note, "смотри [детали](nope.md)\n")
        drop_lines(self.settings, "check-memory")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("check-memory", out, "нет находки про пропавший хук памяти")

    def test_4_quota_refresh_hook(self):
        # Хук освежения квоты живёт в другом событии, и проверяться должен своей
        # строкой.
        drop_lines(self.settings, "quota-refresh")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("SessionStart-хук quota-refresh.sh", out,
                       "нет находки про хук освежения квоты")

    def test_4b_session_task_hook(self):
        # Реестр чатов стоит на том же событии, что освежение квоты, и находка
        # про него своя: без записи дашборд угадывает задачу по транскрипту.
        # Needle держит --hook, а не голое имя файла: тот же скрипт вторым
        # вызовом стоит на PostToolUse (--touch, DK-539), и голое имя срезало бы
        # обе строки разом.
        drop_lines(self.settings, "session-task.py --hook")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("SessionStart-хук session-task.py", out,
                       "нет находки про хук реестра чатов")
        self.assertIn_("реестр чатов", out, "находка не говорит, что ломается без хука")

    def test_4b2_session_task_touch_hook(self):
        # Отметка работы по факту правки (DK-539) стоит на ходе инструмента, а
        # не на рождении сессии, и находка про неё своя: без хука правка файла в
        # боковом дереве задачи не попадает в журнал сессий вовсе. --fix этой
        # раскладки проверен отдельно, в HarnessHooksTest: тут цепочка режет
        # settings.json построчно, а --fix переписал бы файл целиком и увёл
        # следующие шаги цепочки от их собственных строк.
        drop_lines(self.settings, "session-task.py --touch")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("PostToolUse-хук session-task.py --touch", out,
                       "нет находки про хук отметки работы")
        self.assertIn_("не оставляет отметку в журнале сессий", out,
                       "находка не говорит, что ломается без хука отметки работы")

    def test_4c_board_catchup_hook(self):
        # Догон бокового дерева доски стоит на том же старте сессии, и находка
        # про него своя: без хука отставание дерева не видно вовсе.
        drop_lines(self.settings, "board-catchup")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("SessionStart-хук board-catchup.sh", out,
                       "нет находки про хук догона бокового дерева")
        self.assertIn_("устаревшая доска читается как свежая", out,
                       "находка не говорит, что ломается без хука")

    def test_4d_devkit_catchup_hook(self):
        # Догон самого чекаута devkit стоит там же, и находка про него своя: без
        # хука правка со второй машины лежит в origin невостребованной.
        drop_lines(self.settings, "devkit-catchup")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("SessionStart-хук devkit-catchup.sh", out,
                       "нет находки про хук догона чекаута devkit")
        self.assertIn_("правка со второй машины", out,
                       "находка не говорит, что ломается без хука")

    def test_5_notifier_events(self):
        # Уведомитель висит на четырёх событиях сразу, и пропажа любого это
        # находка: без SubagentStop сессия молчит про отработавшего субагента, а
        # без UserPromptSubmit не снимается отметка ожидания.
        full = read(self.settings)
        data = json.loads(full)
        del data["hooks"]["SubagentStop"]
        del data["hooks"]["UserPromptSubmit"]
        write(self.settings, json.dumps(data, ensure_ascii=False, indent=1))
        _, out = self.box.doctor(self.proj)
        self.assertIn_("notify.py не подключён на события SubagentStop, UserPromptSubmit", out,
                       "нет находки про неподключённый хук субагента")
        self.assertNotIn_("события Notification", out, "подключённое событие попало в находку")
        write(self.settings, full)
        drop_lines(self.settings, "notify.py")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("notify.py не подключён на события Notification, Stop, StopFailure, "
                       "SubagentStop, UserPromptSubmit", out,
                       "нет находки про неподключённый уведомитель")
        write(self.settings, full)

    def test_5c_agent_watchdog_events(self):
        # Сторож фоновых субагентов висит на трёх событиях, и пропажа любого это
        # находка: без запуска счёт работам не ведётся, без конца хода сдавать
        # нечего, а сессия уходит спать с незабранным отчётом (DK-519).
        full = read(self.settings)
        drop_lines(self.settings, "agent-watch.py")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("сторож agent-watch.py не подключён на события PostToolUse, "
                       "SubagentStop, Stop", out,
                       "нет находки про неподключённого сторожа субагентов")
        self.assertIn_("сессия уходит спать, считая его работающим", out,
                       "находка не говорит, что ломается без сторожа")
        write(self.settings, full)
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("agent-watch.py не подключён", out,
                          "подключённый сторож попал в находку")

    def test_5b_retry_watchdog_key(self):
        # Без env-ключа недокументированного ретрай-вотчдога доктор называет
        # это находкой (стенд DK-172 разницы в поведении с ключом не нашёл, но
        # находка про пробел раскладки от этого не отменяется); значение
        # человека (хоть "0") находкой не считается.
        full = read(self.settings)
        data = json.loads(full)
        del data["env"][devkitctl.WATCHDOG_KEY]
        write(self.settings, json.dumps(data, ensure_ascii=False, indent=1))
        _, out = self.box.doctor(self.proj)
        self.assertIn_("нет env-ключа %s" % devkitctl.WATCHDOG_KEY, out,
                       "нет находки про отсутствующий ретрай-вотчдог")
        self.assertIn_("недокументированный задел", out,
                       "находка не говорит, что ключ недокументирован")
        data["env"][devkitctl.WATCHDOG_KEY] = "0"
        write(self.settings, json.dumps(data, ensure_ascii=False, indent=1))
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("env-ключа", out, "доктор спорит со значением, вписанным человеком")
        write(self.settings, full)

    def test_6_no_notification_backend(self):
        # Слать нечем: бэкенда на платформе нет (PATH подставной, переменная
        # снята), и доктор называет это находкой с командой самопроверки.
        _, out = self.box.doctor(self.proj, env={"DEVKIT_NOTIFY_BACKEND": None})
        self.assertIn_("уведомлять нечем", out, "нет находки про отсутствие бэкенда уведомлений")
        self.assertIn_("notify.py --self-test", out, "в находке про бэкенд нет команды проверки")

    @unittest.skipUnless(platform.system() == "Darwin", "клик по баннеру поддержан только на macOS")
    def test_7_click_goes_to_finder(self):
        # Слать есть чем, но клик по баннеру уводит в Finder: доктор зовёт
        # поставить отправителя с переходом, а ставит его --fix (DK-157), и в
        # подставном PATH пакетного менеджера нет. Случай ровно macOS-ный, на
        # другой платформе клик не поддержан ни одним бэкендом.
        osascript = executable(self.box.root / "osascript")
        _, out = self.box.doctor(self.proj, env={"DEVKIT_NOTIFY_BACKEND": osascript})
        self.assertIn_("клик по баннеру ведёт не в окно сессии", out,
                       "нет находки про клик мимо окна сессии")
        self.assertIn_("terminal-notifier", out,
                       "в находке про клик нет отправителя, которым чинится переход")
        notifier = executable(self.box.root / "terminal-notifier")
        _, out = self.box.doctor(self.proj, env={"DEVKIT_NOTIFY_BACKEND": notifier})
        self.assertNotIn_("клик по баннеру", out,
                          "находка про клик осталась при отправителе, который клик умеет")

    def test_8_docs_are_not_checked(self):
        # Файл задачи описывает состояние на момент решения и после закрытия не
        # правится, поэтому ссылка на уехавший путь там законна. Проверка ссылок
        # знает эту границу: живая дока проверяется, docs/ стареет (DK-144).
        write(self.note, "живая дока без ссылок\n")
        write(self.proj / "docs" / "tasks" / "XX-1.md", "смотри [код](../../nope/gone.go)\n")
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("битая ссылка", out, "проверка ссылок полезла в docs/")
        write(self.note, "смотри [детали](nope.md)\n")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("битая ссылка", out, "живая дока перестала проверяться вовсе")

    def test_9_hooks_structurally_odd(self):
        # settings.json правят и человек, и чужие инструменты, и структура в нём
        # бывает любая: доктор на таком обязан давать находку и код 1, а не стек
        # (DK-126). Синтаксис цел, форма нет: три случая из сценария задачи.
        full = read(self.settings)
        cases = (
            ("число вместо объекта hooks", '{"hooks": 42}'),
            ("строка вместо списка групп", '{"hooks": {"PostToolUse": "abc"}}'),
            ("элемент группы не словарь", '{"hooks": {"PostToolUse": [42]}}'),
        )
        for why, body in cases:
            write(self.settings, body)
            rc, out = self.box.doctor(self.proj)
            self.assertNotIn_("Traceback", out, "%s уронили доктор стеком" % why)
            self.assertEqual(rc, 1, "%s не дали находки" % why)
            self.assertIn_("структура hooks", out, "нет находки про структуру hooks: %s" % why)
        write(self.settings, full)

    def test_10_fix_on_odd_hooks(self):
        # На той же структуре и --fix не падает: чинить раскладку хуков поверх
        # нельзя, поэтому только находка, а само поле hooks фикс не трогает
        # (DK-126). Прочие части --fix, вроде прав permissions, файл переписывают,
        # и это их дело, а не подтверждение, что структура hooks починилась.
        full = read(self.settings)
        write(self.settings, '{"hooks": 42}')
        rc, out = self.box.doctor(self.proj, "--fix")
        self.assertNotIn_("Traceback", out, "--fix на странной структуре уронил доктор стеком")
        self.assertEqual(rc, 1, "--fix на странной структуре не дал находки")
        self.assertIn_("структура hooks", out, "нет находки про структуру hooks под --fix")
        data = json.loads(read(self.settings))
        self.assertEqual(data.get("hooks"), 42, "--fix починил или убрал поле hooks")
        write(self.settings, full)


class DoctorRootTest(SandboxCase):
    """Корень вне git: проектная половина (правила, git-хуки, доска, обвязка
    выката, ссылки) по дереву вниз не идёт, а машинный контур и строка про
    чекаут печатаются как обычно. Исключение docs/ у check_links считается от
    корня репозитория файла, а не от каталога запуска, и распознаёт репозиторий
    и по каталогу .git, и по файлу gitdir: (worktree, submodule) (DK-160).
    """

    def test_outside_git_refuses_without_walking_the_tree(self):
        # Каталог без git, а ниже по дереву чужой репозиторий с битой ссылкой и
        # в docs/, и в живой доке: находка хоть по одной значила бы, что доктор
        # всё равно пошёл вниз, хотя корень уже не проект.
        outside = self.box.root / "workspace"
        outside.mkdir()
        neighbour = git_init(outside / "neighbour")
        write(neighbour / "README.md", "смотри [детали](nope.md)\n")
        write(neighbour / "docs" / "tasks" / "XX-1.md", "смотри [код](../../nope/gone.go)\n")
        rc, out = self.box.doctor(outside)
        self.assertEqual(rc, 1, "doctor не отбит кодом на каталоге без git: %s" % out)
        self.assertIn_("не git-репозиторий", out, "нет находки про отсутствие репозитория")
        self.assertIn_("подключённого проекта", out, "находка не говорит, куда звать доктора")
        self.assertNotIn_("битая ссылка", out, "доктор пошёл вниз по чужому репозиторию")

    def test_outside_git_still_runs_the_machine_half(self):
        # Машинный контур и строка про режим чекаута от root не зависят, и
        # каталог без git это не повод их гасить: их печатал доктор и раньше,
        # и человек, позвавший его из каталога проектов, часто спрашивает ровно
        # про машину (замечание ревью).
        outside = self.box.root / "workspace2"
        outside.mkdir()
        _, out = self.box.doctor(outside)
        self.assertIn_("чекаут devkit:", out, "вне git пропала строка про режим чекаута")
        self.assertNotIn_("битая ссылка", out, "вне git проверка ссылок всё равно отработала")

    def test_nested_repo_docs_excluded_by_its_own_root(self):
        # Вложенный репозиторий внутри проверяемого дерева, а не сам каталог
        # запуска: его docs/ исключается тем же правилом, что и docs/ корня,
        # иначе путь до него от корня запуска на "docs/" не начинается и
        # исключение мимо (DK-160).
        proj = self.box.project("proj")
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        nested = git_init(proj / "nested")
        write(nested / "README.md", "смотри [код](nope.md)\n")
        write(nested / "docs" / "tasks" / "XX-1.md", "смотри [код](../../nope/gone.go)\n")
        _, out = self.box.doctor(proj)
        self.assertIn_("nested/README.md", out, "живая дока вложенного репозитория не проверена")
        self.assertNotIn_("nested/docs", out, "docs/ вложенного репозитория не исключён")

    def test_nested_worktree_docs_excluded_too(self):
        # git-worktree: .git это файл со строкой "gitdir: ...", не каталог, и
        # is_dir() такой репозиторий бы не признал своим, а walk продолжил бы
        # вниз без исключения docs/ (замечание ревью).
        proj = self.box.project("proj")
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        main = git_init(proj / "wt-main")
        write(main / "seed.txt", "x\n")
        git(main, "add", "-A")
        git(main, "commit", "-qm", "seed")
        rc, out = git(main, "worktree", "add", str(proj / "wt"))
        self.assertEqual(rc, 0, "git worktree add не прошёл: %s" % out)
        self.assertTrue((proj / "wt" / ".git").is_file(), "у worktree .git не файл")
        write(proj / "wt" / "README.md", "смотри [код](nope.md)\n")
        write(proj / "wt" / "docs" / "tasks" / "XX-1.md", "смотри [код](../../nope/gone.go)\n")
        _, out = self.box.doctor(proj)
        self.assertIn_("wt/README.md", out, "живая дока worktree не проверена")
        self.assertNotIn_("wt/docs", out, "docs/ worktree не исключён")

    def test_nested_submodule_style_docs_excluded_too(self):
        # git-submodule: .git это тоже файл со строкой "gitdir: ...", а не
        # каталог, как у worktree; собирается руками, без сети, чтобы не тянуть
        # настоящий git submodule add в стенд (замечание ревью).
        proj = self.box.project("proj")
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        sub = proj / "submod"
        write(sub / ".git", "gitdir: ../.git/modules/submod\n")
        write(sub / "README.md", "смотри [код](nope.md)\n")
        write(sub / "docs" / "tasks" / "XX-1.md", "смотри [код](../../nope/gone.go)\n")
        _, out = self.box.doctor(proj)
        self.assertIn_("submod/README.md", out, "живая дока submodule-стиля не проверена")
        self.assertNotIn_("submod/docs", out, "docs/ submodule-стиля не исключён")


class DeployTest(SandboxCase):
    """Обвязка выката: болванка deploy.local, находки по пустым ключам и
    дописывание недостающих (DK-053, DK-075). Проверки идут цепочкой по одному
    проекту, как их гонял sh-раннер.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        # Доску заводит taskctl, а в подставном PATH он заглушка на «exit 0»:
        # кладём рядом ту, что скелет доски всё-таки пишет.
        boardbin = cls.box.root / "boardbin"
        boardbin.mkdir()
        executable(boardbin / "taskctl", cls.box.board_taskctl())
        cls.boardpath = "%s:%s" % (boardbin, cls.box.cleanpath)
        cls.bproj = cls.box.project("bproj")
        cls.brc, cls.bout = cls.box.dkctl_run("new", "--prefix", "BP", "-C", str(cls.bproj),
                                              path=cls.boardpath)
        cls.deploy = cls.bproj / ".devkit" / "deploy.local"
        cls.fproj = None

    def sysdoctor(self, root, *args):
        return self.box.doctor(root, *args, path=str(self.box.sys))

    def test_01_board_without_taskctl(self):
        # Доска без taskctl в PATH это находка (PATH обрезан до системного).
        write(self.proj / "docs" / "TASKS.md", "# Задачи\n")
        _, out = self.sysdoctor(self.proj)
        self.assertRegex(out, r"утилит devkit не в PATH:[^\n]*taskctl", "нет находки про taskctl")

    def test_02_new_with_board_lays_out_the_deploy_stub(self):
        self.assertTrue(self.deploy.is_file(), "new не завёл .devkit/deploy.local")
        # Хвост подключения (DK-125): чего команда не сделала за человека, она
        # называет сама, с путём файла и ключами.
        self.assertIn_("осталось сделать", self.bout, "new не напечатал, что осталось сделать")
        root = os.path.realpath(str(self.bproj))
        self.assertIn_("%s/.devkit/deploy.local: вписать test =, deploy =" % root, self.bout,
                       "хвост new не назвал незаполненную обвязку выката")
        self.assertIn_("проверить обвязку можно командой devkitctl doctor -C %s" % root, self.bout,
                       "хвост new не назвал команду проверки")
        text = read(self.deploy)
        self.assertRegex(text, r"(?m)^autonomous = false$", "в болванке нет autonomous")
        self.assertRegex(text, r"(?m)^test =$", "в болванке нет пустого ключа test")
        self.assertEqual(git(self.bproj, "check-ignore", "-q", ".devkit/deploy.local")[0], 0,
                         ".devkit/deploy.local не гитигнорнут")

    def test_03_run_log(self):
        # Журнал запусков: new записал свою строку в .devkit/log, файл гитигнорнут.
        log = self.bproj / ".devkit" / "log"
        self.assertTrue(log.is_file(), "new не записал журнал запусков")
        self.assertIn("devkitctl\tnew\t0", read(log), "в журнале нет строки про new")
        self.assertEqual(git(self.bproj, "check-ignore", "-q", ".devkit/log")[0], 0,
                         ".devkit/log не гитигнорнут")

    def test_04_empty_keys_are_findings(self):
        _, out = self.sysdoctor(self.bproj)
        self.assertIn_("пустой deploy=", out, "нет находки про пустую команду выката")
        self.assertIn_("пустой test=", out, "нет находки про пустую команду тестов")
        # Команда выката вписана, а тестов нет: находка про test остаётся одна.
        write(self.deploy, "deploy = make deploy\nautonomous = false\n")
        _, out = self.sysdoctor(self.bproj)
        self.assertNotIn_("пустой deploy=", out,
                          "находка про пустой deploy осталась при вписанной команде")
        self.assertIn_("пустой test=", out, "находка про пустой test пропала вместе с deploy")
        write(self.deploy, "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(self.bproj)
        self.assertNotIn_("deploy", out, "заполненная обвязка выката всё ещё в находках")

    def test_05_autonomous_without_deploy(self):
        write(self.deploy, "autonomous = true\n")
        _, out = self.sysdoctor(self.bproj)
        self.assertIn_("autonomous = true при пустом deploy=", out,
                       "нет находки про autonomous=true без команды")
        self.assertNotIn_("shipctl нечего выкатывать", out,
                          "старая находка дублирует новую при autonomous=true")
        # autonomous=false с пустым deploy= это старая находка, без новой.
        write(self.deploy, "autonomous = false\n")
        _, out = self.sysdoctor(self.bproj)
        self.assertNotIn_("autonomous = true при пустом deploy=", out,
                          "новая находка не должна быть для autonomous=false")
        self.assertIn_("пустой deploy=", out, "старая находка про пустой deploy должна остаться")

    def test_06_config_is_not_gitignored(self):
        nproj = self.box.project("nproj")
        self.box.dkctl_run("new", "--no-board", "-C", str(nproj))
        write(nproj / "docs" / "TASKS.md", "# Задачи\n")
        write(nproj / ".devkit" / "deploy.local", "deploy = make deploy\nautonomous = false\n")
        _, out = self.sysdoctor(nproj)
        self.assertIn_("не гитигнорнут", out, "нет находки про негитигнорнутый конфиг")

    def test_07_fix_finishes_an_old_project(self):
        # doctor --fix доводит обвязку проекта, подключённого до появления
        # выката: заводит deploy.local с гитигнором и возвращает отвязанные хуки.
        fproj = self.box.project("fproj")
        self.__class__.fproj = fproj
        self.__class__.fdeploy = fproj / ".devkit" / "deploy.local"
        self.box.dkctl_run("new", "--prefix", "FP", "-C", str(fproj), path=self.boardpath)
        self.fdeploy.unlink()
        git(fproj, "config", "--unset", "core.hooksPath")
        _, out = self.box.doctor(fproj, "--fix")
        self.assertIn_("починено", out, "doctor --fix ничего не починил")
        self.assertTrue(self.fdeploy.is_file(), "doctor --fix не завёл deploy.local")
        self.assertEqual(git(fproj, "check-ignore", "-q", ".devkit/deploy.local")[0], 0,
                         "doctor --fix не гитигнорил deploy.local")
        self.assertTrue(git(fproj, "config", "core.hooksPath")[1].strip(),
                        "doctor --fix не подключил хуки")
        # Пустой deploy= остаётся находкой: команду выката --fix не выдумывает.
        self.assertIn_("пустой deploy=", out, "doctor --fix должен просить вписать команду")
        # Повторный --fix уже ничего не меняет (идемпотентность).
        _, out = self.box.doctor(fproj, "--fix")
        self.assertNotIn_("починено", out, "повторный doctor --fix не должен ничего менять")
        # Заполненные команды: находки по выкату уходят.
        write(self.fdeploy, "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.box.doctor(fproj)
        self.assertNotIn_("deploy", out, "заполненная обвязка выката всё ещё в находках")

    def test_08_missing_key_is_appended(self):
        # Файл проекта, подключённого до появления test= (DK-053), содержит
        # только deploy и autonomous. --fix дописывает test= в конец, своя шапка,
        # значения deploy и autonomous и их порядок не тронуты.
        write(self.fdeploy, "# моя шапка, не трогать\ndeploy = ssh prod deploy.sh\n"
                            "autonomous = true\n")
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertIn_("дописан недостающий ключ test", out,
                       "doctor --fix не отчитался о дописанном test")
        text = read(self.fdeploy)
        self.assertRegex(text, r"(?m)^# моя шапка, не трогать$", "doctor --fix стёр шапку файла")
        self.assertRegex(text, r"(?m)^deploy = ssh prod deploy\.sh$",
                         "doctor --fix затронул значение deploy")
        self.assertRegex(text, r"(?m)^autonomous = true$",
                         "doctor --fix затронул значение autonomous")
        self.assertRegex(text, r"(?m)^test =$", "дописанный test не пустой")
        self.assertIn("# test это команда тестов", text, "дописанный test без своего комментария")
        # test дописан в хвост, после deploy и autonomous, не между ними.
        keys = [ln.split("=")[0].strip() for ln in text.split("\n") if re.match(r"^[a-z]+ ?=", ln)]
        self.assertEqual(keys[-1], "test", "test дописан не в конец файла: %s" % text)
        # Находка про пустой test= остаётся: --fix дописывает место под ключ, а
        # не выдумывает команду.
        self.assertIn_("пустой test=", out, "находка про пустой test после дописывания пропала")
        # Повторный --fix ключ, который уже дописан, второй раз не трогает.
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertNotIn_("дописан", out, "повторный doctor --fix переписал уже дописанный ключ")

    def test_09_present_empty_key_is_left_alone(self):
        # Отсутствующий ключ и присутствующий пустой это разные случаи: пустой
        # test= --fix не трогает, дописывать там уже нечего.
        write(self.fdeploy, "# шапка\ndeploy = ssh prod deploy.sh\ntest = \nautonomous = true\n")
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertNotIn_("дописан", out, "doctor --fix дописал ключ, который уже есть пустым")
        self.assertIn_("пустой test=", out,
                       "находка про пустой test пропала при уже существующем пустом ключе")

    def test_10_several_missing_keys(self):
        # Дописываются оба (deploy и test), найденный autonomous не трогается, а
        # связанная с ним находка остаётся.
        write(self.fdeploy, "autonomous = true\n")
        _, out = self.box.doctor(self.fproj, "--fix")
        text = read(self.fdeploy)
        self.assertRegex(text, r"(?m)^deploy =$", "doctor --fix не дописал ключ deploy")
        self.assertRegex(text, r"(?m)^test =$", "doctor --fix не дописал ключ test")
        self.assertRegex(text, r"(?m)^autonomous = true$", "doctor --fix затронул autonomous")
        self.assertIn_("autonomous = true при пустом deploy=", out,
                       "находка про autonomous=true при пустом deploy пропала после дописывания")

    def test_11_commented_key_counts_as_missing(self):
        # "# test = ..." выключен руками и для --fix значит то же, что полное
        # отсутствие test.
        write(self.fdeploy, "deploy = x\n# test = отключено руками, до времени\nautonomous = true\n")
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertIn_("дописан недостающий ключ test", out,
                       "doctor --fix не считает закомментированный test отсутствующим")
        self.assertRegex(read(self.fdeploy), r"(?m)^test =$",
                         "doctor --fix не дописал настоящий test поверх закомментированного")

    def test_12_truncated_key_line_counts_as_missing(self):
        # Обрубленная строка ключа (без "=" вообще) это тоже не имеющийся ключ,
        # как и закомментированная: без "=" partition отдал бы всю строку в ключ,
        # и мусор засчитался бы present'ом. Мусорная строка не удаляется (--fix
        # только дописывает, чужой текст не трогает), а рабочая появляется рядом.
        write(self.fdeploy, "deploy = x\ntest\nautonomous = true\n")
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertIn_("дописан недостающий ключ test", out,
                       "doctor --fix не считает обрубленную строку test отсутствующим ключом")
        text = read(self.fdeploy)
        self.assertIn("test", text.split("\n"), "doctor --fix стёр обрубленную строку test")
        self.assertRegex(text, r"(?m)^test =$",
                         "doctor --fix не дописал настоящий test рядом с обрубленной строкой")

    def test_13_full_file_without_trailing_newline(self):
        # Нечего дописывать значит нечего и писать, даже перевод строки в хвост.
        write(self.fdeploy, "deploy = x\ntest = y\nautonomous = true")
        before = self.fdeploy.read_bytes()
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertNotIn_("дописан", out, "doctor --fix дописал в уже полный файл")
        self.assertEqual(self.fdeploy.read_bytes(), before,
                         "doctor --fix изменил байты уже полного файла")

    def test_14_missing_key_without_trailing_newline(self):
        # Перед дописанным комментарием обязан появиться перенос строки, а не
        # склейка с последней имеющейся строкой.
        write(self.fdeploy, "deploy = x\nautonomous = true")
        _, out = self.box.doctor(self.fproj, "--fix")
        self.assertIn_("дописан недостающий ключ test", out,
                       "doctor --fix не дописал test в файл без завершающего перевода строки")
        text = read(self.fdeploy)
        self.assertRegex(text, r"(?m)^autonomous = true$",
                         "дописывание в файл без \\n на конце склеило последнюю строку")
        self.assertRegex(text, r"(?m)^test =$", "test не дописан отдельной строкой")

    def test_15_empty_file(self):
        # Действительно пустой (0 байт) deploy.local: дописываются сразу все три
        # ключа, а результат начинается прямо с комментария, без лишнего переноса
        # строки перед ним.
        write(self.fdeploy, "")
        _, out = self.box.doctor(self.fproj, "--fix")
        for key in ("deploy", "test", "autonomous"):
            self.assertIn_("дописан недостающий ключ %s" % key, out,
                           "doctor --fix не дописал %s в пустой файл" % key)
        self.assertTrue(read(self.fdeploy).startswith("#"),
                        "дописывание в пустой файл начинается не с комментария")
        # Дописанный файл читается read_deploy как обычная пустая болванка.
        _, out = self.box.doctor(self.fproj)
        self.assertIn_("пустой deploy=", out, "после дописывания нет находки про пустой deploy=")
        self.assertIn_("пустой test=", out, "после дописывания нет находки про пустой test=")

    def test_16_machine_ignore_paths(self):
        # Машинные записи .devkit (cmdout/, ship.lock, goal-*) раскладывает
        # автоматика подключения (new), а отсутствие доктор помечает находкой и
        # чинит по --fix, как журнал запусков (DK-278). Проверка идёт в блоке
        # in_git, а не блока доски: cmdout и shipctl работают и в проекте без
        # доски, а цель ведут не все проекты.
        proj = self.box.project("mproj")
        self.box.dkctl_run("new", "--prefix", "MP", "-C", str(proj), path=self.boardpath)
        # Каталога .devkit/cmdout на диске ещё нет: проверка через путь со слэшом
        # не должна давать ложную находку от git check-ignore без слэша.
        self.assertFalse((proj / ".devkit" / "cmdout").exists(),
                         "каталог cmdout не должен существовать перед проверкой")
        # new дописал обе записи в .gitignore: сверка по конкретному пути, а не
        # греп вывода (то же требование стоит отдельной строкой DK-027).
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/cmdout/")[0], 0,
                         "new не гитигнорнул .devkit/cmdout/")
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/ship.lock")[0], 0,
                         "new не гитигнорнул .devkit/ship.lock")
        # Рабочее состояние цикла цели сверяется не только шаблоном, но и живым
        # именем файла (DK-443): в статусе висели именно журнал витков, файл
        # отметок и его замок, а шаблон без них подтвердил бы сам себя.
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/goal-*")[0], 0,
                         "new не гитигнорнул .devkit/goal-*")
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/chat/")[0], 0,
                         "new не гитигнорнул .devkit/chat/")
        for name in ("goal-MP-001.log", "goal-MP-001.mail", "goal-MP-001.mail.lock"):
            self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/" + name)[0], 0,
                             "new не гитигнорнул .devkit/%s" % name)
        # Старый проект, подключённый до появления записей: стёрли их из .gitignore.
        gi = proj / ".gitignore"
        kept = [ln for ln in gi.read_text(encoding="utf-8").splitlines()
                if ln.strip() not in (".devkit/cmdout/", ".devkit/ship.lock", ".devkit/goal-*",
                                      ".devkit/chat/")]
        gi.write_text("\n".join(kept) + "\n", encoding="utf-8")
        rc, out = self.box.doctor(proj)
        self.assertEqual(rc, 1, "doctor не вернул 1 при отсутствующих машинных гитигнор-записях")
        self.assertIn_(".devkit/cmdout/ не гитигнорнут", out,
                       "doctor не нашёл отсутствующий гитигнор cmdout")
        self.assertIn_(".devkit/ship.lock не гитигнорнут", out,
                       "doctor не нашёл отсутствующий гитигнор ship.lock")
        self.assertIn_(".devkit/goal-* не гитигнорнут", out,
                       "doctor не нашёл отсутствующий гитигнор рабочего состояния цели")
        self.assertIn_(".devkit/chat/ не гитигнорнут", out,
                       "doctor не нашёл отсутствующий гитигнор входов разговора")
        # doctor --fix дописывает обе записи со своим комментарием.
        _, out = self.box.doctor(proj, "--fix")
        self.assertIn_("починено: .gitignore: добавлен .devkit/cmdout/", out,
                       "doctor --fix не дописал cmdout")
        self.assertIn_("починено: .gitignore: добавлен .devkit/ship.lock", out,
                       "doctor --fix не дописал ship.lock")
        self.assertIn_("починено: .gitignore: добавлен .devkit/goal-*", out,
                       "doctor --fix не дописал рабочее состояние цели")
        self.assertIn_("починено: .gitignore: добавлен .devkit/chat/", out,
                       "doctor --fix не дописал входы разговора")
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/cmdout/")[0], 0,
                         "после --fix cmdout не гитигнорнут")
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/ship.lock")[0], 0,
                         "после --fix ship.lock не гитигнорнут")
        self.assertEqual(git(proj, "check-ignore", "-q", ".devkit/goal-MP-001.log")[0], 0,
                         "после --fix журнал витков цели не гитигнорнут")
        # Повторный доктор находок по машинным путям не даёт.
        _, out = self.box.doctor(proj)
        self.assertNotIn_(".devkit/cmdout/", out, "повторный doctor всё ещё видит cmdout")
        self.assertNotIn_(".devkit/ship.lock", out, "повторный doctor всё ещё видит ship.lock")
        self.assertNotIn_(".devkit/goal-*", out,
                          "повторный doctor всё ещё видит рабочее состояние цели")


class DeployWorktreeTest(SandboxCase):
    """Линкованное дерево задачи (worktree от shipctl start): .devkit
    гитигнорнута и в него не переезжает, а doctor --fix заводил там болванку
    deploy.local и тут же сам находил её пустые ключи (DK-463). Конфиг выката
    логически принадлежит основному чекауту, в worktree его не заводят и не
    разбирают, а в основном чекауте поведение прежнее.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("wproj")
        boardbin = cls.box.root / "wboardbin"
        boardbin.mkdir()
        executable(boardbin / "taskctl", cls.box.board_taskctl())
        cls.boardpath = "%s:%s" % (boardbin, cls.box.cleanpath)
        cls.box.dkctl_run("new", "--prefix", "WP", "-C", str(cls.proj), path=cls.boardpath)
        cls.deploy = cls.proj / ".devkit" / "deploy.local"
        git(cls.proj, "add", "-A")
        git(cls.proj, "commit", "-qm", "seed")
        cls.wt = cls.box.root / "wproj-wt"
        git(cls.proj, "worktree", "add", "-q", "-b", "wp-task", str(cls.wt))
        cls.wtdeploy = cls.wt / ".devkit" / "deploy.local"

    def sysdoctor(self, root, *args):
        return self.box.doctor(root, *args, path=str(self.box.sys))

    def test_fix_does_not_scaffold_deploy_in_linked_worktree(self):
        # Гитигнорнутая .devkit не переезжает в worktree: болванка из основного
        # чекаута там и так недоступна.
        self.assertFalse(self.wtdeploy.exists(),
                         ".devkit/deploy.local не должен переехать в worktree")
        rc, out = self.sysdoctor(self.wt, "--fix")
        self.assertFalse(self.wtdeploy.exists(),
                         "doctor --fix завёл .devkit/deploy.local в linked worktree")
        self.assertNotIn_("deploy=", out, "doctor --fix в worktree дал находку про deploy=")
        self.assertNotIn_("test=", out, "doctor --fix в worktree дал находку про test=")
        self.assertNotIn_(str(self.wtdeploy), out,
                          "doctor --fix в worktree упомянул путь до deploy.local")

    def test_main_checkout_behaviour_is_unchanged(self):
        # Тот же прогон в основном чекауте всё ещё заводит и находит болванку,
        # как раньше: правка не должна была тронуть этот путь.
        self.assertTrue(self.deploy.is_file(), "new не завёл .devkit/deploy.local в чекауте")
        rc, out = self.sysdoctor(self.proj)
        self.assertIn_("пустой deploy=", out, "в основном чекауте пропала находка про deploy=")
        self.assertIn_("пустой test=", out, "в основном чекауте пропала находка про test=")

    def test_stale_deploy_file_in_worktree_gives_no_findings(self):
        # Замечание ревью: gate только на scaffold_deploy не спасает, когда
        # deploy.local в worktree уже физически лежит (доехал с прежним багом
        # либо положен руками) с пустыми ключами. Разбор пустых deploy=/test=
        # тоже обязан молчать вне основного чекаута, файл --fix не трогает.
        write(self.wtdeploy, "deploy =\ntest =\nautonomous = false\n")
        before = self.wtdeploy.read_bytes()
        rc, out = self.sysdoctor(self.wt, "--fix")
        self.assertNotIn_("пустой deploy=", out,
                          "doctor --fix в worktree дал находку про пустой deploy= "
                          "по уже лежащему файлу")
        self.assertNotIn_("пустой test=", out,
                          "doctor --fix в worktree дал находку про пустой test= "
                          "по уже лежащему файлу")
        self.assertNotIn_("дописан", out, "doctor --fix дописал ключи в worktree")
        self.assertEqual(self.wtdeploy.read_bytes(), before,
                         "doctor --fix изменил лежащий в worktree deploy.local")
        self.wtdeploy.unlink()


class MachineContourTest(SandboxCase):
    """Машинный контур: бинари, определения агентов, скиллы, снимок квоты и
    каталог сборки. Гоняется на отдельном проекте и отдельном HOME, чтобы правки
    --fix не мешали прежним шагам; сборку изображает заглушка go, гонять
    настоящую в самопроверке незачем.
    """

    # Про собранные утилиты доктор отчитывается одной строкой на всю пачку
    # (DK-157), и ищется в ней имя нужной утилиты, а не строка на утилиту.
    BUILT = r"починено: собран[ао] (?:\d+ )?утилит\S* версии"

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        cls.proj = box.project("mproj")
        # Соседний markdown без frontmatter в kit/agents/ имитирует README:
        # раскладка берёт директорию целиком и обязана отличать определение от
        # постороннего файла, иначе тот уедет на машину как агент. То же со
        # скиллами: скилл это директория с SKILL.md.
        write(box.dk / "kit" / "agents" / "README.md", "# agents\n\nПроза, не определение.\n")
        write(box.dk / "kit" / "skills" / "README.md", "# skills\n\nПроза, не скилл.\n")
        cls.mhome = box.root / "mhome"
        (cls.mhome / "go" / "bin").mkdir(parents=True)
        cls.gostub = box.root / "gostub"
        cls.gostub.mkdir()
        executable(cls.gostub / "go", GO_STUB)
        cls.mpath = "%s:%s:%s" % (cls.gostub, cls.mhome / "go" / "bin", box.sys)
        # Вторая подписка объявлена машинным слоем: каталог её конфига девкит
        # берёт оттуда, а не из своей константы, и без объявления доктор про
        # подписку молчит (её на машине просто нет).
        write(cls.mhome / ".devkit" / "harness.local",
              'enabled = ["claude-code"]\n\n[glm-code]\nhome = "~/.devkit/claude-glm"\n')
        # Каталог назначения называется прямо: правило выбора (переменная, потом
        # PATH) проверяется отдельными тестами, остальным тут нужен один
        # известный каталог, и он же стоит в подставном PATH.
        cls.env = {"SANDBOX": str(box.root), "DEVKIT_BIN": str(cls.mhome / "go" / "bin")}

    def docm(self, *args, **kw):
        kw.setdefault("home", self.mhome)
        kw.setdefault("path", self.mpath)
        env = dict(self.env)
        env.update(kw.pop("env", None) or {})
        return self.box.doctor(self.proj, *args, env=env, **kw)

    def test_01_bare_machine(self):
        _, out = self.docm()
        # Повод у всех шести один, и находка про них одна: строка на утилиту
        # превращала бы список «осталось сделать» в простыню (DK-157). Список
        # проверяемых выводится из дерева, поэтому утилиты, до которых прежний
        # перечень в коде не дотягивался, под проверкой наравне.
        missing = [ln for ln in out.splitlines() if "не в PATH" in ln and "утилит devkit" in ln]
        self.assertEqual(len(missing), 1, "находка про бинари не свёрнута в одну: %s" % out)
        for name in build.tools(self.box.dk):
            self.assertIn_(name, missing[0], "%s не попал под проверку" % name)
        self.assertIn_("чекаут devkit: на ветке", out, "доктор не назвал режим чекаута")
        self.assertRegex(out, r"не разложено \d+ скиллов в[^\n]*board-batch", "нет находки про скилл")
        self.assertRegex(out, r"не разложено \d+ определений агентов в[^\n]*exec-medium",
                         "нет находки про определения исполнителей")
        # Обе находки одной строкой на пачку, а не строкой на файл.
        for word in ("скилл", "определени"):
            lines = [ln for ln in out.splitlines() if "не разложен" in ln and word in ln]
            self.assertEqual(len(lines), 1, "находка про %s не свёрнута: %s" % (word, out))
        self.assertIn_("tmux не в PATH", out, "нет находки про tmux")
        self.assertIn_("нет снимка квоты в", out, "нет находки про снимок квоты")
        # Незаведённая вторая подписка молчанием не отличается от заведённой:
        # окно shipctl code без каталога конфига не откроется вовсе, и сказать
        # об этом обязан доктор, а не отказ команды в неудачный момент.
        self.assertIn_("нет конфига второй подписки", out, "нет находки про вторую подписку")
        self.assertIn_("devkitctl doctor --fix", out, "находка про вторую подписку без команды починки")
        self.assertIn_("SessionStart-хук", out, "нет находки про хук освежения квоты")
        self.assertFalse((self.mhome / "go" / "bin" / "agentctl").exists(),
                         "doctor без --fix собрал бинарь")
        # Помета «машина» отделяет машинные находки от проектных, на неё
        # опирается и дока, и сценарий проверки.
        self.assertRegex(out, r"(?m)^машина: tmux не в PATH", "у машинной находки нет пометы")
        self.assertRegex(out, r"(?m)^нет AGENTS\.md", "проектная находка получила помету «машина»")

    def test_02_fix_builds_and_lays_out(self):
        # --fix собирает бинари и раскладывает определения, а неоднозначное
        # (tmux, снимок квоты) оставляет находкой с командой.
        _, out = self.docm("--fix")
        for t in build.tools(self.box.dk):
            self.assertTrue(os.access(str(self.mhome / "go" / "bin" / t), os.X_OK),
                            "doctor --fix не собрал %s: %s" % (t, out))
        agents = self.mhome / ".claude" / "agents"
        self.assertTrue((agents / "exec-medium.md").is_file(),
                        "doctor --fix не разложил определения исполнителей")
        # Роли ревьювера в наборе появились позже исполнителей, и раскладка
        # обязана брать директорию целиком, а не один префикс.
        self.assertTrue((agents / "review-high.md").is_file(),
                        "doctor --fix не разложил определения ревьюверов")
        self.assertFalse((agents / "README.md").exists(),
                         "--fix положил на машину markdown без frontmatter")
        self.assertNotRegex(out, r"установлено \d+ определений агентов[^\n]*README",
                            "--fix отчитался о README как об определении агента")
        skills = self.mhome / ".claude" / "skills"
        self.assertTrue((skills / "board-batch" / "SKILL.md").is_file(),
                        "doctor --fix не разложил скилл")
        self.assertFalse((skills / "README.md").exists(),
                         "--fix положил на машину markdown из kit/skills/ как скилл")
        # Раскладка берёт kit/skills/ целиком, а не знакомый ей список:
        # добавленный скилл иначе не доехал бы до машины молча.
        for s in sorted((self.box.dk / "kit" / "skills").glob("*/SKILL.md")):
            self.assertTrue((skills / s.parent.name / "SKILL.md").is_file(),
                            "doctor --fix не разложил скилл %s" % s.parent.name)
        # Каталог скилла едет целиком: у proofread рядом со SKILL.md лежат
        # пары, словарь и корпус, и без них вычитка в чужом проекте идёт по
        # одним названиям пунктов типологии (DK-331).
        for sat in ("pairs.md", "dictionary.md", "corpus.md"):
            self.assertTrue((skills / "proofread" / sat).is_file(),
                            "doctor --fix не разложил спутник скилла %s" % sat)
        # Оболочка goal-loop доезжает тем же каталогом, звать её всё равно
        # положено из чекаута, но на машине она обязана совпадать с devkit.
        self.assertTrue((skills / "goal-loop" / "goal-run.py").is_file(),
                        "doctor --fix не разложил оболочку скилла goal-loop")
        # Однотипное свёрнуто в строку с числом и именами (DK-157): строка на
        # каждый скилл, агента и хук делала вывод установки нечитаемым.
        self.assertRegex(out, r"починено: установлено \d+ скиллов в[^\n]*board-batch",
                         "скиллы разложены строкой на скилл, а не одной строкой")
        self.assertRegex(out, r"починено: установлено \d+ определений агентов в[^\n]*exec-medium",
                         "определения агентов разложены строкой на файл")
        self.assertRegex(out, r"починено: включено \d+ хук\S* харнеса в[^\n]*quota-refresh\.sh",
                         "хуки харнеса подключены строкой на хук")
        # Права дописываются десятками, и перечислять их человеку незачем:
        # в строке остаётся счёт, а сам перечень лежит в настройках.
        self.assertRegex(out, r"починено: дописано \d+ прав\S* машинного контура в",
                         "нет строки про дописанные права")
        self.assertNotRegex(out, r"права машинного контура[^\n]*Bash\(",
                            "строка про права снова перечисляет сами правила")
        self.assertIn_("tmux не в PATH", out, "--fix не ставит tmux, когда ставить нечем")
        self.assertIn_("пакетного менеджера brew на машине нет", out,
                       "находка про tmux не называет причину, по которой пакет не поставлен")
        self.assertIn_("agentctl quota refresh", out,
                       "--fix не снимает квоту, находка должна остаться")
        # Каталог конфига второй подписки раскладывает --fix, а значения в него
        # вписывает пользователь: endpoint и токен берутся в кабинете подписки.
        conf = self.mhome / ".devkit" / "claude-glm" / "settings.json"
        self.assertIn_("разложена болванка конфига второй подписки", out,
                       "--fix не разложил каталог конфига второй подписки")
        self.assertEqual(json.loads(read(conf))["env"],
                         {"ANTHROPIC_BASE_URL": "", "ANTHROPIC_AUTH_TOKEN": "", "ANTHROPIC_MODEL": ""},
                         "болванка второй подписки разложена не теми ключами")
        self.assertEqual(conf.stat().st_mode & 0o777, 0o600,
                         "болванка с токеном разложена с широкими правами")
        self.assertIn_("пустые ключи", out, "--fix не назвал незаполненные ключи второй подписки")
        # Повторный --fix по машинному контуру уже ничего не чинит.
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix не должен ничего менять")

    def test_03_diverged_agent_definition(self):
        # Правка руками или отставшая копия: plain doctor называет находку с
        # командой cp, файл не трогает.
        agent = self.mhome / ".claude" / "agents" / "exec-low.md"
        write(agent, read(agent) + "\nсвоя строка\n")
        _, out = self.docm()
        self.assertRegex(out, r"разошлось определение агента в[^\n]*exec-low",
                         "нет находки про разошедшееся определение")
        self.assertIn("своя строка", read(agent), "doctor без --fix тронул определение")
        # devkit источник правды для промптов: --fix перекладывает разошедшееся
        # определение и называет в отчёте, что переложил, а не затирает молча.
        _, out = self.docm("--fix")
        self.assertRegex(out, r"починено: обновлено определение агента в[^\n]*exec-low",
                         "--fix не отчитался о перекладке разошедшегося определения")
        self.assertNotIn("своя строка", read(agent), "--fix не переложил разошедшееся определение")
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix после перекладки не должен менять")

    def test_04_diverged_skill(self):
        skill = self.mhome / ".claude" / "skills" / "board-batch" / "SKILL.md"
        write(skill, read(skill) + "\nсвоя строка\n")
        _, out = self.docm()
        self.assertRegex(out, r"разошёлся скилл в[^\n]*board-batch",
                         "нет находки про разошедшийся скилл")
        self.assertIn("своя строка", read(skill), "doctor без --fix тронул скилл")
        _, out = self.docm("--fix")
        self.assertRegex(out, r"починено: обновлён скилл в[^\n]*board-batch",
                         "--fix не отчитался о перекладке скилла")
        self.assertNotIn("своя строка", read(skill), "--fix не переложил разошедшийся скилл")
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix после перекладки скилла не должен менять")

    def test_04b_missing_skill_companion(self):
        # Спутник скилла пришёл в devkit позже самого SKILL.md (так вышло с
        # парами и словарём proofread), и на машине его нет. Это то же
        # расхождение с devkit, что правка SKILL.md руками: находка с командой
        # починки, а --fix докладывает недостающее.
        pairs = self.mhome / ".claude" / "skills" / "proofread" / "pairs.md"
        pairs.unlink()
        _, out = self.docm()
        self.assertRegex(out, r"разошёлся скилл в[^\n]*proofread",
                         "нет находки про скилл без спутника")
        self.assertIn_("разложить: devkitctl doctor --fix", out,
                       "находка про спутника не зовёт починку")
        self.assertFalse(pairs.exists(), "doctor без --fix положил спутника")
        _, out = self.docm("--fix")
        self.assertRegex(out, r"починено: обновлён скилл в[^\n]*proofread",
                         "--fix не отчитался о доложенном спутнике")
        self.assertTrue(pairs.is_file(), "--fix не положил недостающего спутника")
        # Правка спутника руками откатывается так же, как правка SKILL.md:
        # devkit источник правды для промптов целиком, а не заголовком.
        dic = self.mhome / ".claude" / "skills" / "proofread" / "dictionary.md"
        write(dic, read(dic) + "\nсвоя строка\n")
        _, out = self.docm()
        self.assertRegex(out, r"разошёлся скилл в[^\n]*proofread",
                         "нет находки про разошедшегося спутника")
        _, out = self.docm("--fix")
        self.assertNotIn("своя строка", read(dic), "--fix не переложил разошедшегося спутника")
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix после спутника не должен менять")

    def snap(self, taken):
        write(self.mhome / ".devkit" / "quota" / "claude-code.local",
              "taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n" % taken)

    def test_05_quota_snapshot_age(self):
        # Возраст берётся из строки taken, порог 45 минут (тот же, что у
        # корректора pick), поэтому проверка идёт по обе стороны границы, а не
        # «2020 год против сейчас».
        self.snap(taken_at(1))
        _, out = self.docm()
        # Ищется именно строка про этот снимок: слово «протух» есть и в находке
        # про неподключённый хук освежения.
        self.assertIn_("claude-code.local протух (возраст", out,
                       "снимок возрастом час не признан протухшим")
        self.snap(taken_at(0.5))
        _, out = self.docm()
        self.assertNotIn_("claude-code.local", out, "снимок возрастом полчаса попал в находки")

    def test_06_other_taken_formats(self):
        # Остальные форматы момента снятия разбираются наравне с основным.
        for fmt in ("%Y-%m-%dT%H:%M:%S", "%Y-%m-%d %H:%M"):
            self.snap(taken_at(0.5, fmt))
            _, out = self.docm()
            self.assertNotIn_("claude-code.local", out, "момент снятия %s не разобран" % fmt)

    def test_07_unparsed_taken(self):
        # Строка taken есть, но не разобрана (снимок заполняют и руками):
        # возрасту верить нельзя, это находка. Строки нет вовсе: та же находка.
        self.snap("вчера")
        _, out = self.docm()
        self.assertIn_("не разобран момент снятия", out, "нет находки про неразобранный taken")
        write(self.mhome / ".devkit" / "quota" / "claude-code.local",
              "week_all = 40% сброс 2030-01-01T00:00\n")
        _, out = self.docm()
        self.assertIn_("не разобран момент снятия", out, "нет находки про снимок без taken")

    def test_08_snapshot_moves_to_the_directory(self):
        # Одиночный quota.local это как было до DK-038, и читатель его ещё
        # понимает, но чинит расхождение --fix, а не пользователь.
        (self.mhome / ".devkit" / "quota" / "claude-code.local").unlink()
        old = self.mhome / ".devkit" / "quota.local"
        write(old, "taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n" % taken_at(0.5))
        _, out = self.docm()
        self.assertIn_("снимок квоты лежит по старому пути", out,
                       "нет находки про старый путь снимка")
        self.assertNotIn_("нет снимка квоты", out, "старый снимок посчитан отсутствующим")
        self.assertTrue(old.is_file(), "doctor без --fix тронул старый снимок")
        _, out = self.docm("--fix")
        self.assertIn_("починено: снимок квоты переехал", out, "--fix не переложил снимок")
        self.assertTrue((self.mhome / ".devkit" / "quota" / "claude-code.local").is_file(),
                        "--fix не положил снимок в директорию")
        self.assertFalse(old.exists(), "--fix оставил снимок и по старому пути")
        _, out = self.docm()
        self.assertNotIn_(".devkit/quota", out, "после переезда по снимку остались находки")
        # Оба файла сразу: читается новый, а про старый доктор говорит, но не
        # удаляет его сам (правки --fix строго additive).
        write(old, "taken = %s\nweek_all = 40%% сброс 2030-01-01T00:00\n" % taken_at(0.5))
        _, out = self.docm("--fix")
        self.assertIn_("лежит рядом с новым", out, "нет находки про два снимка сразу")
        self.assertTrue(old.is_file(), "--fix удалил старый снимок, хотя правки additive")
        old.unlink()

    def stray(self, name="agentctl", version="v0.0.1", commit="0123456789ab"):
        """Бинарь с чужим коммитом в каталоге назначения: так выглядит машина
        после git pull, где пересобрать забыли.
        """
        return executable(self.mhome / "go" / "bin" / name,
                          BINARY_STUB % build.version_line(name, version, commit))

    def test_09_binary_from_another_commit(self):
        # Правило сходимости одно: коммит бинаря равен коммиту HEAD чекаута.
        # Порога тут нет, любое расхождение это находка, а --fix пересобирает.
        self.stray()
        _, out = self.docm()
        self.assertIn_("agentctl собран из коммита 0123456789ab", out,
                       "нет находки про разошедшийся коммит бинаря")
        self.assertIn_("этого коммита в клоне devkit нет", out,
                       "находка не говорит, что коммит бинаря клону неизвестен")
        self.assertIn_("; чекаут devkit на ветке", out,
                       "находка про расхождение не называет режим чекаута")
        _, out = self.docm("--fix")
        self.assertRegex(out, self.BUILT + r"[^\n]*agentctl",
                         "--fix не пересобрал разошедшийся бинарь")
        _, out = self.docm()
        self.assertNotIn_("собран из коммита", out,
                          "после пересборки находка про расхождение осталась")

    def test_09a_binary_without_the_flag(self):
        # Третий случай сверки: бинарь собран до релизной схемы и про --version
        # не знает вовсе. Починка та же, что у разошедшегося.
        executable(self.mhome / "go" / "bin" / "agentctl")
        _, out = self.docm()
        self.assertIn_("agentctl не отвечает на --version", out,
                       "бинарь без --version не признан находкой")
        _, out = self.docm("--fix")
        self.assertRegex(out, self.BUILT + r"[^\n]*agentctl",
                         "--fix не пересобрал бинарь без --version")
        _, out = self.docm()
        self.assertNotIn_("не отвечает на --version", out,
                          "после пересборки находка про бинарь без --version осталась")

    def test_09b_unsaved_go_changes(self):
        # Единственный хвост, где остаётся mtime: коммит бинаря равен HEAD, а
        # исходники уже другие, и сравнивать коммиты не с чем.
        agentctl = self.mhome / "go" / "bin" / "agentctl"
        os.utime(str(agentctl), (946684800, 946684800))
        _, out = self.docm()
        self.assertNotIn_("несохранённых правок", out,
                          "старый бинарь при чистом дереве объявлен несобранным")
        edit = self.box.dk / "tools" / "agentctl" / "unsaved.go"
        write(edit, "package main\n")
        try:
            _, out = self.docm()
            self.assertIn_("agentctl старее несохранённых правок go-кода", out,
                           "нет находки про несобранные правки")
            _, out = self.docm("--fix")
            self.assertRegex(out, self.BUILT + r"[^\n]*agentctl",
                             "--fix не пересобрал несобранные правки")
            _, out = self.docm()
            self.assertNotIn_("несохранённых правок", out,
                              "после пересборки находка про несобранные правки осталась")
        finally:
            edit.unlink()

    def test_09c_whole_tree_is_checked(self):
        # Список выводится из tools/*/go.mod, поэтому под проверку попадает и то,
        # что появилось после написания доктора.
        newctl = self.box.dk / "tools" / "newctl"
        write(newctl / "go.mod", "module github.com/dronrider/devkit/tools/newctl\n\ngo 1.26\n")
        try:
            _, out = self.docm()
            self.assertIn_("newctl не в PATH", out,
                           "утилита, добавленная в дерево, под проверку не попала")
        finally:
            shutil.rmtree(str(newctl))

    def commit_devkit(self, message):
        git(self.box.dk, "add", "-A")
        rc, out = git(self.box.dk, "commit", "-qm", message)
        self.assertEqual(rc, 0, "коммит в копии devkit не прошёл: %s" % out)

    def test_09d_commit_beside_the_code(self):
        # День на доске devkit выглядит так: собрали бинари, потом легли коммиты
        # доски, HEAD уехал вперёд, а go-исходники утилит никто не трогал.
        # Находок тут быть не должно: ожидание считается по коду. Сверка с HEAD
        # краснела бы после каждого такого коммита, а их за день десятки.
        # Бинари собираются тут же, а не берутся от соседнего теста: без них
        # находки были бы «не в PATH», и коммит доски проверять стало бы нечем.
        self.docm("--fix")
        # Файл кладётся в корень копии: сторож утечки стенда сверяет и пустые
        # директории, а docs/ в копии devkit нет.
        board = self.box.dk / "board.md"
        write(board, "# доска\n")
        self.commit_devkit("DK-000 в Check")
        try:
            _, out = self.docm()
            self.assertNotIn_("собран из коммита", out,
                              "коммит мимо go-исходников объявил бинари разошедшимися")
            self.assertIn_("чекаут devkit: на ветке", out,
                           "доктор перестал называть режим чекаута")
        finally:
            board.unlink()
            self.commit_devkit("убрать доску")

    def test_09e_commit_into_the_code(self):
        # Обратная сторона того же правила: правка go-кода утилиты коммитом
        # находку зажигает, и ровно по той утилите, чей код тронут.
        self.docm("--fix")
        probe = self.box.dk / "tools" / "agentctl" / "probe.go"
        write(probe, "package main\n")
        self.commit_devkit("agentctl: правка кода")
        try:
            _, out = self.docm()
            self.assertRegex(out, r"agentctl собран из коммита \w+ \(версия [^)]+\), "
                                  r"а код утилиты правился позже, в \w{12}",
                             "правка go-кода не дала находки про отставший бинарь")
            self.assertNotIn_("taskctl собран из коммита", out,
                              "правка кода agentctl объявила отставшим и taskctl")
            _, out = self.docm("--fix")
            self.assertRegex(out, self.BUILT + r"[^\n]*agentctl",
                             "--fix не пересобрал отставший бинарь")
            _, out = self.docm()
            self.assertNotIn_("собран из коммита", out,
                              "после пересборки находка про отставший бинарь осталась")
        finally:
            probe.unlink()
            # Снятие правки это тоже коммит в go-код, и бинарь после него снова
            # отстал: стенд возвращается пересборкой, иначе течёт в соседний тест.
            self.commit_devkit("убрать правку кода")
            self.docm("--fix")

    def test_10_shadowed_binary(self):
        # Годная сборка на месте, а в PATH выигрывает чужая копия. Это находка, и
        # пересобирать на каждом прогоне нечего.
        shadow = self.box.root / "shadow"
        shadow.mkdir(exist_ok=True)
        executable(shadow / "agentctl")
        spath = "%s:%s:%s:%s" % (self.gostub, shadow, self.mhome / "go" / "bin", self.box.sys)
        _, out = self.docm("--fix", path=spath)
        self.assertIn_("в PATH выигрывает чужая копия", out, "нет находки про затенённый бинарь")
        self.assertNotRegex(out, self.BUILT + r"[^\n]*agentctl",
                            "затенённый бинарь пересобирается впустую")
        _, out = self.docm("--fix", path=spath)
        self.assertNotIn_("починено", out, "повторный --fix при затенённом бинаре не должен менять")
        self.assertIn_("в PATH выигрывает чужая копия", out,
                       "находка про затенённый бинарь должна остаться")

    def test_11_symlink_is_not_a_shadow(self):
        # Симлинк на тот же бинарь впереди в PATH (~/.local/bin поверх ~/go/bin)
        # это не затенение: сверяется realpath, а не строка пути. Бинарь в GOBIN
        # нарочно с чужим коммитом, иначе до сверки дело не дойдёт: сошедшийся
        # отсекается раньше, и строковое сравнение прошло бы незамеченным.
        localbin = self.box.root / "localbin"
        localbin.mkdir(exist_ok=True)
        link = localbin / "agentctl"
        if link.exists() or link.is_symlink():
            link.unlink()
        os.symlink(str(self.mhome / "go" / "bin" / "agentctl"), str(link))
        self.stray()
        path = "%s:%s:%s:%s" % (self.gostub, localbin, self.mhome / "go" / "bin", self.box.sys)
        _, out = self.docm("--fix", path=path)
        self.assertRegex(out, self.BUILT + r"[^\n]*agentctl",
                         "разошедшийся бинарь за симлинком не пересобран")
        self.assertNotIn_("в PATH выигрывает чужая копия", out,
                          "симлинк на тот же бинарь принят за чужую копию")

    def test_12_build_directory(self):
        # Каталог назначения по решению 7 (DK-149): DEVKIT_BIN сильнее всего, а
        # без него выигрывает тот из двух каталогов, что уже стоит в PATH.
        dbhome, db = self.box.root / "dbhome", self.box.root / "devkitbin"
        _, out = self.docm("--fix", home=dbhome, path="%s:%s:%s" % (self.gostub, db, self.box.sys),
                           env={"DEVKIT_BIN": str(db)})
        self.assertTrue(os.access(str(db / "agentctl"), os.X_OK),
                        "--fix собрал не в DEVKIT_BIN: %s" % out)
        self.assertFalse((dbhome / "go" / "bin" / "agentctl").exists(),
                         "--fix при заданном DEVKIT_BIN лезет в ~/go/bin")
        lbhome = self.box.root / "lbhome"
        _, out = self.docm("--fix", home=lbhome, env={"DEVKIT_BIN": None},
                           path="%s:%s:%s" % (self.gostub, lbhome / ".local" / "bin", self.box.sys))
        self.assertTrue(os.access(str(lbhome / ".local" / "bin" / "agentctl"), os.X_OK),
                        "--fix собрал не в тот каталог, что стоит в PATH: %s" % out)
        self.assertFalse((lbhome / "go" / "bin" / "agentctl").exists(),
                         "--fix полез в ~/go/bin, которого в PATH нет")

    def test_13_no_go_in_path(self):
        # Находка с командой установки идёт в обоих режимах, а не только под
        # --fix, иначе doctor советовал бы сборку командой, которой нет.
        nghome = self.box.root / "nghome"
        for args in (("--fix",), ()):
            _, out = self.docm(*args, home=nghome, path=str(self.box.sys),
                               env={"DEVKIT_BIN": None})
            self.assertIn_("go в PATH нет", out, "нет находки про отсутствующий go")

    def test_14_build_directory_not_in_path(self):
        # --fix соберёт, а пользоваться собранным нечем, и эта находка
        # единственный сигнал. Ради такого случая задача и затевалась. Каталог
        # тут ~/.local/bin: ни его, ни ~/go/bin в PATH нет, и на машине без
        # тулчейна имя ~/go/bin выглядело бы враньём.
        pphome = self.box.root / "pphome"
        _, out = self.docm("--fix", home=pphome, env={"DEVKIT_BIN": None},
                           path="%s:%s" % (self.gostub, self.box.sys))
        self.assertTrue(os.access(str(pphome / ".local" / "bin" / "agentctl"), os.X_OK),
                        "--fix не собрал бинарь: %s" % out)
        # Одна находка на все шесть утилит и одна общая с командой для профиля,
        # а не строка на утилиту плюс общая седьмая (DK-157).
        lying = [ln for ln in out.splitlines() if "самого каталога нет в PATH" in ln]
        self.assertEqual(len(lying), 1, "находка про утилиты мимо PATH не свёрнута: %s" % out)
        for name in build.tools(self.box.dk):
            self.assertIn_(name, lying[0], "утилита %s не названа в свёрнутой находке" % name)
        self.assertIn_("export PATH=", out,
                       "находка про каталог мимо PATH не даёт готовой строки для профиля")

    def test_15_alt_subscription_filled_in(self):
        # Заполненный конфиг второй подписки доктор не трогает и не поминает:
        # это рабочее состояние, а не пробел. Токен из него в вывод не едет ни
        # при каком раскладе, у него есть только признак «есть» или «нет».
        conf = self.mhome / ".devkit" / "claude-glm" / "settings.json"
        write(conf, json.dumps({"env": {
            "ANTHROPIC_BASE_URL": "https://endpoint.example/anthropic",
            "ANTHROPIC_AUTH_TOKEN": "токен-второй-подписки",
            "ANTHROPIC_MODEL": "модель-подписки",
        }}, ensure_ascii=False) + "\n")
        conf.chmod(0o600)
        _, out = self.docm()
        self.assertNotIn_("второй подписки", out, "заполненный конфиг подписки попал в находки")
        self.assertNotIn_("токен-второй-подписки", out, "токен второй подписки напечатан")

    def test_16_alt_subscription_permissions(self):
        # В файле лежит токен, и широкие права это находка с командой: чинит их
        # --fix, а не пользователь.
        conf = self.mhome / ".devkit" / "claude-glm" / "settings.json"
        conf.chmod(0o644)
        _, out = self.docm()
        self.assertIn_("права 644", out, "нет находки про широкие права конфига подписки")
        _, out = self.docm("--fix")
        self.assertIn_("сужены права", out, "--fix не сузил права конфига подписки")
        self.assertEqual(conf.stat().st_mode & 0o777, 0o600, "права конфига подписки не сужены")

    def test_17_alt_subscription_broken_json(self):
        # Битый конфиг --fix не переписывает: там мог остаться вписанный руками
        # токен, и перезапись потеряла бы его молча.
        conf = self.mhome / ".devkit" / "claude-glm" / "settings.json"
        keep = read(conf)
        write(conf, "{не json\n")
        _, out = self.docm("--fix")
        self.assertIn_("не читается как json", out, "нет находки про битый конфиг подписки")
        self.assertEqual(read(conf), "{не json\n", "--fix переписал битый конфиг подписки")
        write(conf, keep)


class PackagesTest(SandboxCase):
    """Доводка двух пакетов и снимок квоты, который снимается сам (DK-157).

    Пакетный менеджер тут заглушка: настоящий brew на машину самопроверки ничего
    ставить не должен, а проверяется то, что доводка зовёт именно его, именно с
    тем пакетом и только когда её позвали ключом.
    """

    def setUp(self):
        super().setUp()
        box = self.box
        name = self._testMethodName
        self.home = box.make_home(box.root / ("pkghome-%s" % name))
        # Каталог, куда заглушка кладёт «поставленное»: он стоит в PATH первым,
        # поэтому поставленный пакет тут же и находится.
        self.pkgbin = box.root / ("pkgbin-%s" % name)
        self.pkgbin.mkdir()
        self.brewbin = box.root / ("brewbin-%s" % name)
        self.brewbin.mkdir()
        executable(self.brewbin / "brew", BREW_STUB)
        self.brewlog = box.root / ("brewlog-%s" % name)
        self.proj = box.project("pkgproj-%s" % name)
        self.withbrew = "%s:%s:%s:%s" % (self.pkgbin, self.brewbin, box.dkbin, box.sys)
        self.nobrew = "%s:%s:%s" % (self.pkgbin, box.dkbin, box.sys)

    def docp(self, *args, **kw):
        env = {"BREW_STUB_BIN": str(self.pkgbin), "BREW_STUB_LOG": str(self.brewlog)}
        env.update(kw.pop("env", None) or {})
        kw.setdefault("path", self.withbrew)
        return self.box.doctor(self.proj, *args, home=self.home, env=env, **kw)

    def called(self):
        return read(self.brewlog) if self.brewlog.exists() else ""

    def test_1_fix_installs_tmux(self):
        # Человек позвал доводку, и tmux ставится ею: список «осталось сделать»
        # после установки не должен звать за пакетом, который машина ставит сама.
        _, out = self.docp("--fix")
        self.assertIn_("установлен tmux (brew install tmux)", out,
                       "--fix не поставил tmux пакетным менеджером")
        self.assertIn("install tmux", self.called(), "пакетный менеджер позван не с тем пакетом")
        self.assertTrue(os.access(str(self.pkgbin / "tmux"), os.X_OK), "tmux не появился в PATH")
        self.assertNotIn_("tmux не в PATH", out, "находка про tmux осталась после установки")

    def test_2_plain_doctor_installs_nothing(self):
        # Голый doctor машину не трогает: он называет находку и команду, а
        # ставить пакет без ключа не вправе.
        _, out = self.docp()
        self.assertIn_("tmux не в PATH", out, "нет находки про tmux")
        self.assertIn_("поставит devkitctl doctor --fix", out,
                       "находка не называет команду, которая поставит пакет")
        self.assertEqual(self.called(), "", "голый doctor позвал пакетный менеджер")
        self.assertFalse((self.pkgbin / "tmux").exists(), "голый doctor поставил пакет")

    def test_3_no_package_manager(self):
        # Менеджера нет: находка остаётся, но с причиной, и чужого менеджера
        # devkit не выдумывает.
        _, out = self.docp("--fix", path=self.nobrew)
        self.assertIn_("пакетного менеджера brew на машине нет", out,
                       "находка не называет причину, по которой пакет не поставлен")
        self.assertFalse((self.pkgbin / "tmux").exists(), "пакет поставлен без менеджера")
        self.assertNotIn_("apt install", out, "доводка подставила чужой пакетный менеджер")

    def test_4_failed_install_is_a_finding(self):
        # Менеджер есть, а установка не прошла: молчать об этом нельзя, иначе
        # находка исчезнет, а пакета не будет.
        _, out = self.docp("--fix", env={"BREW_STUB_FAIL": "1"})
        self.assertIn_("brew install tmux ответил ошибкой: Error: No available formula", out,
                       "провал установки не стал находкой с ответом менеджера")
        self.assertNotIn_("()", out, "в находке пустые скобки вместо ответа менеджера")

    def test_4a_silent_manager_is_told_apart(self):
        # Менеджер отработал нулём, а пакета в PATH нет: так выглядит сломанный
        # менеджер, и говорить «не прошёл» тут враньё, команда как раз прошла.
        _, out = self.docp("--fix", env={"BREW_STUB_SILENT": "1"})
        self.assertIn_("brew install tmux отработал, а tmux в PATH так и не появился", out,
                       "молчаливый менеджер описан не тем, что случилось")
        self.assertNotIn_("ответил ошибкой", out, "нулевой код возврата назван ошибкой")
        self.assertNotIn_("()", out, "в находке пустые скобки")
        self.assertIn("install tmux", self.called(), "менеджер не позван вовсе")

    @unittest.skipUnless(platform.system() == "Darwin", "переход по клику поддержан только на macOS")
    def test_5_notifier_is_installed_too(self):
        # Клик по баннеру мимо окна сессии чинится тем же порядком: отправителя с
        # переходом ставит доводка, а не человек руками.
        osascript = executable(self.box.root / "osascript")
        _, out = self.docp("--fix", env={"DEVKIT_NOTIFY_BACKEND": str(osascript)})
        self.assertIn_("установлен terminal-notifier (brew install terminal-notifier)", out,
                       "--fix не поставил отправителя с переходом по клику")
        self.assertIn("install terminal-notifier", self.called(),
                      "пакетный менеджер позван не с terminal-notifier")
        self.assertNotIn_("клик по баннеру", out, "находка про клик осталась после установки")

    def snapless(self):
        (self.home / ".devkit" / "quota" / "claude-code.local").unlink()

    def test_6_snapshot_is_taken_by_the_hook(self):
        # Снимок квоты человеку снимать незачем: его снимает хук SessionStart при
        # первой же сессии. Хук на месте, tmux поставлен, значит находки нет
        # вовсе: звать человека за тем, что случится само, это ложный хвост.
        self.snapless()
        _, out = self.docp("--fix")
        self.assertIn_("установлен tmux", out, "снимать панель нечем, и проверять нечего")
        self.assertFalse((self.home / ".devkit" / "quota" / "claude-code.local").exists(),
                         "снимок на месте, и находке про него неоткуда взяться")
        self.assertNotIn_("нет снимка квоты", out,
                          "находка про снимок горит там, где его снимет хук")

    def test_7_snapshot_without_tmux(self):
        # Единственное, что мешает хуку, это отсутствующий tmux, и поставить его
        # нечем: тогда находка про снимок законна и называет причину.
        self.snapless()
        _, out = self.docp("--fix", path=self.nobrew)
        self.assertIn_("нет снимка квоты", out, "снимок не снимется, а находки нет")
        self.assertIn_("tmux на машине нет", out, "находка про снимок не называет причину")
        self.assertIn_("agentctl quota refresh", out, "находка не называет команду съёма")

    def test_8_snapshot_without_the_hook(self):
        # Вторая причина: хук не подключён вовсе (машина, где раскладку не
        # звали). Снимок сам не появится, и находка про него остаётся.
        self.snapless()
        drop_lines(self.home / ".claude" / "settings.json", "quota-refresh")
        _, out = self.docp()
        self.assertIn_("нет снимка квоты", out, "снимок не снимется, а находки нет")
        self.assertIn_("не подключён", out, "находка про снимок не называет причину")


class ReleaseFixTest(SandboxCase):
    """Машина потребителя: чекаут стоит на релизном теге, go в PATH нет вовсе, и
    расхождение доктор чинит скачиванием ассетов, а не сборкой. Гитхаб тут не
    участвует, релиз лежит рядом и отдаётся по file://.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        cls.proj = box.project("rproj")
        cls.tag = "v9.9.0"
        git(box.dk, "tag", cls.tag)
        # Чекаут потребителя стоит на теге отвязанным, а не на ветке: только так
        # коммит бинаря и коммит правил сходятся на всём, что вышло релизом.
        git(box.dk, "checkout", "--detach", "--quiet", cls.tag)
        cls.releases = box.root / "releases"
        stub_release(cls.releases, cls.tag, box.commit, build.tools(box.dk))
        cls.rhome = box.root / "rhome"
        cls.dest = cls.rhome / ".local" / "bin"
        cls.rpath = "%s:%s" % (cls.dest, box.sys)
        cls.env = {"SANDBOX": str(box.root), "DEVKIT_BIN": str(cls.dest),
                   update.RELEASE_ENV: "file://%s" % cls.releases}

    def docr(self, *args):
        return self.box.doctor(self.proj, *args, home=self.rhome, path=self.rpath, env=self.env)

    def test_1_advice_is_the_release_not_the_build(self):
        _, out = self.docr()
        self.assertIn_("чекаут devkit: на релизе v9.9.0", out, "доктор не назвал режим чекаута")
        self.assertRegex(out, r"утилит devkit не в PATH: [^\n]*taskctl[^\n]*; поставить бинари "
                              r"релиза v9\.9\.0: devkitctl update",
                         "на теге доктор советует не релиз")
        self.assertNotIn_("go в PATH нет", out,
                          "на машине потребителя доктор зовёт ставить тулчейн")

    def test_2_fix_downloads_the_release(self):
        _, out = self.docr("--fix")
        for t in build.tools(self.box.dk):
            self.assertEqual(update.binary_stamp(self.dest / t), (self.tag, self.box.commit),
                             "--fix не поставил %s из релиза: %s" % (t, out))
        self.assertRegex(out, r"починено: установлено \d+ утилит релиза v9\.9\.0 в[^\n]*taskctl",
                         "--fix не отчитался о поставленных бинарях одной строкой")
        _, out = self.docr()
        self.assertNotIn_("поставить бинари релиза", out,
                          "после установки находка про бинари осталась")

    def fetch_head(self, days):
        # Возраст похода за тегами: сам поход тут делать некуда, origin у копии
        # нет, а меряется он одним mtime.
        path = self.box.dk / ".git" / "FETCH_HEAD"
        when = time.time() - days * 24 * 3600
        os.utime(str(path), (when, when))

    def test_3_release_findings_reach_the_doctor(self):
        # Стык check_machine с находками про релизы: юниты проверяют счёт, а тут
        # проверяется, что доктор их правда печатает. Это шаг 4 сценария
        # проверки задачи, и без него проводка держалась бы на честном слове.
        _, out = self.docr()
        self.assertNotIn_("а вышел", out, "новее тега чекаута ничего нет, а находка зажглась")
        self.assertNotIn_("за тегами devkit", out, "поход за тегами свежий, а находка зажглась")
        # Новейший тег ставится на коммит поверх HEAD, а сам HEAD остаётся на
        # своём релизе: так выглядит машина, до которой релиз ещё не доехал.
        rc, ahead = git(self.box.dk, "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "релиз выше")
        self.assertEqual(rc, 0, ahead)
        git(self.box.dk, "tag", "v9.9.1", ahead.strip())
        self.fetch_head(days=30)
        _, out = self.docr()
        self.assertIn_("машина: на машине стоит devkit v9.9.0, а вышел v9.9.1", out,
                       "доктор не печатает точную находку про вышедший релиз")
        self.assertIn_("devkitctl update", out, "находка не называет команду обновления")
        self.assertIn_("за тегами devkit не ходили 30 дней", out,
                       "доктор не печатает косвенную находку про давний поход за тегами")
        # Смыкание цикла на уровне доктора: поход за тегами гасит косвенную
        # находку, а точная не гаснет ни от чего, кроме обновления.
        self.fetch_head(days=0)
        _, out = self.docr()
        self.assertNotIn_("за тегами devkit", out, "свежий поход за тегами не погасил находку")
        self.assertIn_("а вышел v9.9.1", out, "точная находка погасла от похода за тегами")


class SelfReleaseDoctorTest(SandboxCase):
    """DK-163: на релизном чекауте самого devkit (детач на теге v*, машина
    потребителя, поставившая devkit по CONNECT.md) молчат находки, адресованные
    тому, кто devkit правит: git-хуки нужны для коммита, обвязка выката и доска
    трогаются тем же кругом, вес резидента считает тот, кто пишет скиллы и
    правила. Вклейка правил (rules.check) в этот список не входит: она сверяет
    содержимое самого чекаута, на исправном релизе молчит сама, а находка по
    ней это дефект выпуска, который стоит увидеть и потребителю. На ветке
    (машина разработчика) состав проверок не меняется.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        cls.sdk = box.root / "self" / "devkit"
        cls.sdk.mkdir(parents=True)
        for d in ("tools", "kit", "hooks"):
            shutil.copytree(str(box.dk / d), str(cls.sdk / d))
        # Дока остальных утилит доктору тут не нужна, а её битые ссылки на
        # docs/lld были бы шумом поверх находок, которые проверяются (тот же
        # приём, что у DoctorResidencyTest в weigh_test.py).
        for md in cls.sdk.rglob("*.md"):
            rel = md.relative_to(cls.sdk).parts
            if rel[:2] not in (("kit", "skills"), ("kit", "agents")):
                md.unlink()
        write(cls.sdk / "RULES.core.md", "# ядро\n\nтекст ядра.\n")
        write(cls.sdk / "RULES.board.core.md", "# ядро доски\n\nтекст ядра доски.\n")
        write(cls.sdk / "RULES.md", "# правила\n")
        write(cls.sdk / "RULES.board.md", "# правила доски\n")
        write(cls.sdk / "AGENTS.md", "# devkit\n\nтестовый проект.\n")
        write(cls.sdk / "docs" / "TASKS.md", "# Задачи\n\nПрефикс: SD\n")
        git_init(cls.sdk)
        git(cls.sdk, "add", "-A")
        git(cls.sdk, "commit", "-qm", "стенд для self-релиза")
        cls.sdkctl = cls.sdk / "tools" / "devkitctl" / "devkitctl.py"
        cls.shome = box.root / "self" / "home"
        box.make_home(cls.shome)
        box.global_rules(cls.shome, cls.sdk)
        cls.branch = git(cls.sdk, "symbolic-ref", "--quiet", "--short", "HEAD")[1].strip()
        # --fix подключает git-хуки и заводит болванку deploy.local: без этого
        # шага ветка сама по себе была бы засыпана находками, а тест смотрит на
        # то, гаснут ли они на релизе, а не на то, что они вообще бывают. Тег
        # ставится на закоммиченный результат --fix, иначе релизный чекаут
        # унёс бы с собой те же самые находки, которые он должен гасить.
        cls.sdoc("--fix")
        write(cls.sdk / ".devkit" / "deploy.local",
              "deploy = echo ok\ntest = echo ok\nautonomous = false\n")
        git(cls.sdk, "add", "-A")
        git(cls.sdk, "commit", "-qm", "починка self-стенда")
        cls.tag = "v9.5.0"
        git(cls.sdk, "tag", cls.tag)

    @classmethod
    def sdoc(cls, *args, **kw):
        # PATH обрезан до системного (тот же приём, что у DeployTest.sysdoctor):
        # без бинарей devkit в PATH машинный контур не сверяет их коммит с
        # историей self-чекаута, а нас интересует только проектная половина.
        kw.setdefault("path", str(cls.box.sys))
        return cls.box.dkctl_run("doctor", *(list(args) + ["-C", str(cls.sdk)]),
                                 dkctl=cls.sdkctl, home=cls.shome, **kw)

    def test_2_release_hides_git_hooks_and_weigh(self):
        git(self.sdk, "config", "--unset", "core.hooksPath")
        try:
            _, out = self.sdoc()
            self.assertIn_("git-хуки devkit не подключены", out,
                           "на ветке находка про git-хуки обязана остаться")
            self.assertRegex(out, r"(?m)^вес резидента devkit по карманам",
                             "на ветке доктор не печатает вес резидента")
            git(self.sdk, "checkout", "--detach", "--quiet", self.tag)
            try:
                _, out = self.sdoc()
                self.assertIn_("чекаут devkit: на релизе %s" % self.tag, out,
                               "доктор не назвал режим чекаута")
                self.assertNotIn_("git-хуки devkit не подключены", out,
                                  "git-хуки: находка не должна печататься на релизном чекауте")
                self.assertNotIn_("вес резидента", out,
                                  "таблица веса резидента не должна печататься на релизном чекауте")
            finally:
                git(self.sdk, "checkout", "--quiet", self.branch)
        finally:
            # "hooks" простой строкой, а не relpath от self.sdk: относительно
            # неразрешённого пути (mktemp на macOS отдаёт /var, симлинк на
            # /private/var) relpath посчитал бы обход через /private и
            # обратно, а не то же самое, что положил --fix в setUpClass.
            run(["git", "-C", str(self.sdk), "config", "core.hooksPath", "hooks"])
        _, out = self.sdoc()
        self.assertNotIn_("git-хуки devkit не подключены", out,
                          "восстановление git-хуков не вернуло self-чекаут в чистое состояние")

    def test_3_release_hides_deploy_local(self):
        deploy = self.sdk / ".devkit" / "deploy.local"
        text = read(deploy)
        deploy.unlink()
        try:
            _, out = self.sdoc()
            self.assertIn_(".devkit/deploy.local", out,
                           "на ветке находка про deploy.local обязана остаться")
            git(self.sdk, "checkout", "--detach", "--quiet", self.tag)
            try:
                _, out = self.sdoc()
                self.assertNotIn_(".devkit/deploy.local", out,
                                  "обвязка выката: находка не должна печататься на релизном "
                                  "чекауте")
            finally:
                git(self.sdk, "checkout", "--quiet", self.branch)
        finally:
            write(deploy, text)
        _, out = self.sdoc()
        self.assertNotIn_(".devkit/deploy.local", out,
                          "восстановление deploy.local не вернуло self-чекаут в чистое состояние")

    def test_4_connected_project_unaffected_by_devkit_release(self):
        # Сценарий 3 постановки: обвязка подключённого проекта проверяется в
        # прежнем составе, режим чекаута devkit на это не влияет.
        other = git_init(self.box.root / "self" / "otherproj")
        git(self.sdk, "checkout", "--detach", "--quiet", self.tag)
        try:
            _, out = self.box.dkctl_run("doctor", "-C", str(other), dkctl=self.sdkctl,
                                        home=self.shome)
            self.assertIn_("нет AGENTS.md в корне проекта", out,
                           "подключённый проект без AGENTS.md обязан дать находку "
                           "независимо от режима чекаута devkit")
        finally:
            git(self.sdk, "checkout", "--quiet", self.branch)


class WorktreeTest(SandboxCase):
    """devkit, выложенный worktree ветки задачи: mtime исходников там ничего не
    значит, сборка не запускается, а раскладка машинного контура отправляет в
    основной чекаут.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        cls.proj = box.project("mproj")
        cls.gostub = box.root / "gostub"
        cls.gostub.mkdir()
        executable(cls.gostub / "go", GO_STUB)
        git_init(box.dk)
        git(box.dk, "add", "-A")
        git(box.dk, "commit", "-qm", "init")
        git(box.dk, "worktree", "add", "-q", "-b", "probe", str(box.root / "devkit-wt"))
        cls.wtctl = box.root / "devkit-wt" / "tools" / "devkitctl" / "devkitctl.py"
        # Пути в находках доктор печатает разрешёнными, а mktemp на macOS отдаёт
        # /var, который на деле симлинк на /private/var.
        cls.dkreal = os.path.realpath(str(box.dk))
        cls.wthome = box.root / "wthome"
        cls.wtpath = "%s:%s" % (cls.gostub, box.sys)
        cls.rc, cls.out = cls.wtdoc("--fix")

    @classmethod
    def wtdoc(cls, *args, **kw):
        kw.setdefault("path", cls.wtpath)
        return cls.box.doctor(cls.proj, *args, dkctl=cls.wtctl, home=kw.pop("home", cls.wthome),
                              env={"SANDBOX": str(cls.box.root)}, **kw)

    def test_1_binaries_are_not_built(self):
        self.assertIn_("worktree ветки задачи", self.out, "нет находки про worktree devkit")
        self.assertIn_("%s/tools/devkitctl/devkitctl.py build" % self.dkreal, self.out,
                       "находка не отправляет собирать в основной чекаут")
        self.assertFalse((self.wthome / "go" / "bin" / "agentctl").exists(),
                         "--fix собрал машинный бинарь с фичеветки")

    def test_2_agent_definitions_are_not_laid_out(self):
        self.assertFalse((self.wthome / ".claude" / "agents" / "exec-medium.md").exists(),
                         "--fix разложил определения агентов с фичеветки")
        self.assertIn_("из основного чекаута: python3 %s/tools/devkitctl/devkitctl.py doctor --fix"
                       % self.dkreal, self.out, "находка про определения зовёт не в основной чекаут")

    def test_3_skills_keep_the_same_boundary(self):
        self.assertFalse((self.wthome / ".claude" / "skills" / "board-batch" / "SKILL.md").exists(),
                         "--fix разложил скилл с фичеветки")
        self.assertRegex(self.out, r"не разложено \d+ скиллов в[^\n]*board-batch[^\n]*%s"
                         % re.escape("%s/tools/devkitctl/devkitctl.py doctor --fix" % self.dkreal),
                         "находка про скилл зовёт не в основной чекаут")

    def test_4_global_rules_point_keeps_the_boundary(self):
        # Она значит для каждой сессии на машине сразу, и класть её с
        # непроверенной ветки нельзя так же, как определения агентов и скиллы.
        self.assertFalse((self.wthome / ".claude" / "CLAUDE.md").exists(),
                         "--fix сгенерил глобальную точку правил с фичеветки")
        self.assertRegex(self.out, r"%s/\.claude/CLAUDE\.md.*worktree ветки задачи" % self.wthome,
                         "находка про глобальную точку не называет worktree devkit")
        self.assertIn_("из основного чекаута %s:" % self.dkreal, self.out,
                       "находка про глобальную точку зовёт не в основной чекаут")

    def test_5_machine_perms_keep_the_boundary(self):
        # Рубеж тут строже прочих: выданное право видно каждой сессии на машине
        # сразу, так что выписать их себе правкой перечня на своей же ветке цикл
        # не должен даже случайно.
        self.assertFalse((self.wthome / ".claude" / "settings.json").exists(),
                         "--fix выписал права машинного контура с фичеветки")
        self.assertIn_("не хватает прав машинного контура", self.out,
                       "с worktree потерялась находка про права машинного контура")
        self.assertIn_("права с непроверенной ветки на машину не едут", self.out,
                       "находка про права не называет worktree devkit")
        self.assertIn_("%s/tools/devkitctl/devkitctl.py doctor --fix" % self.dkreal, self.out,
                       "находка про права зовёт чинить не из основного чекаута")

    def test_6_diverged_definition_is_compared_with_the_main_checkout(self):
        agents = self.wthome / ".claude" / "agents"
        agents.mkdir(parents=True, exist_ok=True)
        for f in (self.box.dk / "kit" / "agents").glob("*.md"):
            shutil.copy(str(f), str(agents / f.name))
        write(agents / "exec-high.md", read(agents / "exec-high.md") + "\nсвоя строка\n")
        _, out = self.wtdoc()
        self.assertRegex(out, r"разошлось определение агента в[^\n]*exec-high",
                         "сверка определения идёт не с основным чекаутом")
        # Защита from_main действует и для перезаписи: --fix с worktree ветки
        # задачи разошедшееся определение не перекладывает, находка остаётся.
        _, out = self.wtdoc("--fix")
        self.assertRegex(out, r"разошлось определение агента в[^\n]*exec-high",
                         "с worktree --fix потерял находку про разошедшееся определение")
        self.assertIn("своя строка", read(agents / "exec-high.md"),
                      "с worktree --fix переложил определение с непроверенной ветки")

    def test_7_stale_global_point_is_not_regenerated(self):
        # Маркер сходится со своим телом, а путь devkit в нём чужой: с worktree
        # находка остаётся, --fix её не перегенерирует до основного чекаута.
        (self.wthome / ".claude").mkdir(parents=True, exist_ok=True)
        prof = harness.parse("p.toml", read(self.box.dk / "kit" / "harness" / "claude-code.toml"))
        gclaude = self.wthome / ".claude" / "CLAUDE.md"
        # Текст точки зависит от HOME (путь до devkit пишется от ~), и снимать
        # его надо под тем же HOME, под которым доктор потом её и прочтёт.
        with fake_home(self.wthome):
            write(gclaude, rules.global_thin_text(prof, "/nowhere/stale-devkit"))
        stale = read(gclaude)
        _, out = self.wtdoc("--fix")
        self.assertRegex(out, r"%s устарел.*из основного чекаута %s:" % (gclaude, self.dkreal),
                         "с worktree находка про устаревшую глобальную точку не зовёт в чекаут")
        self.assertEqual(read(gclaude), stale,
                         "с worktree --fix перегенерировал глобальную точку с непроверенной ветки")

    def test_8_binaries_are_compared_with_the_main_checkout(self):
        # Сравнение идёт с HEAD основного чекаута, а не worktree, поэтому
        # параллельная сессия на ветке задачи находок не сыплет. Бинари тут
        # нарочно старее выкладки worktree: раньше mtime дал бы четыре ложные
        # находки, теперь он ничего не решает.
        wtbin = self.box.root / "wtbin"
        wtbin.mkdir(exist_ok=True)
        for t in build.tools(self.box.dk):
            executable(wtbin / t, BINARY_STUB % build.version_line(t, self.box.version,
                                                                   self.box.commit))
            os.utime(str(wtbin / t), (946684800, 946684800))
        _, out = self.wtdoc("--fix", path="%s:%s:%s" % (self.gostub, wtbin, self.box.sys))
        self.assertNotIn_("собран из коммита", out, "из worktree бинари объявлены разошедшимися")
        for t in build.tools(self.box.dk):
            self.assertNotIn_("%s не в PATH" % t, out, "из worktree %s объявлен ненайденным" % t)
        self.assertNotIn_("починено", out, "из worktree --fix что-то пересобрал")

    def test_9_hook_paths_are_not_repointed_to_the_branch_tree(self):
        # Рубеж тот же, что у прав и скиллов, и держит он именно ту беду, с
        # которой задача завелась (DK-582): пути хуков, переписанные на дерево
        # ветки, забирают у машины весь канал разом, а на своей ветке этого не
        # видно. Из основного чекаута их перенацелит --fix, отсюда только
        # находка с путём выкаченного дерева.
        home = self.box.make_home(self.box.root / "wthome-hooks")
        settings = home / ".claude" / "settings.json"
        wt = os.path.realpath(str(self.box.root / "devkit-wt"))
        before = read(settings).replace("%s/hooks/" % self.dkreal, "%s/hooks/" % wt)
        write(settings, before)
        _, out = self.wtdoc("--fix", home=home)
        self.assertEqual(read(settings), before,
                         "с worktree --fix переписал пути хуков на дерево ветки")
        self.assertIn_("не из выкаченного дерева %s" % self.dkreal, out,
                       "находка про хуки не называет выкаченное дерево")
        self.assertIn_("пути хуков с непроверенной ветки на машину не едут", out,
                       "находка про хуки не называет worktree devkit")


class StatsTest(SandboxCase):
    """stats: вывод сводки по журналу запусков, сортировка по частоте."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("sproj")
        write(cls.proj / ".devkit" / "log", LOG)
        cls.rc, cls.out = cls.box.dkctl_run("stats", "-C", str(cls.proj))

    def test_table(self):
        self.assertEqual(self.rc, 0, "stats упал с ошибкой: %s" % self.out)
        self.assertIn_("taskctl move", self.out, "в выводе stats нет taskctl move")
        self.assertIn_("shipctl merge", self.out, "в выводе stats нет shipctl merge")
        self.assertIn_("итого", self.out, "в выводе stats нет итоговой строки")

    def test_sorted_by_frequency(self):
        # taskctl move должно быть первым (4 запуска) несмотря на порядок в журнале.
        first = [ln for ln in self.out.split("\n") if ln and "итого" not in ln][0]
        self.assertRegex(first, r"taskctl move.*4",
                         "taskctl move должно быть первым с 4 запусками")

    def test_error_share(self):
        line = [ln for ln in self.out.split("\n") if "taskctl move" in ln][0]
        self.assertIn("ошибок 1 (25%)", line, "taskctl move должно иметь 1 ошибку (25%)")

    def test_broken_lines_are_skipped(self):
        lines = [ln for ln in self.out.split("\n") if ln and "итого" not in ln]
        self.assertEqual(len(lines), 3, "должно быть 3 команды, найдено %d" % len(lines))

    def test_without_log(self):
        empty = self.box.root / "empty"
        empty.mkdir()
        rc, _ = self.box.dkctl_run("stats", "-C", str(empty))
        self.assertEqual(rc, 2, "stats без журнала должен вернуть код 2")

    def test_only_broken_lines(self):
        bad = self.box.root / "bad"
        write(bad / ".devkit" / "log", "broken\tline\tno\tcode\nthis\tis\tbroken\ttoo\n")
        rc, _ = self.box.dkctl_run("stats", "-C", str(bad))
        self.assertEqual(rc, 2, "stats с одними битыми строками должен вернуть код 2")


class FreshConnectTest(SandboxCase):
    """Подключение обычного проекта с нуля, как его гоняет CONNECT.md (DK-125):
    директории нет вовсе, и заводит её вместе с git-репозиторием сама команда.
    Раньше первый же шаг инструкции падал traceback на записи AGENTS.md, а после
    ручного mkdir проект оставался без git, и shipctl start падал следом.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Автор коммитов: в чистом CI git его не угадывает, а первый коммит
        # делает new.
        write(cls.box.home / ".gitconfig", "[user]\n\tname = t\n\temail = t@t\n")

    def test_0_internal_module_resolves_in_sandbox(self):
        # DK-266: Sandbox обязан выложить internal/, иначе потребитель с relative
        # replace ../../internal в go.mod (shipctl с DK-266) из тестового чекаута
        # с GOWORK=off не собирается. test_2 собирает shipctl для прогона start
        # и тем уже ловил регрессию, но собирать потребителя стоит и напрямую,
        # без привязки к сценарию подключения: фикс копирования internal сам по
        # себе, и падает он на «replacement directory ../../internal does not
        # exist».
        env = go_cache_env()
        env["GOWORK"] = "off"
        rc, out = run(["go", "build", "-o", os.devnull, "."],
                      cwd=str(self.box.dk / "tools" / "shipctl"), env=env)
        self.assertEqual(rc, 0, "shipctl не собрался из Sandbox: %s" % out)

    def test_1_new_on_a_missing_directory(self):
        proj = self.box.root / "fresh-proj"
        self.assertFalse(proj.exists(), "фикстура не та: директория проекта уже есть")
        rc, out = self.box.dkctl_run("new", "--prefix", "FR", "-C", str(proj))
        self.assertEqual(rc, 0, "new на несуществующей директории не прошёл: %s" % out)
        self.assertTrue((proj / "AGENTS.md").is_file(), "new не завёл проект на пустом месте")
        self.assertTrue((proj / ".git").is_dir(), "new оставил проект без git-репозитория")
        self.assertRegex(out, r"директория .* заведена", "new промолчал про заведённую директорию")
        self.assertIn_("git-репозиторий заведён", out, "new промолчал про git init")
        self.assertNotIn_("не git-репозиторий", out, "new считает свежий проект не репозиторием")
        self.assertEqual(git(proj, "config", "core.hooksPath")[1].strip(), "../devkit/hooks",
                         "git-хуки не подключены на заведённом с нуля проекте")

    def test_2_start_after_new(self):
        # Цель правки была не в том, чтобы завести .git, а в том, чтобы следующий
        # шаг инструкции работал, поэтому проверяется он сам: настоящий shipctl
        # start после настоящего new. Заглушки тут не годятся, бинари собираются
        # из той же копии devkit. Входа два: директории нет вовсе и директория уже
        # git-репозиторий без единого коммита (кто-то сделал git init заранее), в
        # обоих HEAD неродившийся, и без первого коммита start падает на «не нашёл
        # ветку main или master».
        realbin = self.box.root / "realbin"
        realbin.mkdir()
        for tool in ("taskctl", "shipctl"):
            env = go_cache_env()
            env["GOWORK"] = "off"
            rc, out = run(["go", "build", "-o", str(realbin / tool), "."],
                          cwd=str(self.box.dk / "tools" / tool), env=env)
            self.assertEqual(rc, 0, "%s для прогона start не собрался: %s" % (tool, out))
        realpath = "%s:%s" % (realbin, self.box.sys)
        for root, prefix, case in ((self.box.root / "start-fresh", "SF", "директории не было"),
                                   (self.box.root / "start-preinit", "SP",
                                    "репозиторий заведён заранее и пуст")):
            if case.startswith("репозиторий"):
                root.mkdir()
                run(["git", "init", "-q", str(root)])
                self.assertNotEqual(run(["git", "-C", str(root), "rev-parse", "--verify",
                                         "HEAD"])[0], 0,
                                    "фикстура не та: в репозитории уже есть коммиты")
            rc, out = self.box.dkctl_run("new", "--prefix", prefix, "-C", str(root),
                                         path=realpath)
            self.assertEqual(rc, 0, "new не прошёл (%s): %s" % (case, out))
            rc, out = run(["git", "-C", str(root), "rev-parse", "--verify", "HEAD"],
                          home=self.box.home, path=realpath)
            self.assertEqual(rc, 0, "после new у проекта нет ни одного коммита (%s): %s" % (case, out))
            rc, out = run(["taskctl", "-C", str(root), "add", "--title", "первая задача",
                           "--type", "task", "--rank", "25+5+1+0+0", "--cost", "S",
                           "--accept", "agent"],
                          home=self.box.home, path=realpath)
            self.assertEqual(rc, 0, "taskctl add после new не прошёл (%s): %s" % (case, out))
            task = "%s-001" % prefix
            rc, out = run(["shipctl", "-C", str(root), "start", task],
                          home=self.box.home, path=realpath)
            self.assertEqual(rc, 0, "shipctl start после new не прошёл (%s): %s" % (case, out))
            wt = Path(str(root) + "-" + task.lower())
            self.assertTrue(wt.is_dir(), "start не завёл дерево задачи (%s): %s" % (case, out))
            rc, out = run(["git", "-C", str(root), "rev-parse", "--verify", task.lower()],
                          home=self.box.home, path=realpath)
            self.assertEqual(rc, 0, "start не завёл ветку задачи (%s): %s" % (case, out))

    def test_3_refusal_leaves_nothing_behind(self):
        # Отказ по аргументам идёт до раскладки: полупустой директории после него
        # не остаётся.
        never = self.box.root / "never-proj"
        rc, out = self.box.dkctl_run("new", "-C", str(never))
        self.assertEqual(rc, 2, "new без --prefix и --no-board не отказал: %s" % out)
        self.assertFalse(never.exists(), "отказавший new оставил за собой директорию")


class HarnessHooksTest(SandboxCase):
    """Хуки харнеса раскладывает doctor --fix, а не человек по README (DK-125)."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.home2 = cls.box.root / "home2"
        write(cls.home2 / ".claude" / "settings.json", '{\n  "model": "opus"\n}\n')
        cls.settings = cls.home2 / ".claude" / "settings.json"

    def test_renamed_hook_is_retired(self):
        # Прежнее имя хука в настройках зовёт файл, которого в чекауте больше нет
        # (DK-440), и харнес спотыкается на нём каждым ходом. Поэтому доктор не
        # дописывает новую строку рядом со старой, а сперва убирает старую.
        home = self.box.root / "home-retired"
        write(home / ".claude" / "settings.json", json.dumps({"hooks": {"PostToolUse": [
            {"hooks": [{"type": "command",
                        "command": "python3 ~/projects/devkit/hooks/inbox.py --hook claude-code"}]}]}},
            ensure_ascii=False, indent=2) + "\n")
        _, out = self.box.doctor(self.proj, home=home)
        self.assertIn_("inbox.py", out, "доктор не заметил отставное имя хука")
        self.assertIn_("переименован в chat-in.py", out,
                       "находка не называет нынешнее имя хука")
        _, out = self.box.doctor(self.proj, "--fix", home=home)
        self.assertIn_("убрано из", out, "--fix не убрал отставную строку")
        hooks = json.loads(read(home / ".claude" / "settings.json"))["hooks"]
        post = [h["command"] for g in hooks["PostToolUse"] for h in g["hooks"]]
        self.assertEqual([c for c in post if "inbox.py" in c], [],
                         "отставная строка осталась в настройках: %s" % post)
        self.assertEqual(len([c for c in post if "chat-in.py" in c]), 1, post)
        # Повторный доктор про отставное имя молчит: чинить больше нечего.
        _, out = self.box.doctor(self.proj, home=home)
        self.assertNotIn_("inbox.py", out, "повторный доктор всё ещё видит отставное имя")

    def test_hooks_are_laid_out_once(self):
        _, out = self.box.doctor(self.proj, home=self.home2)
        self.assertRegex(out, r"не подключено \d+ хук\S* харнеса в[^\n]*PostToolUse[^\n]*check-symbols\.py",
                         "доктор не заметил неподключённые хуки харнеса")
        self.assertRegex(out, r"check-read-secret\.py[^\n]*чтение секретов через Bash идёт мимо хука",
                         "находка про чтение секретов не называет свою дыру")
        # Рубеж подстановки (DK-452) говорит своей строкой: без неё его
        # отсутствие выдавалось бы за дыру чтения секретов.
        self.assertRegex(out, r"check-subst\.py[^\n]*исполняется до их вызова",
                         "доктор не заметил PreToolUse-хук подстановки DK-452")
        # Рубеж синхронности (DK-678) говорит своей категорией: без неё пропажа
        # рубежа выдавалась бы за дыру чтения секретов, а ломается там другое,
        # фоновый ход headless-сессии.
        self.assertRegex(out, r"рубеж check-background\.py не подключён на событии PreToolUse",
                         "доктор не заметил неподключённый рубеж синхронности")
        self.assertIn_("провал выходит тихим", out,
                       "находка не говорит, что ломается без рубежа синхронности")
        self.assertRegex(out, r"на PreToolUse Read[^\n]*check-reread\.py|check-reread\.py[^\n]*на PreToolUse Read",
                         "доктор не заметил PreToolUse-хук повторных чтений")
        self.assertRegex(out, r"на PreToolUse Read[^\n]*check-longfile\.py|check-longfile\.py[^\n]*на PreToolUse Read",
                         "доктор не заметил PreToolUse-хук длинных чтений")
        # Подхват реплики говорит своей категорией: без неё --fix хук положит, а
        # doctor без ключа промолчит, и неподключённый канал чата останется
        # неотличим от штатной тишины.
        self.assertRegex(out, r"подхват реплики chat-in\.py не подключён на событии PostToolUse",
                         "доктор не заметил неподключённый подхват реплики")
        # Отметка работы по факту правки (DK-539) говорит своей категорией,
        # отдельной от рождения сессии на SessionStart: без неё правка файла в
        # боковом дереве задачи не попадает в журнал вовсе.
        self.assertRegex(out, r"PostToolUse-хук session-task\.py --touch не подключён",
                         "доктор не заметил неподключённый хук отметки работы")
        self.assertIn_("не оставляет отметку в журнале сессий", out,
                       "находка не говорит, что ломается без хука отметки работы")
        _, out = self.box.doctor(self.proj, "--fix", home=self.home2)
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*check-symbols\.py на PostToolUse",
                         "--fix не разложил хуки харнеса")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*check-read-secret\.py на PreToolUse",
                         "--fix не разложил PreToolUse-хук чтения секретов")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*check-subst\.py на PreToolUse",
                         "--fix не разложил PreToolUse-хук подстановки DK-452")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*check-background\.py на PreToolUse",
                         "--fix не разложил рубеж синхронности DK-678")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*check-reread\.py на PreToolUse",
                         "--fix не разложил PreToolUse-хук повторных чтений")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*check-longfile\.py на PreToolUse",
                         "--fix не разложил PreToolUse-хук длинных чтений")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*chat-in\.py на PostToolUse",
                         "--fix не разложил подхват реплики")
        self.assertRegex(out,
                         r"включено \d+ хук\S* харнеса в[^\n]*session-task\.py на SessionStart, PostToolUse",
                         "--fix не разложил хук отметки работы")
        self.assertRegex(out, r"включено \d+ хук\S* харнеса в[^\n]*agent-watch\.py на PostToolUse",
                         "--fix не разложил сторожа фоновых субагентов")
        data = json.loads(read(self.settings))
        self.assertEqual(data.get("model"), "opus", "рукописное в настройках потерялось")
        hooks = data["hooks"]
        post = [h["command"] for g in hooks["PostToolUse"] for h in g["hooks"]]
        self.assertEqual(len([c for c in post if "check-symbols.py" in c]), 1, post)
        self.assertEqual(len([c for c in post if "chat-in.py" in c]), 1, post)
        self.assertEqual(len([c for c in post if "session-task.py --touch" in c]), 1, post)
        # PostToolUse: три группы, проверки текстов на своём матчере, подхват
        # реплики с отметкой работы на пустом и сторож фоновых субагентов на
        # инструменте делегирования. Матчер у подхвата пустой не по недосмотру:
        # реплику надо доставлять на любом ходе идущего витка, а не на записи
        # файла, и отметку работы (DK-539) режет своим списком WORK_TOOLS сам
        # скрипт, а не матчер.
        self.assertEqual([g.get("matcher") for g in hooks["PostToolUse"]],
                         ["Edit|Write|NotebookEdit", None, "Agent"], hooks["PostToolUse"])
        self.assertEqual(len([c for c in post if "agent-watch.py" in c]), 1, post)
        chat = [h["command"] for g in hooks["PostToolUse"] if not g.get("matcher")
                for h in g["hooks"]]
        self.assertEqual(len(chat), 2, chat)
        self.assertIn("chat-in.py", chat[0])
        self.assertIn("session-task.py --touch", chat[1])
        # PreToolUse: три группы на трёх матчерах, Bash (чтение секретов и
        # подстановка), Bash с инструментом делегирования (рубеж синхронности:
        # фоном зовутся оба, и матчер у рубежа поэтому свой) и Read (повторные
        # чтения и длинные чтения). Каждая своим скриптом, порядок как в
        # HOOK_LAYOUT.
        pre = [h["command"] for g in hooks["PreToolUse"] for h in g["hooks"]]
        self.assertEqual(len([c for c in pre if "check-read-secret.py" in c]), 1, pre)
        self.assertEqual(len([c for c in pre if "check-subst.py" in c]), 1, pre)
        self.assertEqual(len([c for c in pre if "check-background.py" in c]), 1, pre)
        self.assertEqual(len([c for c in pre if "check-reread.py" in c]), 1, pre)
        self.assertEqual(len([c for c in pre if "check-longfile.py" in c]), 1, pre)
        self.assertEqual([g.get("matcher") for g in hooks["PreToolUse"]],
                         ["Bash", "Bash|Agent", "Read"], hooks["PreToolUse"])
        for event in ("Notification", "Stop", "StopFailure", "SubagentStop", "UserPromptSubmit"):
            cmds = [h["command"] for g in hooks[event] for h in g["hooks"]]
            self.assertEqual(len([c for c in cmds if "notify.py" in c]), 1, (event, cmds))
        # Сторож фоновых субагентов (DK-519) стоит рядом с уведомителем на конце
        # хода и на конце субагента: уведомитель говорит наружу человеку, сторож
        # внутрь сессии.
        for event in ("SubagentStop", "Stop"):
            cmds = [h["command"] for g in hooks[event] for h in g["hooks"]]
            self.assertEqual(len([c for c in cmds if "agent-watch.py" in c]), 1, (event, cmds))
        # Ретрай-вотчдог (DK-172) ложится тем же --fix: env-ключ, с которым
        # обрыв сети ретраится, а не останавливает ход до ручного «продолжай».
        self.assertEqual(data.get("env", {}).get(devkitctl.WATCHDOG_KEY),
                         devkitctl.WATCHDOG_VALUE, data.get("env"))
        start = [h["command"] for g in hooks["SessionStart"] for h in g["hooks"]]
        self.assertEqual(len([c for c in start if "quota-refresh.sh" in c]), 1, start)
        self.assertEqual(len([c for c in start if "session-task.py" in c]), 1, start)
        # Догон бокового дерева доски (DK-269) ложится туда же, третьим
        # SessionStart-хуком, и повторный --fix его не дублирует.
        self.assertEqual(len([c for c in start if "board-catchup.sh" in c]), 1, start)
        # Догон самого чекаута devkit ложится четвёртым, и повторный --fix его
        # тоже не дублирует.
        self.assertEqual(len([c for c in start if "devkit-catchup.sh" in c]), 1, start)
        # Повторный --fix хуки второй раз не раскладывает.
        _, out = self.box.doctor(self.proj, "--fix", home=self.home2)
        self.assertNotIn_("хук харнеса на", out, "повторный --fix разложил хуки второй раз")
        self.assertNotIn_("env-ключ", out, "повторный --fix вписал вотчдог второй раз")
        post = [h["command"] for g in json.loads(read(self.settings))["hooks"]["PostToolUse"]
                for h in g["hooks"]]
        self.assertEqual(len(post), 7, post)

    def test_hooks_from_a_stray_tree_are_repointed(self):
        # DK-582: строка с путём чужого дерева выглядит подключённым хуком, и по
        # именам скриптов доктор её пропускал, а работает там своя копия файла.
        # Правка хука в основном чекауте до сессий машины при этом не доезжает
        # вовсе, и молчание канала от штатной работы не отличить.
        home = self.box.make_home(self.box.root / "home-stray")
        settings = home / ".claude" / "settings.json"
        dkreal = os.path.realpath(str(self.box.dk))
        stray = str(self.box.root / "devkit-branch")
        before = read(settings).replace("%s/hooks/notify.py" % dkreal,
                                        "%s/hooks/notify.py" % stray)
        write(settings, before)
        _, out = self.box.doctor(self.proj, home=home)
        self.assertIn_("не из выкаченного дерева %s" % dkreal, out,
                       "доктор не заметил хук из чужого дерева")
        self.assertIn_(stray, out, "находка не называет чужое дерево")
        self.assertIn_("notify.py", out, "находка не называет хук из чужого дерева")
        self.assertEqual(read(settings), before, "доктор без --fix правил настройки")
        _, out = self.box.doctor(self.proj, "--fix", home=home)
        self.assertIn_("перенацелено", out, "--fix не перенацелил хуки на выкаченное дерево")
        after = read(settings)
        self.assertNotIn(stray, after, "путь чужого дерева остался в настройках")
        self.assertEqual(len([c for c in after.split("\n") if "notify.py" in c]), 5, after)
        # Повторный доктор про дерево молчит: перенацеливать больше нечего.
        _, out = self.box.doctor(self.proj, home=home)
        self.assertNotIn_("не из выкаченного дерева", out,
                          "повторный доктор всё ещё видит хук из чужого дерева")


GLM_PROFILE = """# Профиль стенда: близнец claude-code с путями от {home}.

[detect]

[rules]
mode = "import"
file = "CLAUDE.md"
import_line = "@{path}"
global_file = "{home}/CLAUDE.md"

[delegate]
mode = "cli"
command = ["claude", "-p", "{prompt}"]
agents_dir = "{home}/agents"
agents_format = "claude-code"

[hooks]
protocol = "claude-code"
config = "{home}/settings.json"
events = ["write", "session-start", "notify", "subagent-done", "turn-done", "prompt-submit"]
memory_index = "/memory/MEMORY.md"

[skills]
dir = "{home}/skills"
format = "claude-code"
discovery = "auto"

[quota]
"""

MACHINE_CONF = 'enabled = ["claude-code", "glm-code"]\n\n[glm-code]\nhome = "~/.claude-glm"\n'


def snapshot(home, skip=()):
    """Слепок раскладки в HOME по содержимому: путь -> байты.

    Им держится главный инвариант переезда с констант ~/.claude на профили: на
    машине с одним включённым харнесом разложенное обязано совпасть побайтно с
    тем, что раскладывалось до переезда.
    """
    out = {}
    for p in sorted(Path(home).rglob("*")):
        rel = p.relative_to(home).as_posix()
        if not p.is_file() or any(rel.startswith(s) for s in skip):
            continue
        out[rel] = p.read_bytes()
    return out


class RetryWatchdogTest(unittest.TestCase):
    """Env-ключ ретрай-вотчдога в настройках харнеса (DK-172).

    Ключ публичной докой Claude Code не описан, найден strings бинаря;
    разделяющий замер стенда (docs/tasks/DK-172.md) разницы в поведении
    харнеса с ним и без него не нашёл. Раскладка всё равно кладёт его
    безвредным заделом: пробел чинит doctor --fix, а значение, вписанное
    человеком, не трогается: выключенный вотчдог это его решение."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="dk172-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.settings = self.dir / "settings.json"

    def test_gap_on_empty_settings(self):
        gap, finding = devkitctl.watchdog_gap("", self.settings)
        self.assertTrue(gap)
        self.assertIn(devkitctl.WATCHDOG_KEY, finding)
        self.assertIn("doctor --fix", finding)

    def test_no_gap_when_key_is_set(self):
        text = json.dumps({"env": {devkitctl.WATCHDOG_KEY: "1"}})
        self.assertEqual(devkitctl.watchdog_gap(text, self.settings), (False, ""))

    def test_human_value_is_kept(self):
        # Хоть "0": выключенный вотчдог это решение человека, а не пробел.
        text = json.dumps({"env": {devkitctl.WATCHDOG_KEY: "0"}})
        self.assertEqual(devkitctl.watchdog_gap(text, self.settings), (False, ""))

    def test_env_of_wrong_shape_is_a_manual_finding(self):
        gap, finding = devkitctl.watchdog_gap(json.dumps({"env": ["x"]}), self.settings)
        self.assertFalse(gap)
        self.assertIn("не объект json", finding)

    def test_broken_json_is_silent(self):
        # Про нечитаемый файл говорят проверки того же файла рубежом раньше.
        self.assertEqual(devkitctl.watchdog_gap("{оборвано", self.settings), (False, ""))

    def test_install_keeps_neighbours(self):
        write(self.settings, json.dumps({"model": "opus", "env": {"FOO": "bar"}}))
        said = devkitctl.install_watchdog(self.settings)
        self.assertEqual(len(said), 1, said)
        self.assertIn(devkitctl.WATCHDOG_KEY, said[0])
        data = json.loads(read(self.settings))
        self.assertEqual(data["model"], "opus")
        self.assertEqual(data["env"]["FOO"], "bar")
        self.assertEqual(data["env"][devkitctl.WATCHDOG_KEY], devkitctl.WATCHDOG_VALUE)
        # Повторная установка молчит: ключ уже стоит.
        self.assertEqual(devkitctl.install_watchdog(self.settings), [])

    def test_install_into_missing_file(self):
        said = devkitctl.install_watchdog(self.settings)
        self.assertEqual(len(said), 1, said)
        data = json.loads(read(self.settings))
        self.assertEqual(data["env"][devkitctl.WATCHDOG_KEY], devkitctl.WATCHDOG_VALUE)


class AltSubDirTest(unittest.TestCase):
    """Каталог конфига второй подписки берётся из машинного ключа (DK-180).

    Ключ этот один на трёх читателей: раскладку машинного хозяйства
    (плейсхолдер {home} в путях профиля), окружение подпроцесса делегирования и
    окно редактора shipctl code. Была бы у каждого своя константа, они бы
    разъехались, а разъехавшись, дали бы сессию на дорогой подписке, считающую
    себя дешёвой.
    """

    def home(self, machine=""):
        home = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, str(home), True)
        if machine:
            write(home / ".devkit" / "harness.local", machine)
        return home

    def test_undeclared_subscription_is_silent(self):
        # Харнеса второй подписки в машинном слое нет, значит подписки на этой
        # машине нет вовсе, и звать вписывать её endpoint значит выдумать
        # человеку работу.
        home = self.home('enabled = ["claude-code"]\n')
        with fake_home(home):
            findings, fixed = devkitctl.check_alt_sub(True)
        self.assertEqual((findings, fixed), ([], []),
                         "доктор говорит про вторую подписку на машине, где её не заведено")
        self.assertFalse(list(home.rglob("settings.json")),
                         "болванка второй подписки разложена без объявления в машинном слое")

    def test_blank_goes_to_the_declared_dir(self):
        # Каталог нарочно не тот, что стоял константой: раскладка обязана идти
        # по ключу, а не по памяти утилиты.
        home = self.home('enabled = ["claude-code", "glm-code"]\n\n[glm-code]\n'
                         'home = "~/своя-вторая-подписка"\n')
        with fake_home(home):
            findings, fixed = devkitctl.check_alt_sub(True)
        conf = home / "своя-вторая-подписка" / "settings.json"
        self.assertTrue(conf.is_file(), "болванка не разложена по машинному ключу: %s" % (findings,))
        self.assertEqual(json.loads(read(conf))["env"],
                         {k: "" for k in devkitctl.ALT_SUB_KEYS},
                         "болванка разложена не теми ключами")
        self.assertTrue([f for f in fixed if "своя-вторая-подписка" in f],
                        "отчёт не назвал каталог, в который разложена болванка: %s" % (fixed,))
        # Пустые ключи остаются находкой: значения берутся в кабинете подписки, и
        # придумать их автоматике нечем.
        self.assertTrue([f for f in findings if "пустые ключи" in f],
                        "пустые ключи болванки не названы находкой: %s" % (findings,))


class WindowCopyTest(unittest.TestCase):
    """Окружение копии окна второй подписки против машинного слоя (DK-192).

    Копия окна это вечное дерево рядом с проектом, в котором живёт окно
    редактора, а ключи подписки лежат в настройках самой директории: иначе окно,
    переоткрытое из дока, молча уходило бы на дорогую подписку. Хозяин ключей
    при этом машинный слой, и разъезд с ним стоит ровно того же, только тише:
    окно ходит по старым ключам, а видно это по счёту в конце недели.
    """

    def stand(self, env=None, copy_env=None, name="проект"):
        # Стенд это машинный слой с объявленной подпиской, проект и копия окна
        # рядом с ним. Копия узнаётся по редиректу .git, как настоящее
        # линкованное дерево.
        home = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, str(home), True)
        write(home / ".devkit" / "harness.local",
              'enabled = ["claude-code", "glm-code"]\n\n[glm-code]\nhome = "~/.devkit/claude-glm"\n')
        write(home / ".devkit" / "claude-glm" / "settings.json",
              json.dumps({"env": env if env is not None else {
                  "ANTHROPIC_BASE_URL": "https://новый.example",
                  "ANTHROPIC_AUTH_TOKEN": "токен-новый",
                  "ANTHROPIC_MODEL": "glm-4.6",
              }}))
        root = home / "projects" / name
        write(root / "docs" / "TASKS.md", "# доска\n")
        copy = home / "projects" / (name + "-" + devkitctl.WINDOW_SUFFIX)
        write(copy / ".git", "gitdir: %s\n" % (root / ".git" / "worktrees" / "копия"))
        if copy_env is not None:
            write(copy / devkitctl.WINDOW_ENV_FILE, json.dumps(copy_env))
            (copy / devkitctl.WINDOW_ENV_FILE).chmod(0o600)
        return home, root, copy

    def test_drift_is_a_finding_and_fix_rewrites_it(self):
        home, root, copy = self.stand(copy_env={
            "env": {"ANTHROPIC_BASE_URL": "https://старый.example",
                    "ANTHROPIC_AUTH_TOKEN": "токен-старый",
                    "ANTHROPIC_MODEL": "glm-4.6",
                    "DEVKIT_HARNESS": "glm-code"},
            "permissions": {"allow": ["Bash(ls:*)"]},
        })
        with fake_home(home):
            findings, fixed = devkitctl.check_window_copy(root, False)
        self.assertTrue([f for f in findings if "ANTHROPIC_BASE_URL" in f and "ANTHROPIC_AUTH_TOKEN" in f],
                        "разъехавшиеся ключи копии не названы находкой: %s" % (findings,))
        self.assertFalse([f for f in findings if "токен-новый" in f or "токен-старый" in f],
                         "токен напечатан в находке: %s" % (findings,))
        with fake_home(home):
            findings, fixed = devkitctl.check_window_copy(root, True)
        doc = json.loads(read(copy / devkitctl.WINDOW_ENV_FILE))
        self.assertEqual(doc["env"]["ANTHROPIC_BASE_URL"], "https://новый.example")
        self.assertEqual(doc["env"]["ANTHROPIC_AUTH_TOKEN"], "токен-новый")
        self.assertEqual(doc["permissions"], {"allow": ["Bash(ls:*)"]},
                         "починка стёрла написанное человеком рядом с ключами подписки")
        self.assertTrue(fixed, "починка прошла молча")
        with fake_home(home):
            self.assertEqual(devkitctl.check_window_copy(root, False), ([], []),
                             "после починки доктор всё ещё находит расхождение")

    def test_copy_without_env_is_a_finding(self):
        # Копия есть, а окружения в ней нет: окно в ней уходит на первую
        # подписку, и молчать про это дороже всего.
        home, root, copy = self.stand()
        with fake_home(home):
            findings, _ = devkitctl.check_window_copy(root, False)
        self.assertTrue([f for f in findings if str(copy) in f],
                        "копия без окружения второй подписки прошла молча: %s" % (findings,))

    def test_doctor_from_the_copy_checks_the_copy(self):
        # Доктора зовут и из самого окна: проверять он обязан ту копию, в
        # которой стоит, а не искать соседа с ещё одним суффиксом.
        home, root, copy = self.stand()
        with fake_home(home):
            findings, _ = devkitctl.check_window_copy(copy, False)
        self.assertTrue([f for f in findings if str(copy) in f],
                        "доктор из копии окна её не проверил: %s" % (findings,))

    def test_project_without_copy_is_silent(self):
        # Окном второй подписки работают не над каждым проектом, и копии рядом
        # может не быть вовсе.
        home, root, copy = self.stand()
        shutil.rmtree(str(copy))
        with fake_home(home):
            self.assertEqual(devkitctl.check_window_copy(root, True), ([], []),
                             "доктор говорит про копию окна там, где её не заведено")


class SecondSubscriptionLayoutTest(SandboxCase):
    """Раскладка машинного контура по профилям включённых харнесов (DK-179).

    Шаги стоят друг на друге: сперва машина с одним харнесом, потом на ту же
    машину включается второй. Так и проверяется инвариант, ради которого
    переезд затевался, а не два независимых стенда.
    """

    CHAIN = True

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("hlproj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.home = cls.box.root / "hlhome"
        cls.home.mkdir()
        cls.alt = cls.home / ".claude-glm"
        cls.machine = cls.home / ".devkit" / "harness.local"

    def doc(self, *args):
        return self.box.doctor(self.proj, *args, home=self.home)

    def test_01_one_harness_lays_out_as_before(self):
        self.doc("--fix")
        self.doc("--fix")
        self.__class__.before = snapshot(self.home)
        self.assertTrue((self.home / ".claude" / "settings.json").is_file(),
                        "раскладка одного харнеса не завела настройки")
        self.assertTrue((self.home / ".claude" / "skills" / "board-batch" / "SKILL.md").is_file(),
                        "раскладка одного харнеса не завела скиллы")

    def test_02_second_harness_gets_its_own_full_set(self):
        write(self.box.dk / "kit" / "harness" / "glm-code.toml", GLM_PROFILE)
        write(self.machine, MACHINE_CONF)
        _, out = self.doc("--fix")
        # Первый комплект не тронут ни на файл: переезд с констант на профиль
        # обязан быть незаметным там, где путь профиля совпал с константой.
        after = snapshot(self.home, skip=(".claude-glm/", ".devkit/harness.local"))
        self.assertEqual(after, self.before,
                         "раскладка первого харнеса изменилась после включения второго: %s" % out)
        # Второй комплект полный: правила, хуки, права, скиллы, определения.
        self.assertIn_("devkit", read(self.alt / "CLAUDE.md"),
                       "второму харнесу не написана глобальная точка правил")
        data = json.loads(read(self.alt / "settings.json"))
        post = [h["command"] for g in data["hooks"]["PostToolUse"] for h in g["hooks"]]
        self.assertTrue([c for c in post if "check-symbols.py" in c],
                        "второму харнесу не подключены хуки: %s" % post)
        self.assertIn("Bash(git:*)", data["permissions"]["allow"],
                      "второму харнесу не разложены права машинного контура")
        self.assertTrue((self.alt / "skills" / "board-batch" / "SKILL.md").is_file(),
                        "второму харнесу не разложены скиллы")
        self.assertTrue((self.alt / "agents" / "exec-medium.md").is_file(),
                        "второму харнесу не разложены определения агентов")
        # Файл правил проекта при этом один: у обоих харнесов режим import и
        # одна строка импорта, генератор пишет им один и тот же текст.
        self.assertEqual(sorted(p.name for p in Path(self.proj).glob("*.md")),
                         ["AGENTS.md", "CLAUDE.md"], "файл правил проекта раздвоился")
        self.assertNotIn_("разного текста", out, "штатная раскладка дала находку про один файл")

    def test_03_repeated_fix_changes_nothing(self):
        before = snapshot(self.home)
        _, out = self.doc("--fix")
        self.assertNotIn_("починено", out, "повторный --fix чинит уже разложенное")
        self.assertEqual(snapshot(self.home), before, "повторный --fix переписал раскладку")

    def test_04_profile_without_machine_home_refuses(self):
        # Машинного ключа нет, подставлять в {home} нечего, и раскладка в
        # каталог с фигурными скобками в имени хуже отказа.
        write(self.machine, 'enabled = ["claude-code", "glm-code"]\n')
        _, out = self.doc()
        self.assertIn_("вписать home в секцию [glm-code]", out,
                       "доктор промолчал про профиль с {home} без машинного ключа")
        self.assertEqual(len([ln for ln in out.splitlines() if "вписать home в секцию" in ln]), 1,
                         "один пробел назван дважды: %s" % out)
        self.assertFalse(list(self.home.glob("*{home}*")), "разложено в каталог с плейсхолдером")
        write(self.machine, MACHINE_CONF)

    def test_05_two_harnesses_asking_one_file_for_different_text(self):
        # Совпавший файл правил это штатная раскладка второй подписки, а вот
        # разный текст у одного файла генератор писал бы сам за собой на
        # каждом проходе.
        write(self.box.dk / "kit" / "harness" / "glm-code.toml",
              GLM_PROFILE.replace('import_line = "@{path}"', 'import_line = "@{path} # своё"'))
        _, out = self.doc()
        self.assertIn_("разного текста", out, "доктор не заметил спор двух харнесов за один файл")
        self.assertIn_("CLAUDE.md", out, "находка не назвала файл, за который спор")
        write(self.box.dk / "kit" / "harness" / "glm-code.toml", GLM_PROFILE)

    def test_06_stray_hook_tree_is_found_in_both_harnesses(self):
        # Дерево, из которого зовётся хук, сверяется у каждого включённого
        # харнеса. На машине DK-582 путь дерева ветки стоял в обоих файлах, и
        # находка про один из них закрывала бы половину беды.
        stray = str(self.box.root / "devkit-branch")
        dkreal = os.path.realpath(str(self.box.dk))
        files = (self.home / ".claude" / "settings.json", self.alt / "settings.json")
        for f in files:
            write(f, read(f).replace("%s/hooks/notify.py" % dkreal,
                                     "%s/hooks/notify.py" % stray))
        _, out = self.doc()
        for f in files:
            self.assertRegex(out, r"%s[^\n]*не из выкаченного дерева" % re.escape(str(f)),
                             "доктор не заметил хук из чужого дерева в %s" % f)
        self.doc("--fix")
        for f in files:
            self.assertNotIn(stray, read(f), "путь чужого дерева остался в %s" % f)

    @classmethod
    def tearDownClass(cls):
        (cls.box.dk / "kit" / "harness" / "glm-code.toml").unlink(missing_ok=True)
        super().tearDownClass()


class SinglePointOfBuildTest(unittest.TestCase):
    """Точка сборки одна, и советует доктор её же.

    Копии старого цикла `go build -o ~/go/bin/<утилита>` пережили перевод
    доктора на `devkitctl build` в двух подсказках (DK-150, замечание ревью):
    подсказку никто не звал, и разойтись со сборкой ей ничего не мешало.
    """

    SRC = Path(__file__).resolve().parent / "devkitctl.py"

    def test_no_own_go_build_left(self):
        stale = [ln for ln in read(self.SRC).split("\n")
                 if "go build" in ln and not ln.lstrip().startswith("#")]
        self.assertEqual(stale, [], "в devkitctl.py осталась своя сборка мимо build.py")

    def test_advice_calls_the_one_command(self):
        src = read(self.SRC)
        self.assertNotIn("~/go/bin/taskctl", src,
                         "подсказка зовёт собирать утилиту руками, мимо devkitctl build")
        self.assertIn("devkitctl.py build", src,
                      "подсказки перестали называть точку сборки")


def age_cmdout_dir(root, name, body, days_ago):
    """Каталог вывода .devkit/cmdout/<name>/out с содержимым и mtime на days_ago
    дней назад. Порог чистки это возраст, поэтому mtime фиксируется явно."""
    d = Path(root) / ".devkit" / "cmdout" / name
    d.mkdir(parents=True, exist_ok=True)
    out = d / "out"
    write(out, body)
    when = time.time() - days_ago * 86400
    os.utime(str(d), (when, when))
    os.utime(str(out), (when, when))
    return d


class CmdoutCleanTest(SandboxCase):
    """Чистка устаревших выводов .devkit/cmdout: находка doctor и её --fix
    (DK-267). Doctor не дублирует порог возраста из internal/frame, а судит о
    нём через сухой прогон «cmdout clean --dry-run», поэтому для проверки нужен
    живой бинарь cmdout из этой же копии devkit, а не заглушка Sandbox.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Подмена cmdout в sandbox-бине на настоящую сборку из копии devkit:
        # заглушка печатает версию и выходит нулём, а для чистки нужен бинарь,
        # знающий подкоманду. Версию зашиваем ту же, что у заглушки, чтобы
        # check_binaries на ней не загорелся.
        err = build.compile_one(cls.box.dk, "cmdout", cls.box.bin / "cmdout",
                                cls.box.version, cls.box.commit)
        if err:
            raise unittest.SkipTest(err)
        cls.proj = cls.box.project("cmdout-proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        # Старый каталог на 30 дней старше порога 7, свежий сегодняшним днём.
        cls.old_dir = age_cmdout_dir(cls.proj, "20260101T000000-old",
                                     "старый вывод\n", 30)
        cls.fresh_dir = age_cmdout_dir(cls.proj, "20260812T120000-fresh",
                                       "свежий вывод\n", 0)

    def test_1_doctor_finds_accumulation(self):
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 1, "doctor не увидел скопления cmdout: %s" % out)
        self.assertIn_("cmdout", out, "нет находки про скопление cmdout")
        # doctor без --fix не удаляет: оба каталога на месте.
        self.assertTrue(self.old_dir.exists(), "doctor удалил без --fix")
        self.assertTrue(self.fresh_dir.exists(), "doctor задел свежий без --fix")

    def test_2_fix_cleans_stale(self):
        rc, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено", out, "doctor --fix не почистил cmdout")
        self.assertFalse(self.old_dir.exists(), "старый каталог не удалён через --fix")
        self.assertTrue(self.fresh_dir.exists(), "doctor --fix задел свежий каталог")

    def test_3_repeat_doctor_is_clean(self):
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 0, "повторный doctor нашёл находку: %s" % out)
        self.assertTrue(self.fresh_dir.exists(), "свежий каталог пропал после повтора")


class GoWorkFindingTest(SandboxCase):
    """Чужой go.work рядом с го-проектом (DK-115).

    Го-проект подложенным go.work не покрыт, и го-команды из его каталога
    отвечают «directory prefix . does not contain modules listed in go.work»:
    shipctl merge видит красные тесты на ровном месте. Доктор печатает находку
    с причиной и командой починки, молчит, когда проект вписан в go.work, либо
    команды уже обёрнуты GOWORK=off, либо го в них не зовётся напрямую.
    """

    # Чужой go.work: один сиблинг в use, проект нет. Форма с блоком use ( ... )
    # покрывает оба способа писать пути, однострочный и блочный.
    FOREIGN = "go 1.21\n\nuse (\n\t./sibling\n)\n"

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Рабочее дерево под go.work, отдельное от остальных классов: sandbox
        # свой на класс. Сиблинг в use, проект нет.
        cls.ws = cls.box.root / "ws"
        cls.ws.mkdir()
        sibling = cls.ws / "sibling"
        sibling.mkdir()
        write(sibling / "go.mod", "module sibling\n\ngo 1.21\n")
        write(cls.ws / "go.work", cls.FOREIGN)

    def setUp(self):
        super().setUp()
        # Тест, переписывавший go.work под себя, не должен протечь в следующий:
        # иначе тишина одного теста приезжала бы находкой другого.
        write(self.ws / "go.work", self.FOREIGN)

    def make_proj(self, name):
        proj = git_init(self.ws / name)
        write(proj / "go.mod", "module %s\n\ngo 1.21\n" % name)
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        write(proj / "docs" / "TASKS.md", "# Задачи\n")
        return proj

    def deploy_local(self, proj):
        return proj / ".devkit" / "deploy.local"

    def sysdoctor(self, root, *args):
        return self.box.doctor(root, *args, path=str(self.box.sys))

    def test_1_foreign_gowork_finds_missing_gowork_off(self):
        # Базовый сценарий DK-115: го-проект под чужим go.work, в ключе test=
        # го зовётся напрямую, GOWORK=off нет.
        proj = self.make_proj("bare")
        write(self.deploy_local(proj),
              "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertIn_("go.work", out, "нет находки про чужой go.work рядом")
        self.assertIn_("directory prefix", out, "находка не назвала причину отказа го")
        self.assertIn_("GOWORK=off", out, "находка не назвала команду починки")
        self.assertIn_("test=", out, "находка не назвала ключ, которому грозит отказ")

    def test_2_silent_when_project_is_listed_in_gowork(self):
        # Тот же проект, но вписан в go.work: отказа го нет, находка молчит.
        proj = self.make_proj("listed")
        write(self.ws / "go.work",
              "go 1.21\n\nuse (\n\t./sibling\n\t./listed\n)\n")
        write(self.deploy_local(proj),
              "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertNotIn_("go.work", out, "находка горит на проекте, вписанном в go.work")

    def test_3_silent_when_gowork_off_in_command(self):
        # Команда уже обёрнута GOWORK=off: находка не нужна, го не откажет.
        proj = self.make_proj("wrapped")
        write(self.deploy_local(proj),
              "deploy = make deploy\n"
              "test = export GOWORK=off; go test ./...\n"
              "autonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertNotIn_("go.work", out, "находка горит при уже обёрнутой команде")

    def test_4_silent_when_command_does_not_invoke_go_directly(self):
        # Команды, которые го не зовут напрямую (python, make), GOWORK=off не
        # нужны: внутренние обёртки ставят его сами, и находка сбивает с толку.
        proj = self.make_proj("nogocmd")
        write(self.deploy_local(proj),
              "deploy = python3 build.py\ntest = make test\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertNotIn_("go.work", out,
                          "находка советует GOWORK=off команде, которая го не зовёт")

    def test_5_silent_without_go_mod(self):
        # Без go.mod это не го-проект: находка про go.work нерелевантна.
        proj = git_init(self.ws / "nogo")
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        write(proj / "docs" / "TASKS.md", "# Задачи\n")
        write(self.deploy_local(proj),
              "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertNotIn_("go.work", out, "находка горит на проекте без go.mod")

    def test_6_silent_without_gowork_in_ancestors(self):
        # Нет go.work ни в одном предке: находке не о чем говорить.
        proj = git_init(self.box.root / "lonely")
        write(proj / "go.mod", "module lonely\n\ngo 1.21\n")
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        write(proj / "docs" / "TASKS.md", "# Задачи\n")
        write(self.deploy_local(proj),
              "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertNotIn_("go.work", out, "находка горит без go.work по дереву вверх")

    def test_7_finding_names_both_keys_when_both_invoke_go(self):
        # И deploy=, и test= зовут го без GOWORK=off: находка зовёт оба ключа.
        proj = self.make_proj("twobare")
        write(self.deploy_local(proj),
              "deploy = go build -o /tmp/x .\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertIn_("deploy=", out, "находка не назвала ключ deploy=")
        self.assertIn_("test=", out, "находка не назвала ключ test=")

    def test_8_comment_in_use_block_does_not_mask_missing_project(self):
        # Регрессия: парсер go.work не пропускал комментарии // внутри
        # use ( ... ), и строка-комментарий резолвилась в корень /, откуда
        # project_in_gowork отвечал True для любого проекта и находка молчала
        # на валидном чужом go.work с комментарием в use-блоке.
        proj = self.make_proj("cmt")
        write(self.ws / "go.work",
              "go 1.21\n\nuse (\n\t// sibling only, proj is foreign\n\t./sibling\n)\n")
        write(self.deploy_local(proj),
              "deploy = make deploy\ntest = go test ./...\nautonomous = false\n")
        _, out = self.sysdoctor(proj)
        self.assertIn_("go.work", out,
                       "комментарий в use-блоке закрыл находку, хотя проект не перечислен")


class ProseConfigTest(unittest.TestCase):
    """Пропавший конфиг порогов прозы виден доктором. Без этой находки сторож
    молчит на каждой записи, а молчание не отличить от чистого текста."""

    def setUp(self):
        self.was = os.environ.get("DEVKIT_PROSE_CONFIG")

    def tearDown(self):
        if self.was is None:
            os.environ.pop("DEVKIT_PROSE_CONFIG", None)
        else:
            os.environ["DEVKIT_PROSE_CONFIG"] = self.was

    def test_missing_config_is_a_finding(self):
        os.environ["DEVKIT_PROSE_CONFIG"] = os.path.join(tempfile.mkdtemp(),
                                                         "prose.toml")
        found = devkitctl.check_prose_config()
        self.assertEqual(len(found), 1, found)
        self.assertIn("сторож прозы молчит", found[0])

    def test_config_without_a_metric_is_a_finding(self):
        path = os.path.join(tempfile.mkdtemp(), "prose.toml")
        write(path, '[prose]\nmode = "warn"\nmin_words = 120\n'
                    'suffixes = [".md"]\n[warn]\n[block]\n')
        os.environ["DEVKIT_PROSE_CONFIG"] = path
        found = devkitctl.check_prose_config()
        self.assertEqual(len(found), 1, found)
        self.assertIn("colon_mid", found[0])

    def test_shipped_config_keeps_the_doctor_quiet(self):
        os.environ.pop("DEVKIT_PROSE_CONFIG", None)
        self.assertEqual(devkitctl.check_prose_config(), [])


class MachineBuildTest(unittest.TestCase):
    """Сборка на машину: этой дорогой едет выкат, и после укладки победитель
    PATH обязан отвечать свежей сборкой (DK-599). Go тут заглушка, как в
    build_test: проверяется сверка вокруг сборки, а не компилятор.
    """

    NAME = "alfactl"

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-machine-build-test-"))
        self.dk = git_init(self.root / "devkit")
        write(self.dk / "tools" / self.NAME / "go.mod",
              "module github.com/dronrider/devkit/tools/%s\n\ngo 1.26\n" % self.NAME)
        write(self.dk / "tools" / self.NAME / "main.go", "package main\n\nfunc main() {}\n")
        git(self.dk, "add", "-A")
        git(self.dk, "commit", "-qm", "init")
        self.dest = self.root / "gobin"
        self.shadow = self.root / "shadow"
        stub = self.root / "stub"
        executable(stub / "go", GO_STUB)
        self.saved = {k: os.environ.get(k) for k in ("PATH", "SANDBOX")}
        os.environ["SANDBOX"] = str(self.root)
        os.environ["PATH"] = os.pathsep.join(
            (str(stub), str(self.shadow), str(self.dest), os.environ["PATH"]))

    def tearDown(self):
        for k, v in self.saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        shutil.rmtree(str(self.root), ignore_errors=True)

    def test_clean_machine_is_silent(self):
        self.assertEqual(devkitctl.machine_build(self.dk, self.dest,
                                                 log=lambda *a: None), [])

    def test_shadowing_copy_fails_the_build(self):
        # Живой случай DK-599 в миниатюре: свежая сборка легла в каталог
        # назначения, а в PATH выигрывает копия из чужого каталога, и без
        # сверки выкат отчитался бы зелёным при старой команде на машине.
        executable(self.shadow / self.NAME,
                   BINARY_STUB % build.version_line(self.NAME, "v0.1.0", "чужое"))
        found = devkitctl.machine_build(self.dk, self.dest, log=lambda *a: None)
        self.assertEqual(len(found), 1, found)
        self.assertIn(self.NAME, found[0])
        self.assertIn("чужое", found[0])
        self.assertIn(str(self.dest), found[0], "находка не говорит, где лежит свежая сборка")
        self.assertEqual(update.binary_stamp(self.dest / self.NAME),
                         (build.stamp(self.dk)[0], build.stamp(self.dk)[1]),
                         "сверка PATH обязана идти после укладки, а не вместо неё")

    def test_broken_build_does_not_reach_the_path_check(self):
        # Проваленная сборка называет свою беду, и сверять победителя PATH
        # после неё нечем: находки сборки не должны тонуть в находках сверки.
        os.environ["GO_STUB_NO_FLAG"] = self.NAME
        try:
            found = devkitctl.machine_build(self.dk, self.dest, log=lambda *a: None)
        finally:
            os.environ.pop("GO_STUB_NO_FLAG", None)
        self.assertEqual(len(found), 1, found)
        self.assertIn("ждали от --version", found[0])


if __name__ == "__main__":
    unittest.main()
