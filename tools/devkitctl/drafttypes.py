"""Проверка словаря типов черновиков тем же подмножеством TOML, что у taskctl."""

import os

import harness

KEYS = {"name": harness.STR, "title": harness.STR, "version": harness.INT,
        "skill": harness.STR, "prompt": harness.STR, "default_type": harness.STR,
        "required": harness.ARR}
FIELDS = {"title", "body", "type", "rank", "size", "accept", "assignee", "due", "control"}


def check_file(path, skills_dir):
    with open(path, encoding="utf-8") as f:
        text = f.read()
    doc = harness.parse(path, text)
    if len(doc.order) != 1:
        raise ValueError("%s: неизвестная секция" % path)
    head = doc.table("")
    for key, value in head.items():
        if key not in KEYS:
            raise ValueError("%s: неизвестное поле %s" % (path, key))
        if value.kind != KEYS[key]:
            raise ValueError("%s: неверный тип поля %s" % (path, key))
    name = os.path.basename(path)[:-5]
    if not harness.bare_key(name) or doc.str_of("", "name") != name:
        raise ValueError("%s: name должен совпадать с именем файла" % path)
    if not doc.str_of("", "title").strip():
        raise ValueError("%s: пустое поле title" % path)
    version = doc.get("", "version")
    if version is None or version.val != 1:
        raise ValueError("%s: version должен быть 1" % path)
    skill, prompt = doc.str_of("", "skill"), doc.str_of("", "prompt")
    if bool(skill) == bool(prompt):
        raise ValueError("%s: укажите ровно одно из полей skill и prompt" % path)
    if skill and (not harness.bare_key(skill) or not os.path.isfile(os.path.join(skills_dir, skill, "SKILL.md"))):
        raise ValueError("%s: skill %s не найден" % (path, skill))
    if doc.str_of("", "default_type") not in ("bug", "task", "LLD"):
        raise ValueError("%s: неверное поле default_type" % path)
    required = doc.arr_of("", "required")
    if any(field not in FIELDS for field in required) or len(set(required)) != len(required):
        raise ValueError("%s: неверное поле required" % path)


def check(kit_dir, project_dir, skills_dir):
    findings = []
    for directory in (kit_dir, project_dir):
        if not os.path.isdir(directory):
            continue
        for name in sorted(os.listdir(directory)):
            path = os.path.join(directory, name)
            if not name.endswith(".toml") or not os.path.isfile(path):
                continue
            try:
                check_file(path, skills_dir)
            except (ValueError, OSError, harness.TomlError) as exc:
                findings.append("тип черновика: %s" % exc)
    return findings
