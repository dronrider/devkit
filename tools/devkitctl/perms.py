#!/usr/bin/env python3
"""Права машинного контура: чем работает сессия, поднятая без человека.

  python3 perms.py [--fix]
      сверить настройки харнеса (~/.claude/settings.json) с перечнем прав
      машинного контура и назвать недостающее; --fix дописывает его,
      рукописного не трогая. Выход 0 права на месте, 1 находки, 2 ошибка
      запуска.

Виток цели поднимается голым `claude -p`, и одобрить запрос харнеса в такой
сессии некому: неразрешённый инструмент отвечает отказом, а сессия идёт дальше
и не делает ничего. Так встал первый виток цели DK-109: девять отказов подряд,
включая правку файлов, и ни строки в «Журнале», так что со стороны стоп был
неотличим от молчания. Права поэтому раскладываются заранее, тем же порядком,
каким доктор кладёт определения агентов и скиллы, а оболочка цели спрашивает
их перед первым витком.
"""
import json
import os
import say
import sys
from pathlib import Path

# Три формы слова для счёта в отчёте: одно право, два права, тридцать пять прав.
RIGHT = ("право машинного контура", "права машинного контура", "прав машинного контура")
DENY_RIGHT = ("deny-правило на чтение секрета",
              "deny-правила на чтение секрета",
              "deny-правил на чтение секрета")
SETTINGS = "~/.claude/settings.json"
DOCTOR = Path(__file__).resolve().parent / "devkitctl.py"

# Перечень прав машинного контура. Список лежит тут, а не во флагах вызова
# `claude -p`, потому что права нужны всем входам сразу (оболочка, витки
# сессии-диспетчера, субагенты пачки), а у разложенного на машине списка один
# хозяин и одна диагностика. Правится он задачей на доске devkit: цикл себе
# полномочий не выписывает.
MACHINE_ALLOW = (
    # Утилиты devkit: доска, конвейер, вердикт pick с гейтом бюджета, стенды.
    "Bash(taskctl:*)",
    "Bash(shipctl:*)",
    "Bash(agentctl:*)",
    "Bash(regcheck:*)",
    "Bash(obeycheck:*)",
    # Дашборд и обвязка проекта зовутся именем: у обоих в PATH стоит своя
    # обёртка релиза, и Bash(python3:*) её не кроет. Сценарии проверки этого
    # репозитория держатся на них двоих, `dashboard smoke` и `devkitctl doctor`
    # стоят почти в каждом (DK-739).
    #
    # Дашборд взят двумя подкомандами, а не целым бинарём. `dashboard secret`
    # печатает живой токен входа панели, а с `--rotate` разом разлогинивает все
    # сессии, и маска на весь бинарь отдавала бы это сессии без человека без
    # единого подтверждения. Read-запреты сюда не достают, они режут чтение
    # файла, а токен приходит выводом команды.
    "Bash(dashboard check:*)",
    "Bash(dashboard smoke:*)",
    "Bash(devkitctl:*)",
    # git-хуки и уведомитель зовутся интерпретатором путём из чекаута devkit,
    # своих имён в PATH у них нет.
    "Bash(python3:*)",
    # Git целиком: виток коммитит, сливает, пушит и заводит деревья задач.
    "Bash(git:*)",
    # Чем гоняются тесты и сборки проектов. Перечень открытый: про инструмент,
    # которого тут нет, виток скажет строкой «Журнала» и маркером, а список
    # правится задачей на доске devkit.
    "Bash(sh:*)",
    "Bash(go:*)",
    "Bash(cargo:*)",
    "Bash(npm:*)",
    # node отдельно от npm: стенды дашборда это отдельные файлы
    # `node testdata/*.mjs`, и через npm они не зовутся.
    "Bash(node:*)",
    "Bash(make:*)",
    "Bash(pytest:*)",
    # Мелочь, которой смотрят дерево, текст и время. Без неё виток встаёт на
    # первом же `wc -l`, а польза от запрета нулевая: то же самое он читает
    # инструментами харнеса.
    "Bash(ls:*)",
    "Bash(cat:*)",
    "Bash(head:*)",
    "Bash(tail:*)",
    "Bash(wc:*)",
    "Bash(grep:*)",
    "Bash(rg:*)",
    "Bash(find:*)",
    "Bash(sed:*)",
    "Bash(awk:*)",
    "Bash(sort:*)",
    "Bash(uniq:*)",
    "Bash(diff:*)",
    "Bash(date:*)",
    "Bash(mkdir:*)",
    "Bash(cp:*)",
    "Bash(mv:*)",
    "Bash(chmod:*)",
    # Снимок квоты снимается панелью /usage в отдельной сессии tmux.
    "Bash(tmux:*)",
    # Чтение и правка файлов. Дерево задачи shipctl заводит рядом с проектом, и
    # виток работает разом в нескольких деревьях, так что путями эти права не
    # сузить: машинных путей в devkit нет.
    "Read",
    "Edit",
    "Write",
)

# Секретные пути, чтение которых инструментом Read режется в deny. Значения
# секретов не должны ехать в контекст модели (цель DK-207), а secretctl даёт к
# ним доступ без прямого чтения файла, поэтому рубить чтение можно, не оставляя
# агента без средства получить значение. Обход этого запрета через Bash (cat,
# grep и подобное) прикрывает PreToolUse-хук check-read-secret.py, пути те же.
# local-docs ищется по cwd проекта, остальные по домашнему каталогу машины.
SECRET_DENY = (
    "Read(~/.claude/access.local.md)",
    "Read(~/.ssh/**)",
    "Read(~/.devkit/secrets/**)",
    "Read(./local-docs/**)",
)
SECTIONS = ("allow", "deny", "ask")


def rule_list(rules, head=8):
    rules = list(rules)
    if len(rules) <= head:
        return ", ".join(rules)
    return ", ".join(rules[:head]) + " и ещё %d" % (len(rules) - head)


def covered(rule, granted):
    # Правило считается выданным и тогда, когда разрешён инструмент целиком:
    # Bash покрывает любое Bash(...), и требовать поверх него перечень значило
    # бы звать чинить исправное.
    if rule in granted:
        return True
    tool = rule.split("(", 1)[0]
    return tool in granted or ("%s(*)" % tool) in granted


def load(settings):
    # Возврат это (данные, беда). Файла нет и файл пустой это не беда, а машина,
    # на которой права ещё не раскладывали.
    try:
        text = settings.read_text(encoding="utf-8")
    except FileNotFoundError:
        return {}, None
    except OSError as e:
        return None, str(e)
    if not text.strip():
        return {}, None
    try:
        data = json.loads(text)
    except ValueError as e:
        return None, str(e)
    if not isinstance(data, dict):
        return None, "верхний уровень не объект"
    perms = data.get("permissions", {})
    if not isinstance(perms, dict):
        return None, "permissions не объект"
    for key in SECTIONS:
        v = perms.get(key, [])
        if not isinstance(v, list) or any(not isinstance(s, str) for s in v):
            return None, "permissions.%s не список строк" % key
    return data, None


def granted(data, key):
    return (data.get("permissions") or {}).get(key) or []


def write(settings, data, allow_missing, deny_missing=()):
    # Правка строго additive: рукописные правила остаются на месте и в своём
    # порядке, недостающие дописываются в конец, прочие ключи настроек
    # переезжают как есть. Файл при этом переписывается целиком, отступом в два
    # пробела: JSON комментариев не держит, и терять в нём нечего. Дописываются
    # и allow (права машинного контура), и deny (чтение секретов): оба списка
    # держит один рубеж, и чужой порядок в каждом остаётся как записан.
    perms = data.setdefault("permissions", {})
    if allow_missing:
        perms.setdefault("allow", []).extend(allow_missing)
    if deny_missing:
        perms.setdefault("deny", []).extend(deny_missing)
    settings.parent.mkdir(parents=True, exist_ok=True)
    tmp = settings.with_name(settings.name + ".devkit-tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(str(tmp), str(settings))


def check(settings, fix=False, worktree_main=None):
    # worktree_main это тот же рубеж, каким огорожены определения агентов и
    # скиллы: непустым он приходит, когда devkit выложен worktree ветки задачи,
    # и тогда перечень только сверяется, а чинить зовут из основного чекаута.
    # Права видны каждой сессии на машине сразу, и выписать их себе
    # непроверенной веткой цикл не должен даже случайно.
    from_main = worktree_main is None
    doctor = DOCTOR if from_main else Path(worktree_main) / "tools" / "devkitctl" / "devkitctl.py"
    whence = "" if from_main else ("devkit тут выложен worktree ветки задачи, права с "
                                   "непроверенной ветки на машину не едут; из основного чекаута: ")
    settings = Path(os.path.expanduser(str(settings)))
    findings, fixed = [], []
    data, bad = load(settings)
    if bad is not None:
        return ["настройки харнеса %s не разбираются (%s): прав машинного контура в них "
                "не проверить и не дописать, а без них сессия без человека встаёт на первом "
                "же отказе; починить файл руками" % (settings, bad)], fixed
    blocked = []
    for key in ("deny", "ask"):
        rules = [r for r in MACHINE_ALLOW if covered(r, granted(data, key))]
        if rules:
            blocked += rules
            findings.append("права машинного контура запрещены в permissions.%s файла %s (%s): "
                            "сессия без человека на них встанет, а записанное руками --fix "
                            "не трогает" % (key, settings, rule_list(rules)))
    allow = granted(data, "allow")
    deny = granted(data, "deny")
    missing = [r for r in MACHINE_ALLOW if r not in blocked and not covered(r, allow)]
    # SECRET_DENY режет инструмент Read на секретных путях, и из deny выпадает
    # отдельной статьёй: обход через Bash прикрывает хук, а прямое чтение из
    # контекста модели рубится именно тут.
    missing_deny = [r for r in SECRET_DENY if r not in deny]
    if not missing and not missing_deny:
        return findings, fixed
    if fix and from_main:
        write(settings, data, missing, missing_deny)
        if missing:
            # Сами правила тут не перечисляются: их три десятка, читать этот список
            # человеку незачем, а посмотреть его есть где, в самих настройках.
            fixed.append("дописано %s в %s" % (say.counted(len(missing), RIGHT), settings))
        if missing_deny:
            fixed.append("поставлено %s в %s: инструмент Read режется на файле доступов, "
                         "приватных ключах, хранилище secretctl и local-docs, значение "
                         "берётся через secretctl (цель DK-207)"
                         % (say.counted(len(missing_deny), DENY_RIGHT), settings))
        return findings, fixed
    if missing:
        findings.append("в %s не хватает прав машинного контура, %d из %d (%s): одобрять запросы "
                        "харнеса в сессии без человека некому, и виток цели молча не сделает "
                        "ничего; разложить: %spython3 %s doctor --fix"
                        % (settings, len(missing), len(MACHINE_ALLOW), rule_list(missing),
                           whence, doctor))
    if missing_deny:
        findings.append("в %s не хватает %s в deny (%s): файл доступов, приватные ключи, "
                        "хранилище secretctl и local-docs читаются инструментом Read, и "
                        "значение уезжает в контекст модели; рубится deny, значение берётся "
                        "через secretctl (цель DK-207); разложить: %spython3 %s doctor --fix"
                        % (settings, say.counted(len(missing_deny), DENY_RIGHT),
                           rule_list(missing_deny), whence, doctor))
    return findings, fixed


def main(argv):
    fix = False
    for a in argv:
        if a == "--fix":
            fix = True
        elif a in ("-h", "--help"):
            sys.stdout.write(__doc__)
            return 0
        else:
            sys.stderr.write("неизвестный аргумент %s\n" % a)
            return 2
    findings, fixed = check(SETTINGS, fix)
    for m in fixed:
        print("починено: %s" % m)
    for f in findings:
        print(f)
    if findings:
        return 1
    if not fixed:
        print("права машинного контура на месте")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
