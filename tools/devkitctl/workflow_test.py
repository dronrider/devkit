#!/usr/bin/env python3
"""Тесты сторожа полноты карты переходов (DK-407).

Синтетика для самого сторожа (скилл, статус, рубеж, отсутствие карты, чужой
проект, неразбираемый источник), живой чекаут для инварианта карты и стенд
для проводки находки через doctor.
"""
import shutil
import tempfile
import unittest
from pathlib import Path

import workflow
from testenv import SandboxCase, git, git_init, write

# Настоящий чекаут devkit: карта в нём обязана быть полна, а источники
# статусов и рубежей обязаны разбираться.
DEVKIT_SRC = Path(__file__).resolve().parents[2]

# Синтетический taskctl: та же форма таблицы секций и констант рубежей,
# что и в живом, имена изменены, чтобы карта синтетики не совпала с картой
# живого чекаута случайно.
BOARD_GO = '''package main

var sectByPrefix = []struct{ prefix, key string }{
	{"## In progress", SectInProgress},
	{"## Check", SectCheck},
	{"## Backlog", SectBacklog},
	{"## Blocked", SectBlocked},
}
'''

PROGRESS_GO = '''package main

const (
	progressBoard   = 0.00
	progressCommit  = 0.35
	progressReady   = 0.56
	progressRelease = 0.89
	progressDone    = 1.00
)
'''

SKILLS = ("board-task", "board-groom", "proofread")

FULL_MAP = """# Карта переходов

Полная карта синтетического стенда.

- Backlog -> In progress и обратно в духе `taskctl move`: скилл `board-task`.
- Проверено в `Check`, уехало в Blocked и вернулось: скилл `board-groom`.
- Проза вычитана скиллом `proofread`.

Рубежи: 0.00, 0.35, 0.56, 0.89, 1.00.
"""


def make_devkit(base, map_text=FULL_MAP, board_go=BOARD_GO):
    """Синтетический чекаут devkit: признак is_devkit, скиллы и источники."""
    root = Path(base)
    (root / "hooks").mkdir(parents=True)
    write(root / "RULES.core.md", "# ядро\n")
    write(root / "tools" / "taskctl" / "board.go", board_go)
    write(root / "tools" / "taskctl" / "progress.go", PROGRESS_GO)
    for name in SKILLS:
        write(root / "kit" / "skills" / name / "SKILL.md",
              "---\nname: %s\ndescription: скилл стенда.\n---\n" % name)
    if map_text is not None:
        write(root / "docs" / "workflow.md", map_text)
    return root


class WorkflowGuardTest(unittest.TestCase):

    def setUp(self):
        self.temp = Path(tempfile.mkdtemp(prefix="workflow-test-"))

    def tearDown(self):
        shutil.rmtree(self.temp, True)

    def test_full_map_is_silent(self):
        root = make_devkit(self.temp / "dk")
        self.assertEqual(workflow.check(root), [])

    def test_unmentioned_skill_is_a_finding(self):
        root = make_devkit(self.temp / "dk",
                           FULL_MAP.replace("скиллом `proofread`", "другой рукой"))
        findings = workflow.check(root)
        self.assertEqual(findings, ["карта переходов: скилл proofread не упомянут"])

    def test_unmentioned_status_is_a_finding(self):
        root = make_devkit(self.temp / "dk",
                           FULL_MAP.replace("уехало в Blocked и вернулось", "уехало в сторону"))
        findings = workflow.check(root)
        self.assertEqual(findings, ["карта переходов: статус доски Blocked не упомянут"])

    def test_unmentioned_mark_is_a_finding(self):
        root = make_devkit(self.temp / "dk",
                           FULL_MAP.replace("0.35, 0.56", "0.35"))
        findings = workflow.check(root)
        self.assertEqual(findings, ["карта переходов: рубеж 0.56 не упомянут"])

    def test_missing_map_is_a_finding(self):
        root = make_devkit(self.temp / "dk", map_text=None)
        findings = workflow.check(root)
        self.assertEqual(len(findings), 1)
        self.assertIn("docs/workflow.md", findings[0])

    def test_foreign_project_is_silent(self):
        # Проект без признака devkit карта не проверяется вовсе: находка
        # звала бы чинить чужое дерево.
        root = make_devkit(self.temp / "proj")
        (root / "RULES.core.md").unlink()
        root.joinpath("docs", "workflow.md").unlink()
        self.assertEqual(workflow.check(root), [])

    def test_unparseable_sources_are_findings(self):
        # Источник, который перестал разбираться, не даёт сторожу молчать:
        # молчание неотличимо от согласного.
        root = make_devkit(self.temp / "dk", board_go="package main\n")
        findings = workflow.check(root)
        self.assertIn(workflow.NO_STATUSES, findings)

    def test_real_checkout_map_is_complete(self):
        # Инвариант живого чекаута: карта упоминает все скиллы, все статусы
        # доски и все рубежи из настоящих источников taskctl.
        self.assertEqual(workflow.check(DEVKIT_SRC), [])

    def test_real_sources_parse(self):
        # Живые board.go и progress.go разбираются, и в них ожидаемое число
        # записей: четыре статуса доски и пять рубежей. Регрессию парсера
        # видно здесь, а не молчанием сторожа на живом чекауте.
        self.assertEqual(workflow.statuses(DEVKIT_SRC),
                         ["In progress", "Check", "Backlog", "Blocked"])
        self.assertEqual(workflow.marks(DEVKIT_SRC),
                         ["0.00", "0.35", "0.56", "0.89", "1.00"])


class DoctorMapTest(SandboxCase):
    """Находка карты переходов доезжает до doctor, а не только до модуля:
    стенд это копия devkit без docs/, признак чекаута добавляется на месте.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.sdk = cls.box.root / "wf" / "devkit"
        cls.sdk.mkdir(parents=True)
        for d in ("tools", "kit"):
            shutil.copytree(str(cls.box.dk / d), str(cls.sdk / d))
        (cls.sdk / "hooks").mkdir()
        write(cls.sdk / "RULES.core.md", "# ядро\n")
        git_init(cls.sdk)
        git(cls.sdk, "add", "-A")
        git(cls.sdk, "commit", "-qm", "стенд карты переходов")

    def test_missing_map_is_a_doctor_finding(self):
        _, out = self.box.doctor(self.sdk)
        self.assertIn("карты переходов docs/workflow.md нет", out,
                      "doctor не назвал отсутствие карты переходов: %s" % out)

    def test_unmentioned_skill_is_a_doctor_finding(self):
        # Карта со всеми упоминаниями, кроме одного скилла: находка называет
        # скилл по имени и доезжает до вывода doctor.
        names = sorted(p.name for p in (self.sdk / "kit" / "skills").iterdir()
                       if p.is_dir())
        omitted = "proofread"
        text = "# Карта\n\nСкиллы: %s.\n\nСтатусы: In progress, Check, Backlog, Blocked.\n\nРубежи: 0.00 0.35 0.56 0.89 1.00.\n" \
            % ", ".join("`%s`" % n for n in names if n != omitted)
        write(self.sdk / "docs" / "workflow.md", text)
        try:
            _, out = self.box.doctor(self.sdk)
            self.assertIn("карта переходов: скилл %s не упомянут" % omitted, out,
                          "doctor не назвал неупомянутый скилл: %s" % out)
        finally:
            (self.sdk / "docs" / "workflow.md").unlink()


if __name__ == "__main__":
    unittest.main()
