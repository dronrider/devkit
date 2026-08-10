"""Тесты носителя дашборда: launchd-агент, находки доктора и живость по
/healthz. Запускатель и поход за /healthz подставные, launchd не трогается."""
import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import dashboard


class Fake:
    """Подставной запускатель: помнит вызовы и отвечает заданным кодом."""

    def __init__(self, code=0, out=""):
        self.calls = []
        self.code = code
        self.out = out

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        return subprocess.CompletedProcess(argv, self.code, self.out, None)

    def argv_with(self, needle):
        return [a for a in self.calls if any(needle in str(x) for x in a)]


def ok_fetch(port):
    return json.dumps({"ok": True, "version": "dashboard dev", "projects": 1,
                       "errors": []})


class Stand(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="dashboard-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        self.binary = self.dir / "bin" / "dashboard"
        self.binary.parent.mkdir(parents=True)
        self.binary.write_text("#!/bin/sh\n", encoding="utf-8")
        self.plist = self.home / "Library" / "LaunchAgents" / ("%s.plist" % dashboard.LABEL)

    def check(self, fix=False, call=None, platform="darwin", binary=True,
              fetch=ok_fetch, from_main=True):
        call = Fake() if call is None else call
        which = (lambda name: str(self.binary)) if binary else (lambda name: None)
        f, d = dashboard.check(fix=fix, main=self.dir / "devkit", from_main=from_main,
                               home=self.home, platform=platform,
                               call=call, which=which, fetch=fetch)
        return f, d, call


class AgentTest(Stand):
    def test_missing_binary_is_a_finding(self):
        f, d, call = self.check(binary=False)
        self.assertEqual(len(f), 1, f)
        self.assertIn("devkitctl update", f[0])
        self.assertEqual(d, [])
        self.assertEqual(call.calls, [])

    def test_missing_agent_is_a_finding(self):
        f, d, _ = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn("doctor --fix", f[0])
        self.assertEqual(d, [])

    def test_fix_installs_and_loads(self):
        f, d, call = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertTrue(self.plist.exists(), "агент не положен")
        text = self.plist.read_text(encoding="utf-8")
        self.assertIn("<string>%s</string>" % self.binary, text)
        self.assertIn("<string>serve</string>", text)
        self.assertIn("<key>KeepAlive</key><true/>", text)
        self.assertIn("<key>EnvironmentVariables</key>", text)
        self.assertIn("<key>PATH</key><string>%s" % self.binary.parent, text)
        self.assertTrue(call.argv_with("bootstrap"), "launchd не позван: %s" % call.calls)

    def test_agent_pointing_elsewhere(self):
        self.check(fix=True)
        old = self.plist.read_text(encoding="utf-8").replace(str(self.binary), "/старый/бинарь")
        self.plist.write_text(old, encoding="utf-8")
        f, _, _ = self.check()
        self.assertIn("/старый/бинарь", f[0])
        f, d, _ = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn(str(self.binary), self.plist.read_text(encoding="utf-8"))

    def test_agent_path_parts(self):
        # PATH агента: каталог бинаря первым, системные пути launchd следом,
        # без дублей, когда бинарь и так лежит в системном каталоге.
        path = dashboard.agent_path(str(self.binary))
        parts = path.split(":")
        self.assertEqual(parts[0], str(self.binary.parent))
        for p in dashboard.SYSTEM_PATH:
            self.assertIn(p, parts)
        dup = dashboard.agent_path("/usr/bin/dashboard").split(":")
        self.assertEqual(len(dup), len(set(dup)), "дубли в PATH агента: %s" % dup)
        self.assertEqual(dup[0], "/usr/bin")

    def test_old_agent_without_path_is_rewritten(self):
        # Агент, положенный до EnvironmentVariables, это находка, и --fix
        # переписывает его: иначе дефект PATH пережил бы доводку.
        self.check(fix=True)
        text = self.plist.read_text(encoding="utf-8")
        head, tail = text.split("  <key>EnvironmentVariables</key>\n", 1)
        old = head + tail.split("  </dict>\n", 1)[1]
        self.plist.write_text(old, encoding="utf-8")
        f, d, _ = self.check()
        self.assertEqual(len(f), 1, f)
        f, d, _ = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("<key>EnvironmentVariables</key>",
                      self.plist.read_text(encoding="utf-8"))

    def test_fix_from_worktree_refuses(self):
        # На машину едет только проверенное: с worktree ветки задачи plist не
        # кладётся, доводка отсылает в основной чекаут.
        f, d, call = self.check(fix=True, from_main=False)
        self.assertEqual(d, [])
        self.assertIn("основного чекаута", f[0])
        self.assertFalse(self.plist.exists())
        self.assertEqual(call.calls, [])

    def test_installed_but_not_loaded(self):
        self.check(fix=True)
        dead = Fake(code=113)
        f, d, _ = self.check(call=dead)
        self.assertEqual(len(f), 1, f)
        self.assertIn("не поднят", f[0])
        self.assertEqual(d, [])

    def test_launchctl_refusal_is_named(self):
        broken = Fake(code=5, out="Bootstrap failed")
        f, d, _ = self.check(fix=True, call=broken)
        self.assertEqual(len(f), 1, f)
        self.assertIn("не взял", f[0])
        self.assertEqual(d, [])

    def test_other_platform_quiet_without_use(self):
        f, d, _ = self.check(platform="linux", binary=False)
        self.assertEqual((f, d), ([], []))

    def test_other_platform_names_systemd_when_used(self):
        f, d, _ = self.check(platform="linux")
        self.assertEqual(len(f), 1, f)
        self.assertIn("systemd", f[0])


class ProbeTest(Stand):
    def loaded_check(self, fetch):
        (self.home / ".devkit" / "dashboard.local").write_text(
            "root = /x\ntoken = abc\n", encoding="utf-8")
        self.check(fix=True)
        return self.check(fetch=fetch)

    def test_healthy_dashboard_is_quiet(self):
        f, d, _ = self.loaded_check(ok_fetch)
        self.assertEqual((f, d), ([], []))

    def test_no_config_skips_the_probe(self):
        # Конфига ещё нет, значит сервер ни разу не стартовал (его кладёт сам
        # serve): агент только что взведён, и стучаться в /healthz некуда.
        self.check(fix=True)

        def boom(port):
            raise AssertionError("probe не должен ходить в сеть без конфига")
        f, d, _ = self.check(fetch=boom)
        self.assertEqual((f, d), ([], []))

    def test_dead_healthz_is_a_finding(self):
        def dead(port):
            raise OSError("connection refused")
        f, _, _ = self.loaded_check(dead)
        self.assertEqual(len(f), 1, f)
        self.assertIn("/healthz", f[0])
        self.assertIn("dashboard.log", f[0])

    def test_config_errors_are_named(self):
        def errs(port):
            return json.dumps({"ok": True, "errors": ["в конфиге нет ни одной строки root"]})
        f, _, _ = self.loaded_check(errs)
        self.assertEqual(len(f), 1, f)
        self.assertIn("root", f[0])

    def test_foreign_answer_is_a_finding(self):
        f, _, _ = self.loaded_check(lambda port: "<html>чужой сервер</html>")
        self.assertEqual(len(f), 1, f)
        self.assertIn("не своим /healthz", f[0])


class ConfTest(Stand):
    def test_port_default_and_override(self):
        self.assertEqual(dashboard.conf_port(self.home), dashboard.DEFAULT_PORT)
        conf = self.home / ".devkit" / "dashboard.local"
        conf.write_text("root = /x\nport = 7200\n", encoding="utf-8")
        self.assertEqual(dashboard.conf_port(self.home), 7200)
        conf.write_text("port = мусор\n", encoding="utf-8")
        self.assertEqual(dashboard.conf_port(self.home), dashboard.DEFAULT_PORT)

    def test_probe_uses_configured_port(self):
        conf = self.home / ".devkit" / "dashboard.local"
        conf.write_text("port = 7300\n", encoding="utf-8")
        seen = []

        def spy(port):
            seen.append(port)
            return ok_fetch(port)

        dashboard.probe(self.home, spy)
        self.assertEqual(seen, [7300])


if __name__ == "__main__":
    unittest.main()
