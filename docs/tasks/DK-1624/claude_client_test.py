"""Тест прогона клиента Claude Code с таймаутом группы (DK-1624).

Живой дефект из прогона на рабочей машине: обёртка подписки при убийстве
оставляла настоящего клиента держать PIPE, и чтение вывода ждало без
предела. Фикстура повторяет ту же форму: обёртка порождает потомка,
наследующего её stdout, и оба засыпают.
"""
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

import claude_client

WRAPPER = """
import subprocess
import sys
import time

child = subprocess.Popen(
    [sys.executable, "-c", "import time; time.sleep(300)"])
with open(sys.argv[1], "w") as handle:
    handle.write(str(child.pid))
time.sleep(300)
"""


class TimeoutGroupTest(unittest.TestCase):
    def test_timeout_kills_wrapper_and_child(self):
        with tempfile.TemporaryDirectory(prefix="dk1624_stuck_") as tmp:
            pid_file = Path(tmp) / "child.pid"
            wrapper = Path(tmp) / "wrapper.py"
            wrapper.write_text(WRAPPER, encoding="utf-8")
            run = claude_client.run(
                [sys.executable, str(wrapper), str(pid_file)], cwd=tmp,
                input_lines=['{"type":"user"}'], timeout=2,
                stdout_log=Path(tmp) / "out.jsonl",
                stderr_log=Path(tmp) / "err.txt")
            self.assertTrue(run["timed_out"])
            child_pid = int(pid_file.read_text(encoding="utf-8").strip())
            dead = False
            for _ in range(50):
                try:
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    dead = True
                    break
                time.sleep(0.1)
            self.assertTrue(dead, "потомок обёртки жив после таймаута")

    def test_normal_run_collects_output(self):
        with tempfile.TemporaryDirectory(prefix="dk1624_ok_") as tmp:
            run = claude_client.run(
                [sys.executable, "-c",
                 "import sys; sys.stdout.write('{\"ok\": true}')"],
                cwd=tmp, input_lines=[], timeout=30,
                stdout_log=Path(tmp) / "out.jsonl",
                stderr_log=Path(tmp) / "err.txt")
            self.assertFalse(run["timed_out"])
            self.assertEqual(run["exit"], 0)
            self.assertEqual(run["events"], [{"ok": True}])


if __name__ == "__main__":
    unittest.main()
