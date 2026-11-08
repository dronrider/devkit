import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

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
