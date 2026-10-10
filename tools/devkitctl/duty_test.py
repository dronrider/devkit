"""Дежурный агент и его граница с DK-1115 (DK-1323).

Сценарий проверки задачи: тик поднимает дежурного по маркеру эскалации,
разбор кончается вопросом либо черновиком, громкий зов не идёт раньше
разбора, одинаковые отказы в разбор не входят. Тихий исход самого подъёма
проверяет ResumeFailedTest в watch_test.py.
"""
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import duty


class Fake:
    """Подставной запускатель: помнит вызовы и отвечает заданным кодом."""

    def __init__(self, code=0, out=""):
        self.calls = []
        self.code = code
        self.out = out

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        return subprocess.CompletedProcess(argv, self.code, self.out, None)

    def argv_with(self, needle):
        return [a for a in self.calls if any(needle in str(x) for x in a)]


class DutyAgentTest(unittest.TestCase):
    """Разбор неизвестных отказов дежурным (DK-1323)."""

    TASK = "DK-901"
    WHY = "подъём не помог 3 раз подряд, сессия 3d1c4c04"

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="duty-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        self.proj = self.dir / "proj"
        (self.proj / "docs" / "tasks").mkdir(parents=True)
        self.call = Fake(out="ступень: поднято, код 0")
        self.bin = "/bin/подставной-taskctl"

    def task_file(self, marks):
        """Файл задачи с записями исхода в «Ходе работы»."""
        body = "".join("- 2026-10-10 %s: %s.\n" % (duty.DUTY_MARK, m) for m in marks)
        path = self.proj / "docs" / "tasks" / ("%s.md" % self.TASK)
        path.write_text("# %s\n\n## Ход работы\n\n%s\n## Сценарий проверки\n\n1. шаг\n"
                        % (self.TASK, body), encoding="utf-8")
        return path

    def duty_lines(self, call=None):
        return duty.duty_root(self.proj, call=call or self.call, taskctl=self.bin,
                              home=self.home)

    def runs(self, call=None):
        call = call or self.call
        return [a for a in call.calls if a and a[0] == self.bin and "run" in a]

    def notifies(self, call=None):
        """Вызовы самого уведомителя: путь скрипта вторым аргументом.

        Текст заказа дежурному цитирует hooks/notify.py, и поиск по всему
        argv ловил бы сам заказ; здесь берётся аргумент имени скрипта.
        """
        call = call or self.call
        return [a for a in call.calls if len(a) > 1 and "notify.py" in str(a[1])]

    # Шаг 1: тик поднимает дежурного по маркеру и разбирает исход.

    def test_tick_rises_duty_by_marker(self):
        self.task_file([self.WHY])
        lines = self.duty_lines()
        ran = self.runs()
        self.assertEqual(len(ran), 1, self.call.calls)
        self.assertEqual(ran[0][:5], [self.bin, "-C", str(self.proj), "run", self.TASK])
        self.assertIn("дежурный поднят", " ".join(lines))

    def test_marker_outside_stage_is_not_a_duty_address(self):
        # Метка в другом разделе это цитата или сценарий, а не адрес дежурного.
        path = self.proj / "docs" / "tasks" / ("%s.md" % self.TASK)
        path.write_text("# %s\n\n## Сценарий проверки\n\n1. %s: цитата.\n"
                        % (self.TASK, duty.DUTY_MARK), encoding="utf-8")
        self.assertEqual(self.duty_lines(), [])
        self.assertEqual(self.runs(), [])

    # Шаг 2: разбор кончается вопросом либо черновиком, и это сказано в заказе.

    def test_order_ends_with_ask_or_draft(self):
        order = duty.duty_order(self.TASK, self.WHY)
        self.assertIn("taskctl ask", order)
        self.assertIn("taskctl draft", order)
        self.task_file([self.WHY])
        self.duty_lines()
        joined = " ".join(str(x) for a in self.runs() for x in a)
        self.assertIn("taskctl ask", joined)
        self.assertIn("taskctl draft", joined)

    def test_order_forbids_code_fixes(self):
        # Дежурный это слой эскалации без прав: правка с тестом остаётся за
        # конвейером задачи.
        self.assertIn("правок кода не делай", duty.duty_order(self.TASK, self.WHY))

    def test_order_says_loud_call_is_the_tail(self):
        self.assertIn("хвост разбора", duty.duty_order(self.TASK, self.WHY))

    # Шаг 3: громкий зов не идёт раньше разбора, он его хвост.

    def test_duty_spawn_does_not_shout(self):
        self.task_file([self.WHY])
        self.duty_lines()
        self.assertEqual(self.notifies(), [],
                         "громкий зов ушёл до разбора дежурного: %s" % self.call.calls)

    def test_spawn_failure_retries_next_tick(self):
        # Разбор не начался: подпись не пишется, повтор идёт ближайшим тиком
        # и исход не виснет (замечание ревью). Громкий зов раньше потолка
        # не уходит: это хвост постоянного сбоя, его держит тест потолка.
        self.task_file([self.WHY])
        call = Fake(code=2, out="лестница отказала")
        lines = self.duty_lines(call=call)
        self.assertEqual(len(self.runs(call)), 1, call.calls)
        self.assertEqual(duty.read_state(home=self.home), set(),
                         "неудачный подъём занял подпись, повтора не будет")
        self.assertIn("повтор тиком", " ".join(lines))
        call2 = Fake(code=2, out="лестница отказала")
        self.duty_lines(call=call2)
        self.assertEqual(len(self.runs(call2)), 1, "неудачный подъём не повторился")
        self.assertEqual(self.notifies(call2), [], "зов ушёл раньше потолка")

    def test_spawn_ceiling_shouts_and_stops_retry(self):
        # Постоянный сбой подъёма добивается потолком попыток: человеку идёт
        # реальный громкий зов, подпись занимает запись и наружу больше не
        # долбят (замечание ревью).
        self.task_file([self.WHY])
        for _ in range(duty.SPAWN_TRIES):
            call = Fake(code=2, out="лестница отказала")
            lines = self.duty_lines(call=call)
        self.assertEqual(len(self.notifies(call)), 1,
                         "на потолке подъёмов человека не позвали: %s" % call.calls)
        self.assertIn("громкий зов человеку ушёл", " ".join(lines))
        self.assertIn(duty.signature(self.WHY), duty.read_state(home=self.home))
        call2 = Fake(code=2)
        self.duty_lines(call=call2)
        self.assertEqual(self.runs(call2), [], "после потолка поднимают снова")

    # Шаг 4: одинаковые отказы в разбор не входят (граница DK-1115).

    def test_identical_failures_skip_duty(self):
        # Две записи с одной причиной и разными сессиями это один и тот же
        # отказ: дежурный поднимается один раз, вторая запись мимо разбора.
        self.task_file([self.WHY, "подъём не помог 3 раз подряд, сессия иная-сессия"])
        lines = self.duty_lines()
        self.assertEqual(len(self.runs()), 1, self.call.calls)
        self.assertIn("DK-1115", " ".join(lines))

    def test_new_signature_still_rises(self):
        # Новая причина это снова неизвестный исход, она в разбор входит.
        self.task_file([self.WHY])
        self.duty_lines()
        self.task_file([self.WHY, "402 Insufficient balance: резюмом не лечится, сессия x"])
        call2 = Fake(out="ступень: поднято, код 0")
        self.duty_lines(call=call2)
        self.assertEqual(len(self.runs(call2)), 1, call2.calls)

    def test_picked_marker_is_not_picked_again(self):
        self.task_file([self.WHY])
        self.duty_lines()
        self.assertEqual(len(self.runs()), 1)
        self.duty_lines()
        self.assertEqual(len(self.runs()), 1, "дежурного подняли дважды: %s" % self.call.calls)

    def test_signature_strips_session(self):
        self.assertEqual(duty.signature(self.WHY), "подъём не помог 3 раз подряд")
        self.assertEqual(duty.signature("402 Insufficient balance: резюмом не лечится"),
                         "402 Insufficient balance: резюмом не лечится")

    def test_parse_mark_reads_the_reason(self):
        line = "- 2026-10-10 %s: %s." % (duty.DUTY_MARK, self.WHY)
        self.assertEqual(duty.parse_mark(line), self.WHY)
        self.assertIsNone(duty.parse_mark("- 2026-10-10 разработка: ход идёт."))

    def test_order_is_one_for_shell_and_duty(self):
        # Начало заказа одно на оболочку конвейера и на этот заход: разойдись
        # оно, дежурная голова не узнала бы свой заказ и пошла бы дальше, в
        # правки. Сторож пары как у TestReplyOrderIsOneForShellAndCallers.
        src = (Path(__file__).resolve().parents[2]
               / "kit" / "skills" / "board-task" / "task-run.py")
        text = src.read_text(encoding="utf-8")
        m = re.search(r'(?m)^DUTY_ORDER = "([^"]+)"', text)
        self.assertIsNotNone(
            m, "в task-run.py нет DUTY_ORDER, оболочка заказ дежурного не узнает")
        self.assertEqual(m.group(1), duty.ORDER,
                         "оболочка ждёт заказ с %r, а дежурный шлёт %r"
                         % (m.group(1), duty.ORDER))
        self.assertTrue(duty.duty_order(self.TASK, self.WHY).startswith(duty.ORDER))

    def test_mark_is_one_for_watch_and_duty(self):
        # Метка записи одна на сторожа, который её пишет, и на разбор: он
        # читает её отсюда же, и вторая копия слова разошлась бы с первой.
        import watch
        self.assertEqual(watch.DUTY_MARK, duty.DUTY_MARK)

    def test_watch_tick_carries_the_duty(self):
        # Тик сторожа зовёт дежурного по записи исхода: сценарий проверки зовёт
        # это подъёмом тиком по маркеру, а сама механика лежит в duty_root.
        import watch
        self.task_file([self.WHY])
        lines = watch.duty_rows(self.proj, call=self.call, taskctl=self.bin,
                                home=self.home)
        self.assertEqual(len(self.runs()), 1, self.call.calls)
        self.assertIn("дежурный поднят", " ".join(lines))

    def test_dry_run_sees_the_decision_without_touching_state(self):
        # Без act подъём гаснет, память не пишется: стенд смотрит решение
        # захода и не сжигает подписи.
        self.task_file([self.WHY])
        lines = duty.duty_root(self.proj, call=self.call, taskctl=self.bin,
                               home=self.home, act=False)
        self.assertEqual(self.runs(), [], self.call.calls)
        self.assertIn("ждёт дежурного", " ".join(lines))
        self.assertEqual(duty.read_state(home=self.home), set())
        # Второй заход с act видит ту же запись как неразобранную.
        self.duty_lines()
        self.assertEqual(len(self.runs()), 1, self.call.calls)


if __name__ == "__main__":
    unittest.main()
