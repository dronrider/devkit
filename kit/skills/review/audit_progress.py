#!/usr/bin/env python3
"""Run a saved STYLE-001 audit through a file executor and final coverage."""

import argparse
import json
import os
import signal
import tempfile
from pathlib import Path
import subprocess
import sys


def review(taskctl, executor, cwd, audit_id, request, summary):
    latest = {}
    try:
        return run_queue(taskctl, executor, cwd, audit_id, request, summary, latest)
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError, KeyError, KeyboardInterrupt) as err:
        try:
            saved = save_partial(taskctl, cwd, audit_id, request, summary, latest, err)
            print(f"Частичный итог сохранён: {saved}", file=sys.stderr)
        except (OSError, ValueError, KeyError) as save_error:
            print(f"Не удалось сохранить частичный итог: {save_error}", file=sys.stderr)
        raise


def save_partial(taskctl, cwd, audit_id, request, summary, latest, error):
    command = [taskctl, "-C", str(cwd), "review", "audit-progress", audit_id, "--json", "--summary", str(summary)]
    if request:
        command += ["--request", str(request)]
    try:
        result = subprocess.run(command, cwd=cwd, check=True, text=True, capture_output=True, timeout=30)
        latest = json.loads(result.stdout)
    except (OSError, subprocess.SubprocessError, ValueError):
        pass
    text = ["# Частичный итог ревью", "", "Ревью остановлено. Coverage не подтверждён.", "",
            f"Причина: {error or 'прерывание выполнения'}", ""]
    if latest:
        text += [f"Файлы: {latest.get('files_ready', '?')}/{latest.get('files_total', '?')}; "
                 f"цели: {latest.get('closed', '?')}/{latest.get('targets_total', '?')}; "
                 f"не заполнены: {latest.get('missing', '?')}; "
                 f"отклонены: {latest.get('rejected', '?')}; pending: {latest.get('pending', '?')}.", ""]
    audit = {}
    try:
        audit = json.loads(Path(latest['audit_path']).read_text(encoding="utf-8"))
    except (OSError, ValueError, KeyError) as err:
        text += [f"Не удалось прочитать аудит: {err}", ""]
    text += ["## Сохранённые находки и вопросы", "",
             "Ниже записи аудита; их наличие не означает прохождения проверки доказательств.", ""]
    for rule in audit.get('rules', []):
        for entry in rule.get('evidence', []):
            if entry.get('result') not in ('violated', 'pending'):
                continue
            text += [f"### {entry.get('object', rule.get('id', ''))} / {entry.get('check', '')}", "",
                     f"{entry['result']}: {entry.get('observation', '')}", "",
                     f"Источник: {entry.get('location', '')}", ""]
            for fact in entry.get('facts', {}).get('question', []):
                text += [fact.get('text', ''), ""]
    text += ["## Незакрытые проверки", "", "```json",
             json.dumps({k: latest.get(k, []) for k in ('open_files', 'issues', 'unassigned', 'inventory_issues')},
                        ensure_ascii=False, indent=2), "```", ""]
    destination = summary if not summary.exists() else summary.with_name(summary.stem + '.partial' + summary.suffix)
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=destination.parent, delete=False) as out:
            temporary = Path(out.name)
            out.write('\n'.join(text))
        os.replace(temporary, destination)
    finally:
        if temporary and temporary.exists():
            temporary.unlink()
    return destination


def run_queue(taskctl, executor, cwd, audit_id, request, summary, latest):
    command = [taskctl, "-C", str(cwd), "review"]
    request_args = ["--request", str(request)] if request else []
    context = {"id": audit_id, "project": str(cwd), "taskctl": taskctl,
               "request": str(request) if request else None}
    visited = set()
    repairs = {}
    phase_attempts = {}
    coverage_issue = ""

    while True:
        result = subprocess.run(
            [*command, "audit-progress", audit_id, "--json", "--summary", str(summary), *request_args],
            cwd=cwd, check=True, text=True, stdout=subprocess.PIPE,
        )
        progress = json.loads(result.stdout)
        latest.clear()
        latest.update(progress)
        files = progress.pop("open_files")
        next_file = next((file for file in files if file["path"] not in visited), None)
        repair = False
        accepted = []
        inventory_issues = progress.get("inventory_issues", [])
        if next_file is None and files and not inventory_issues:
            next_file = next_repair(files, repairs)
            if next_file:
                repair = True
                accepted = accepted_evidence(latest["audit_path"], next_file)

        if progress["ready_for_summary"]:
            phase = "summary"
            item = str(summary)
            work = {"summary": item, "coverage_issue": coverage_issue}
        elif next_file is not None:
            phase = "file"
            item = next_file["path"]
            work = {"file": next_file, "repair": repair}
            if repair:
                work["accepted_evidence"] = accepted
            else:
                visited.add(item)
        elif inventory_issues:
            phase = "inventory"
            item = "перечень targets"
            work = {"inventory_issues": inventory_issues}
        elif files:
            raise stalled_files(progress, files)
        else:
            phase = "inventory"
            item = "перечень целей и файлов"
            work = {}

        progress["next_file"] = next_file
        if phase != "file":
            if phase_attempts.get(phase, 0) >= 2:
                diagnostic = coverage_issue if phase == "summary" else json.dumps(progress, ensure_ascii=False)
                raise RuntimeError(f"нет прогресса после двух попыток для {item}\n{diagnostic}")
            phase_attempts[phase] = phase_attempts.get(phase, 0) + 1
        print(f"{phase}: {item}; файлы {progress['files_ready']}/{progress['files_total']}", flush=True)
        payload = {**context, **work, "phase": phase, "progress": progress}
        subprocess.run(executor, cwd=cwd, input=json.dumps(payload, ensure_ascii=False) + "\n",
                       check=True, text=True)

        if phase == "summary":
            coverage = subprocess.run(
                [*command, "audit-check", audit_id, "--coverage", str(summary), *request_args],
                cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            )
            if coverage.returncode == 0:
                print(coverage.stdout.strip())
                return
            coverage_issue = coverage.stdout.strip() or f"audit-check завершился с кодом {coverage.returncode}"
            print(coverage_issue, file=sys.stderr)


def next_repair(files, attempts):
    candidates = []
    for file in files:
        path = file["path"]
        for goal in file.get("goals", ()):
            checks = repair_state(goal)
            if checks:
                candidates.append(((path, goal["object"]), checks, {"path": path, "goals": [goal]}))
        if file.get("issues"):
            candidates.append(((path, None), {"inventory": 1}, {"path": path, "issues": file["issues"]}))
    for untried in (True, False):
        for key, checks, job in candidates:
            previous = attempts.get(key)
            if (untried and previous is None) or (not untried and previous is not None and repair_advanced(previous, checks)):
                attempts[key] = checks
                return job
    return None


def repair_state(goal):
    state = {check: 1 for check in goal.get("rejected_checks", ())}
    for check in goal.get("missing_checks", ()):
        state[check] = 0
    for check in state:
        blockers = [b for b in goal.get("blockers", ()) if b.get("check") == check]
        if state[check] == 1 and any(b.get("stage") == "conclusion" for b in blockers):
            state[check] = 2
    return state


def repair_advanced(previous, current):
    checks = previous.keys() | current.keys()
    return (all(current.get(c, 3) >= previous.get(c, 3) for c in checks) and
            any(current.get(c, 3) > previous.get(c, 3) for c in checks))


def open_checks(goal):
    return frozenset((*goal.get("missing_checks", ()), *goal.get("rejected_checks", ())))


def accepted_evidence(audit_path, file):
    if not file.get("goals"):
        return []
    goal = file["goals"][0]
    audit = json.loads(Path(audit_path).read_text(encoding="utf-8"))
    rule = next((r for r in audit["rules"] if r["id"].endswith(":STYLE-001")), {})
    return [e for e in rule.get("evidence", ()) if
            e.get("object") == goal["object"] and e.get("check") not in open_checks(goal)]


def stalled_files(progress, files):
    diagnostic = {"open_files": files, "issues": progress.get("issues", []),
                  "unassigned": progress.get("unassigned", [])}
    return RuntimeError("нет прогресса по незакрытым файлам\n" +
                        json.dumps(diagnostic, ensure_ascii=False, indent=2))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("id", metavar="ID")
    parser.add_argument("-C", type=Path, default=Path.cwd(), help="project directory")
    parser.add_argument("--taskctl", default="taskctl")
    parser.add_argument("--request", type=Path)
    parser.add_argument("--summary", type=Path, required=True)
    parser.add_argument("--executor", nargs=argparse.REMAINDER, required=True,
                        help="command and arguments, last option; receives a JSON job on stdin")
    args = parser.parse_args(argv)
    if not args.executor:
        parser.error("--executor requires a command")
    taskctl = str(Path(args.taskctl).resolve()) if "/" in args.taskctl else args.taskctl
    try:
        review(taskctl, args.executor, args.C.resolve(), args.id,
               args.request.resolve() if args.request else None, args.summary.resolve())
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError, KeyError, KeyboardInterrupt) as err:
        print(f"Ошибка обхода ревью: {err}", file=sys.stderr)
        return 1
    return 0


def interrupted(signum, frame):
    raise KeyboardInterrupt(f"сигнал {signum}")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupted)
    sys.exit(main())
