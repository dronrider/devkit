#!/usr/bin/env python3
"""Самопроверка сторожа фоновых работ (DK-519, разряд команд DK-571): реестр
запущенных работ и сдача сессии того, о чём её не уведомили. Разбор идёт с
живых образцов запуска и конца хода, стенд это временный каталог реестра и
подложный транскрипт, а время и ожидание вести подставляются, чтобы прогон не
зависел ни от часов, ни от sleep.
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
# ID фоновой команды и сама команда: ID у харнеса короткий и не похож на ID
# субагента, и путать их в стенде незачем.
SHID = "boat3slub"
CMD = "shipctl merge DK-571"
NOW = 1787657000.0


def sample(name):
    with open(os.path.join(DATA, name + ".json"), encoding="utf-8") as f:
        return json.load(f)


def event(kind, session=SID, transcript="", agent_id="", agent_type="general-purpose",
          description="", output="", message="", jobs=(), active=False,
          job_kind="subagent", command=""):
    return hookio.Agent(kind=kind, session=session, cwd="/tmp/work", transcript=transcript,
                        agent_id=agent_id, job=job_kind, agent_type=agent_type,
                        description=description, command=command, output=output,
                        message=message, jobs=jobs, active=active)


def job(agent_id=AID, kind="subagent", status="running", description="разбор", command=""):
    return hookio.Job(id=agent_id, kind=kind, status=status, description=description,
                      command=command)


def notice(job_id=SHID, code=0, name="Слияние ветки"):
    """Весть харнеса о конце фоновой команды, снятая живьём (DK-571): очередь
    несёт её в транскрипт целиком, вместе с кодом возврата в тексте."""
    return ('{"type":"queue-operation","operation":"enqueue","content":"<task-notification>\\n'
            '<task-id>%s</task-id>\\n<status>completed</status>\\n'
            '<summary>Background command \\"%s\\" completed (exit code %d)</summary>\\n'
            '</task-notification>"}\n' % (job_id, name, code))


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

    def launch(self, agent_id=AID, description="разбор черновиков", output="/tmp/out.txt",
               now=NOW):
        return self.handle(event(hookio.AGENT_LAUNCHED, agent_id=agent_id,
                                 description=description, output=output), now=now)

    def finish(self, agent_id=AID, message="разобрано три черновика", now=NOW):
        return self.handle(event(hookio.SUBAGENT_DONE, agent_id=agent_id, message=message),
                           now=now)

    def launch_shell(self, job_id=SHID, command=CMD, description="слияние задачи", now=NOW):
        return self.handle(event(hookio.AGENT_LAUNCHED, agent_id=job_id, agent_type="",
                                 job_kind="shell", command=command,
                                 description=description), now=now)

    def journal(self):
        with open(os.path.join(self.dir, "agents.log"), encoding="utf-8") as f:
            return f.read()

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

    def test_pack_of_finishes_waits_once(self):
        # Замечание ревью: диспетчер поднимает исполнителей пачкой, и три конца
        # подряд не должны складывать задержку конца хода. Срок у пачки один.
        path = self.transcript("")
        for n in range(3):
            self.launch(agent_id="agent%d" % n, description="работа %d" % n)
            self.finish(agent_id="agent%d" % n, message="готово %d" % n)
        said = self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 1)
        self.assertAlmostEqual(self.sleeper.slept, watch.GRACE - 1, places=3)
        for n in range(3):
            self.assertIn("работа %d" % n, said["reason"])

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

    # Разряд команд оболочки (DK-571)

    def test_background_command_sample_is_a_launch(self):
        ev = hookio.parse_agent("claude-code", sample("tool-done-bash-background"))
        self.assertEqual(ev.kind, hookio.AGENT_LAUNCHED)
        self.assertEqual(ev.job, "shell")
        self.assertEqual(ev.agent_id, "bpmzinhpl")
        self.assertIn("sleep 8", ev.command)

    def test_shell_turn_sample_carries_the_command(self):
        ev = hookio.parse_agent("claude-code", sample("turn-done-shell"))
        self.assertEqual([(j.id, j.kind, j.status) for j in ev.jobs],
                         [("boat3slub", "shell", "running")])
        self.assertIn("sleep 40", ev.jobs[0].command)

    def test_command_launch_lands_in_the_registry(self):
        self.launch_shell()
        entry = self.registry()[SHID]
        self.assertEqual(entry["job"], "shell")
        self.assertEqual(entry["command"], CMD)
        self.assertEqual(entry["state"], watch.RUNNING)
        line = self.journal()
        self.assertIn("разряд shell", line)
        self.assertIn("событие запуск", line)
        self.assertIn(CMD, line)

    def test_command_from_the_job_list_is_registered(self):
        # Раскладка хуков, положенная до разряда команд, зовёт сторожа одним
        # инструментом делегирования, и запуск команды до него не доходит.
        # Перечень работ конца хода приходит всегда, и счёт заводится по нему.
        said = self.handle(event(hookio.TURN_DONE, transcript=self.transcript(""),
                                 jobs=(job(SHID, kind="shell", command=CMD),)), now=NOW + 60)
        self.assertIsNone(said)
        entry = self.registry()[SHID]
        self.assertEqual(entry["job"], "shell")
        self.assertEqual(entry["command"], CMD)
        self.assertEqual(entry["state"], watch.RUNNING)
        self.assertIn("событие запуск", self.journal())

    def test_running_command_is_not_counted_lost(self):
        # Команда, которую харнес всё ещё числит своей, идёт, и сдавать про неё
        # нечего: пропажа считается только по субагентам.
        self.launch_shell()
        said = self.handle(event(hookio.TURN_DONE, transcript=self.transcript(""),
                                 jobs=(job(SHID, kind="shell", command=CMD),)), now=NOW + 60)
        self.assertIsNone(said)
        self.assertEqual(self.registry()[SHID]["state"], watch.RUNNING)

    def test_command_alive_at_the_end_of_the_turn_leaves_a_line(self):
        # Ход кончился, команда идёт: разбудить сессию её концу нечем, и умрёт
        # она вместе с сессией. Строка журнала это единственный след.
        self.launch_shell()
        self.handle(event(hookio.TURN_DONE, transcript=self.transcript(""),
                          jobs=(job(SHID, kind="shell", command=CMD),)), now=NOW + 60)
        line = self.journal()
        self.assertIn("событие ожидание", line)
        self.assertIn("разряд shell", line)
        self.assertIn("вместе с сессией", line)

    def test_command_gone_from_the_job_list_is_finished(self):
        # Своего события про конец команды у харнеса нет, и конец её виден
        # только тем, что работы в перечне не стало. Код возврата приезжает
        # вестью харнеса, и она же значит, что сессия про конец знает.
        path = self.transcript(notice())
        self.launch_shell()
        said = self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertIsNone(said)
        entry = self.registry()[SHID]
        self.assertEqual(entry["state"], watch.DONE)
        line = self.journal()
        self.assertIn("событие конец", line)
        self.assertIn("код возврата 0", line)
        self.assertIn(CMD, line)

    def test_finished_command_without_a_notice_is_handed_over(self):
        # Регрессия DK-571: фоновое слияние доехало, весть о нём потерялась, и
        # сессия ушла бы спать, считая команду идущей.
        path = self.transcript("")
        self.launch_shell()
        said = self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertIsNotNone(said)
        self.assertEqual(said["decision"], "block")
        self.assertIn("фоновая команда", said["reason"])
        self.assertIn(CMD, said["reason"])
        self.assertIn(SHID, said["reason"])
        self.assertTrue(self.registry()[SHID]["told"])
        self.assertIn("событие сдача", self.journal())

    def test_failed_command_carries_its_exit_code(self):
        # Код возврата читается из вести харнеса, а не выдумывается: упавшее
        # слияние отличается от прошедшего только им.
        path = self.transcript(notice(code=1))
        self.launch_shell()
        self.handle(event(hookio.TURN_DONE, transcript=path), now=NOW + 60)
        self.assertEqual(self.registry()[SHID]["message"], "1")
        self.assertIn("код возврата 1", self.journal())

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
        # Единственный заход, где время берётся с часов: сторож в подпроцессе
        # считает now сам, и запись, заведённая застывшей датой, вымелась бы у
        # него по LIFETIME на следующий день после написания теста.
        path = self.transcript("")
        live = time.time()
        self.launch(now=live)
        self.finish(now=live)
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

    def test_hook_counts_a_background_command(self):
        # Ход Bash с фоновым ответом, снятый живьём: сторож стоит на нём тем же
        # матчером, что и на делегировании, и команда обязана попасть в реестр.
        launch = sample("tool-done-bash-background")
        launch["session_id"] = SID
        env = dict(os.environ)
        env[watch.DIR_ENV] = self.dir
        run = subprocess.run([sys.executable, HOOK, "--hook", "claude-code"],
                             input=json.dumps(launch), capture_output=True, text=True, env=env)
        self.assertEqual(run.returncode, 0, run.stderr)
        entry = self.registry()["bpmzinhpl"]
        self.assertEqual(entry["job"], "shell")
        self.assertIn("sleep 8", entry["command"])
        self.assertIn("разряд shell", self.journal())

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
