#!/usr/bin/env python3
"""Обрезка журнала по LF сохраняет байты вывода refresh."""
import unittest

from quota_journal import cap_lines


class TestQuotaJournal(unittest.TestCase):
    def test_cap_matches_tail_lines_without_changing_carriage_returns(self):
        for end in (b"", b"\n"):
            with self.subTest(end=end):
                data = b"old\rline\ncurrent\rbody\nstatus" + end
                self.assertEqual(cap_lines(data, 2), b"current\rbody\nstatus" + end)
                self.assertEqual(cap_lines(data, 100), data)
                self.assertEqual(cap_lines(data, 0), b"")


if __name__ == "__main__":
    unittest.main()
