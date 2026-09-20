#!/usr/bin/env python3
"""Самопроверка писателя этапов из хуков (DK-911): формат записи тот же, что у
internal/stage на go, живой этап и его закрытие, приведение дерева задачи к
основному чекауту. Текст записи в первом тесте дословно тот, что читает
TestOldRecordReadsAsWait и TestCloseEndsLiveStageOfKind на go-стороне.
"""
import os
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import stagerun  # noqa: E402

AT = time.mktime((2026, 8, 15, 10, 0, 0, 0, 0, -1))


class Writer(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", self.home]))

    def read(self, root="/p", task="T-1"):
        with open(stagerun.path(self.home, root, task), encoding="utf-8") as f:
            return f.read()

    def test_put_writes_the_go_format(self):
        stagerun.put(self.home, "/p", "T-1", stagerun.REVIEW,
                     "субагент sonnet/high по определению review-high", "sess", AT, work="a1b2")
        self.assertEqual(
            self.read(),
            "# этапы задачи T-1: пишет конвейер devkit, читает дашборд\n"
            "id = T-1\nroot = /p\n"
            "этап = ревью | 2026-08-15T10:00:00 | субагент sonnet/high по определению review-high | sess |  | a1b2\n")

    def test_closed_stage_carries_its_end(self):
        stagerun.put(self.home, "/p", "T-1", stagerun.REVIEW, "субагент", "sess", AT,
                     end=AT + 1200, work="a1b2")
        self.assertIsNone(stagerun.live(self.home, "/p", "T-1"))
        self.assertIn("| 2026-08-15T10:20:00 | a1b2", self.read())

    def test_close_ends_the_live_stage_of_its_kind(self):
        stagerun.put(self.home, "/p", "T-1", stagerun.REVIEW, "субагент", "sess", AT, work="a1b2")
        self.assertFalse(stagerun.close(self.home, "/p", "T-1", stagerun.MERGE, AT + 60))
        self.assertFalse(stagerun.close(self.home, "/p", "T-1", stagerun.REVIEW, AT + 60, work="zzz"))
        self.assertTrue(stagerun.close(self.home, "/p", "T-1", stagerun.REVIEW, AT + 600,
                                       "ходов 12, минут 10", "a1b2"))
        rec = stagerun.load(stagerun.path(self.home, "/p", "T-1"))
        self.assertEqual(rec["stages"][0]["end"], "2026-08-15T10:10:00")
        self.assertEqual(rec["stages"][0]["note"], "субагент, ходов 12, минут 10")
        self.assertFalse(stagerun.close(self.home, "/p", "T-1", stagerun.REVIEW, AT + 700))

    def test_closed_stage_on_top_keeps_the_live_one(self):
        # DK-911, замечание ревью: синхронная вычитка внутри идущей разработки
        # ложится закрытой поверх, живой остаётся разработка, и её конец
        # находит свой этап под вычиткой.
        stagerun.put(self.home, "/p", "T-1", stagerun.DEV, "субагент", "sess", AT, work="d1")
        stagerun.put(self.home, "/p", "T-1", stagerun.PROOF, "вычитка", "sess", AT + 600,
                     end=AT + 900, work="p1")
        self.assertEqual(stagerun.live(self.home, "/p", "T-1")["kind"], stagerun.DEV)
        self.assertTrue(stagerun.close(self.home, "/p", "T-1", stagerun.DEV, AT + 3600, "минут 60", "d1"))
        self.assertIsNone(stagerun.live(self.home, "/p", "T-1"))
        rec = stagerun.load(stagerun.path(self.home, "/p", "T-1"))
        self.assertEqual([s["end"] for s in rec["stages"]],
                         ["2026-08-15T11:00:00", "2026-08-15T10:15:00"])

    def test_close_names_the_executor_from_the_model(self):
        # DK-911, второй круг ревью: конец этапа вписывает модель субагента в
        # запись без исполнителя, запись с моделью не трогает.
        stagerun.put(self.home, "/p", "T-1", stagerun.DEV, "субагент по определению exec-high, работа d1",
                     "sess", AT, work="d1")
        self.assertTrue(stagerun.close(self.home, "/p", "T-1", stagerun.DEV, AT + 60, "минут 1", "d1",
                                       model="haiku"))
        rec = stagerun.load(stagerun.path(self.home, "/p", "T-1"))
        self.assertEqual(rec["stages"][0]["note"], "субагент haiku/high по определению exec-high, работа d1, минут 1")
        self.assertEqual(stagerun.name_executor("субагент opus/high по определению exec-high", "haiku"),
                         "субагент opus/high по определению exec-high")
        self.assertEqual(stagerun.name_executor("субагент по определению proofread", "sonnet"),
                         "субагент sonnet по определению proofread")
        self.assertEqual(stagerun.name_executor("субагент по определению exec-low", ""),
                         "субагент по определению exec-low")

    def test_last_work_skips_waits(self):
        stagerun.put(self.home, "/p", "T-1", stagerun.REVIEW, "", "", AT)
        stagerun.put(self.home, "/p", "T-1", stagerun.WAIT_HUMAN, "вопрос", "", AT + 60)
        self.assertEqual(stagerun.last_work(self.home, "/p", "T-1")["kind"], stagerun.REVIEW)
        self.assertIsNone(stagerun.last_work(self.home, "/p", "T-9"))

    def test_old_record_is_kept_whole(self):
        # Запись прежней сборки без конца и работы дописывается, а не рвётся.
        os.makedirs(stagerun.runs_dir(self.home))
        with open(stagerun.path(self.home, "/p", "T-1"), "w", encoding="utf-8") as f:
            f.write("id = T-1\nroot = /p\nэтап = разработка | 2026-08-15T09:00:00 | opus/high по вердикту pick | s1\n")
        stagerun.put(self.home, "/p", "T-1", stagerun.REVIEW, "субагент", "s2", AT)
        rec = stagerun.load(stagerun.path(self.home, "/p", "T-1"))
        self.assertEqual([s["kind"] for s in rec["stages"]], ["разработка", "ревью"])
        self.assertEqual(rec["stages"][0]["session"], "s1")

    def test_unknown_kind_is_refused(self):
        with self.assertRaises(ValueError):
            stagerun.put(self.home, "/p", "T-1", "снаружи", "", "", AT)

    def test_separator_stays_out_of_the_note(self):
        stagerun.put(self.home, "/p", "T-1", stagerun.DEV, "a | b\nc", "", AT)
        self.assertEqual(stagerun.load(stagerun.path(self.home, "/p", "T-1"))["stages"][0]["note"], "a / b c")

    def test_slug_matches_go(self):
        self.assertEqual(stagerun.slug("/Users/rider/projects/devkit"), "Users-rider-projects-devkit")

    def test_work_note_reads_turns_from_the_report(self):
        self.assertEqual(stagerun.work_note("Ревью прошло, ходов 14, замечаний нет", 543), "ходов 14, минут 9")
        self.assertEqual(stagerun.work_note("без числа", 100), "минут 2")


class MainRoot(unittest.TestCase):
    def test_worktree_folds_to_the_checkout(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", tmp]))
        main = os.path.join(tmp, "proj")
        subprocess.run(["git", "init", "-q", "-b", "main", main], check=True)
        subprocess.run(["git", "-C", main, "-c", "user.email=t@t", "-c", "user.name=t",
                        "commit", "-q", "--allow-empty", "-m", "seed"], check=True)
        wt = os.path.join(tmp, "proj-dk-1")
        subprocess.run(["git", "-C", main, "worktree", "add", "-q", wt, "-b", "dk-1"], check=True)
        self.assertEqual(os.path.realpath(stagerun.main_root(wt)), os.path.realpath(main))
        self.assertEqual(os.path.realpath(stagerun.main_root(main)), os.path.realpath(main))

    def test_non_git_root_is_returned_as_is(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", tmp]))
        self.assertEqual(stagerun.main_root(tmp), tmp)


if __name__ == "__main__":
    unittest.main()
