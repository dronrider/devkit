"""Корп-контур на стороне devkitctl: обвязка клона и её диагностика.

Дома рабочие файлы devkit лежат в дереве проекта, а в корп-клоне их там быть не
должно: доска, файлы задач и .devkit переезжают в боковую директорию
../<проект>-local, а в клоне остаётся обвязка (редирект devkit.local, строка в
.git/info/exclude, обёртки хуков и один тонкий файл контекста харнеса). Вся она
живёт в .git и в негитигнорнутом дереве, поэтому переклонирование и
«git clean -xdf» теряют её молча: клон выглядит обычным проектом без devkit,
хотя доска цела. Отсюда две обязанности модуля, подключение и диагностика, и
подключение же служит восстановлением.

Признак контура тот же, что у утилит devkit (taskctl/corp.go, corpLocal): ключ
devkit.local в конфиге клона. Разбор его не переписывается в третий раз, а
берётся из hooks/corp.py: там он уже переведён на python, и оттуда же приезжает
раскладка цепочки хуков (install_chain).
"""
import importlib.util
import os
import subprocess
import time
from pathlib import Path

TRACKER = os.path.join(".devkit", "tracker.local")
# Отметку последнего pull статусов пишет trackctl sync (trackctl/cmd.go,
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
        repo = tracker_value(d, "repo", devkit)
        if not repo:
            continue
        if not os.path.isabs(repo):
            repo = os.path.join(d, repo)
        if same_path(repo, base):
            return d
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


def ensure_binding(local, clone):
    """Ключ repo в привязке боковой директории: им обвязка клона находится и
    тогда, когда сам клон про devkit уже ничего не знает. Остальные ключи
    привязки заводит человек (trackctl/README.md), и написанное не трогается."""
    path = os.path.join(local, TRACKER)
    text = ""
    if os.path.exists(path):
        with open(path, encoding="utf-8", errors="replace") as f:
            text = f.read()
        for raw in text.split("\n"):
            key, sep, _ = raw.strip().partition("=")
            if sep and key.strip() == "repo":
                return ""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    head = "" if not text or text.endswith("\n") else "\n"
    if not text:
        head = ("# Привязка проекта к трекеру (trackctl/README.md). Ключ repo кладёт\n"
                "# подключение: по нему обвязка клона находится после переклонирования.\n")
    rel = os.path.relpath(clone, local)
    with open(path, "a", encoding="utf-8") as f:
        f.write("%srepo = %s\n" % (head, rel))
    return rel


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
    missing = [n for n in thin_names if n not in set(exclude_lines(clone))]
    if missing:
        if fix:
            for n in ensure_exclude(clone, thin_names):
                fixed.append(".git/info/exclude: спрятан %s" % n)
        else:
            findings.append(".git/info/exclude не прячет %s: файл контекста харнеса обязан лежать "
                            "в корне проекта, и без строки exclude он торчит в чужом git status"
                            % ", ".join(missing))
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
            rel = ensure_binding(local, clone)
            if rel:
                fixed.append("%s: вписан repo = %s" % (TRACKER, rel))
        else:
            findings.append("в привязке %s нет ключа repo: по нему потерянная обвязка клона "
                            "находится из боковой директории, вписать путь до %s"
                            % (os.path.join(local, TRACKER), clone))
    findings += sync_findings(local, devkit)
    return findings, fixed
