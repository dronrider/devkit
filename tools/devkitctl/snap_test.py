#!/usr/bin/env python3
"""Съёмщик остатка второй подписки, kit/harness/snap/glm-code.sh.

Скрипт ходит в эндпоинт мониторинга, поэтому в стенде на PATH кладётся свой
curl: он печатает заготовленный ответ и о сети не знает. Проверяется разбор
ответа, а не сеть, и живой прогон стенд не заменяет.
"""
import json
import os
import tempfile
import unittest
from pathlib import Path

from testenv import DEVKIT_SRC, executable, run

SNAP = DEVKIT_SRC / "kit" / "harness" / "snap" / "glm-code.sh"

# Пятичасовое окно это unit=3 number=5, недельное unit=6 number=1: пару окна
# держит LLD DK-090, и по ней съёмщик окна и опознаёт.
WINDOW_5H = {"type": "CREDIT_LIMIT", "unit": 3, "number": 5}
WINDOW_WEEK = {"type": "CREDIT_LIMIT", "unit": 6, "number": 1}


def limit(window, usage, current, reset=None):
    lim = dict(window, usage=usage, currentValue=current)
    if reset is not None:
        lim["nextResetTime"] = reset
    return lim


class GlmSnapTest(unittest.TestCase):
    def snap(self, limits):
        """Прогон съёмщика над заготовленным ответом. Отдаёт код и вывод."""
        box = Path(tempfile.mkdtemp(prefix="devkit-snap-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(str(box), True))
        home = box / "harness"
        (home).mkdir()
        (home / "settings.json").write_text(json.dumps({"env": {
            "ANTHROPIC_BASE_URL": "https://пример.тест/api/anthropic",
            "ANTHROPIC_AUTH_TOKEN": "токен-стенда",
        }}), encoding="utf-8")
        body = json.dumps({"data": {"limits": limits}})
        stub = box / "bin"
        stub.mkdir()
        # Настоящий curl читает конфиг с токеном из stdin, и заглушка делает то
        # же: молча его глотает, иначе съёмщик упрётся в сломанный пайп.
        executable(stub / "curl", "#!/bin/sh\ncat >/dev/null\ncat <<'JSON'\n%s\nJSON\n" % body)
        return run([str(SNAP)],
                   env={"DEVKIT_HARNESS": "glm-code", "DEVKIT_HARNESS_HOME": str(home)},
                   path="%s:%s" % (stub, os.environ["PATH"]))

    def test_untouched_window_does_not_drop_snapshot(self):
        """Нетронутое окно это законное состояние, а не отказ.

        Пока по окну не потрачено ни кредита, z.ai времени сброса не присылает:
        часы окна пускает первая трата. Прежний съёмщик считал это поломкой и
        уходил с ненулевым кодом, унося вместе с пустым окном и недельное, где
        данные были в порядке, поэтому остаток второй подписки стоял сутки.
        """
        rc, out = self.snap([limit(WINDOW_5H, 1000, 0),
                             limit(WINDOW_WEEK, 1000, 170, 1788000000000)])
        self.assertEqual(rc, 0, "нетронутое окно уронило снимок целиком: %s" % out)
        self.assertIn("window5h_all = 0%", out, out)
        self.assertIn("week_all = 17%", out, out)

    def test_untouched_window_differs_from_absence(self):
        """Нулевая трата и «данных нет» это разные строки снимка.

        Пустое окно приезжает бакетом с нулём и сроком сброса от своей длины:
        своего у него нет, а без срока бакет прочитался бы просроченным и тянул
        бы вердикт вниз. Отсутствие данных, наоборот, это отсутствие строки.
        """
        rc, out = self.snap([limit(WINDOW_5H, 1000, 0),
                             limit(WINDOW_WEEK, 1000, 170, 1788000000000)])
        self.assertEqual(rc, 0, out)
        five = [ln for ln in out.splitlines() if ln.startswith("window5h_all")]
        self.assertEqual(len(five), 1, "пустое окно потерялось или задвоилось: %s" % out)
        self.assertRegex(five[0], r"^window5h_all = 0% сброс \d{4}-\d\d-\d\dT\d\d:\d\d$")

    def test_spent_window_without_reset_is_refused(self):
        """Потраченному окну без срока сброса верить нечему.

        Ноль без срока объясняется неначатым окном, а трата без срока это
        поломка ответа, и молча досчитывать ей срок значило бы выдумать цифру.
        """
        rc, out = self.snap([limit(WINDOW_5H, 1000, 400),
                             limit(WINDOW_WEEK, 1000, 170, 1788000000000)])
        self.assertNotEqual(rc, 0, "трата без срока сброса прошла молча: %s" % out)
        self.assertIn("window5h_all", out, out)

    def test_missing_window_is_refused(self):
        """Одно окно в ответе это отказ: снимок с половиной окон читался бы как
        снимок подписки без второго лимита."""
        rc, out = self.snap([limit(WINDOW_WEEK, 1000, 170, 1788000000000)])
        self.assertNotEqual(rc, 0, "снимок с одним окном прошёл: %s" % out)


if __name__ == "__main__":
    unittest.main()
