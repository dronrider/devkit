"""Тесты песочницы сюит: окружение команд снимает чужой хвост конвейера."""
import os
import unittest

import selfcheck
import testenv


class SandboxDropsPipelineTail(unittest.TestCase):
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
