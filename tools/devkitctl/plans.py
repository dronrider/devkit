"""Проверка набора шаблонов плана (LLD DK-972, решения 2 и 9).

Шаблоны читает go-пакет internal/plans, а доктору нужна та же сверка слоёв:
проектная копия встроенного шаблона обязана нести каждый встроенный этап либо
секцией, либо записью dropped с причиной. Отставшую копию иначе видно только на
инциденте, а разобрать её файл умеет тот же парсер подмножества TOML, которым
читаются профили харнесов.
"""

import os

import harness

# Кто ведёт этап и какие ворота след спрашивают. Словарь тот же, что в
# internal/plans: разойдись он, и доктор пропускал бы то, на чём встаёт утилита.
BY = ("сам", "субагент", "человек")
GATES = ("check", "close", "merge", "ready", "push")
ACCEPTS = ("agent", "mixed", "user")
# Известные ключи шапки и этапа. Опечатку в ключе никто не читает, и настройка
# выглядит поставленной, поэтому незнакомый ключ идёт предупреждением.
HEAD_KEYS = ("name", "title", "types", "dropped")
STAGE_KEYS = ("title", "skill", "by", "agent", "trace", "gate", "types", "paths",
              "accept", "slot", "scenario")


class PlanError(Exception):
    pass


def unknown_keys(name, keys, known, section):
    """Предупреждения на ключи вне словаря. Свод тот же, что в internal/plans."""
    where = "в секции [%s]" % section if section else "на верхнем уровне"
    return ["%s: незнакомый ключ %s %s, пропущен" % (name, harness.quote(k), where)
            for k in keys if k not in known]


def read_template(path):
    """Разбор одного файла шаблона: шапка, порядок секций и проверка ключей.

    Строгость та же, что у go-стороны (internal/plans). Значение, по которому
    движок выбирает поведение, с опечаткой отбивается отказом. Незнакомый ключ
    уезжает предупреждением в поле warns.
    """
    name = os.path.basename(path)
    base = name[:-len(".toml")] if name.endswith(".toml") else name
    with open(path, encoding="utf-8") as f:
        text = f.read()
    d = harness.parse(name, text)
    got = d.str_of("", "name")
    if got and got != base:
        raise PlanError("%s: name = %s, а файл называется %s: имя шаблона это имя файла"
                        % (name, harness.quote(got), harness.quote(base)))
    title = d.str_of("", "title")
    if not title:
        raise PlanError("%s: нет title: по нему шаблон выбирают в plan templates" % name)
    dropped = {}
    for raw in d.arr_of("", "dropped"):
        who, sep, why = raw.partition(":")
        if not sep or not who.strip() or not why.strip():
            raise PlanError("%s: dropped = %s без причины: жду \"<этап>: <причина>\""
                            % (name, harness.quote(raw)))
        dropped[who.strip()] = why.strip()
    warns = unknown_keys(name, list(d.table("").keys()), HEAD_KEYS, "")
    stages = []
    slots = 0
    for sec in d.order:
        if sec == "":
            continue
        by = d.str_of(sec, "by")
        if by not in BY:
            raise PlanError("%s: [%s] by = %s, допустимы %s"
                            % (name, sec, harness.quote(by), ", ".join(BY)))
        if not d.str_of(sec, "title"):
            raise PlanError("%s: [%s] нет title: русским именем этап зовут план, ворота "
                            "и пометка исключения" % (name, sec))
        if not d.str_of(sec, "trace"):
            raise PlanError("%s: [%s] нет trace: этап без следа пишется trace = \"слово\""
                            % (name, sec))
        for g in d.arr_of(sec, "gate"):
            if g not in GATES:
                raise PlanError("%s: [%s] gate = %s, ворота известны эти: %s"
                                % (name, sec, harness.quote(g), ", ".join(GATES)))
        for a in d.arr_of(sec, "accept"):
            if a not in ACCEPTS:
                raise PlanError("%s: [%s] accept = %s, виды приёмки эти: %s"
                                % (name, sec, harness.quote(a), ", ".join(ACCEPTS)))
        slot = d.get(sec, "slot")
        if slot is not None and slot.kind == harness.BOOL and slot.val:
            slots += 1
        warns += unknown_keys(name, list(d.table(sec).keys()), STAGE_KEYS, sec)
        stages.append((sec, d.str_of(sec, "title")))
    if not stages:
        raise PlanError("%s: этапов нет: шаблон без секций плана не собирает" % name)
    if slots > 1:
        raise PlanError("%s: slot = true стоит у %d этапов, а свои пункты агента "
                        "ложатся в один" % (name, slots))
    return {"name": base, "title": title, "dropped": dropped, "stages": stages,
            "warns": warns}


def read_dir(path):
    """Слой набора словарём по имени. Пропавший каталог это пустой слой."""
    out, findings = {}, []
    if not os.path.isdir(path):
        return out, findings
    for fn in sorted(os.listdir(path)):
        if not fn.endswith(".toml"):
            continue
        try:
            t = read_template(os.path.join(path, fn))
        except (PlanError, harness.TomlError, OSError) as e:
            findings.append("шаблон плана битый: %s" % e)
            continue
        out[t["name"]] = t
        findings += ["шаблон плана: %s" % w for w in t["warns"]]
    return out, findings


def check_plans(kit_dir, project_dir):
    """Находки доктора: битый шаблон и проектная копия, отставшая от встроенной."""
    kit, findings = read_dir(kit_dir)
    proj, pf = read_dir(project_dir)
    findings += pf
    for name, t in sorted(proj.items()):
        base = kit.get(name)
        if base is None:
            continue
        for key, title in base["stages"]:
            if key in [s[0] for s in t["stages"]] or key in t["dropped"]:
                continue
            findings.append(
                "шаблон плана %s: копия проекта отстала, этапа [%s] (%s) в ней нет ни "
                "секцией, ни в dropped; вписать этап либо снять с причиной: "
                "dropped = [\"%s: <причина>\"]" % (name, key, title, key))
    return findings
