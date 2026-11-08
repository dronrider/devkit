"""Изолированная проверка установленного восстановления без внешней модели."""
import contextlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time

import duty
import lift
import recovery
import taskstate

TASK = "RV-901"
MODEL = "glm-5.3"
HARNESS = "glm-code"

CLIENT = '''#!/usr/bin/env python3
import json, os, pathlib, sys, time
args = sys.argv[1:]
if "--session-id" in args:
    sid = args[args.index("--session-id") + 1]
    folder = pathlib.Path.home() / ".devkit" / "agents"
    folder.mkdir(parents=True, exist_ok=True)
    (folder / (sid+".json")).write_text(json.dumps({"agents":{"synthetic":{
        "state":"running", "job":"shell", "command":"synthetic local operation"}}}))
time.sleep(30)
'''


def fingerprint(root):
    return {str(path.relative_to(root)): path.read_bytes()
            for path in root.rglob("*") if path.is_file()}


def wait_for(check, seconds=8):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = check()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError("срок ожидания стенда истёк")


def clean_workers(home, groups):
    try:
        pid = int((home / ".devkit" / ("task-%s.lock" % TASK) / "pid").read_text().strip())
        if pid > 0:
            groups.add(pid)
    except (OSError, ValueError):
        pass
    for pid in list(groups):
        try:
            if pid != os.getpid() and os.getpgid(pid) == pid:
                os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    groups.clear()


def main():
    for name in ("taskctl", "agentctl"):
        if not shutil.which(name):
            print("recovery-check: нет %s в PATH" % name, file=sys.stderr)
            return 1
    devkit = Path(__file__).resolve().parents[2]
    saved = dict(os.environ)
    groups = set()
    try:
        with tempfile.TemporaryDirectory(prefix="devkit-recovery-check-") as temp, contextlib.ExitStack() as cleanup:
            area = Path(temp)
            home, root, binary = area / "home", area / "project", area / "bin"
            cleanup.callback(clean_workers, home, groups)
            for path in (home / ".devkit" / "quota", root / "docs" / "tasks", binary, home / ".devkit" / "claude-stand"):
                path.mkdir(parents=True)
            (root / ".devkit").mkdir()
            (root / ".devkit" / "devkit").symlink_to(devkit, target_is_directory=True)
            (root / "docs" / "TASKS.md").write_text(
                "# Стенд: доска (префикс RV)\n\n## In progress\n\n"
                "| ID | Задача | Тип | P | R | Цена | Ссылка |\n"
                "|----|--------|-----|---|---|------|--------|\n"
                "| RV-901 | Синтетическая работа | task | P3 | 1 (0+1+0+0+0) | S | [tasks/RV-901.md](tasks/RV-901.md) |\n"
                "\n## Check\n\n## Backlog\n\n## Blocked\n")
            (root / "docs" / "tasks" / (TASK+".md")).write_text("# RV-901: синтетическая работа\n")
            (home / ".devkit" / "harness.local").write_text(
                'default = "glm-code"\nenabled = ["glm-code"]\nauto = ["glm-code"]\n\n[glm-code]\nhome = "~/.devkit/claude-stand"\nenv = ["CLAUDE_CONFIG_DIR={home}"]\nmini = "glm-5.3-flash"\nbase = "glm-5.3"\npro = "glm-5.3"\nmax = "glm-5.3"\n')
            import devkitctl
            settings = home / ".devkit" / "claude-stand" / "settings.json"
            gaps, _, stale, moved = devkitctl.hook_gaps("", settings)
            devkitctl.install_hooks(settings, gaps, devkit, stale, moved)
            quota_path = home / ".devkit" / "quota" / "glm-code.local"
            quota = "taken = %s\nwindow5h_all = 10%% сброс 2099-01-01T00:00\nweek_all = 10%% сброс 2099-01-01T00:00\n" % time.strftime("%Y-%m-%dT%H:%M")
            quota_path.write_text(quota)
            (binary / "claude").write_text(CLIENT)
            (binary / "claude").chmod(0o700)
            for name in ("taskctl", "agentctl", "git", "ps", "sh", "env"):
                source = shutil.which(name)
                if not source:
                    raise RuntimeError("для стенда нет команды " + name)
                (binary / name).symlink_to(source)
            (binary / "python3").symlink_to(sys.executable)
            env = {}
            env.update(HOME=str(home), PATH=str(binary),
                       DEVKIT_HOME=str(devkit), DEVKIT_RUN_ADOPT_WAIT="5")
            os.environ.clear()
            os.environ.update(env)
            compatibility = subprocess.run(["agentctl", "-C", str(root), "quota", "--json", "--harness", HARNESS],
                                           capture_output=True, text=True, timeout=5)
            if compatibility.returncode or json.loads(compatibility.stdout).get("schema") != "agentctl/quota":
                raise RuntimeError("agentctl не поддерживает машинный ответ квоты; обновите весь комплект devkit")
            launched = subprocess.run(
                ["taskctl", "-C", str(root), "run", TASK, "--harness", HARNESS,
                 "--model", MODEL, "--hidden", "--order", "Синтетическая локальная проверка"],
                capture_output=True, text=True, timeout=10)
            if launched.returncode:
                raise RuntimeError("штатный запуск стенда отказал: " + (launched.stderr or launched.stdout)[-1500:])

            def owner():
                try:
                    data = taskstate.read(home, root, TASK)
                except FileNotFoundError:
                    return None
                return data["run"] if data.get("run", {}).get("pid") else None
            source = wait_for(owner)
            pid = source["pid"]
            if os.getpgid(pid) != pid:
                raise RuntimeError("синтетический конвейер не получил отдельную группу")
            groups.add(pid)
            os.killpg(pid, signal.SIGKILL)
            wait_for(lambda: lift.dead_pid(pid) and recovery.group_gone(source.get("group")))
            groups.discard(pid)
            _, halted = lift.incident_rows(
                [{"id":TASK,"title":"Синтетическая работа","_section":"in-progress"}],
                root=str(root), home=str(home / ".devkit"))
            hook = duty.incidents_mod()
            records = [rec for rec in hook.read(home=home)
                       if rec.get("task") == TASK and not rec.get("outcome")]
            if TASK not in halted or len(records) != 1 or records[0].get("identity", {}).get("id") != source["id"]:
                raise RuntimeError("исчезновение штатного запуска не связано с исходным ID")
            rec = records[0]
            before = fingerprint(area)
            dry = recovery.resume(hook, rec, root, home=home, act=False)
            if fingerprint(area) != before or not any("запуск восстановления разрешён" in line for line in dry):
                raise RuntimeError("сухой прогон изменил файлы либо не подтвердил возможность: " + " ".join(dry))
            print("OK: сухой прогон не изменил файлы стенда")
            quota_path.write_text(quota.replace("10%", "100%"))
            refusal = recovery.resume(hook, rec, root, home=home)
            if Path(recovery.log_path(home)).exists() or not any("квота" in line for line in refusal):
                raise RuntimeError("исчерпанная квота не запретила запуск: " + " ".join(refusal))
            quota_path.write_text(quota)
            state_path = taskstate.path(home, root, TASK)
            state = taskstate.read(home, root, TASK)
            state["stopped"] = True
            state_path.write_text(json.dumps(state))
            refusal = recovery.resume(hook, rec, root, home=home)
            if Path(recovery.log_path(home)).exists() or not any("остановлена пользователем" in line for line in refusal):
                raise RuntimeError("ручная остановка не запретила запуск")
            state["stopped"] = False
            state_path.write_text(json.dumps(state))
            print("OK: квота и ручная остановка запретили запуск без расхода попытки")
            result = recovery.resume(hook, rec, root, home=home, wait=8)
            current = taskstate.read(home, root, TASK).get("run") or {}
            if current.get("pid"):
                groups.add(current["pid"])
            after = [item for item in hook.read(home=home) if item.get("id") == rec["id"]][0]
            if after.get("outcome") != hook.RESUMED:
                log = home / ".devkit" / "heads" / (TASK+".log")
                raise RuntimeError("новый рабочий ход не подтверждён: " + " ".join(result) + "\n" + (log.read_text()[-1800:] if log.exists() else ""))
            if current.get("id") == source["id"] or current.get("harness") != HARNESS or current.get("model") != MODEL:
                raise RuntimeError("новый запуск не сохранил исходный маршрут")
            rows = recovery._read(home)
            reserves = [row for row in rows if row.get("kind") == "reserve"]
            if len(reserves) != 1 or not recovery.proof_for(reserves[0]["attempt"], str(root.resolve()), TASK, home):
                raise RuntimeError("нет единственного устойчивого резерва либо связанного доказательства")
            recovery.resume(hook, rec, root, home=home, wait=0.1)
            if len(recovery._read(home)) != len(rows):
                raise RuntimeError("повтор закрытого инцидента изменил бюджет")
            print("OK: штатный конвейер восстановлен по новому рабочему действию, маршрут сохранён")
            print("OK: одна попытка сохранена, повтор не запущен")
            for pid in list(groups):
                try:
                    if os.getpgid(pid) == pid:
                        os.killpg(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            groups.clear()
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        print("recovery-check: %s" % error, file=sys.stderr)
        return 1
    finally:
        for pid in groups:
            try:
                if os.getpgid(pid) == pid:
                    os.killpg(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        os.environ.clear()
        os.environ.update(saved)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
