#!/usr/bin/env python3
"""Рубеж пуша: ручной пуш пользователя рубеж не видит, пуш из сессии агента
отбивается с разбором, а разрешение от утилит devkit пропускает. Своего модуля
у хука нет, проверяется он как процесс, которым его зовёт git.
"""
import os
import subprocess
import unittest

HOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pre-push")
SESSION = ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CURSOR_AGENT")


def run(**env):
    clean = {k: v for k, v in os.environ.items() if k not in SESSION}
    clean.pop("DEVKIT_PUSH_OK", None)
    return subprocess.run([HOOK, "origin", "git@example.com:x.git"],
                          env=dict(clean, **env), input="",
                          capture_output=True, text=True)


class TestPrePush(unittest.TestCase):
    def test_user_push_is_not_seen(self):
        self.assertEqual(run().returncode, 0)

    def test_agent_push_is_refused_with_advice(self):
        r = run(CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        self.assertIn("DEVKIT_PUSH_OK", r.stderr)

    def test_devkit_permission_passes(self):
        self.assertEqual(run(CLAUDECODE="1", DEVKIT_PUSH_OK="1").returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
