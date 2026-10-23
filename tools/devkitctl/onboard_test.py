"""Тесты подготовки профиля подписки: онбординг и доверие деревьям проектов
раскладываются в .claude.json до первого подъёма, по корням дашборда и
машинному слою подписок. Дом машины и корень проектов выдуманные, клиент не
зовётся."""
import json
import shutil
import tempfile
import unittest
from pathlib import Path

import harness
import onboard


def board_dir(d):
    (d / "docs").mkdir(parents=True)
    (d / "docs" / "TASKS.md").write_text("# Доска\n", encoding="utf-8")
    return d


class Stand(unittest.TestCase):
    """Стенд: дом машины с конфигом дашборда, корень проектов с деревом
    задачи и каталог подписки в машинном слое."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="onboard-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        self.projects = self.dir / "projects"
        self.project = board_dir(self.projects / "devkit")
        self.tree = board_dir(self.projects / "devkit-dk-1")
        self.conf = self.home / ".devkit" / "dashboard.local"
        self.conf.write_text("root = %s\n" % self.projects, encoding="utf-8")
        self.harness_home = self.dir / "sub"
        self.harness_home.mkdir()

    def check(self, fix=False, profiles=None, homes=None):
        if profiles is None:
            profiles = [("routerai", harness.parse(
                "routerai.toml", '[hooks]\nprotocol = "claude-code"\n'))]
        return onboard.check(fix, profiles, {} if homes is None else homes,
                             "devkitctl doctor --fix", home=self.home)

    def marks(self, data, d):
        return (data["projects"][str(d)]["hasTrustDialogAccepted"]
                and data["projects"][str(d)]["hasCompletedProjectOnboarding"])


class TreesTest(Stand):
    """Перечень деревьев: каталоги с доской, где дашборд поднимает сессии."""

    def test_root_project_tree_lies_outside_root(self):
        # Корень сам держит доску: дерево задачи лежит сиблингом корня, вне
        # его, и обход по детям корня его не видит.
        root = board_dir(self.dir / "solo")
        tree = board_dir(self.dir / "solo-dk-9")
        self.assertEqual(onboard.project_trees([root]), [root, tree])

    def test_boardless_dirs_are_skipped(self):
        (self.projects / "devkit-backup").mkdir()
        (self.projects / "devkit-dk-2").mkdir()
        self.assertEqual(onboard.project_trees([self.projects]),
                         [self.project, self.tree])

    def test_missing_root_gives_no_trees(self):
        self.assertEqual(onboard.project_trees([self.dir / "nowhere"]), [])


class CheckTest(Stand):
    """Находка без --fix, починка с ним, молчание на готовом."""

    def test_missing_profile_is_a_finding(self):
        f, d = self.check()
        self.assertEqual(d, [])
        self.assertEqual(len(f), 1, f)
        self.assertIn("routerai", f[0])
        self.assertIn(str(self.project), f[0])
        self.assertIn(str(self.tree), f[0])
        self.assertIn("DK-1282", f[0])
        self.assertIn("devkitctl doctor --fix", f[0])
        self.assertFalse((self.home / ".claude.json").exists(),
                         "без --fix файл состояния заводиться не должен")

    def test_long_list_is_cut(self):
        for i in range(2, 10):
            board_dir(self.projects / ("devkit-dk-%d" % i))
        f, _ = self.check()
        self.assertIn("и ещё 4", f[0])

    def test_fix_creates_fresh_state_file(self):
        conf = self.home / ".claude.json"
        f, d = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("routerai", d[0])
        self.assertIn("2 дерева проектов", d[0])
        data = json.loads(conf.read_text(encoding="utf-8"))
        for t in (self.project, self.tree):
            self.assertTrue(self.marks(data, t), str(t))
        self.assertEqual(oct(conf.stat().st_mode & 0o777), oct(0o600))

    def test_fix_writes_marks_and_keeps_the_rest(self):
        conf = self.home / ".claude.json"
        conf.write_text(json.dumps({
            "tipsHistory": {"intro": True},
            "projects": {str(self.project): {"allowedTools": ["Bash"]}}}),
            encoding="utf-8")
        f, d = self.check(fix=True)
        self.assertEqual(f, [])
        data = json.loads(conf.read_text(encoding="utf-8"))
        self.assertEqual(data["tipsHistory"], {"intro": True})
        self.assertEqual(data["projects"][str(self.project)]["allowedTools"], ["Bash"])
        for t in (self.project, self.tree):
            self.assertTrue(self.marks(data, t), str(t))

    def test_half_marked_tree_still_stalls_the_lift(self):
        # Доверие и онбординг закрывают разные вопросы клиента, и половинный
        # набор подъём всё равно останавливает.
        conf = self.home / ".claude.json"
        conf.write_text(json.dumps({"projects": {
            str(self.project): {"hasTrustDialogAccepted": True},
            str(self.tree): {"hasCompletedProjectOnboarding": True}}}), encoding="utf-8")
        f, d = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn(str(self.project), f[0])
        self.assertIn(str(self.tree), f[0])
        self.check(fix=True)
        data = json.loads(conf.read_text(encoding="utf-8"))
        for t in (self.project, self.tree):
            self.assertTrue(self.marks(data, t), str(t))

    def test_trusted_profile_is_silent(self):
        self.check(fix=True)
        f, d = self.check()
        self.assertEqual((f, d), ([], []))
        f, d = self.check(fix=True)
        self.assertEqual((f, d), ([], []))

    def test_broken_projects_section_is_rewritten(self):
        conf = self.home / ".claude.json"
        conf.write_text(json.dumps({"projects": "мусор"}), encoding="utf-8")
        f, d = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        data = json.loads(conf.read_text(encoding="utf-8"))
        self.assertTrue(self.marks(data, self.tree))


class BrokenStateTest(Stand):
    """Битый файл состояния это беда самого клиента: доктор обходит его
    молча, не находкой и не починкой, как и trust_dirs у замера."""

    def test_garbage_state_is_left_alone(self):
        conf = self.home / ".claude.json"
        conf.write_text("{не json", encoding="utf-8")
        f, d = self.check()
        self.assertEqual(f, [])
        f, d = self.check(fix=True)
        self.assertEqual((f, d), ([], []))
        self.assertEqual(conf.read_text(encoding="utf-8"), "{не json")

    def test_non_dict_state_is_left_alone(self):
        conf = self.home / ".claude.json"
        conf.write_text("[1, 2]", encoding="utf-8")
        f, d = self.check(fix=True)
        self.assertEqual((f, d), ([], []))


class SkipTest(Stand):
    """Кого доктор не трогает: подписки других протоколов и машины без
    корней дашборда."""

    def test_non_claude_protocol_is_skipped(self):
        profiles = [("codex", harness.parse("codex.toml", '[rules]\nmode = "strict"\n'))]
        f, d = self.check(fix=True, profiles=profiles)
        self.assertEqual((f, d), ([], []))
        self.assertFalse((self.home / ".claude.json").exists())

    def test_alt_sub_state_lives_in_harness_home(self):
        homes = {"routerai": str(self.harness_home)}
        f, d = self.check(fix=True, homes=homes)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        data = json.loads((self.harness_home / ".claude.json").read_text(encoding="utf-8"))
        self.assertTrue(self.marks(data, self.tree))
        self.assertFalse((self.home / ".claude.json").exists(),
                         "без ключа home в машинном слое файл состояния лежит на доме машины")

    def test_no_roots_no_findings(self):
        self.conf.unlink()
        f, d = self.check(fix=True)
        self.assertEqual((f, d), ([], []))
        self.assertFalse((self.home / ".claude.json").exists())


if __name__ == "__main__":
    unittest.main()
