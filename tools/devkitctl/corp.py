"""Корп-контур на стороне devkitctl: обвязка клона и её диагностика.

Дома рабочие файлы devkit лежат в дереве проекта, а в корп-клоне их там быть не
должно: доска, файлы задач и .devkit переезжают в боковую директорию
../<проект>-local, а в клоне остаётся обвязка (редирект devkit.local, строка в
.git/info/exclude, обёртки хуков и один тонкий файл контекста харнеса). Вся она
живёт в .git и в негитигнорнутом дереве, поэтому переклонирование и
«git clean -xdf» теряют её молча: клон выглядит обычным проектом без devkit,
хотя доска цела. Отсюда две обязанности модуля, подключение и диагностика, и
подключение же служит восстановлением.

Признак контура тот же, что у утилит devkit (tools/taskctl/corp.go, corpLocal): ключ
devkit.local в конфиге клона. Разбор его не переписывается в третий раз, а
берётся из hooks/corp.py: там он уже переведён на python, и оттуда же приезжает
раскладка цепочки хуков (install_chain).
"""
import importlib.util
import os
import re
import subprocess
import sys
import time
from pathlib import Path

# Каталог обвязки в корне клона: тут же лежат ссылки на соседние деревья, через
# которые клон зовёт правила (rules.LINK_DIR, docs/lld/DK-193-rules-delivery.md).
LINK_DIR = ".devkit"
TRACKER = os.path.join(LINK_DIR, "tracker.local")
# Отметку последнего pull статусов пишет trackctl sync (tools/trackctl/cmd.go,
# syncMarkPath), здесь она только читается: доктор говорит про свежесть доски то
# же, что trackctl status.
SYNC_MARK = os.path.join(".devkit", "tracker.sync")
# Сутки без sync это уже расхождение доски с тикетами, которое видно глазом на
# стендапе раньше, чем утилитой.
SYNC_MAX_AGE = 24 * 3600
# Рубеж следов держится сообщением коммита и диффом врозь: pre-commit смотрит
# индекс, commit-msg сообщение. Клон с одной обёрткой пропускает локальный ID в
# сообщении молча, поэтому цепочка ставится и проверяется на обоих.
HOOKS = ("pre-commit", "commit-msg")
# Что в корп-индексе значит утечку обвязки. Тонкие файлы харнесов приезжают
# отдельно, они зависят от включённых профилей.
INDEX_WATCH = ("AGENTS.md", "docs/TASKS.md", "docs/tasks", ".devkit", "local-docs")
# Ключи привязки, которые подключение спрашивает флагами: сам он их не выдумает,
# а молчаливое пустое место в файле человек находит уже на упавшей команде.
BINDING_ASK = ("contour", "key", "branch")
CONTOUR_TEMPLATE = """\
# Контур компании для trackctl (tools/trackctl/README.md): свойство компании, один на
# все её репозитории. Токен в файле не лежит, token_env называет переменную
# окружения с ним. Таблица [status] укладывает статусы трекера в секции доски,
# ниже обычные имена Jira: сверить их со своим трекером.
adapter = "jira"
# Адрес трекера, https://tracker.example
base_url = ""
# Имя пользователя в трекере, от него идут assign и ворклоги
user = ""
token_env = "TRACKER_TOKEN"
cost_s = "4h"
cost_m = "1d"
cost_l = "3d"

[status]
backlog = ["Open", "Backlog"]
in_progress = ["In Progress"]
check = ["Review", "Testing"]
blocked = ["Blocked", "On Hold"]
done = ["Done", "Closed", "Rejected"]

[fields_in_progress]
assignee = "{user}"
"""

_hooks_mod = None


def hooks_corp(devkit):
    """Модуль hooks/corp.py как модуль: признак контура и раскладка цепочки
    живут там, и второго их экземпляра тут нет."""
    global _hooks_mod
    if _hooks_mod is None:
        spec = importlib.util.spec_from_file_location(
            "devkit_hooks_corp", str(Path(devkit) / "hooks" / "corp.py"))
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        _hooks_mod = mod
    return _hooks_mod


def git(start, *args):
    try:
        p = subprocess.run(["git", "-C", str(start)] + list(args),
                           stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    except OSError:
        return ""
    if p.returncode != 0:
        return ""
    return p.stdout.decode("utf-8", "replace").strip()


def checkout(start):
    """Директория основного чекаута, то есть родитель git-common-dir. От неё
    считается относительный путь редиректа: у дерева ветки вершина своя, и от
    неё путь сошёлся бы, только пока дерево лежит сиблингом проекта."""
    common = git(start, "rev-parse", "--git-common-dir")
    if not common:
        return ""
    if not os.path.isabs(common):
        common = os.path.join(os.path.abspath(str(start)), common)
    return os.path.dirname(os.path.normpath(common))


def common_dir(start):
    common = git(start, "rev-parse", "--git-common-dir")
    if not common:
        return ""
    if not os.path.isabs(common):
        common = os.path.join(os.path.abspath(str(start)), common)
    return os.path.normpath(common)


def local_dir(start, devkit):
    """Боковая директория корп-контура, пусто у домашнего проекта."""
    return hooks_corp(devkit).local_dir(str(start))


def tracker_value(local, key, devkit):
    return hooks_corp(devkit).tracker_value(str(local), key)


def same_path(a, b):
    # Временные директории на macOS лежат под /var, который сам симлинк на
    # /private/var, и голое сравнение строк расходится на ровном месте.
    try:
        a = os.path.realpath(a)
        b = os.path.realpath(b)
    except OSError:
        pass
    return os.path.normpath(a) == os.path.normpath(b)


def lost_local(clone, devkit):
    """Боковая директория, чья привязка ключом repo указывает на этот клон.
    Так потерянная обвязка находится с обеих сторон: и из клона, где про devkit
    уже ничего не осталось, и из самой боковой директории."""
    base = checkout(clone)
    if not base:
        return ""
    parent = os.path.dirname(base)
    try:
        names = sorted(os.listdir(parent))
    except OSError:
        return ""
    for name in names:
        d = os.path.join(parent, name)
        if not name.endswith("-local") or not os.path.isdir(d):
            continue
        # Директория контура держит проекты подкаталогами, и привязка лежит
        # тогда на уровень ниже: смотрятся оба уровня, старый и общий.
        try:
            inner = [os.path.join(d, n) for n in sorted(os.listdir(d))]
        except OSError:
            inner = []
        for cand in [d] + [i for i in inner if os.path.isdir(i)]:
            repo = tracker_value(cand, "repo", devkit)
            if not repo:
                continue
            if not os.path.isabs(repo):
                repo = os.path.join(cand, repo)
            if same_path(repo, base):
                return cand
    return ""


def pair(root, devkit):
    """Пара (клон, боковая директория) для стартовой директории, ("", "") у
    домашнего проекта. Считается с трёх сторон: редирект в конфиге клона,
    привязка с ключом repo, когда doctor гоняют в самой боковой директории, и
    соседняя «*-local» с этим клоном в repo, когда редирект уже потерян."""
    root = str(root)
    local = local_dir(root, devkit)
    if local:
        return checkout(root) or root, local
    repo = tracker_value(root, "repo", devkit)
    if repo:
        if not os.path.isabs(repo):
            repo = os.path.join(root, repo)
        return os.path.normpath(repo), root
    lost = lost_local(root, devkit)
    if lost:
        return checkout(root) or root, lost
    return "", ""


def hooks_dir(clone):
    common = common_dir(clone)
    return os.path.join(common, "hooks") if common else ""


def exclude_path(clone):
    common = common_dir(clone)
    return os.path.join(common, "info", "exclude") if common else ""


def exclude_lines(clone):
    path = exclude_path(clone)
    if not path or not os.path.exists(path):
        return []
    with open(path, encoding="utf-8", errors="replace") as f:
        return [ln.strip() for ln in f]


def hidden_names(thin_names):
    """Что прячется строкой exclude: тонкие файлы харнесов, и только они.

    Ссылка .devkit из exclude ушла (DK-583): ripgrep и git читают одни и те же
    источники игнора, и спрятанное от git прячется заодно от поиска редактора,
    то есть доска перестаёт находиться из окна клона. Ссылка живёт в дереве
    открыто, висит в git status одной строкой «?? .devkit», а от случайного
    «git add .» её стережёт обёртка pre-commit.
    """
    return list(thin_names)


def ensure_exclude(clone, names):
    """Дописать в .git/info/exclude недостающие строки. Отдаёт дописанное."""
    have = set(exclude_lines(clone))
    missing = [n for n in names if n not in have]
    if not missing:
        return []
    path = exclude_path(clone)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    text = ""
    if os.path.exists(path):
        with open(path, encoding="utf-8", errors="replace") as f:
            text = f.read()
    head = "" if not text or text.endswith("\n") else "\n"
    with open(path, "a", encoding="utf-8") as f:
        f.write(head + "# devkit: обвязка корп-контура, в индекс не едет.\n")
        f.write("".join(n + "\n" for n in missing))
    return missing


def drop_exclude(clone, names):
    """Убрать из .git/info/exclude строки, которые devkit туда клал, а больше не
    кладёт. Отдаёт убранное; чужие строки не трогаются."""
    path = exclude_path(clone)
    if not path or not os.path.exists(path):
        return []
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = f.read().split("\n")
    drop = [n for n in names if n in [ln.strip() for ln in lines]]
    if not drop:
        return []
    kept = [ln for ln in lines if ln.strip() not in drop]
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(kept))
    return drop


def link_target(clone, local):
    """Куда ведёт ссылка .devkit клона: путь до боковой директории от корня
    клона. Относительный, пока она лежит рядом, как и редирект в конфиге."""
    return os.path.relpath(local, clone)


def tree_link_state(clone, local):
    """Что лежит в клоне под именем .devkit: «на месте», «ведёт не туда»,
    «занято» (свой каталог или файл) либо «нет»."""
    path = os.path.join(clone, LINK_DIR)
    if os.path.islink(path):
        return "на месте" if same_path(path, local) else "ведёт не туда"
    if os.path.exists(path):
        return "занято"
    return "нет"


def stale_links(path):
    """Ссылки на соседние деревья в старом каталоге обвязки клона. Пусто, если
    там лежит хоть что-то своё: тогда каталог не наш и сносить его нельзя."""
    try:
        names = sorted(os.listdir(path))
    except OSError:
        return []
    if any(not os.path.islink(os.path.join(path, n)) for n in names):
        return []
    return names


def ensure_tree_link(clone, local):
    """Ссылка .devkit -> боковая директория в дереве клона. Отдаёт строку о
    сделанном либо пусто, когда ссылка уже на месте.

    Клон, подключённый до DK-583, держал под этим именем каталог с двумя
    ссылками на соседние деревья: он сносится, потому что те же ссылки лежат
    теперь в самой боковой директории. Каталог со своим содержимым не трогается
    вовсе, про него говорит находка доктора.
    """
    path = os.path.join(clone, LINK_DIR)
    state = tree_link_state(clone, local)
    if state == "на месте":
        return ""
    want = link_target(clone, local)
    if state == "занято":
        names = stale_links(path)
        if not names:
            return ""
        for n in names:
            os.unlink(os.path.join(path, n))
        os.rmdir(path)
        os.symlink(want, path)
        return ("ссылка %s -> %s заменила каталог обвязки: боковая директория "
                "открыта из клона одним окном" % (LINK_DIR, want))
    if state == "ведёт не туда":
        os.unlink(path)
    os.symlink(want, path)
    return "ссылка %s -> %s: доска и файлы задач видны из клона" % (LINK_DIR, want)


def contour_local(clone, contour):
    """Боковая директория по умолчанию: общая на контур
    ../<контур>-local/<проект>, а без имени контура прежняя ../<проект>-local
    (DK-074). Одна директория на контур держит сколько угодно проектов, и в
    рабочей раскладке человека сбоку лежит одна папка, а не по одной на клон."""
    clone = str(clone)
    parent = os.path.dirname(os.path.abspath(clone))
    name = os.path.basename(os.path.abspath(clone))
    if contour:
        return os.path.join(parent, contour + "-local", name)
    return os.path.join(parent, name + "-local")


def repo_top(path):
    """Вершина git-репозитория, которому принадлежит ближайшая существующая
    директория пути. Пусто, если репозитория там нет: тогда его заводит
    подключение."""
    d = os.path.abspath(str(path))
    while d and not os.path.isdir(d):
        parent = os.path.dirname(d)
        if parent == d:
            return ""
        d = parent
    return git(d, "rev-parse", "--show-toplevel")


def binding_keys(local):
    """Ключи, реально написанные в привязке, вне зависимости от значения."""
    path = os.path.join(local, TRACKER)
    keys = set()
    if not os.path.exists(path):
        return keys
    with open(path, encoding="utf-8", errors="replace") as f:
        for raw in f:
            key, sep, _ = raw.strip().partition("=")
            if sep:
                keys.add(key.strip())
    return keys


def ensure_binding(local, clone, values=None):
    """Ключи привязки боковой директории. Ключ repo подключение пишет само (им
    обвязка клона находится и тогда, когда клон про devkit уже ничего не знает),
    остальное берётся из values, то есть из флагов подключения. Написанное не
    трогается: повторный прогон только дописывает недостающее. Отдаёт список
    пар (ключ, значение) в том виде, в каком они легли в файл."""
    path = os.path.join(local, TRACKER)
    text = ""
    if os.path.exists(path):
        with open(path, encoding="utf-8", errors="replace") as f:
            text = f.read()
    have = binding_keys(local)
    want = [("repo", os.path.relpath(clone, local))]
    for key in BINDING_ASK:
        val = (values or {}).get(key, "")
        if val:
            want.append((key, val))
    add = [(k, v) for k, v in want if k not in have]
    if not add:
        return []
    os.makedirs(os.path.dirname(path), exist_ok=True)
    head = "" if not text or text.endswith("\n") else "\n"
    if not text:
        head = ("# Привязка проекта к трекеру (tools/trackctl/README.md). Ключ repo кладёт\n"
                "# подключение: по нему обвязка клона находится после переклонирования.\n")
    with open(path, "a", encoding="utf-8") as f:
        f.write(head + "".join("%s = %s\n" % (k, v) for k, v in add))
    return add


def interactive():
    """Есть ли кого спрашивать. Первый прогон подключения задаёт вопросы только
    живому человеку: в headless-прогоне (сессия агента, CI) вопрос повис бы
    молча, и там команда идёт как раньше, а недоделанное называет хвостом."""
    try:
        return bool(sys.stdin.isatty() and sys.stdout.isatty())
    except (AttributeError, ValueError):
        return False


def ask(prompt, default=""):
    """Вопрос первого прогона. Пустой ответ значит согласие с предложенным в
    скобках, оборванный ввод (Ctrl-D) значит то же самое: подключение доводит
    остальное, а неотвеченное уезжает в хвост «осталось сделать»."""
    line = "%s [%s]: " % (prompt, default) if default else "%s: " % prompt
    try:
        got = input(line).strip()
    except EOFError:
        print("")
        return default
    return got or default


def prefix_ok(prefix):
    """Годится ли префикс доске. Рубеж тот же, что у taskctl init
    (tools/taskctl/init.go, prefixArgRe): доску заводит он, и отказ его на
    ответе человека выглядел бы падением подключения."""
    return bool(re.match(r"^[A-ZА-Я]+$", prefix))


def prefix_hint(name):
    """Префикс доски, предложенный по имени клона: инициалы слов, а у имени в
    одно слово две первые буквы. Правило нарочно предсказуемое, а не умное:
    предложение человек видит в вопросе и меняет на месте, а угадывать он должен
    не алгоритм, а свой ответ."""
    words = [w for w in re.split(r"[^A-Za-zА-Яа-яЁё]+", name) if w]
    if not words:
        return ""
    got = (words[0][:2] if len(words) == 1 else "".join(w[0] for w in words[:4])).upper()
    return got if prefix_ok(got) else ""


def contour_path(name):
    return os.path.join(os.path.expanduser("~"), ".devkit", "tracker", name + ".local")


def contour_value(name, key):
    """Значение верхнего ключа контура компании. Формат там подмножество TOML,
    и читаются отсюда только плоские строки шапки, до первой секции."""
    path = contour_path(name)
    if not os.path.exists(path):
        return ""
    with open(path, encoding="utf-8", errors="replace") as f:
        for raw in f:
            line = raw.strip()
            if line.startswith("["):
                break
            if line.startswith("#") or "=" not in line:
                continue
            k, _, val = line.partition("=")
            if k.strip() == key:
                return val.strip().strip("\"'")
    return ""


def ensure_contour(name, values=None):
    """Контур компании ~/.devkit/tracker/<имя>.local. Таблица статусов кладётся
    заполненной обычными именами Jira: сверить её с трекером компании дешевле,
    чем писать с нуля. Адрес и пользователь приезжают ответами первого прогона
    (values), а без них файл ложится болванкой, как раньше. Заполненный файл не
    трогается, отдаётся путь заведённого либо пусто."""
    path = contour_path(name)
    if os.path.exists(path):
        return ""
    text = CONTOUR_TEMPLATE
    for key in ("base_url", "user"):
        # Кавычки из ответа выкидываются: значение и так ложится в кавычках, а
        # свои развалили бы файл на первом же чтении.
        val = (values or {}).get(key, "").replace('"', "").strip()
        if val:
            text = text.replace('%s = ""' % key, '%s = "%s"' % (key, val), 1)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    return path


def remaining(clone, local, devkit):
    """Что после подключения осталось сделать человеку, с путями файлов и
    именами ключей. Молчание тут неотличимо от готового проекта, поэтому
    подключение печатает этот список, а не оставляет его в README."""
    steps = []
    binding = os.path.join(local, TRACKER)
    contour = tracker_value(local, "contour", devkit)
    if not contour:
        steps.append("привязка %s: вписать contour (имя контура компании) и key (ключ проекта "
                     "в трекере) либо повторить corp с --contour и --key" % binding)
    else:
        cpath = contour_path(contour)
        if not os.path.exists(cpath):
            steps.append("контур компании %s: файла нет, завести его по tools/trackctl/README.md" % cpath)
        else:
            miss = [k for k in ("base_url", "user") if not contour_value(contour, k)]
            if miss:
                steps.append("контур компании %s: заполнить %s" % (cpath, ", ".join(miss)))
            env = contour_value(contour, "token_env")
            if not env and not contour_value(contour, "token_file"):
                steps.append("контур компании %s: назвать token_env либо token_file, "
                             "откуда брать токен трекера" % cpath)
            elif env and not os.environ.get(env):
                steps.append("токен трекера: export %s=<токен>, имя переменной названо в %s"
                             % (env, cpath))
        if not tracker_value(local, "key", devkit):
            steps.append("привязка %s: вписать key, ключ проекта в трекере" % binding)
    if not git(local, "remote"):
        steps.append("личный приватный remote доски: git -C %s remote add origin <адрес>; "
                     "доска не едет в корп-origin, и без своего remote обрыв машины её теряет"
                     % local)
    return steps


def ensure_redirect(clone, local):
    """Редирект корня в конфиге клона. Путь относительный, пока боковая
    директория лежит рядом: клон переезжает вместе с ней."""
    rel = os.path.relpath(local, clone)
    git(clone, "config", "--local", "devkit.local", rel)
    return rel


def ensure_hooks(clone, devkit):
    """Цепочка на обоих хуках. Отдаёт список (хук, что стало, куда переехал
    чужой хук); повторный прогон отдаёт «на месте» и ничего не трогает."""
    hd = hooks_dir(clone)
    out = []
    if not hd:
        return out
    install = hooks_corp(devkit).install_chain
    for name in HOOKS:
        state, chained = install(hd, name, str(Path(devkit) / "hooks"))
        out.append((name, state, chained))
    return out


def hook_findings(clone, devkit):
    """Находки по цепочке: файла нет вовсе либо обёртку перезаписал чужой
    инсталлер (пропал маркер). Оба случая чинятся повторной раскладкой."""
    hd = hooks_dir(clone)
    findings = []
    if not hd:
        return findings
    is_chain = hooks_corp(devkit).is_chain
    for name in HOOKS:
        path = os.path.join(hd, name)
        if not os.path.exists(path):
            findings.append("цепочка хуков потеряна на %s: рубеж следов держат оба хука врозь "
                            "(дифф смотрит pre-commit, сообщение коммита commit-msg), и клон с "
                            "одной обёрткой пропускает локальный ID молча" % name)
        elif not is_chain(path):
            findings.append("обёртка %s перезаписана чужим инсталлером (пропал маркер devkit): "
                            "проверки devkit до коммита не доезжают" % name)
    return findings


def index_findings(clone, thin_names):
    """Файлы обвязки в корп-индексе. Смысл боковой директории в том, что в
    чужой репозиторий не уезжает ничего нашего, и попавший в индекс файл это
    утечка, а не мелочь оформления."""
    watched = list(thin_names) + list(INDEX_WATCH)
    out = git(clone, "ls-files", "--", *watched)
    if not out:
        return []
    files = ", ".join(sorted(set(out.split("\n"))))
    return ["в корп-индексе лежит обвязка devkit (%s): она не для чужого репозитория, "
            "убрать из индекса (git rm --cached) и держать в .git/info/exclude" % files]


def prefix_findings(local, devkit):
    """Совпадение префикса доски с ключом проекта. Рубеж следов на такой паре
    снимает правило про локальный ID (hooks/corp.py, prefix_collision): ID
    строки и ключ тикета там неотличимы, и рубеж валил бы каждый коммит по
    конвенции компании. Молчать об этом нельзя: снаружи ослабленный рубеж
    выглядит ровно как работающий."""
    mod = hooks_corp(devkit)
    if not mod.prefix_collision(str(local)):
        return []
    prefix = mod.board_prefix(str(local))
    return ["префикс доски %s совпадает с ключом проекта в трекере: рубеж следов не отличает "
            "локальный ID доски от ключа тикета и правило про ID на этом проекте не работает "
            "(путь боковой директории и слова контура рубеж стережёт по-прежнему); "
            "развести префикс доски с ключом проекта" % prefix]


def sync_findings(local, devkit):
    """Свежесть последнего pull статусов. Спрашивается только у проекта,
    привязанного к трекеру (ключ contour): без него sync гонять нечем, и
    молчание тут штатно, а не потеря."""
    if not tracker_value(local, "contour", devkit):
        return []
    mark = os.path.join(local, SYNC_MARK)
    if not os.path.exists(mark):
        return ["sync с трекером не гонялся ни разу: доска и тикеты расходятся молча, "
                "догоняет их trackctl sync"]
    age = time.time() - os.stat(mark).st_mtime
    if age > SYNC_MAX_AGE:
        return ["последний sync с трекером %d дн назад: доска отстала от тикетов, "
                "догоняет её trackctl sync" % int(age // 86400)]
    return []


def check(clone, local, devkit, thin_names, fix):
    """Корп-проверки доктора. Возврат (находки, починенное), как их ждёт
    doctor. Включаются только контуром: домашний проект сюда не заходит."""
    findings, fixed = [], []
    if not local_dir(clone, devkit):
        # Дальше проверять нечего: без редиректа утилиты devkit этот клон
        # корп-проектом не считают вовсе, и остальные находки повторяли бы одно.
        findings.append("обвязка корп-клона %s потеряна (нет git config devkit.local), "
                        "а доска цела в %s: так выглядит переклонирование или git clean -xdf; "
                        "восстанавливает обвязку «devkitctl corp -C %s»" % (clone, local, clone))
        return findings, fixed
    if not same_path(local_dir(clone, devkit), local):
        findings.append("редирект клона %s ведёт в %s, а привязка называет %s: "
                        "две боковые директории на один клон, разобрать руками"
                        % (clone, local_dir(clone, devkit), local))
    findings += index_findings(clone, thin_names)
    hide = hidden_names(thin_names)
    missing = [n for n in hide if n not in set(exclude_lines(clone))]
    if missing:
        if fix:
            for n in ensure_exclude(clone, hide):
                fixed.append(".git/info/exclude: спрятан %s" % n)
        else:
            findings.append(".git/info/exclude не прячет %s: тонкий файл контекста харнеса лежит "
                            "в корне клона, и без строки exclude он торчит в чужом git status"
                            % ", ".join(missing))
    if LINK_DIR in set(exclude_lines(clone)):
        if fix:
            for n in drop_exclude(clone, [LINK_DIR]):
                fixed.append(".git/info/exclude: строка %s убрана, доска видна поиску" % n)
        else:
            findings.append(".git/info/exclude прячет %s: поиск редактора читает те же источники "
                            "игнора, что git, и доска за ссылкой перестаёт находиться из окна "
                            "клона; убрать строку: devkitctl doctor --fix" % LINK_DIR)
    state = tree_link_state(clone, local)
    if state != "на месте":
        if state == "занято" and not stale_links(os.path.join(clone, LINK_DIR)):
            findings.append("%s в клоне это свой каталог или файл, а не ссылка на %s: имя занято "
                            "чужим, убрать руками (mv %s %s.bak) и повторить devkitctl doctor "
                            "--fix" % (LINK_DIR, local, os.path.join(clone, LINK_DIR),
                                       os.path.join(clone, LINK_DIR)))
        elif fix:
            done = ensure_tree_link(clone, local)
            if done:
                fixed.append(done)
        else:
            findings.append("нет ссылки %s -> %s в клоне: доска и файлы задач открываются из окна "
                            "клона через неё, и без ссылки их не видят ни поиск, ни сессия; "
                            "положить: devkitctl doctor --fix"
                            % (LINK_DIR, link_target(clone, local)))
    hf = hook_findings(clone, devkit)
    if hf and fix:
        for name, state, chained in ensure_hooks(clone, devkit):
            if state != "на месте":
                fixed.append("хук %s: цепочка %s%s"
                             % (name, state, " (чужой хук в %s.chained)" % name if chained else ""))
    else:
        findings += hf
    if not git(local, "remote"):
        findings.append("у боковой директории %s нет remote: доска пушится в личный приватный "
                        "remote, а не в корп-origin, и без него обрыв машины её теряет" % local)
    repo = tracker_value(local, "repo", devkit)
    if not repo:
        if fix:
            for key, val in ensure_binding(local, clone):
                fixed.append("%s: вписан %s = %s" % (TRACKER, key, val))
        else:
            findings.append("в привязке %s нет ключа repo: по нему потерянная обвязка клона "
                            "находится из боковой директории, вписать путь до %s"
                            % (os.path.join(local, TRACKER), clone))
    findings += prefix_findings(local, devkit)
    findings += sync_findings(local, devkit)
    return findings, fixed
