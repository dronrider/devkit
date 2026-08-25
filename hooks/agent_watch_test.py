#!/usr/bin/env python3
"""Самопроверка сторожа фоновых субагентов (DK-519): реестр запущенных работ и
сдача сессии того, о чём её не уведомили. Разбор идёт с живых образцов запуска и
конца хода, стенд это временный каталог реестра и подложный транскрипт, а время
и ожидание вести подставляются, чтобы прогон не зависел ни от часов, ни от sleep.
Хук вдобавок гоняется подпроцессом с подсунутым stdin: важно не только что
функция считает, но и что команда из settings.json печатает решение и уходит
нулём.
"""
import importlib
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "agent-watch.py")
DATA = os.path.join(HERE, "testdata", "claude-code")

sys.path.insert(0, HERE)
import hookio  # noqa: E402
watch = importlib.import_module("agent-watch")

SID = "0ebb6e3b-7d4e-4b8b-8a82-a340dd843209"
AID = "a555b8615fe4588f3"
NOW = 1787657000.0


def sample(name):
    with open(os.path.join(DATA, name + ".json"), encoding="utf-8") as f:
        return json.load(f)


def event(kind, session=SID, transcript="", agent_id="", agent_type="general-purpose",
          description="", output="", message="", jobs=(), active=False):
    return hookio.Agent(kind=kind, session=session, cwd="/tmp/work", transcript=transcript,
                        agent_id=agent_id, agent_type=agent_type, description=description,
                        output=output, message=message, jobs=jobs, active=active)


def job(agent_id=AID, kind="subagent", status="running", description="разбор"):
    return hookio.Job(id=agent_id, kind=kind, status=status, description=description)


class Sleeper(object):
    """Подставное ожидание: считает, сколько сторож просидел бы на транскрипте."""

    def __init__(self):
        self.slept = 0.0

    def __call__(self, seconds):
        self.slept += seconds


class Watch(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="dk519-")
        self.env = {watch.DIR_ENV: self.dir}
        self.out = []

    def handle(self, ev, now=NOW, env=None):
        """Событие через сторожа. Возвращает напечатанное решение либо None."""
        sink = _Sink()
        self.sleeper = Sleeper()
        watch.handle(ev, self.env if env is None else env, now, self.sleeper, sink)
        return sink.value()

    def registry(self):
        return watch.load_registry(watch.registry_path(SID, self.env))

    def transcript(self, text=""):
        path = os.path.join(self.dir, "session.jsonl")
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def launch(self, agent_id=AID, description="разбор черновиков", output="/tmp/out.txt"):
        return self.handle(event(hookio.AGENT_LAUNCHED, agent_id=agent_id,
                                 description=description, output=output))

    def finish(self, agent_id=AID, message="разобрано три черновика", now=NOW):
        return self.handle(event(hookio.SUBAGENT_DONE, agent_id=agent_id, message=message),
                           now=now)

    # Разбор живых образцов

    def test_launch_sample_is_a_background_agent(self):
        ev = hookio.parse_agent("claude-code", sample("tool-done-agent-launch"))
        self.assertEqual(ev.kind, hookio.AGENT_LAUNCHED)
        self.assertEqual(ev.agent_id, "ac08b50abbc64e119")
        self.assertEqual(ev.agent_type, "general-purpose")
        self.assertTrue(ev.output.endswith(".output"))

    def test_plain_tool_turn_is_not_a_launch(self):
        self.assertIsNone(hookio.parse_agent("claude-code", sample("tool-done-bash")))

    def test_turn_sample_carries_the_background_jobs(self):
        ev = hookio.parse_agent("claude-code", sample("turn-done-background"))
        self.assertEqual(ev.kind, hookio.TURN_DONE)
        self.assertEqual([(j.id, j.kind, j.status) for j in ev.jobs],
                         [("ac08b50abbc64e119", "subagent", "running")])

    def test_subagent_sample_names_the_agent(self):
        ev = hookio.parse_agent("claude-code", sample("subagent-done"))
        self.assertEqual(ev.kind, hookio.SUBAGENT_DONE)
        self.assertEqual(ev.agent_id, "a7131ebeaabc745f2")

    # Реестр

    def test_launch_lands_in_the_registry(self):
        self.launch()
        entry = self.registry()[AID]
        self.assertEqual(entry["state"], watch.RUNNING)
        self.assertEqual(entry["description"], "разбор черновиков")
        self.assertEqual(entry["output"], "/tmp/out.txt")

    def test_finish_marks_the_entry_done(self):
        self.launch()
        self.finish()
        entry = self.registry()[AID]
        self.assertEqual(entry["state"], watch.DONE)
        self.assertEqual(entry["message"], "разобрано три черновика")

    def test_synchronous_agent_stays_out_of_the_registry(self):
        # Конец субагента, которого никто не запускал фоном: он отчитался ходом
        # инструмента, и сторожу тут делать нечего.
        self.finish(agent_id="deadbeef")
        self.assertEqual(self.registry(), {})

    # Сдача

    def test_finished_agent_without_a_notice_is_handed_over(self):
        # Регрессия DK-519: ход кончился, отчёт субагента до сессии не дошёл, и
        # без сторожа сессия ушла бы спать, считая его работающим.
        path = self.transcript("")
        self.launch()
        self.finish()
        said = self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertIsNotNone(said)
        self.assertEqual(said["decision"], "block")
        self.assertIn(AID, said["reason"])
        self.assertIn("разобрано три черновика", said["reason"])
        self.assertIn("/tmp/out.txt", said["reason"])
        self.assertTrue(self.registry()[AID]["told"])

    def test_delivered_notice_keeps_the_watchdog_quiet(self):
        path = self.transcript('{"type":"queue-operation","content":"<task-notification>\\n'
                               '<task-id>%s</task-id>"}\n' % AID)
        self.launch()
        self.finish()
        self.assertIsNone(self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60))
        self.assertTrue(self.registry()[AID]["told"])

    def test_fresh_finish_waits_for_the_notice(self):
        # Конец, случившийся секунду назад: весть харнеса ещё в пути, и сторож
        # пережидает её, а не сдаёт наперегонки.
        path = self.transcript("")
        self.launch()
        self.finish()
        self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 1)
        self.assertAlmostEqual(self.sleeper.slept, watch.GRACE - 1, places=3)

    def test_old_finish_is_handed_over_at_once(self):
        path = self.transcript("")
        self.launch()
        self.finish()
        self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertEqual(self.sleeper.slept, 0.0)

    def test_handover_happens_once(self):
        path = self.transcript("")
        self.launch()
        self.finish()
        self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertIsNone(self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 61))

    def test_running_agent_is_left_alone(self):
        path = self.transcript("")
        self.launch()
        self.assertIsNone(self.handle(event(hookio.TURN_DONE, transcript=path, jobs=(job(),)),
                                      now=NOW + 60))
        self.assertEqual(self.registry()[AID]["state"], watch.RUNNING)

    def test_agent_gone_from_the_job_list_is_reported_lost(self):
        # Субагент, убитый перезапуском процесса харнеса: конца его сессия не
        # видела, и в перечне фоновых работ его больше нет.
        path = self.transcript("")
        self.launch()
        said = self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertIn("кончился молча", said["reason"])
        self.assertEqual(self.registry()[AID]["state"], watch.LOST)

    def test_failed_job_is_reported(self):
        path = self.transcript("")
        self.launch()
        said = self.handle(event(hookio.TURN_DONE, transcript=path,
                                 jobs=(job(status="failed"),)), now=NOW + 60)
        self.assertIn("кончился молча", said["reason"])

    def test_someone_elses_job_does_not_count(self):
        # Фоновая команда оболочки с тем же ID сторожа не касается: он смотрит
        # только на работы разряда subagent.
        path = self.transcript("")
        self.launch()
        said = self.handle(event(hookio.TURN_DONE, transcript=path,
                                 jobs=(job(kind="shell"),)), now=NOW + 60)
        self.assertIsNotNone(said)

    def test_continued_turn_is_not_handed_over_twice(self):
        path = self.transcript("")
        self.launch()
        self.finish()
        self.assertIsNone(self.handle(event(hookio.TURN_DONE, transcript=path, active=True),
                                      now=NOW + 60))

    def test_stale_entries_are_swept(self):
        self.launch()
        self.handle(event(hookio.TURN_DONE, transcript=self.transcript("")),
                    now=NOW + watch.LIFETIME + 1)
        self.assertEqual(self.registry(), {})

    def test_log_follows_the_registry_dir(self):
        # Журнал стенда лежит в своём каталоге: дописывать машинный файл, по
        # которому разбирают живые жалобы, самопроверка не вправе.
        self.launch()
        with open(os.path.join(self.dir, "agents.log"), encoding="utf-8") as f:
            line = f.read()
        self.assertIn("событие запуск", line)
        self.assertIn(AID, line)
        self.assertEqual(watch.log_path({}), watch.LOG)

    def test_busy_lock_does_not_break_the_turn(self):
        # Замок держит соседний ход той же сессии: сторож пропускает правку и
        # уходит нулём, а не роняет чужую работу из-за своего файла.
        self.launch()
        path = watch.registry_path(SID, self.env)
        held = watch.take_lock(path)
        self.assertIsNotNone(held)
        try:
            said = self.handle(event(hookio.SUBAGENT_DONE, agent_id=AID, message="готово"))
            self.assertIsNone(said)
            self.assertEqual(self.registry()[AID]["state"], watch.RUNNING)
        finally:
            watch.drop_lock(held)
        self.finish()
        self.assertEqual(self.registry()[AID]["state"], watch.DONE)

    def test_parallel_launches_both_land(self):
        # Диспетчер поднимает исполнителей пачкой, и два хода инструмента правят
        # один файл: без замка запись про один запуск затирала бы соседнюю.
        import threading
        done = []

        def run(n):
            watch.handle(event(hookio.AGENT_LAUNCHED, agent_id="agent%d" % n,
                               description="работа %d" % n), self.env, NOW, time.sleep, _Sink())
            done.append(n)

        threads = [threading.Thread(target=run, args=(n,)) for n in range(6)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(sorted(done), list(range(6)))
        self.assertEqual(sorted(self.registry()), ["agent%d" % n for n in range(6)])

    def test_switch_off_stops_the_watchdog(self):
        env = dict(self.env)
        env[watch.OFF_ENV] = "1"
        code = watch.run_hook("claude-code", env)
        self.assertEqual(code, 0)
        self.assertEqual(self.registry(), {})

    # Прогон командой из settings.json

    def test_hook_prints_the_decision_and_exits_zero(self):
        path = self.transcript("")
        self.launch()
        self.finish()
        turn = sample("turn-done")
        turn["session_id"] = SID
        turn["transcript_path"] = path
        env = dict(os.environ)
        env[watch.DIR_ENV] = self.dir
        env["DEVKIT_AGENT_WATCH_GRACE"] = "0"
        run = subprocess.run([sys.executable, HOOK, "--hook", "claude-code"],
                             input=json.dumps(turn), capture_output=True, text=True, env=env)
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertEqual(json.loads(run.stdout)["decision"], "block")

    def test_unknown_protocol_is_a_refusal(self):
        run = subprocess.run([sys.executable, HOOK, "--hook", "нетакого"],
                             input="{}", capture_output=True, text=True)
        self.assertEqual(run.returncode, 2)
        self.assertIn("нетакого", run.stderr)

    def test_junk_on_stdin_does_not_break_the_session(self):
        run = subprocess.run([sys.executable, HOOK, "--hook", "claude-code"],
                             input="не json", capture_output=True, text=True)
        self.assertEqual(run.returncode, 0)
        self.assertEqual(run.stdout, "")


class _Sink(object):
    """Куда сторож печатает решение: канал сдачи ждёт файлоподобный объект."""

    def __init__(self):
        self.text = ""

    def write(self, text):
        self.text += text

    def value(self):
        return json.loads(self.text) if self.text.strip() else None


if __name__ == "__main__":
    unittest.main()
