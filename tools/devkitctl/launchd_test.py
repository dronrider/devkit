"""Тесты общей механики launchd-агентов: дом машины, вызов launchctl и сверка
взведённого агента с положенным plist. Настоящий launchd не трогается, вместо
него подставной запускатель."""
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import launchd
import testenv

LABEL = "ru.devkit.test-agent"


class Fake:
    """Подставной запускатель: помнит вызовы и отвечает заданным кодом."""

    def __init__(self, code=0, out="", codes=None):
        self.calls = []
        self.code = code
        self.out = out
        self.codes = list(codes or [])

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        code = self.codes.pop(0) if self.codes else self.code
        return subprocess.CompletedProcess(argv, code, self.out, None)

    def argv_with(self, needle):
        return [a for a in self.calls if any(needle in str(x) for x in a)]


def printed(path):
    """Вывод `launchctl print` для агента, взведённого из указанного plist."""
    return ("gui/501/%s = {\n\tactive count = 1\n\tpath = %s\n\tstate = running\n}"
            % (LABEL, path))


class HomeTest(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="launchd-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)

    def test_machine_home_ignores_the_home_variable(self):
        # Стенд тестов держит HOME подставным (testenv), а launchd раскладку
        # берёт из учётной записи: подмена переменной службы машины не двигает.
        with testenv.fake_home(self.dir):
            self.assertNotEqual(str(launchd.machine_home()), os.environ["HOME"])

    def test_own_home_sees_the_substitute(self):
        self.assertFalse(launchd.own_home(self.dir, self.dir / "machine"))
        self.assertTrue(launchd.own_home(self.dir, self.dir))

    def test_own_home_follows_symlinks(self):
        real = self.dir / "real"
        real.mkdir()
        link = self.dir / "link"
        link.symlink_to(real)
        self.assertTrue(launchd.own_home(link, real))

    def test_foreign_line_names_both_homes(self):
        line = launchd.foreign_line("дашборд", self.dir, self.dir / "x.plist",
                                    self.dir / "machine")
        self.assertIn(str(self.dir), line)
        self.assertIn("machine", line)
        self.assertIn("x.plist", line)


class AgentTest(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="launchd-agent-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.plist = self.dir / ("%s.plist" % LABEL)
        self.plist.write_text("<plist/>\n", encoding="utf-8")

    def test_reload_boots_out_before_bootstrap(self):
        call = Fake()
        self.assertEqual(launchd.reload_agent(LABEL, self.plist, call), "")
        self.assertEqual([a[1] for a in call.calls], ["bootout", "bootstrap"])

    def test_reload_falls_back_to_load(self):
        # Старый launchctl bootstrap не знает, и доводка договаривается с ним
        # через load -w.
        call = Fake(codes=[0, 5, 0])
        self.assertEqual(launchd.reload_agent(LABEL, self.plist, call), "")
        self.assertTrue(call.argv_with("load"))

    def test_reload_names_the_refusal(self):
        call = Fake(code=5, out="Bootstrap failed")
        self.assertIn("Bootstrap failed", launchd.reload_agent(LABEL, self.plist, call))

    def test_missing_launchctl_is_not_an_exception(self):
        def boom(argv, **kw):
            raise OSError("no launchctl")
        code, out = launchd.launchctl(["list", LABEL], boom)
        self.assertEqual(code, 127)
        self.assertIn("no launchctl", out)

    def test_agent_source_reads_the_path(self):
        call = Fake(out=printed("/Users/rider/Library/LaunchAgents/x.plist"))
        self.assertEqual(launchd.agent_source(LABEL, call),
                         "/Users/rider/Library/LaunchAgents/x.plist")

    def test_agent_source_empty_when_launchctl_refuses(self):
        self.assertEqual(launchd.agent_source(LABEL, Fake(code=113)), "")

    def test_hijack_is_seen_by_the_path(self):
        thief = "/private/tmp/чужая-сессия/Library/LaunchAgents/%s.plist" % LABEL
        call = Fake(out=printed(thief))
        self.assertEqual(launchd.hijacked(LABEL, self.plist, call), thief)

    def test_own_agent_is_not_a_hijack(self):
        call = Fake(out=printed(str(self.plist)))
        self.assertEqual(launchd.hijacked(LABEL, self.plist, call), "")

    def test_unknown_agent_is_not_a_hijack(self):
        # Агента нет вовсе, и говорить про перехват нечего: это другая находка.
        self.assertEqual(launchd.hijacked(LABEL, self.plist, Fake(code=113)), "")


if __name__ == "__main__":
    unittest.main()
