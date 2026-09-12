"""Стенд трёх ожиданий конвейера (DK-936).

У цели DK-902 три уровня ожидания, и каждый сделан своей задачей. Внутри
хода ждёт голова отметкой `agentctl wait`, а оболочка конвейера ждёт за неё и
воронкой такой проход не считает. Дольше хода ждёт запись на диске. Строка
стоит в Blocked с машинным разрядом, а соседняя взведена ребром «после».
Обход ждущих поднимает обе командами `taskctl close` и `shipctl merge`.
Событие, прошедшее мимо обеих команд, добирает тик сторожка. Тесты у каждой
задачи свои, а сквозного прогона через все утилиты разом не было. Связка
соединяет go-утилиты, python-оболочку, git и подставной tmux, и ломается
именно на этом стыке.

Круг гоняется на временном репозитории со своей доской и своим домом. Дом
подменяется по той же причине, что и в selfcheck. Утилиты devkit пишут в
~/.devkit без ведома вызвавшего (замок головы, реестр этапов, отметки
ожидания), и стенд обязан оставить машину нетронутой. Он вовсе не трогает
служб launchd, тик зовётся прямо функцией обхода, а не через демона.

Подставных программ две. Клиент харнеса это голова, которая умеет ровно то,
что проверяется: отметить ожидание, отметиться в журнале подъёмов, запарковать
свою строку. Подставной tmux отказывает на любой команде, и лестница носителей
падает с живого окна на headless, не трогая tmux-сервер машины.

Отличие от selfcheck: тот проверяет связку в движении по одной задаче, а этот
берёт три ожидания и на каждое печатает строку исхода. Общее у них временный
проект и подставной выкат, а общие куски стенд берёт у соседа вместо второй
копии.
"""
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import selfcheck
import watch

HERE = Path(__file__).resolve().parent
DEVKIT = HERE.parent.parent

PREFIX = "WT"
# Строки временной доски. Первая проходит воронку оболочки, вторая это
# предпосылка, чьё слияние и закрытие поднимают двух ждущих, последние две
# изображают событие, прошедшее мимо утилит.
FUNNEL = "WT-001"
DEP = "WT-002"
PARKED = "WT-003"
ARMED = "WT-004"
HAND = "WT-005"
MISSED = "WT-006"

# Режимы подставного клиента. В режиме ожидания голова кончает ход отметкой
# `agentctl wait`, в режиме записи она только отмечается в журнале подъёмов:
# обходу ждущих и тику важен сам факт поднятой головы.
MODE_WAIT = "ожидание"
MODE_RECORD = "запись"

# Сколько проходов подряд голова кончает ожиданием. Воронка оболочки встаёт на
# третьем холостом проходе, поэтому ожиданий берётся на один больше. Три
# прошли бы и при сломанном счёте.
WAIT_PASSES = 4
# Срок отметки ожидания. Короче секунды его не поставить (время в записи идёт
# до секунды), а длинный растянул бы прогон вчетверо.
WAIT_UNTIL = "5s"
# Причина парковки, которой голова кончает первое ожидание. Проза, а не
# машинный разряд. Разбуженная строка ушла бы в подъём посреди прогона.
PARK_WHY = "стенд: голова отработала свои проходы"

# Подставной выкат и тесты временного проекта: обе команды на самом python3,
# метка выката ложится в каталог проекта.
DEPLOY_CMD = "python3 -c \"open('.devkit/waitcheck-deployed','w').close()\""
TEST_CMD = "python3 -c pass"

# Пары окружения стенда. Уведомления выключены: звать человека к синтетической
# задаче незачем. Пауза между проходами снимается, а шаг ожидания оставляется
# секундой: нулевой шаг превратил бы ожидание в холостой цикл процессора.
# DEVKIT_HOME указывает на дерево devkit, откуда лестница носителей берёт
# оболочку конвейера и профиль харнеса.
STAND_ENV = {
    "DEVKIT_NOTIFY_OFF": "1",
    "DEVKIT_TASK_PASS_PAUSE": "0",
    "DEVKIT_TASK_WATCH_STEP": "1",
    "DEVKIT_HARNESS": "claude-code",
}

# Сценарий синтетической предпосылки и её раздел «Проверка». Ворота слияния
# спрашивают сценарий, ворота закрытия непустую проверку, а сведения о себе
# стенд пишет сам. Слова соседнего круга рассказывали бы тут о чужой работе.
SCENARIO_WORDS = ("1. devkitctl waitcheck во временном каталоге.\n"
                  "2. Каждое из трёх ожиданий отвечает «ок», итог «три ожидания "
                  "прошли».\n\n"
                  "Шаг, выполнимый без выката, лежит блоком: его гоняет обкатка,\n"
                  "и ворота перевода в Check спрашивают её отметку.\n\n"
                  "```sh\ntest -f docs/TASKS.md && grep -q %s docs/TASKS.md\n```\n\n"
                  "Ожидаемый итог: слияние этой строки поднимает взведённого "
                  "соседа, а закрытие припаркованного.\n")
VERIFY_WORDS = ("слияние и закрытие этой строки поднимают ждущих соседей, стенд "
                "смотрит на журнал подъёмов\n")

# Ожидание подъёма головы: обход ждущих отдаёт лестницу оболочке, а та пишет
# свою строку в журнал подъёмов уже своим ходом. Строка, оставшаяся стоять,
# ловится не сроком, а секцией доски. Обход синхронный, и к возврату команды
# она либо вышла в работу, либо нет.
RAISE_WAIT = 60
# Ожидание тишины: головы стенда кончают работу сами, и уборка идёт после них.
QUIET_WAIT = 120

CLIENT_BODY = '''
import os
import subprocess
import sys


def mode():
    """Режим стенда, положенный рядом файлом. Без файла голова только
    отмечается. Обход ждущих и тик поднимают её тем же способом."""
    try:
        with open(MODE, encoding="utf-8") as f:
            return f.read().strip() or RECORD
    except OSError:
        return RECORD


def note(line):
    with open(LOG, "a", encoding="utf-8") as f:
        f.write(line + "\\n")


def seen(word):
    """Сколько проходов голова отработала в этом режиме, считая текущий."""
    try:
        with open(LOG, encoding="utf-8") as f:
            return len([ln for ln in f if ln.startswith(word + "\\t")])
    except OSError:
        return 0


def main():
    # Заказ прохода идёт последним словом командной строки клиента, и ID
    # задачи стоит в нём: по нему стенд узнаёт, чью голову подняли.
    said = sys.argv[-1]
    m = mode()
    note(m + "\\t" + said)
    if m != WAIT:
        return 0
    if seen(WAIT) <= WAITS:
        # Ход кончился машинным ожиданием. Отметку ставит сама утилита. Копия
        # записи разошлась бы с нею на первой правке.
        subprocess.run(MARK)
        return 0
    # Проходы ожидания кончились, и голова паркует свою строку. Оболочка встаёт
    # на запаркованной задаче штатным нулём, и стенду видно, что воронка её не
    # сняла.
    subprocess.run(["taskctl", "-C", PROJ, "move", TASK, "blocked",
                    "--reason", WHY])
    return 0


sys.exit(main())
'''

TMUX_BODY = '''
import sys

with open(LOG, "a", encoding="utf-8") as f:
    f.write(" ".join(sys.argv[1:]) + "\\n")
# Стенду tmux-сервер машины ни к чему. Отказ на любой команде уводит лестницу
# носителей с живого окна на headless, и проверяется она целиком.
sys.exit(1)
'''


def wait_cmd(proj):
    """Команда, которой голова отмечает машинное ожидание. Отметку ставит сама
    утилита, и звено это в стенде одно. Тест откатывает его подменой команды, и
    голова без отметки уходит в воронку."""
    return ["agentctl", "wait", FUNNEL, "срок", "--until", WAIT_UNTIL,
            "--note", "стенд ждёт соседа", "-C", str(proj)]


def write_stub(path, head, body):
    """Подставная программа: шапка с путями стенда и общее тело."""
    path.write_text("#!/usr/bin/env python3\n" + head + body, encoding="utf-8")
    path.chmod(0o755)


def stubs(bin_dir, log, mode_file, proj, heads_log):
    """Клиент харнеса и tmux в каталоге стенда.

    Пути зашиваются в сами программы, а не едут окружением. Голову поднимают
    утилиты через свои подпроцессы, и пара, поставленная стендом, до неё
    доходит не всегда.
    """
    bin_dir.mkdir(parents=True, exist_ok=True)
    head = ("LOG = %r\nMODE = %r\nPROJ = %r\nTASK = %r\nWAITS = %d\n"
            "MARK = %r\nWHY = %r\nWAIT = %r\nRECORD = %r\n"
            % (str(heads_log), str(mode_file), str(proj), FUNNEL, WAIT_PASSES,
               wait_cmd(proj), PARK_WHY, MODE_WAIT, MODE_RECORD))
    write_stub(bin_dir / "claude", head, CLIENT_BODY)
    write_stub(bin_dir / "tmux", "LOG = %r\n" % str(log), TMUX_BODY)


def deploy_stub(root):
    """deploy.local временного проекта: подставные тесты и выкат, autonomous.

    Без autonomous слияние не довело бы строку до Check, а обход ждущих стоит
    в конце слияния, и проверять было бы нечего.
    """
    dep = Path(root) / ".devkit" / "deploy.local"
    dep.parent.mkdir(parents=True, exist_ok=True)
    dep.write_text("deploy = %s\ntest = %s\nautonomous = true\n"
                   % (DEPLOY_CMD, TEST_CMD), encoding="utf-8")
    return dep


def raises(heads_log):
    """Заказы, дошедшие до подставного клиента: режим и текст заказа."""
    try:
        with open(str(heads_log), encoding="utf-8") as f:
            return [ln.rstrip("\n") for ln in f if ln.strip()]
    except OSError:
        return []


def raised(heads_log, task):
    """Поднималась ли голова задачи: ID стоит в тексте заказа."""
    return any(task in ln for ln in raises(heads_log))


def await_raise(heads_log, task, limit=RAISE_WAIT):
    """Ждать подъёма головы. Обход ждущих отдаёт лестницу оболочке, и своя
    строка в журнале подъёмов появляется у неё уже следующим ходом."""
    until = time.time() + limit
    while time.time() < until:
        if raised(heads_log, task):
            return True
        time.sleep(0.2)
    return raised(heads_log, task)


def locks(home):
    """Замки голов, ещё держащих задачи. Замок это директория с pid оболочки,
    и по нему видно, что голова стенда не закончила."""
    d = Path(home) / ".devkit"
    try:
        return sorted(p.name for p in d.glob("task-*.lock"))
    except OSError:
        return []


def quiet(home, limit=QUIET_WAIT):
    """Ждать, пока головы стенда закончат. Уборка снесла бы проект из-под живой
    оболочки, и её строки посыпались бы в вывод уже после отчёта."""
    until = time.time() + limit
    while time.time() < until and locks(home):
        time.sleep(0.2)
    return locks(home)


def woke(cmd_run, proj, home, heads_log, task):
    """Подъём строки: вышла в работу, получила голову и дождалась её конца.
    Возврат это пустая строка при удавшемся подъёме либо слова о том, чего не
    вышло.

    Секция спрашивается первой и сразу. Обход ждущих синхронный, и строка,
    оставшаяся стоять, видна по доске, не дожидаясь срока. Голова приходит
    своим ходом, стенд её ждёт.

    Конца головы стенд ждёт по той же причине, по какой ждёт её появления.
    Голова живёт своими проходами и на каждом спрашивает доску командой
    `taskctl show`, а та меряет возраст строки через `git status` (age.go).
    Второй git в том же репозитории берёт .git/index.lock, и следующий коммит
    стенда падает с «Another git process seems to be running». Замок головы
    лежит в ~/.devkit/task-<ID>.lock и снимается её выходом, по нему стенд и
    ждёт.
    """
    sect = board_sect(cmd_run, proj, task)
    if sect != "in-progress":
        return "строка осталась в %s" % (sect or "прежней секции")
    if not await_raise(heads_log, task):
        return "строка вышла в работу, а голова так и не поднялась"
    held = quiet(home)
    if held:
        return "голова поднялась и не закончила за %d с (%s)" % (QUIET_WAIT, ", ".join(held))
    return ""


def board_sect(cmd_run, proj, task):
    """Секция строки словами `taskctl show`."""
    rc, out = cmd_run(["taskctl", "-C", str(proj), "show", task])
    if rc != 0:
        return ""
    m = re.search(r"^%s\s+в\s+(\S+)" % re.escape(task), out, re.M)
    return m.group(1) if m else ""


def project(cmd_run, proj, log):
    """Временный проект: подключение, игноры, origin, подставной выкат.

    Голый репозиторий под origin нужен тем же, чем в selfcheck. Autonomous
    поднимает пуш при переводе доски и при слиянии, и без origin стенд умирал
    бы на первом же движении строки.
    """
    rc, out = cmd_run(["devkitctl", "new", "--prefix", PREFIX, "-C", str(proj)])
    if rc != 0:
        return rc, out
    cmd_run(["git", "-C", str(proj), "add", "--", ".gitignore"])
    cmd_run(["git", "-C", str(proj), "commit", "-q", "-m",
             "chore: %s игноры машинных файлов devkit" % FUNNEL])
    selfcheck.bare_origin(lambda args, cwd=None: cmd_run(args, cwd), proj)
    deploy_stub(proj)
    log("  проект: ок")
    return 0, out


def add_row(cmd_run, proj, task, title, status=""):
    """Строка на доске одной командой, с коммитом доски вместе с файлом задачи."""
    args = ["taskctl", "-C", str(proj), "add", "--title", title, "--type", "task",
            "--rank", "25+5+1+0+0", "--cost", "S", "--accept", "agent",
            "--id", task, "-m", "docs(tasks): %s строка заведена" % task]
    if status:
        args += ["--status", status]
    return cmd_run(args)


def funnel_wait(cmd_run, proj, devkit, mode_file, client, heads_log):
    """Ожидание первое: голова с живым ожиданием переживает воронку оболочки.

    Оболочка конвейера зовётся напрямую с подставным клиентом. Клиент кончает
    ход отметкой `agentctl wait` столько раз подряд, сколько воронке хватило бы
    с запасом. Последним проходом он паркует строку, и оболочка встаёт на этом
    штатным нулём. Провал тут это стоп воронки. Она сняла бы голову вместе с
    работой, как снимала до DK-899.
    """
    mode_file.write_text(MODE_WAIT, encoding="utf-8")
    rc, out = add_row(cmd_run, proj, FUNNEL, "голова ждёт машинного события",
                      status="in-progress")
    if rc != 0:
        return step("строка ожидания не завелась", rc, out)
    runner = Path(devkit) / "kit" / "skills" / "board-task" / "task-run.py"
    rc, out = cmd_run(["python3", str(runner), FUNNEL, "-C", str(proj),
                       "--project", "waitcheck", "--", str(client)])
    passes = len([ln for ln in raises(heads_log) if ln.startswith(MODE_WAIT + "\t")])
    held = len(re.findall(r"проход \d+ ждёт:", out))
    if rc != 0:
        return step("оболочка вышла кодом %d вместо нуля: воронка сняла голову "
                    "с живым ожиданием (проходов %d, ожиданий %d)"
                    % (rc, passes, held), rc, out)
    if passes <= WAIT_PASSES:
        return step("проходов всего %d при %d ожиданиях: оболочка встала раньше "
                    "срока" % (passes, WAIT_PASSES), 1, out)
    if held < WAIT_PASSES:
        return step("оболочка насчитала %d ожиданий из %d: отметка `agentctl "
                    "wait` до неё не дошла" % (held, WAIT_PASSES), 1, out)
    return step("проходов %d, из них ожиданий %d, оболочка вышла нулём"
                % (passes, held), 0, out)


def step(said, rc, out):
    """Исход ожидания: слова, код и вывод для разбора провала."""
    return {"said": said, "rc": rc, "out": out}


def task_work(cmd_run, task, tree):
    """Работа задачи в её дереве: файл, коммит, сценарий, обкатка, уровень ревью.

    Слияние пускает ветку только со сценарием в файле задачи и записью ревью, а
    ворота перевода в Check спрашивают отметку обкатки. Стенд проходит их тем
    же порядком, каким их проходит человек.
    """
    runner = lambda args, cwd=None: cmd_run(args, cwd)
    work = Path(tree) / "src" / (task.lower() + ".txt")
    work.parent.mkdir(parents=True, exist_ok=True)
    work.write_text("работа %s\n" % task, encoding="utf-8")
    # Тест рядом с правкой: ворота слияния спрашивают его у всякой ветки с
    # кодом, и синтетическая задача проходит их наравне с настоящей.
    test = Path(tree) / "src" / (task.lower().replace("-", "_") + "_test.py")
    test.write_text("def test_работа():\n    assert True\n", encoding="utf-8")
    cmd_run(["git", "-C", str(tree), "add", "--", "src"])
    rc, out = cmd_run(["git", "-C", str(tree), "commit", "-q", "-m",
                       "feat: %s работа задачи" % task])
    if rc != 0:
        return rc, out
    rc, out = selfcheck.write_scenario(runner, tree, task, about=SCENARIO_WORDS % task,
                                       verify=VERIFY_WORDS)
    if rc != 0:
        return rc, out
    rc, out = selfcheck.rehearse_scenario(runner, tree, task)
    if rc != 0:
        return rc, out
    return cmd_run(["taskctl", "-C", str(tree), "review", "level", task, "0",
                    "синтетическая задача стенда ожиданий, критичности нет",
                    "-m", "docs(tasks): %s уровень ревью проставлен стендом" % task])


def arm_row(cmd_run, proj):
    """Взвод строки Backlog: согласие человека на самостоятельный старт. Звено
    это в стенде одно, и тест откатывает его подменой функции. Невзведённая
    строка слияния не дождётся."""
    return cmd_run(["taskctl", "-C", str(proj), "arm", ARMED,
                    "-m", "docs(tasks): %s взвод строки" % ARMED])


def event_wait(cmd_run, proj, home, mode_file, heads_log):
    """Ожидание второе: `shipctl merge` и `taskctl close` поднимают ждущих.

    Строка PARKED стоит в Blocked с машинным разрядом «закрытие: <ID>»,
    строка ARMED лежит в Backlog со взводом и ребром «после <ID>». Ребро
    снимается слиянием кода (DK-943), разряд закрытия строкой в архиве, и
    события эти делают разные команды: сперва merge, следом close. Провал тут
    это строка, оставшаяся стоять после своего события.
    """
    mode_file.write_text(MODE_RECORD, encoding="utf-8")
    rc, out = add_row(cmd_run, proj, DEP, "предпосылка соседей")
    if rc != 0:
        return step("строка предпосылки не завелась", rc, out)
    rc, out = add_row(cmd_run, proj, PARKED, "ждёт закрытия предпосылки",
                      status="in-progress")
    if rc != 0:
        return step("строка ждущего не завелась", rc, out)
    rc, out = add_row(cmd_run, proj, ARMED, "стартует по слиянию предпосылки")
    if rc != 0:
        return step("строка взвода не завелась", rc, out)
    rc, out = cmd_run(["taskctl", "-C", str(proj), "move", PARKED, "blocked",
                       "--reason", "закрытие: %s ждём архива предпосылки" % DEP,
                       "-m", "docs(tasks): %s парковка ожиданием закрытия" % PARKED])
    if rc != 0:
        return step("строка %s не запарковалась" % PARKED, rc, out)
    rc, out = cmd_run(["taskctl", "-C", str(proj), "dep", "add", ARMED, DEP])
    if rc != 0:
        return step("ребро «после %s» не легло" % DEP, rc, out)
    rc, out = arm_row(cmd_run, proj)
    if rc != 0:
        return step("взвод строки %s не встал" % ARMED, rc, out)
    rc, out = cmd_run(["shipctl", "-C", str(proj), "start", DEP])
    if rc != 0:
        return step("ветка предпосылки не завелась", rc, out)
    tree = selfcheck.task_tree(proj, DEP)
    rc, out = task_work(cmd_run, DEP, tree)
    if rc != 0:
        return step("работа предпосылки не доехала до ворот слияния", rc, out)
    rc, merge_out = cmd_run(["shipctl", "-C", str(proj), "merge", DEP])
    if rc != 0:
        return step("слияние предпосылки не прошло", rc, merge_out)
    why = woke(cmd_run, proj, home, heads_log, ARMED)
    if why:
        return step("%s не стартовала по слиянию %s: %s" % (ARMED, DEP, why), 1, merge_out)
    rc, close_out = cmd_run(["taskctl", "-C", str(proj), "close", DEP,
                             "-m", "docs(tasks): %s предпосылка закрыта" % DEP,
                             "--push"])
    if rc != 0:
        return step("закрытие предпосылки не прошло", rc, close_out)
    why = woke(cmd_run, proj, home, heads_log, PARKED)
    if why:
        return step("%s не разбужена закрытием %s: %s" % (PARKED, DEP, why), 1, close_out)
    return step("слияние %s подняло %s, закрытие подняло %s"
                % (DEP, ARMED, PARKED), 0, merge_out + "\n" + close_out)


def hand_merge(cmd_run, proj, task):
    """Слияние работы задачи руками, мимо утилит devkit.

    Так событие и проезжает мимо обхода. Человек слил ветку сам, и ни `merge`,
    ни `close` про это не знали. Признак «слита» определяется по коммиту с ID
    первым в subject, поэтому работа едет отдельным коммитом в своей ветке.
    """
    branch = task.lower()
    rc, out = cmd_run(["git", "-C", str(proj), "checkout", "-q", "-b", branch])
    if rc != 0:
        return rc, out
    work = Path(proj) / "src" / (branch + ".txt")
    work.parent.mkdir(parents=True, exist_ok=True)
    work.write_text("работа %s руками\n" % task, encoding="utf-8")
    cmd_run(["git", "-C", str(proj), "add", "--", "src"])
    rc, out = cmd_run(["git", "-C", str(proj), "commit", "-q", "-m",
                       "feat: %s работа слита руками" % task])
    if rc != 0:
        return rc, out
    rc, out = cmd_run(["git", "-C", str(proj), "checkout", "-q", "main"])
    if rc != 0:
        return rc, out
    # В origin слияние не уезжает. Рубеж пуша спрашивает у кода след ревью, а
    # ревью синтетической ветки тут не при чём. До origin работу сам довезёт
    # обход тика, он пушит доску своим разрешением.
    return cmd_run(["git", "-C", str(proj), "merge", "-q", "--no-ff", "-m",
                    "chore: %s ветка слита руками" % task, branch])


def tick_wait(cmd_run, proj, home, mode_file, heads_log):
    """Ожидание третье: тик сторожка добирает пропущенное событие.

    Строка MISSED ждёт слияния соседа, а соседа сливают руками. Ни `merge`, ни
    `close` при этом не звучат, и обход ждущих в конце них не случается вовсе.
    Тик зовётся напрямую своей функцией обхода. Прогон демоном поднял бы съём
    квоты и полез бы в службы машины, а стенд обязан её не трогать. Что тик
    доходит до такого корня, видно по реестру этапов, и стенд спрашивает его
    тем же способом, каким его читает обход тика.
    """
    mode_file.write_text(MODE_RECORD, encoding="utf-8")
    # Предпосылка берётся в работу: рубеж пуша пропускает код только со строкой
    # вне Backlog, и синтетическая ветка проходит его тем же порядком.
    rc, out = add_row(cmd_run, proj, HAND, "предпосылка, слитая руками",
                      status="in-progress")
    if rc != 0:
        return step("строка предпосылки тика не завелась", rc, out)
    rc, out = add_row(cmd_run, proj, MISSED, "ждёт слияния, прошедшего мимо утилит",
                      status="in-progress")
    if rc != 0:
        return step("строка ждущего тика не завелась", rc, out)
    rc, out = cmd_run(["taskctl", "-C", str(proj), "move", MISSED, "blocked",
                       "--reason", "слияние: %s ждём кода предпосылки" % HAND,
                       "-m", "docs(tasks): %s парковка ожиданием слияния" % MISSED])
    if rc != 0:
        return step("строка %s не запарковалась" % MISSED, rc, out)
    # Запись этапа заводит корень в реестре ~/.devkit/runs, по которому тик и
    # находит доски задач вне цикла цели.
    rc, out = cmd_run(["agentctl", "-C", str(proj), "stage", MISSED, "разработка"])
    if rc != 0:
        return step("запись этапа не легла: тику не по чему найти корень", rc, out)
    rc, out = hand_merge(cmd_run, proj, HAND)
    if rc != 0:
        return step("слияние руками не прошло", rc, out)
    if raised(heads_log, MISSED):
        return step("%s поднялась до тика: событие кто-то обошёл раньше" % MISSED,
                    1, out)
    roots = [os.path.realpath(r) for r in watch.run_roots(str(home))]
    if os.path.realpath(str(proj)) not in roots:
        return step("корень %s не виден тику: реестр этапов называет %s"
                    % (proj, ", ".join(roots) or "ничего"), 1, out)
    lines = watch.waiters(str(proj), call=tick_call(cmd_run), taskctl=which(cmd_run, "taskctl"))
    said = "\n".join(lines)
    why = woke(cmd_run, proj, home, heads_log, MISSED)
    if why:
        return step("%s не поднята тиком: пропущенное событие так и не добрано (%s)"
                    % (MISSED, why), 1, said)
    return step("тик добрал слияние %s руками и поднял %s" % (HAND, MISSED), 0, said)


def which(cmd_run, name):
    """Путь утилиты в том PATH, которым работает стенд. Обходу тика бинарь
    называется явно. У самого стенда окружение другое, чем у его подпроцессов."""
    rc, out = cmd_run(["python3", "-c",
                       "import shutil, sys; sys.stdout.write(shutil.which(%r) or '')" % name])
    path = out.strip().split("\n")[-1].strip() if rc == 0 else ""
    return path or None


def tick_call(cmd_run):
    """Прогонщик команд для обхода тика: тик вызывает subprocess.run, а стенду
    нужен его PATH и его дом."""
    def call(args, **kw):
        rc, out = cmd_run([str(a) for a in args])
        return subprocess.CompletedProcess(args, rc, out, "")
    return call


def circle(cmd_run, proj, home, devkit, mode_file, client, heads_log, log):
    """Три ожидания подряд. Провал ожидания круг не останавливает: три исхода
    это три разных механизма, и знать про все три полезнее, чем про первый."""
    steps = []
    rc, out = project(cmd_run, proj, log)
    if rc != 0:
        steps.append({"name": "проект", "said": "временный проект не завёлся",
                      "rc": rc, "out": out})
        return steps
    waits = (
        ("живое ожидание против воронки",
         lambda: funnel_wait(cmd_run, proj, devkit, mode_file, client, heads_log)),
        ("close и merge поднимают ждущих",
         lambda: event_wait(cmd_run, proj, home, mode_file, heads_log)),
        ("тик добирает пропущенное",
         lambda: tick_wait(cmd_run, proj, home, mode_file, heads_log)),
    )
    for name, go in waits:
        got = go()
        got["name"] = name
        steps.append(got)
        log("  ожидание «%s»: %s (%s)"
            % (name, "ок" if got["rc"] == 0 else "не прошло", got["said"]))
    return steps


def main(argv=None, cmd_run=None, where=None, log=print, path=None, devkit=None):
    """Прогон стенда с отчётом по ожиданиям. Отдаёт код: 0 все три прошли, 1 нет.

    cmd_run, where, path и devkit вынесены параметрами для тестов: стенд в
    тестах гоняется по подставному PATH с собранными бинарями и по копии
    devkit, а не по PATH и дереву машины.
    """
    devkit = Path(devkit) if devkit else DEVKIT
    if where is None:
        where = Path(tempfile.mkdtemp(prefix="devkit-waitcheck-"))
        mine = True
    else:
        where = Path(where)
        mine = False
    proj = where / "proj"
    home = where / "home"
    home.mkdir(parents=True, exist_ok=True)
    bin_dir = where / "bin"
    mode_file = where / "режим"
    heads_log = where / "головы"
    tmux_log = where / "tmux"
    stubs(bin_dir, tmux_log, mode_file, proj, heads_log)
    mode_file.write_text(MODE_RECORD, encoding="utf-8")
    heads_log.write_text("", encoding="utf-8")
    env = dict(STAND_ENV)
    env["DEVKIT_HOME"] = str(devkit)
    # Дом стенда свой и называется он явно: замок головы, реестр этапов и
    # отметки ожидания лежат в ~/.devkit, и стенд смотрит туда же, куда пишут
    # утилиты. Автор коммитов задаётся парами окружения: в подменном доме
    # ~/.gitconfig нет, а git без имени коммитить отказывается.
    env["HOME"] = str(home)
    env.update(selfcheck.GIT_ENV)
    # Подставные программы стоят впереди всего: клиента харнеса и tmux голова
    # ищет в PATH, и настоящие тут взялись бы за работу по-настоящему. Боевой
    # прогон дописывает к ним PATH машины, тесты свой.
    env["PATH"] = str(bin_dir) + os.pathsep + (path or os.environ.get("PATH", ""))
    if cmd_run is None:
        def cmd_run(args, cwd=None, extra=None):
            e = dict(env)
            e.update(extra or {})
            return selfcheck.run(args, cwd=cwd, env=e)
    else:
        given = cmd_run

        def cmd_run(args, cwd=None, extra=None):
            e = dict(env)
            e.update(extra or {})
            return given(args, cwd=cwd, env=e)
    log("стенд трёх ожиданий: временный проект в %s" % proj)
    try:
        steps = circle(cmd_run, proj, home, devkit, mode_file, bin_dir / "claude",
                       heads_log, log)
        held = quiet(home)
        if held:
            log("головы стенда не закончили: %s" % ", ".join(held))
        failed = [s for s in steps if s["rc"] != 0]
        if not failed:
            log("три ожидания прошли: конвейер снимается с каждого")
            return 0
        for s in failed:
            log("ожидание «%s» не прошло: %s" % (s["name"], s["said"]))
            for ln in selfcheck.tail(s["out"]).split("\n"):
                log("  %s" % ln)
        return 1
    finally:
        if mine:
            shutil.rmtree(str(where), ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
