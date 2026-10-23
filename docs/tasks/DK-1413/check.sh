#!/bin/sh
set -eu
python3 - "$0" <<'PY'
"""Одноразовая проверка материалов пробы DK-1413."""

import hashlib
import json
from pathlib import Path
import re
import sys
import subprocess


HERE = Path(sys.argv[1]).resolve().parent
ROOT = HERE.parents[2]
ANSWERS_COMMIT = "60515a7979f923f0c406f987008be8631f592763"
ACCESS_COMMIT = "34c242d64e11ab7f76adfb8fe8209dc641165044"


def git(*args):
    return subprocess.check_output(["git", "--no-replace-objects", "-C", str(ROOT), *args], text=True)


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require(condition, message):
    if not condition:
        raise SystemExit("ошибка: " + message)


def from_snapshot(name, commit):
    path = "docs/tasks/DK-1413/" + name
    expected = git("rev-parse", commit + ":" + path).strip()
    actual = git("hash-object", "--no-filters", str(HERE / name)).strip()
    require(actual == expected, "файл не совпал со снимком " + name)


protocol = json.loads((HERE / "runs.json").read_text())
frozen_commit = protocol["frozen_commit"]
inputs_commit = protocol["inputs_commit"]
commits = [frozen_commit, inputs_commit, ANSWERS_COMMIT, ACCESS_COMMIT]
require(len(set(commits)) == 4, "этапы должны иметь разные снимки")
for commit in commits:
    require(re.fullmatch(r"[0-9a-f]{40}", commit), "некорректный ID снимка")
    require(git("cat-file", "-t", commit).strip() == "commit", "недоступен исходный коммит " + commit)
for before, after in zip(commits, commits[1:]):
    require(git("merge-base", before, after).strip() == before, "порядок исходных снимков")
from_snapshot("runs.json", ANSWERS_COMMIT)
from_snapshot("frozen.json", frozen_commit)
frozen = json.loads((HERE / "frozen.json").read_text())
for name in frozen["files"]:
    from_snapshot(name, frozen_commit)
for case in ["bug", "feature", "multi"]:
    from_snapshot(case + ".prepared.md", inputs_commit)
for run in protocol["runs"]:
    stage = ANSWERS_COMMIT if run["name"] == "bug.prepared.v2" else inputs_commit
    from_snapshot(run["input"], stage)
    from_snapshot(run["response"], ANSWERS_COMMIT)
for name in ["bug.prepared.v2.md", "bug.prepared.v2.input.txt"]:
    from_snapshot(name, ANSWERS_COMMIT)
for name in ["bug.original.access.json", "bug.prepared.access.json"]:
    from_snapshot(name, ACCESS_COMMIT)
print("Снимки: исходные git blob совпали, frozen -> inputs -> answers -> access")
for name, digest in frozen["files"].items():
    require(sha(HERE / name) == digest, "изменён замороженный файл " + name)
print(f"Замороженные файлы: {len(frozen['files'])}, отпечатки совпали")

for case, task in [("bug", "DK-1306"), ("feature", "DK-1370"), ("multi", "DK-492")]:
    ref = frozen["source_refs"][case]
    original = (HERE / (case + ".original.md")).read_text()
    require(original == git("show", f"{ref}:docs/tasks/{task}.md"), "исходник " + task)
    sources = (HERE / (case + ".sources.md")).read_text()
    pattern = r"Источник: `([^`]+):(\d+)-(\d+)`, ревизия `([^`]+)`\.\n\n```text\n(.*?)^```$"
    excerpts = re.findall(pattern, sources, re.S | re.M)
    require(bool(excerpts), "нет проверяемых выдержек " + case)
    for source, start, end, revision, body in excerpts:
        full = git("show", f"{revision}:{source}").splitlines()
        expected = "\n".join(full[int(start) - 1:int(end)]) + "\n"
        require(body == expected, "выдержка " + source + ":" + start)
    suffix = "\n--- ОБЩИЕ МАТЕРИАЛЫ ПАРЫ ---\n" + sources
    for side in ["original", "prepared"]:
        input_text = (HERE / f"{case}.{side}.input.txt").read_text()
        require(input_text.endswith(suffix), "общий материал " + case + "." + side)
        spec = (HERE / f"{case}.{side}.md").read_text()
        require("--- ПОСТАНОВКА ---\n" + spec + suffix in input_text, "постановка во входе")
print("Исходники: 3, выдержки совпали с ревизиями, общие материалы пар равны")

old_tree = git("ls-tree", "-r", "--name-only", protocol["frozen_commit"], "docs/tasks/DK-1413")
input_tree = git("ls-tree", "-r", "--name-only", protocol["inputs_commit"], "docs/tasks/DK-1413")
require(".prepared." not in old_tree, "копии уже были в коммите эталона")
require(".answer." not in input_tree, "ответы уже были в коммите входов")
print("Порядок: эталон раньше копий, исходные входы зафиксированы без ответов")

runs = protocol["runs"]
require(len(runs) == 7, "ожидались семь запусков")
require(len({run["agent"] for run in runs}) == 7, "контекст агента повторён")
for run in runs:
    require(run["fork_turns"] == "none", "унаследована история " + run["name"])
    require(run["model_override"] is None and run["reasoning_effort_override"] is None, "разные настройки")
    require(sha(HERE / run["input"]) == run["input_sha256"], "вход " + run["name"])
    require(sha(HERE / run["response"]) == run["response_sha256"], "ответ " + run["name"])
    response = json.loads((HERE / run["response"]).read_text())
    require(response["agent"] == run["agent"] and bool(response["response"]), "адрес ответа")
    require(run["tool_calls_attested"] == 1, "аттестация чтений")
print("Запуски: 7 разных контекстов, входы и дословные ответы связаны отпечатками")

v2 = (HERE / "bug.prepared.v2.input.txt").read_text()
require("--- ПОСТАНОВКА ---\n" + (HERE / "bug.prepared.v2.md").read_text() in v2, "копия повторного входа")
require(v2.endswith("--- ОБЩИЕ МАТЕРИАЛЫ ПАРЫ ---\n" + (HERE / "bug.sources.md").read_text()), "источник повторного входа")
require(not git("diff", "07cd996d51f7baefc788d196a22d5d2558ca1b81", frozen["source_refs"]["basis"], "--", "internal/taskform/stand.go", "internal/taskform/stand_test.go"), "несовместимы ревизии помощника")
print("Повтор R2 сохранён отдельно; код помощника между ревизиями не менялся")
print("Механические проверки пройдены; смысл матрицы и перенос проверяются независимо")
PY
