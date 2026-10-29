#!/usr/bin/env python3
"""Журнал инцидентов конвейера (DK-1415).

Запись переживает перезапуск, повтор того же сбоя дополняет её, а не плодит
новые, исход закрывает и не перезатирается, сводка считает остановки по
причинам и исходам.
"""
import os
import tempfile
import time
import unittest
from datetime import datetime, timedelta
from pathlib import Path

import incidents


class IncidentsTest(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="incidents-test-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(str(self.dir), True))
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        os.environ.pop(incidents.LOG_ENV, None)

    def path(self):
        return self.home / ".devkit" / incidents.LOG_NAME

    def test_report_opens_incident(self):
        ident = incidents.report("DK-1", incidents.IDLE_RUNS, "3 прохода вхолостую",
                                 run="окно dk-1", root="/repo",
                                 logs=["/repo/.devkit/log"], home=self.home)
        self.assertTrue(ident, "запись не записалась")
        text = self.path().read_text(encoding="utf-8")
        self.assertIn(ident, text)
        self.assertIn(incidents.IDLE_RUNS, text)
        self.assertIn("DK-1", text)
        self.assertIn("/repo/.devkit/log", text)
        rec = incidents.open_for("DK-1", home=self.home)
        self.assertEqual(rec.get("id"), ident)
        self.assertFalse(rec.get("outcome"))

    def test_repeat_inside_window_grows_the_same_incident(self):
        now = datetime.now()
        first = incidents.report("DK-2", incidents.HEAD_EXIT, "голова вышла",
                                 home=self.home, now=now)
        again = incidents.report("DK-2", incidents.HEAD_EXIT, "голова вышла",
                                 home=self.home, now=now + timedelta(minutes=5))
        self.assertEqual(first, again)
        rec = incidents.open_for("DK-2", home=self.home)
        self.assertEqual(rec.get("seen"), 2)
        self.assertEqual(len(incidents.read(home=self.home)), 1)

    def test_repeat_after_resume_opens_new_incident(self):
        now = datetime.now()
        first = incidents.report("DK-3", incidents.PASSES_OUT, "проходы исчерпаны",
                                 home=self.home, now=now)
        incidents.settle(first, incidents.RESUMED, home=self.home)
        later = incidents.report("DK-3", incidents.PASSES_OUT, "проходы исчерпаны",
                                 home=self.home,
                                 now=now + timedelta(minutes=10))
        self.assertNotEqual(first, later)
        self.assertEqual(len(incidents.read(home=self.home)), 2)

    def test_repeat_after_other_outcome_grows_the_same_incident(self):
        """Вопрос и черновик остановку не убирают: пока она стоит, повторы
        дополняют ту же запись, а не плодят новые."""
        first = incidents.report("DK-12", incidents.PROC_GONE, "мёртвый замок",
                                 home=self.home)
        incidents.settle(first, incidents.TRIES_OUT, home=self.home)
        again = incidents.report("DK-12", incidents.PROC_GONE, "мёртвый замок",
                                 home=self.home)
        self.assertEqual(first, again)
        self.assertEqual(len(incidents.read(home=self.home)), 1)

    def test_other_reason_is_other_incident(self):
        now = datetime.now()
        one = incidents.report("DK-4", incidents.IDLE_RUNS, "воронка",
                               home=self.home, now=now)
        two = incidents.report("DK-4", incidents.BROKEN, "доска не прочиталась",
                               home=self.home, now=now)
        self.assertNotEqual(one, two)

    def test_settle_keeps_first_outcome(self):
        ident = incidents.report("DK-5", incidents.PROC_GONE, "замок с мёртвым pid",
                                 home=self.home)
        incidents.settle(ident, incidents.ASK_HUMAN, home=self.home)
        incidents.settle(ident, incidents.RESUMED, home=self.home)
        rec = incidents.open_for("DK-5", home=self.home)
        self.assertIsNone(rec)
        stored = [r for r in incidents.read(home=self.home) if r["id"] == ident][0]
        self.assertEqual(stored["outcome"], incidents.ASK_HUMAN)

    def test_note_is_capped(self):
        ident = incidents.report("DK-6", incidents.HEAD_SILENT, "молчание",
                                 home=self.home)
        for n in range(incidents.NOTES_CAP + 5):
            incidents.note(ident, "попытка %d" % n, home=self.home)
        rec = [r for r in incidents.read(home=self.home) if r["id"] == ident][0]
        self.assertEqual(len(rec["notes"]), incidents.NOTES_CAP)

    def test_summary_counts_reasons_and_outcomes(self):
        one = incidents.report("DK-7", incidents.IDLE_RUNS, "воронка", home=self.home)
        incidents.settle(one, incidents.RESUMED, home=self.home)
        two = incidents.report("DK-8", incidents.IDLE_RUNS, "воронка", home=self.home)
        incidents.settle(two, incidents.ASK_HUMAN, home=self.home)
        incidents.report("DK-9", incidents.HEAD_EXIT, "выход", home=self.home)
        words = " ".join(incidents.summary(home=self.home))
        self.assertIn("воронка: 2", words)
        self.assertIn("%s 1" % incidents.RESUMED, words)
        self.assertIn("%s 1" % incidents.ASK_HUMAN, words)
        self.assertIn("голова вышла: 1", words)
        self.assertIn("открыт 1", words)

    def test_env_overrides_the_log_path(self):
        other = self.dir / "stand" / "incidents.jsonl"
        os.environ[incidents.LOG_ENV] = str(other)
        self.addCleanup(os.environ.pop, incidents.LOG_ENV, None)
        ident = incidents.report("DK-10", incidents.BROKEN, "ошибка", home=self.home)
        self.assertTrue(other.is_file())
        self.assertTrue(ident)
        self.assertFalse(self.path().exists())

    def test_broken_line_does_not_break_reading(self):
        self.path().write_text("не json вообще\n", encoding="utf-8")
        self.assertEqual(incidents.read(home=self.home), [])
        self.assertEqual(incidents.summary(home=self.home), ["инцидентов нет"])

    def test_no_secrets_in_record(self):
        """В записи нет адресов и доступов: только ID, коды, пути и слова."""
        ident = incidents.report("DK-11", incidents.ORDER_LOST,
                                 "заказ не дошёл до окна dk-11",
                                 run="окно dk-11", root="/Users/rider/x",
                                 logs=["/Users/rider/x/.devkit/log"], home=self.home)
        text = self.path().read_text(encoding="utf-8")
        self.assertIn(ident, text)
        for word in ("token", "secret", "password", "http://", "https://"):
            self.assertNotIn(word, text)

    def test_concurrent_writers_keep_every_record(self):
        """Стоп конвейера, тик подъёма и дежурный пишут в один журнал из
        разных процессов. Каждый перезаписывает журнал от прочитанного
        снимка, и без замка одновременная запись теряет чужую строку.
        Замок в тесте проверяется на писателях за общим барьером: пауза
        в записи разводит снимки, и в журнале обязаны остаться все строки."""
        writers, rounds = 4, 3
        start = self.dir / "start"
        plain = incidents._write

        def slowed(records, home=None):
            time.sleep(0.05)
            return plain(records, home)

        pids = []
        for n in range(writers):
            pid = os.fork()
            if pid == 0:
                status = 1
                try:
                    incidents._write = slowed
                    while not start.exists():
                        time.sleep(0.01)
                    for i in range(rounds):
                        incidents.report("DK-race-%d-%d" % (n, i),
                                         incidents.PROC_GONE, "гонка писателей",
                                         home=self.home)
                    status = 0
                finally:
                    os._exit(status)
            pids.append(pid)
        time.sleep(0.3)
        start.write_text("", encoding="utf-8")
        for pid in pids:
            _, status = os.waitpid(pid, 0)
            self.assertEqual(os.waitstatus_to_exitcode(status), 0,
                             "писатель %d упал" % pid)
        records = incidents.read(home=self.home)
        self.assertEqual(len(records), writers * rounds,
                         "журнал потерял строки: %d из %d"
                         % (len(records), writers * rounds))


if __name__ == "__main__":
    unittest.main()
