#!/usr/bin/env python3
"""Корп-контур: подключение клона к боковой директории с доской, цепочка хуков,
рубеж следов и корп-проверки доктора (DK-085, DK-124, DK-125).

Доску заводит taskctl, а в подставном PATH он заглушка на «exit 0», поэтому на
эти проверки кладётся своя, которая скелет доски всё-таки пишет: без доски
проверять было бы нечего.
"""
import os
import shutil
import unittest

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
        self.assertIn("@.devkit/local/AGENTS.md", thin,
                      "тонкий файл клона не импортирует AGENTS.md боковой директории")
        self.assertIn("@.devkit/devkit/RULES.board.md", thin,
                      "тонкий файл клона не импортирует правила доски через ссылку")
        self.assertFalse([ln for ln in thin if ln.startswith("@..")],
                         "тонкий файл клона зовёт импорт путём наружу: %s" % thin)
        links = self.clone / rules.LINK_DIR
        self.assertEqual(os.path.realpath(str(links / rules.DEVKIT_LINK)),
                         os.path.realpath(str(self.box.dk)),
                         "ссылка на дерево devkit ведёт не туда")
        self.assertEqual(os.path.realpath(str(links / rules.LOCAL_LINK)), self.localr,
                         "ссылка на боковую директорию ведёт не туда")
        self.assertIn(rules.LINK_DIR, read(self.clone / ".git" / "info" / "exclude").split("\n"),
                      "corp не спрятал каталог со ссылками строкой exclude")
        self.assertFalse((self.clone / "AGENTS.md").exists(),
                         "corp положил AGENTS.md в дерево корп-клона")
        self.assertEqual(git(self.clone, "status", "--short")[1].strip(), "",
                         "обвязка торчит в git status корп-клона")

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
        cls.local = cls.box.root / "corp-two-local"
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


if __name__ == "__main__":
    unittest.main()
