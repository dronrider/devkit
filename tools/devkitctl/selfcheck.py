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
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

PREFIX = "SC"
TASK = "SC-001"
# Выкат и тесты временного проекта подставные, обе команды на самом python3
# без внешних утилит: слияние гоняет их через sh -c в том же PATH, и
# минимальному окружению вроде CI-контейнера незачем знать touch(1) и true(1).
DEPLOY_CMD = "python3 -c \"open('.devkit/selfcheck-deployed','w').close()\""
TEST_CMD = "python3 -c pass"
GIT_ENV = {
    "GIT_AUTHOR_NAME": "selfcheck",
    "GIT_AUTHOR_EMAIL": "selfcheck@devkit.local",
    "GIT_COMMITTER_NAME": "selfcheck",
    "GIT_COMMITTER_EMAIL": "selfcheck@devkit.local",
}
SCENARIO_HEADER = "## Сценарий проверки"
VERIFY_HEADER = "## Проверка"
TIMEOUT = 900


def run(args, cwd=None, env=None, timeout=TIMEOUT, home=None):
    """Прогон команды в окружении круга. Отдаёт код и слитый вывод.

    home подставляет сабпроцессам вместо настоящего дома временный каталог:
    утилиты devkit пишут в ~/.devkit и без ведома вызвавшего (taskctl ведёт
    записи runs, уведомитель журнал notify.log), и без подмены круг оставлял
    бы след в машине, которую проверяет. Без home наследуется окружение
    родителя: так круг зовётся из тестов со своим стендовым домом.
    """
    e = dict(os.environ)
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
    """
    doc = Path(tree) / "docs" / "tasks" / ("%s.md" % task)
    doc.parent.mkdir(parents=True, exist_ok=True)
    text = doc.read_text(encoding="utf-8") if doc.exists() else ""
    if not text.strip():
        text = "# %s: проверка связки\n" % task
    if not re.search(r"^%s\b" % re.escape(SCENARIO_HEADER), text, flags=re.M):
        text += ("\n%s\n\n1. devkitctl selfcheck во временном каталоге.\n"
                 "2. Каждый шаг круга отвечает «ок», итог «связка жива».\n\n"
                 "Шаг, выполнимый без выката, лежит блоком: его гоняет обкатка\n"
                 "круга, и ворота перевода в Check спрашивают её отметку.\n\n"
                 "```sh\ntest -f docs/TASKS.md && grep -q %s docs/TASKS.md\n```\n\n"
                 "Ожидаемый итог: круг закрыл временную задачу и не оставил "
                 "после себя ни проекта, ни дерева задачи.\n"
                 % (SCENARIO_HEADER, task))
    if not re.search(r"^%s\b" % re.escape(VERIFY_HEADER), text, flags=re.M):
        text += ("\n%s\n\nвыкат круга оставляет метку .devkit/selfcheck-deployed, "
                 "круг проверяет её после слияния\n" % VERIFY_HEADER)
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


def stamp_result(proj, task):
    """Факт прогона в раздел «Проверка» основного дерева после слияния."""
    if not (Path(proj) / ".devkit" / "selfcheck-deployed").exists():
        return False
    doc = Path(proj) / "docs" / "tasks" / ("%s.md" % task)
    if not doc.exists():
        return True
    text = doc.read_text(encoding="utf-8")
    line = "метка выката .devkit/selfcheck-deployed на месте после слияния"
    if line in text:
        return True
    text = re.sub(r"^(%s)\b.*$" % re.escape(VERIFY_HEADER),
                  r"\1\n\n%s" % line, text, count=1, flags=re.M)
    doc.write_text(text, encoding="utf-8")
    return True


def circle(cmd_run, proj, log):
    """Круг шагов от временного проекта до закрытой задачи.

    cmd_run это прогонщик команд (у тестов subprocess с подставным PATH, у
    командной строки просто run), proj каталог временного проекта, log печатает
    строку отчёта. Отдаёт список шагов; провал шага останавливает круг: дальше
    шаги стояли бы на сломанном и говорили бы «не прошёл» о непрогнанном.
    """
    steps = []

    def go(name, args, cwd=None):
        try:
            rc, out = cmd_run(args, cwd)
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
    if go("строка на доске", ["taskctl", "-C", str(proj), "add",
                              "--title", "проверка связки", "--type", "task",
                              "--rank", "25+5+1+0+0", "--cost", "S",
                              "--accept", "agent",
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
    rc, out = rehearse_scenario(cmd_run, task_tree(proj, TASK), TASK)
    steps.append({"name": "обкатка сценария", "rc": rc, "out": out})
    log("  обкатка сценария: %s" % ("ок" if rc == 0 else "не прошёл"))
    if rc != 0:
        return steps
    if go("слияние с выкатом", ["shipctl", "-C", str(proj), "merge", TASK]) != 0:
        return steps
    if not stamp_result(proj, TASK):
        # Слияние ответило «ок», а следа выката нет: выкат не сработал, и
        # «ок» здесь было бы ложным.
        merged = steps[-1]
        merged["rc"] = 1
        merged["out"] += "\nвыкат не оставил метку .devkit/selfcheck-deployed"
        log("  слияние с выкатом: не прошёл")
        return steps
    go("закрытие задачи", ["taskctl", "-C", str(proj), "close", TASK,
                           "-m", "docs(tasks): %s проверка связки закрыта" % TASK,
                           "--push"])
    return steps


def main(argv=None, cmd_run=None, where=None, log=print):
    """Прогон круга с отчётом по шагам. Отдаёт код: 0 связка жива, 1 нет.

    cmd_run и where вынесены параметрами для тестов: круг в тестах гоняется по
    подставному PATH с собранными бинарями, а не по PATH машины.
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
        cmd_run = lambda args, cwd=None: run(args, cwd=cwd, home=home)
    log("самопроверка связки devkit: временный проект в %s" % proj)
    try:
        steps = circle(cmd_run, proj, log)
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
