"""Живой круг связки devkit одной командой (DK-158).

Доктор сверяет раскладку: файлы на месте, бинари одной версии с чекаутом,
команды в конфиге. Чего он не меряет, так это работает ли связка в движении:
заводится ли строка на доске, выдаётся ли вердикт pick, рождается ли ветка
задачи в своём дереве, проходит ли слияние с тестами и выкатом. Этот круг
команда гоняет сама, во временном проекте, и за собой убирает.

Отличие от тестов devkit: тесты гоняются на стенде из исходников до установки,
selfcheck зовётся после неё, поверх установленных бинарей. Проверяется не код,
а собранное и разложенное: тот PATH, что достанется человеку, и те утилиты,
что стоят на машине. Поэтому все утилиты зовутся по имени из PATH, а не путём
в чекаут devkit. Своего HOME у круга нет: утилиты devkit пишут в дом и без
ведома вызвавшего (taskctl ведёт записи ~/.devkit/runs, уведомитель пишет
~/.devkit/notify.log), поэтому сабпроцессы круга получают вместо настоящего
дома свой каталог, а автор коммитов задаётся переменными окружения
GIT_AUTHOR_* и GIT_COMMITTER_*, а не записью в ~/.gitconfig.

Временный проект устраивается как живой: рядом с ним заводится голый
репозиторий под origin (autonomous поднимает пуш в start и merge, и без
origin круг падал бы на первом же переводе доски), а в файл задачи пишется
сценарий проверки, без которого ворота слияния задачу не пускают. Выкат
подставной: команда пишет метку в свой каталог, сработает и в CI, и на
ноутбуке, и не тронет ничего снаружи.

Находки доктора по машине не провалят круг: доктор здесь отдельный шаг, его
отчёт печатается, а судится сам шаг (код 2 это ошибка запуска, дальше круг не
пойдёт; код 1 с находками это живой доктор, назвавший проблемы раскладки, им
посвящена отдельная строка в конце отчёта).
"""
import contextlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEVKIT = HERE.parent.parent

PREFIX = "SC"
TASK = "SC-001"
# Выкат и тесты временного проекта подставные, обе команды на самом python3
# без внешних утилит: слияние гоняет их через sh -c в том же PATH, и
# минимальному окружению вроде CI-контейнера незачем знать touch(1) и true(1).
DEPLOY_MARKER = ".devkit/selfcheck-deployed"
DEPLOY_CMD = "python3 -c \"open('%s','w').close()\"" % DEPLOY_MARKER
TEST_CMD = "python3 -c pass"
GIT_ENV = {
    "GIT_AUTHOR_NAME": "selfcheck",
    "GIT_AUTHOR_EMAIL": "selfcheck@devkit.local",
    "GIT_COMMITTER_NAME": "selfcheck",
    "GIT_COMMITTER_EMAIL": "selfcheck@devkit.local",
}
# Хвост окружения конвейера задачи (DK-1694): круг зовут и из живой сессии,
# окружение родителя несёт её запись запуска, голову и замок. Синтетический
# круг заводит собственные, а чужой DEVKIT_TASK_RUN_ID заставлял стендовый
# task-run искать чужую запись восстановления и снимать голову кодом 2.
PIPE_ENV_DROP = (
    "DEVKIT_TASK",
    "DEVKIT_RUN_DEPTH",
    "DEVKIT_TASK_LOCK_FROM",
    "DEVKIT_TASK_HEAD_SESSION",
    "DEVKIT_TASK_HEAD_SESSION_ARGS",
    "DEVKIT_TASK_RUN_ID",
    "DEVKIT_TASK_RUN_MODEL",
    "DEVKIT_RECOVERY_ATTEMPT",
)
SCENARIO_HEADER = "## Сценарий проверки"
VERIFY_HEADER = "## Проверка"
TIMEOUT = 900

# Единственная предпосылка, которую waitcheck сливает через shipctl.
WAIT_MARKER = "src/wt-002.txt"
WAIT_TEST = "src/wt_002_test.py"
WAIT_ASSERTION = 'assert Path("src/wt-002.txt").read_text(encoding="utf-8") == "работа WT-002\\n"'
WAIT_TEST_BODY = "from pathlib import Path\n\n" + WAIT_ASSERTION + "\n"

SELF_SCENARIO = ("1. devkitctl selfcheck во временном каталоге.\n"
                 "2. Каждый шаг круга отвечает «ок», итог «связка жива».\n\n"
                 "Шаг, выполнимый без выката, лежит блоком: его гоняет обкатка\n"
                 "круга, и ворота перевода в Check спрашивают её отметку.\n\n"
                 "```sh\ntest -f docs/TASKS.md && grep -q %s docs/TASKS.md\n```\n\n"
                 "Ожидаемый итог: круг закрыл временную задачу и не оставил "
                 "после себя ни проекта, ни дерева задачи.\n")
SELF_VERIFY = ("выкат круга оставляет метку %s, "
               "круг проверяет её после слияния\n" % DEPLOY_MARKER)

WAIT_SCENARIO = ("1. devkitctl waitcheck во временном каталоге.\n"
                 "2. Каждое из трёх ожиданий отвечает «ок», итог «три ожидания "
                 "прошли».\n\n"
                 "Шаг, выполнимый без выката, лежит блоком: его гоняет обкатка,\n"
                 "и ворота перевода в Check спрашивают её отметку.\n\n"
                 "```sh\ntest -f docs/TASKS.md && grep -q %s docs/TASKS.md\n```\n\n"
                 "Ожидаемый итог: слияние этой строки поднимает взведённого "
                 "соседа, а закрытие припаркованного.\n")
WAIT_VERIFY = ("слияние и закрытие этой строки поднимают ждущих соседей, стенд "
               "смотрит на журнал подъёмов\n")

FIXTURE_SECTIONS = {
    TASK: (SELF_SCENARIO, SELF_VERIFY),
    "WT-002": (WAIT_SCENARIO, WAIT_VERIFY),
}


# У временных проектов нет собственных правил. Для нового общего или
# проектного ID сначала требуется разобрать его на этих двух фикстурах.
FIXTURE_RULE_IDS = {
    "%s-%03d" % (prefix, number)
    for prefix, last in (("common-architecture:ARCH", 28), ("common-style:STYLE", 16),
                         ("common-tests:TEST", 9))
    for number in range(1, last + 1)
}


@contextlib.contextmanager
def task_description(situation, desired, dod):
    body = """## Что происходит

%s

## Чего хотим

%s

## DoD

- %s

## Ранг

25+5+1+0+0 = 31, P2.

- Серьёзность 25: отказ проверяемой связки мешает вести работу через devkit.
- Ценность 5: сценарий проверяет повседневные переходы задачи.
- Неопределённость 1: последовательность шагов задана стендом.
- Поправка на баг 0: это синтетическая задача проверки, дефект не заявлен.
- Рычаг 0: дополнительных изменений за пределами стенда нет.
""" % (situation, desired, dod)
    with tempfile.NamedTemporaryFile(mode="w", suffix=".md", encoding="utf-8") as body_file:
        body_file.write(body)
        body_file.flush()
        yield body_file.name


def run(args, cwd=None, env=None, timeout=TIMEOUT, home=None):
    """Прогон команды в окружении круга. Отдаёт код и слитый вывод.

    home подставляет сабпроцессам вместо настоящего дома временный каталог:
    утилиты devkit пишут в ~/.devkit и без ведома вызвавшего (taskctl ведёт
    записи runs, уведомитель журнал notify.log), и без подмены круг оставлял
    бы след в машине, которую проверяет. Без home наследуется окружение
    родителя: так круг зовётся из тестов со своим стендовым домом.
    """
    e = dict(os.environ)
    for name in PIPE_ENV_DROP:
        e.pop(name, None)
    e.update(GIT_ENV)
    if home is not None:
        e["HOME"] = str(home)
    if env:
        e.update(env)
    p = subprocess.run([str(a) for a in args], cwd=cwd and str(cwd), env=e,
                       stdin=subprocess.DEVNULL, timeout=timeout,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    return p.returncode, p.stdout.decode("utf-8", "replace").strip()


def tail(text, limit=15):
    """Вывод шага для отчёта: последние строки, а не весь поток."""
    lines = [ln for ln in text.split("\n") if ln.strip()]
    return "\n".join(lines[-limit:])


def leftover_branches(proj, cmd_run=None):
    """Ветки задачи, оставшиеся в репозитории проекта после круга.

    Слияние уносит ветку задачи с собой (merge удаляет её), и оставшаяся
    ветка это след за кругом. Основная ветка следом не считается: она была у
    проекта до круга. Слепок снимается до сноса проекта, снесённый
    репозиторий уже ничего не расскажет.
    """
    runner = cmd_run or run
    rc, out = runner(["git", "-C", str(proj), "for-each-ref",
                      "--format=%(refname:short)", "refs/heads"])
    if rc != 0:
        return []
    branches = [ln.strip() for ln in out.split("\n") if ln.strip()]
    rc, out = runner(["git", "-C", str(proj), "symbolic-ref", "--short", "HEAD"])
    trunk = out.strip().split("\n")[-1].strip() if rc == 0 and out.strip() else ""
    return [b for b in branches if b != trunk]


def deploy_stub(root):
    """deploy.local временного проекта: подставные test и deploy.

    Болванку, которую положил new, круг заполняет сам: команда выката пишет
    метку в свой каталог, тесты пустой командой, autonomous поднят, чтобы
    слияние прошло с выкатом и перевело задачу в Check.
    """
    dep = Path(root) / ".devkit" / "deploy.local"
    dep.parent.mkdir(parents=True, exist_ok=True)
    dep.write_text("deploy = %s\ntest = %s\nautonomous = true\n"
                   % (DEPLOY_CMD, TEST_CMD), encoding="utf-8")
    return dep


def commit_ignores(cmd_run, proj):
    """Коммит .gitignore подключения в синтетическом проекте.

    Подключение кладёт файл, но не коммитит: чужие файлы проекта в первый
    коммит не едут. Настоящий проект коммитит его сам, а круг до DK-643 жил и
    без этого. С обкаткой сценария в дереве задачи заводится журнал запусков
    .devkit/log, и без унаследованных игноров он лежит там untracked: слияние
    после этого не убирает дерево задачи, и круг оставляет за собой след.
    """
    cmd_run(["git", "-C", str(proj), "add", "--", ".gitignore"])
    # ID задачи в subject спрашивает хук commit-msg на проекте с доской, и круг
    # зовёт хуки настоящие: коммит без ID он отбил бы.
    return cmd_run(["git", "-C", str(proj), "commit", "-q", "-m",
                    "chore: %s игноры машинных файлов devkit" % TASK])


def bare_origin(cmd_run, proj):
    """Голый репозиторий под origin и первое выталкивание в него.

    autonomous=true превращает пуш в часть конвейера: taskctl move при start
    и слияние при merge пушат сами, и без origin круг умирал бы на переводе
    доски с «No configured push destination». Ветка проекта спрашивается у
    самого git: имя по умолчанию зависит от настроек машины.
    """
    origin = Path(proj).parent / "origin.git"
    cmd_run(["git", "init", "-q", "--bare", str(origin)])
    cmd_run(["git", "-C", str(proj), "remote", "add", "origin", str(origin)])
    rc, out = cmd_run(["git", "-C", str(proj), "symbolic-ref", "--short", "HEAD"])
    branch = out.strip().split("\n")[-1].strip() if rc == 0 and out.strip() else "main"
    cmd_run(["git", "-C", str(proj), "push", "-q", "-u", "origin", branch])
    # Ветка задачи рождается от main в проекте, и перевод доски при start
    # пушит её без upstream: без отслеживания origin/<ветка> push падает с
    # «no upstream branch». Стартовое выталкивание задаёт связку.
    cmd_run(["git", "-C", str(proj), "config", "push.autoSetupRemote", "true"])
    return origin


def task_tree(proj, task):
    """Дерево задачи по правилу shipctl treePath: сиблинг ../<проект>-<id>."""
    return Path("%s-%s" % (proj, task.lower()))


def write_scenario(cmd_run, tree, task):
    """Сценарий проверки и раздел «Проверка» в файл задачи, коммитом в ветку.

    Ворота слияния пускают ветку только со сценарием в файле задачи, а ворота
    закрытия требуют у агентского вида непустой раздел «Проверка»: круг
    дописывает оба в дереве задачи, и слияние привозит их в main. Файл
    пишется тем же заголовком, каким его заводит taskctl file.

    Слова сценария и проверки берёт своими и соседний стенд ожиданий (DK-936):
    ворота у них одни, а рассказывать о себе каждый обязан сам.
    """
    doc = Path(tree) / "docs" / "tasks" / ("%s.md" % task)
    doc.parent.mkdir(parents=True, exist_ok=True)
    text = doc.read_text(encoding="utf-8") if doc.exists() else ""
    if not text.strip():
        text = "# %s: проверка связки\n" % task
    about, verify = FIXTURE_SECTIONS[task]
    if not re.search(r"^%s\b" % re.escape(SCENARIO_HEADER), text, flags=re.M):
        text += "\n%s\n\n%s" % (SCENARIO_HEADER, about % task)
    if not re.search(r"^%s\b" % re.escape(VERIFY_HEADER), text, flags=re.M):
        text += "\n%s\n\n%s" % (VERIFY_HEADER, verify)
    doc.write_text(text, encoding="utf-8")
    cmd_run(["git", "-C", str(tree), "add", "--", "docs/tasks/%s.md" % task])
    return cmd_run(["git", "-C", str(tree), "commit", "-q", "-m",
                    "docs(tasks): %s сценарий проверки" % task])


def rehearse_scenario(cmd_run, tree, task):
    """Обкатка сценария в дереве задачи и коммит её записи.

    Ворота перевода в Check спрашивают у агентского вида отметку обкатки
    (DK-643), и круг проходит их тем же способом, каким их проходит человек:
    живым прогоном шага в свежем дереве. Пометка-исключение закрыла бы шаг
    молчанием, а круг на то и заведён, чтобы звать утилиты по-настоящему.
    Запись прогона уезжает своим коммитом: отметку такой коммит не отменяет,
    он трогает один файл задачи.
    """
    rc, out = cmd_run(["taskctl", "-C", str(tree), "rehearse", task])
    if rc != 0:
        return rc, out
    cmd_run(["git", "-C", str(tree), "add", "--", "docs/tasks/%s.md" % task])
    return cmd_run(["git", "-C", str(tree), "commit", "-q", "-m",
                    "docs(tasks): %s обкатка сценария" % task])


def audit_report(cmd_run, tree, task, standards):
    """Отчёт аудита ревью синтетической задачи: снимок утилитой, выводы стендом.

    Ворота слияния требуют у ветки задачи не только строку уровня, но и полный
    отчёт аудита ревью (DK-1675): строка говорит, что ревью шло по скиллу,
    отчёт покрывает код и правила. Снимок делает штатная taskctl review audit
    с базой в точке ответвления ветки, пересчёт хешей остаётся утилите, и
    стенд их не дублирует. Стенд знает точный состав двух временных задач.
    В обеих он выполняет шаг сценария, у WT-002 также проверяет файл работы
    и чувствительность теста к подмене строки. Отчёт уезжает коммитом в ветку,
    потому что ворота читают его из
    закоммиченного дерева, а не из рабочего каталога. standards это чекаут
    общих правил, с него снимается инвентарь правил: окружение стенда чекаут
    само по себе не знает, и у ворота слияния та же зависимость.
    """
    tree = Path(tree)
    env = {"DEVKIT_HOME": str(standards)}

    def call(args):
        return cmd_run(args, str(tree), env)

    if task not in (TASK, "WT-002") or tree.name != "proj-" + task.lower():
        return 1, "отчёт самопроверки предназначен только для её временных задач"
    rc, out = call(["git", "remote", "get-url", "origin"])
    if rc != 0 or out.strip() != str(tree.parent / "origin.git"):
        return 1, "у проекта нет локального origin стенда: " + out
    rc, out = call(["git", "branch", "--show-current"])
    if rc != 0:
        return rc, out
    head_branch = out.strip().split("\n")[-1].strip()
    if head_branch != task.lower():
        return 1, "отчёт самопроверки вне ветки задачи: " + head_branch
    rc, out = call(["git", "for-each-ref", "--format=%(refname:short)",
                    "refs/heads"])
    if rc != 0:
        return rc, out
    others = [ln.strip() for ln in out.split("\n")
              if ln.strip() and ln.strip() != head_branch]
    if not others:
        return 1, "в дереве задачи нет ствола, база аудита не берётся"
    rc, out = call(["git", "merge-base", "HEAD"] + others)
    if rc != 0:
        return rc, out
    base = out.strip().split("\n")[-1].strip()
    doc = "docs/tasks/%s.md" % task
    wait = task == "WT-002"
    changes = {doc: "M"}
    if wait:
        changes.update({WAIT_MARKER: "A", WAIT_TEST: "A"})
    rc, out = call(["git", "status", "--porcelain", "--untracked-files=all"])
    if rc != 0 or out.strip():
        return 1, "в ветке стенда есть незакоммиченные файлы: " + out
    rc, out = call(["git", "diff", "--name-status", base, "--"])
    if rc != 0 or set(out.splitlines()) != {status + "\t" + path for path, status in changes.items()}:
        return 1, "состав правки отличается от задачи стенда: " + out
    command = "test -f docs/TASKS.md && grep -q %s docs/TASKS.md" % task
    rc, base_card = call(["git", "show", base + ":" + doc])
    if rc != 0:
        return rc, base_card
    about, verify = FIXTURE_SECTIONS[task]
    expected = "%s\n\n%s\n\n%s\n%s\n\n%s" % (
        base_card.rstrip("\n"), SCENARIO_HEADER, about % task, VERIFY_HEADER, verify)
    card = (tree / doc).read_text(encoding="utf-8")
    if card != expected:
        return 1, "STYLE-016: изменения в %s расходятся со сценарием и проверяемым исходом стенда" % doc
    lines = card.splitlines()
    scenario = "%s:%d" % (doc, lines.index(command) + 1)
    rc, out = call(["sh", "-c", command])
    if rc != 0:
        return rc, "шаг сценария не нашёл %s: %s" % (task, out)
    if wait:
        if ((tree / WAIT_MARKER).read_text(encoding="utf-8") != "работа WT-002\n"
                or (tree / WAIT_TEST).read_text(encoding="utf-8") != WAIT_TEST_BODY):
            return 1, "маркер работы или проверка WT-002 отличаются от фикстуры"
        rc, out = call(["python3", "-I", WAIT_TEST])
        if rc != 0:
            return rc, out
        with tempfile.TemporaryDirectory(dir=tree.parent) as mutation:
            marker = Path(mutation) / WAIT_MARKER
            marker.parent.mkdir()
            marker.write_text("работа WT-999\n", encoding="utf-8")
            rc, out = cmd_run(["python3", "-I", str(tree / WAIT_TEST)], mutation, env)
            if rc != 1 or "AssertionError" not in out:
                return 1, "подмена ID в рабочем файле должна уронить сравнение: " + out
    rc, out = call(["taskctl", "-C", str(tree), "review", "audit", task,
                    "--base", base])
    if rc != 0:
        return rc, out
    report = tree / "docs" / "tasks" / ("%s.review-audit.json" % task)
    try:
        data = json.loads(report.read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        return 1, "отчёт аудита не читается (%s): %s" % (report, e)
    data["architecture"].update({
        "goal": ("проверить, что слияние предпосылки поднимает ждущую задачу" if wait
                 else "провести синтетическую задачу стенда от строки до слияния"),
        "decision": "штатные команды утилит devkit на временном проекте",
        "boundaries": "Правка временной задачи ограничена файлами " + ", ".join(sorted(changes)),
        "alternative": "заглушки утилит отклонены: стенд проверяет живую связку",
        "justification": "Сценарий проверяется в подготовленной ветке. Шаг %s вернул код 0." % command,
        "result": "checked",
        "evidence": [scenario] + ([WAIT_MARKER + ":1", WAIT_TEST + ":3"] if wait else []),
    })
    scope = ("В диффе только постановка и сценарий %s." % doc if not wait else
             "В диффе %s, текстовый маркер %s и проверка его содержимого %s." % (doc, WAIT_MARKER, WAIT_TEST))
    for rule in data["rules"]:
        if rule["id"] not in FIXTURE_RULE_IDS:
            return 1, "неизвестное стенду правило: " + rule["id"]
        rule.update(applicability="not_applicable", result="checked",
                    reason=scope + " Рабочие модули, их настройки и контракты не меняются.")
        if rule["id"] == "common-style:STYLE-016":
            rule["reason"] = ("В ветке нет публичной операции или изменения её описания. "
                              "Внутренний сценарий %s сверён с контрактом стенда, "
                              "в разделе «Проверка» указано: %s" % (scenario, verify.strip()))
        if wait:
            for file in rule.get("files", []):
                if file["path"] == WAIT_MARKER:
                    file["reason"] = "Текстовая строка «работа WT-002»."
                elif file["path"] == WAIT_TEST:
                    reason = {
                        "common-architecture:ARCH-018": "Скрипт читает один локальный файл, запросов к БД нет.",
                        "common-style:STYLE-001": "Скрипт не объявляет функций, типов или переменных.",
                        "common-style:STYLE-015": "Файл состоит из импорта Path и сравнения, комментариев нет.",
                    }.get(rule["id"])
                    if reason is None:
                        return 1, "стенд не проверил файлы правила " + rule["id"]
                    file["reason"] = reason
                else:
                    return 1, "неизвестный файл в инвентаре стенда: " + file["path"]
            if rule["id"] in ("common-tests:TEST-001", "common-tests:TEST-004",
                              "common-tests:TEST-005", "common-tests:TEST-006"):
                wait_test_facts(rule, scenario)
    report.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n",
                      encoding="utf-8")
    rc, out = call(["taskctl", "-C", str(tree), "review", "audit-check", task])
    if rc != 0:
        return rc, out
    rc, out = cmd_run(["git", "-C", str(tree), "add", "--",
                       "docs/tasks/%s.review-audit.json" % task], str(tree), env)
    if rc != 0:
        return rc, out
    return cmd_run(["git", "-C", str(tree), "commit", "-q", "-m",
                    "docs(tasks): %s отчёт аудита ревью" % task], str(tree), env)


def wait_test_facts(rule, scenario):
    check = WAIT_TEST + ":3"
    marker = WAIT_MARKER + ":1"
    item = "содержимое маркера WT-002"
    reason = "python3 -I %s сверил файл работы %s с ожидаемой строкой и вернул 0." % (WAIT_TEST, WAIT_MARKER)
    rule.update(applicability="applicable", reason=reason, objects=[item])

    def fact(location, text, **fields):
        return [dict(location=location, text=text, **fields)]

    ev = dict(object=item, location=check, result="checked", observation=reason)
    if rule["id"] == "common-tests:TEST-001":
        rule["evidence"] = [ev]
    elif rule["id"] == "common-tests:TEST-006":
        rule["evidence"] = [dict(ev, observation="С подменой WT-002 на WT-999 тот же скрипт вернул 1 с AssertionError.")]
    elif rule["id"] == "common-tests:TEST-004":
        rule["evidence"] = [dict(ev, facts=dict(
            test=fact(check, "Скрипт проверяет содержимое файла из ветки задачи."),
            dependency=fact(marker, "Прочитан файл работы из временного репозитория."),
            unit_selection=fact(check, "У временного проекта нет команды изолированных юнитов."),
            integration_selection=fact(check, "Команда теста слияния запускает этот файл явно.")))]
    else:
        rule["evidence"] = [
            dict(ev, check="strength", facts=dict(
                assertion=fact(check, "Содержание сверяется с буквальным образцом.", quote=WAIT_ASSERTION),
                boundary=fact(marker, "Строка содержит ID предпосылки и завершается переводом строки."),
                failure_case=fact(marker, "При замене WT-002 на WT-999 сравнение завершилось с AssertionError."))),
            dict(ev, check="overlap", kind="single", facts=dict(
                first=fact(check, "Проверено содержимое файла работы.", related=check, quote=WAIT_ASSERTION),
                first_level=fact(check, "Чтение файла из дерева проекта.", value="component"),
                search=fact(marker, "В ветке один тест содержимого маркера. Шаг сценария %s проверяет наличие строки доски." % scenario,
                            value="system")))]


def stamp_result(proj, task):
    """Факт прогона в раздел «Проверка» основного дерева после слияния."""
    if not (Path(proj) / DEPLOY_MARKER).exists():
        return False
    doc = Path(proj) / "docs" / "tasks" / ("%s.md" % task)
    if not doc.exists():
        return True
    text = doc.read_text(encoding="utf-8")
    line = "метка выката %s на месте после слияния" % DEPLOY_MARKER
    if line in text:
        return True
    text = re.sub(r"^(%s)\b.*$" % re.escape(VERIFY_HEADER),
                  r"\1\n\n%s" % line, text, count=1, flags=re.M)
    doc.write_text(text, encoding="utf-8")
    return True


def circle(cmd_run, proj, log, standards=None):
    """Круг шагов от временного проекта до закрытой задачи.

    cmd_run это прогонщик команд (у тестов subprocess с подставным PATH, у
    командной строки просто run), proj каталог временного проекта, log печатает
    строку отчёта, standards чекаут общих правил для отчёта аудита ревью (по
    умолчанию тот, из которого запущен сам selfcheck). Отдаёт список шагов;
    провал шага останавливает круг: дальше шаги стояли бы на сломанном и
    говорили бы «не прошёл» о непрогнанном.
    """
    standards = standards or DEVKIT
    steps = []

    def go(name, args, cwd=None, env=None):
        try:
            rc, out = cmd_run(args, cwd, env)
        except FileNotFoundError:
            rc, out = 127, "%s: нет такой команды в PATH" % args[0]
        steps.append({"name": name, "rc": rc, "out": out})
        log("  %s: %s" % (name, "ок" if rc == 0 else "не прошёл"))
        return rc

    if go("доктор", ["devkitctl", "doctor", "-C", str(proj)]) == 2:
        # Код 2 это отказ запуска: дальше круг не пойдёт. Код 1 это находки
        # раскладки, живой доктор о них сказал сам, их круг перечислит в конце.
        return steps
    if go("подключение", ["devkitctl", "new", "--prefix", PREFIX,
                          "-C", str(proj)]) != 0:
        return steps
    commit_ignores(cmd_run, proj)
    bare_origin(cmd_run, proj)
    deploy_stub(proj)
    # Коммит с -m берёт доску вместе с файлом задачи: без него файл оставался
    # бы untracked в основном дереве, и слияние отказывалось затирать его
    # версией из ветки.
    with task_description(
            "Проверяется установленная связка devkit во временном проекте с подставным выкатом.",
            "Провести задачу от создания строки через отдельную ветку до слияния, выката и закрытия.",
            "Все шаги selfcheck завершились успешно, метка выката создана, временные файлы удалены.") as body_file:
        if go("строка на доске", ["taskctl", "-C", str(proj), "add",
                                  "--title", "проверка связки", "--type", "task",
                                  "--rank", "25+5+1+0+0", "--cost", "S",
                                  "--body-file", body_file, "--accept", "agent",
                                  "-m", "docs(tasks): %s проверка связки заведена" % TASK]) != 0:
            return steps
    if go("вердикт pick", ["agentctl", "-C", str(proj), "pick", TASK]) != 0:
        return steps
    verdict = steps[-1]
    if not (re.search(r"^model: ", verdict["out"], re.M)
            and re.search(r"^effort: ", verdict["out"], re.M)):
        # Нулевой код без строк model/effort это не назначение, а молчание
        # утилиты: вердиктом обязан быть вердикт.
        verdict["rc"] = 1
        verdict["out"] = "в выводе pick нет строк model/effort:\n" + verdict["out"]
        log("  вердикт pick: не прошёл")
        return steps
    if go("ветка задачи", ["shipctl", "-C", str(proj), "start", TASK]) != 0:
        return steps
    rc, out = write_scenario(cmd_run, task_tree(proj, TASK), TASK)
    steps.append({"name": "сценарий задачи", "rc": rc, "out": out})
    log("  сценарий задачи: %s" % ("ок" if rc == 0 else "не прошёл"))
    if rc != 0:
        return steps
    # Отчёт стоит до обкатки: мерка свежести отметки пускает после себя
    # коммиты одних файлов доски, а json отчёта к ним не относится. Записи
    # уровня и отметки лежат в разделах «Ревью» и «Проверка», отчёт их не
    # хеширует, и позже правки постановки отчёт не старят.
    rc, out = audit_report(cmd_run, task_tree(proj, TASK), TASK, standards)
    steps.append({"name": "отчёт аудита ревью", "rc": rc, "out": out})
    log("  отчёт аудита ревью: %s" % ("ок" if rc == 0 else "не прошёл"))
    if rc != 0:
        return steps
    rc, out = rehearse_scenario(cmd_run, task_tree(proj, TASK), TASK)
    steps.append({"name": "обкатка сценария", "rc": rc, "out": out})
    log("  обкатка сценария: %s" % ("ок" if rc == 0 else "не прошёл"))
    if rc != 0:
        return steps
    # Ворота слияния (reviewLevelGate) требуют в разделе «Ревью» строку
    # уровня, а живой конвейер ставит её ревьювером по скиллу review. Круг
    # ревьювера не зовёт, и синтетическая задача честно берёт нулевой
    # уровень: мелочь без критичности, решение о пропуске записано с причиной
    # (шкала уровней в kit/skills/review/SKILL.md).
    if go("уровень ревью", ["taskctl", "-C", str(task_tree(proj, TASK)), "review",
                            "level", TASK, "0",
                            "синтетическая задача круга самопроверки, критичности нет",
                            "-m", "docs(tasks): %s уровень ревью проставлен кругом" % TASK]) != 0:
        return steps
    # Слияние проверяет отчёт в дереве задачи, и его воротам нужен тот же
    # чекаут общих правил: без него инвентарь правил отчёта не собирается.
    if go("слияние с выкатом", ["shipctl", "-C", str(proj), "merge", TASK],
          env={"DEVKIT_HOME": str(standards)}) != 0:
        return steps
    if not stamp_result(proj, TASK):
        # Слияние ответило «ок», а следа выката нет: выкат не сработал, и
        # «ок» здесь было бы ложным.
        merged = steps[-1]
        merged["rc"] = 1
        merged["out"] += "\nвыкат не оставил метку " + DEPLOY_MARKER
        log("  слияние с выкатом: не прошёл")
        return steps
    # Слияние оставило выкат непроверенным, и отметку smoke в конвейере ставит
    # прогон проверяющего после выката (checkrun). Круг зовёт отметку сам, тем
    # же порядком, каким зовёт разбор долга: без неё close отобьётся на
    # непроверенном выкате (DK-1363).
    if go("отметка smoke", ["shipctl", "-C", str(proj), "smoke", TASK]) != 0:
        return steps
    # Отметку долга открывает перевод в Check внутри merge. В конвейере хвост
    # закрывает проверяющий по заказу checkrun, круг зовёт тот же шаг сам.
    if go("разбор долга", ["taskctl", "-C", str(proj), "debt-done", TASK,
                           "пересечений нет"]) != 0:
        return steps
    go("закрытие задачи", ["taskctl", "-C", str(proj), "close", TASK,
                           "-m", "docs(tasks): %s проверка связки закрыта" % TASK,
                           "--push"])
    return steps


def main(argv=None, cmd_run=None, where=None, log=print, standards=None):
    """Прогон круга с отчётом по шагам. Отдаёт код: 0 связка жива, 1 нет.

    cmd_run, where и standards вынесены параметрами для тестов: круг в тестах
    гоняется по подставному PATH с собранными бинарями и по копии devkit, а не
    по PATH и дереву машины.
    """
    cmd_run = cmd_run or run
    if where is None:
        where = Path(tempfile.mkdtemp(prefix="devkit-selfcheck-"))
        mine = True
    else:
        mine = False
    where = Path(where)
    proj = where / "proj"
    home = where / "home"
    home.mkdir(parents=True, exist_ok=True)
    if cmd_run is run:
        # Боевой прогон работает с подменённым домом: утилиты devkit пишут в
        # ~/.devkit без ведома вызвавшего, и круг обязан оставить машину
        # нетронутой. Тесты приносят свой прогонщик со стендовым домом.
        cmd_run = lambda args, cwd=None, env=None: run(args, cwd=cwd, env=env,
                                                       home=home)
    log("самопроверка связки devkit: временный проект в %s" % proj)
    try:
        steps = circle(cmd_run, proj, log, standards)
        # Уборка судится двумя слепками: ветки репозитория проекта до и после
        # круга (слияние обязано унести ветку задачи с собой) и пустота места
        # после сноса собственных каталогов круга.
        branches = leftover_branches(proj, cmd_run)
        shutil.rmtree(str(proj), ignore_errors=True)
        shutil.rmtree(str(where / "origin.git"), ignore_errors=True)
        shutil.rmtree(str(home), ignore_errors=True)
        residue = sorted(p.name for p in where.iterdir())
        if branches:
            log("за кругом осталась ветка задачи в репозитории проекта: %s"
                % ", ".join(branches))
        if residue:
            log("за кругом осталось в месте круга: %s" % ", ".join(residue))
        doctor = next((s for s in steps if s["name"] == "доктор"), None)
        failed = next((s for s in steps if s["rc"] != 0
                       and not (s is doctor and s["rc"] == 1)), None)
        if failed is None:
            if doctor and doctor["rc"] == 1:
                log("доктор назвал находки по машине, круг при этом прошёл:")
                for ln in tail(doctor["out"], 20).split("\n"):
                    log("  %s" % ln)
            log("связка жива: круг прошёл целиком")
            return 0
        log("шаг «%s» не прошёл, связка не жива:" % failed["name"])
        for ln in tail(failed["out"]).split("\n"):
            log("  %s" % ln)
        return 1
    finally:
        if mine:
            shutil.rmtree(str(where), ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
