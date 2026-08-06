#!/usr/bin/env python3
"""Подключение проекта и диагностика обвязки: new, doctor по проекту и по
машинному контуру, обвязка выката, рубеж основного чекаута и сводка запусков.
"""
import json
import os
import platform
import re
import shutil
import time
import unittest
from pathlib import Path

from testenv import (BINARY_STUB, GO_STUB, SandboxCase, build, executable,
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
        self.assertIn("@../devkit/RULES.md", thin.split("\n"),
                      "тонкий CLAUDE.md не ссылается на правила devkit")
        self.assertNotIn("RULES.board.md", thin, "проекту без доски выписаны правила доски")
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
        self.assertIn_("notify.py не подключён на события Notification, Stop, SubagentStop, "
                       "UserPromptSubmit", out, "нет находки про неподключённый уведомитель")
        write(self.settings, full)

    def test_6_no_notification_backend(self):
        # Слать нечем: бэкенда на платформе нет (PATH подставной, переменная
        # снята), и доктор называет это находкой с командой самопроверки.
        _, out = self.box.doctor(self.proj, env={"DEVKIT_NOTIFY_BACKEND": None})
        self.assertIn_("уведомлять нечем", out, "нет находки про отсутствие бэкенда уведомлений")
        self.assertIn_("notify.py --self-test", out, "в находке про бэкенд нет команды проверки")

    @unittest.skipUnless(platform.system() == "Darwin", "клик по баннеру поддержан только на macOS")
    def test_7_click_goes_to_finder(self):
        # Слать есть чем, но клик по баннеру уводит в Finder: доктор предлагает
        # отправителя с переходом. Случай ровно macOS-ный, на другой платформе
        # клик не поддержан ни одним бэкендом, и предлагать там нечего.
        osascript = executable(self.box.root / "osascript")
        _, out = self.box.doctor(self.proj, env={"DEVKIT_NOTIFY_BACKEND": osascript})
        self.assertIn_("клик по баннеру ведёт не в окно сессии", out,
                       "нет находки про клик мимо окна сессии")
        self.assertIn_("brew install terminal-notifier", out,
                       "в находке про клик нет команды установки")
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
        self.assertIn_("taskctl не в PATH", out, "нет находки про taskctl")

    def test_02_new_with_board_lays_out_the_deploy_stub(self):
        self.assertTrue(self.deploy.is_file(), "new не завёл .devkit/deploy.local")
        # Хвост подключения (DK-125): чего команда не сделала за человека, она
        # называет сама, с путём файла и ключами.
        self.assertIn_("осталось сделать", self.bout, "new не напечатал, что осталось сделать")
        root = os.path.realpath(str(self.bproj))
        self.assertIn_("%s/.devkit/deploy.local: вписать test =, deploy =" % root, self.bout,
                       "хвост new не назвал незаполненную обвязку выката")
        self.assertIn_("проверка: devkitctl doctor -C %s" % root, self.bout,
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


class MachineContourTest(SandboxCase):
    """Машинный контур: бинари, определения агентов, скиллы, снимок квоты и
    каталог сборки. Гоняется на отдельном проекте и отдельном HOME, чтобы правки
    --fix не мешали прежним шагам; сборку изображает заглушка go, гонять
    настоящую в самопроверке незачем.
    """

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
        self.assertIn_("agentctl не в PATH", out, "нет находки про бинарь agentctl")
        # Список проверяемых выводится из дерева, поэтому утилиты, до которых
        # прежний перечень в коде не дотягивался, под проверкой наравне.
        for t in ("trackctl", "obeycheck"):
            self.assertIn_("%s не в PATH" % t, out, "%s не попал под проверку" % t)
        self.assertIn_("чекаут devkit: на ветке", out, "доктор не назвал режим чекаута")
        self.assertIn_("exec-medium.md", out, "нет находки про определения исполнителей")
        self.assertRegex(out, r"нет скилла .*board-batch/SKILL\.md", "нет находки про скилл")
        self.assertIn_("tmux не в PATH", out, "нет находки про tmux")
        self.assertIn_("нет снимка квоты в", out, "нет находки про снимок квоты")
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
        self.assertNotIn_("README.md положено", out,
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
        self.assertIn_("tmux не в PATH", out, "--fix не ставит tmux, находка должна остаться")
        self.assertIn_("agentctl quota refresh", out,
                       "--fix не снимает квоту, находка должна остаться")
        # Повторный --fix по машинному контуру уже ничего не чинит.
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix не должен ничего менять")

    def test_03_diverged_agent_definition(self):
        # Правка руками или отставшая копия: plain doctor называет находку с
        # командой cp, файл не трогает.
        agent = self.mhome / ".claude" / "agents" / "exec-low.md"
        write(agent, read(agent) + "\nсвоя строка\n")
        _, out = self.docm()
        self.assertIn_("exec-low.md разошлось", out, "нет находки про разошедшееся определение")
        self.assertIn("своя строка", read(agent), "doctor без --fix тронул определение")
        # devkit источник правды для промптов: --fix перекладывает разошедшееся
        # определение и называет в отчёте, что переложил, а не затирает молча.
        _, out = self.docm("--fix")
        self.assertRegex(out, r"починено:.*exec-low\.md разошлось",
                         "--fix не отчитался о перекладке разошедшегося определения")
        self.assertNotIn("своя строка", read(agent), "--fix не переложил разошедшееся определение")
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix после перекладки не должен менять")

    def test_04_diverged_skill(self):
        skill = self.mhome / ".claude" / "skills" / "board-batch" / "SKILL.md"
        write(skill, read(skill) + "\nсвоя строка\n")
        _, out = self.docm()
        self.assertRegex(out, r"скилл .*board-batch/SKILL\.md разошёлся",
                         "нет находки про разошедшийся скилл")
        self.assertIn("своя строка", read(skill), "doctor без --fix тронул скилл")
        _, out = self.docm("--fix")
        self.assertRegex(out, r"починено:.*board-batch/SKILL\.md разошёлся",
                         "--fix не отчитался о перекладке скилла")
        self.assertNotIn("своя строка", read(skill), "--fix не переложил разошедшийся скилл")
        _, out = self.docm("--fix")
        self.assertNotIn_("починено", out, "повторный --fix после перекладки скилла не должен менять")

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
        self.assertIn_("починено: agentctl в", out, "--fix не пересобрал разошедшийся бинарь")
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
        self.assertIn_("починено: agentctl в", out, "--fix не пересобрал бинарь без --version")
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
            self.assertIn_("починено: agentctl в", out, "--fix не пересобрал несобранные правки")
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
            self.assertIn_("починено: agentctl в", out, "--fix не пересобрал отставший бинарь")
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
        self.assertIn_("в PATH выигрывает", out, "нет находки про затенённый бинарь")
        self.assertNotIn_("починено: agentctl в", out,
                          "затенённый бинарь пересобирается впустую")
        _, out = self.docm("--fix", path=spath)
        self.assertNotIn_("починено", out, "повторный --fix при затенённом бинаре не должен менять")
        self.assertIn_("в PATH выигрывает", out, "находка про затенённый бинарь должна остаться")

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
        self.assertIn_("починено: agentctl в", out,
                       "разошедшийся бинарь за симлинком не пересобран")
        self.assertNotIn_("в PATH выигрывает", out,
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
        self.assertIn_("не в PATH: добавить директорию", out,
                       "нет находки про каталог сборки вне PATH")
        self.assertIn_("export PATH=", out,
                       "находка про каталог мимо PATH не даёт готовой строки для профиля")


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
        self.assertIn_("taskctl не в PATH; поставить бинари релиза v9.9.0: devkitctl update", out,
                       "на теге доктор советует не релиз")
        self.assertNotIn_("go в PATH нет", out,
                          "на машине потребителя доктор зовёт ставить тулчейн")

    def test_2_fix_downloads_the_release(self):
        _, out = self.docr("--fix")
        for t in build.tools(self.box.dk):
            self.assertEqual(update.binary_stamp(self.dest / t), (self.tag, self.box.commit),
                             "--fix не поставил %s из релиза: %s" % (t, out))
        self.assertIn_("починено: taskctl в", out, "--fix не отчитался о поставленном бинаре")
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
        self.assertIn_("cp %s/kit/agents/review-high.md" % self.dkreal, self.out,
                       "находка про определения зовёт не в основной чекаут")

    def test_3_skills_keep_the_same_boundary(self):
        self.assertFalse((self.wthome / ".claude" / "skills" / "board-batch" / "SKILL.md").exists(),
                         "--fix разложил скилл с фичеветки")
        self.assertIn_("cp %s/kit/skills/board-batch/SKILL.md" % self.dkreal, self.out,
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
        self.assertIn_("cp %s/kit/agents/exec-high.md" % self.dkreal, out,
                       "сверка определения идёт не с основным чекаутом")
        # Защита from_main действует и для перезаписи: --fix с worktree ветки
        # задачи разошедшееся определение не перекладывает, находка остаётся.
        _, out = self.wtdoc("--fix")
        self.assertIn_("cp %s/kit/agents/exec-high.md" % self.dkreal, out,
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
                           "--type", "task", "--rank", "25+5+1+0+0", "--cost", "S"],
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

    def test_hooks_are_laid_out_once(self):
        _, out = self.box.doctor(self.proj, home=self.home2)
        self.assertIn_("PostToolUse-хук check-symbols.py не подключён", out,
                       "доктор не заметил неподключённые хуки харнеса")
        _, out = self.box.doctor(self.proj, "--fix", home=self.home2)
        self.assertIn_("хук харнеса на PostToolUse", out, "--fix не разложил хуки харнеса")
        data = json.loads(read(self.settings))
        self.assertEqual(data.get("model"), "opus", "рукописное в настройках потерялось")
        hooks = data["hooks"]
        post = [h["command"] for g in hooks["PostToolUse"] for h in g["hooks"]]
        self.assertEqual(len([c for c in post if "check-symbols.py" in c]), 1, post)
        self.assertEqual([g.get("matcher") for g in hooks["PostToolUse"]],
                         ["Edit|Write|NotebookEdit"], hooks["PostToolUse"])
        for event in ("Notification", "Stop", "SubagentStop", "UserPromptSubmit"):
            cmds = [h["command"] for g in hooks[event] for h in g["hooks"]]
            self.assertEqual(len([c for c in cmds if "notify.py" in c]), 1, (event, cmds))
        self.assertIn("quota-refresh.sh", str(hooks["SessionStart"]))
        # Повторный --fix хуки второй раз не раскладывает.
        _, out = self.box.doctor(self.proj, "--fix", home=self.home2)
        self.assertNotIn_("хук харнеса на", out, "повторный --fix разложил хуки второй раз")
        post = [h["command"] for g in json.loads(read(self.settings))["hooks"]["PostToolUse"]
                for h in g["hooks"]]
        self.assertEqual(len(post), 3, post)


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


if __name__ == "__main__":
    unittest.main()
