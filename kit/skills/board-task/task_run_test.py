#!/usr/bin/env python3
"""Самопроверка оболочки конвейера task-run.py. Головы играет стаб вместо
клиента, доску стаб вместо taskctl: настоящих сессий тут не поднимается ни
одной, как и в самопроверке цикла цели. Каждый стенд это свой временный корень
со своей обвязкой .devkit и своим HOME, чтобы журнал утилит и уведомитель
писали в него. Оба стаба написаны на python и живут только во временной
директории теста: файлами репозитория они не становятся, как и любая другая
фикстура, изображающая чужую программу.
"""
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(HERE, "task-run.py")

# Стаб доски: печатает строку задачи тем же форматом, каким её печатает taskctl
# («DK-1 в in-progress»), а секцию читает из файла стенда. Пометку про
# пользовательскую приёмку он печатает второй строкой, когда её просят.
TASKCTL_STUB = r'''#!/usr/bin/env python3
import os
import sys

state = os.environ["DEVKIT_TEST_STATE"]
with open(state, encoding="utf-8") as f:
    sect = f.read().strip()
if sys.argv[1:2] != ["show"]:
    sys.exit(0)
tail = ""
if sect.startswith("check-user"):
    sect, tail = "check", "  сценарий пользовательский"
sys.stdout.write("%s в %s\n" % (sys.argv[2], sect))
sys.stdout.write("| %s | строка | task | P2 | 10 | S | file |\n" % sys.argv[2])
if tail:
    sys.stdout.write(tail + "\n")
'''

# Стаб головы: играет очередную строку сценария и записывает заказ, с которым
# его подняли. Строки сценария: «работа» ничего не делает, «закрой» уводит
# строку в архив, «паркуй» в blocked, «падение» выходит ненулевым кодом.
# Сценарий кончился, значит повторяется последняя строка: голова, которая не
# двигает строку, так и не двигает её до конца.
CLAUDE_STUB = r'''#!/usr/bin/env python3
import os
import sys

state = os.environ["DEVKIT_TEST_STATE"]
calls = os.environ["DEVKIT_TEST_CALLS"]
plan = os.environ["DEVKIT_TEST_PLAN"].split("|")
order = sys.argv[sys.argv.index("-p") + 1] if "-p" in sys.argv else ""
with open(calls, "a", encoding="utf-8") as f:
    f.write(order + "\n")
n = 0
with open(calls, encoding="utf-8") as f:
    n = len([l for l in f if l.strip() != ""])
step = plan[min(n, len(plan)) - 1]
if step == "закрой":
    open(state, "w", encoding="utf-8").write("архиве\n")
elif step == "паркуй":
    open(state, "w", encoding="utf-8").write("blocked\n")
elif step == "падение":
    sys.exit(1)
'''


def write_stub(path, body):
    with open(path, "w", encoding="utf-8") as f:
        f.write(body)
    os.chmod(path, 0o755)


class Stand:
    """Временный корень со стабами, доской в файле и своим HOME."""

    def __init__(self, sect="in-progress", plan="работа"):
        self.root = tempfile.mkdtemp(prefix="task-run-")
        self.bin = os.path.join(self.root, "bin")
        os.makedirs(self.bin)
        os.makedirs(os.path.join(self.root, ".devkit"))
        self.state = os.path.join(self.root, "sect")
        self.calls = os.path.join(self.root, "calls")
        with open(self.state, "w", encoding="utf-8") as f:
            f.write(sect + "\n")
        open(self.calls, "w", encoding="utf-8").close()
        write_stub(os.path.join(self.bin, "taskctl"), TASKCTL_STUB)
        write_stub(os.path.join(self.bin, "claude-stub"), CLAUDE_STUB)
        self.plan = plan

    def env(self):
        return {
            "PATH": self.bin + os.pathsep + os.environ.get("PATH", ""),
            "HOME": self.root,
            "DEVKIT_TEST_STATE": self.state,
            "DEVKIT_TEST_CALLS": self.calls,
            "DEVKIT_TEST_PLAN": self.plan,
            "DEVKIT_TASK_PASS_PAUSE": "0",
            "DEVKIT_TMUX": "task-DK-1",
            # Уведомитель на стенде молчит: звать человека к синтетической
            # задаче незачем, а канал его проверяется своей самопроверкой.
            "DEVKIT_NOTIFY_OFF": "1",
        }

    def run(self, *args):
        argv = [sys.executable, RUN, "DK-1", "-C", self.root] + list(args)
        argv += ["--", os.path.join(self.bin, "claude-stub")]
        return subprocess.run(argv, capture_output=True, text=True, env=self.env())

    def orders(self):
        with open(self.calls, encoding="utf-8") as f:
            return [l.rstrip("\n") for l in f if l.strip() != ""]

    def journal(self):
        path = os.path.join(self.root, ".devkit", "log")
        if not os.path.isfile(path):
            return []
        with open(path, encoding="utf-8") as f:
            return [l.rstrip("\n") for l in f if l.strip() != ""]

    def drop(self):
        shutil.rmtree(self.root, ignore_errors=True)


class TestPasses(unittest.TestCase):
    """Голова выходит, а конвейер живёт: следующий проход поднимается тем же
    порядком, пока строка не закрыта."""

    def setUp(self):
        self.stands = []

    def tearDown(self):
        for s in self.stands:
            s.drop()

    def stand(self, **kw):
        s = Stand(**kw)
        self.stands.append(s)
        return s

    def test_second_pass_closes_the_task(self):
        # Первый проход это выход головы с незакрытой задачей: до DK-691 тут
        # конвейер и кончался, окно tmux закрывалось, а задача стояла.
        s = self.stand(plan="работа|закрой")
        r = s.run()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(len(s.orders()), 2)
        self.assertIn("задача закрыта", r.stdout)

    def test_first_order_and_next_order_differ(self):
        s = self.stand(plan="работа|закрой")
        s.run("--order", "Выполни DK-1", "--again", "Продолжай выполнение DK-1")
        self.assertEqual(s.orders(), ["Выполни DK-1", "Продолжай выполнение DK-1"])

    def test_head_exit_is_written_to_journal(self):
        # DoD задачи: выход головы с незакрытой задачей виден в журнале, и
        # строка называет сессию, задачу и её статус на момент выхода.
        s = self.stand(plan="работа|закрой")
        s.run()
        first = [l for l in s.journal() if "выход головы" in l]
        self.assertTrue(first, s.journal())
        self.assertIn("task-DK-1", first[0])
        self.assertIn("DK-1 в in-progress", first[0])
        self.assertIn("\ttask-run\t", first[0])

    def test_closed_task_raises_nobody(self):
        s = self.stand(sect="архиве")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(s.orders(), [])

    def test_parked_task_stops_the_pipeline(self):
        # Парковка это законный конец: строку ждёт человек, и долбить в неё
        # проходами значит жечь бюджет.
        s = self.stand(plan="паркуй")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(len(s.orders()), 1)
        self.assertIn("ждёт человека", r.stdout)

    def test_user_acceptance_stops_the_pipeline(self):
        s = self.stand(sect="check-user")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(s.orders(), [])
        self.assertIn("приёмки человеком", r.stdout)

    def test_funnel_stops_after_three_idle_passes(self):
        # Голова выходит быстро и строку не двигает: обычно это сломанное
        # окружение, и шесть попыток тут ничем не лучше трёх.
        s = self.stand(plan="работа")
        r = s.run("--passes", "6")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(len(s.orders()), 3)
        self.assertIn("вхолостую", r.stdout)

    def test_passes_run_out(self):
        s = self.stand(plan="падение")
        r = s.run("--passes", "2")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(len(s.orders()), 2)
        self.assertIn("проходы исчерпаны", r.stdout)

    def test_head_crash_does_not_stop_the_pipeline(self):
        # Код возврата головы тут не вердикт: вердикт это статус строки, и
        # упавший проход поднимается следующим.
        s = self.stand(plan="падение|закрой")
        r = s.run()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(len(s.orders()), 2)

    def test_board_silence_is_not_a_blind_launch(self):
        s = self.stand()
        env = s.env()
        env["PATH"] = os.environ.get("PATH", "")
        argv = [sys.executable, RUN, "DK-1", "-C", s.root, "--",
                os.path.join(s.bin, "claude-stub")]
        r = subprocess.run(argv, capture_output=True, text=True, env=env)
        self.assertEqual(r.returncode, 2)
        self.assertEqual(s.orders(), [])


if __name__ == "__main__":
    unittest.main(verbosity=0)
