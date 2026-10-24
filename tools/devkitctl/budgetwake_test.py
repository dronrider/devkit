import json
import os
import subprocess
import tempfile
import unittest
from datetime import datetime
from pathlib import Path
from unittest.mock import patch

import budgetwake
import lift


class BudgetWakeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name, "project")
        self.home = Path(self.tmp.name, "home")
        (self.root / ".devkit").mkdir(parents=True)
        self.home.mkdir()
        (self.home / ".devkit").mkdir()
        (self.home / ".devkit/sessions.log").write_text(
            "2026-10-24T00:00:00 сессия sid задача DK-1 дерево %s "
            "транскрипт %s/second/projects/slug/sid.jsonl tmux goal-DK-1\n"
            % (self.root, self.home))
        (self.root / ".devkit/deploy.local").write_text("autonomous = true\n")
        self.now = datetime.now()
        self.rows = [{"id": "DK-1", "title": "Цель: тест", "block": "бюджет исчерпан"}]
        self.calls = []
        self.gate = "gate: ok\nweek_all: потрачено 10 из 100; снимок 1м назад"
        self.launch_code = 0
        self.wake_code = 0
        self.batch = 3
        self.layout = {"default": "first", "harnesses": [
            {"name": "first", "enabled": True, "home": str(self.home / "first")},
            {"name": "second", "enabled": True, "home": str(self.home / "second")}]}

    def call(self, argv, **kw):
        self.calls.append(argv)
        code, text = 0, ""
        if "list" in argv:
            text = json.dumps({"sections": [{"key": "blocked", "rows": self.rows}]})
        elif "spend" in argv:
            text = self.gate
        elif "harness" in argv:
            text = json.dumps(self.layout)
        elif "budget" in argv:
            text = "batch: %s" % self.batch
        elif "wake" in argv:
            code = self.wake_code
        elif any("goal-run.py" in a for a in argv):
            code = self.launch_code
        return subprocess.CompletedProcess(argv, code, text)

    def run_wake(self, **kw):
        lines, self.attempted = budgetwake.wake(
            str(self.root), self.now, call=self.call,
            taskctl="taskctl", agentctl="agentctl", home=self.home, **kw)
        return lines

    def launched(self):
        return [c for c in self.calls if any("goal-run.py" in a for a in c)]

    def test_goal_outside_watch_registry_resumes_with_peek_before_wake(self):
        lines = self.run_wake()
        self.assertEqual(len(self.launched()), 1)
        self.assertEqual(self.attempted, 1)
        spend = next(c for c in self.calls if "spend" in c)
        self.assertIn("--peek", spend)
        self.assertNotIn("--record", spend)
        self.assertEqual(spend[1:5], ["exec", "--harness", "second", "--"])
        self.assertEqual(self.launched()[0][-2:], ["--harness", "second"])
        self.assertLess(self.calls.index(self.launched()[0]),
                        next(i for i, c in enumerate(self.calls) if "wake" in c))
        self.assertIn("цикл поднят", " ".join(lines))

    def test_cumulative_over_and_unknown_snapshot_keep_parking(self):
        for gate in ("gate: over\nwindow5h_all: потрачено 158 из 100, исчерпан",
                     "gate: over\nweek_all: потрачено 101 из 100, исчерпан",
                     "gate: ok\nснимка квоты нет, расход посчитан по журналу",
                     "gate: ok\nснимок 3ч назад, протух",
                     "gate: ok\nмомент снятия неизвестен",
                     "gate: ok\nснимок из будущего, часы разошлись",
                     "gate: ok\n", "gate: unknown\nснимок 1м назад"):
            with self.subTest(gate=gate):
                self.calls.clear()
                self.gate = gate
                self.assertIn("парковка по бюджету сохранена", " ".join(self.run_wake()))
                self.assertFalse(self.launched())
                self.assertFalse(any("wake" in c for c in self.calls))

    def test_only_explicit_budget_parked_goals(self):
        self.rows += [{"id": "DK-2", "title": "Задача", "block": "бюджет исчерпан"},
                      {"id": "DK-3", "title": "Цель: ждёт", "block": "вопрос: бюджет?"}]
        self.run_wake()
        self.assertEqual([c[2] for c in self.launched()], ["DK-1"])

    def test_live_goal_and_task_lock_prevent_probe(self):
        for location in (self.root / ".devkit/goal-DK-1.lock",
                         self.home / ".devkit/task-DK-1.lock"):
            with self.subTest(location=location):
                location.mkdir(parents=True)
                (location / "pid").write_text(str(os.getpid()))
                self.calls.clear()
                self.assertIn("носитель жив", " ".join(self.run_wake()))
                self.assertFalse(any("spend" in c for c in self.calls))
                (location / "pid").unlink()
                location.rmdir()

    def test_live_registered_session_prevents_probe(self):
        with patch("watch.entries", return_value=["entry"]), \
                patch("watch.read_entry", return_value={"root": str(self.root),
                                                        "goal": "DK-1", "session": "sid"}), \
                patch("watch.session_alive", return_value=True):
            self.run_wake()
        self.assertFalse(any("spend" in c for c in self.calls))

    def test_launch_failure_keeps_parking_and_settles_retry(self):
        self.launch_code = 2
        self.assertIn("парковка сохранена", " ".join(self.run_wake()))
        self.assertFalse(any("wake" in c for c in self.calls))
        self.calls.clear()
        self.assertIn("выдержка", " ".join(self.run_wake()))
        self.assertFalse(self.launched())

    def test_capacity_and_machine_limit(self):
        self.run_wake(left=0)
        self.assertFalse(self.launched())
        self.batch = 0
        self.run_wake()
        self.assertFalse(self.launched())
        self.assertEqual(self.attempted, 0)

    def test_machine_limit_applies_to_attempts_even_when_launch_fails(self):
        self.rows *= 0
        self.rows += [{"id": "DK-%d" % i, "title": "Цель: тест",
                       "block": "бюджет исчерпан"} for i in range(1, 5)]
        registry = self.home / ".devkit/sessions.log"
        registry.write_text(registry.read_text().replace("DK-1", "DK-2") + registry.read_text())
        self.launch_code = 2
        self.run_wake(left=1)
        self.assertEqual(len(self.launched()), 1)
        self.assertEqual(self.attempted, 1)

    def test_wake_failure_is_visible_after_launch(self):
        self.wake_code = 2
        self.assertIn("снятие парковки отказало", " ".join(self.run_wake()))
        self.assertEqual(self.attempted, 1)

    def test_attempt_limit(self):
        state = self.home / ".devkit"
        for _ in range(lift.LIFT_TRIES):
            lift.mark_lifted(str(self.root), "DK-1", str(state), self.now.timestamp() - lift.SETTLE - 1)
        self.assertIn("предел попыток", " ".join(self.run_wake()))
        self.assertFalse(self.launched())

    def test_unknown_disabled_or_ambiguous_original_harness_keeps_parking(self):
        for mode in ("unknown", "disabled", "ambiguous"):
            with self.subTest(mode=mode):
                if mode == "unknown":
                    self.layout["harnesses"] = []
                elif mode == "disabled":
                    self.layout["harnesses"] = [{"name": "second", "enabled": False,
                                                "home": str(self.home / "second")}]
                else:
                    self.layout["harnesses"] = [
                        {"name": name, "enabled": True, "home": str(self.home / "second")}
                        for name in ("second", "duplicate")]
                self.calls.clear()
                self.assertIn("подписка прежнего носителя", " ".join(self.run_wake()))
                self.assertFalse(any("spend" in c for c in self.calls))
                self.assertFalse(self.launched())

    def test_disabled_autonomy_and_dry_run(self):
        self.run_wake(act=False)
        self.assertFalse(self.launched())
        (self.root / ".devkit/deploy.local").write_text("autonomous = false\n")
        self.assertIn("автономия не включена", " ".join(self.run_wake()))
        self.assertFalse(self.launched())


if __name__ == "__main__":
    unittest.main()
