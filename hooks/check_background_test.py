#!/usr/bin/env python3
"""Рубеж синхронности: фоновый запуск в headless-сессии отбивается, в живой
проходит, синхронный вызов не трогается нигде. Окружение каждому прогону
собирается тут же и целиком: вид сессии считается по нему, и переменные той
сессии, в которой идёт сам прогон, сделали бы исход тестов машинозависимым."""
import importlib.util
import io
import json
import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
TOOL = os.path.join(HERE, "check-background.py")
DATA = os.path.join(HERE, "testdata", "claude-code")

_spec = importlib.util.spec_from_file_location("check_background", TOOL)
hook = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(hook)

HEADLESS = {"CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}
LIVE = {"CLAUDE_CODE_ENTRYPOINT": "claude-vscode"}


def run(*args, **kw):
    env = dict(kw.pop("env", {}))
    env.setdefault("PATH", os.environ.get("PATH", ""))
    env.setdefault("HOME", os.environ.get("HOME", ""))
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, env=env, **kw)


def event(tool, **fields):
    return json.dumps({"tool_name": tool, "tool_input": dict(fields)})


def sample(name):
    with open(os.path.join(DATA, name), encoding="utf-8") as f:
        return f.read()


class TestSessionKind(unittest.TestCase):
    """Чем сессия опознана. Ожидание тут выписано руками по значениям, которые
    клиент ставит сам: sdk-cli приезжает от `claude -p`, claude-vscode и cli от
    живого окна."""

    def test_print_mode_is_headless(self):
        self.assertIn("sdk-cli", hook.headless({"CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}))

    def test_sdk_sessions_are_headless(self):
        for point in ("sdk-ts", "sdk-py"):
            self.assertTrue(hook.headless({"CLAUDE_CODE_ENTRYPOINT": point}), point)

    def test_live_window_is_not_headless(self):
        for point in ("cli", "claude-vscode"):
            self.assertEqual(hook.headless({"CLAUDE_CODE_ENTRYPOINT": point}), "", point)

    def test_empty_environment_is_not_headless(self):
        self.assertEqual(hook.headless({}), "")

    def test_run_depth_outweighs_inherited_entrypoint(self):
        # Ребёнок `agentctl run` доносит до себя entrypoint живого родителя, и
        # без этой ветки headless-сессия конвейера считалась бы живой.
        sign = hook.headless({"CLAUDE_CODE_ENTRYPOINT": "claude-vscode",
                              "DEVKIT_RUN_DEPTH": "1"})
        self.assertIn("agentctl run", sign)


class TestBackgroundField(unittest.TestCase):
    def test_true_is_background(self):
        self.assertTrue(hook.wants_background("Bash", {"run_in_background": True}))

    def test_word_true_is_background(self):
        self.assertTrue(hook.wants_background("Bash", {"run_in_background": "true"}))

    def test_false_is_synchronous_for_both_tools(self):
        for tool in ("Bash", "Agent"):
            self.assertFalse(hook.wants_background(tool, {"run_in_background": False}), tool)
            self.assertFalse(hook.wants_background(tool, {"run_in_background": "false"}), tool)

    def test_missing_field_is_synchronous_for_bash(self):
        self.assertFalse(hook.wants_background("Bash", {"command": "ls"}))

    def test_missing_field_is_background_for_delegation(self):
        # Замер на ревью DK-678: вызов делегирования без поля возвращает
        # status = "async_launched", то есть дефолт харнеса тут асинхронный.
        self.assertTrue(hook.wants_background("Agent", {"subagent_type": "exec-high"}))


class TestHookRefusal(unittest.TestCase):
    def test_background_bash_is_refused(self):
        r = run("--hook", env=HEADLESS,
                input=event("Bash", command="sleep 300", run_in_background=True))
        self.assertEqual(r.returncode, 2)
        self.assertIn("синхронно", r.stderr)
        self.assertIn("sdk-cli", r.stderr)
        # Предлог тут не украшение: без него первая строка отказа читается
        # обрубком («инструмент Bash зван run_in_background: true»).
        self.assertIn("зван с run_in_background: true", r.stderr)

    def test_refusal_names_the_replacement(self):
        r = run("--hook", env=HEADLESS,
                input=event("Bash", command="shipctl merge DK-1", run_in_background=True))
        self.assertIn("timeout", r.stderr)
        self.assertIn("600000", r.stderr)

    def test_async_subagent_is_refused(self):
        r = run("--hook", env=HEADLESS,
                input=event("Agent", subagent_type="exec-high", run_in_background=True))
        self.assertEqual(r.returncode, 2)
        self.assertIn("Agent", r.stderr)

    def test_run_depth_session_is_refused(self):
        r = run("--hook", env={"CLAUDE_CODE_ENTRYPOINT": "claude-vscode",
                               "DEVKIT_RUN_DEPTH": "1"},
                input=event("Bash", command="sleep 300", run_in_background=True))
        self.assertEqual(r.returncode, 2)
        self.assertIn("DEVKIT_RUN_DEPTH", r.stderr)

    def test_live_session_keeps_background(self):
        r = run("--hook", env=LIVE,
                input=event("Bash", command="sleep 300", run_in_background=True))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")

    def test_synchronous_call_passes_in_headless(self):
        r = run("--hook", env=HEADLESS, input=event("Bash", command="go test ./..."))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")

    def test_background_false_passes_in_headless(self):
        r = run("--hook", env=HEADLESS,
                input=event("Agent", subagent_type="exec-high", run_in_background=False))
        self.assertEqual(r.returncode, 0)

    def test_delegation_without_field_is_refused(self):
        r = run("--hook", env=HEADLESS, input=event("Agent", subagent_type="exec-high"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("без поля run_in_background", r.stderr)
        self.assertIn("run_in_background: false", r.stderr)

    def test_delegation_without_field_passes_in_live_session(self):
        r = run("--hook", env=LIVE, input=event("Agent", subagent_type="exec-high"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")

    def test_event_without_input_passes(self):
        r = run("--hook", env=HEADLESS, input=json.dumps({"tool_name": "Bash"}))
        self.assertEqual(r.returncode, 0)

    def test_bad_json_passes(self):
        self.assertEqual(run("--hook", env=HEADLESS, input="not json").returncode, 0)

    def test_unknown_protocol_is_named(self):
        r = run("--hook", "кодекс", env=HEADLESS,
                input=event("Bash", command="sleep 300", run_in_background=True))
        self.assertEqual(r.returncode, 2)
        self.assertIn("не заведён", r.stderr)


class TestSampleEvents(unittest.TestCase):
    """Живые снимки событий: форма входа лежит на стороне харнеса, и признак
    фона в ней фиксирует образец, снятый с работающего клиента, а не память."""

    def test_live_background_bash_is_refused(self):
        r = run("--hook", env=HEADLESS, input=sample("pre-tool-use-bash-background.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("Bash", r.stderr)

    def test_live_background_bash_passes_in_live_session(self):
        r = run("--hook", env=LIVE, input=sample("pre-tool-use-bash-background.json"))
        self.assertEqual(r.returncode, 0)

    def test_live_async_agent_is_refused(self):
        r = run("--hook", env=HEADLESS, input=sample("pre-tool-use-agent-async.json"))
        self.assertEqual(r.returncode, 2)

    def test_live_synchronous_bash_passes(self):
        r = run("--hook", env=HEADLESS, input=sample("pre-tool-use-bash.json"))
        self.assertEqual(r.returncode, 0)

    def test_live_plain_delegation_is_refused(self):
        # Образец снят с headless-прогона, в котором про фон не было сказано ни
        # слова, а харнес всё равно вернул async_launched.
        r = run("--hook", env=HEADLESS, input=sample("pre-tool-use-agent-plain.json"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("Agent", r.stderr)


class TestWhy(unittest.TestCase):
    """Рубеж молчит, пока пропускает, и вид сессии иначе виден только фоновым
    вызовом наугад."""

    def test_headless_session_is_named(self):
        out = io.StringIO()
        hook.run_why(HEADLESS, out)
        self.assertIn("headless", out.getvalue())
        self.assertIn("sdk-cli", out.getvalue())

    def test_live_session_is_named(self):
        out = io.StringIO()
        hook.run_why(LIVE, out)
        self.assertIn("живая", out.getvalue())

    def test_why_runs_as_command(self):
        r = run("--why", env=HEADLESS)
        self.assertEqual(r.returncode, 0)
        self.assertIn("headless", r.stdout)

    def test_bare_call_shows_usage(self):
        r = run(env=HEADLESS)
        self.assertEqual(r.returncode, 2)
        self.assertIn("--hook", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
