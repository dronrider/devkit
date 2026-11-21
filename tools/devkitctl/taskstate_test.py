import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from datetime import datetime

import taskstate


class TaskStateTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name) / "home"
        self.root = Path(self.temp.name) / "root"
        self.root.mkdir()
        self.name = taskstate.path(self.home, self.root, "DK-1")
        self.name.parent.mkdir(parents=True)
        self.name.with_suffix(".json.lock").touch()
        self.attempt = "RV-" + "1" * 32
        self.data = {"version":1,"root":str(self.root.resolve()),"task":"DK-1", "stopped":False,
                     "run":{"id":"run-one", "harness":"glm-code-2", "model":"glm-5.3", "pid":0, "attempt":self.attempt}}
        self.name.write_text(json.dumps(self.data))

    def test_legacy_app_run_requires_matching_persisted_identity(self):
        self.data["run"]["pid"] = 999999
        self.name.write_text(json.dumps(self.data))
        directory = self.home / ".devkit" / "chats"
        directory.mkdir()
        sid = "as-original"
        stored = directory / (sid + ".json")
        chat = {"task": "DK-1", "thread": "thread-one", "protocol": "codex",
                "taskRunSession": sid, "taskRunID": "run-one", "taskRunPID": 999999,
                "taskRun": True,
                "taskRunRoot": str(self.root.resolve()), "harness": "glm-code-2",
                "model": "glm-5.3"}
        stored.write_text(json.dumps(chat))
        with mock.patch.object(taskstate, "_legacy_app_pass", wraps=taskstate._legacy_app_pass) as journal:
            taskstate.app_runs(self.home, self.root, ["DK-1"])
            journal.assert_not_called()
        runs = taskstate.app_runs(self.home, self.root, ["DK-1"])
        self.assertEqual(runs["DK-1"]["appChat"], sid)
        self.assertTrue(taskstate.app_handles_recovery(runs["DK-1"]))
        stored.write_text(json.dumps(dict(chat, taskRun=False)))
        finished = taskstate.app_runs(self.home, self.root, ["DK-1"])["DK-1"]
        self.assertFalse(taskstate.app_handles_recovery(finished))
        for field, value in (("taskRunRoot", "/other"), ("taskRunID", "other"),
                             ("taskRunSession", "other"), ("taskRunPID", 1)):
            with self.subTest(field=field):
                stored.write_text(json.dumps(dict(chat, **{field: value})))
                self.assertEqual(taskstate.app_runs(self.home, self.root, ["DK-1"]), {})

    def test_legacy_app_run_keeps_ownership_after_interrupted_daemon_shutdown(self):
        self.data["run"].update(pid=999999, started="2026-11-20T10:00:00+03:00")
        self.name.write_text(json.dumps(self.data))
        directory = self.home / ".devkit" / "chats"
        directory.mkdir()
        sid = "as-original"
        chat = {"task": "DK-1", "thread": "thread-one", "protocol": "codex",
                "taskRunSession": sid, "taskRunID": "run-one", "taskRunPID": 999999,
                "taskRunRoot": str(self.root.resolve()), "harness": "glm-code-2",
                "model": "glm-5.3"}
        stored = directory / (sid + ".json")
        stored.write_text(json.dumps(chat))

        log = self.home / ".devkit" / "heads" / "DK-1.log"
        log.parent.mkdir()
        old = datetime.fromisoformat("2026-11-20T09:59:59+03:00").astimezone().strftime("%Y-%m-%dT%H:%M:%S")
        new = datetime.fromisoformat("2026-11-20T10:00:01+03:00").astimezone().strftime("%Y-%m-%dT%H:%M:%S")
        launch = " task-run DK-1: проход поднят: python3 task-run.py --app-turn-client"
        def write_log(*lines):
            log.write_text("\n".join(lines) + "\n")
            modified = datetime.fromisoformat("2026-11-20T10:00:02+03:00").timestamp()
            os.utime(log, (modified, modified))

        def recovery_owned():
            run = taskstate.app_runs(self.home, self.root, ["DK-1"]).get("DK-1")
            return run is not None and taskstate.app_handles_recovery(run)

        same = datetime.fromisoformat("2026-11-20T10:00:00+03:00").astimezone().strftime("%Y-%m-%dT%H:%M:%S")
        write_log(same + launch)
        with mock.patch.object(taskstate, "_legacy_app_pass", wraps=taskstate._legacy_app_pass) as journal:
            taskstate.app_runs(self.home, self.root, ["DK-1"])
            self.assertEqual(journal.call_count, 1)
        self.assertTrue(recovery_owned())
        self.assertTrue(taskstate.app_runs(self.home, self.root, ["DK-1"])["DK-1"]["appRecoveryAmbiguous"])
        write_log(same + launch, new + " task-run DK-1: стоп: старый проход")
        self.assertTrue(recovery_owned())
        write_log(old + launch)
        self.assertFalse(recovery_owned())
        write_log(old + launch, new + launch)
        self.assertTrue(recovery_owned())
        # В раздельном HOME адрес сохранён в конфиге дашборда. Тик в
        # машинном HOME всё равно видит незавершённый проход оболочки.
        stored.unlink()
        self.assertTrue(recovery_owned())
        write_log(old + launch, new + launch, new + " task-run DK-1: стоп: штатное завершение")
        self.assertFalse(recovery_owned())

    def test_owner_acknowledgment_preserves_identity(self):
        taskstate.acknowledge(self.home, self.root, "DK-1", "run-one", os.getpid())
        got = taskstate.identity(self.home, self.root, "DK-1", os.getpid())
        self.assertEqual(got["id"], "run-one")
        self.assertIsNone(taskstate.identity(self.home, self.root, "DK-1", 999999999))

    def test_stop_and_wrong_run_forbid_acknowledgment(self):
        with self.assertRaises(ValueError):
            taskstate.acknowledge(self.home, self.root, "DK-1", "other", os.getpid())
        self.data["stopped"] = True
        self.name.write_text(json.dumps(self.data))
        with self.assertRaises(ValueError):
            taskstate.acknowledge(self.home, self.root, "DK-1", "run-one", os.getpid())

    def test_progress_requires_owner_and_attempt(self):
        taskstate.acknowledge(self.home, self.root, "DK-1", "run-one", os.getpid())
        proof = self.home / ".devkit" / "recovery.jsonl.proofs" / (self.attempt + ".json")
        for run, attempt, pid in (("other", self.attempt, os.getpid()),
                                  ("run-one", "RV-"+"2"*32, os.getpid()),
                                  ("run-one", self.attempt, 999999999)):
            with self.assertRaises(ValueError):
                taskstate.progress(self.home, self.root, "DK-1", run, attempt, pid, "session")
            self.assertFalse(proof.exists())
        taskstate.progress(self.home, self.root, "DK-1", "run-one", self.attempt, os.getpid(), "session")
        got = json.loads(proof.read_text())
        self.assertEqual(got["run_id"], "run-one")
        self.assertEqual(got["stage"], "turn")
        self.data["stopped"] = True
        self.name.write_text(json.dumps(self.data))
        with self.assertRaises(ValueError):
            taskstate.progress(self.home, self.root, "DK-1", "run-one", self.attempt, os.getpid(), "session")

    def test_missing_corrupt_and_symlink_state_fail_closed(self):
        for text in ("{", "{}", json.dumps({**self.data, "version":2})):
            self.name.write_text(text)
            with self.assertRaises(ValueError):
                taskstate.read(self.home, self.root, "DK-1")
        self.name.unlink()
        with self.assertRaises(OSError):
            taskstate.read(self.home, self.root, "DK-1")
        self.name.symlink_to(self.root / "missing")
        with self.assertRaises(OSError):
            taskstate.read(self.home, self.root, "DK-1")
