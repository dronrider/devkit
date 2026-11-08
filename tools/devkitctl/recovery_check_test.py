import contextlib
import io
import os
import subprocess
import unittest
from unittest.mock import patch

import recovery_check


class RecoveryCheckTest(unittest.TestCase):
    def test_unknown_installed_api_does_not_start_a_worker(self):
        before = dict(os.environ)
        with patch.object(recovery_check.shutil, "which", return_value="/synthetic/bin"), \
             patch.object(recovery_check.subprocess, "run", return_value=subprocess.CompletedProcess([], 2, "", "unknown --json")) as call, \
             contextlib.redirect_stderr(io.StringIO()) as error:
            code = recovery_check.main()
        self.assertEqual(code, 1)
        self.assertIn("обновите весь комплект", error.getvalue())
        self.assertEqual(call.call_count, 1)
        self.assertNotIn("run", call.call_args.args[0])
        self.assertEqual(dict(os.environ), before)
