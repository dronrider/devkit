"""Сквозной тест сценария переноса на фикстурах вместо клиентов (DK-1624).

Фикстуры изображают подтверждённое поведение живых клиентов из ручных проб
диспетчера, поэтому тест проверяет механику сценария целиком: обе фазы,
перезапуск с resume, повторный импорт с дублями, сжатие, обратную подачу и
отчёт. Исходы живого прогона остаются за диспетчером, тут они предсказаны
фикстурами.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

DIR = Path(__file__).resolve().parent
DRIVER = DIR / "chat-port-check.py"


class Stand(unittest.TestCase):
    def run_driver(self, extra_env=None):
        self.work = Path(tempfile.mkdtemp(prefix="dk1624_work_"))
        self.out = Path(tempfile.mkdtemp(prefix="dk1624_out_"))
        self.env = dict(os.environ)
        self.env["FAKE_CODEX_STATE"] = str(self.out / "fake_state.json")
        if extra_env:
            self.env.update(extra_env)
        return self.drive()

    def drive(self):
        """Прогон сценария в self.work/self.out с текущим self.env."""
        done = subprocess.run(self.build_argv(), capture_output=True,
                              text=True, env=self.env,
                              stdin=subprocess.DEVNULL, timeout=180)
        self.done = done
        self.report = json.loads(
            (self.out / "report.json").read_text(encoding="utf-8"))
        return done

    def build_argv(self):
        return [
            sys.executable, str(DRIVER),
            "--workdir", str(self.work), "--out", str(self.out),
            "--claude-cmd", "%s %s" % (sys.executable,
                                       DIR / "fake_claude_client.py"),
            "--codex-cmd", "%s %s" % (sys.executable,
                                      DIR / "fake_codex_server.py"),
            "--codex-model", "fake-model", "--timeout", "60",
        ]

    def tearDown(self):
        for path in (getattr(self, "work", None),
                     getattr(self, "out", None)):
            if path is not None and path.exists():
                shutil.rmtree(path, ignore_errors=True)

    def table_row(self, direction, kind):
        for row in self.report["table"]:
            if row["direction"] == direction and row["kind"] == kind:
                return row
        raise AssertionError("нет строки таблицы %s / %s" % (direction, kind))


class HappyPathTest(Stand):
    def test_full_chain(self):
        done = self.run_driver()
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.report["errors"], [])
        self.assertIn("отчёт:", done.stdout)

        phase = self.report["stages"]["claude_phase"]
        self.assertEqual(phase["exit"], 0)
        self.assertFalse(phase["is_error"])
        self.assertTrue(phase["edit_file_written"])
        self.assertTrue(phase["assistant_fact_found"])
        self.assertTrue(phase["hidden_fact_found"])
        self.assertEqual(sorted(phase["marks_found"]),
                         ["mark_end", "mark_mid", "mark_start"])
        self.assertGreater(
            self.report["limits"]["long_output_bytes"], 5000)

        items = self.report["stages"]["history_items"]
        self.assertTrue(items["seed_user_message_restored"])
        self.assertEqual(items["kinds"],
                         {"message": 3, "function_call": 2,
                          "function_call_output": 2})

        round1 = self.report["stages"]["codex_round1"]
        self.assertTrue(round1["inject_accepted"])
        self.assertFalse(
            round1["feed_visibility"]["thread_read_shows_injected"])
        self.assertFalse(
            round1["feed_visibility"]["items_list_shows_injected"])
        self.assertEqual(round1["turn_status"], "completed")
        self.assertTrue(all(round1["match"].values()))

        round2 = self.report["stages"]["codex_round2"]
        self.assertTrue(all(round2["restart_match"].values()))
        self.assertEqual(round2["count_reported"], 4)
        self.assertEqual(round2["duplicates"], "да")
        self.assertTrue(round2["exact_before"]["long_command"].startswith(
            "printf 'MARK START"))
        self.assertTrue(round2["compaction_seen"])
        self.assertIsNone(round2["exact_after"]["long_command"])
        self.assertTrue(round2["codex_fact_echoed"])

        attempts = self.report["stages"]["reverse"]["attempts"]
        self.assertEqual(len(attempts), 1)
        self.assertEqual(attempts[0]["tag"], "with_tools")
        self.assertTrue(all(attempts[0]["match"].values()))
        self.assertTrue(attempts[0]["match"]["hidden_tool_fact"])
        self.assertEqual(self.report["stages"]["reverse_outcome_tools"],
                         "перенесено")

        self.assertEqual(
            self.table_row("claude->codex", "текст пользователя")["outcome"],
            "перенесено")
        self.assertEqual(
            self.table_row("claude->codex",
                           "результат инструмента: длинный вывод")["outcome"],
            "перенесено")
        self.assertEqual(self.table_row(
            "claude->codex",
            "те же факты после перезапуска app-server")["outcome"],
            "перенесено")
        self.assertEqual(self.table_row(
            "claude->codex", "повторный импорт без дублей")["outcome"],
            "потеряно")
        self.assertEqual(self.table_row(
            "claude->codex", "сжатие: факты против точных строк")["outcome"],
            "потеряно")
        self.assertEqual(
            self.table_row("codex->claude", "история инструментов")["outcome"],
            "перенесено")

        report_text = json.dumps(self.report, ensure_ascii=False)
        self.assertNotIn(str(self.work.resolve()), report_text)
        self.assertNotIn(str(self.out.resolve()), report_text)
        self.assertEqual(self.report["setup"]["workdir"],
                         self.work.resolve().name)


class RejectToolsTest(Stand):
    def test_fallback_without_tool_history(self):
        done = self.run_driver({"FAKE_CLAUDE_REJECT_TOOLS": "1"})
        self.assertEqual(done.returncode, 0, done.stderr)
        attempts = self.report["stages"]["reverse"]["attempts"]
        self.assertEqual([attempt["tag"] for attempt in attempts],
                         ["with_tools", "text_only"])
        self.assertNotEqual(attempts[0]["exit"], 0)
        self.assertIn("InputValidationError", attempts[0]["stderr"])
        self.assertEqual(attempts[1]["exit"], 0)
        text_only = attempts[1]["match"]
        self.assertTrue(text_only["user_fact"])
        self.assertTrue(text_only["assistant_fact"])
        self.assertTrue(text_only["codex_fact"])
        self.assertFalse(text_only["mark_start"])
        self.assertFalse(text_only["mark_end"])
        self.assertTrue(text_only["mark_mid"])
        self.assertFalse(text_only["hidden_tool_fact"])
        self.assertEqual(self.report["stages"]["reverse_outcome_tools"],
                         "не поддерживается")
        self.assertEqual(
            self.table_row("codex->claude", "текст пользователя")["outcome"],
            "перенесено")
        self.assertEqual(
            self.table_row("codex->claude", "история инструментов")["outcome"],
            "не поддерживается")


class RejectInjectTest(Stand):
    def test_inject_refusal_is_recorded(self):
        done = self.run_driver({"FAKE_CODEX_REJECT_INJECT": "1"})
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.report["errors"], [])
        round1 = self.report["stages"]["codex_round1"]
        self.assertFalse(round1["inject_accepted"])
        self.assertIn("запрещён фикстурой", round1["inject_error"])
        self.assertNotIn("match", round1)
        self.assertEqual(
            self.table_row("claude->codex", "текст пользователя")["outcome"],
            "не проверено")
        self.assertEqual(
            self.table_row("codex->claude", "текст пользователя")["outcome"],
            "перенесено")


class RerunRefusalTest(Stand):
    def test_second_run_refuses_instead_of_touching_dirs(self):
        self.run_driver()
        argv = self.build_argv()
        again = subprocess.run(argv, capture_output=True, text=True,
                               env=self.env, stdin=subprocess.DEVNULL,
                               timeout=180)
        self.assertEqual(again.returncode, 2)
        self.assertIn("свежем каталоге", again.stderr)
        # Прошлый прогон не тронут: отчёт и проекты остались на месте.
        self.assertTrue((self.out / "report.json").exists())
        self.assertTrue((self.work / "claude_proj" / "notes.txt").exists())

    def test_used_out_refuses_too(self):
        self.run_driver()
        fresh_work = Path(tempfile.mkdtemp(prefix="dk1624_work_"))
        self.addCleanup(shutil.rmtree, fresh_work, ignore_errors=True)
        argv = self.build_argv()
        argv[argv.index("--workdir") + 1] = str(fresh_work)
        again = subprocess.run(argv, capture_output=True, text=True,
                               env=self.env, stdin=subprocess.DEVNULL,
                               timeout=180)
        self.assertEqual(again.returncode, 2)
        self.assertIn("report.json", again.stderr)


if __name__ == "__main__":
    unittest.main()
