#!/usr/bin/env python3
"""Проверка текста коммита: следы ассистента, развёрнутое body, тип префикса
против истории проекта и разбор в тексте находки. Отсюда же проверяется точка
входа commit-msg целиком: она гоняет эту проверку и проверку символов.

История подаётся проверке на stdin, как её подаёт хук (git log --format=%s).
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-commit.py")
DASH = "\u2014"  # длинное тире

# История длиннее порога check-commit (HISTORY_MIN): перечень префиксов
# становится нормой проекта только на ней, короткую проверка не судит.
HIST = "\n".join(["feat(core): раз", "fix: два", "docs: три", "fix: четыре",
                  "feat: пять", "docs: шесть", "fix: семь", "feat: восемь",
                  "docs: девять", "fix: десять"]) + "\n"

# Подписи стоят прямо в subject, без родовых «co-authored-by» и «generated
# with»: те ловятся и без перечня, а перечень тут и проверяется.
TRACES = ("noreply@anthropic.com", "codex@openai.com", "noreply@openai.com",
          "cursoragent@cursor.com", "aider@aider.chat", "openhands@all-hands.dev",
          "devin-ai-integration", "copilot-swe-agent", "google-labs-jules")


class TestCheckCommit(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.msg = os.path.join(self.tmp, "msg")

    def run_tool(self, text, hist=HIST):
        with open(self.msg, "w", encoding="utf-8") as f:
            f.write(text)
        return subprocess.run([sys.executable, TOOL, self.msg], input=hist,
                              capture_output=True, text=True)

    def test_clean_message_passes(self):
        self.assertEqual(self.run_tool("fix: чистая строка\n").returncode, 0)

    def test_assistant_trace_is_caught(self):
        r = self.run_tool("fix: правка\n\nCo-authored-by: Claude "
                          "<noreply@anthropic.com>\n")
        self.assertEqual(r.returncode, 1)
        self.assertIn("след ассистента", r.stdout)

    def test_body_is_caught(self):
        r = self.run_tool("fix: правка\n\nразвёрнутое body с деталями\n")
        self.assertEqual(r.returncode, 1)
        self.assertIn("одной строкой", r.stdout)

    def test_git_comments_are_not_read(self):
        r = self.run_tool("fix: чисто\n# Co-authored-by в комментарии\n")
        self.assertEqual(r.returncode, 0)

    def test_every_known_trace(self):
        for trace in TRACES:
            r = self.run_tool("fix: правка от %s\n" % trace)
            self.assertEqual(r.returncode, 1, trace)
            self.assertIn("след ассистента", r.stdout)

    def test_alien_address_is_not_a_trace(self):
        r = self.run_tool("fix: правка от noreply@example.com про генерацию отчёта\n")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_type_out_of_history_is_caught(self):
        r = self.run_tool("perf: чужой тип\n")
        self.assertEqual(r.returncode, 1)
        self.assertIn("не встречается в истории", r.stdout)

    def test_revert_always_passes(self):
        self.assertEqual(self.run_tool("revert: XR-1 откат правки\n").returncode, 0)

    def test_empty_history_keeps_prefix_check_off(self):
        r = self.run_tool("perf: первый типизированный\n", hist="\n")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_fresh_project_keeps_prefix_check_off(self):
        # Свежий проект (DK-125): в истории один-два коммита подключения, и
        # нормой проекта их префиксы ещё не стали. Иначе первый же
        # «docs(tasks)» доски вставал бы находкой на проекте, который только
        # что завела devkitctl new.
        r = self.run_tool("docs(tasks): MP-001 в работу\n",
                          hist="chore: подключение devkit\n")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_short_history_keeps_prefix_check_off(self):
        r = self.run_tool("perf: чужой тип\n", hist="feat: раз\nfix: два\ndocs: три\n")
        self.assertEqual(r.returncode, 0, r.stdout)

    def test_advice_for_assistant_trace(self):
        r = self.run_tool("fix: правка\n\nCo-authored-by: X <noreply@anthropic.com>\n")
        self.assertIn("как переписать", r.stdout)
        self.assertIn("amend", r.stdout)

    def test_advice_for_prefix(self):
        r = self.run_tool("perf: чужой тип\n")
        self.assertIn("git log -n 100", r.stdout)


class TestCommitMsgHook(unittest.TestCase):
    """Хук целиком: он читает историю репозитория, поэтому и зовётся изнутри
    репозитория, а не снаружи, где история была бы чужой."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.msg = os.path.join(self.tmp, "msg")
        self.repo = os.path.join(self.tmp, "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)

    def seed(self, count):
        """Историю сеют глубже порога HISTORY_MIN: на паре коммитов проверка
        префикса молчит намеренно, и «чужой тип» тут не поймался бы."""
        for i in range(count):
            with open(os.path.join(self.repo, "f.txt"), "w", encoding="utf-8") as f:
                f.write("x%d\n" % i)
            subprocess.run(["git", "-C", self.repo, "add", "f.txt"], check=True)
            subprocess.run(["git", "-C", self.repo, "commit", "-qm",
                            "feat: сид истории %d" % i], check=True)

    def hook(self, text):
        with open(self.msg, "w", encoding="utf-8") as f:
            f.write(text)
        return subprocess.run([os.path.join(HERE, "commit-msg"), self.msg],
                              cwd=self.repo, capture_output=True, text=True).returncode

    def test_clean_message_passes(self):
        self.assertEqual(self.hook("fix: обычный коммит\n"), 0)

    def test_dash_in_message_is_caught(self):
        self.assertEqual(self.hook("fix: коммит с тире %s\n" % DASH), 1)

    def test_git_comments_are_not_read(self):
        self.assertEqual(self.hook("fix: чисто\n# комментарий с тире %s\n" % DASH), 0)

    def test_type_out_of_history_is_caught(self):
        self.seed(12)
        self.assertEqual(self.hook("perf: чужой тип\n"), 1)

    def test_type_from_history_passes(self):
        self.seed(12)
        self.assertEqual(self.hook("feat: свой тип\n"), 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
