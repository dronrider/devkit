#!/usr/bin/env python3
"""Раскладка devkit: белый список верхнего уровня, место кода, граница sh и
сторож, сверяющий список из кода с разделом «Раскладка» README (DK-144).

Проверка гоняется на синтетическом чекауте: разложенный по правилу молчит, а
подложенное краснеет. Живое дерево тут не годится, оно меняется каждой задачей,
и тест по нему говорил бы про сегодняшнее состояние, а не про правило.
"""
import unittest

import layout
from testenv import DEVKIT_SRC, SandboxCase, git, git_init, read, write

SH_OK = "#!/bin/sh\nexec python3 \"$@\"\n"
SH_FUNC = "#!/bin/sh\nrun() {\n  echo x\n}\nrun\n"
SH_BIG = "#!/bin/sh\n" + "echo x\n" * 120
SH_HUGE = "#!/bin/sh\n" + "echo x\n" * 150


def fake_devkit(root):
    """Синтетический чекаут devkit, разложенный по правилу."""
    git_init(root)
    write(root / "RULES.core.md", "# Ядро правил\n")
    write(root / "RULES.md", "# Правила\n")
    write(root / "README.md", "# devkit\n")
    write(root / "AGENTS.md", "# agents\n")
    write(root / "CLAUDE.md", "@AGENTS.md\n")
    write(root / "CONNECT.md", "# подключение\n")
    write(root / "RANKING.md", "# ранг\n")
    write(root / ".gitignore", ".devkit/*.local\n")
    write(root / ".github" / "workflows" / "ci.yml", "name: ci\n")
    write(root / "tools" / "taskctl" / "go.mod", "module taskctl\n")
    write(root / "tools" / "taskctl" / "ops.go", "package main\n")
    write(root / "tools" / "taskctl" / "testdata" / "board.md", "# Задачи\n")
    write(root / "tools" / "devkitctl" / "devkitctl.py", "print(1)\n")
    write(root / "tools" / "devkitctl" / "README.md", "# devkitctl\n")
    write(root / "tools" / "obeycheck" / "go.mod", "module obeycheck\n")
    write(root / "tools" / "obeycheck" / "testdata" / "test_tool.py", "print(1)\n")
    write(root / "tools" / "obeycheck" / "testdata" / "fake-agent.sh", SH_FUNC)
    write(root / "internal" / "go.mod", "module internal\n")
    write(root / "internal" / "frame" / "capture.go", "package frame\n")
    write(root / "kit" / "skills" / "check-skills.py", "print(1)\n")
    write(root / "kit" / "skills" / "check_skills_test.py", "print(1)\n")
    write(root / "kit" / "skills" / "goal-loop" / "SKILL.md", "---\nname: goal-loop\n---\n")
    write(root / "kit" / "skills" / "goal-loop" / "goal-run.py", "print(1)\n")
    write(root / "kit" / "harness" / "claude-code.toml", "[detect]\n")
    write(root / "kit" / "harness" / "testdata" / "min.toml", "[detect]\n")
    write(root / "kit" / "harness" / "snap" / "claude-code.sh", SH_HUGE)
    write(root / "hooks" / "pre-commit", SH_OK)
    write(root / "hooks" / "quota-refresh.sh", SH_OK)
    write(root / "hooks" / "check-symbols.py", "print(1)\n")
    write(root / "docs" / "TASKS.md", "# Задачи\n")
    write(root / "docs" / "tasks" / "DK-1-check.sh", SH_FUNC)
    write(root / "docs" / "lld" / "DK-1.md", "# LLD\n")
    git(root, "add", "-A")
    return root


class LayoutTest(SandboxCase):
    """Синтетический чекаут: чистый молчит, подложенное краснеет."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.dk = fake_devkit(cls.box.root / "fakedevkit")

    def plant(self, rel, text):
        """Файл под git: проверка ходит по ls-files, положенного мимо не видит."""
        write(self.dk / rel, text)
        git(self.dk, "add", "-A")
        return rel

    def drop(self, rel):
        (self.dk / rel).unlink()
        git(self.dk, "add", "-A")

    def test_clean_tree_is_silent(self):
        self.assertEqual(layout.check(self.dk), [],
                         "раскладка по правилу дала находки: %s" % layout.check(self.dk))

    def test_planted_files_are_named_with_the_rule(self):
        # Три подлога из сценария DK-144, по находке на каждый, и каждая находка
        # называет правило, а не только факт.
        planted = (self.plant("foo.go", "package main\n"),
                   self.plant("kit/build.sh", SH_BIG),
                   self.plant("scratch/note.md", "# заметка\n"))
        try:
            found = layout.check(self.dk)
        finally:
            for rel in planted:
                self.drop(rel)
        self.assertEqual(len(found), 3, "находок %d, а подлогов три: %s" % (len(found), found))
        self.assertIn("foo.go: %s" % layout.TOOL_CODE, found)
        self.assertIn("scratch/: %s" % layout.LOOSE, found)
        self.assertTrue([f for f in found if f.startswith("kit/build.sh: %s" % layout.SH_ONLY)],
                        "sh в kit/ не назван правилом: %s" % found)

    def test_exceptions_are_silent(self):
        # Съёмщик квоты на полторы сотни строк, чужой код в testdata/, сценарий
        # проверки задачи с функциями и общая фикстура харнесов лежат в чистом
        # чекауте: молчание по ним проверяет test_clean_tree_is_silent, а тут
        # проверяется, что молчат они по своим правилам, а не по случайности.
        for rel in ("kit/harness/snap/claude-code.sh", "docs/tasks/DK-1-check.sh"):
            self.assertEqual(layout.sh_findings(self.dk, rel), [],
                             "исключение %s посчитано находкой" % rel)
        # В testdata/ лежит чужой код, и мерить его нашим правилом неверно: у
        # obeycheck инструмент go-шный, а фикстура синтетического проекта
        # написана на python, и «одним языком» она инструмент не делает.
        planted = (self.plant("tools/obeycheck/testdata/big.sh", SH_HUGE),
                   self.plant("kit/harness/testdata/gen.py", "print(1)\n"))
        try:
            found = layout.check(self.dk)
        finally:
            for rel in planted:
                self.drop(rel)
        self.assertEqual(found, [], "проверка полезла в testdata/: %s" % found)

    def test_sh_shape_in_an_allowed_place(self):
        # В своём месте sh тоже мерится: сотня строк и своя функция это уже
        # программа, и место ей в python.
        big = self.plant("kit/skills/goal-loop/run.sh", SH_BIG)
        func = self.plant("hooks/helper.sh", SH_FUNC)
        try:
            found = layout.check(self.dk)
        finally:
            self.drop(big)
            self.drop(func)
        self.assertTrue([f for f in found if f.startswith("%s: %s (строк 121" % (big, layout.SH_BIG))],
                        "длинный sh в своём месте не назван: %s" % found)
        self.assertIn("%s: %s (своя функция)" % (func, layout.SH_BIG), found)

    def test_shebang_counts_as_sh(self):
        # Три точки входа git расширения не имеют, и проверка по одному лишь
        # *.sh не увидела бы именно тех файлов, ради которых правило пишет
        # исключение.
        rel = self.plant("kit/runner", SH_OK)
        try:
            found = layout.check(self.dk)
        finally:
            self.drop(rel)
        self.assertIn("kit/runner: %s" % layout.SH_ONLY, found)

    def test_skill_shell_lives_next_to_its_skill_md(self):
        # Оболочка скилла лежит ровно в kit/skills/<имя>/, рядом со своим
        # SKILL.md. Пока проверка смотрела на префикс kit/skills/, все четыре
        # подлога проходили молча: и файл прямо в каталоге скиллов, и файл
        # подкаталогом глубже оболочки.
        py_flat = self.plant("kit/skills/random-helper.py", "print(1)\n")
        sh_flat = self.plant("kit/skills/random.sh", SH_OK)
        py_deep = self.plant("kit/skills/goal-loop/sub/deep.py", "print(1)\n")
        sh_deep = self.plant("kit/skills/goal-loop/sub/deep.sh", SH_OK)
        try:
            found = layout.check(self.dk)
        finally:
            for rel in (py_flat, sh_flat, py_deep, sh_deep):
                self.drop(rel)
        self.assertEqual(len(found), 4, "находок %d, а подлогов четыре: %s" % (len(found), found))
        for rel in (py_flat, py_deep):
            self.assertIn("%s: %s" % (rel, layout.TOOL_CODE), found)
        for rel in (sh_flat, sh_deep):
            self.assertIn("%s: %s" % (rel, layout.SH_ONLY), found)

    def test_selfcheck_exception_is_closed(self):
        # Самопроверка скиллов и её тест названы поимённо: соседний python в том
        # же каталоге исключением не становится. Молчание по самой самопроверке
        # проверяет чистое дерево, тут проверяется, что список закрытый.
        self.assertEqual(layout.lang_rule("kit/skills/check-skills.py"), "",
                         "точечное исключение под самопроверку скиллов не работает")
        self.assertEqual(layout.lang_rule("kit/skills/check_helper.py"), layout.TOOL_CODE,
                         "исключение под самопроверку накрыло соседний файл")

    def test_material_directories(self):
        go_in_skill = self.plant("kit/skills/goal-loop/gen.go", "package main\n")
        try:
            found = layout.check(self.dk)
        finally:
            self.drop(go_in_skill)
        self.assertIn("%s: %s" % (go_in_skill, layout.MATERIAL), found,
                      "место оболочки скилла пустило в kit/ ещё и go")
        go_in_kit = self.plant("kit/agents/gen.go", "package main\n")
        py_in_docs = self.plant("docs/lld/plot.py", "print(1)\n")
        try:
            found = layout.check(self.dk)
        finally:
            self.drop(go_in_kit)
            self.drop(py_in_docs)
        self.assertIn("%s: %s" % (go_in_kit, layout.MATERIAL), found)
        self.assertIn("%s: %s" % (py_in_docs, layout.MATERIAL), found)

    def test_one_language_per_tool(self):
        mixed = self.plant("tools/taskctl/plot.py", "print(1)\n")
        loose = self.plant("tools/helper.sh", SH_OK)
        try:
            found = layout.check(self.dk)
        finally:
            self.drop(mixed)
            self.drop(loose)
        self.assertTrue([f for f in found if f.startswith("tools/taskctl: %s" % layout.ONE_LANG)],
                        "инструмент на двух языках не найден: %s" % found)
        self.assertTrue([f for f in found if f.startswith("tools/helper.sh: %s" % layout.ONE_LANG)],
                        "файл в tools/ мимо директории инструмента не найден: %s" % found)

    def test_internal_is_go_only(self):
        # internal/ завёл DK-237 под общий go-модуль каркаса: go там лежит как у
        # себя дома (молчание чистого дерева это и проверяет), а python в нём
        # находка, потому что каркас go-шный.
        self.assertEqual(layout.lang_rule("internal/frame/capture.go"), "",
                         "go в internal/ посчитан нарушением")
        planted = self.plant("internal/frame/helper.py", "print(1)\n")
        try:
            found = layout.check(self.dk)
        finally:
            self.drop(planted)
        self.assertIn("%s: %s" % (planted, layout.TOOL_CODE), found,
                      "python в internal/ прошёл молча: %s" % found)

    def test_untracked_file_is_not_a_finding(self):        # На живом дереве лежат __pycache__, собранные бинари и рабочие файлы
        # worktree: проверка по диску краснела бы на чистом репозитории каждый
        # день.
        write(self.dk / "taskctl.bin.go", "package main\n")
        try:
            self.assertEqual(layout.check(self.dk), [], "проверка пошла по диску, а не по git")
        finally:
            (self.dk / "taskctl.bin.go").unlink()

    def test_foreign_project_is_left_alone(self):
        # В чужом проекте раскладка своя, и правило туда не едет.
        alien = git_init(self.box.root / "alien")
        write(alien / "main.go", "package main\n")
        write(alien / "app.py", "print(1)\n")
        git(alien, "add", "-A")
        self.assertFalse(layout.is_devkit(alien), "чужой проект принят за devkit")
        self.assertEqual(layout.check(alien), [], "правило раскладки уехало в чужой проект")


class LayoutCommandTest(SandboxCase):
    """Форма прогона: `doctor --layout` печатает только находки раскладки и
    ставит код выхода по ним. Полный доктор смотрит на HOME, бинари, tmux и
    снимок квоты, и шаг CI на нём не завёлся бы.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.dk = fake_devkit(cls.box.root / "cmddevkit")

    def test_clean_tree_exits_zero(self):
        rc, out = self.box.dkctl_run("doctor", "--layout", "-C", str(self.dk))
        self.assertEqual(rc, 0, "чистая раскладка дала код %d: %s" % (rc, out))
        self.assertIn_("раскладка в порядке", out, "нет строки про чистую раскладку")
        self.assertNotIn_("машина:", out, "в прогоне раскладки всплыла машинная обвязка")

    def test_finding_exits_one(self):
        write(self.dk / "foo.go", "package main\n")
        git(self.dk, "add", "-A")
        try:
            rc, out = self.box.dkctl_run("doctor", "--layout", "-C", str(self.dk))
        finally:
            (self.dk / "foo.go").unlink()
            git(self.dk, "add", "-A")
        self.assertEqual(rc, 1, "находка раскладки не дала кода 1: %s" % out)
        self.assertIn_("foo.go: %s" % layout.TOOL_CODE, out, "находка не называет правило")
        self.assertNotIn_("машина:", out, "в прогоне раскладки всплыла машинная обвязка")

    def test_foreign_project_says_so(self):
        alien = git_init(self.box.root / "cmdalien")
        write(alien / "main.go", "package main\n")
        git(alien, "add", "-A")
        rc, out = self.box.dkctl_run("doctor", "--layout", "-C", str(alien))
        self.assertEqual(rc, 0, "чужой проект дал находку раскладки: %s" % out)
        self.assertIn_("на самом devkit", out, "не сказано, почему проверять нечего")


class SelfcheckFilesTest(unittest.TestCase):
    """Поимённое исключение стоит на живых файлах: уехавший в tools/ или
    переименованный инструмент оставил бы в проверке дыру, о которой никто не
    узнает.
    """

    def test_named_files_exist(self):
        for rel in layout.SKILLS_SELFCHECK:
            self.assertTrue((DEVKIT_SRC / rel).is_file(),
                            "исключение раскладки названо на %s, а файла нет" % rel)


class ReadmeGuardTest(unittest.TestCase):
    """Сторож: белый список из кода против перечня записей в разделе
    «Раскладка» README. Разъедься они, и сессия, читающая README как правило,
    считала бы нарушением исправное дерево.
    """

    def setUp(self):
        self.text = read(DEVKIT_SRC / "README.md")

    def test_lists_match(self):
        self.assertEqual(layout.readme_gaps(self.text), [],
                         "белый список проверки разошёлся с разделом «Раскладка» README")

    def test_all_fifteen_entries_are_named(self):
        entries = layout.readme_entries(self.text)
        self.assertEqual(len(layout.TOP_LEVEL), 15,
                         "записей белого списка %d, а правило называет пятнадцать" % len(layout.TOP_LEVEL))
        for e in layout.TOP_LEVEL:
            self.assertTrue(any(name == e or name.endswith(".md") for name in entries),
                            "README не называет запись «%s»" % e)

    def test_missing_entry_in_readme_reddens(self):
        cut = self.text.replace("`.devkit/` с рабочей обвязкой выката", "рабочая обвязка выката")
        self.assertNotEqual(cut, self.text, "текст README изменился, сторож правится вместе с ним")
        gaps = layout.readme_gaps(cut)
        self.assertTrue([g for g in gaps if ".devkit" in g],
                        "пропажа записи в README прошла молча: %s" % gaps)

    def test_extra_entry_in_readme_reddens(self):
        added = self.text.replace("и `.devkit/` с рабочей обвязкой",
                                  "`.scratch/` и `.devkit/` с рабочей обвязкой")
        self.assertNotEqual(added, self.text, "текст README изменился, сторож правится вместе с ним")
        gaps = layout.readme_gaps(added)
        self.assertTrue([g for g in gaps if ".scratch" in g],
                        "лишняя запись в README прошла молча: %s" % gaps)


if __name__ == "__main__":
    unittest.main()
