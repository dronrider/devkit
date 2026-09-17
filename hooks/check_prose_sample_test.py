#!/usr/bin/env python3
"""Проверка сторожа выборки prose (DK-1024): PreToolUse-хук отбивает запись
долгоживущего текста без следа выборки подсказкой с командой и жанром,
пропускает запись со следом, не трогает путь вне конфига и путь в
исключениях, различает контекст субагента и диспетчера тем же порядком, что
check-reread.py (DK-608), и молчит вместо выдумывания списка, когда
kit/prose.toml неполон.

Боевой kit/prose.toml проверяется отдельно (`TestShippedConfig`): секции
`[sample]` и `[sample-<жанр>]` обязаны сойтись с жанрами, зашитыми в код
хука, иначе сторож молчит на живой машине.
"""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-prose-sample.py")


def load():
    spec = importlib.util.spec_from_file_location("check_prose_sample", TOOL)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, HERE)
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.pop(0)
    return mod


guard = load()

DEFAULT_GENRES = {
    "task": ["docs/tasks/*.md"],
    "lld": ["docs/lld/*.md"],
    "readme": ["README.md"],
    "skill": ["kit/skills/*/SKILL.md"],
}


def config(path, mode="block", exclude=None, genres=None):
    """Конфиг путей на диск. genres это жанр -> список путей, умолчание
    покрывает все четыре жанра из кода хука одним паттерном на директорию."""
    lines = ["[sample]", 'mode = "%s"' % mode,
             "exclude = [%s]" % ", ".join('"%s"' % e for e in (exclude or []))]
    for genre, paths in (genres if genres is not None else DEFAULT_GENRES).items():
        lines.append("[sample-%s]" % genre)
        lines.append("paths = [%s]" % ", ".join('"%s"' % p for p in paths))
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    return path


def run(*args, **kw):
    env = dict(os.environ)
    env.update(kw.pop("env", {}))
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, env=env, **kw)


def write_event(path, cwd, session="s1", agent=None, tool="Write", multi=False):
    if multi:
        ti = {"file_path": path, "edits": [{"old_string": "a", "new_string": "b"}]}
    else:
        ti = {"file_path": path, "content": "текст"}
    e = {"session_id": session, "cwd": cwd, "hook_event_name": "PreToolUse",
         "tool_name": tool, "tool_input": ti}
    if agent:
        e["agent_id"] = agent
    return json.dumps(e)


class GuardCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        os.makedirs(os.path.join(self.tmp, ".git"))
        os.makedirs(os.path.join(self.tmp, "docs", "tasks"))
        self.path = os.path.join(self.tmp, "docs", "tasks", "DK-1.md")
        self.conf = os.path.join(self.tmp, "prose.toml")
        self.state = tempfile.mkdtemp()
        config(self.conf)

    def mark(self, context="s1"):
        os.makedirs(self.state, exist_ok=True)
        with open(os.path.join(self.state, context + ".json"), "w", encoding="utf-8") as f:
            f.write("{}")

    def hook(self, session="s1", agent=None, path=None, tool="Write", multi=False):
        return run("--hook", "--state", self.state,
                   env={guard.CONFIG_ENV: self.conf},
                   input=write_event(path or self.path, self.tmp, session=session,
                                     agent=agent, tool=tool, multi=multi))


class TestBlockWithoutMark(GuardCase):
    def test_write_without_sample_is_blocked(self):
        r = self.hook()
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("prose.py sample", r.stderr)
        self.assertIn("--genre task", r.stderr)
        self.assertIn(self.path, r.stderr)

    def test_hint_has_trailing_newline(self):
        # Без него harness склеивает подсказку со следующим выводом.
        r = self.hook()
        self.assertTrue(r.stderr.endswith("\n"), repr(r.stderr))

    def test_multi_edit_is_covered(self):
        # Дыра без MultiEdit в матчере POST_MATCHER не должна повториться на
        # новом рубеже: guard разбирает edits тем же parse_write, что и
        # Write/Edit.
        r = self.hook(tool="MultiEdit", multi=True)
        self.assertEqual(r.returncode, 2, r.stderr)


class TestPassesWithMark(GuardCase):
    def test_write_passes_with_mark(self):
        self.mark()
        r = self.hook()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr, "")

    def test_warn_mode_does_not_block(self):
        config(self.conf, mode="warn")
        r = self.hook()
        self.assertEqual(r.returncode, 0, r.stderr)
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("prose.py sample", said)


class TestGenreByPath(GuardCase):
    def test_readme_genre_named(self):
        readme = os.path.join(self.tmp, "README.md")
        r = self.hook(path=readme)
        self.assertIn("--genre readme", r.stderr)

    def test_skill_genre_named(self):
        skill_dir = os.path.join(self.tmp, "kit", "skills", "prose")
        os.makedirs(skill_dir, exist_ok=True)
        path = os.path.join(skill_dir, "SKILL.md")
        r = self.hook(path=path)
        self.assertIn("--genre skill", r.stderr)


class TestUnknownPathPasses(GuardCase):
    def test_path_outside_config_passes(self):
        other = os.path.join(self.tmp, "src", "main.go")
        os.makedirs(os.path.dirname(other), exist_ok=True)
        r = self.hook(path=other)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stderr, "")


class TestExclude(GuardCase):
    def test_excluded_path_passes_even_under_genre(self):
        config(self.conf, exclude=["docs/tasks/DK-1.md"])
        r = self.hook()
        self.assertEqual(r.returncode, 0, r.stderr)


class TestSubagentContext(GuardCase):
    """Контекст субагента свой: выборка диспетчера, взятая под тем же
    session_id без agent_id, субагенту не засчитывается (DK-608)."""

    def test_dispatcher_mark_does_not_cover_subagent(self):
        self.mark("s1")
        r = self.hook(agent="a1")
        self.assertEqual(r.returncode, 2, r.stderr)

    def test_subagent_own_mark_passes(self):
        self.mark("s1.a1")
        r = self.hook(agent="a1")
        self.assertEqual(r.returncode, 0, r.stderr)


class TestNoSession(GuardCase):
    def test_missing_session_id_passes(self):
        raw = json.loads(write_event(self.path, self.tmp))
        del raw["session_id"]
        r = run("--hook", "--state", self.state, env={guard.CONFIG_ENV: self.conf},
               input=json.dumps(raw))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestConfigGaps(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "prose.toml")

    def test_missing_config_silent_on_hook(self):
        r = run("--hook", env={guard.CONFIG_ENV: self.conf},
               input=write_event("/tmp/x/docs/tasks/DK-1.md", "/tmp/x"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")

    def test_missing_file_names_gap(self):
        r = run("--config", env={guard.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("файла нет", r.stdout)

    def test_incomplete_config_names_gaps(self):
        with open(self.conf, "w", encoding="utf-8") as f:
            f.write('[sample]\nmode = "block"\n')
        r = run("--config", env={guard.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("[sample-task] paths", r.stdout)
        self.assertIn("[sample-lld] paths", r.stdout)


class TestShippedConfig(unittest.TestCase):
    def test_shipped_config_is_complete(self):
        shipped = os.path.join(os.path.dirname(HERE), "kit", "prose.toml")
        r = run("--config", env={guard.CONFIG_ENV: shipped})
        self.assertEqual(r.returncode, 0, r.stdout)


if __name__ == "__main__":
    unittest.main()
