#!/usr/bin/env python3
"""Прогон подхвата реплики под подменёнными часами (DK-1187). Тесты доставки
из chat_in_test.py держат в фикстуре строку «Входящих» с временем, а рядом, в
файле отметок, стоит время записи. Сверка отметки подстрокой времени красила
прогон ровно в ту минуту суток, что совпала с фикстурой: прогон слияния идёт
десять минут и в такую минуту попадает.

Стенд подменяет часы соседнего прогона: в PYTHONPATH кладётся sitecustomize,
который смещает time.time и time.localtime до минуты фикстуры, а минута
читается из самой фикстуры, чтобы стенд не разъехался с ней после правки.
Смещение постоянное, значит часы идут своим ходом, а не стоят: срок ожидания и
разница двух отметок остаются настоящими.

Гоняется как обычный тест: python3 chat_in_clock_test.py
"""
import os
import re
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SUITE = os.path.join(HERE, "chat_in_test.py")
# Тесты, чей вердикт читает файл отметок текстом. Гонять под подменёнными
# часами весь класс доставки дорого (минута на прогон), а совпасть с часами
# может только сверка с текстом отметки.
WATCHED = ("DeliveryTest.test_same_text_sent_again_after_the_turn_took_it",
           "DeliveryTest.test_mark_without_its_line_is_not_carried_over")

FAKE_CLOCK = '''"""Подменённые часы прогона: смещение до минуты фикстуры."""
import time

_real_time = time.time
_real_localtime = time.localtime
_now = _real_time()
_day = _real_localtime(_now)
_delta = time.mktime((_day.tm_year, _day.tm_mon, _day.tm_mday,
                      %d, %d, 58, 0, 0, -1)) - _now


def _time():
    return _real_time() + _delta


def _localtime(secs=None):
    return _real_localtime(_real_time() + _delta if secs is None else secs)


time.time = _time
time.localtime = _localtime
'''


def fixture_minute():
    """Час и минута из первой строки «Входящих» в фикстуре соседнего файла."""
    with open(SUITE, encoding="utf-8") as f:
        found = re.search(r'incoming\("\d{4}-\d{2}-\d{2} (\d{2}):(\d{2}),', f.read())
    if not found:
        raise AssertionError("в фикстуре chat_in_test.py не нашлось строки с временем")
    return int(found.group(1)), int(found.group(2))


class ClockTest(unittest.TestCase):
    def test_delivery_verdict_does_not_depend_on_the_machine_clock(self):
        hour, minute = fixture_minute()
        with tempfile.TemporaryDirectory() as fake:
            with open(os.path.join(fake, "sitecustomize.py"), "w", encoding="utf-8") as f:
                f.write(FAKE_CLOCK % (hour, minute))
            env = dict(os.environ, PYTHONPATH=fake)
            p = subprocess.run([sys.executable, SUITE] + list(WATCHED),
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, env=env)
        self.assertEqual(p.returncode, 0,
                         "прогон доставки красен при часах на %02d:%02d, минуте фикстуры:\n%s"
                         % (hour, minute, p.stdout))


if __name__ == "__main__":
    unittest.main(verbosity=0)
