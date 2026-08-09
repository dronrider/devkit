#!/usr/bin/env python3
"""Генератор правил: маркеры и хеши, глубина, тонкие файлы харнесов, вклейка,
глобальная точка правил и раскладки для стенда послушания.
"""
import os
import re
import shutil
import tempfile
import unittest
from pathlib import Path

from testenv import (PY, DEVKIT_SRC, SandboxCase, fake_home, git_init, harness, read, rules,
                     run, without_pockets, write)

MARKER = re.compile(r"^<!-- devkit:generated body=[0-9a-f]{12} -->$")
EMBED_MARKER = re.compile(r"^<!-- devkit:rules begin src=[0-9a-f]{12} body=[0-9a-f]{12} -->$", re.M)
POINTERS_MARKER = re.compile(
    r"^<!-- devkit:rules begin depth=pointers src=[0-9a-f]{12} body=[0-9a-f]{12} -->$", re.M)
EMBED_TOOL = ('# Подставной профиль для самопроверки: импортов инструмент не понимает,\n'
              '# правила доезжают до него только вклейкой, остальных осей у него нет.\n'
              '[detect]\n\n[rules]\nmode = "embed"\n\n[delegate]\nmode = "none"\n'
              '\n[hooks]\n\n[quota]\n')


def first_line(path):
    return read(path).split("\n")[0]


class GeneratorUnitsTest(unittest.TestCase):
    """Юниты генератора правил: хеш маркера, поломанные маркеры, забор кода.
    Гоняются прямо по модулю, доктор до них не нужен.
    """

    def test_marker_hash(self):
        prof = harness.parse("p.toml", '[rules]\nmode = "import"\nfile = "CLAUDE.md"\n'
                                       'import_line = "@{path}"\n')
        thin = rules.thin_text(prof, "/proj", "/proj/../devkit", board=False, embed=True)
        stamp, body = rules.generated_parts(thin)
        self.assertEqual(body, "@AGENTS.md\n")
        self.assertEqual(stamp, rules.digest(body), thin)
        self.assertEqual(rules.generated_parts("# рукописный\n"), (None, None))

    def test_block_round_trip(self):
        block = rules.block_text("aabbccddeeff", "правила\n")
        text = rules.put_block("# проект\n", block)
        self.assertEqual(rules.find_block(text)[1], "aabbccddeeff", text)
        self.assertEqual(rules.find_block(text)[3], "правила\n", text)
        self.assertEqual(rules.drop_block(text), "# проект\n")

    def test_broken_markers(self):
        block = rules.block_text("aabbccddeeff", "правила\n")
        with self.assertRaises(rules.BrokenMarkers, msg="конец без начала принят за целую вклейку"):
            rules.find_block("# проект\n<!-- devkit:rules end -->\n")
        with self.assertRaises(rules.BrokenMarkers, msg="начало без конца принято за целую вклейку"):
            rules.find_block(block.split("\n")[0] + "\nтекст\n")

    def test_code_fence_is_text(self):
        # Пример в блоке кода это текст, а не вклейка и не импорт: и то и другое
        # показано в дизайне DK-033 и в доке, скан по ним не должен спотыкаться.
        block = rules.block_text("aabbccddeeff", "правила\n")
        sample = "пример:\n\n```\n" + block + "@../devkit/RULES.md\n```\n"
        self.assertIsNone(rules.find_block(sample), sample)
        self.assertEqual(rules.handwritten_imports(sample), [])
        self.assertEqual(rules.handwritten_imports("# проект\n@../devkit/RULES.md\n"),
                         [(2, "@../devkit/RULES.md")])


class DepthTest(unittest.TestCase):
    """Глубина правил: три ветки оси скиллов, признак глубины в тонком файле и
    побайтная сверка сегодняшней раскладки с эталоном. Гоняется на выдуманном
    devkit во временной директории, чтобы нарезанное ядро можно было изобразить
    файлами, которых в настоящем devkit пока нет.
    """

    @staticmethod
    def prof(skills):
        return harness.parse("p.toml", '[rules]\nmode = "import"\nfile = "T.md"\n'
                                       'import_line = "@{path}"\n' + skills)

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-depth-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.dk = self.work / "devkit"
        self.proj = self.work / "proj"
        self.proj.mkdir(parents=True)
        write(self.dk / "RULES.md", "# правила\n")
        write(self.dk / "RULES.board.md", "# правила доски\n")
        write(self.dk / "kit" / "skills" / "board-test" / "SKILL.md",
              "---\nname: board-test\ndescription: Процедура доски. Звать, когда трогают задачу.\n"
              "---\n\n# Доска\n")

    def test_declared_depth(self):
        none, empty = self.prof(""), self.prof("\n[skills]\n")
        auto = self.prof('\n[skills]\ndir = "~/.t/skills"\ndiscovery = "auto"\n')
        manual = self.prof('\n[skills]\ndiscovery = "manual"\n')
        self.assertEqual(rules.declared_depth(none)[0], rules.DEPTH_FULL)
        self.assertEqual(rules.declared_depth(empty)[0], rules.DEPTH_FULL)
        # Пустая секция и её отсутствие дают одну глубину, а значат разное: у
        # первого ось разобрана, у второго до неё не дошли, и сказать об этом
        # надо по-разному.
        self.assertNotEqual(rules.declared_depth(none)[1], rules.declared_depth(empty)[1])
        self.assertEqual(rules.declared_depth(auto)[0], rules.DEPTH_CORE)
        self.assertEqual(rules.declared_depth(manual)[0], rules.DEPTH_POINTERS)

    def test_depth_waits_for_the_core(self):
        # Ядра ещё нет: объявленная глубина остаётся обещанием, доезжает полный
        # текст, и признака в маркере нет. Указателям ядро нужно не меньше, чем
        # ядерной ветке: рядом с полным текстом таблица указателей стала бы
        # второй копией тех же процедур.
        dk, proj = str(self.dk), str(self.proj)
        manual = self.prof('\n[skills]\ndiscovery = "manual"\n')
        self.assertEqual(rules.actual_depth(dk, proj, True, rules.DEPTH_CORE), rules.DEPTH_FULL)
        self.assertEqual(rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS), rules.DEPTH_FULL)
        early = rules.thin_text(manual, proj, dk, True, False,
                                rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS))
        self.assertTrue(early.startswith("<!-- devkit:generated body="), early)
        self.assertNotIn("Процедуры devkit", early)
        self.assertIn("@.devkit/devkit/RULES.md\n", early)
        # Ядро нарезано наполовину: пока полон хоть один текст, глубины нет ни у
        # одной из двух неполных веток.
        write(self.dk / "RULES.core.md", "# ядро\n")
        self.assertEqual(rules.actual_depth(dk, proj, True, rules.DEPTH_CORE), rules.DEPTH_FULL)
        self.assertEqual(rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS), rules.DEPTH_FULL)
        write(self.dk / "RULES.board.core.md", "# ядро доски\n")
        self.assertEqual(rules.actual_depth(dk, proj, True, rules.DEPTH_CORE), rules.DEPTH_CORE)
        self.assertEqual(rules.actual_depth(dk, proj, True, rules.DEPTH_POINTERS),
                         rules.DEPTH_POINTERS)

    def test_core_thin_file(self):
        write(self.dk / "RULES.core.md", "# ядро\n")
        write(self.dk / "RULES.board.core.md", "# ядро доски\n")
        dk, proj = str(self.dk), str(self.proj)
        auto = self.prof('\n[skills]\ndir = "~/.t/skills"\ndiscovery = "auto"\n')
        # При вклейке тонкий файл правил не везёт вовсе, и глубина не про него.
        embedded = rules.thin_text(auto, proj, dk, True, True, rules.DEPTH_CORE)
        self.assertTrue(embedded.startswith("<!-- devkit:generated body="), embedded)
        thin = rules.thin_text(auto, proj, dk, True, False, rules.DEPTH_CORE)
        self.assertTrue(thin.startswith("<!-- devkit:generated depth=core body="), thin)
        # Ядро общих правил везёт глобальная точка машины, и тонкий файл его не
        # зовёт вовсе; ядро доски она взять не может, и его он зовёт через
        # ссылку каталога обвязки, а не путём наружу (DK-193).
        self.assertNotIn("RULES.core.md", thin)
        self.assertIn("@.devkit/devkit/RULES.board.core.md\n", thin)
        self.assertNotIn("@../devkit/", thin)

    def test_pointers(self):
        # Указатели: строка на скилл с описанием и путём, по которому его читать.
        write(self.dk / "RULES.core.md", "# ядро\n")
        write(self.dk / "RULES.board.core.md", "# ядро доски\n")
        dk, proj = str(self.dk), str(self.proj)
        ptr = rules.pointers_text(dk, proj)
        self.assertIn("board-test", ptr)
        self.assertIn("`../devkit/kit/skills/board-test/SKILL.md`", ptr)
        self.assertIn("Звать, когда трогают задачу.", ptr)
        manual = self.prof('\n[skills]\ndiscovery = "manual"\n')
        thin = rules.thin_text(manual, proj, dk, True, False, rules.DEPTH_POINTERS)
        self.assertTrue(thin.startswith("<!-- devkit:generated depth=pointers body="), thin)
        self.assertIn("board-test", thin)
        stamp, body = rules.generated_parts(thin)
        self.assertEqual(stamp, rules.digest(body), thin)
        # Хеш маркера с глубиной сходится с телом, а тронутый руками файл
        # расходится: без этого правку молча перетёрли бы при первой же
        # перегенерации.
        stamp, body = rules.generated_parts(thin + "своя строка\n")
        self.assertIsNotNone(stamp)
        self.assertNotEqual(stamp, rules.digest(body))

    def test_embed_depth_is_the_fullest(self):
        # Вклейка одна на всех, и глубина у неё самая полная из запрошенных.
        self.assertEqual(rules.embed_depth([rules.DEPTH_CORE, rules.DEPTH_FULL]), rules.DEPTH_FULL)
        self.assertEqual(rules.embed_depth([rules.DEPTH_CORE, rules.DEPTH_POINTERS]),
                         rules.DEPTH_POINTERS)
        self.assertEqual(rules.embed_depth([]), rules.DEPTH_FULL)

    def test_today_layout_matches_expected(self):
        # Эталон сегодняшней раскладки, снятый с генератора: пока ядро не
        # названо, активный профиль обязан давать те же байты. Глубину считает
        # выдуманный devkit без нарезанного ядра, эталон снят с него же.
        cc = harness.parse("claude-code.toml",
                           read(DEVKIT_SRC / "kit" / "harness" / "claude-code.toml"))
        declared = rules.declared_depth(cc)[0]
        self.assertEqual(declared, rules.DEPTH_CORE)
        fact = rules.actual_depth(str(self.dk), str(self.work / "nowhere"), True, declared)
        self.assertEqual(fact, rules.DEPTH_FULL, fact)
        for name, board in (("thin-board.expected", True), ("thin-noboard.expected", False)):
            want = read(DEVKIT_SRC / "tools" / "devkitctl" / "testdata" / name)
            got = rules.thin_text(cc, "/nowhere/proj", "/nowhere/devkit", board, False, fact)
            self.assertEqual(got, want, name)


class SelfHostingGuardTest(unittest.TestCase):
    """DK-127: чекаут devkit сам себе описываемый проект (self-hosting) берёт
    правила из себя, а не из чужого чекаута, которым его назвали снаружи.
    На диске имитируется сам инцидент: root это настоящий чекаут devkit,
    devkit-параметр указывает на другую его копию (временную, для тестов).
    """

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-guard-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.root = self.work / "checkout"
        write(self.root / "RULES.md", "# свои правила\n")
        write(self.root / "RULES.board.md", "# свои правила доски\n")
        write(self.root / "AGENTS.md", "# devkit\n")
        write(self.root / "docs" / "TASKS.md", "# Задачи\n")
        write(self.root / "tools" / "devkitctl" / "devkitctl.py", "# заглушка\n")
        self.stray_dk = self.work / "elsewhere" / "devkit"
        write(self.stray_dk / "RULES.md", "# чужие правила\n")
        write(self.stray_dk / "RULES.board.md", "# чужие правила доски\n")
        shutil.copytree(str(DEVKIT_SRC / "kit" / "harness"), str(self.stray_dk / "kit" / "harness"))
        self.home = self.work / "home"

    def test_is_devkit_checkout(self):
        self.assertTrue(rules.is_devkit_checkout(self.root),
                        "настоящий чекаут devkit не опознан")
        self.assertFalse(rules.is_devkit_checkout(self.stray_dk),
                         "копия без RULES.md ошибочно опознана чекаутом devkit")
        self.assertFalse(rules.is_devkit_checkout(self.work),
                         "пустая директория ошибочно опознана чекаутом devkit")

    def test_self_hosting_root_ignores_the_devkit_it_was_named(self):
        # Своё ядро devkit себе не импортирует, даже когда снаружи назвали
        # чужой чекаут: единственный законный источник тут сам root.
        sources = rules.rule_sources(self.stray_dk, self.root, True)
        self.assertEqual(sources, [self.root / "RULES.board.md"], sources)

    def test_stray_devkit_does_not_leak_into_thin_file(self):
        with fake_home(self.home):
            findings, fixed = rules.check(self.root, self.stray_dk, fix=True)
        self.assertIn("CLAUDE.md сгенерирован для харнеса claude-code", fixed, (findings, fixed))
        claude = read(self.root / "CLAUDE.md")
        self.assertNotIn(str(self.stray_dk), claude, claude)
        self.assertNotIn("elsewhere", claude, claude)
        self.assertIn("@RULES.board.md", claude, claude)

    def test_sibling_devkit_thin_file_still_generated(self):
        # Регресс: обычный подключённый проект (devkit не self-hosting)
        # продолжает брать правила из devkit, которого ему назвали, как раньше.
        proj = self.work / "proj"
        write(proj / "AGENTS.md", "# проект\n")
        sources = rules.rule_sources(self.stray_dk, proj, True)
        self.assertEqual(sources, [self.stray_dk / "RULES.md", self.stray_dk / "RULES.board.md"],
                         sources)


class StablePathGuardTest(unittest.TestCase):
    """DK-127, третий круг: обычный подключённый проект (devkit не
    self-hosting) не должен переписывать свою коммитируемую обвязку под тот
    физический чекаут devkit, из которого позвали доктора (чужой клон,
    временная копия для тестов). После DK-193 тонкий файл чекаута не называет
    вовсе, он зовёт правила через ссылку `.devkit/devkit`, и держать стабильным
    остаётся её саму.
    """

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-stable-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.proj = self.work / "workspace" / "proj"
        write(self.proj / "AGENTS.md", "# проект\n")
        write(self.proj / "docs" / "TASKS.md", "# Задачи\n")
        self.canon_dk = self.work / "canon" / "devkit"
        write(self.canon_dk / "RULES.md", "# правила\n")
        write(self.canon_dk / "RULES.board.md", "# правила доски\n")
        shutil.copytree(str(DEVKIT_SRC / "kit" / "harness"), str(self.canon_dk / "kit" / "harness"))
        self.home = self.work / "home"

    def clone(self, name):
        # Копия devkit того же содержимого в никак не связанной директории:
        # так собран и одноразовый git-clone из ревью.
        c = self.work / name / "devkit"
        shutil.copytree(str(self.canon_dk), str(c))
        return c

    def link(self):
        return os.readlink(str(self.proj / rules.LINK_DIR / rules.DEVKIT_LINK))

    def test_thin_file_names_no_checkout(self):
        # Коммитируемый файл обязан выходить одинаковым на любой машине, а
        # физический путь чекаута у каждой свой: в тонком файле его больше нет.
        with fake_home(self.home):
            findings, fixed = rules.check(self.proj, self.canon_dk, fix=True)
        self.assertIn("CLAUDE.md сгенерирован для харнеса claude-code", fixed, (findings, fixed))
        thin = read(self.proj / "CLAUDE.md")
        self.assertIn("@.devkit/devkit/RULES.md\n", thin, thin)
        self.assertNotIn("canon", thin, thin)
        self.assertNotIn("..", thin, thin)
        self.assertEqual(self.link(), os.path.relpath(str(self.canon_dk),
                                                      str(self.proj / rules.LINK_DIR)))

    def test_clone_of_the_same_content_touches_nothing(self):
        with fake_home(self.home):
            rules.check(self.proj, self.canon_dk, fix=True)
        before, link = read(self.proj / "CLAUDE.md"), self.link()
        clone = self.clone("elsewhere")
        with fake_home(self.home):
            findings, fixed = rules.check(self.proj, clone, fix=True)
        self.assertEqual(read(self.proj / "CLAUDE.md"), before,
                         "тонкий файл переписан прогоном из другого чекаута devkit")
        self.assertEqual(self.link(), link,
                         "ссылка перевешена на чекаут, из которого позвали доктора")
        self.assertEqual(fixed, [], "чужой чекаут дал правку на исправном проекте: %r" % (fixed,))
        self.assertFalse([f for f in findings if "CLAUDE.md" in f or "devkit/devkit" in f],
                         "чужой чекаут дал находку на исправном проекте: %r" % (findings,))

    def test_updated_rules_arrive_without_touching_the_project(self):
        # Обновление правил в дереве, куда ведёт ссылка, до сессии доезжает
        # само: тонкий файл называет путь, а не содержимое, и трогать его
        # обновлению devkit больше незачем.
        with fake_home(self.home):
            rules.check(self.proj, self.canon_dk, fix=True)
        before = read(self.proj / "CLAUDE.md")
        write(self.canon_dk / "RULES.board.md", "# правила доски\n\nновая строка\n")
        with fake_home(self.home):
            findings, fixed = rules.check(self.proj, self.canon_dk, fix=True)
        self.assertEqual(read(self.proj / "CLAUDE.md"), before, (findings, fixed))
        through = self.proj / rules.LINK_DIR / rules.DEVKIT_LINK / "RULES.board.md"
        self.assertIn("новая строка", read(through),
                      "обновлённые правила не читаются путём, которым их зовёт тонкий файл")


class DevkitLinkTest(unittest.TestCase):
    """Ссылка `.devkit/devkit` на дерево правил: кому она кладётся, что доктор
    говорит про пропавшую, чужую и занятую (DK-193, решения 2 и 5). Ядро в
    выдуманном devkit нарезано: без него глубина падает до полного текста, и
    ссылка нужна даже проекту без доски.
    """

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-link-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.dk = self.work / "devkit"
        for name, text in (("RULES.md", "# правила\n"), ("RULES.core.md", "# ядро\n"),
                           ("RULES.board.md", "# правила доски\n"),
                           ("RULES.board.core.md", "# ядро доски\n")):
            write(self.dk / name, text)
        shutil.copytree(str(DEVKIT_SRC / "kit" / "harness"), str(self.dk / "kit" / "harness"))
        self.proj = self.work / "proj"
        write(self.proj / "AGENTS.md", "# проект\n")
        self.home = self.work / "home"
        self.link = self.proj / rules.LINK_DIR / rules.DEVKIT_LINK

    def board(self):
        write(self.proj / "docs" / "TASKS.md", "# Задачи\n")

    def check(self, fix=False):
        with fake_home(self.home):
            return rules.check(self.proj, self.dk, fix=fix)

    def test_project_without_board_gets_neither_import_nor_link(self):
        # Ядро общих правил везёт глобальная точка, и проекту без доски звать из
        # devkit больше нечего: ссылку ему класть незачем.
        findings, fixed = self.check(fix=True)
        thin = read(self.proj / "CLAUDE.md")
        self.assertEqual(rules.generated_parts(thin)[1], "@AGENTS.md\n", thin)
        self.assertFalse(self.link.exists() or self.link.is_symlink(),
                         "ссылка положена проекту, который через неё ничего не зовёт")
        self.assertFalse([f for f in fixed if "ссылка" in f], (findings, fixed))
        self.assertEqual(self.check()[0], [], "чистый проект без доски дал находки")

    def test_board_project_gets_the_link_and_calls_rules_through_it(self):
        self.board()
        findings, fixed = self.check(fix=True)
        self.assertTrue([f for f in fixed if "положена ссылка .devkit/devkit" in f],
                        (findings, fixed))
        self.assertEqual(os.readlink(str(self.link)),
                         os.path.relpath(str(self.dk), str(self.proj / rules.LINK_DIR)),
                         "цель ссылки не относительный путь до соседнего дерева")
        thin = read(self.proj / "CLAUDE.md")
        self.assertIn("@.devkit/devkit/RULES.board.core.md\n", thin, thin)
        self.assertTrue((self.link / "RULES.board.core.md").is_file(),
                        "импорт тонкого файла не разворачивается по диску")
        self.assertEqual(self.check()[0], [], "чистый проект с доской дал находки")

    def test_missing_link_is_a_finding_and_fix_lays_it_back(self):
        self.board()
        self.check(fix=True)
        self.link.unlink()
        findings, _ = self.check()
        self.assertTrue([f for f in findings if "нет ссылки .devkit/devkit" in f], findings)
        # Молчаливого состояния у дыры не остаётся: про тот же путь говорит и
        # проверка битого импорта.
        self.assertTrue([f for f in findings if "не разворачивается" in f], findings)
        findings, fixed = self.check(fix=True)
        self.assertTrue([f for f in fixed if "положена ссылка" in f], (findings, fixed))
        self.assertEqual(findings, [], findings)

    def test_link_to_a_stranger_is_repointed(self):
        self.board()
        self.check(fix=True)
        stray = self.work / "stray"
        stray.mkdir()
        self.link.unlink()
        self.link.symlink_to(os.path.relpath(str(stray), str(self.link.parent)))
        findings, _ = self.check()
        self.assertTrue([f for f in findings if "ссылка .devkit/devkit ведёт в" in f], findings)
        findings, fixed = self.check(fix=True)
        self.assertTrue([f for f in fixed if "перевешена" in f], (findings, fixed))
        self.assertTrue((self.link / "RULES.board.core.md").is_file(),
                        "перевешенная ссылка ведёт мимо дерева правил")

    def test_taken_name_is_left_alone(self):
        self.board()
        write(self.proj / rules.LINK_DIR / rules.DEVKIT_LINK, "чужой файл\n")
        findings, fixed = self.check(fix=True)
        self.assertTrue([f for f in findings if "имя занято" in f], findings)
        self.assertFalse([f for f in fixed if "ссылка" in f],
                         "--fix тронул занятое имя: %s" % (fixed,))
        self.assertEqual(read(self.link), "чужой файл\n", "--fix затёр чужой файл")


class GlobalPointUnitsTest(unittest.TestCase):
    """Юниты глобальной точки правил: tilde_path (представление от ~ там, где
    devkit лежит внутри home, иначе абсолютный путь) и global_target (тянет
    ядро, если оно нарезано, иначе полный текст, тем же условием, что и
    rule_sources). Своя фикстура HOME, чтобы не зависеть от home настоящей
    машины, где devkit скорее всего и лежит внутри него.
    """

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-global-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.home = self.work / "home"
        (self.home / "nested" / "devkit-in-home").mkdir(parents=True)
        home = fake_home(self.home)
        home.__enter__()
        self.addCleanup(home.__exit__, None, None, None)

    def test_tilde_path(self):
        inhome = str(self.home / "nested" / "devkit-in-home")
        outside = str(self.work / "outside")
        os.makedirs(outside)
        self.assertEqual(rules.tilde_path(inhome), "~/nested/devkit-in-home")
        self.assertEqual(rules.tilde_path(outside), os.path.realpath(outside))

    def test_global_target_follows_the_core(self):
        gd = self.work / "gdevkit"
        write(gd / "RULES.md", "# правила\n")
        self.assertEqual(rules.global_target(str(gd)), rules.Path(str(gd)) / "RULES.md")
        write(gd / "RULES.core.md", "# ядро\n")
        self.assertEqual(rules.global_target(str(gd)), rules.Path(str(gd)) / "RULES.core.md")


class MachinePathsUnitsTest(unittest.TestCase):
    """Пути машинного хозяйства харнеса: каталог из машинного слоя, подстановка
    {home} в пути профиля и отказ там, где подставлять нечего.
    """

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-homes-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.home = self.work / "home"
        self.home.mkdir()
        home = fake_home(self.home)
        home.__enter__()
        self.addCleanup(home.__exit__, None, None, None)
        self.conf = self.home / ".devkit" / "harness.local"

    def test_home_comes_from_the_machine_layer(self):
        write(self.conf, 'enabled = ["glm-code"]\n\n[glm-code]\nhome = "~/.claude-glm"\n')
        homes = rules.machine_homes(str(self.conf))
        # Ведущий ~/ разворачивается тут же: буквальный завёл бы каталог с
        # именем ~ в рабочем дереве, причём один раз и навсегда.
        self.assertEqual(homes, {"glm-code": str(self.home / ".claude-glm")})
        path, bad = rules.harness_path("glm-code", "{home}/skills", homes)
        self.assertEqual(bad, "")
        self.assertEqual(path, self.home / ".claude-glm" / "skills")

    def test_no_machine_key_refuses_by_words(self):
        homes = rules.machine_homes(str(self.conf))
        path, bad = rules.harness_path("glm-code", "{home}/skills", homes, str(self.conf))
        self.assertIsNone(path, "путь с неподставленным {home} ушёл в раскладку")
        self.assertIn("вписать home в секцию [glm-code]", bad)
        self.assertIn(str(self.conf), bad, "отказ не назвал файл, куда вписывать")

    def test_literal_path_needs_no_machine_key(self):
        # Профиль claude-code пишет пути литералами, и миграции у машинных
        # конфигов от этой оси нет.
        path, bad = rules.harness_path("claude-code", "~/.claude/skills", {})
        self.assertEqual((path, bad), (self.home / ".claude" / "skills", ""))
        self.assertEqual(rules.harness_path("claude-code", "", {}), (None, ""))

    def test_one_file_two_harnesses(self):
        same = [("claude-code", "CLAUDE.md", "@AGENTS.md\n", "режим import"),
                ("glm-code", "CLAUDE.md", "@AGENTS.md\n", "режим import")]
        keep, findings = rules.one_text_per_file(same)
        # Совпавший текст это штатная раскладка второй подписки: пишет файл
        # один харнес, второй застаёт его уже совпавшим.
        self.assertEqual((keep, findings), (["claude-code"], []))
        other = same[:1] + [("glm-code", "CLAUDE.md", "@СВОЁ.md\n", "своя строка импорта")]
        keep, findings = rules.one_text_per_file(other)
        self.assertEqual(keep, [], "файл со спором двух харнесов всё равно пишется")
        self.assertEqual(len(findings), 1, findings)
        self.assertIn("разного текста", findings[0])
        self.assertIn("своя строка импорта", findings[0], "находка не назвала, чем харнесы разошлись")


class CoreBudgetTest(unittest.TestCase):
    """Разрез ядра: за резидентную часть правил платит каждый запрос каждой
    сессии, поэтому бюджет тут не пожелание, а проверка. Гоняется на настоящих
    файлах devkit, а не на копии: мерить надо тот текст, который реально доедет.
    """

    def test_core_files_are_cut(self):
        for f in ("RULES.core.md", "RULES.board.core.md"):
            self.assertTrue((DEVKIT_SRC / f).is_file(),
                            "нет %s: резидентного ядра правил не нарезано" % f)

    def test_core_budgets(self):
        core = len(read(DEVKIT_SRC / "RULES.core.md"))
        board = len(read(DEVKIT_SRC / "RULES.board.core.md"))
        self.assertLessEqual(core, 5500, "ядро правил длиннее бюджета 5500 символов: %d" % core)
        self.assertLessEqual(board, 1500,
                             "ядро правил доски длиннее бюджета 1500 символов: %d" % board)

    def test_core_does_not_copy_the_full_text(self):
        # Ядро и полный текст никогда не лежат в контексте вместе, но
        # переписанный под одну строку пункт легко подменить копипастой из
        # полного текста, и тогда ядро растёт молча. Сравниваются предложения, а
        # не строки: перенос строки у этих файлов разный, и построчное сравнение
        # пропустило бы копию.
        bad = []
        for core, full in (("RULES.core.md", "RULES.md"),
                           ("RULES.board.core.md", "RULES.board.md")):
            whole = " ".join(read(DEVKIT_SRC / full).split())
            for sent in " ".join(read(DEVKIT_SRC / core).split()).split(". "):
                if len(sent) >= 60 and sent in whole:
                    bad.append("%s -> %s: %s" % (core, full, sent[:80]))
            if "`%s`" % full not in read(DEVKIT_SRC / core):
                bad.append("%s не называет %s: разбор пункта искать негде" % (core, full))
        self.assertEqual(bad, [], "ядро дублирует полный текст правил")

    def test_thin_file_is_built_for_the_cut_core(self):
        work = Path(tempfile.mkdtemp(prefix="devkit-cut-"))
        self.addCleanup(shutil.rmtree, str(work), True)
        cc = harness.parse("claude-code.toml",
                           read(DEVKIT_SRC / "kit" / "harness" / "claude-code.toml"))
        proj = work / "proj"
        (proj / "docs").mkdir(parents=True)
        for board in (True, False):
            tasks = proj / "docs" / "TASKS.md"
            if board:
                write(tasks, "")
            elif tasks.exists():
                tasks.unlink()
            fact = rules.actual_depth(str(DEVKIT_SRC), str(proj), board,
                                      rules.declared_depth(cc)[0])
            self.assertEqual(fact, rules.DEPTH_CORE, "ядро нарезано, а доехала глубина %s" % fact)
            thin = rules.thin_text(cc, str(proj), str(DEVKIT_SRC), board, False, fact)
            # Ядро общих правил в тонком файле не выписывается: его везёт
            # глобальная точка машины, и вторая копия того же текста стоила бы
            # проекту контекста ни за что (DK-193, решение 3).
            self.assertNotIn("RULES.core.md", thin)
            self.assertNotIn("RULES.md\n", thin, "в тонкий файл уехал полный текст правил")
            self.assertEqual("@.devkit/devkit/RULES.board.core.md\n" in thin, board, thin)
            self.assertNotIn("RULES.board.md\n", thin,
                             "в тонкий файл уехал полный текст правил доски")


class LayoutTest(unittest.TestCase):
    """Боевая пара раскладок для стенда послушания: их собирает генератор, а не
    рука, иначе стенд сравнивал бы не то, что доезжает до сессии.
    """

    def setUp(self):
        self.work = Path(tempfile.mkdtemp(prefix="devkit-layout-"))
        self.addCleanup(shutil.rmtree, str(self.work), True)
        self.rules_cli = DEVKIT_SRC / "tools" / "devkitctl" / "rules.py"
        self.project = DEVKIT_SRC / "tools" / "obeycheck" / "testdata" / "project"
        # Раскладка забирает в стенд и глобальную точку правил, а ищет её в
        # HOME. Свой HOME с точкой, сгенерированной тем же генератором: без него
        # тест мерил бы раскладку машины, на которой запущен, и на голой машине
        # («в раскладке нет глобальной точки правил») краснел бы.
        self.home = self.work / "home"
        (self.home / ".claude").mkdir(parents=True)
        prof = harness.parse("p.toml", read(DEVKIT_SRC / "kit" / "harness" / "claude-code.toml"))
        with fake_home(self.home):
            write(self.home / ".claude" / "CLAUDE.md",
                  rules.global_thin_text(prof, str(DEVKIT_SRC)))

    def test_pair_of_layouts(self):
        lay = self.work / "lay"
        for d in ("full", "core"):
            rc, out = run([PY, str(self.rules_cli), "--layout", d, str(lay / d), str(self.project)],
                          home=self.home)
            self.assertEqual(rc, 0, "раскладка глубины %s не собралась: %s" % (d, out))
        # Ядро общих правил приезжает в сессию глобальной точкой, и в раскладке
        # оно лежит под home/, а не рядом с тонким файлом: тонкий файл его не
        # зовёт вовсе (DK-193, решение 3).
        self.assertFalse((lay / "core" / "RULES.core.md").exists(),
                         "ядро общих правил лежит рядом с тонким файлом, а он его не зовёт")
        self.assertTrue((lay / "core" / "RULES.board.core.md").is_file(),
                        "в раскладке ядра нет RULES.board.core.md")
        self.assertFalse((lay / "core" / "RULES.md").exists(),
                         "в раскладку ядра уехал полный текст правил")
        self.assertTrue((lay / "full" / "RULES.md").is_file(),
                        "в раскладке полного текста нет RULES.md")
        self.assertTrue((lay / "full" / "home" / ".claude" / "CLAUDE.md").is_file(),
                        "в раскладке нет глобальной точки правил")
        # Правила, доехавшие дважды, стенд считал бы за одну раскладку:
        # глобальная точка тянет тот же текст, что и тонкий файл, и в раскладке
        # он лежит одной копией.
        self.assertFalse((lay / "core" / "home" / ".claude" / "CLAUDE_RULES.md").exists(),
                         "текст правил лёг в раскладку вторым экземпляром через глобальную точку")
        self.assertEqual(self.dangling_imports(lay), [],
                         "импорты раскладки не разворачиваются внутри неё")

    @staticmethod
    def dangling_imports(root):
        # Раскладка едет в чужой проект целиком, и путь наружу из неё не
        # развернётся: правила молча не доедут, а стенд этого не заметит.
        bad = []
        for lay in sorted(Path(root).iterdir()):
            for f in sorted(lay.rglob("*.md")):
                for ln in read(f).split("\n"):
                    ln = ln.strip()
                    if not ln.startswith("@") or " " in ln or len(ln) < 2:
                        continue
                    spec = ln[1:]
                    if spec.startswith("~/"):
                        target = lay / "home" / spec[2:]
                    elif os.path.isabs(spec):
                        target = Path(spec)
                    else:
                        target = f.parent / spec
                    if not target.is_file():
                        bad.append("%s: %s" % (f, ln))
        return bad

    def test_global_point_naming_the_core_directly(self):
        # Та же пара, но с глобальной точкой, которая называет ядро прямо
        # (сегодняшний сгенерированный вид, а не вчерашний симлинк на полный
        # текст). На «full» в раскладке уже лежит RULES.md, и ядро от
        # глобальной точки снимается дедупом как тот же текст; на «core» ядро
        # приезжает только ей, и снимать его нечем и незачем. Своя копия ядра
        # под glhome: импорт обязан быть путём от ~, а сам devkit почти
        # наверняка снаружи любого синтетического home этого теста.
        glhome = self.work / "glhome"
        (glhome / ".claude").mkdir(parents=True)
        (glhome / "nested-devkit").mkdir(parents=True)
        shutil.copy(str(DEVKIT_SRC / "RULES.core.md"), str(glhome / "nested-devkit" / "RULES.core.md"))
        write(glhome / ".claude" / "CLAUDE.md",
              "<!-- devkit:generated body=stub -->\nэталон не нужен, только импорт\n\n"
              "@~/nested-devkit/RULES.core.md\n")
        for d in ("full", "core"):
            out_dir = self.work / "gllay" / d
            rc, out = run([PY, str(self.rules_cli), "--layout", d, str(out_dir), str(self.project)],
                          home=glhome)
            self.assertEqual(rc, 0, "раскладка глубины %s с прямым импортом ядра не собралась: %s"
                             % (d, out))
            stray = list((out_dir / "home").rglob("RULES.core.md"))
            self.assertEqual(len(stray), 0 if d == "full" else 1,
                             "ядро от глобальной точки в раскладке %s легло не по разу: %s"
                             % (d, stray))
            self.assertFalse((out_dir / "RULES.core.md").exists(),
                             "ядро легло рядом с тонким файлом, а тонкий файл его не зовёт")


class GlobalPointDoctorTest(SandboxCase):
    """Глобальная точка правил значит для любой сессии на машине, а не только
    для проекта, поэтому проверяется отдельным блоком, не внутри цепочки находок
    конкретного проекта. Фикстура после каждого теста возвращается в чистое
    состояние.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.gclaude = cls.box.home / ".claude" / "CLAUDE.md"
        cls.clean = read(cls.gclaude)
        # Пути в находках доктор печатает разрешёнными, а mktemp на macOS отдаёт
        # /var, который на деле симлинк на /private/var.
        cls.dk_resolved = os.path.realpath(str(cls.box.dk))

    def setUp(self):
        super().setUp()
        write(self.gclaude, self.clean)

    def stale(self):
        prof = harness.parse("p.toml", read(self.box.dk / "kit" / "harness" / "claude-code.toml"))
        write(self.gclaude, rules.global_thin_text(prof, "/nowhere/stale-devkit"))

    def test_missing_point_is_generated(self):
        self.gclaude.unlink()
        _, out = self.box.doctor(self.proj)
        self.assertIn_("нет %s: правила харнеса claude-code до сессий вне проектов devkit "
                       "не доезжают" % self.gclaude, out,
                       "нет находки про пропавшую глобальную точку правил")
        self.assertFalse(self.gclaude.exists(), "doctor без --fix сам сгенерил глобальную точку")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: написана глобальная точка правил харнеса claude-code: %s"
                       % self.gclaude, out, "--fix не сгенерил пропавшую глобальную точку")
        self.assertEqual(read(self.gclaude), self.clean,
                         "сгенерированная глобальная точка разошлась с эталоном")
        self.assertRegex(first_line(self.gclaude), MARKER,
                         "у глобальной точки нет маркера с хешем")

    def test_point_imports_devkit_rules(self):
        # $dk тут без нарезанного ядра, и глобальная точка тянет то же, что
        # тянула бы и обычная нарезка без ядра: полный текст.
        text = read(self.gclaude)
        self.assertIn("@%s/RULES.md" % self.dk_resolved, text,
                      "глобальная точка не импортирует правила devkit")
        self.assertNotIn("AGENTS.md", text,
                         "глобальная точка тянет AGENTS.md, а он не её, а проектный")

    def test_handwritten_point_is_not_touched(self):
        # Правлена руками, маркера нет: генератор не трогает, локальное
        # предлагается перенести в RULES.local.md.
        write(self.gclaude, "# моя редакция\n\n@~/.claude/CLAUDE_RULES.md\n")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("%s без маркера devkit:generated, генератор его не трогает" % self.gclaude,
                       out, "нет находки про правленную руками глобальную точку")
        self.assertIn_("mv %s %s.bak" % (self.gclaude, self.gclaude), out,
                       "находка не называет точную команду перекладки файла")
        self.assertIn_("и повторить devkitctl doctor --fix", out,
                       "находка не зовёт повторить doctor --fix")
        self.assertIn("моя редакция", read(self.gclaude), "--fix затёр глобальную точку")

    def test_body_diverged_from_the_hash(self):
        # Маркер на месте, а тело под ним поправили руками: находка другая (тело
        # разошлось с хешем, а не «маркера нет»), и защита та же.
        write(self.gclaude, self.clean + "\nсвоя строка\n")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("%s правлен руками, содержимое разошлось с хешем маркера" % self.gclaude,
                       out, "нет находки про разошедшийся с хешем маркер глобальной точки")
        self.assertIn_("mv %s %s.bak" % (self.gclaude, self.gclaude), out,
                       "находка не называет точную команду перекладки файла")
        self.assertIn_("и повторить devkitctl doctor --fix", out,
                       "находка не зовёт повторить doctor --fix")
        self.assertIn("своя строка", read(self.gclaude),
                      "--fix затёр глобальную точку с разошедшимся хешем маркера")

    def test_stale_point_is_regenerated(self):
        # Маркер есть и сходится со своим телом, а тело устарело (девкит будто
        # переехал): находка другая, «устарел», и --fix перегенерирует под
        # текущий путь, а не оставляет старый.
        self.stale()
        _, out = self.box.doctor(self.proj)
        self.assertIn_("%s устарел: путь devkit или состав ядра изменились" % self.gclaude, out,
                       "нет находки про устаревшую глобальную точку")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: глобальная точка правил %s переписана" % self.gclaude, out,
                       "--fix не перегенерил устаревшую глобальную точку")
        self.assertEqual(read(self.gclaude), self.clean,
                         "перегенерированная глобальная точка разошлась с эталоном")

    def test_import_does_not_expand(self):
        # Девкит из-под глобальной точки убрали (переехал или не склонирован),
        # находка про это, а не тишина.
        grules = self.box.dk / "RULES.md"
        keep = self.box.root / "RULES.md.bak"
        shutil.move(str(grules), str(keep))
        try:
            _, out = self.box.doctor(self.proj)
            self.assertIn_("%s:7: импорт @%s/RULES.md не разворачивается"
                           % (self.gclaude, self.dk_resolved), out,
                           "нет находки про неразворачивающийся импорт глобальной точки")
        finally:
            shutil.move(str(keep), str(grules))
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 0, "doctor нашёл находки после восстановления devkit: %s" % out)


class ThinFilesTest(SandboxCase):
    """Файлы правил: рукописный AGENTS.md источник, тонкие файлы харнесов
    генерятся. Гоняется на своём проекте, чтобы правки --fix не мешали прежним
    шагам.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("rproj")
        cls.box.dkctl_run("new", "--prefix", "RP", "-C", str(cls.proj))

    def test_1_board_makes_the_thin_file_stale(self):
        # taskctl в PATH заглушка, доски она не заводит: кладём её руками, и
        # тонкий файл от этого устаревает, ему не хватает правил доски.
        write(self.proj / "docs" / "TASKS.md", "# Задачи\n")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("CLAUDE.md устарел", out, "появление доски не сделало тонкий файл устаревшим")
        self.assertNotIn("RULES.board.md", read(self.proj / "CLAUDE.md"),
                         "doctor без --fix сам перегенерил тонкий файл")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: CLAUDE.md перегенерирован", out,
                       "--fix не перегенерил устаревший файл")
        self.assertIn("\n@.devkit/devkit/RULES.board.md\n", "\n" + read(self.proj / "CLAUDE.md"),
                      "в перегенерённом файле нет правил доски")
        _, out = self.box.doctor(self.proj)
        self.assertNotRegex(without_pockets(out), r"CLAUDE\.md|AGENTS\.md",
                            "после перегенерации остались находки по правилам")

    def test_2_handwritten_thin_file(self):
        # Генератор файл не перетирает даже под --fix, а зовёт перенести правку
        # в AGENTS.md. Иначе чужой текст пропал бы молча.
        write(self.proj / "CLAUDE.md", read(self.proj / "CLAUDE.md") + "своя строка\n")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("CLAUDE.md правлен руками", out,
                       "нет находки про правленый руками тонкий файл")
        self.assertIn("своя строка", read(self.proj / "CLAUDE.md"),
                      "--fix затёр правку в тонком файле")
        (self.proj / "CLAUDE.md").unlink()
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: CLAUDE.md сгенерирован", out,
                       "--fix не сгенерил пропавший тонкий файл")

    def test_3_move_to_agents_md(self):
        # Проект, подключённый до переезда на AGENTS.md: рукописный CLAUDE.md
        # остаётся нетронутым, а доктор подсказывает переезд. Автоматом его не
        # делают: в живых CLAUDE.md бывают локальные импорты, автоматике их не
        # разобрать.
        hproj = git_init(self.box.root / "hproj")
        write(hproj / "CLAUDE.md", "# Старый проект\n\n@../devkit/RULES.md\n")
        _, out = self.box.doctor(hproj, "--fix")
        self.assertIn_("git mv CLAUDE.md AGENTS.md", out, "нет подсказки про переезд на AGENTS.md")
        self.assertFalse((hproj / "AGENTS.md").exists(), "--fix перенёс рукописный файл сам")
        self.assertIn("@../devkit/RULES.md", read(hproj / "CLAUDE.md"),
                      "--fix тронул рукописный CLAUDE.md")
        shutil.move(str(hproj / "CLAUDE.md"), str(hproj / "AGENTS.md"))
        _, out = self.box.doctor(hproj)
        self.assertIn_("AGENTS.md:3: строка импорта @../devkit/RULES.md", out,
                       "импорт, переехавший в AGENTS.md, не назван находкой")
        _, out = self.box.doctor(hproj, "--fix")
        self.assertIn_("починено: CLAUDE.md сгенерирован", out,
                       "--fix не сгенерил тонкий файл после переезда")
        write(hproj / "AGENTS.md", "# Старый проект\n")
        _, out = self.box.doctor(hproj)
        self.assertNotIn_("строка импорта", out, "находка про импорт осталась после его удаления")
        self.assertNotRegex(without_pockets(out), r"CLAUDE\.md|AGENTS\.md",
                            "после переезда остались находки по правилам")


class EmbedTest(SandboxCase):
    """Вклейка правил у инструмента, который импортов не понимает. Проверки идут
    цепочкой по одному проекту, как их гонял sh-раннер: каждый следующий шаг
    стоит на раскладке, которую оставил предыдущий, поэтому в именах порядковый
    номер, а стенд объявлен цепочкой. Дальше класса это не течёт: копия devkit у
    каждого класса своя.
    """

    CHAIN = True

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("rproj", board=True)
        cls.box.dkctl_run("new", "--prefix", "RP", "-C", str(cls.proj))
        cls.box.doctor(cls.proj, "--fix")
        cls.agents = cls.proj / "AGENTS.md"
        cls.thin = cls.proj / "CLAUDE.md"
        cls.profile = cls.box.dk / "kit" / "harness" / "embed-tool.toml"
        write(cls.profile, EMBED_TOOL)
        write(cls.box.home / ".devkit" / "harness.local",
              'enabled = ["claude-code", "embed-tool"]\n')

    def test_1_rules_are_embedded(self):
        _, out = self.box.doctor(self.proj)
        self.assertIn_("нет вклейки правил", out, "нет находки про недостающую вклейку")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: вклейка правил добавлена в AGENTS.md", out, "--fix не вклеил правила")
        self.assertIn_("починено: CLAUDE.md перегенерирован", out,
                       "тонкий файл не перегенерён под вклейку")
        text = read(self.agents)
        self.assertRegex(text, EMBED_MARKER, "у вклейки нет маркера начала с двумя хешами")
        self.assertIn("\n<!-- devkit:rules end -->\n", text, "у вклейки нет маркера конца")
        self.assertIn("Правила работы с ассистентом", text, "во вклейке нет текста RULES.md")
        self.assertIn("Правила проектов с доской", text, "во вклейке нет правил доски")
        self.assertEqual(len([ln for ln in read(self.thin).split("\n") if ln.startswith("@")]), 1,
                         "при вклейке тонкий файл всё ещё ходит в devkit")
        _, out = self.box.doctor(self.proj)
        self.assertNotRegex(without_pockets(out), r"вклейка|CLAUDE\.md",
                            "после вклейки остались находки по правилам")

    def test_2_handwritten_embed_is_not_overwritten(self):
        keep = read(self.agents)
        write(self.agents, keep.replace("<!-- devkit:rules end -->",
                                                     "своя правка\n<!-- devkit:rules end -->"))
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("вклейку правил в AGENTS.md правили руками", out, "тронутая вклейка не названа")
        self.assertIn("своя правка", read(self.agents), "--fix затёр правку внутри маркеров")
        write(self.agents, keep)

    def test_3_devkit_rules_changed(self):
        # Правила devkit обновились: src разошёлся, body цел, вклейка
        # перегенерируется.
        board = self.box.dk / "RULES.board.md"
        write(board, read(board) + "\nновая строка правил доски\n")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("вклейка правил в AGENTS.md протухла против devkit", out,
                       "протухшая вклейка не названа")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: вклейка правил в AGENTS.md обновлена под devkit", out,
                       "--fix не обновил протухшую вклейку")
        self.assertIn("новая строка правил доски", read(self.agents),
                      "обновлённая вклейка без новой строки правил")

    def test_4_pointers_wait_for_the_core(self):
        # Ось скиллов у embed-инструмента: discovery = "manual" значит, что к
        # ядру правил прикладывается таблица указателей. Ядра ещё нет:
        # указателям заменять нечего, рядом с полным текстом они были бы второй
        # копией тех же процедур.
        write(self.profile, EMBED_TOOL + '\n[skills]\ndiscovery = "manual"\n')
        before = read(self.agents)
        _, out = self.box.doctor(self.proj)
        self.assertNotRegex(without_pockets(out), r"вклейка|CLAUDE\.md",
                            "manual без нарезанного ядра сделал вклейку устаревшей: %s" % out)
        self.assertEqual(read(self.agents), before, "manual без нарезанного ядра тронул вклейку")
        self.assertNotIn("Процедуры devkit отдельными файлами", before,
                         "указатели приехали раньше ядра, рядом с полным текстом правил")
        self.assertNotIn("devkit:rules begin depth=", before,
                         "признак глубины уехал в маркер, хотя доехал полный текст")

    def test_5_core_brings_the_pointers(self):
        # Ядро нарезано: та же объявленная глубина теперь доезжает целиком, и
        # смена источников видна как протухание.
        write(self.box.dk / "RULES.core.md", "# Ядро правил\n\nстрока ядра\n")
        write(self.box.dk / "RULES.board.core.md", "# Ядро правил доски\n\nстрока ядра доски\n")
        _, out = self.box.doctor(self.proj)
        self.assertIn_("вклейка правил в AGENTS.md протухла против devkit", out,
                       "нарезанное ядро не сделало вклейку протухшей")
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: вклейка правил в AGENTS.md обновлена под devkit", out,
                       "--fix не переложил вклейку под указатели")
        text = read(self.agents)
        self.assertRegex(text, POINTERS_MARKER, "в маркере вклейки нет признака глубины")
        self.assertIn("\n## Процедуры devkit отдельными файлами\n", text,
                      "во вклейке нет таблицы указателей на скиллы")
        self.assertIn("`../devkit/kit/skills/board-batch/SKILL.md`", text,
                      "в таблице указателей нет пути до скилла")

    def test_6_every_skill_is_in_the_table(self):
        # Указатель это единственный вход к процедуре у инструмента без скиллов,
        # поэтому в таблице обязаны быть все, а не те, о ком генератор знал на
        # момент правки.
        text = read(self.agents)
        for skill in sorted((self.box.dk / "kit" / "skills").glob("*/SKILL.md")):
            name = skill.parent.name
            self.assertIn("`../devkit/kit/skills/%s/SKILL.md`" % name, text,
                          "в таблице указателей нет скилла %s" % name)
        self.assertIn("строка ядра доски", text,
                      "указатели приехали вместо текста ядра, а не вместе с ним")
        self.assertNotIn("Правила работы с ассистентом", text,
                         "под указателями остался полный текст правил вместо ядра")
        _, out = self.box.doctor(self.proj)
        self.assertNotRegex(without_pockets(out), r"вклейка|CLAUDE\.md",
                            "после перекладки под указатели остались находки")

    def test_7_axis_removed_brings_the_full_text_back(self):
        (self.box.dk / "RULES.core.md").unlink()
        (self.box.dk / "RULES.board.core.md").unlink()
        write(self.profile, EMBED_TOOL)
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: вклейка правил в AGENTS.md обновлена под devkit", out,
                       "--fix не вернул вклейку к полному тексту")
        self.assertNotIn("Процедуры devkit отдельными файлами", read(self.agents),
                         "таблица указателей осталась после снятия оси скиллов")
        self.assertRegex(read(self.agents), EMBED_MARKER,
                         "признак глубины остался в маркере при полной вклейке")

    def test_8_only_one_copy_in_the_tree(self):
        copy = self.proj / "docs" / "copy.md"
        write(copy, read(self.agents))
        _, out = self.box.doctor(self.proj)
        self.assertIn_("вклейка правил лежит не в одном файле", out, "две вклейки в дереве не названы")
        copy.unlink()
        # Маркеры в примере кода это документация, а не вторая копия.
        sample = self.proj / "docs" / "sample.md"
        write(sample, "пример:\n\n```\n<!-- devkit:rules begin src=aaaaaaaaaaaa body=bbbbbbbbbbbb -->\n"
                      "текст\n<!-- devkit:rules end -->\n```\n")
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("вклейка правил лежит не в одном файле", out,
                          "пример в блоке кода принят за вклейку")
        sample.unlink()

    def test_9_last_embed_tool_switched_off(self):
        # Вклейка убирается, импорты возвращаются. Это единственный случай,
        # когда --fix удаляет, и только целый блок со своим body.
        write(self.box.home / ".devkit" / "harness.local", 'enabled = ["claude-code"]\n')
        _, out = self.box.doctor(self.proj, "--fix")
        self.assertIn_("починено: вклейка правил убрана из AGENTS.md", out,
                       "--fix не убрал ненужную вклейку")
        self.assertIn_("починено: CLAUDE.md перегенерирован", out,
                       "тонкий файл не вернул импорты devkit")
        self.assertNotIn("devkit:rules", read(self.agents), "в AGENTS.md остались маркеры вклейки")
        self.assertIn("\n@.devkit/devkit/RULES.md\n", "\n" + read(self.thin),
                      "в тонком файле не вернулись правила devkit")
        (self.box.home / ".devkit" / "harness.local").unlink()


class ImportReachTest(unittest.TestCase):
    """Граница, по которой клиент решает, разворачивать импорт или пропустить.

    Замер 2026-08-08 (README devkitctl, раздел «Куда доезжают импорты правил»):
    разворачивается только путь, ведущий внутрь каталога самого файла, и
    считается это по записанному пути, а не по тому, куда он ведёт по диску.
    Логика поэтому лексическая и проверяется без файловой системы.
    """

    def setUp(self):
        self.thin = "/tmp/proj/CLAUDE.md"

    def test_1_inside_the_project(self):
        for spec in ("AGENTS.md", "./AGENTS.md", "docs/rules.md", "link/RULES.core.md",
                     "sub/../AGENTS.md", "/tmp/proj/docs/rules.md"):
            self.assertFalse(rules.import_escapes(self.thin, spec),
                             "импорт внутрь проекта принят за путь наружу: %s" % spec)

    def test_2_outside_the_project(self):
        for spec in ("../devkit/RULES.md", "../../devkit/RULES.md", "/etc/rules.md",
                     "~/projects/devkit/RULES.md", "sub/../../devkit/RULES.md"):
            self.assertTrue(rules.import_escapes(self.thin, spec),
                            "импорт наружу принят за путь внутрь проекта: %s" % spec)

    def test_3_neighbour_with_a_common_prefix(self):
        # Каталог-сосед, чьё имя начинается с имени проекта: сравнение идёт по
        # разделителю пути, а не по префиксу строки.
        self.assertTrue(rules.import_escapes(self.thin, "../proj-local/RULES.md"),
                        "сосед с общим префиксом имени принят за путь внутрь проекта")


class ImportDepthTest(unittest.TestCase):
    """Глубина, на которую клиент идёт по цепочке импортов: четыре ступени.

    Замером (README devkitctl, раздел «Куда доезжают импорты правил») из цепочки
    в семь файлов доехали четыре. Число это не украшение: по нему обход считает,
    что везёт глобальная точка, а от этого зависит третья находка доктора.
    Возьми глубину меньше, и текст, который до сессии доезжает, доктор объявит
    недоехавшим; возьми больше, и наоборот.
    """

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="devkitctl-hops-"))
        self.addCleanup(shutil.rmtree, str(self.tmp), True)
        # Точка, из неё цепочка на пять ступеней вглубь.
        write(self.tmp / "point.md", "# точка\n\n@h1.md\n")
        for i in range(1, 5):
            write(self.tmp / ("h%d.md" % i), "ступень %d\n@h%d.md\n" % (i, i + 1))
        write(self.tmp / "h5.md", "ступень 5\n")

    def names(self, **kw):
        return sorted(name for name, _ in rules.reachable_texts(self.tmp / "point.md", **kw))

    def test_1_four_hops_arrive_and_the_fifth_does_not(self):
        self.assertEqual(self.names(), ["h1.md", "h2.md", "h3.md", "h4.md"],
                         "обход прошёл цепочку не на замеренную глубину")

    def test_2_the_edge_moves_with_the_measured_depth(self):
        # Граница считается от точки, а не от корня дерева: на шаге глубины
        # уезжает ровно один файл с хвоста цепочки.
        self.assertEqual(self.names(hops=1), ["h1.md"], "шаг глубины считается не по ступеням")
        self.assertEqual(self.names(hops=5), ["h1.md", "h2.md", "h3.md", "h4.md", "h5.md"],
                         "лишняя ступень не доехала при увеличенной глубине")

    def test_3_the_point_itself_is_not_a_delivered_text(self):
        # Сам файл точки это не правила, а способ их позвать: в доехавшее он не
        # попадает, иначе находка сверялась бы с его собственным текстом.
        self.assertNotIn("point.md", self.names())


class ProjectImportsTest(SandboxCase):
    """Импорты файла правил проекта: полный текст вместо ядра и импорт, который
    лежит на диске, а до контекста не доезжает (DK-190).
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("iproj")
        cls.thin = cls.proj / "CLAUDE.md"

    def findings(self, text):
        write(self.thin, text)
        with fake_home(self.box.home):
            return rules.check_project_imports(str(self.proj), str(self.box.dk))

    def test_1_full_text_instead_of_the_core(self):
        core = self.box.dk / "RULES.core.md"
        write(core, "# ядро\n\nтекст ядра\n")
        try:
            found = self.findings("# правила\n\n@../devkit/RULES.md\n")
            self.assertTrue([f for f in found if "тянет полный текст правил" in f
                             and "RULES.core.md" in f and "CLAUDE.md:3" in f],
                            "импорт полного текста рядом с нарезанным ядром не назван: %s" % (found,))
        finally:
            core.unlink()

    def test_2_core_import_is_silent_about_the_full_text(self):
        core = self.box.dk / "RULES.core.md"
        write(core, "# ядро\n\nтекст ядра\n")
        try:
            found = self.findings("# правила\n\n@../devkit/RULES.core.md\n")
            self.assertFalse([f for f in found if "полный текст" in f],
                             "импорт ядра принят за полный текст: %s" % (found,))
        finally:
            core.unlink()

    def test_3_import_out_of_the_project_does_not_arrive(self):
        # Правила доски глобальная точка не везёт, и путь наружу их не довозит:
        # в сессию они не приезжают вовсе, а расчёт карманов считает их
        # резидентными. Молчать тут нельзя, разница видна только замером.
        found = self.findings("# правила\n\n@../devkit/RULES.board.md\n")
        self.assertTrue([f for f in found if "до контекста не доезжает" in f
                         and "RULES.board.md" in f],
                        "недоехавший импорт правил доски не назван: %s" % (found,))

    def test_4_what_the_global_point_brings_is_not_a_finding(self):
        # Тот же текст приезжает глобальной точкой, минуя файл проекта: импорт
        # наружу дублирует уже доехавшее, и находки тут нет.
        found = self.findings("# правила\n\n@../devkit/RULES.md\n")
        self.assertFalse([f for f in found if "до контекста не доезжает" in f],
                         "находка на импорт, который доезжает глобальной точкой: %s" % (found,))

    def test_5_import_inside_the_project_is_silent(self):
        write(self.proj / "docs" / "own.md", "своё\n")
        found = self.findings("# правила\n\n@AGENTS.md\n@docs/own.md\n")
        self.assertEqual(found, [], "импорт внутрь проекта дал находку")

    def test_6_missing_import_is_left_to_check_imports(self):
        # Про импорт, которому нечего разворачивать, говорит другая проверка, и
        # вторая находка на ту же строку была бы шумом.
        found = self.findings("# правила\n\n@../devkit/RULES.gone.md\n")
        self.assertEqual(found, [], "несуществующий импорт назван недоехавшим: %s" % (found,))

    def test_7_import_in_a_code_block_is_a_sample(self):
        found = self.findings("# правила\n\n```\n@../devkit/RULES.board.md\n```\n")
        self.assertEqual(found, [], "строка импорта в блоке кода принята за импорт: %s" % (found,))

    def test_9_generated_thin_file_of_a_board_project_arrives(self):
        # Гашение дыры DK-190: подключённый проект с доской зовёт правила через
        # ссылку, путь остаётся внутри проекта, и находки про недоехавший импорт
        # у него нет вовсе.
        proj = self.box.project("gproj", board=True)
        self.box.dkctl_run("new", "--prefix", "GP", "-C", str(proj))
        write(proj / ".devkit" / "deploy.local",
              "deploy = echo выкат\ntest = echo тесты\nautonomous = false\n")
        rc, out = self.box.doctor(proj)
        self.assertNotIn_("до контекста не доезжает", out,
                          "правила доски не доезжают до подключённого проекта")
        self.assertEqual(rc, 0, "доктор нашёл находки на свежем проекте с доской: %s" % out)
        self.assertIn("@.devkit/devkit/RULES.board.md\n", read(proj / "CLAUDE.md"))

    def test_8_doctor_names_both_defects_of_a_handwritten_file(self):
        # Стык с доктором: рукописный CLAUDE.md проекта, подключённого до
        # переезда на AGENTS.md, тянет полный текст правил доски, и не доезжает
        # ни то ни другое (случай it-road-course из разбора DK-190).
        core = self.box.dk / "RULES.board.core.md"
        write(core, "# ядро доски\n\nтекст ядра доски\n")
        write(self.thin, "# правила проекта\n\n@../devkit/RULES.board.md\n")
        try:
            _, out = self.box.doctor(self.proj)
            self.assertIn_("CLAUDE.md:3: импорт @../devkit/RULES.board.md тянет полный текст", out,
                           "доктор не назвал импорт полного текста вместо ядра")
            self.assertIn_("CLAUDE.md:3: импорт @../devkit/RULES.board.md есть на диске, "
                           "а до контекста не доезжает", out,
                           "доктор не назвал недоехавший импорт")
        finally:
            core.unlink()
            self.thin.unlink()


if __name__ == "__main__":
    unittest.main()
