"""Тесты сторожка цикла цели.

Стенд тут маленький и свой: дом с реестром, проект с доской и журналом
запусков. Уведомитель и launchctl зовутся подставным запускателем, потому что
проверяется решение сторожка (звать или молчать), а не то, как выглядит баннер;
живой канал уведомлений проверяет notify_test.py.
"""
import io
import os
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta
from pathlib import Path

import testenv
import watch

GOAL = "DK-900"
BOARD = """# Задачи стенда

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
%s

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
%s
"""
ROW = "| %s | Цель: стенд | task | P1 | 60 (50+5+3+0+2) | XL | [tasks/%s.md](tasks/%s.md) |"


def stamp(when):
    return when.strftime(watch.STAMP)


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


class Stand(unittest.TestCase):
    """Дом с реестром и проект с доской, целью в In progress и журналом."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="watch-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit" / "goals").mkdir(parents=True)
        self.proj = self.dir / "proj"
        (self.proj / ".devkit").mkdir(parents=True)
        (self.proj / "docs" / "tasks").mkdir(parents=True)
        self.now = datetime(2026, 8, 7, 12, 0, 0)
        self.board(in_progress=True)

    def board(self, in_progress=True, goal=GOAL):
        row = ROW % (goal, goal, goal)
        text = BOARD % (row if in_progress else "", "" if in_progress else row)
        (self.proj / "docs" / "TASKS.md").write_text(text, encoding="utf-8")

    def runlog(self, ago_minutes, tool="agentctl", cmd="spend"):
        when = stamp(self.now - timedelta(minutes=ago_minutes))
        with open(str(self.proj / ".devkit" / "log"), "a", encoding="utf-8") as f:
            f.write("%s\t%s\t%s\t0\n" % (when, tool, cmd))

    def entry(self, seen_minutes=None, goal=GOAL, root=None, **extra):
        data = {"goal": goal, "root": str(self.proj if root is None else root),
                "file": str(self.proj / "docs" / "tasks" / ("%s.md" % goal))}
        if seen_minutes is not None:
            data["seen"] = stamp(self.now - timedelta(minutes=seen_minutes))
        data.update(extra)
        path = self.home / ".devkit" / "goals" / ("%s-стенд.watch" % goal)
        watch.write_entry(path, data)
        return path

    def look(self, path, idle=45 * 60, call=None):
        call = Fake() if call is None else call
        called, line = watch.look(path, self.now, idle, call)
        return called, line, call


class LookTest(Stand):

    def test_stopped_loop_shouts(self):
        # Инцидент DK-153: цель в работе, а движения нет часами. Сторожок обязан
        # позвать громко и назвать цель со временем простоя.
        path = self.entry(seen_minutes=200)
        self.runlog(200)
        called, line, call = self.look(path)
        self.assertTrue(called, "вставший цикл не позвал: %s" % line)
        self.assertIn(GOAL, line)
        self.assertIn("3ч", line, "в отчёте нет времени простоя: %s" % line)
        notify = call.argv_with("notify.py")
        self.assertEqual(len(notify), 1, "уведомитель позван не один раз: %s" % call.calls)
        self.assertNotIn("--quiet", notify[0], "зов вставшего цикла обязан быть громким")
        self.assertIn(GOAL, notify[0][2], "в заголовке уведомления нет цели: %s" % notify[0])
        self.assertIn(str(self.proj.name), notify[0][3])

    def test_shout_names_the_whole_resume_command(self):
        # Сам цикл сторожок не поднимает, поэтому зов обязан нести команду
        # продолжения целиком: путь до оболочки, цель и корень проекта. Человек
        # её выполняет, а не собирает по доке, и путь тут абсолютный, потому
        # что оболочка на машину не уезжает и в PATH её нет.
        path = self.entry(seen_minutes=200)
        self.runlog(200)
        called, line, call = self.look(path)
        self.assertTrue(called, line)
        body = call.argv_with("notify.py")[0][3]
        want = "python3 %s %s -C %s" % (
            watch.DEVKIT / "kit" / "skills" / "goal-loop" / "goal-run.py", GOAL, self.proj)
        self.assertIn(want, body, "в зове нет готовой команды продолжения: %s" % body)

    def test_stop_marked_in_entry(self):
        # Отметка стопа это и защита от баннера каждые пять минут, и тот самый
        # факт «стоп замечен», на который встаёт перезапуск витка.
        path = self.entry(seen_minutes=200)
        self.look(path)
        self.assertEqual(watch.read_entry(path)["stopped"], stamp(self.now))

    def test_second_pass_keeps_quiet(self):
        path = self.entry(seen_minutes=200)
        self.look(path)
        called, line, call = self.look(path)
        self.assertFalse(called, "по тому же стопу позвали второй раз: %s" % line)
        self.assertEqual(call.calls, [])
        self.assertIn("уже позвали", line)

    def test_live_loop_is_silent(self):
        # Живой цикл дёргать нельзя: утилита звалась только что.
        path = self.entry(seen_minutes=1)
        self.runlog(1)
        called, line, call = self.look(path)
        self.assertFalse(called, "живой цикл позвал: %s" % line)
        self.assertEqual(call.calls, [])
        self.assertIn("тихо", line)

    def test_movement_clears_the_mark(self):
        path = self.entry(seen_minutes=200, stopped=stamp(self.now - timedelta(minutes=100)))
        self.runlog(1)
        called, line, _ = self.look(path)
        self.assertFalse(called)
        self.assertNotIn("stopped", watch.read_entry(path),
                         "цикл поехал, а отметка стопа осталась: %s" % line)

    def test_run_log_beats_stale_gate_line(self):
        # Движение меряется по журналу запусков: строка гейта старая, но виток
        # только что звал taskctl, и это движение.
        path = self.entry(seen_minutes=200)
        self.runlog(200)
        self.runlog(2, tool="taskctl", cmd="move")
        called, line, _ = self.look(path)
        self.assertFalse(called, "виток двигался, а сторожок позвал: %s" % line)

    def test_no_run_log_falls_back_to_gate(self):
        path = self.entry(seen_minutes=200)
        called, line, _ = self.look(path)
        self.assertTrue(called, "без журнала запусков цель осталась без надзора: %s" % line)

    def test_goal_out_of_progress_drops_the_entry(self):
        path = self.entry(seen_minutes=200)
        self.board(in_progress=False)
        called, line, call = self.look(path)
        self.assertFalse(called, "цель уже не в работе, а сторожок позвал: %s" % line)
        self.assertFalse(path.exists(), "запись снятой цели осталась в реестре")
        self.assertIn("Check", line)
        self.assertEqual(call.calls, [])

    def test_goal_off_the_board_drops_the_entry(self):
        path = self.entry(seen_minutes=200)
        self.board(in_progress=False, goal="DK-901")
        called, _, _ = self.look(path)
        self.assertFalse(called)
        self.assertFalse(path.exists(), "закрытая цель осталась в реестре")

    def test_missing_root_drops_the_entry(self):
        path = self.entry(seen_minutes=200, root=str(self.dir / "нет"))
        called, line, _ = self.look(path)
        self.assertFalse(called)
        self.assertFalse(path.exists())
        self.assertIn("снята", line)

    def test_broken_board_still_watched(self):
        # Доски нет вовсе: проект сломан, и это тем более повод звать, а не
        # снимать цель с надзора.
        os.remove(str(self.proj / "docs" / "TASKS.md"))
        path = self.entry(seen_minutes=200)
        called, line, _ = self.look(path)
        self.assertTrue(called, "проект без доски остался без надзора: %s" % line)
        self.assertTrue(path.exists())


class RunTest(Stand):

    def sweep(self, idle=None, call=None):
        call = Fake() if call is None else call
        out = io.StringIO()
        rc = watch.run(now=self.now, idle=idle, home=self.home, out=out, call=call)
        return rc, out.getvalue(), call

    def test_exit_code_names_the_find(self):
        self.entry(seen_minutes=200)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 1, out)
        self.entry(seen_minutes=1)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 0, out)

    def test_empty_registry_says_so(self):
        rc, out, call = self.sweep()
        self.assertEqual(rc, 0)
        self.assertIn("целей под надзором нет", out)
        self.assertEqual(call.calls, [])

    def test_heartbeat_written(self):
        # По этой строке доктор судит, работает ли носитель сторожка.
        self.entry(seen_minutes=1)
        self.sweep()
        beat = (self.home / ".devkit" / "goal-watch.log").read_text(encoding="utf-8")
        self.assertIn("целей под надзором 1", beat)
        self.assertIsNotNone(watch.heartbeat(self.home))

    def test_threshold_from_config(self):
        # Порог настраиваемый: тот же простой при большем пороге зова не даёт.
        self.entry(seen_minutes=50)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 1, out)
        (self.home / ".devkit" / "watch.local").write_text("idle = 90\n", encoding="utf-8")
        self.entry(seen_minutes=50)
        rc, out, _ = self.sweep()
        self.assertEqual(rc, 0, "порог из ~/.devkit/watch.local не подхвачен: %s" % out)


class ConfigTest(Stand):

    def test_default_and_overrides(self):
        self.assertEqual(watch.conf_idle(self.home), watch.IDLE)
        conf = self.home / ".devkit" / "watch.local"
        conf.write_text("# порог\nidle = 20\n", encoding="utf-8")
        self.assertEqual(watch.conf_idle(self.home), 20 * 60)
        conf.write_text("idle = скоро\n", encoding="utf-8")
        self.assertEqual(watch.conf_idle(self.home), watch.IDLE,
                         "непонятная строка порога обязана падать в умолчание")

    def test_entry_keeps_unknown_keys(self):
        # Перезапуск витка допишет сюда свои ключи, и терять их сторожку нельзя.
        path = self.entry(seen_minutes=200, tries="2")
        self.look(path)
        self.assertEqual(watch.read_entry(path)["tries"], "2")

    def test_run_log_tail_survives_cut_line(self):
        # Журнал читается хвостом, и первая строка хвоста бывает обрезанной.
        with open(str(self.proj / ".devkit" / "log"), "w", encoding="utf-8") as f:
            f.write("x" * (watch.TAIL + 10) + "\n")
        self.runlog(7)
        self.assertEqual(watch.last_run_stamp(str(self.proj)),
                         self.now - timedelta(minutes=7))

    def test_board_section_read(self):
        self.assertEqual(watch.board_section(str(self.proj), GOAL), "In progress")
        self.board(in_progress=False)
        self.assertTrue(watch.board_section(str(self.proj), GOAL).startswith("Check"))


class AgentTest(Stand):
    """Носитель расписания: launchd-агент и находки доктора про него."""

    def setUp(self):
        super().setUp()
        self.main = self.dir / "devkit"
        (self.main / "tools" / "devkitctl").mkdir(parents=True)
        self.plist = self.home / "Library" / "LaunchAgents" / ("%s.plist" % watch.LABEL)

    def check(self, fix=False, call=None, from_main=True, platform="darwin"):
        call = Fake() if call is None else call
        f, d = watch.check(fix=fix, main=self.main, from_main=from_main,
                           home=self.home, platform=platform, call=call)
        return f, d, call

    def beat(self, ago_minutes=1):
        (self.home / ".devkit" / "goal-watch.log").write_text(
            "%s целей под надзором 0, вставших 0\n"
            % stamp(datetime.now() - timedelta(minutes=ago_minutes)), encoding="utf-8")

    def test_missing_agent_is_a_finding(self):
        f, d, _ = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn("doctor --fix", f[0])
        self.assertEqual(d, [])

    def test_fix_installs_and_loads(self):
        f, d, call = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertTrue(self.plist.exists(), "агент не положен")
        text = self.plist.read_text(encoding="utf-8")
        self.assertIn(str(self.main / "tools" / "devkitctl" / "devkitctl.py"), text)
        self.assertIn("<string>watch</string>", text)
        self.assertIn("<integer>%d</integer>" % watch.EVERY, text)
        self.assertTrue(call.argv_with("bootstrap"), "launchd не позван: %s" % call.calls)

    def test_fix_from_worktree_refuses(self):
        # Агент показывает на чекаут, и класть на машину ветку задачи нельзя.
        f, d, call = self.check(fix=True, from_main=False)
        self.assertEqual(d, [])
        self.assertIn("основного чекаута", f[0])
        self.assertFalse(self.plist.exists())
        self.assertEqual(call.calls, [])

    def test_agent_pointing_elsewhere(self):
        self.check(fix=True)
        old = self.plist.read_text(encoding="utf-8").replace(str(self.main), "/старый/чекаут")
        self.plist.write_text(old, encoding="utf-8")
        f, _, _ = self.check()
        self.assertIn("/старый/чекаут", f[0])
        f, d, _ = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertIn(str(self.main), self.plist.read_text(encoding="utf-8"))

    def test_agent_not_loaded(self):
        self.check(fix=True)
        self.beat()
        f, _, _ = self.check(call=Fake(code=1))
        self.assertIn("не поднят", f[0])

    def test_silent_agent_is_a_finding(self):
        self.check(fix=True)
        self.beat(ago_minutes=(watch.HEARTBEAT_MISS * watch.EVERY) // 60 + 5)
        f, _, _ = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn("не отрабатывал", f[0])

    def test_working_agent_is_quiet(self):
        self.check(fix=True)
        self.beat()
        f, d, _ = self.check()
        self.assertEqual((f, d), ([], []))

    def test_other_platform_says_it_has_no_carrier(self):
        f, _, _ = self.check(platform="linux")
        self.assertEqual(f, [], "без целей под надзором на чужой платформе находки нет")
        self.entry(seen_minutes=1)
        f, _, _ = self.check(platform="linux")
        self.assertIn("носителя сторожка", f[0])


class CommandTest(testenv.SandboxCase):
    """Сторожок изнутри devkitctl: команда и её след в журнале запусков."""

    def test_watch_does_not_touch_the_run_log(self):
        # Движение цикла сторожок меряет журналом запусков, и своя строка раз в
        # пять минут выглядела бы там движением на вставшем цикле.
        devdir = self.box.dk / ".devkit"
        devdir.mkdir()
        try:
            rc, out = self.box.dkctl_run("watch")
            self.assertEqual(rc, 0, out)
            self.assertIn("целей под надзором нет", out)
            self.assertFalse((devdir / "log").exists(),
                             "сторожок написал в журнал запусков: %s" % out)
        finally:
            shutil.rmtree(str(devdir), ignore_errors=True)

    def test_watch_finds_a_stopped_goal(self):
        proj = self.box.project("вставший", board=False)
        (proj / ".devkit").mkdir()
        (proj / "docs").mkdir(parents=True, exist_ok=True)
        (proj / "docs" / "TASKS.md").write_text(
            BOARD % (ROW % (GOAL, GOAL, GOAL), ""), encoding="utf-8")
        old = stamp(datetime.now() - timedelta(hours=3))
        (proj / ".devkit" / "log").write_text("%s\tagentctl\tspend\t0\n" % old, encoding="utf-8")
        goals = self.box.home / ".devkit" / "goals"
        goals.mkdir(parents=True, exist_ok=True)
        watch.write_entry(goals / ("%s-стенд.watch" % GOAL),
                          {"goal": GOAL, "root": str(proj), "seen": old})
        self.addCleanup(shutil.rmtree,
                        str(self.box.home / ".devkit" / "goals"), True)
        rc, out = self.box.dkctl_run("watch")
        self.assertEqual(rc, 1, out)
        self.assertIn(GOAL, out)
        self.assertIn("зову", out)
        # Уведомитель позван настоящий, и он молчит про корень во временной
        # директории: стенд ему песочница, а не рабочий проект.
        self.assertIn("уведомление пропущено", out)


if __name__ == "__main__":
    unittest.main()
