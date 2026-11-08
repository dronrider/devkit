"""Автоматическое восстановление исчезнувшего конвейера (DK-1645).

Сценарий проверки задачи: одна попытка на инцидент и корень, три попытки
за скользящие сутки, машинный замок операции, устойчивый резерв до запуска
и подтверждение только связанным с попыткой живым ходом оболочки. Код 0,
чужая голова и мёртвый pid инцидент не закрывают, сухой прогон не пишет
ничего.
"""
import fcntl
import json
import os
import shutil
import subprocess
import sys
import select
import tempfile
import unittest
import time
from unittest.mock import patch
from datetime import datetime, timedelta
from pathlib import Path

import duty
import recovery
import taskstate


class Fake:
    """Подставной запускатель: помнит вызовы и отвечает заданным кодом."""

    def __init__(self, code=0, out=""):
        self.calls = []
        self.code = code
        self.out = out

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        return subprocess.CompletedProcess(argv, self.code, self.out, None)


class Shell(Fake):
    """Подставная оболочка конвейера: берёт метку из заказа и оставляет
    доказательство хода, как его пишет task-run.py, вместе с замком задачи."""

    def __init__(self, code=0, stage="turn", home=None, proj=None, pid=None,
                 bad_mark=False, bad_root=False, lock_pid=None):
        Fake.__init__(self, code=code)
        self.stage = stage
        self.home = home
        self.proj = proj
        self.pid = os.getpid() if pid is None else pid
        self.lock_pid = self.pid if lock_pid is None else lock_pid
        self.bad_mark = bad_mark
        self.bad_root = bad_root

    def __call__(self, argv, **kw):
        res = Fake.__call__(self, argv, **kw)
        order = argv[argv.index("--order") + 1]
        found = recovery.MARK_RE.search(order)
        if not found:
            return res
        mark = "RV-" + "0" * 32 if self.bad_mark else found.group(0)
        task = argv[argv.index("run") + 1]
        root = os.path.realpath("/чужой-корень") if self.bad_root else \
            os.path.realpath(str(self.proj))
        rec = {"attempt": mark, "task": task, "root": root, "pid": self.pid,
               "stage": self.stage, "run_id": "new-run",
               "when": datetime.now().strftime(recovery.STAMP)}
        state_path = taskstate.path(self.home, self.proj, task)
        data = json.loads(state_path.read_text())
        data["run"] = {"id": "new-run", "pid": self.pid,
                       "harness": "glm-code-2", "model": "glm-5.3", "attempt": mark}
        state_path.write_text(json.dumps(data))
        path = os.path.join(recovery.proof_dir(self.home),
                            "%s.json" % mark)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(rec, f, ensure_ascii=False)
        os.replace(tmp, path)
        lock = Path(self.home) / ".devkit" / ("task-%s.lock" % task)
        lock.mkdir(parents=True, exist_ok=True)
        (lock / "pid").write_text("%d\n" % self.lock_pid, encoding="utf-8")
        (lock / "beat").write_text("", encoding="utf-8")
        return res


class RecoveryTest(unittest.TestCase):
    """Одна попытка восстановления: бюджет, замок и доказательство."""

    TASK = "DK-901"

    def setUp(self):
        environment = patch.dict(os.environ)
        environment.start()
        self.addCleanup(environment.stop)
        for key in ("DEVKIT_RECOVERY_LOG", "DEVKIT_INCIDENTS_LOG"):
            os.environ.pop(key, None)
        self.dir = Path(tempfile.mkdtemp(prefix="recovery-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        self.proj = self.dir / "proj"
        (self.proj / "docs" / "tasks").mkdir(parents=True)
        self.board("## In progress\n"
                   "| %s | поправить мелочь | task | P3 | 1 (1+0+0+0+0) | S |"
                   " [x](x.md) |\n" % self.TASK)
        self.hook = duty.incidents_mod()
        self.source = {"id":"old-run", "harness":"glm-code-2", "model":"glm-5.3", "pid":999999999,"group":999999999}
        name = taskstate.path(self.home, self.proj, self.TASK)
        name.parent.mkdir(parents=True)
        Path(str(name)+".lock").touch()
        name.write_text(json.dumps({"version":1,"root":os.path.realpath(self.proj),"task":self.TASK,"stopped":False,"run":self.source}))
        self.ident = self.hook.report(
            self.TASK, self.hook.PROC_GONE,
            "замок задачи держит мёртвый процесс 999", run="окно dk-901",
            root=str(self.proj), home=self.home, identity=self.source)
        lock = self.home / ".devkit" / ("task-%s.lock" % self.TASK)
        lock.mkdir(parents=True, exist_ok=True)
        (lock / "pid").write_text(str(self.source["pid"]))
        self.bin = "/bin/подставной-taskctl"
        self.wait = 0.3

    def board(self, text):
        (self.proj / "docs" / "TASKS.md").write_text(
            "# Доска\n\n%s\n" % text, encoding="utf-8")

    def shell(self, **kw):
        kw.setdefault("home", self.home)
        kw.setdefault("proj", self.proj)
        return Shell(**kw)

    def inspect(self, argv, **kw):
        if "harness" in argv and "quota" not in argv:
            value = {"harnesses":[{"name":"glm-code-2", "enabled":True, "auto":True, "models":[{"model":"glm-5.3"}]}]}
        else:
            value = {"schema":"agentctl/quota","version":1,"harness":"glm-code-2","fresh":True,"complete":True,"available":True}
        return subprocess.CompletedProcess(argv, 0, json.dumps(value), "")

    def resume(self, call=None, **kw):
        kw.setdefault("inspect_call", self.inspect)
        kw.setdefault("call", call if call is not None else self.shell())
        kw.setdefault("taskctl", self.bin)
        kw.setdefault("home", self.home)
        kw.setdefault("wait", self.wait)
        rec = [r for r in self.hook.read(home=self.home)
               if r.get("id") == self.ident][0]
        return recovery.resume(self.hook, rec, self.proj, **kw)

    def record(self):
        return [r for r in self.hook.read(home=self.home)
                if r.get("id") == self.ident][0]

    def journal(self):
        return recovery._read(home=self.home)

    def reserve(self, ident=None, when=None, root=None):
        recovery._append({"kind": "reserve",
                          "when": (when or datetime.now()).strftime(
                              recovery.STAMP),
                          "attempt": recovery.new_mark(),
                          "incident": ident or ("INC-%d" % id(self)),
                          "task": self.TASK,
                          "root": root or os.path.realpath(str(self.proj))},
                         home=self.home)

    # Успех закрывает ровно один инцидент и только по связанному ходу.

    def test_confirmed_attempt_settles_exactly_this_incident(self):
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("инцидент закрыт возобновлением", joined)
        self.assertEqual(self.record().get("outcome"), self.hook.RESUMED)
        self.assertIn("попытка RV-",
                      " ".join(self.record().get("notes") or []))
        marks = [r for r in self.journal() if r.get("kind") == "reserve"]
        results = [r for r in self.journal()
                   if r.get("kind") == "result"
                   and r.get("outcome") == recovery.CONFIRMED]
        self.assertEqual((len(marks), len(results)), (1, 1), self.journal())
        order = call.calls[0][call.calls[0].index("--order") + 1]
        self.assertTrue(recovery.MARK_RE.search(order), order)
        self.assertIn("продолжай %s" % self.TASK, order)

    def test_rc0_without_proof_leaves_incident_open_and_spends(self):
        lines = self.resume(call=Fake(code=0, out="поднято, код 0"))
        joined = " ".join(lines)
        self.assertIn("доказательства рабочего хода нет", joined)
        self.assertIn("инцидент остаётся открытым", joined)
        self.assertFalse(self.record().get("outcome"))
        second = self.shell()
        again = self.resume(call=second)
        self.assertIn("уже израсходована", " ".join(again))
        self.assertEqual(second.calls, [], "повтор запустил вызов")

    def test_foreign_mark_head_does_not_confirm(self):
        lines = self.resume(call=self.shell(bad_mark=True))
        joined = " ".join(lines)
        self.assertIn("доказательства рабочего хода нет", joined)
        self.assertFalse(self.record().get("outcome"))

    def test_proof_of_other_root_is_not_confirmed(self):
        lines = self.resume(call=self.shell(bad_root=True))
        self.assertIn("доказательства рабочего хода нет", " ".join(lines))
        self.assertFalse(self.record().get("outcome"))

    def test_pid_without_tasklock_ownership_is_not_confirmed(self):
        # замок задачи после доказательства держит другой процесс
        lines = self.resume(call=self.shell(lock_pid=999999))
        joined = " ".join(lines)
        self.assertIn("ход не дождался подтверждения", joined)
        self.assertFalse(self.record().get("outcome"))

    def test_dead_shell_is_stalled_not_confirmed(self):
        p = subprocess.Popen(["/bin/sleep", "0"])
        p.wait()
        lines = self.resume(call=self.shell(pid=p.pid))
        joined = " ".join(lines)
        self.assertIn("доказательство есть, живого хода нет", joined)
        self.assertIn("инцидент остаётся открытым", joined)

    def test_start_stage_without_turn_waits_and_stalls(self):
        lines = self.resume(call=self.shell(stage="start"))
        self.assertIn("ход не дождался подтверждения", " ".join(lines))
        self.assertFalse(self.record().get("outcome"))

    def test_late_proof_does_not_close_incident(self):
        shell = self.shell()
        def late(argv, **kw):
            time.sleep(0.03)
            return shell(argv, **kw)
        lines = self.resume(call=late, wait=0.01)
        self.assertFalse(self.record().get("outcome"))
        self.assertNotIn("инцидент закрыт возобновлением", " ".join(lines))

    def test_failed_result_write_keeps_incident_open(self):
        with patch.object(recovery, "_append", return_value=False):
            _, line = recovery._finish(self.hook, self.ident,
                                      recovery.new_mark(), recovery.CONFIRMED,
                                      self.home, "", "подтверждено")
        self.assertFalse(self.record().get("outcome"))
        self.assertIn("не записался", line)

    def test_deadline_reaps_only_owned_launcher(self):
        launcher = self.dir / "launcher"
        launcher.write_text("#!/bin/sh\nexec sleep 30\n")
        launcher.chmod(0o700)
        owned = []
        popen = subprocess.Popen
        def launch(*args, **kw):
            proc = popen(*args, **kw)
            owned.append(proc)
            return proc
        start = time.monotonic()
        try:
            with patch.object(recovery.subprocess, "Popen", side_effect=launch):
                recovery._launch_and_confirm(
                    self.hook, self.ident, recovery.new_mark(), self.TASK,
                    self.proj, os.path.realpath(self.proj),
                    str(self.home / ".devkit"), taskctl=str(launcher),
                    home=self.home, wait=0.1)
            self.assertLess(time.monotonic() - start, 2)  # стенной порог: проверяется срок остановки собственного запускателя
            self.assertEqual(len(owned), 1)
            self.assertIsNotNone(owned[0].poll(), "собственный запускатель остался жив")
        finally:
            for proc in owned:
                if proc.poll() is None:
                    proc.kill()
                proc.wait()

    def test_source_state_and_quota_fail_closed(self):
        path = taskstate.path(self.home, self.proj, self.TASK)
        original = path.read_text()
        rec = self.record()
        for state in (None, "{", json.dumps({**json.loads(original), "stopped":True}),
                      json.dumps({**json.loads(original), "run": {**self.source, "id":"other"}})):
            if state is None:
                path.unlink(missing_ok=True)
            else:
                path.write_text(state)
            self.assertTrue(recovery.readiness(rec, self.proj, self.home, self.inspect))
        path.write_text(original)
        for key in ("fresh", "complete", "available"):
            def inspect(argv, **kw):
                result = self.inspect(argv, **kw)
                if "quota" in argv:
                    data = json.loads(result.stdout)
                    data[key] = False
                    result.stdout = json.dumps(data)
                return result
            self.assertTrue(recovery.readiness(rec, self.proj, self.home, inspect))
        legacy = dict(rec)
        legacy.pop("identity")
        self.assertTrue(recovery.readiness(legacy, self.proj, self.home, self.inspect))

    def test_wait_marker_distinguishes_active_and_historical_wait(self):
        from datetime import timezone
        state_path = taskstate.path(self.home, self.proj, self.TASK)
        data = json.loads(state_path.read_text())
        data["run"]["started"] = datetime.now(timezone.utc).isoformat()
        state_path.write_text(json.dumps(data))
        name = self.home / ".devkit" / "waits" / (self.TASK+".json")
        name.parent.mkdir()
        mark = {"task":self.TASK,"since":datetime.now().strftime(recovery.STAMP),
                "until":(datetime.now()+timedelta(hours=1)).strftime(recovery.STAMP),
                "kind":"срок","target":""}
        name.write_text(json.dumps(mark))
        call = self.shell()
        self.assertIn("ждёт события", " ".join(self.resume(call=call)))
        self.assertEqual(call.calls, [])
        self.assertEqual(self.journal(), [])
        mark["since"] = (datetime.now()-timedelta(hours=2)).strftime(recovery.STAMP)
        mark["until"] = (datetime.now()-timedelta(hours=1)).strftime(recovery.STAMP)
        name.write_text(json.dumps(mark))
        self.assertEqual(recovery.readiness(self.record(),self.proj,self.home,self.inspect), "")
        mark["until"] = (datetime.now()+timedelta(hours=1)).strftime(recovery.STAMP)
        name.write_text(json.dumps(mark))
        self.assertEqual(recovery.readiness(self.record(),self.proj,self.home,self.inspect), "")

    def test_current_wait_condition_uses_machine_exit_code(self):
        from datetime import timezone
        state_path = taskstate.path(self.home,self.proj,self.TASK)
        data = json.loads(state_path.read_text())
        data["run"]["started"] = datetime.now(timezone.utc).isoformat()
        state_path.write_text(json.dumps(data))
        name = self.home / ".devkit" / "waits" / (self.TASK+".json")
        name.parent.mkdir()
        name.write_text(json.dumps({"task":self.TASK,"since":datetime.now().strftime(recovery.STAMP),
            "until":(datetime.now()+timedelta(hours=1)).strftime(recovery.STAMP),"kind":"есть","target":"/synthetic"}))
        for code in (0,1,2):
            def inspect(argv, **kw):
                if "wait" in argv:
                    return subprocess.CompletedProcess(argv,code,"машинный результат","")
                return self.inspect(argv,**kw)
            why = recovery.readiness(self.record(),self.proj,self.home,inspect)
            self.assertEqual(bool(why), code != 0)

    def test_live_original_process_group_refuses_recovery(self):
        path = taskstate.path(self.home,self.proj,self.TASK)
        data = json.loads(path.read_text())
        data["run"]["group"] = os.getpgrp()
        path.write_text(json.dumps(data))
        rec = self.record()
        rec["identity"]["group"] = os.getpgrp()
        self.assertIn("группа исходного запуска", recovery.readiness(rec,self.proj,self.home,self.inspect))

    def test_original_route_is_explicit(self):
        call = self.shell()
        self.resume(call=call)
        args = call.calls[0]
        for flag, value in (("--harness", "glm-code-2"), ("--model", "glm-5.3"), ("--recovery-run", "old-run")):
            self.assertEqual(args[args.index(flag)+1], value)
        self.assertRegex(args[args.index("--recovery-attempt")+1], r"^RV-[0-9a-f]{32}$")

    # Отказы запуска честны, попытка расходуется по факту резерва.

    def test_busy_lock_of_task_is_overtaken(self):
        lines = self.resume(call=Fake(code=3, out="замок занят"))
        joined = " ".join(lines)
        self.assertIn("замок задачи занят чужой работой", joined)
        self.assertFalse(self.record().get("outcome"))

    def test_launch_failure_names_the_reason(self):
        lines = self.resume(call=Fake(code=2, out="лестница отказала"))
        joined = " ".join(lines)
        self.assertIn("запуск не состоялся", joined)
        self.assertIn("лестница отказала", joined)
        self.assertFalse(self.record().get("outcome"))

    # Бюджет устойчив: окно, попытка на инцидент, обрыв и повреждение.

    def test_day_budget_of_three_blocks_fourth(self):
        for _ in range(recovery.DAY_TRIES):
            self.reserve()
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("исчерпан", joined)
        self.assertEqual(call.calls, [], "лимит не остановил запуск")

    def test_sliding_window_forgives_old_reserves(self):
        old = datetime.now() - (recovery.WINDOW + timedelta(hours=1))
        for _ in range(recovery.DAY_TRIES):
            self.reserve(when=old)
        lines = self.resume()
        self.assertIn("инцидент закрыт возобновлением", " ".join(lines))

    def test_unclosed_reserve_after_crash_blocks_free_retry(self):
        mark = recovery.new_mark()
        recovery._append({"kind": "reserve",
                          "when": datetime.now().strftime(recovery.STAMP),
                          "attempt": mark, "incident": self.ident,
                          "task": self.TASK,
                          "root": os.path.realpath(str(self.proj))},
                         home=self.home)
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("бесплатного повтора нет", joined)
        self.assertEqual(call.calls, [])

    def test_broken_journal_forbids_launch(self):
        path = Path(recovery.log_path(self.home))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('{"kind": "reserve"\n{"kind": "reserve", "x": 1}\n',
                        encoding="utf-8")
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("повреждён", joined)
        self.assertEqual(call.calls, [])

    def test_corrupt_incident_log_refuses_before_reserving(self):
        path = Path(self.hook.log_path(self.home))
        path.write_text(path.read_text() + "broken\n")
        call = self.shell()
        self.assertIn("журнал инцидентов повреждён", " ".join(self.resume(call=call)))
        self.assertEqual(call.calls, [])
        self.assertEqual(self.journal(), [])

    def test_torn_or_unknown_journal_forbids_launch(self):
        path = Path(recovery.log_path(self.home))
        for text in ('[]\n', '{"kind":"unknown"}\n',
                     json.dumps({"kind":"reserve","attempt":recovery.new_mark(),
                                 "root":str(self.proj),"task":self.TASK,
                                 "incident":"other","when":datetime.now().strftime(recovery.STAMP)})):
            path.write_text(text)
            call = self.shell()
            self.assertIn("повреждён", " ".join(self.resume(call=call)))
            self.assertEqual(call.calls, [])

    def test_unreadable_journal_forbids_launch(self):
        path = Path(recovery.log_path(self.home))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("", encoding="utf-8")
        path.chmod(0)
        self.addCleanup(os.chmod, str(path), 0o644)
        call = self.shell()
        lines = self.resume(call=call)
        self.assertIn("не читается", " ".join(lines))
        self.assertEqual(call.calls, [])

    def test_failed_budget_write_forbids_launch(self):
        env = recovery.LOG_ENV
        blocked = self.dir / "блокатор" / "recovery.jsonl"
        blocked.parent.mkdir()
        blocked.write_text("", encoding="utf-8")
        blocked.chmod(0o444)
        os.environ[env] = str(blocked)
        self.addCleanup(os.environ.pop, env, None)
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("сбой записи бюджета", joined)
        self.assertEqual(call.calls, [])

    def test_busy_operation_lock_spends_nothing(self):
        keeper = open(recovery.lock_path(self.home), "a", encoding="utf-8")
        self.addCleanup(keeper.close)
        fcntl.flock(keeper.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("уже идёт восстановление", joined)
        self.assertEqual(call.calls, [])
        self.assertEqual([r for r in self.journal()
                          if r.get("kind") == "reserve"], [],
                         "занятый замок потратил попытку")
        keeper.close()
        again = self.resume()
        self.assertIn("инцидент закрыт возобновлением", " ".join(again))

    def test_operation_is_exclusive_between_processes_and_roots(self):
        code = ("import recovery,sys; "
                "guard=recovery.operation(home=sys.argv[1]); "
                "taken=guard.__enter__(); print(taken,flush=True); "
                "sys.stdin.read(); guard.__exit__(None,None,None)")
        proc = subprocess.Popen([sys.executable, "-c", code, str(self.home)],
                                cwd=Path(__file__).parent, stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, text=True)
        try:
            self.assertTrue(select.select([proc.stdout], [], [], 3)[0])
            self.assertEqual(proc.stdout.readline().strip(), "True")
            call = self.shell()
            lines = self.resume(call=call)
            self.assertIn("уже идёт восстановление", " ".join(lines))
            self.assertEqual(call.calls, [])
            self.assertEqual(self.journal(), [])
        finally:
            proc.stdin.close()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
            proc.stdout.close()

    def test_process_exit_after_durable_reserve_has_no_free_retry(self):
        mark = recovery.new_mark()
        row = {"kind":"reserve", "when":datetime.now().strftime(recovery.STAMP),
               "attempt":mark, "incident":self.ident, "task":self.TASK,
               "root":os.path.realpath(self.proj)}
        code = ("import recovery,json,sys,os; "
                "guard=recovery.operation(home=sys.argv[1]); guard.__enter__(); "
                "ok=recovery._append(json.loads(sys.argv[2]),home=sys.argv[1]); "
                "os._exit(0 if ok else 2)")
        result = subprocess.run([sys.executable,"-c",code,str(self.home),json.dumps(row)],
                                cwd=Path(__file__).parent, timeout=3)
        self.assertEqual(result.returncode, 0)
        call = self.shell()
        lines = self.resume(call=call)
        self.assertIn("бесплатного повтора нет", " ".join(lines))
        self.assertEqual(call.calls, [])
        self.assertEqual(recovery.day_spent(self.home), 1)

    # Машинные запреты до резерва: владелец, человек, доска, причина.

    def test_live_owner_refuses_without_spend(self):
        lock = self.home / ".devkit" / ("task-%s.lock" % self.TASK)
        lock.mkdir(parents=True, exist_ok=True)
        (lock / "pid").write_text("%d\n" % os.getpid(), encoding="utf-8")
        (lock / "beat").write_text("", encoding="utf-8")
        call = self.shell()
        lines = self.resume(call=call)
        joined = " ".join(lines)
        self.assertIn("живой владелец", joined)
        self.assertEqual(call.calls, [])
        self.assertEqual(self.journal(), [])

    def test_unclear_owner_refuses(self):
        lock = self.home / ".devkit" / ("task-%s.lock" % self.TASK)
        lock.mkdir(parents=True, exist_ok=True)
        (lock / "pid").write_text("не число\n", encoding="utf-8")
        lines = self.resume(call=Fake())
        self.assertIn("неопределённый", " ".join(lines))

    def test_ask_flag_refuses(self):
        ask = self.proj / ".devkit" / "chat" / ("task-%s.ask" % self.TASK)
        ask.parent.mkdir(parents=True)
        ask.write_text("вопрос лежит", encoding="utf-8")
        lines = self.resume(call=Fake())
        self.assertIn("вопрос человеку", " ".join(lines))

    def test_row_outside_work_sections_refuses(self):
        self.board("## Backlog\n| %s | лежит | task | P3 | 1 | S | [x](x.md)"
                   " |\n" % self.TASK)
        lines = self.resume(call=Fake())
        self.assertIn("вне рабочих секций", " ".join(lines))

    def test_open_passes_out_incident_of_same_task_refuses(self):
        self.hook.report(self.TASK, self.hook.PASSES_OUT, "проходов нет",
                         root=str(self.proj), home=self.home)
        lines = self.resume(call=Fake())
        self.assertIn("проходы исчерпаны", " ".join(lines))

    def test_other_reason_is_not_first_stage(self):
        ident = self.hook.report(self.TASK, self.hook.IDLE_RUNS,
                                 "3 прохода вхолостую",
                                 root=str(self.proj), home=self.home)
        self.hook.settle(self.ident, self.hook.RESUMED, home=self.home)
        rec = [r for r in self.hook.read(home=self.home)
               if r.get("id") == ident][0]
        lines = recovery.resume(self.hook, rec, self.proj,
                                call=Fake(), taskctl=self.bin,
                                home=self.home, wait=self.wait)
        self.assertIn("первый этап не применяется", " ".join(lines))

    def test_closed_incident_refuses(self):
        self.hook.settle(self.ident, self.hook.DRAFT_MADE, home=self.home)
        lines = self.resume(call=Fake())
        self.assertIn("уже закрыт", " ".join(lines))

    def test_incident_of_other_root_refuses(self):
        ident = self.hook.report(self.TASK, self.hook.PROC_GONE,
                                 "чужой корень", root="/чужой-корень",
                                 home=self.home)
        rec = [r for r in self.hook.read(home=self.home)
               if r.get("id") == ident][0]
        lines = recovery.resume(self.hook, rec, self.proj,
                                call=Fake(), taskctl=self.bin,
                                home=self.home, wait=self.wait)
        self.assertIn("чужого либо пустого корня", " ".join(lines))

    # Сухой прогон не оставляет следов.

    def test_dry_run_mutates_nothing(self):
        call = self.shell()
        lines = self.resume(call=call, act=False)
        joined = " ".join(lines)
        self.assertIn("сухой прогон без запуска", joined)
        self.assertEqual(call.calls, [])
        self.assertEqual(self.journal(), [])
        self.assertFalse(os.path.exists(recovery.lock_path(self.home)))
        self.assertEqual(self.record().get("notes") or [], [])
        self.assertFalse(self.record().get("outcome"))
