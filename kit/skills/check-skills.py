#!/usr/bin/env python3
"""Самопроверка скиллов devkit: у каждого разбираемый frontmatter с именем по
директории и описанием, по которому видно, когда скилл звать, а у процедурной
части правил (board-task, board-ship, test-standard) ещё и вход из полного
текста правил. Инструмент без скиллов зовёт процедуру по указателю, и путь в
указателе берётся из тех же файлов, поэтому пропавший скилл это не косметика,
а потерянная процедура.

  check-skills.py

Без аргументов: проверяет весь каталог kit/skills рядом со своим расположением.
Выход 0 всё в порядке, 1 есть находки (список в stderr).
"""
import os
import re
import sys

RULES_BACKLINK_RE = re.compile(r"RULES\.(board\.)?md")


def meta(path, key):  # значение ключа frontmatter
    try:
        with open(path, encoding="utf-8") as f:
            lines = f.read().split("\n")
    except OSError:
        return ""
    if not lines or lines[0] != "---":
        return ""
    prefix = key + ":"
    for line in lines[1:]:
        if line == "---":
            break
        if line.startswith(prefix):
            return line[len(prefix):].lstrip(" ")
    return ""


def read(path):
    try:
        with open(path, encoding="utf-8") as f:
            return f.read()
    except OSError:
        return None


def check_skills(here):
    """Возврат (находки, число скиллов)."""
    fails = []
    n = 0
    for name in sorted(os.listdir(here)):
        skill = os.path.join(here, name)
        if not os.path.isdir(skill) or name == "__pycache__":
            continue
        n += 1
        f = os.path.join(skill, "SKILL.md")
        text = read(f)
        if text is None:
            fails.append("%s: нет SKILL.md, скилл сессия не подхватит" % name)
            continue
        first_line = text.split("\n", 1)[0]
        if first_line != "---":
            fails.append("%s: нет frontmatter, описание в промпт не уедет" % name)
        got = meta(f, "name")
        if got != name:
            fails.append("%s: во frontmatter имя «%s», а скилл зовётся по директории" % (name, got))
        desc = meta(f, "description")
        if not desc:
            fails.append("%s: пустое описание, звать скилл сессии будет не по чему" % name)
        elif "Звать," not in desc and "Use this skill" not in desc:
            fails.append("%s: описание не говорит, когда скилл звать: %s" % (name, desc))
        if text.count("\n") <= 10:
            fails.append("%s: тело скилла пустое" % name)
    if n == 0:
        fails.append("скиллов не нашлось вовсе")
    return fails, n


def check_procedural_rules(here, root):
    # Процедурная часть правил: разрез резидента (docs/lld/DK-100-context-tree.md,
    # задача D) вынес шаги отсюда в скиллы, и вход к ним обязан быть назван в
    # полном тексте. Инструмент со скиллами зовёт их по описанию, а тот, кому
    # текст вклеивается целиком, только по этой строке: без неё процедура не
    # уехала, а пропала.
    fails = []
    for skill, src in (("board-task", "RULES.board.md"), ("board-ship", "RULES.board.md"),
                       ("test-standard", "RULES.md")):
        if not os.path.isfile(os.path.join(here, skill, "SKILL.md")):
            fails.append("%s: процедура правил скиллом не заведена" % skill)
        text = read(os.path.join(root, src)) or ""
        if ("kit/skills/%s/SKILL.md" % skill) not in text:
            fails.append("%s не называет путь до скилла %s: процедуру по правилам не найти" % (src, skill))
    return fails


def check_goal_split(here):
    # Режим цели разрезан по шву между постановкой и витком (DK-118): постановка
    # зовётся один раз на цель, а виток десятки раз, и до разреза каждый виток
    # тащил в контекст текст постановки. Разрез держится только текстом, поэтому
    # проверяется, что половины не срослись обратно и что вход из одной в
    # другую назван: потерянный вход это цель, которую некому продолжить.
    fails = []
    for skill, other in (("goal-start", "goal-loop"), ("goal-loop", "goal-start")):
        f = os.path.join(here, skill, "SKILL.md")
        text = read(f)
        if text is None:
            fails.append("%s: скилл режима цели не заведён" % skill)
            continue
        if other not in text:
            fails.append("%s: не называет соседа %s, вторая половина режима потеряна" % (skill, other))
    start = "\n" + (read(os.path.join(here, "goal-start", "SKILL.md")) or "")
    loop = "\n" + (read(os.path.join(here, "goal-loop", "SKILL.md")) or "")
    if "\n## Разделы файла цели" not in start:
        fails.append("goal-start: нет разделов файла цели, постановке нечем заводить цель")
    if "\n## Маркеры выхода" not in loop:
        fails.append("goal-loop: нет маркеров выхода, витку нечем кончиться")
    if "\n## Виток" in start:
        fails.append("goal-start: тащит процедуру витка, разрез сросся обратно")
    if "\n## Разделы файла цели" in loop:
        fails.append("goal-loop: тащит постановку, разрез сросся обратно")
    return fails


def check_groom(here):
    # Грумминг разрезан на фазу разбора и фазу оформления (DK-129): у захода
    # три входа, а у разобранного черновика четыре исхода, и под каждый исход
    # своя команда taskctl. Выпавший из текста вход или исход это ветка
    # разбора, которую сессия молча не пройдёт, и заметить пропажу на живом
    # заходе некому.
    fails = []
    groom = os.path.join(here, "board-groom", "SKILL.md")
    text = read(groom)
    if text is None:
        fails.append("board-groom: скилл грумминга не заведён")
        return fails
    body = "\n" + text
    if "\n## Входы" not in body:
        fails.append("board-groom: нет раздела про входы, откуда берётся список черновиков не сказано")
    n_in = body.count("\n- `/board-groom")
    if n_in != 3:
        fails.append("board-groom: входов названо %d, а их три (один ID, список ID, весь накопитель)" % n_in)
    if "\n## Исходы разбора" not in body:
        fails.append("board-groom: нет раздела про исходы, разобранный черновик остаётся без следа")
    for cmd in ("add --id", "draft attach", "draft defer", "draft drop"):
        if cmd not in text:
            fails.append("board-groom: исход без команды, в процедуре нет taskctl %s" % cmd)
    return fails


def check_team(here):
    # Командная работа по одной доске (DK-174): у скилла два несущих куска,
    # захват с немедленным пушем и перечень столкновений. Столкновение, выпавшее
    # из текста, это ветка разбора, которую сессия молча не пройдёт, а заметить
    # пропажу можно только на живом столкновении двух рук, то есть поздно.
    fails = []
    text = read(os.path.join(here, "board-team", "SKILL.md"))
    if text is None:
        fails.append("board-team: скилл командной работы не заведён")
        return fails
    body = "\n" + text
    if "\n## Захват задачи" not in body:
        fails.append("board-team: нет раздела про захват, брать задачу при живых соседях нечем")
    if "--push" not in text:
        fails.append("board-team: захват не объявляется пушем, соседу его не увидеть")
    n = body.count("\n**")
    if n < 4:
        fails.append("board-team: разобрано столкновений %d, а названо их четыре" % n)
    for skill in ("board-task", "board-ship", "board-batch"):
        if skill not in text:
            fails.append("board-team: не называет соседа %s, граница процедур размыта" % skill)
    return fails


def check_rules_backlink(here):
    # Скилл ссылается на правило, из которого выведен: расхождение процедуры
    # с правилом иначе замечается только чтением обоих подряд.
    fails = []
    for skill in ("board-task", "board-ship", "test-standard"):
        text = read(os.path.join(here, skill, "SKILL.md")) or ""
        if not RULES_BACKLINK_RE.search(text):
            fails.append("%s: скилл не называет правило, из которого выведен" % skill)
    return fails


def check_background_rule(root):
    # DK-165: у фоновой команды Bash в субагенте уведомление доходит только в
    # главный цикл сессии, и ход, законченный текстом, считается финальным
    # ответом. Без прямого правила в промпте исполнитель возвращается с
    # незакоммиченной работой, а диспетчер без страховки принимает это за отчёт.
    # Правило лежит во всех промптах исполнителей и ревьюверов, страховка
    # диспетчера в board-batch и board-ship: выпавший из текста маркер это та же
    # ловушка, что и вживую.
    fails = []
    agents = os.path.join(root, "kit", "agents")
    prompts = ("exec-low", "exec-medium", "exec-high", "exec-xhigh",
               "review-low", "review-medium", "review-high", "review-xhigh")
    for prompt in prompts:
        text = read(os.path.join(agents, prompt + ".md"))
        if text is None:
            fails.append("%s: промпт агента не найден, правило фонового прогона некуда положить" % prompt)
            continue
        if "возврат диспетчеру" not in text:
            fails.append("%s: не сказано, что конец хода это возврат диспетчеру" % prompt)
        if "foreground" not in text:
            fails.append("%s: не сказано ждать фоновый прогон в foreground" % prompt)
    skills = os.path.join(root, "kit", "skills")
    for skill in ("board-batch", "board-ship"):
        text = read(os.path.join(skills, skill, "SKILL.md"))
        if text is None:
            continue  # отдельно ловится check_skills
        if "foreground" not in text:
            fails.append("%s: нет страховки диспетчера от возврата с незакоммиченным" % skill)
    return fails


def run(here, root):
    """Все проверки разом. Возврат (находки, число скиллов)."""
    fails = []
    skill_fails, n = check_skills(here)
    fails += skill_fails
    fails += check_procedural_rules(here, root)
    fails += check_goal_split(here)
    fails += check_groom(here)
    fails += check_team(here)
    fails += check_rules_backlink(here)
    fails += check_background_rule(root)
    return fails, n


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    root = os.path.dirname(os.path.dirname(here))
    fails, n = run(here, root)
    for text in fails:
        sys.stderr.write("FAIL: %s\n" % text)
    if not fails:
        print("ok: скиллы, проверено %d" % n)
        return 0
    sys.stderr.write("провалов: %d\n" % len(fails))
    return 1


if __name__ == "__main__":
    sys.exit(main())
