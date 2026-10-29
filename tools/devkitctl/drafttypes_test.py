import tempfile
import unittest
from pathlib import Path

import drafttypes
from testenv import DEVKIT_SRC


class DraftTypesTest(unittest.TestCase):
    def test_builtins(self):
        kit = DEVKIT_SRC / "kit"
        self.assertEqual(drafttypes.check(str(kit / "draft-types"), "/missing", str(kit / "skills")), [])

    def test_invalid_and_empty_project_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / "project"
            project.mkdir()
            target = project / "task.toml"
            for text in ("", 'name = "task"\nunknown = 1\n'):
                target.write_text(text, encoding="utf-8")
                findings = drafttypes.check("/missing", str(project), "/missing")
                self.assertEqual(len(findings), 1)
                self.assertIn(str(target), findings[0])
