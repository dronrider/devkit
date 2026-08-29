#!/usr/bin/env python3
"""Корп-контур: подключение клона к боковой директории с доской, цепочка хуков,
рубеж следов, диалог первого прогона и корп-проверки доктора (DK-085, DK-124,
DK-125, DK-240).

Доску заводит taskctl, а в подставном PATH он заглушка на «exit 0», поэтому на
эти проверки кладётся своя, которая скелет доски всё-таки пишет: без доски
проверять было бы нечего.
"""
import os
import shutil
import tempfile
import unittest

import corp

from testenv import SandboxCase, executable, git, git_init, read, rules, run, write

FOREIGN_HOOK = "#!/bin/sh\necho чужой pre-commit\nexit 0\n"


class CorpTest(SandboxCase):
    """Проверки идут цепочкой по одной раскладке, как их гонял sh-раннер: её
    заводит настоящий прогон corp, а каждая следующая мутация стоит на том, что
    оставила предыдущая, поэтому в именах порядковый номер.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        corpbin = box.root / "corpbin"
        corpbin.mkdir()
        executable(corpbin / "taskctl", box.board_taskctl())
        cls.corppath = "%s:%s" % (corpbin, box.cleanpath)
        cls.clone = box.root / "corp-proj"
        cls.local = box.root / "corp-proj-local"
        git_init(cls.clone)
        # Чужой хук проекта: он должен пережить подключение и остаться в цепочке.
        executable(cls.clone / ".git" / "hooks" / "pre-commit", FOREIGN_HOOK)
        write(cls.clone / "readme.md", "readme\n")
        git(cls.clone, "add", "-A")
        git(cls.clone, "commit", "-qm", "init")
        cls.rc, cls.out = box.dkctl_run("corp", "--prefix", "CP", "-C", str(cls.clone),
                                        path=cls.corppath)
        # Пути в находках доктор печатает разрешёнными, а mktemp на macOS отдаёт
        # /var, который на деле симлинк на /private/var.
        cls.cloner = os.path.realpath(str(cls.clone))
        cls.localr = os.path.realpath(str(cls.local))

    @classmethod
    def corpdoc(cls, *args, **kw):
        root = kw.pop("root", cls.clone)
        return cls.box.dkctl_run("doctor", *(list(args) + ["-C", str(root)]),
                                 path=cls.corppath, **kw)

    def test_01_corp_lays_out_the_pair(self):
        self.assertEqual(self.rc, 0, "corp не прошёл: %s" % self.out)
        self.assertTrue((self.local / "docs" / "TASKS.md").is_file(),
                        "corp не завёл доску в боковой директории: %s" % self.out)
        self.assertTrue((self.local / "docs" / "tasks").is_dir(),
                        "corp не завёл docs/tasks в боковой директории")
        self.assertTrue((self.local / ".devkit").is_dir(),
                        "corp не завёл .devkit в боковой директории")
        self.assertTrue((self.local / ".git").is_dir(),
                        "боковая директория без своего git: доску некуда пушить")
        self.assertTrue((self.local / "AGENTS.md").is_file(),
                        "corp не положил AGENTS.md в боковую директорию")
        self.assertEqual(git(self.clone, "config", "--local", "devkit.local")[1].strip(),
                         "../corp-proj-local", "corp не выставил редирект")
        self.assertRegex(read(self.local / ".devkit" / "tracker.local"), r"(?m)^repo = \.\./corp-proj$",
                         "corp не вписал repo в привязку")
        self.assertIn("CLAUDE.md", read(self.clone / ".git" / "info" / "exclude").split("\n"),
                      "corp не спрятал тонкий файл строкой exclude")
        self.assertTrue((self.clone / "CLAUDE.md").is_file(),
                        "corp не положил тонкий файл контекста в корень клона")
        # Оба импорта клона ведут наружу, и оба записаны путями от его корня
        # через свою ссылку: путь наружу клиент не разворачивает молча (DK-193).
        thin = read(self.clone / "CLAUDE.md").split("\n")
        self.assertIn("@.devkit/AGENTS.md", thin,
                      "тонкий файл клона не импортирует AGENTS.md боковой директории")
        self.assertIn("@.devkit/.devkit/devkit/RULES.board.md", thin,
                      "тонкий файл клона не импортирует правила доски через ссылку")
        self.assertFalse([ln for ln in thin if ln.startswith("@..")],
                         "тонкий файл клона зовёт импорт путём наружу: %s" % thin)
        link = self.clone / rules.LINK_DIR
        self.assertTrue(link.is_symlink(),
                        "каталог обвязки клона это не ссылка на боковую директорию")
        self.assertEqual(os.path.realpath(str(link)), self.localr,
                         "ссылка .devkit ведёт не в боковую директорию")
        self.assertTrue((link / "docs" / "TASKS.md").is_file(),
                        "доска не открывается из клона путём .devkit/docs/TASKS.md")
        self.assertEqual(os.path.realpath(str(link / rules.LINK_DIR / rules.DEVKIT_LINK)),
                         os.path.realpath(str(self.box.dk)),
                         "ссылка на дерево devkit ведёт не туда")
        self.assertNotIn(rules.LINK_DIR, read(self.clone / ".git" / "info" / "exclude").split("\n"),
                         "corp спрятал ссылку строкой exclude: поиск редактора читает те же "
                         "источники игнора, и доска перестанет находиться из окна клона")
        self.assertFalse((self.clone / "AGENTS.md").exists(),
                         "corp положил AGENTS.md в дерево корп-клона")
        # Ссылка висит в статусе клона одной строкой, и это цена того, что доска
        # видна поиску: от индекса её стережёт обёртка pre-commit (DK-583).
        self.assertEqual(git(self.clone, "status", "--short")[1].strip(), "?? .devkit",
                         "в git status корп-клона не одна ссылка обвязки")

    def test_02_chain_on_both_hooks(self):
        # Рубеж по сообщению держит только commit-msg, и клон с одной обёрткой
        # пропускал бы локальный ID молча (найдено живым прогоном DK-086).
        for h in ("pre-commit", "commit-msg"):
            self.assertIn("devkit-corp-chain", read(self.clone / ".git" / "hooks" / h),
                          "цепочка не развёрнута на %s" % h)
        self.assertIn("чужой pre-commit", read(self.clone / ".git" / "hooks" / "pre-commit.chained"),
                      "чужой хук проекта не переехал в pre-commit.chained")

    def test_03_clean_corp_project(self):
        # Обвязка выката заполнена, remote у боковой директории есть, находок нет.
        write(self.local / ".devkit" / "deploy.local",
              "deploy = echo выкат\ntest = echo тесты\nautonomous = false\n")
        run(["git", "init", "-q", "--bare", str(self.box.root / "corp-board.git")])
        git(self.local, "remote", "add", "origin", str(self.box.root / "corp-board.git"))
        rc, out = self.corpdoc()
        self.assertEqual(rc, 0, "doctor нашёл находки на чистом корп-проекте: %s" % out)
        # И тот же доктор, прогнанный в боковой директории, тоже чист.
        rc, out = self.corpdoc(root=self.local)
        self.assertEqual(rc, 0, "doctor из боковой директории нашёл находки: %s" % out)

    def test_04_home_project_has_no_corp_checks(self):
        proj = self.box.project("proj")
        self.box.dkctl_run("new", "--no-board", "-C", str(proj))
        _, out = self.corpdoc(root=proj)
        self.assertNotIn_("боковой директории", out, "корп-проверки включились в домашнем проекте")

    def test_05_exclude_line_lost(self):
        # Пропала exclude-строка, тонкий файл торчит в чужом git status.
        excl = self.clone / ".git" / "info" / "exclude"
        write(excl, "\n".join(ln for ln in read(excl).split("\n") if ln != "CLAUDE.md"))
        _, out = self.corpdoc()
        self.assertIn_("exclude не прячет CLAUDE.md", out, "нет находки на пропавшую exclude-строку")
        _, out = self.corpdoc("--fix")
        self.assertIn_("починено: .git/info/exclude: спрятан CLAUDE.md", out,
                       "--fix не вернул exclude-строку")
        self.assertIn("CLAUDE.md", read(excl).split("\n"), "--fix сказал про exclude, а строки нет")

    def test_06_chain_lost_on_commit_msg(self):
        (self.clone / ".git" / "hooks" / "commit-msg").unlink()
        _, out = self.corpdoc()
        self.assertIn_("цепочка хуков потеряна на commit-msg", out,
                       "нет находки на потерянную цепочку commit-msg")
        rc, out = self.corpdoc("--fix")
        self.assertEqual(rc, 0, "--fix не вернул цепочку на commit-msg: %s" % out)
        self.assertIn("devkit-corp-chain", read(self.clone / ".git" / "hooks" / "commit-msg"),
                      "--fix сказал про цепочку, а обёртки нет")

    def test_07_wrapper_overwritten(self):
        # Обёртку перезаписал чужой инсталлер, маркер пропал.
        executable(self.clone / ".git" / "hooks" / "pre-commit", "#!/bin/sh\nexit 0\n")
        _, out = self.corpdoc()
        self.assertIn_("обёртка pre-commit перезаписана", out,
                       "нет находки на перезаписанную обёртку")
        rc, out = self.corpdoc("--fix")
        self.assertEqual(rc, 0, "--fix не вернул перезаписанную обёртку: %s" % out)
        self.assertIn("devkit-corp-chain", read(self.clone / ".git" / "hooks" / "pre-commit"),
                      "--fix не восстановил обёртку pre-commit")
        # Чужой хук проекта раскладка тут затёрла им же перезаписанной обёрткой:
        # своим файлом она считает только помеченный маркером, а всё остальное
        # чужим хуком. Фикстура возвращается к настоящему чужому хуку, дальше он
        # ещё проверяется.
        executable(self.clone / ".git" / "hooks" / "pre-commit.chained", FOREIGN_HOOK)

    def test_08_devkit_files_in_the_corp_index(self):
        git(self.clone, "add", "-f", "CLAUDE.md")
        _, out = self.corpdoc()
        self.assertIn_("в корп-индексе лежит обвязка devkit (CLAUDE.md)", out,
                       "нет находки на обвязку в корп-индексе")
        git(self.clone, "rm", "-q", "--cached", "CLAUDE.md")

    def test_09_local_dir_without_remote(self):
        git(self.local, "remote", "remove", "origin")
        _, out = self.corpdoc()
        self.assertIn_("у боковой директории %s нет remote" % self.localr, out,
                       "нет находки на боковую директорию без remote")
        git(self.local, "remote", "add", "origin", str(self.box.root / "corp-board.git"))

    def test_10_tracker_without_repo_key(self):
        # Из привязки пропал ключ repo, и обвязку клона стало нечем найти.
        tracker = self.local / ".devkit" / "tracker.local"
        write(tracker, "".join(ln + "\n" for ln in read(tracker).split("\n")
                               if ln and not ln.startswith("repo = ")))
        _, out = self.corpdoc()
        self.assertIn_("нет ключа repo", out, "нет находки на привязку без ключа repo")
        rc, out = self.corpdoc("--fix")
        self.assertEqual(rc, 0, "--fix не вписал repo в привязку: %s" % out)
        self.assertRegex(read(tracker), r"(?m)^repo = \.\./corp-proj$",
                         "--fix сказал про repo, а ключа нет")

    def test_11_sync_freshness(self):
        # Свежесть sync спрашивается только у привязанного к трекеру проекта: до
        # ключа contour гонять sync нечем, и молчание тут штатно.
        _, out = self.corpdoc()
        self.assertNotIn_("sync", out, "доктор спросил про sync у проекта без привязки к трекеру")
        # Ключ проекта в трекере разведён с префиксом доски (CP): так его и
        # держат, иначе рубеж следов не отличает локальный ID доски от ключа
        # тикета (DK-124).
        tracker = self.local / ".devkit" / "tracker.local"
        write(tracker, read(tracker) + "contour = corp\nkey = TR\n")
        _, out = self.corpdoc()
        self.assertIn_("sync с трекером не гонялся ни разу", out,
                       "нет находки на ни разу не гонявшийся sync")
        stamp = self.local / ".devkit" / "tracker.sync"
        write(stamp, "")
        os.utime(str(stamp), (946684800, 946684800))
        _, out = self.corpdoc()
        self.assertRegex(out, r"последний sync с трекером .* дн назад",
                         "нет находки на протухшую отметку sync")
        os.utime(str(stamp), None)
        rc, out = self.corpdoc()
        self.assertEqual(rc, 0, "доктор нашёл находки при свежей отметке sync: %s" % out)

    def test_12_trace_boundary(self):
        # Рубеж следов на настоящей раскладке подключения (DK-124): проверяется
        # он не на самодельной фикстуре, а на том, что выше положил сам
        # «devkitctl corp», то есть цепочкой хуков в корп-клоне. Разведённые
        # префикс доски (CP) и ключ проекта (TR): коммит по конвенции компании
        # проходит, локальный ID доски в сообщении валит коммит.
        write(self.clone / "note.md", "заметка проекта\n")
        git(self.clone, "add", "note.md")
        rc, out = git(self.clone, "commit", "-qm", "chore: TR-7 правка по тикету")
        self.assertEqual(rc, 0, "коммит с ключом тикета не прошёл цепочку хуков: %s" % out)
        write(self.clone / "note2.md", "вторая заметка\n")
        git(self.clone, "add", "note2.md")
        rc, out = git(self.clone, "commit", "-m", "chore: TR-7 правка по строке CP-7")
        self.assertNotEqual(rc, 0, "локальный ID доски в сообщении прошёл рубеж следов: %s" % out)
        self.assertIn_("локальный ID доски", out, "находка рубежа без разбора вида")

    def test_13_prefix_equal_to_the_key(self):
        # Тот же клон с префиксом доски, совпавшим с ключом проекта: локальный ID
        # и ключ тикета там одна и та же строка, правило про ID снимается целиком
        # (иначе рубеж валил бы каждый коммит по конвенции компании), а доктор
        # про ослабленный рубеж говорит вслух: снаружи снятое правило выглядит
        # как работающее.
        tracker = self.local / ".devkit" / "tracker.local"
        keep = read(tracker)
        write(tracker, keep.replace("key = TR", "key = CP"))
        rc, out = git(self.clone, "commit", "-m", "chore: CP-7 правка по тикету")
        self.assertEqual(rc, 0, "коммит по конвенции компании завернул рубеж следов: %s" % out)
        write(self.clone / "note3.md", "третья заметка\n")
        git(self.clone, "add", "note3.md")
        rc, out = git(self.clone, "commit", "-m",
                      "chore: CP-8 смотри %s/docs/TASKS.md" % self.local)
        self.assertNotEqual(rc, 0, "путь боковой директории прошёл ослабленный рубеж: %s" % out)
        self.assertIn_("путь боковой директории", out,
                       "ослабленный рубеж потерял путь боковой директории")
        git(self.clone, "reset", "-q")
        (self.clone / "note3.md").unlink()
        _, out = self.corpdoc()
        self.assertIn_("префикс доски CP совпадает с ключом проекта", out,
                       "доктор промолчал про ослабленный совпадением префиксов рубеж")
        write(tracker, keep)
        _, out = self.corpdoc()
        self.assertNotIn_("префикс доски", out, "доктор ругается на разведённые префикс и ключ")

    def test_14_idempotent_rerun(self):
        # Повторный прогон боковую директорию не трогает (доска с правкой цела),
        # чужой хук не теряет и тонкий файл не переписывает.
        board = self.local / "docs" / "TASKS.md"
        write(board, read(board) + "CP-001 своя строка доски\n")
        self.__class__.board_keep = read(board)
        self.__class__.thin_keep = read(self.clone / "CLAUDE.md")
        rc, out = self.box.dkctl_run("corp", "-C", str(self.clone), path=self.corppath)
        self.assertEqual(rc, 0, "повторный corp не прошёл: %s" % out)
        self.assertEqual(read(board), self.board_keep, "повторный corp переписал доску")
        self.assertEqual(read(self.clone / "CLAUDE.md"), self.thin_keep,
                         "повторный corp переписал тонкий файл")
        self.assertIn("чужой pre-commit", read(self.clone / ".git" / "hooks" / "pre-commit.chained"),
                      "повторный corp потерял чужой хук проекта")
        self.assertIn_("хук pre-commit: цепочка на месте", out,
                       "повторный corp не сказал, что цепочка уже на месте")
        rc, out = self.corpdoc()
        self.assertEqual(rc, 0, "doctor после повторного corp нашёл находки: %s" % out)

    def test_15_reclone_loses_and_corp_restores(self):
        # Обвязка живёт в .git и в негитигнорнутом дереве, поэтому свежий клон её
        # теряет молча. Находит потерю доктор из боковой директории, по ключу
        # repo привязки, а восстанавливает повторное подключение.
        origin = self.box.root / "corp-origin.git"
        run(["git", "init", "-q", "--bare", str(origin)])
        git(self.clone, "push", "-q", str(origin), "HEAD:master")
        shutil.rmtree(str(self.clone))
        run(["git", "clone", "-q", str(origin), str(self.clone)])
        git(self.clone, "config", "user.name", "t")
        git(self.clone, "config", "user.email", "t@t")
        self.assertEqual(git(self.clone, "config", "--local", "devkit.local")[1].strip(), "",
                         "редирект пережил переклонирование, фикстура не та")
        _, out = self.corpdoc(root=self.local)
        self.assertIn_("обвязка корп-клона %s потеряна" % self.cloner, out,
                       "доктор из боковой директории не нашёл потерянную обвязку клона")
        self.assertIn_("devkitctl corp -C %s" % self.cloner, out,
                       "находка про потерянную обвязку не зовёт команду восстановления")
        rc, out = self.box.dkctl_run("corp", "-C", str(self.clone), path=self.corppath)
        self.assertEqual(rc, 0, "corp не восстановил обвязку после переклонирования: %s" % out)
        self.assertEqual(git(self.clone, "config", "--local", "devkit.local")[1].strip(),
                         "../corp-proj-local", "восстановление не вернуло редирект")
        self.assertIn("devkit-corp-chain", read(self.clone / ".git" / "hooks" / "commit-msg"),
                      "восстановление не вернуло цепочку commit-msg")
        self.assertIn("CLAUDE.md", read(self.clone / ".git" / "info" / "exclude").split("\n"),
                      "восстановление не вернуло exclude-строку")
        self.assertEqual(read(self.local / "docs" / "TASKS.md"), self.board_keep,
                         "восстановление тронуло доску")
        self.assertEqual(read(self.clone / "CLAUDE.md"), self.thin_keep,
                         "восстановленный тонкий файл разошёлся с прежним")
        _, out = self.box.dkctl_run("corp", "-C", str(self.clone), path=self.corppath)
        self.assertIn_("хук commit-msg: цепочка на месте", out,
                       "прогон после восстановления опять раскладывал цепочку")


class CorpConnectTest(SandboxCase):
    """Подключение корп-проекта целиком, как его гоняет CONNECT.md (DK-125):
    привязка и контур заводятся флагами, remote доски выставляется, а
    недоделанное перечисляет сама команда. Проверяется всё на настоящем прогоне
    corp, а не на фикстуре: расхождение фикстуры с раскладкой уже дважды
    пропускало баги серии.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        corpbin = cls.box.root / "corpbin"
        corpbin.mkdir()
        executable(corpbin / "taskctl", cls.box.board_taskctl())
        cls.corppath = "%s:%s" % (corpbin, cls.box.cleanpath)
        cls.clone = cls.box.root / "corp-two"
        # Контур corp2 из флагов: боковая директория общая на контур, проект в
        # ней подкаталогом (DK-583).
        cls.local = cls.box.root / "corp2-local" / "corp-two"
        git_init(cls.clone)
        write(cls.clone / "readme.md", "readme\n")
        git(cls.clone, "add", "-A")
        git(cls.clone, "commit", "-qm", "init")
        cls.board = cls.box.root / "board-two.git"
        run(["git", "init", "-q", "--bare", str(cls.board)])
        cls.contour = cls.box.home / ".devkit" / "tracker" / "corp2.local"

    def corp(self, *args, **kw):
        return self.box.dkctl_run("corp", "-C", str(self.clone), *args,
                                  path=self.corppath, **kw)

    def flags(self):
        return ("--prefix", "CT", "--contour", "corp2", "--key", "ABC",
                "--branch", "{key}-x-{slug}", "--remote", str(self.board))

    def test_1_corp_with_tracker_binding(self):
        rc, out = self.corp(*self.flags())
        self.assertEqual(rc, 0, "corp с привязкой не прошёл: %s" % out)
        tracker = read(self.local / ".devkit" / "tracker.local")
        self.assertRegex(tracker, r"(?m)^contour = corp2$", "corp не вписал contour в привязку")
        self.assertRegex(tracker, r"(?m)^key = ABC$", "corp не вписал key в привязку")
        self.assertRegex(tracker, r"(?m)^branch = \{key\}-x-\{slug\}$",
                         "corp не вписал branch в привязку")
        self.assertEqual(git(self.local, "remote", "get-url", "origin")[1].strip(), str(self.board),
                         "corp не выставил remote доски")
        self.assertTrue(self.contour.is_file(), "corp не завёл болванку контура компании: %s" % out)
        self.assertRegex(read(self.contour), r'(?m)^adapter = "jira"$',
                         "в болванке контура нет адаптера")
        self.assertRegex(read(self.contour), r"(?m)^in_progress = ",
                         "в болванке контура не расписана таблица статусов")
        # Хвост подключения: недоделанное названо путями и ключами, а не
        # оставлено человеку на догадку.
        self.assertIn_("осталось сделать", out, "corp не напечатал, что осталось сделать")
        self.assertIn_("контур компании %s: заполнить base_url, user" % self.contour, out,
                       "хвост corp не назвал незаполненный контур")
        self.assertIn_("токен трекера: export TRACKER_TOKEN", out,
                       "хвост corp не назвал переменную с токеном")
        self.assertIn_("вписать test =", out, "хвост corp не спросил команду тестов")
        self.assertNotIn_("deploy =", out,
                          "хвост corp спросил команду выката, а выкат там ведёт процесс компании")
        self.assertNotIn_("remote add origin", out,
                          "хвост corp зовёт добавить remote, хотя выставил его сам")

    def test_2_rerun_keeps_the_filled_contour(self):
        # Повторный прогон с теми же флагами ключи не дублирует и заполненный
        # контур не переписывает: подключение это и восстановление тоже.
        filled = (read(self.contour).replace('base_url = ""', 'base_url = "https://tracker.example"')
                  .replace('user = ""', 'user = "ivanov"'))
        write(self.contour, filled)
        rc, out = self.corp(*self.flags())
        self.assertEqual(rc, 0, "повторный corp с привязкой не прошёл: %s" % out)
        keys = [ln for ln in read(self.local / ".devkit" / "tracker.local").split("\n")
                if ln == "key = ABC"]
        self.assertEqual(len(keys), 1, "повторный corp продублировал ключ привязки")
        self.assertEqual(read(self.contour), filled, "повторный corp переписал заполненный контур")
        self.assertNotIn_("контур компании", out, "повторный corp снова зовёт заполнять контур")

    def test_3_nothing_left_by_hand(self):
        # Заполнено всё: хвост говорит об этом вслух, молчание тут неотличимо от
        # недоделанной раскладки.
        write(self.local / ".devkit" / "deploy.local",
              "deploy =\ntest = echo тесты\nautonomous = false\n")
        _, out = self.corp(env={"TRACKER_TOKEN": "t"})
        self.assertIn_("подключение завершено", out,
                       "corp промолчал о том, что раскладка доведена")

    def test_4_empty_deploy_is_normal_in_the_corp_contour(self):
        # Слияние и выкат там ведёт процесс компании, а вот пустой test= это
        # находка и здесь.
        write(self.local / ".devkit" / "tracker.sync", "")
        _, out = self.box.dkctl_run("doctor", "-C", str(self.clone), path=self.corppath)
        self.assertNotIn_("пустой deploy=", out, "доктор требует команду выката у корп-проекта")
        write(self.local / ".devkit" / "deploy.local", "deploy =\ntest =\nautonomous = false\n")
        _, out = self.box.dkctl_run("doctor", "-C", str(self.clone), path=self.corppath)
        self.assertIn_("пустой test=", out, "доктор промолчал про пустой test= у корп-проекта")

    def test_5_prefix_equal_to_the_key_is_refused(self):
        # Префикс доски, совпавший с ключом проекта: на незаведённой доске это
        # отказ, а не находка потом. Рубеж следов на такой паре правило про
        # локальный ID снимает.
        third = self.box.root / "corp-three"
        git_init(third)
        write(third / "readme.md", "readme\n")
        git(third, "add", "-A")
        git(third, "commit", "-qm", "init")
        rc, out = self.box.dkctl_run("corp", "-C", str(third), "--prefix", "ABC", "--key", "ABC",
                                     path=self.corppath)
        self.assertEqual(rc, 2, "corp взял префикс доски, совпавший с ключом проекта: %s" % out)
        self.assertIn_("рубеж следов", out, "отказ corp не назвал последствие совпадения")
        self.assertFalse((self.box.root / "corp-three-local" / "docs" / "TASKS.md").exists(),
                         "отказавший corp всё-таки завёл доску")


class PrefixHintTest(unittest.TestCase):
    """Предложение префикса по имени клона (DK-240): правило предсказуемое, а
    предложенное годится доске без проверки человеком."""

    def test_1_one_word_gives_two_letters(self):
        self.assertEqual(corp.prefix_hint("gateway"), "GA")

    def test_2_words_give_initials(self):
        self.assertEqual(corp.prefix_hint("ucs-platform"), "UP")
        self.assertEqual(corp.prefix_hint("api_gw.core"), "AGC")

    def test_3_digits_are_not_letters(self):
        self.assertEqual(corp.prefix_hint("api2-gw"), "AG")

    def test_4_name_without_letters_gives_nothing(self):
        # Предложить нечего, и вопрос уходит без подсказки, а не с мусором.
        self.assertEqual(corp.prefix_hint("2026"), "")
        self.assertEqual(corp.prefix_hint(""), "")

    def test_5_hint_is_always_good_for_the_board(self):
        for name in ("gateway", "ucs-platform", "api2-gw", "касса-онлайн", "x"):
            hint = corp.prefix_hint(name)
            self.assertTrue(not hint or corp.prefix_ok(hint),
                            "предложенный по имени %s префикс %s доска не возьмёт" % (name, hint))


class ContourAnswersTest(unittest.TestCase):
    """Ответы первого прогона в файле контура компании (DK-240)."""

    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.was = os.environ.get("HOME")
        os.environ["HOME"] = self.home
        self.addCleanup(shutil.rmtree, self.home, True)

    def tearDown(self):
        if self.was is None:
            os.environ.pop("HOME", None)
        else:
            os.environ["HOME"] = self.was

    def test_1_answers_land_in_the_file(self):
        corp.ensure_contour("acme", {"base_url": "https://tracker.example", "user": "ivanov"})
        text = read(corp.contour_path("acme"))
        self.assertRegex(text, r'(?m)^base_url = "https://tracker\.example"$',
                         "адрес трекера не вписан")
        self.assertRegex(text, r'(?m)^user = "ivanov"$', "имя пользователя не вписано")
        self.assertRegex(text, r'(?m)^assignee = "\{user\}"$',
                         "подстановка assignee подменена ответом")
        self.assertEqual(corp.contour_value("acme", "base_url"), "https://tracker.example")

    def test_2_without_answers_it_is_the_old_stub(self):
        corp.ensure_contour("acme")
        self.assertRegex(read(corp.contour_path("acme")), r'(?m)^base_url = ""$',
                         "болванка контура разъехалась с прежней")

    def test_3_quotes_from_the_answer_are_dropped(self):
        corp.ensure_contour("acme", {"base_url": '"https://t.example"', "user": "ivanov"})
        self.assertEqual(corp.contour_value("acme", "base_url"), "https://t.example",
                         "кавычки из ответа развалили файл контура")

    def test_4_filled_file_is_left_alone(self):
        corp.ensure_contour("acme", {"base_url": "https://t.example", "user": "ivanov"})
        before = read(corp.contour_path("acme"))
        self.assertEqual(corp.ensure_contour("acme", {"user": "petrov"}), "",
                         "повторный прогон завёл контур заново")
        self.assertEqual(read(corp.contour_path("acme")), before,
                         "повторный прогон переписал заполненный контур")


class InteractiveCorpTest(SandboxCase):
    """Первый прогон corp одной командой (DK-240): недостающее команда
    спрашивает под tty, а без него ведёт себя как раньше. Диалог гоняется на
    настоящем прогоне под псевдотерминалом: подставить isatty значило бы
    проверять заглушку вместо команды.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        corpbin = cls.box.root / "askbin"
        corpbin.mkdir()
        executable(corpbin / "taskctl", cls.box.board_taskctl())
        cls.corppath = "%s:%s" % (corpbin, cls.box.cleanpath)

    def clone(self, name):
        root = git_init(self.box.root / name)
        write(root / "readme.md", "readme\n")
        git(root, "add", "-A")
        git(root, "commit", "-qm", "init")
        return root

    def corp(self, clone, *args, **kw):
        return self.box.dkctl_run("corp", "-C", str(clone), *args, path=self.corppath, **kw)

    def test_1_first_run_asks_the_contour_and_proposes_the_prefix(self):
        clone = self.clone("corp-gate")
        rc, out = self.corp(clone, "--contour", "gatecorp", "--key", "ABC",
                            answers=["", "https://tracker.example", "ivanov"])
        self.assertEqual(rc, 0, "первый прогон под tty не прошёл: %s" % out)
        board = self.box.root / "gatecorp-local" / "corp-gate" / "docs" / "TASKS.md"
        self.assertIn_("префикс CG", read(board),
                       "доска заведена не с предложенным по имени клона префиксом")
        contour = read(self.box.home / ".devkit" / "tracker" / "gatecorp.local")
        self.assertRegex(contour, r'(?m)^base_url = "https://tracker\.example"$',
                         "ответ про адрес трекера не доехал до контура: %s" % out)
        self.assertRegex(contour, r'(?m)^user = "ivanov"$',
                         "ответ про пользователя не доехал до контура: %s" % out)
        self.assertNotIn_("заполнить base_url", out,
                          "после ответов хвост опять зовёт в редактор контура")
        self.assertIn_("с ответами первого прогона", out,
                       "прогон не сказал, что контур заведён с ответов")
        # Про боковую директорию сказано до вопросов, а не строкой отчёта потом.
        self.assertIn_("лягут в боковую директорию", out,
                       "прогон не сказал заранее, куда лягут доска и файлы задач")
        self.assertLess(out.index("лягут в боковую директорию"), out.index("префикс ID задач"),
                        "про боковую директорию сказано уже после вопросов: %s" % out)

    def test_2_proposed_prefix_is_replaced_in_place(self):
        clone = self.clone("corp-gate-two")
        rc, out = self.corp(clone, answers=["zz"])
        self.assertEqual(rc, 0, "прогон с заменой префикса не прошёл: %s" % out)
        board = read(self.box.root / "corp-gate-two-local" / "docs" / "TASKS.md")
        self.assertIn_("префикс ZZ", board, "доска заведена не с названным префиксом")

    def test_3_prefix_equal_to_the_key_is_asked_again(self):
        # Рубеж коллизии остаётся рубежом и в диалоге, но стоит он не отказом:
        # префикс тут ещё выбирается, и второй ответ команда принимает.
        clone = self.clone("corp-clash")
        rc, out = self.corp(clone, "--key", "ABC", answers=["ABC", "CC"])
        self.assertEqual(rc, 0, "прогон с переспросом префикса не прошёл: %s" % out)
        self.assertIn_("рубеж следов", out, "переспрос не назвал причину")
        board = read(self.box.root / "corp-clash-local" / "docs" / "TASKS.md")
        self.assertIn_("префикс CC", board, "доска заведена с префиксом, равным ключу проекта")

    def test_4_bad_answers_end_with_a_refusal(self):
        clone = self.clone("corp-bad")
        rc, out = self.corp(clone, answers=["1", "a b", "-"])
        self.assertEqual(rc, 2, "прогон принял негодный префикс: %s" % out)
        self.assertIn_("заглавными буквами", out, "команда не сказала, чем плох ответ")
        self.assertFalse((self.box.root / "corp-bad-local" / "docs" / "TASKS.md").exists(),
                         "отказавший прогон всё-таки завёл доску")

    def test_5_headless_run_does_not_hang(self):
        # Без tty вопросов нет вовсе: команда отказывает с прежним кодом 2 и
        # называет флаг, а не ждёт ввода, которого некому дать.
        clone = self.clone("corp-headless")
        rc, out = self.corp(clone)
        self.assertEqual(rc, 2, "headless-прогон без префикса прошёл: %s" % out)
        self.assertIn_("без tty", out, "отказ без tty не назвал причину")
        self.assertIn_("--prefix", out, "отказ без tty не назвал флаг")
        self.assertNotIn_("лягут в боковую директорию", out,
                          "отказ без tty рассказал про боковую директорию, которой не завёл")
        self.assertFalse((self.box.root / "corp-headless-local" / "docs" / "TASKS.md").exists(),
                         "отказавший headless-прогон завёл доску")

    def test_6_headless_contour_stays_a_stub(self):
        clone = self.clone("corp-headless-two")
        rc, out = self.corp(clone, "--prefix", "HT", "--contour", "headcorp")
        self.assertEqual(rc, 0, "headless-прогон с флагами не прошёл: %s" % out)
        self.assertIn_("заполнить base_url, user", out,
                       "headless-прогон перестал звать заполнить контур")
        # Прогон, который и правда заводит доску, про боковую директорию говорит
        # и без tty: вопросов там нет, а место рабочих файлов от этого не зависит.
        self.assertIn_("лягут в боковую директорию", out,
                       "headless-прогон промолчал о том, куда лягут доска и файлы задач")
        self.assertRegex(read(self.box.home / ".devkit" / "tracker" / "headcorp.local"),
                         r'(?m)^base_url = ""$', "болванка контура заполнилась без ответов")


if __name__ == "__main__":
    unittest.main()


class CorpWindowTest(SandboxCase):
    """Одно окно на корп-проект (DK-583): доска лежит за ссылкой .devkit в
    дереве клона, ссылка не спрятана строкой exclude, и потому её видят и поиск
    редактора, и сессия. Цена этого одна строка «?? .devkit» в чужом статусе, и
    держит её обёртка pre-commit: в индекс обвязка не уезжает.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        corpbin = cls.box.root / "winbin"
        corpbin.mkdir()
        executable(corpbin / "taskctl", cls.box.board_taskctl())
        cls.corppath = "%s:%s" % (corpbin, cls.box.cleanpath)
        cls.clone = cls.box.root / "corp-win"
        cls.local = cls.box.root / "corp-win-local"
        git_init(cls.clone)
        write(cls.clone / "readme.md", "readme\n")
        git(cls.clone, "add", "-A")
        git(cls.clone, "commit", "-qm", "init")
        cls.rc, cls.out = cls.box.dkctl_run("corp", "--prefix", "CW", "-C", str(cls.clone),
                                            path=cls.corppath)
        # Хвост подключения (команда тестов, remote доски) заполняется сразу:
        # ниже проверяется код доктора, и чужие находки мешали бы ему.
        write(cls.local / ".devkit" / "deploy.local",
              "deploy = echo выкат\ntest = echo тесты\nautonomous = false\n")
        run(["git", "init", "-q", "--bare", str(cls.box.root / "corp-win-board.git")])
        git(cls.local, "remote", "add", "origin", str(cls.box.root / "corp-win-board.git"))

    def test_1_board_opens_from_the_clone(self):
        self.assertEqual(self.rc, 0, "corp не прошёл: %s" % self.out)
        board = self.clone / ".devkit" / "docs" / "TASKS.md"
        self.assertTrue(board.is_file(), "доска не открывается путём .devkit/docs/TASKS.md")
        self.assertIn("префикс CW", read(board), "за ссылкой лежит не доска проекта")

    def test_2_the_link_is_not_hidden_from_search(self):
        # Поиск редактора читает те же источники игнора, что git, и спрятанное
        # от git прячется заодно от него: доска за ссылкой обязана быть видна
        # обоим, поэтому проверяется сам механизм, а не только строка exclude.
        rc, _ = git(self.clone, "check-ignore", "-q", ".devkit/docs/TASKS.md")
        self.assertNotEqual(rc, 0, "доска за ссылкой игнорится, и поиск редактора её не найдёт")
        rg = shutil.which("rg")
        if rg:
            out = run([rg, "--hidden", "--follow", "--files-with-matches", "префикс CW", "."],
                      cwd=str(self.clone))[1]
            self.assertIn(".devkit", out, "rg из клона не нашёл текст доски: %s" % out)

    def test_3_index_of_the_corp_commit_is_guarded(self):
        # Ссылка висит в статусе, и «git add .» кладёт её в индекс молча. Это
        # последнее место, где видно: дальше она уехала бы в чужую историю.
        write(self.clone / "note.md", "заметка\n")
        git(self.clone, "add", ".")
        rc, out = git(self.clone, "commit", "-m", "chore: правка")
        self.assertNotEqual(rc, 0, "обвязка devkit прошла в коммит корп-репозитория: %s" % out)
        self.assertIn_("обвязка devkit в индексе", out, "хук не назвал причину отказа")
        self.assertIn_("git rm --cached", out, "хук не сказал, чем снять обвязку из индекса")
        git(self.clone, "rm", "-q", "--cached", ".devkit")
        rc, out = git(self.clone, "commit", "-m", "chore: правка")
        self.assertEqual(rc, 0, "коммит без обвязки в индексе не прошёл: %s" % out)

    def test_4_thin_file_in_the_index_is_guarded_too(self):
        git(self.clone, "add", "-f", "CLAUDE.md")
        rc, out = git(self.clone, "commit", "-m", "chore: файл контекста")
        self.assertNotEqual(rc, 0, "тонкий файл контекста прошёл в коммит: %s" % out)
        self.assertIn_("CLAUDE.md", out, "хук не назвал тонкий файл в индексе")
        git(self.clone, "rm", "-q", "--cached", "CLAUDE.md")

    def test_5_clean_drops_the_link_and_fix_returns_it(self):
        # «git clean -xdf» сносит ссылку, а файлы за ней целы: в индексе её нет,
        # и для git это обычный неотслеживаемый файл.
        run(["git", "-C", str(self.clone), "clean", "-xdfq"])
        self.assertFalse((self.clone / ".devkit").exists(), "clean не снёс ссылку, фикстура не та")
        self.assertTrue((self.local / "docs" / "TASKS.md").is_file(), "clean достал доску за ссылкой")
        _, out = self.box.dkctl_run("doctor", "-C", str(self.clone), path=self.corppath)
        self.assertIn_("нет ссылки .devkit", out, "доктор промолчал про снесённую ссылку")
        rc, out = self.box.dkctl_run("doctor", "--fix", "-C", str(self.clone), path=self.corppath)
        self.assertEqual(rc, 0, "--fix не вернул ссылку: %s" % out)
        self.assertTrue((self.clone / ".devkit" / "docs" / "TASKS.md").is_file(),
                        "--fix сказал про ссылку, а доска из клона не открывается")

    def test_6_stale_exclude_line_is_dropped(self):
        # Клон, подключённый до DK-583, прячет .devkit строкой exclude, и доска
        # из его окна не находится. Строку убирает тот же --fix.
        excl = self.clone / ".git" / "info" / "exclude"
        write(excl, read(excl) + ".devkit\n")
        _, out = self.box.dkctl_run("doctor", "-C", str(self.clone), path=self.corppath)
        self.assertIn_("exclude прячет .devkit", out, "доктор промолчал про спрятанную ссылку")
        rc, out = self.box.dkctl_run("doctor", "--fix", "-C", str(self.clone), path=self.corppath)
        self.assertEqual(rc, 0, "--fix не убрал строку exclude: %s" % out)
        self.assertNotIn(".devkit", read(excl).split("\n"), "строка exclude осталась после --fix")

    def test_7_old_link_dir_becomes_a_link(self):
        # Прежняя раскладка держала под этим именем каталог с двумя ссылками на
        # соседние деревья: подключение заменяет его ссылкой на боковую
        # директорию, потому что те же ссылки лежат теперь в ней самой.
        link = self.clone / ".devkit"
        link.unlink()
        link.mkdir()
        (link / "devkit").symlink_to(str(self.box.dk))
        (link / "local").symlink_to(str(self.local))
        rc, out = self.box.dkctl_run("corp", "-C", str(self.clone), path=self.corppath)
        self.assertEqual(rc, 0, "corp не прошёл на клоне прежней раскладки: %s" % out)
        self.assertTrue(link.is_symlink(), "каталог обвязки не стал ссылкой: %s" % out)
        self.assertTrue((link / "docs" / "TASKS.md").is_file(),
                        "после замены каталога доска из клона не открывается")

    def test_8_foreign_dir_under_the_name_is_left_alone(self):
        # Под тем же именем лежит чужое: сносить его молча дороже, чем сказать.
        link = self.clone / ".devkit"
        link.unlink()
        link.mkdir()
        write(link / "own.txt", "чужое\n")
        _, out = self.box.dkctl_run("doctor", "-C", str(self.clone), path=self.corppath)
        self.assertIn_("это свой каталог или файл", out, "доктор молча снёс бы чужой каталог")
        _, out = self.box.dkctl_run("doctor", "--fix", "-C", str(self.clone), path=self.corppath)
        self.assertTrue((link / "own.txt").is_file(), "--fix снёс чужой каталог")
        shutil.rmtree(str(link))
        self.box.dkctl_run("doctor", "--fix", "-C", str(self.clone), path=self.corppath)


class CorpContourTest(SandboxCase):
    """Боковая директория общая на контур (DK-583): два проекта одного контура
    ложатся подкаталогами ../<контур>-local, репозиторий там один на всех, а
    путь можно назвать и явным флагом --local.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        corpbin = cls.box.root / "ctbin"
        corpbin.mkdir()
        executable(corpbin / "taskctl", cls.box.board_taskctl())
        cls.corppath = "%s:%s" % (corpbin, cls.box.cleanpath)

    def clone(self, name):
        root = git_init(self.box.root / name)
        write(root / "readme.md", "readme\n")
        git(root, "add", "-A")
        git(root, "commit", "-qm", "init")
        return root

    def corp(self, clone, *args):
        return self.box.dkctl_run("corp", "-C", str(clone), *args, path=self.corppath)

    def test_1_two_projects_share_one_repo(self):
        one, two = self.clone("ct-one"), self.clone("ct-two")
        rc, out = self.corp(one, "--prefix", "CO", "--contour", "acme")
        self.assertEqual(rc, 0, "corp первого проекта контура не прошёл: %s" % out)
        contour = self.box.root / "acme-local"
        self.assertTrue((contour / "ct-one" / "docs" / "TASKS.md").is_file(),
                        "доска первого проекта легла не в директорию контура: %s" % out)
        self.assertTrue((contour / ".git").is_dir(),
                        "репозиторий заведён не на директории контура: %s" % out)
        self.assertFalse((contour / "ct-one" / ".git").exists(),
                         "у проекта контура свой вложенный репозиторий")
        rc, out = self.corp(two, "--prefix", "CT", "--contour", "acme")
        self.assertEqual(rc, 0, "corp второго проекта контура не прошёл: %s" % out)
        self.assertIn_("подкаталогом репозитория", out,
                       "corp не сказал, что доска легла в готовый репозиторий контура")
        self.assertFalse((contour / "ct-two" / ".git").exists(),
                         "второй проект завёл свой репозиторий внутри контура")
        self.assertEqual(git(one, "config", "--local", "devkit.local")[1].strip(),
                         "../acme-local/ct-one", "редирект ведёт не в директорию контура")
        self.assertTrue((one / ".devkit" / "docs" / "TASKS.md").is_file(),
                        "доска первого проекта не открывается из его клона")
        self.assertTrue((two / ".devkit" / "docs" / "TASKS.md").is_file(),
                        "доска второго проекта не открывается из его клона")

    def test_2_lost_rig_is_found_through_the_contour(self):
        # Редирект потерян переклонированием, а привязка лежит подкаталогом
        # директории контура: обвязка находится и оттуда.
        clone = self.box.root / "ct-one"
        keep = git(clone, "config", "--local", "devkit.local")[1].strip()
        git(clone, "config", "--unset", "devkit.local")
        self.assertEqual(os.path.realpath(corp.lost_local(str(clone), str(self.box.dk))),
                         os.path.realpath(str(self.box.root / "acme-local" / "ct-one")),
                         "потерянная обвязка не нашлась по соседней директории контура")
        git(clone, "config", "devkit.local", keep)

    def test_3_local_flag_names_the_path(self):
        clone = self.clone("ct-three")
        where = self.box.root / "boards" / "ct-three"
        rc, out = self.corp(clone, "--prefix", "CF", "--local", str(where))
        self.assertEqual(rc, 0, "corp с --local не прошёл: %s" % out)
        self.assertTrue((where / "docs" / "TASKS.md").is_file(),
                        "доска легла не туда, куда позвал --local: %s" % out)
        self.assertEqual(os.path.realpath(str(clone / ".devkit")), os.path.realpath(str(where)),
                         "ссылка клона ведёт не в названную флагом директорию")

    def test_4_old_layout_survives(self):
        # Клон, подключённый прежней раскладкой, остаётся при своей
        # ../<проект>-local: редирект называет её явно, и переезжать ему незачем.
        clone = self.clone("ct-old")
        old = self.box.root / "ct-old-local"
        rc, out = self.corp(clone, "--prefix", "CD", "--local", str(old))
        self.assertEqual(rc, 0, "corp прежней раскладки не прошёл: %s" % out)
        rc, out = self.corp(clone, "--contour", "acme")
        self.assertEqual(rc, 0, "повторный corp с контуром не прошёл: %s" % out)
        self.assertEqual(git(clone, "config", "--local", "devkit.local")[1].strip(), "../ct-old-local",
                         "повторный прогон утащил прежний клон в директорию контура")
