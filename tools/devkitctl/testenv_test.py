"""Тесты песочницы сюит: окружение команд снимает чужой хвост конвейера."""
import os
import sys
import unittest
from unittest import mock

import selfcheck
import testenv


class SandboxDropsPipelineTail(unittest.TestCase):
    def test_chat_session_is_local_to_sandbox(self):
        argv = [sys.executable, "-c",
                "import os; print(os.environ.get('DEVKIT_CHAT_SESSION_ID', 'absent'))"]
        with mock.patch.dict(os.environ, DEVKIT_CHAT_SESSION_ID="parent-chat-session"):
            for extra, want in ((None, "absent"),
                                ({"DEVKIT_CHAT_SESSION_ID": "sandbox-chat-session"},
                                 "sandbox-chat-session")):
                with self.subTest(extra=extra):
                    rc, out = testenv.run(argv, env=extra)
                    self.assertEqual(rc, 0, out)
                    self.assertEqual(out, want + "\n")
            self.assertEqual(os.environ["DEVKIT_CHAT_SESSION_ID"], "parent-chat-session")

    def test_effort_is_local_to_sandbox(self):
        argv = [sys.executable, "-c",
                "import os; print(os.environ.get('DEVKIT_EFFORT', 'absent'))"]
        with mock.patch.dict(os.environ, DEVKIT_EFFORT="high"):
            for extra, want in ((None, "absent"),
                                ({"DEVKIT_EFFORT": "low"}, "low")):
                with self.subTest(extra=extra):
                    rc, out = testenv.run(argv, env=extra)
                    self.assertEqual(rc, 0, out)
                    self.assertEqual(out, want + "\n")
            self.assertEqual(os.environ["DEVKIT_EFFORT"], "high")

    def test_pipeline_tail_dropped(self):
        # Сюиты зовут и из живой сессии задачи, и песочница обязана снимать
        # её хвост с окружения команд (DK-1694): чужой DEVKIT_TASK_RUN_ID
        # ронял стендовому task-run подтверждение запуска, воронка снимала
        # голову кодом 2.
        for name in selfcheck.PIPE_ENV_DROP:
            os.environ[name] = "foreign"
        try:
            e = testenv.sandbox_env()
        finally:
            for name in selfcheck.PIPE_ENV_DROP:
                del os.environ[name]
        leaked = [n for n in selfcheck.PIPE_ENV_DROP if n in e]
        self.assertEqual(leaked, [], "песочница пропустила хвост конвейера")


if __name__ == "__main__":
    unittest.main()
