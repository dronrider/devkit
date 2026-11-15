#!/usr/bin/env python3
"""Съёмщик остатка Codex, kit/harness/snap/codex.sh.

Скрипт говорит JSON-RPC со штатным codex app-server, поэтому в стенде на PATH
кладётся свой codex: заглушка отвечает заготовленными сообщениями строками и
о живом входе не знает. Проверяется разбор ответа и отказы источника, а не
сеть, и живой прогон стенд не заменяет.
"""
import json
import os
import tempfile
import time
import unittest
from pathlib import Path

from testenv import DEVKIT_SRC, executable, run

SNAP = DEVKIT_SRC / "kit" / "harness" / "snap" / "codex.sh"

# Живой ответ подписки от 2026-11-13, обезличен: недельное окно стоит первым,
# пятичасового нет вовсе, и оба места ответа несут один и тот же снимок. Окна
# классифицируются длиной, поэтому позиция первичного окна здесь ничему не
# противоречит.
LIVE_RESULT = {
    "rateLimits": {
        "limitId": "codex",
        "limitName": None,
        "primary": {"usedPercent": 46, "windowDurationMins": 10080,
                    "resetsAt": 1794937422},
        "secondary": None,
    },
    "rateLimitsByLimitId": {
        "codex": {
            "limitId": "codex",
            "limitName": None,
            "primary": {"usedPercent": 46, "windowDurationMins": 10080,
                        "resetsAt": 1794937422},
            "secondary": None,
        },
    },
}


def window(used, mins, reset):
    """Окно ответа: процент, длина в минутах и срок сброса (None без срока)."""
    return {"usedPercent": used, "windowDurationMins": mins, "resetsAt": reset}


def stub_body(result=None, error=None, die_after_init=False, pid_file=None):
    """Тело заглушки codex: initialize, затем ответ на чтение квот.

    Умирание после initialize отдельной веткой: имитирует смерть app-server
    до ответа, которую съёмщик обязан назвать, а не пересидеть. pid_file это
    путь, куда заглушка кладёт свой pid: так тест видит, убил ли её съёмщик.
    """
    pid_note = ""
    if pid_file is not None:
        pid_note = "import os\nopen(%r, \"w\").write(str(os.getpid()))\n" % str(pid_file)
    after = "        sys.exit(0)\n" if die_after_init else ""
    # Ответ вставляется в исходник заглушки строкой-литералом и разбирается
    # json.loads на месте: вставка готового JSON словом null или true была бы
    # синтаксической ошибкой python, и заглушка умирала бы до ответа.
    if error is not None:
        answer = ('        print(json.dumps({"jsonrpc": "2.0", "id": 2, '
                  '"error": json.loads(%r)}), flush=True)\n' % json.dumps(error))
    else:
        answer = ('        print(json.dumps({"jsonrpc": "2.0", "id": 2, '
                  '"result": json.loads(%r)}), flush=True)\n' % json.dumps(result))
    return ('#!/usr/bin/env python3\n'
            'import json, sys\n'
            + pid_note +
            'for line in sys.stdin:\n'
            '    msg = json.loads(line)\n'
            '    if msg.get("id") == 1:\n'
            '        print(json.dumps({"jsonrpc": "2.0", "id": 1, '
            '"result": {}}), flush=True)\n'
            + after +
            '        continue\n'
            '    if msg.get("id") == 2:\n'
            + answer +
            '        break\n')


class CodexSnapTest(unittest.TestCase):
    def snap(self, **kw):
        """Прогон съёмщика над заглушкой codex. Отдаёт код и вывод."""
        box = Path(tempfile.mkdtemp(prefix="devkit-codex-snap-"))
        self.addCleanup(lambda: __import__("shutil").rmtree(str(box), True))
        bin_ = box / "bin"
        bin_.mkdir()
        executable(bin_ / "codex", stub_body(**kw))
        return run([str(SNAP)], env={"DEVKIT_HARNESS": "codex"},
                   path="%s:%s" % (bin_, os.environ["PATH"]))

    def test_live_answer_week_only(self):
        """Живой ответ подписки: недельное окно первым, пятичасового нет.

        Окна узнаются длиной, а не позицией, и отсутствие вторичного окна это
        не поломка: у подписки просто нет второго лимита. Снимок обязан нести
        недельный остаток и его сброс.
        """
        rc, out = self.snap(result=LIVE_RESULT)
        self.assertEqual(rc, 0, out)
        lines = out.splitlines()
        self.assertRegex(lines[0], r"^taken = \d{4}-\d\d-\d\dT\d\d:\d\d$", out)
        week = [ln for ln in lines if ln.startswith("week_all")]
        self.assertEqual(len(week), 1, "недельное окно потерялось или задвоилось: %s" % out)
        self.assertRegex(week[0], r"^week_all = 46% сброс \d{4}-\d\d-\d\dT\d\d:\d\d$", out)
        self.assertNotIn("window5h", out, out)

    def test_both_windows_with_resets(self):
        """Оба окна со сроками сброса едут в снимок, потолок обрезается."""
        result = {"rateLimits": {"primary": window(120, 300, 1794937422),
                                 "secondary": window(46, 10080, 1795000000)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertEqual(rc, 0, out)
        self.assertIn("window5h_all = 100% сброс ", out, out)
        self.assertIn("week_all = 46% сброс ", out, out)

    def test_untouched_window_has_no_reset(self):
        """Нетронутое окно без срока сброса печатается процентом одним.

        Протокол обновлений разреженный, и null здесь нетронутое поле, а не
        ноль: подменять его датой наугад значило бы выдумать сброс. Формат
        снимка такую строку допускает.
        """
        result = {"rateLimits": {"primary": window(0, 300, None),
                                 "secondary": window(46, 10080, 1794937422)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertEqual(rc, 0, out)
        five = [ln for ln in out.splitlines() if ln.startswith("window5h_all")]
        self.assertEqual(len(five), 1, "пустое окно потерялось или задвоилось: %s" % out)
        self.assertRegex(five[0], r"^window5h_all = 0%$")

    def test_limit_picked_from_map(self):
        """Снимок берётся из карты по ключу codex, а не из одиночного вида.

        Ответ несёт вид дважды, и числа могут разъехаться: картой сервер
        называет лимиты по limit_id, и приоритет у ключа подписки.
        """
        result = {"rateLimits": {"primary": window(99, 10080, 1795000000)},
                  "rateLimitsByLimitId": {
                      "gizmo": {"primary": window(7, 10080, 1795000000)},
                      "codex": {"primary": window(46, 10080, 1794937422)},
                  }}
        rc, out = self.snap(result=result)
        self.assertEqual(rc, 0, out)
        self.assertIn("week_all = 46% сброс ", out, out)
        self.assertNotIn("99%", out, out)
        self.assertNotIn("7%", out, out)

    def test_map_without_codex_refused(self):
        """Карта без ключа codex это отказ, а не падение на первый ключ."""
        result = {"rateLimits": {"primary": window(46, 10080, 1794937422)},
                  "rateLimitsByLimitId": {
                      "gizmo": {"primary": window(7, 10080, 1795000000)},
                  }}
        rc, out = self.snap(result=result)
        self.assertNotEqual(rc, 0, "чужой лимит прошёл как codex: %s" % out)
        self.assertIn("codex", out, out)

    def test_source_error_refused(self):
        """Отказ источника проходит без повтора и не трогает снимок."""
        rc, out = self.snap(error={"code": -32603,
                                   "message": "backend rate limits unavailable"})
        self.assertNotEqual(rc, 0, "отказ источника прошёл молча: %s" % out)
        self.assertIn("backend rate limits unavailable", out, out)
        self.assertNotIn("week_all", out, out)

    def test_missing_week_window_refused(self):
        """Ответ без недельного окна это отказ: обязательный бакет пуст."""
        result = {"rateLimits": {"primary": window(12, 300, 1794937422)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertNotEqual(rc, 0, "снимок без недельного окна прошёл: %s" % out)
        self.assertIn("недельного", out, out)

    def test_unknown_window_length_refused(self):
        """Незнакомая длина окна это отказ, а не догадка по остатку."""
        result = {"rateLimits": {"primary": window(46, 1440, 1794937422)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertNotEqual(rc, 0, "незнакомое окно классифицировано наугад: %s" % out)
        self.assertIn("1440", out, out)

    def test_null_duration_window_refused(self):
        """Существующее окно без длины это отказ, а не отсутствие окна.

        Объект окна с windowDurationMins = null несёт процент реального окна,
        и выбросить его значило бы выдать неполный ответ за отсутствие
        необязательного окна. Отсутствие окна выглядит иначе, это
        secondary = null, и оно законный пропуск. Съёмщик отказывает с
        причиной и не печатает снимок вовсе.
        """
        result = {"rateLimits": {"primary": window(46, 10080, 1794937422),
                                 "secondary": window(90, None, None)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertNotEqual(rc, 0, "окно без длины выброшено молча: %s" % out)
        self.assertIn("нет длины", out, out)
        self.assertNotIn("week_all", out, "отказ напечатал снимок: %s" % out)

    def test_null_duration_without_week_refused(self):
        """Одно окно без длины это отказ с причиной про длину окна."""
        result = {"rateLimits": {"primary": window(46, None, None)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertNotEqual(rc, 0, "снимок без единого окна прошёл: %s" % out)
        self.assertIn("нет длины", out, out)

    def test_spent_window_without_reset_prints_percent(self):
        """Потраченное окно без срока сброса печатается процентом без даты.

        Подставлять дату наугад значило бы выдумать сброс, а ноль подменял бы
        трату. Строка без даты законна форматом снимка, а диагноз такому
        бакету ставит вердикт quota --json, отказом в доступности без даты
        сброса (случай no-reset в tools/agentctl/quota_json_test.go).
        """
        result = {"rateLimits": {"primary": window(46, 10080, 1794937422),
                                 "secondary": window(12, 300, None)},
                  "rateLimitsByLimitId": None}
        rc, out = self.snap(result=result)
        self.assertEqual(rc, 0, out)
        five = [ln for ln in out.splitlines() if ln.startswith("window5h_all")]
        self.assertEqual(len(five), 1, "окно без сброса потерялось или задвоилось: %s" % out)
        self.assertRegex(five[0], r"^window5h_all = 12%$")

    def test_process_death_after_initialize(self):
        """Смерть app-server до ответа называется причиной, а не тишиной."""
        rc, out = self.snap(die_after_init=True)
        self.assertNotEqual(rc, 0, "смерть процесса прошла молча: %s" % out)
        self.assertIn("вышел", out, out)

    def test_damaged_answer_refuses_and_kills_server(self):
        """Повреждённый ответ отказывает съёмщика и убивает app-server.

        Сервер порождён съёмщиком, и его жизнь не должна переживать отказ
        разбора: периодический тик наслаивал бы мёртвые app-server одну за
        другой. Заглушка кладёт свой pid, и тест ждёт смерти процесса.
        """
        for damaged in (None, [1, 2]):
            box = Path(tempfile.mkdtemp(prefix="devkit-codex-snap-"))
            self.addCleanup(lambda: __import__("shutil").rmtree(str(box), True))
            pid_file = box / "codex.pid"
            bin_ = box / "bin"
            bin_.mkdir()
            executable(bin_ / "codex", stub_body(result=damaged, pid_file=pid_file))
            rc, out = run([str(SNAP)], env={"DEVKIT_HARNESS": "codex"},
                          path="%s:%s" % (bin_, os.environ["PATH"]))
            self.assertNotEqual(rc, 0, "повреждённый ответ %r прошёл: %s" % (damaged, out))
            pid = int(pid_file.read_text())
            gone = False
            for _ in range(100):
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    gone = True
                    break
                time.sleep(0.05)
            self.assertTrue(gone, "app-server %d жив после отказа разбора" % pid)

    def test_without_harness_env_refused(self):
        """Скрипт без окружения agentctl отказывает, а не гадает, чей остаток."""
        rc, out = run([str(SNAP)], env={"DEVKIT_HARNESS": None})
        self.assertNotEqual(rc, 0, out)
        self.assertIn("DEVKIT_HARNESS", out, out)


if __name__ == "__main__":
    unittest.main()
