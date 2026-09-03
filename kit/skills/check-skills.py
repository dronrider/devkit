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
# Граница слова отсекает «асинхронный»: этим словом правило не сказано, а
# отменено.
SYNC_SPAWN_RE = re.compile(r"\bсинхронн")
# Рубеж, который отбивает фоновый вызов в headless-сессии (DK-678).
SYNC_HOOK = "check-background.py"
# Пункт горячего списка прозы: номер с точкой в начале строки.
HOT_ITEM_RE = re.compile(r"\n\d+\. ")
# Три разряда реакции на живую реплику и находка на каждый пропавший: в
# находку берётся то слово, без которого разряда не сказать.
LIVE_REPLY_GRADES = (
    ("сразу", "ответ и поправка не названы действующими сразу, а быстрая реакция это и есть повод канала"),
    ("wait-human", "стоп не выходит маркером wait-human, виток бросит пачку на середине"),
    ("конвейер", "смена направления не ждёт конца задачи в конвейере, разворот оставит мусор в деревьях"),
)


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


def section(text, heading):
    """Тело раздела до следующего заголовка того же уровня, пустая строка если
    раздела нет. Заголовок даётся целиком, вместе с решётками.

    Заголовок внутри ограды примера концом раздела не считается: в скиллах в
    таких оградах лежат куски markdown со своими «## », и раздел резался бы
    посреди примера, а проверка хвоста молча смотрела бы на обрезок."""
    out = []
    inside = False
    fenced = False
    for line in text.split("\n"):
        if line.startswith("```"):
            fenced = not fenced
        elif not fenced and line.startswith("## "):
            if inside:
                break
            inside = line == heading
            continue
        if inside:
            out.append(line)
    return "\n".join(out)


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


def check_goal_cut(here):
    # DK-208: нарезка цели вынесена третьим скиллом, потому что зовут её двое, и
    # правила у них одни. Пробная нарезка постановки продумывает состав до
    # старта цикла и оставляет список кандидатов, виток его материализует
    # строками, а всякая нарезка кончается сверкой оценки с остатком бюджета.
    # Связка держится одним текстом, и пропажа любой её части тихая: без формата
    # кандидатов виток режет цель заново поверх продуманного списка, без сверки
    # расхождение с рамкой вылезает на gate: over, то есть после слитого
    # бюджета.
    fails = []
    cut = read(os.path.join(here, "goal-cut", "SKILL.md"))
    start = read(os.path.join(here, "goal-start", "SKILL.md"))
    loop = read(os.path.join(here, "goal-loop", "SKILL.md"))
    if cut is None:
        fails.append("goal-cut: скилл нарезки не заведён, правила состава цели терять некуда")
        return fails
    if not section(cut, "## Список кандидатов"):
        fails.append("goal-cut: нет формата списка кандидатов, пробной нарезке нечего оставить витку")
    if start is not None:
        if not section(start, "## Пробная нарезка"):
            fails.append("goal-start: нет пробной нарезки, состав цели до старта цикла не прикинуть")
        if "goal-cut" not in start:
            fails.append("goal-start: пробная нарезка не зовёт goal-cut, правила нарезки разъедутся")
    if loop is not None and "goal-cut" not in loop:
        fails.append("goal-loop: виток не зовёт goal-cut, нарезка витка разойдётся с пробной")
    check = section(cut, "## Сверка оценки с остатком бюджета")
    if not check:
        fails.append("goal-cut: нет сверки оценки с бюджетом, расхождение вылезет только на gate: over")
        return fails
    if "wait-human" not in check:
        fails.append("goal-cut: сверка не выходит маркером wait-human, виток пройдёт мимо расхождения молча")
    if "порог" not in check.lower():
        fails.append("goal-cut: ширина порога расхождения не названа, сверке нечем решать")
    if "не правит" not in check:
        fails.append("goal-cut: не сказано, что чисел «Бюджета» цикл не правит")
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


# Формулировка прогона сценария чужими руками (DK-642). Совпадение дословное:
# перефразированное правило сторож перестал бы видеть, а прогон сценария молча
# вернулся бы к автору правки.
VERIFY_PHRASE = "прогоняет не автор правки"

# Тексты, которые обязаны нести формулировку: правила доски и четыре скилла её
# конвейера. Ключ это имя в находке, значение путь от каталога скиллов или от
# корня репозитория.
VERIFY_TEXTS = ("board-task", "board-ship", "board-batch", "goal-loop")


def check_verify_runner(here, root):
    # DK-642: сценарий проверки прогоняет не автор правки, ворота taskctl close
    # сверяют прогонявшего с исполнителем разработки. Ворота держат механику, а
    # текст держит поведение: без фразы в правилах и скиллах агент прогон не
    # отметит вовсе, и воротам будет нечего сверять.
    fails = []
    files = [(name, os.path.join(here, name, "SKILL.md")) for name in VERIFY_TEXTS]
    files.append(("RULES.board.md", os.path.join(root, "RULES.board.md")))
    # Резидентное ядро несёт ту же строку: полный текст правил сессия читает по
    # надобности, а закрытие задачи идёт и без него. Прогон стенда DK-642
    # показал закрытие без отметки прогона при фразе только в полном тексте.
    files.append(("RULES.board.core.md", os.path.join(root, "RULES.board.core.md")))
    for name, path in files:
        text = read(path)
        if text is None:
            fails.append("%s: текста нет, формулировку прогона сверять не с чем" % name)
            continue
        if VERIFY_PHRASE not in text:
            fails.append("%s: нет формулировки «%s», прогон сценария вернётся к автору правки" % (name, VERIFY_PHRASE))
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


REVIEW_SECTIONS = ("## Вход", "## Порядок ревью", "## Сколько ревью нужно",
                   "## Вопросы по уровням", "## Замечания и три яруса",
                   "## Бюджет и стоп", "## Разговор с автором",
                   "## Отработка замечаний автором", "## Второй круг")
REVIEW_TIERS = ("Блокирующее", "Неблокирующее", "Мелочь")
REVIEW_KEYS = ("level1", "level2", "level3", "critical_paths", "checks")
REVIEW_SIDE_FILES = ("examples.md", "threads.md")


def check_review(here, root):
    # Скилл ревью один на все входы (доска, MR, дифф без трекера), и цифры
    # бюджетов он читает из конфига, а не из своего текста. Без раздела уровней
    # ревьювер читает дифф целиком на любой мелочи, без ярусов всякое замечание
    # снова гонит задачу на круг, а определение ревьювера, которое всё ещё
    # запрещает править код, спорит со скиллом молча.
    fails = []
    text = read(os.path.join(here, "review", "SKILL.md"))
    if text is None:
        fails.append("review: скилл ревью не заведён")
        return fails
    body = "\n" + text
    for section in REVIEW_SECTIONS:
        if "\n" + section not in body:
            fails.append("review: нет раздела «%s»" % section[3:])
    for level in range(4):
        if "| %d," % level not in text:
            fails.append("review: в таблице уровней нет уровня %d" % level)
    for tier in REVIEW_TIERS:
        if "- %s:" % tier not in text:
            fails.append("review: ярус «%s» не описан" % tier)
    if ".devkit/review.conf" not in text:
        fails.append("review: бюджеты не отданы конфигу .devkit/review.conf")
    for side in REVIEW_SIDE_FILES:
        if read(os.path.join(here, "review", side)) is None:
            fails.append("review: нет файла %s, скилл на него ссылается" % side)
        elif side not in text:
            fails.append("review: файл %s лежит рядом, а скилл его не называет" % side)
    conf = read(os.path.join(root, ".devkit", "review.conf"))
    if conf is None:
        fails.append("review: образца .devkit/review.conf в devkit нет")
    else:
        for key in REVIEW_KEYS:
            if not re.search(r"^%s\s*=" % key, conf, re.M):
                fails.append("review: в .devkit/review.conf нет ключа %s" % key)
    agents = os.path.join(root, "kit", "agents")
    for prompt in ("review-low", "review-medium", "review-high", "review-xhigh"):
        agent = read(os.path.join(agents, prompt + ".md"))
        if agent is None:
            continue  # отдельно ловится check_background_rule
        if "`review`" not in agent:
            fails.append("%s: определение не зовёт скилл review" % prompt)
        if "код ревьювер не правит" in agent:
            fails.append("%s: запрет править код спорит со скиллом review" % prompt)
    for prompt in ("exec-low", "exec-medium", "exec-high", "exec-xhigh"):
        agent = read(os.path.join(agents, prompt + ".md"))
        if agent is None:
            continue
        if not re.search(r"Отработка\s+замечаний", agent):
            fails.append("%s: исполнитель не знает раздел отработки замечаний скилла review" % prompt)
    return fails


def check_proofread(here):
    # Скилл вычитки (DK-184): процедуре нужен материал, и без него он теряет
    # смысл. pairs.md держит восемь пар по типологии DK-173, dictionary.md
    # принятые термины проекта, corpus.md сторожевой прогон. Вычитка, которой
    # подсунули пустой словарь, помечает «поезд» и «виток» как кандидаты, а
    # без корпуса правка пар или словаря уходит без страховки.
    fails = []
    skill = os.path.join(here, "proofread")
    sk = read(os.path.join(skill, "SKILL.md"))
    if sk is None:
        fails.append("proofread: скилл вычитки не заведён")
        return fails
    pairs = read(os.path.join(skill, "pairs.md")) or ""
    dictionary = read(os.path.join(skill, "dictionary.md")) or ""
    corpus = read(os.path.join(skill, "corpus.md")) or ""
    # Восемь пунктов типологии, по паре на каждый.
    for i in range(1, 9):
        marker = "## %d." % i
        if pairs.count(marker) < 1:
            fails.append("proofread: в pairs.md нет пары для пункта типологии %d" % i)
    if pairs.count("\nХорошо:") < 8:
        fails.append("proofread: в pairs.md пар «плохо/хорошо» меньше восьми")
    # Словарь принятых терминов проекта (открытый вопрос 1 LLD DK-173).
    for term in ("поезд", "виток", "лестница", "накопитель",
                 "сторожок", "заход", "ворота", "рубеж", "дорезка"):
        if term not in dictionary:
            fails.append("proofread: в dictionary.md нет термина «%s»" % term)
    # Сторожевой корпус: плохая половина с 16 фразами, эталонная с тремя
    # задачами и пороги зафиксированы.
    if "## Плохая половина" not in corpus:
        fails.append("proofread: в corpus.md нет плохой половины, некому поймать регрессию")
    if "## Эталонная половина" not in corpus:
        fails.append("proofread: в corpus.md нет эталонной половины, ложные находки не видны")
    if "14 находок из 16" not in corpus:
        fails.append("proofread: в corpus.md не зафиксирован порог плохой половины")
    if "двух ложных" not in corpus:
        fails.append("proofread: в corpus.md не зафиксирован порог эталонной половины")
    return fails


def check_proofread_spawn(here):
    # DK-548: скилл кладёт процедуру в ход тому, кто его позвал, а текст читает
    # субагент. Прогон стенда 2026-08-27 показал, чем кончается ход, когда
    # порядок не назван. Сессия звала скилл, отвечала «вычитка запущена» и
    # кончала ход, приняв скилл за фоновую задачу. Ни правок, ни строки следа в
    # файле после такого хода нет, а таблица стенда называет это непозванной
    # вычиткой.
    fails = []
    text = read(os.path.join(here, "proofread", "SKILL.md"))
    if text is None:
        return fails  # пропажу скилла отдельно ловит check_proofread
    if not SYNC_SPAWN_RE.search(text):
        fails.append("proofread: спавн субагента не назван синхронным, "
                     "ход кончится словами «вычитка запущена»")
    if "Agent" not in text:
        fails.append("proofread: не назван инструмент, "
                     "которым позвавший поднимает субагента")
    return fails


def check_prose(root):
    # DK-523: корпус эталонов работает только там, где его позвали до первой
    # написанной фразы. Точек три в файлах и четвёртая, правка README, названа
    # в самом скилле: своего файла у неё нет, и потерять её проще всего.
    # Горячий список из пяти примет держится там же: список короче пяти это
    # потерянная примета замера, и заметить пропажу можно только по числам
    # следующего замера, то есть через цикл.
    fails = []
    skills = os.path.join(root, "kit", "skills")
    text = read(os.path.join(skills, "prose", "SKILL.md"))
    if text is None:
        fails.append("prose: скилл письма не заведён")
        return fails
    body = "\n" + text
    hot = section(text, "## Горячий список")
    if "\n## Горячий список" not in body:
        fails.append("prose: нет горячего списка примет, в контекст письма едут одни фрагменты")
    else:
        n = len(HOT_ITEM_RE.findall("\n" + hot))
        if n != 5:
            fails.append("prose: примет в горячем списке %d, а замер DK-446 дал пять" % n)
    if "README" not in text:
        fails.append("prose: правка README не названа точкой вызова, а своего скилла у неё нет")
    for path, who in ((os.path.join(skills, "board-groom", "SKILL.md"), "board-groom"),
                      (os.path.join(skills, "board-task", "SKILL.md"), "board-task"),
                      (os.path.join(root, "kit", "agents", "exec-xhigh.md"), "exec-xhigh")):
        point = read(path)
        if point is None:
            continue  # пропажу файла ловят check_skills и check_background_rule
        if "prose.py sample" not in point:
            fails.append("%s: не зовёт выборку эталонов, текст пишется без корпуса" % who)
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


def check_sync_spawn(root):
    # DK-314: работу с экрана дашборда ведёт headless-сессия (`claude -p`), а
    # она кончает ход финальным текстом и добивает недождавшиеся фоновые задачи
    # через десять минут. Диспетчер, спавнящий исполнителя или ревьювера фоном,
    # теряет любого субагента длиннее этих десяти минут: работа остаётся
    # незакоммиченным диффом в дереве задачи, tmux-сессия схлопывается. Правило
    # держится одним текстом board-batch и board-ship, поэтому вместе со словом
    # про синхронный спавн проверяется и причина: без неё правило читается как
    # предпочтение диспетчера и переживёт первое же сомнение.
    #
    # DK-678: с тех пор у правила появился рубеж, и запрет привязан к виду
    # сессии. Скилл обязан называть и рубеж: разъехавшись с ним, текст либо
    # обещает отказ там, где его нет, либо зовёт фон там, где он отбит.
    fails = []
    skills = os.path.join(root, "kit", "skills")
    for skill in ("board-batch", "board-ship"):
        text = read(os.path.join(skills, skill, "SKILL.md"))
        if text is None:
            continue  # отдельно ловится check_skills
        if not SYNC_SPAWN_RE.search(text):
            fails.append("%s: спавн субагента не назван синхронным, в headless фоновый исполнитель гибнет" % skill)
        if "headless" not in text:
            fails.append("%s: синхронный спавн назван без причины, а причина это headless-сессия и её десять минут" % skill)
        if SYNC_HOOK not in text:
            fails.append("%s: запрет на фон не назвал рубеж %s, и текст разъедется с механикой отказа"
                         % (skill, SYNC_HOOK))
    return fails


def check_live_reply(here):
    # DK-343 по LLD DK-136: виток получает реплику человека теперь и посреди
    # работы, добавкой контекста от подхвата реплики. Правило реакции живёт одним
    # текстом скилла, и пропажа любого его разряда тихая: без немедленного
    # ответ на вопрос витка ждёт конца пачки, без границы шага стоп бросает
    # пачку на середине, без конца задачи в конвейере разворот оставляет
    # исполнителей в чужих деревьях и непроверенный выкат в очереди. Сюда же
    # зов оболочки: права машинного контура дают `Bash(python3:*)`
    # (MACHINE_ALLOW в tools/devkitctl/perms.py), правила под голый путь там
    # нет, и headless-виток встанет на запросе разрешения, который некому
    # одобрить.
    fails = []
    text = read(os.path.join(here, "goal-loop", "SKILL.md"))
    if text is None:
        return fails  # отдельно ловится check_goal_split
    body = section(text, "## Живая реплика")
    if not body:
        fails.append("goal-loop: нет раздела про живую реплику, доставленная посреди витка строка останется без правила")
    else:
        if "подхват" not in body or "контекст" not in body:
            fails.append("goal-loop: не сказано, что реплику вносит в контекст витка подхват, виток станет ждать шага 1")
        for word, why in LIVE_REPLY_GRADES:
            if word not in body:
                fails.append("goal-loop: %s" % why)
        if "Журнал" not in body:
            fails.append("goal-loop: живая реплика не оседает записью «Журнала», поворот работы останется без причины")
    for line in text.split("\n"):
        if "goal-run.py" in line and "/" in line and "python3" not in line:
            fails.append("goal-loop: оболочка зовётся путём без python3, а машинный контур даёт только Bash(python3:*)")
            break
    return fails


def run(here, root):
    """Все проверки разом. Возврат (находки, число скиллов)."""
    fails = []
    skill_fails, n = check_skills(here)
    fails += skill_fails
    fails += check_procedural_rules(here, root)
    fails += check_goal_split(here)
    fails += check_goal_cut(here)
    fails += check_live_reply(here)
    fails += check_groom(here)
    fails += check_verify_runner(here, root)
    fails += check_team(here)
    fails += check_review(here, root)
    fails += check_proofread(here)
    fails += check_proofread_spawn(here)
    fails += check_prose(root)
    fails += check_rules_backlink(here)
    fails += check_background_rule(root)
    fails += check_sync_spawn(root)
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
