#!/usr/bin/env python3
"""Частичный прогон под потолком одновременных прогонов (DK-1219).

Живой `go test` тут не гоняется: проверяется обёртка, а не go. Синтетический
каталог с одним питоновым тестом даёт настоящий подпроцесс за доли секунды, а
го-ветка проверяется по собранной команде.
"""
import io
import json
import os
import shutil
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

import parallel
import testenv
import testrun

PASSING = """import unittest


class T(unittest.TestCase):
    def test_ok(self):
        self.assertTrue(True)
"""

FAILING = """import unittest


class T(unittest.TestCase):
    def test_red(self):
        self.assertTrue(False)
"""


class Stand(unittest.TestCase):
    """Синтетический проект: корень с `.git` и `.devkit`, пакет с тестом."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-testrun-"))
        (self.root / ".git").mkdir()
        (self.root / ".devkit").mkdir()
        self.slots = self.root / "slots"
        # Слоты стенда свои: иначе юнит соревновался бы за общий слот машины с
        # настоящим прогоном рядом, тем же приёмом, каким это делает стенд
        # parallel_test.
        self.patchers = [
            mock.patch.object(parallel, "SLOT_DIR", str(self.slots)),
            mock.patch.object(parallel, "SLOT_POLL_SECS", 0.01),
        ]
        for p in self.patchers:
            p.start()

    def tearDown(self):
        for p in self.patchers:
            p.stop()
        shutil.rmtree(str(self.root), ignore_errors=True)

    def pkg(self, name="pkg", body=PASSING):
        d = self.root / name
        testenv.write(d / "x_test.py", body)
        return d

    def journal(self):
        path = self.root / testrun.LOG_REL
        if not path.exists():
            return []
        return [json.loads(line) for line in
                path.read_text(encoding="utf-8").splitlines() if line.strip()]


class KindTest(Stand):
    def test_go_module_by_go_mod(self):
        d = self.root / "tools" / "x"
        testenv.write(d / "go.mod", "module x\n")
        self.assertEqual(testrun.kind(d), "go")

    def test_python_suite_by_test_files(self):
        self.assertEqual(testrun.kind(self.pkg()), "unittest")

    def test_own_runner_is_older_than_discover(self):
        d = self.pkg("own")
        testenv.write(d / "suite.py", "raise SystemExit(0)\n")
        self.assertEqual(testrun.kind(d), "suite")

    def test_directory_without_tests_has_no_kind(self):
        d = self.root / "docs"
        d.mkdir()
        self.assertIsNone(testrun.kind(d))

    def test_run_refuses_directory_without_tests(self):
        d = self.root / "docs"
        d.mkdir()
        out = io.StringIO()
        self.assertEqual(testrun.run(str(d), out=out), 2)
        self.assertIn("ни go.mod, ни suite.py, ни файлов *_test.py",
                      out.getvalue())
        self.assertEqual(self.journal(), [], "отказ в журнал не пишется")


class CommandTest(Stand):
    """Приращение niceness, а не абсолютный уровень: `nice -n` прибавляет к
    niceness вызывающего процесса, и сам прогон стенда бывает уже ницирован
    обёрткой (найдено прогоном этой сюиты через `devkitctl test`, где
    приращение сошлось к нулю). Ожидание считается той же разницей, что и в
    `parallel.with_priority`, а пропажу самой обёртки ловит первое слово."""

    def nice_prefix(self):
        return ["nice", "-n",
                str(parallel.NICE_LEVEL - os.getpriority(os.PRIO_PROCESS, 0))]

    def test_go_command_carries_share_priority_and_gowork(self):
        argv, env = testrun.build(self.root / "tools" / "x", "go", 4, [])
        self.assertEqual(argv[:3], self.nice_prefix())
        self.assertIn("-p", argv)
        self.assertEqual(argv[argv.index("-p") + 1], "4")
        self.assertEqual(argv[-1], "./...", "список пакетов идёт последним")
        self.assertEqual(env["GOWORK"], "off")

    def test_go_command_takes_extra_flags_after_packages(self):
        argv, _ = testrun.build(self.root / "tools" / "x", "go", 2,
                                ["-run", "TestX"])
        self.assertEqual(argv[-3:], ["./...", "-run", "TestX"])

    def test_own_timeout_replaces_the_default(self):
        argv, _ = testrun.build(self.root / "tools" / "x", "go", 2,
                                ["-timeout=40m"])
        self.assertIn("-timeout=40m", argv)
        self.assertNotIn("-timeout=" + testrun.GO_TIMEOUT, argv)
        self.assertEqual(len([t for t in argv if t.startswith("-timeout")]), 1)

    def test_suite_command_takes_the_share_as_jobs(self):
        d = self.pkg("own")
        testenv.write(d / "suite.py", "raise SystemExit(0)\n")
        argv, _ = testrun.build(d, "suite", 6, [])
        self.assertEqual(argv[-3:], ["suite.py", "-j", "6"])

    def test_named_test_goes_to_unittest_directly(self):
        argv, _ = testrun.build(self.pkg(), "unittest", 3, ["x_test.T.test_ok"])
        self.assertEqual(argv[-3:], ["-m", "unittest", "x_test.T.test_ok"])
        self.assertNotIn("discover", argv)

    def test_named_test_beats_the_own_runner(self):
        d = self.pkg("own")
        testenv.write(d / "suite.py", "raise SystemExit(0)\n")
        argv, _ = testrun.build(d, "suite", 6, ["x_test"])
        self.assertEqual(argv[-3:], ["-m", "unittest", "x_test"])

    def test_unittest_command_discovers_by_pattern(self):
        argv, env = testrun.build(self.pkg(), "unittest", 3, [])
        self.assertEqual(argv[:3], self.nice_prefix())
        self.assertEqual(argv[-4:], ["unittest", "discover", "-p", "*_test.py"])
        self.assertIsNone(env, "питоновая сюита идёт с окружением раннера")

    def test_run_filter_goes_to_unittest_k_for_python(self):
        # Хвост сценария пишется по образцу go (`-run DutyAgent`), а питоновая
        # сюита имени теста не принимает: ключ переводится в отбор unittest -k.
        d = self.pkg("own")
        testenv.write(d / "suite.py", "raise SystemExit(0)\n")
        argv, _ = testrun.build(d, "suite", 6, ["-run", "DutyAgent"])
        self.assertEqual(argv[-6:], ["unittest", "discover", "-p", "*_test.py",
                                     "-k", "*DutyAgent*"])
        self.assertNotIn("suite.py", argv)

    def test_run_filter_keeps_go_flag(self):
        argv, _ = testrun.build(self.root / "tools" / "x", "go", 2,
                                ["-run", "TestX"])
        self.assertEqual(argv[-3:], ["./...", "-run", "TestX"])


class RunTest(Stand):
    def test_green_run_returns_zero_and_prints_component_line(self):
        out = io.StringIO()
        self.assertEqual(testrun.run(str(self.pkg()), out=out), 0)
        self.assertIn("бюджет параллельности:", out.getvalue())
        self.assertRegex(out.getvalue(), r"pkg\s+\(pkg\)\s+\d+\.\ds ok")

    def test_red_run_returns_one_and_marks_journal(self):
        out = io.StringIO()
        self.assertEqual(testrun.run(str(self.pkg(body=FAILING)), out=out), 1)
        rec = self.journal()[-1]
        self.assertFalse(rec["ok"])
        self.assertFalse(rec["components"][0]["ok"])

    def test_journal_record_names_the_scope(self):
        testrun.run(str(self.pkg()), out=io.StringIO())
        rec = self.journal()[-1]
        self.assertEqual(rec["scope"], "pkg")
        self.assertEqual(rec["components"][0]["name"], "pkg")
        self.assertEqual(rec["diff"], [])
        self.assertTrue(rec["ok"])

    def test_journal_silent_without_devkit_directory(self):
        shutil.rmtree(str(self.root / ".devkit"))
        self.assertEqual(testrun.run(str(self.pkg()), out=io.StringIO()), 0)
        self.assertEqual(self.journal(), [])

    def test_file_argument_runs_its_directory(self):
        d = self.pkg()
        out = io.StringIO()
        self.assertEqual(testrun.run(str(d / "x_test.py"), out=out), 0)
        self.assertEqual(self.journal()[-1]["scope"], "pkg")


class TaskIDTest(Stand):
    def test_branch_of_task_gives_the_id(self):
        root = testenv.git_init(self.root / "proj")
        testenv.write(root / "a.txt", "a\n")
        testenv.git(root, "add", "a.txt")
        testenv.git(root, "-c", "core.hooksPath=", "commit", "-q", "-m", "init")
        testenv.git(root, "checkout", "-q", "-b", "dk-1219")
        self.assertEqual(testrun.task_id(root), "DK-1219")

    def test_main_branch_gives_no_id(self):
        root = testenv.git_init(self.root / "proj")
        testenv.write(root / "a.txt", "a\n")
        testenv.git(root, "add", "a.txt")
        testenv.git(root, "-c", "core.hooksPath=", "commit", "-q", "-m", "init")
        testenv.git(root, "checkout", "-q", "-b", "main")
        self.assertEqual(testrun.task_id(root), "")

    def test_outside_git_gives_no_id(self):
        d = Path(tempfile.mkdtemp(prefix="devkitctl-testrun-nogit-"))
        try:
            self.assertEqual(testrun.task_id(d), "")
        finally:
            shutil.rmtree(str(d), ignore_errors=True)


class SlotTest(Stand):
    """Частичный прогон стоит в той же очереди, что и полный (DoD DK-1219)."""

    def test_busy_slot_holds_the_partial_run_and_prints_the_wait_line(self):
        holder_entered = threading.Event()
        holder_may_release = threading.Event()

        def holder():
            with parallel.full_run_slot(n=1, dir_path=str(self.slots),
                                        poll=0.01, out=io.StringIO()):
                holder_entered.set()
                holder_may_release.wait(timeout=10)

        t = threading.Thread(target=holder)
        t.start()
        self.assertTrue(holder_entered.wait(timeout=10))

        out = io.StringIO()
        done = threading.Event()
        rc = []

        def waiter():
            rc.append(testrun.run(str(self.pkg()), out=out))
            done.set()

        w = threading.Thread(target=waiter)
        w.start()
        # Прогон обязан стоять в цикле ожидания, а не проскочить мимо занятого
        # слота: факт проверяется незавершённостью потока и строкой ожидания, а
        # не стенным временем самого прогона.
        for _ in range(200):
            if "занят" in out.getvalue():
                break
            time.sleep(0.01)
        self.assertIn("потолок 1 прогонов тестов занят", out.getvalue())
        self.assertFalse(done.is_set(), "занятый слот обязан держать прогон")

        holder_may_release.set()
        t.join(timeout=10)
        self.assertTrue(done.wait(timeout=60), "слот освободился, прогон пошёл")
        w.join(timeout=10)
        self.assertEqual(rc, [0])
        self.assertIn("потолок прогонов тестов освободился", out.getvalue())

    def test_free_slot_runs_without_waiting(self):
        out = io.StringIO()
        self.assertEqual(testrun.run(str(self.pkg()), out=out), 0)
        self.assertNotIn("жду свободный слот", out.getvalue())

    def test_full_run_wait_line_keeps_its_own_wording(self):
        # Метка по умолчанию не съехала: строку полного прогона читает человек
        # в выводе слияния, и она осталась прежней.
        out = io.StringIO()
        os.makedirs(str(self.slots), exist_ok=True)
        with parallel.full_run_slot(n=1, dir_path=str(self.slots), poll=0.01,
                                    out=io.StringIO()):
            waited = io.StringIO()
            stop = threading.Event()

            def waiter():
                with parallel.full_run_slot(n=1, dir_path=str(self.slots),
                                            poll=0.01, out=waited):
                    stop.set()

            w = threading.Thread(target=waiter, daemon=True)
            w.start()
            for _ in range(200):
                if "занят" in waited.getvalue():
                    break
                time.sleep(0.01)
            self.assertIn("потолок 1 полных прогонов занят", waited.getvalue())
        w.join(timeout=10)
        self.assertEqual(out.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
