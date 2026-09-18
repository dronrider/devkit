#!/usr/bin/env python3
"""Самопроверка указателя на скилл chat (DK-1032): хук на UserPromptSubmit,
который кладёт CHAT_RULE/NO_TASK_CHAT_RULE только тогда, когда скилл chat в
транскрипте после последней границы сжатия ещё не звался. Разбор функций идёт
прямыми вызовами (быстро и точно про хвост файла), а прогон хука целиком
подпроцессом с подсунутым stdin, как у остальных хуков в этом каталоге.
"""
import importlib
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "chat-pointer.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "prompt-submit.json")

sys.path.insert(0, HERE)
import hookio  # noqa: E402
chat_pointer = importlib.import_module("chat-pointer")


def sample():
    with open(SAMPLE, encoding="utf-8") as f:
        return json.load(f)


def assistant_skill_call(skill="chat"):
    """Строка транскрипта: ход ассистента с вызовом Skill заданного имени.
    Форма та же, что снята живьём (docstring hooks/chat-pointer.py)."""
    return json.dumps({"type": "assistant", "message": {"content": [
        {"type": "tool_use", "name": "Skill", "input": {"skill": skill}}]}})


def assistant_text(text="Ответ."):
    return json.dumps({"type": "assistant", "message": {"content": [
        {"type": "text", "text": text}]}})


def compact_boundary():
    return json.dumps({"type": "system", "subtype": "compact_boundary"})


class TestTailLines(unittest.TestCase):
    def test_missing_file_is_an_empty_tail(self):
        self.assertEqual(chat_pointer.tail_lines("/no/such/file"), [])

    def test_small_file_comes_back_whole(self):
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as f:
            f.write("одна строка\nдругая строка\n")
            path = f.name
        self.addCleanup(os.remove, path)
        self.assertEqual(chat_pointer.tail_lines(path), ["одна строка", "другая строка"])

    def test_big_file_reads_only_the_tail(self):
        # Файл больше хвостового окна: в начале мусор, который в хвост попасть
        # не должен, к концу метка, которую и ищет проверка.
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as f:
            f.write("мусор " * 100000 + "\n")
            f.write("метка-в-хвосте\n")
            path = f.name
        self.addCleanup(os.remove, path)
        lines = chat_pointer.tail_lines(path, size=4096)
        self.assertIn("метка-в-хвосте", lines)
        self.assertNotIn("мусор " * 100000, lines)
        self.assertLess(sum(len(l) for l in lines), 4096 + 1)


class TestSkillChatCall(unittest.TestCase):
    def test_skill_chat_tool_use_is_a_call(self):
        event = json.loads(assistant_skill_call())
        self.assertTrue(chat_pointer.skill_chat_call(event))

    def test_other_skill_is_not_a_call(self):
        event = json.loads(assistant_skill_call("work-plan"))
        self.assertFalse(chat_pointer.skill_chat_call(event))

    def test_plain_text_is_not_a_call(self):
        event = json.loads(assistant_text())
        self.assertFalse(chat_pointer.skill_chat_call(event))

    def test_user_event_is_not_a_call(self):
        # Эхо «Skill /chat is already loaded above» харнес кладёт типом user,
        # не assistant: разбору тут смотреть незачем.
        event = {"type": "user", "isMeta": True,
                 "message": {"content": "Skill /chat is already loaded above"}}
        self.assertFalse(chat_pointer.skill_chat_call(event))


class TestCalledSinceCompact(unittest.TestCase):
    def write(self, lines):
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as f:
            for line in lines:
                f.write(line + "\n")
            path = f.name
        self.addCleanup(os.remove, path)
        return path

    def test_no_file_means_not_called(self):
        self.assertFalse(chat_pointer.called_since_compact("/no/such/file"))

    def test_empty_transcript_means_not_called(self):
        path = self.write([])
        self.assertFalse(chat_pointer.called_since_compact(path))

    def test_call_with_no_boundary_counts(self):
        path = self.write([assistant_text(), assistant_skill_call(), assistant_text()])
        self.assertTrue(chat_pointer.called_since_compact(path))

    def test_no_call_at_all_is_not_called(self):
        path = self.write([assistant_text(), assistant_text()])
        self.assertFalse(chat_pointer.called_since_compact(path))

    def test_call_before_the_boundary_does_not_count(self):
        # Вызов раньше единственной границы: после неё скилл не звался.
        path = self.write([assistant_skill_call(), compact_boundary(), assistant_text()])
        self.assertFalse(chat_pointer.called_since_compact(path))

    def test_call_after_the_boundary_counts(self):
        path = self.write([assistant_text(), compact_boundary(), assistant_skill_call()])
        self.assertTrue(chat_pointer.called_since_compact(path))

    def test_only_the_last_boundary_matters(self):
        # Вызов лежит между двумя границами: после последней его уже нет.
        path = self.write([compact_boundary(), assistant_skill_call(), compact_boundary(),
                           assistant_text()])
        self.assertFalse(chat_pointer.called_since_compact(path))

    def test_junk_lines_are_skipped(self):
        path = self.write(["не json", "", "  ", "[1,2,3]", assistant_skill_call()])
        self.assertTrue(chat_pointer.called_since_compact(path))


class TestHook(unittest.TestCase):
    """Хук целиком: команда из settings.json на живом событии."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", self.tmp]))
        self.transcript = os.path.join(self.tmp, "t.jsonl")

    def write_transcript(self, lines):
        with open(self.transcript, "w", encoding="utf-8") as f:
            for line in lines:
                f.write(line + "\n")

    def event(self):
        return dict(sample(), transcript_path=self.transcript)

    def run_hook(self, event, extra=None, args=("--hook",)):
        env = dict(os.environ)
        env.pop("DEVKIT_TASK", None)
        env.pop("DEVKIT_HIDDEN", None)
        env.update(extra or {})
        return subprocess.run([sys.executable, HOOK] + list(args),
                              input=json.dumps(event), capture_output=True,
                              text=True, env=env)

    def said(self, result):
        return json.loads(result.stdout)["hookSpecificOutput"]["additionalContext"]

    def test_no_transcript_yet_gets_the_pointer(self):
        # Первая реплика человека: транскрипта на диске ещё может не быть.
        os.remove(self.transcript) if os.path.exists(self.transcript) else None
        r = self.run_hook(self.event())
        self.assertEqual((r.returncode, r.stderr), (0, ""))
        self.assertIn("вызови скилл chat инструментом Skill", self.said(r))

    def test_task_order_says_po_zadache(self):
        self.write_transcript([])
        r = self.run_hook(self.event(), {"DEVKIT_TASK": "DK-1032"})
        self.assertEqual(r.returncode, 0)
        self.assertIn("по задаче", self.said(r))

    def test_no_task_order_skips_po_zadache(self):
        self.write_transcript([])
        r = self.run_hook(self.event())
        self.assertEqual(r.returncode, 0)
        self.assertNotIn("по задаче", self.said(r))

    def test_pointer_never_says_every_reply(self):
        self.write_transcript([])
        r = self.run_hook(self.event(), {"DEVKIT_TASK": "DK-1032"})
        self.assertEqual(r.returncode, 0)
        self.assertNotIn("на каждой реплике", self.said(r))

    def test_skill_already_called_is_silent(self):
        self.write_transcript([assistant_skill_call()])
        r = self.run_hook(self.event())
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))

    def test_hidden_session_is_silent_even_without_a_call(self):
        self.write_transcript([])
        r = self.run_hook(self.event(), {"DEVKIT_HIDDEN": "1"})
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))

    def test_hidden_session_is_silent_with_a_task(self):
        self.write_transcript([])
        r = self.run_hook(self.event(), {"DEVKIT_TASK": "DK-1032", "DEVKIT_HIDDEN": "1"})
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))

    def test_call_before_compact_still_gets_the_pointer(self):
        self.write_transcript([assistant_skill_call(), compact_boundary()])
        r = self.run_hook(self.event())
        self.assertEqual(r.returncode, 0)
        self.assertIn("вызови скилл chat инструментом Skill", self.said(r))

    def test_call_after_compact_is_silent(self):
        self.write_transcript([compact_boundary(), assistant_skill_call()])
        r = self.run_hook(self.event())
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))

    def test_other_events_are_silent(self):
        with open(os.path.join(HERE, "testdata", "claude-code", "turn-done.json"),
                  encoding="utf-8") as f:
            r = self.run_hook(json.load(f))
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))

    def test_broken_input_is_a_silent_zero(self):
        r = subprocess.run([sys.executable, HOOK, "--hook"], input="не json",
                           capture_output=True, text=True)
        self.assertEqual((r.returncode, r.stdout, r.stderr), (0, "", ""))

    def test_unknown_protocol_names_the_reason(self):
        r = self.run_hook(self.event(), args=("--hook", "кодекс"))
        self.assertEqual(r.returncode, 2)
        self.assertIn("кодекс", r.stderr)

    def test_without_the_key_it_prints_the_help(self):
        r = self.run_hook(self.event(), args=())
        self.assertEqual(r.returncode, 2)
        self.assertIn("chat-pointer.py --hook", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
