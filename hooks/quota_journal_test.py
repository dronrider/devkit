#!/usr/bin/env python3
"""Обрезка журнала по LF сохраняет байты вывода refresh."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from quota_journal import cap_lines, publish


class TestQuotaJournal(unittest.TestCase):
    def test_cap_matches_tail_lines_without_changing_carriage_returns(self):
        for end in (b"", b"\n"):
            with self.subTest(end=end):
                data = b"old\rline\ncurrent\rbody\nstatus" + end
                self.assertEqual(cap_lines(data, 2), b"current\rbody\nstatus" + end)
                self.assertEqual(cap_lines(data, 100), data)
                self.assertEqual(cap_lines(data, 0), b"")

    def test_cleanup_failure_reports_published_journal(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            log = state / "quota-refresh.log"
            error = state / "quota-refresh.error"
            previous = b"previous complete record\n"
            record = b"next complete record\n"
            log.write_bytes(previous)
            error.write_text("previous error\n", encoding="utf-8")
            with patch.object(Path, "unlink", side_effect=OSError):
                self.assertEqual(publish(state, record, 102400, 500), 1)
            self.assertEqual(log.read_bytes(), previous + record)
            self.assertEqual(error.read_text(encoding="utf-8"),
                             "ошибка записи журнала: очистка\n")
            self.assertEqual(set(path.name for path in state.iterdir()),
                             {"quota-refresh.log", "quota-refresh.error"})


if __name__ == "__main__":
    unittest.main()
