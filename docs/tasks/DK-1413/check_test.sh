#!/bin/sh
set -eu
python3 - "$0" "${1-}" <<'PYTEST'
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

SOURCE = Path(sys.argv[1]).resolve().parent
ROOT = SOURCE.parents[2]
PREFIX = "docs/tasks/DK-1413/"


def require(condition, message):
    if not condition:
        raise SystemExit(message)


with tempfile.TemporaryDirectory(prefix="dk1413-provenance-") as work:
    work = Path(work)
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("GIT_")}
    (work / "home").mkdir()
    env.update(HOME=str(work / "home"), GIT_CONFIG_NOSYSTEM="1",
               GIT_CONFIG_GLOBAL="/dev/null")
    repo = work / "repo"
    subprocess.run(["git", "clone", "--quiet", "--no-checkout", "--no-hardlinks",
                    str(ROOT), str(repo)], env=env, check=True)
    require(not (repo / ".git/objects/info/alternates").exists(), "копия зависит от исходного хранилища")
    here = repo / PREFIX
    shutil.copytree(SOURCE, here)
    checker = (SOURCE / "check.sh").read_bytes()
    if sys.argv[2]:
        checker = subprocess.check_output(["git", "-C", str(ROOT), "show",
                                          sys.argv[2] + ":" + PREFIX + "check.sh"], env=env)
    (here / "check.sh").write_bytes(checker)
    protocol = json.loads((here / "runs.json").read_text())
    ancestor = subprocess.check_output(["git", "-C", str(repo), "merge-base",
                                       protocol["frozen_commit"], "HEAD"], text=True, env=env).strip()
    require(ancestor != protocol["frozen_commit"], "стенд не воспроизводит rebase")

    def run():
        return subprocess.run(["sh", str(here / "check.sh")], cwd=repo, env=env,
                              text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    result = run()
    require(result.returncode == 0, "после rebase: ожидался успех\n" + result.stdout)
    print("PASS: rebase, отдельная копия git без alternates и hardlinks")

    def rejected(label, changes, expected):
        previous = {name: (here / name).read_bytes() for name in changes}
        try:
            for name, body in changes.items():
                (here / name).write_bytes(body)
            result = run()
            require(result.returncode != 0 and expected in result.stdout,
                    label + ": подмена не обнаружена ожидаемой проверкой\n" + result.stdout)
            print("PASS: " + label)
        finally:
            for name, body in previous.items():
                (here / name).write_bytes(body)

    def encoded(value):
        return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()

    original = (here / "bug.original.md").read_bytes() + b"\nchanged\n"
    rejected("подмена исходной постановки", {"bug.original.md": original}, "снимком bug.original.md")
    frozen = json.loads((here / "frozen.json").read_text())
    frozen["files"]["bug.original.md"] = hashlib.sha256(original).hexdigest()
    rejected("подмена исходника вместе с его контрольной суммой",
             {"bug.original.md": original, "frozen.json": encoded(frozen)}, "снимком frozen.json")
    rejected("подмена подготовленной копии",
             {"bug.prepared.md": (here / "bug.prepared.md").read_bytes() + b"changed\n"},
             "снимком bug.prepared.md")
    response_path = protocol["runs"][0]["response"]
    response = json.loads((here / response_path).read_text())
    response["response"] += " changed"
    response = encoded(response)
    rejected("подмена raw-ответа", {response_path: response}, "снимком " + response_path)
    changed = json.loads((here / "runs.json").read_text())
    changed["runs"][0]["response_sha256"] = hashlib.sha256(response).hexdigest()
    rejected("подмена ответа вместе с протоколом",
             {response_path: response, "runs.json": encoded(changed)}, "снимком runs.json")
    changed = json.loads((here / "runs.json").read_text())
    changed["frozen_commit"], changed["inputs_commit"] = changed["inputs_commit"], changed["frozen_commit"]
    rejected("перестановка frozen и inputs", {"runs.json": encoded(changed)}, "порядок исходных снимков")
    before_inputs = subprocess.check_output(["git", "-C", str(repo), "rev-parse",
                                            protocol["frozen_commit"] + "^"], env=env).strip()
    wrong_stage = checker.replace(b'ANSWERS_COMMIT = "60515a7979f923f0c406f987008be8631f592763"',
                                  b'ANSWERS_COMMIT = "' + before_inputs + b'"')
    require(wrong_stage != checker, "не найден якорь этапа ответов")
    rejected("этап ответов раньше входов", {"check.sh": wrong_stage}, "порядок исходных снимков")
    print("Все проверки происхождения: 8/8")
PYTEST
