#!/usr/bin/env python3
"""Проверка отметки выборки prose (DK-1024): PostToolUse-хук на Bash ставит
след по команде `prose.py sample`, не трогает прочие команды Bash и прочие
инструменты, гасит след на SessionStart с source=compact и не трогает его на
обычном старте, а контекст субагента ведёт свой файл, тем же порядком, что у
check-reread.py (DK-608)."""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "prose-mark.py")


def load():
    spec = importlib.util.spec_from_file_location("prose_mark", TOOL)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, HERE)
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.pop(0)
    return mod


prose_mark = load()


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def bash_event(command, session="s1", agent=None):
    e = {"session_id": session, "hook_event_name": "PostToolUse",
         "tool_name": "Bash", "tool_input": {"command": command}}
    if agent:
        e["agent_id"] = agent
    return json.dumps(e)


def start_event(source="startup", session="s1", agent=None):
    e = {"session_id": session, "hook_event_name": "SessionStart", "source": source}
    if agent:
        e["agent_id"] = agent
    return json.dumps(e)


class MarkCase(unittest.TestCase):
    def setUp(self):
        self.state = tempfile.mkdtemp()

    def hook(self, event):
        return run("--hook", "--state", self.state, input=event)

    def marked(self, context="s1"):
        return os.path.exists(os.path.join(self.state, context + ".json"))


class TestMarksOnSample(MarkCase):
    def test_sample_command_marks_context(self):
        r = self.hook(bash_event(
            "python3 ~/projects/devkit/kit/skills/prose/prose.py sample --genre task"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(self.marked())

    def test_other_bash_command_does_not_mark(self):
        r = self.hook(bash_event("git status"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.marked())

    def test_other_tool_does_not_mark(self):
        event = json.loads(bash_event("prose.py sample --genre task"))
        event["tool_name"] = "Read"
        r = self.hook(json.dumps(event))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.marked())

    def test_prose_word_alone_does_not_mark(self):
        # Регулярка ловит именно "prose.py sample", а не любое упоминание
        # prose: чужая команда "grep prose" метить след не должна.
        r = self.hook(bash_event("grep -rn prose kit/skills"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.marked())


class TestSubagentContext(MarkCase):
    def test_subagent_mark_is_its_own_file(self):
        r = self.hook(bash_event(
            "python3 kit/skills/prose/prose.py sample --genre lld", agent="a1"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.marked("s1"))
        self.assertTrue(self.marked("s1.a1"))


class TestCompactClears(MarkCase):
    def test_compact_clears_mark(self):
        self.hook(bash_event("prose.py sample --genre task"))
        self.assertTrue(self.marked())
        r = self.hook(start_event(source="compact"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.marked())

    def test_plain_startup_keeps_mark(self):
        self.hook(bash_event("prose.py sample --genre task"))
        r = self.hook(start_event(source="startup"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(self.marked())

    def test_resume_keeps_mark(self):
        self.hook(bash_event("prose.py sample --genre task"))
        r = self.hook(start_event(source="resume"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(self.marked())

    def test_compact_clears_only_its_own_context(self):
        self.hook(bash_event("prose.py sample --genre task", agent="a1"))
        r = self.hook(start_event(source="compact", agent="a1"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.marked("s1.a1"))


class TestNoSession(MarkCase):
    def test_missing_session_id_is_noop(self):
        raw = json.loads(bash_event("prose.py sample --genre task"))
        del raw["session_id"]
        r = self.hook(json.dumps(raw))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(os.listdir(self.state), [])


if __name__ == "__main__":
    unittest.main()
