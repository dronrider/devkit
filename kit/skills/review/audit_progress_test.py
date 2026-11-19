"""Exercise the review loop with real taskctl and a one-job executor."""

import json
import copy
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("audit_progress.py")
SPEC = importlib.util.spec_from_file_location("audit_progress", SCRIPT)
LOOP = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LOOP)
TASKCTL = os.environ.get("DEVKIT_TEST_TASKCTL")
EXECUTOR = """import json, pathlib, sys
job = json.load(sys.stdin)
phase = job["phase"]
state = job["progress"]
summary = pathlib.Path("review.md")
with open(sys.argv[1], "a") as log:
    print(json.dumps(job), file=log)
if sys.argv[2] == "crash":
    sys.exit(12)
if sys.argv[2] == "interrupt":
    import signal, os
    os.kill(os.getppid(), signal.SIGTERM)
    sys.exit(0)
if sys.argv[2] == "stall":
    sys.exit(0)
path = pathlib.Path(state["audit_path"])
audit = json.loads(path.read_text())
rule = next(r for r in audit["rules"] if r["id"].endswith(":STYLE-001"))
if phase == "file":
    assert not summary.exists()
    rule["applicability"] = "applicable"
    goals = [goal["object"] for goal in job["file"].get("goals", [])]
    for obj in goals:
        target = next(t for t in rule["targets"] if t["object"] == obj)
        symbol, loc = target["symbol"], target["location"]
        checks = ("meaning", "result", "writes")
        if sys.argv[2] == "one_goal" and job["file"]["path"] == "alpha.go":
            if job["repair"] and symbol == "alpha":
                continue
            checks = ("result", "writes") if job["repair"] else ("meaning",)
        for check in checks:
            entry = dict(object=obj, check=check, location=loc, observation="Проверено по телу.")
            if symbol == "createInvoice":
                jobs = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
                repairs = sum(j.get("repair", False) and j.get("file", {}).get("path") == "alpha.go" for j in jobs)
                verdict = "violated" if repairs >= 2 else "checked"
                entry.update(result=verdict, heading="## Name")
                if check == "meaning":
                    entry.update(symbol=symbol, word_checks=[
                        dict(word=w, meaning="обещание имени", fact="возвращаются архивы", location=loc, result=verdict)
                        for w in ("create", "Invoice")])
                elif check == "result":
                    entry.update(object_comparison=dict(expected=["Invoice"] if repairs else [],
                        actual="archives", actual_at="alpha.go:5", proof=dict(expression="archives", type="[]Archive", words=[
                        dict(word="Invoice", role="object", fact=dict(location="alpha.go:4", quote='archives := []Archive{{Invoice: "invoice"}}',
                            value="archives", text="Архив содержит счёт."))])),
                        result_evidence=dict(word="Invoice", returned="archives", return_at="alpha.go:5", consumer="caller", consumer_at=loc))
                else:
                    entry.update(result="not_applicable", observation="Изменений внешних данных нет.")
            elif check == "meaning":
                entry.update(result="checked", symbol=symbol, word_checks=[
                    dict(word=symbol, meaning="имя операции", fact="объявление операции", location=loc, result="checked")])
                if symbol == "alpha" and (sys.argv[2] == "changing_rejection" or
                        (sys.argv[2] == "damage_then_repair" and not job["repair"]) or
                        (sys.argv[2] == "reject_then_repair" and not job["repair"])):
                    entry["word_checks"] = []
                    if sys.argv[2] == "changing_rejection":
                        attempt = len(pathlib.Path(sys.argv[1]).read_text().splitlines())
                        entry["word_checks"] = [dict(word=f"wrong{attempt}")]
            else:
                entry["result"] = "not_applicable"
            evidence = rule.setdefault("evidence", [])
            evidence[:] = [e for e in evidence if (e["object"], e["check"]) != (obj, check)]
            evidence.append(entry)
    if sys.argv[2] in ("damage_then_repair", "damage_stall") and job["file"]["path"] == "alpha.go" and not job["repair"]:
        rule["targets"].append(dict(object="alpha.go:1#Extra", symbol="Extra", location="alpha.go:1"))
    rule["result"] = "checked" if len(rule["evidence"]) == len(rule["targets"]) * 3 else "pending"
    if any(e["result"] == "violated" for e in rule["evidence"]):
        rule.update(result="violated", reason="Имя обещает счёт, возвращаются архивы.")
    path.write_text(json.dumps(audit))
elif phase == "inventory":
    assert job["inventory_issues"][0]["code"] == "targets_modified"
    assert state["ready_for_summary"] is False
    if sys.argv[2] != "damage_stall":
        import subprocess
        command = [job["taskctl"], "review", "audit-repair", job["id"], "--targets", "--request", job["request"]]
        subprocess.run(command, check=True)
        restored = json.loads(path.read_text())
        repaired = next(r for r in restored["rules"] if r["id"].endswith(":STYLE-001"))
        assert repaired["evidence"] == rule["evidence"]
        repaired["result"] = "checked"
        path.write_text(json.dumps(restored))
elif phase == "summary":
    assert state["ready_for_summary"] and state["files_ready"] == 3 and state["closed"] == 3
    if job["coverage_issue"]:
        audit["architecture"] = dict(result="pending", reason="Только STYLE-001.", heading="## Scope")
        for other in audit["rules"]:
            if other["id"] != rule["id"]:
                other["reason"] = "Только STYLE-001."
                other["evidence"] = [dict(result="pending", heading="## Scope", observation="Только STYLE-001.")]
        path.write_text(json.dumps(audit))
    summary.write_text("## Scope\\n\\nТолько STYLE-001.\\n" +
        ("## Name\\n\\nalpha.go:3 createInvoice возвращает архивы, имя обещает счёт.\\n" if sys.argv[2] == "conclusion" else ""))
else:
    raise AssertionError(job)
"""


class RepairQueueTest(unittest.TestCase):
    def goal(self, obj="createInvoice", stage="evidence", check="result"):
        return {"object": obj, "rejected_checks": [check], "blockers": [
            {"check": check, "stage": stage, "code": "result_object", "message": "diagnostic"}]}

    def test_new_contradiction_gets_a_turn_after_other_goals(self):
        first = self.goal()
        second = self.goal("addAttributes", check="writes")
        files = [{"path": "a.go", "goals": [first, second]}]
        attempts = {}
        self.assertEqual(LOOP.next_repair(files, attempts)["goals"], [first])
        first["blockers"][0]["stage"] = "conclusion"
        self.assertEqual(LOOP.next_repair(files, attempts)["goals"], [second])
        self.assertEqual(LOOP.next_repair(files, attempts)["goals"], [first])
        first["blockers"][0].update(message="another diagnostic", code="another_code")
        self.assertIsNone(LOOP.next_repair(files, attempts))

    def test_fill_then_evidence_then_conclusion_then_close(self):
        goal = {"object": "createInvoice", "missing_checks": ["result"]}
        files = [{"path": "a.go", "goals": [goal]}]
        attempts = {}
        self.assertIsNotNone(LOOP.next_repair(files, attempts))
        goal.clear()
        goal.update(self.goal())
        self.assertIsNotNone(LOOP.next_repair(files, attempts))
        goal["blockers"][0]["stage"] = "conclusion"
        self.assertIsNotNone(LOOP.next_repair(files, attempts))
        goal["rejected_checks"] = []
        goal["blockers"] = []
        self.assertIsNone(LOOP.next_repair(files, attempts))

    def test_messages_and_regressions_do_not_extend_queue(self):
        first = self.goal()
        first["rejected_checks"].append("writes")
        files = [{"path": "a.go", "goals": [first]}]
        attempts = {}
        LOOP.next_repair(files, attempts)
        first["blockers"][0]["message"] = "new wording"
        self.assertIsNone(LOOP.next_repair(files, attempts))
        first["blockers"][0]["stage"] = "conclusion"
        first["missing_checks"] = ["meaning"]
        self.assertIsNone(LOOP.next_repair(files, attempts))
        first.pop("missing_checks")
        self.assertIsNotNone(LOOP.next_repair(files, attempts))
        saved = copy.deepcopy(first)
        first["blockers"][0]["stage"] = "evidence"
        self.assertIsNone(LOOP.next_repair(files, attempts))
        first.update(saved)
        self.assertIsNone(LOOP.next_repair(files, attempts))

    def test_contradiction_and_remaining_evidence_get_one_turn_together(self):
        goal = self.goal()
        files = [{"path": "a.go", "goals": [goal]}]
        attempts = {}
        LOOP.next_repair(files, attempts)
        goal["blockers"].append({"check": "result", "stage": "conclusion", "code": "result_object"})
        job = LOOP.next_repair(files, attempts)
        self.assertEqual(job["goals"][0]["blockers"], goal["blockers"])
        self.assertIsNone(LOOP.next_repair(files, attempts))


@unittest.skipUnless(TASKCTL, "set DEVKIT_TEST_TASKCTL to run the CLI integration tests")
class AuditProgressTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        directory = Path(temp.name)
        self.project = directory / "project"
        self.project.mkdir()
        standards = directory / "standards"
        standards.mkdir()
        (standards / "RULES.architecture.md").write_text("- **ARCH-001.** Scope.\n", encoding="utf-8")
        (standards / "RULES.style.md").write_text("- **STYLE-001.** Name reflects purpose.\n", encoding="utf-8")
        (standards / "RULES.md").write_text("## Тесты обязательны\n\n- **TEST-001.** Scope.\n", encoding="utf-8")
        self.env = {**os.environ, "DEVKIT_HOME": str(standards), "GIT_CONFIG_GLOBAL": os.devnull,
                    "GIT_CONFIG_NOSYSTEM": "1"}
        marker = standards / "kit/skills/board-task/task-run.py"
        marker.parent.mkdir(parents=True)
        marker.touch()
        self.request = directory / "REQUEST.md"
        self.request.write_text("# Task\n\nReview names.\n", encoding="utf-8")
        self.executor = directory / "executor.py"
        self.executor.write_text(EXECUTOR, encoding="utf-8")
        self.log = directory / "jobs.jsonl"
        self.taskctl = Path(shutil.which(TASKCTL)).resolve()
        self.run_cmd("git", "init", "-q")
        self.run_cmd("git", "-c", "user.name=Test", "-c", "user.email=test@example.com",
                     "commit", "--allow-empty", "-qm", "base")
        for name in ("alpha", "beta", "gamma"):
            (self.project / (name + ".go")).write_text(f"package review\nfunc {name}() {{}}\n", encoding="utf-8")
        self.run_cmd(self.taskctl, "review", "audit", "XR-1", "--base", "HEAD", "--request", self.request)

    def run_cmd(self, *command, check=True):
        result = subprocess.run(command, cwd=self.project, env=self.env, text=True, capture_output=True, timeout=15)
        if check:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def run_loop(self, mode):
        result = self.run_cmd(sys.executable, SCRIPT, "XR-1", "--taskctl", self.taskctl,
                              "--request", self.request, "--summary", "review.md",
                              "--executor", sys.executable, self.executor, self.log, mode, check=False)
        jobs = [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()]
        return result, jobs

    def test_next_file_and_summary_after_queue_is_closed(self):
        result, jobs = self.run_loop("success")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual([job["phase"] for job in jobs], ["file", "file", "file", "summary", "summary"])
        self.assertEqual([job["file"]["path"] for job in jobs[:3]], ["alpha.go", "beta.go", "gamma.go"])
        self.assertEqual([job["progress"]["files_ready"] for job in jobs], [0, 1, 2, 3, 3])
        self.assertEqual(jobs[0]["file"]["goals"][0]["missing_checks"], ["meaning", "result", "writes"])
        self.assertTrue(all(job["progress"]["ready_for_summary"] for job in jobs[3:]))
        self.assertIn("архитектурная оценка", jobs[4]["coverage_issue"])
        self.assertIn("ссылки на итог сверены", result.stdout)

    def test_rejected_goal_is_repaired_after_other_files(self):
        result, jobs = self.run_loop("reject_then_repair")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual([j.get("file", {}).get("path") for j in jobs[:4]],
                         ["alpha.go", "beta.go", "gamma.go", "alpha.go"])
        self.assertTrue(all(j["phase"] == "file" and not j["repair"] for j in jobs[:3]))
        self.assertEqual(jobs[1]["progress"]["rejected"], 1)
        self.assertTrue(jobs[3]["repair"])
        self.assertEqual(jobs[3]["progress"]["files_ready"], 2)
        self.assertEqual(len(jobs[3]["file"]["goals"]), 1)
        self.assertIn("пропущены слова alpha", jobs[3]["file"]["goals"][0]["issues"][0])
        self.assertEqual(jobs[4]["phase"], "summary")
        self.assertIn("ссылки на итог сверены", result.stdout)

    def test_targets_damage_defers_inventory_until_other_files(self):
        result, jobs = self.run_loop("damage_then_repair")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual([j["phase"] for j in jobs],
                         ["file", "file", "file", "inventory", "file", "summary", "summary"])
        self.assertEqual([j["file"]["path"] for j in jobs[:3]], ["alpha.go", "beta.go", "gamma.go"])
        self.assertEqual(jobs[1]["progress"]["targets_total"], 3)
        self.assertEqual(jobs[3]["inventory_issues"][0]["added"][0]["object"], "alpha.go:1#Extra")
        self.assertEqual(jobs[4]["file"]["path"], "alpha.go")
        self.assertTrue(jobs[4]["repair"])
        self.assertTrue(jobs[5]["progress"]["ready_for_summary"])

    def test_unfixed_targets_save_partial_after_other_files(self):
        result, jobs = self.run_loop("damage_stall")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual([j["phase"] for j in jobs], ["file", "file", "file", "inventory", "inventory"])
        self.assertEqual([j["file"]["path"] for j in jobs[:3]], ["alpha.go", "beta.go", "gamma.go"])
        partial = (self.project / "review.md").read_text()
        self.assertIn("targets_modified", partial)
        self.assertIn("Coverage не подтверждён", partial)

    def test_evidence_repair_reaches_conclusion_before_summary(self):
        (self.project / "alpha.go").write_text('package review\ntype Archive struct { Invoice string }\nfunc createInvoice() []Archive {\n archives := []Archive{{Invoice: "invoice"}}\n return archives\n}\n')
        self.run_cmd(self.taskctl, "review", "audit", "XR-1", "--base", "HEAD", "--refresh", "--request", self.request)
        result, jobs = self.run_loop("conclusion")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        repairs = [j for j in jobs if j.get("repair")]
        self.assertEqual(len(repairs), 2)
        self.assertEqual([j["file"]["goals"][0]["rejected_checks"] for j in repairs], [["result"], ["result"]])
        self.assertEqual([j["file"]["goals"][0]["blockers"][0]["stage"] for j in repairs], ["evidence", "conclusion"])
        audit = json.loads(Path(jobs[-1]["progress"]["audit_path"]).read_text())
        rule = next(r for r in audit["rules"] if r["id"].endswith(":STYLE-001"))
        entries = {e["check"]: e for e in rule["evidence"] if e["object"].endswith("#createInvoice")}
        self.assertEqual(entries["meaning"]["result"], "violated")
        self.assertEqual(entries["result"]["result"], "violated")
        self.assertEqual(entries["result"]["object_comparison"]["expected"], ["Invoice"])
        self.assertIn("createInvoice", (self.project / "review.md").read_text())

    def test_changing_rejection_does_not_restart_attempts(self):
        result, jobs = self.run_loop("changing_rejection")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual([j["file"]["path"] for j in jobs],
                         ["alpha.go", "beta.go", "gamma.go", "alpha.go"])
        self.assertTrue(all(j["repair"] for j in jobs[3:]))
        self.assertEqual(jobs[3]["file"]["goals"][0]["rejected_checks"], ["meaning"])
        self.assertIn("wrong1", jobs[3]["file"]["goals"][0]["issues"][0])
        self.assertIn("alpha.go", result.stderr)
        self.assertIn("wrong4", result.stderr)
        self.assertIn("Ревью остановлено", (self.project / "review.md").read_text())

    def test_repairs_issue_one_goal_and_skip_stalled_goal(self):
        (self.project / "alpha.go").write_text("package review\nfunc alpha() {}\nfunc delta() {}\n")
        self.run_cmd(self.taskctl, "review", "audit", "XR-1", "--base", "HEAD", "--refresh",
                     "--request", self.request)
        result, jobs = self.run_loop("one_goal")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        repairs = [j for j in jobs if j.get("repair")]
        self.assertEqual(len(repairs), 2)
        self.assertTrue(all(len(j["file"]["goals"]) == 1 for j in repairs))
        self.assertEqual([j["file"]["goals"][0]["object"].split("#")[-1] for j in repairs], ["alpha", "delta"])
        self.assertEqual([[e["check"] for e in j["accepted_evidence"]] for j in repairs], [["meaning"], ["meaning"]])
        summary = (self.project / "review.md").read_text()
        self.assertIn("alpha.go:2#alpha", summary)
        self.assertNotIn("alpha.go:3#delta", summary)

    def test_crash_saves_partial_and_preserves_existing_summary(self):
        (self.project / "review.md").write_text("Предыдущий итог")
        result, jobs = self.run_loop("crash")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(jobs), 1)
        self.assertEqual((self.project / "review.md").read_text(), "Предыдущий итог")
        self.assertIn("Coverage не подтверждён", (self.project / "review.partial.md").read_text())

    def test_signal_saves_partial(self):
        result, jobs = self.run_loop("interrupt")
        self.assertEqual(result.returncode, 1)
        self.assertIn("Ревью остановлено", (self.project / "review.md").read_text())

    def test_stalled_executor_does_not_start_summary(self):
        result, jobs = self.run_loop("stall")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertTrue(all(job["phase"] == "file" for job in jobs))
        self.assertEqual([j["file"]["path"] for j in jobs], ["alpha.go", "beta.go", "gamma.go"] * 2)
        self.assertIn("нет прогресса", result.stderr)
        self.assertIn("Ревью остановлено", (self.project / "review.md").read_text())


if __name__ == "__main__":
    unittest.main()
