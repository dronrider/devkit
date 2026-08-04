"""Живой замер веса резидента: два прогона claude -p и сверка с расчётом.

Резидент это текст, который едет в каждый запрос сессии, и весит он столько,
сколько показывает харнес, а не столько, сколько насчитали по символам. Отсюда
два числа рядом. Замер: одинаковый тривиальный запрос гоняется дважды, базовый
прогон без раскладки devkit, целевой с ней, а полный вход запроса это
input_tokens плюс запись и чтение кеша; разница двух прогонов и есть цена
текста devkit в токенах. Расчёт: сумма символов по карманам резидента,
переведённая в токены коэффициентом из DK-100. Печатается и то и другое, а
рядом расхождение: расчёт быстрый и бесплатный, но верить ему можно только
сверенному.

Дизайн замера в docs/lld/DK-100-context-tree.md (раздел «Сколько весит
резидент»), порог резидента и его карманы в docs/tasks/DK-029.md.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import rules

# Символов на токен: снято замером DK-100 на русской прозе правил. Тем же
# прогоном коэффициент и перепроверяется, расхождение печатается.
CHARS_PER_TOKEN = 2.45
# Потолок резидента devkit в токенах: из бюджета дизайна DK-100, а не из
# сегодняшнего веса. Тот же порог берёт DK-029 для находки доктора.
LIMIT = 6500
# Бюджет двух карманов резидента, тоже из DK-100: ядро и ядро доски меряются
# отдельно от общего потолка, чтобы находка называла разбухший карман, а не
# только итог.
CORE_LIMIT = 5500
BOARD_LIMIT = 1500
# Второй порог DK-029, на тело скилла: оно не резидентно, но платится записью
# в момент вызова. DK-118 переставила его с сегодняшних тел на ту же меру, что
# у резидента: один вызов скилла стоит сессии не дороже, чем весь резидент,
# который она платит безусловно. Это LIMIT в символах (6 500 * 2,45 = 15 925),
# округлённый вверх до сотен.
SKILL_BODY_LIMIT = 16000
RUNS = 3
PROMPT = "ответь одним словом: готов"
RUN_TIMEOUT = 600
USAGE_KEYS = ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")
# Что из ~/.claude в слепок не едет: транскрипты, история, снимки шелла и
# прочее, чего в запросе нет, а весит оно сотни мегабайт. Индекс памяти уезжает
# вместе с projects/, и это к лучшему: текст там не наш, дизайн считает его
# отдельно от devkit.
SNAP_SKIP = ("projects", "file-history", "shell-snapshots", "sessions",
             "session-env", "todos", "debug", "telemetry", "backups", "cache",
             "statsig", "ide", "history.jsonl")
# Связка ключей macOS: там лежит авторизация клиента, и от HOME он ищет её тут.
KEYCHAIN_DIR = "Library/Keychains"


class WeighError(Exception):
    pass


def read_text(path):
    return Path(path).read_text(encoding="utf-8", errors="replace")


def num(n):
    # Числа группами по три: 14 290 читается глазом, 14290 нет.
    s = "%d" % round(n)
    sign, s = ("-", s[1:]) if s.startswith("-") else ("", s)
    parts = []
    while len(s) > 3:
        parts.insert(0, s[-3:])
        s = s[:-3]
    parts.insert(0, s)
    return sign + " ".join(parts)


def dec(x):
    return ("%.2f" % x).replace(".", ",")


def home_path(spec):
    return Path(os.path.expanduser(spec))


def under(home, spec):
    # Пути машинной раскладки профиль пишет от ~, и в слепке они лежат по тем же
    # относительным путям.
    rel = spec[2:] if spec.startswith("~/") else os.path.relpath(
        os.path.expanduser(spec), os.path.expanduser("~"))
    return Path(home) / rel


def frontmatter(path):
    lines = read_text(path).split("\n")
    if not lines or lines[0].strip() != "---":
        return {}
    out = {}
    for ln in lines[1:]:
        if ln.strip() == "---":
            break
        key, sep, val = ln.partition(":")
        if sep and key.strip() and not key[:1].isspace():
            out[key.strip()] = val.strip()
    return out


def listing_chars(paths):
    # От определения агента и от скилла резидентны имя и описание, тело
    # читается по вызову. Считается то, что лежит в контексте.
    total = 0
    for p in paths:
        fm = frontmatter(p)
        total += len(fm.get("name", "")) + len(fm.get("description", ""))
    return total


def imports_of(path):
    base = Path(path).parent
    out = []
    for ln in read_text(path).split("\n"):
        ln = ln.strip()
        if len(ln) < 2 or not ln.startswith("@") or " " in ln:
            continue
        spec = ln[1:]
        out.append(home_path(spec) if spec.startswith("~") else base / spec)
    return out


def agent_defs(devkit):
    # Определение агента опознаётся по effort в шапке, тем же признаком, каким
    # его отбирает doctor: соседняя проза в agents/ на машину не уезжает и в
    # системном промпте себя не описывает.
    return [p for p in sorted((Path(devkit) / "agents").glob("*.md"))
            if "effort" in frontmatter(p)]


def skill_defs(devkit):
    return sorted((Path(devkit) / "skills").glob("*/SKILL.md"))


def active_profile(root, devkit):
    # Мерится раскладка одного харнеса, того, под которым идёт сессия. Включён
    # обычно один; когда включено несколько, берётся первый, а имя печатается,
    # чтобы читатель видел, чей вес перед ним.
    profiles, findings = rules.enabled_harnesses(root, Path(devkit) / "harness")
    if not profiles:
        raise WeighError("включённых харнесов нет, мерить нечью раскладку: %s"
                         % ("; ".join(findings) or "проверить enabled в конфигах харнесов"))
    return profiles[0]


def fact_depth(root, devkit, profile, board):
    # Глубина правил, доехавшая до харнеса на самом деле: объявленную профилем
    # урезает то, что в devkit ещё не нарезано. Резидентен тот файл, который
    # импортирует тонкий, а не тот, который харнес себе объявил.
    return rules.actual_depth(devkit, root, board, rules.declared_depth(profile)[0])


def pockets(root, devkit, profile):
    """Карманы резидента активного харнеса: список (название, символов).

    Список из дизайна DK-100: глобальная точка правил со своими импортами,
    AGENTS.md проекта, тонкий файл харнеса и файлы правил, которые он тянет,
    листинг определений agents/ и листинг скиллов. Этой же суммой считается
    порог резидента в doctor (DK-029), поэтому расчёт живёт отдельной функцией,
    а не внутри замера.
    """
    root, devkit = Path(root), Path(devkit)
    out, seen = [], set()

    def add(label, path):
        path = Path(path)
        if not path.is_file():
            return
        key = path.resolve()
        if key in seen:
            # Один файл правил приезжает и глобальной точкой, и импортом тонкого
            # файла, а в контексте лежит одной копией.
            return
        seen.add(key)
        out.append((label, len(read_text(path))))

    global_file = profile.str_of("rules", "global_file")
    if global_file:
        point = home_path(global_file)
        add("глобальная точка %s" % global_file, point)
        queue = imports_of(point) if point.is_file() else []
        while queue:
            p = queue.pop(0)
            if p.is_file() and p.resolve() not in seen:
                queue += imports_of(p)
                add("%s (импорт глобальной точки)" % p.name, p)
    add("%s проекта" % rules.AGENTS_FILE, root / rules.AGENTS_FILE)
    # У embed-харнеса правила лежат вклейкой в самом AGENTS.md, и он их уже
    # посчитал: тонкого файла у такого нет, а сложить сверху ещё и файлы правил
    # значило бы заплатить за них дважды.
    if rules.mode_of(profile) == "import":
        thin = profile.str_of("rules", "file")
        if thin:
            add("тонкий %s" % thin, root / thin)
        board = (root / "docs" / "TASKS.md").exists()
        for src in rules.rule_sources(devkit, root, board, fact_depth(root, devkit, profile, board)):
            add("%s (импорт тонкого файла)" % src.name, src)
    if profile.str_of("delegate", "agents_dir"):
        # Харнес без своих субагентов определений не читает, и описывать себя в
        # его системном промпте им негде.
        out.append(("листинг определений agents/", listing_chars(agent_defs(devkit))))
    out.append(("листинг скиллов", listing_chars(skill_defs(devkit))))
    return out


def pockets_breakdown(weights):
    return "; ".join("%s: %s символов (%s токенов)" % (label, num(c), num(c / CHARS_PER_TOKEN))
                      for label, c in weights)


def pocket_cap(label, core_limit, board_limit):
    # Карман опознаётся по имени файла в начале ярлыка: pockets() кладёт его
    # первым словом что при импорте глобальной точкой, что при импорте тонким
    # файлом, а расходиться с DK-100 названию файла было бы странно и без нужды.
    if label.startswith("RULES.core.md "):
        return "ядро", core_limit
    if label.startswith("RULES.board.core.md "):
        return "ядро доски", board_limit
    return None, None


def evaluate_residency(weights, limit=LIMIT, core_limit=CORE_LIMIT, board_limit=BOARD_LIMIT):
    """Находки по готовому списку карманов (label, символов): карман ядра, карман
    ядра доски, итог по всем карманам. Порог доктора (DK-029), расчёт
    статический: сумма символов по карманам DK-100, переведённая в токены тем
    же коэффициентом, каким её сверяет живой замер `weigh`. Логика отделена от
    сбора карманов (`pockets`), чтобы пороги проверялись без файловой системы.
    """
    if not weights:
        return []
    breakdown = pockets_breakdown(weights)
    findings = []
    for label, chars in weights:
        title, cap = pocket_cap(label, core_limit, board_limit)
        if title and chars > cap:
            findings.append(
                "вес резидента, %s (%s): %s символов (%s токенов), порог %s символов; разбивка: %s"
                % (title, label, num(chars), num(chars / CHARS_PER_TOKEN), num(cap), breakdown))
    total_chars = sum(c for _, c in weights)
    total_tokens = total_chars / CHARS_PER_TOKEN
    if total_tokens > limit:
        findings.append(
            "вес резидента devkit: %s токенов (%s символов), потолок %s токенов; разбивка: %s"
            % (num(total_tokens), num(total_chars), num(limit), breakdown))
    return findings


def residency_findings(root, devkit, limit=LIMIT, core_limit=CORE_LIMIT, board_limit=BOARD_LIMIT):
    """Находки веса резидента активного харнеса. Находка не проектная (карманы
    общие для всех проектов devkit), поэтому doctor() зовёт её только для
    самого чекаута devkit, сравнением root с DEVKIT, а не для каждого
    подключённого проекта.
    """
    root, devkit = Path(root), Path(devkit)
    try:
        _, profile = active_profile(root, devkit)
    except WeighError as e:
        return [str(e)]
    return evaluate_residency(pockets(root, devkit, profile), limit, core_limit, board_limit)


def pockets_report(root, devkit, limit=LIMIT, core_limit=CORE_LIMIT, board_limit=BOARD_LIMIT):
    """(строки таблицы карманов, находки) для печати в doctor.

    Вес печатается всегда, не только когда есть находка (DK-029, сценарий
    проверки, шаг 1): иначе распухание было бы видно единственный день, тот, в
    который карман переполз через порог.
    """
    root, devkit = Path(root), Path(devkit)
    try:
        name, profile = active_profile(root, devkit)
    except WeighError as e:
        return [], [str(e)]
    weights = pockets(root, devkit, profile)
    if not weights:
        return [], []
    width = max(len(label) for label, _ in weights)
    lines = ["вес резидента devkit по карманам (харнес %s):" % name]
    for label, c in weights:
        lines.append("  %-*s  %9s симв  %8s ток" % (width, label, num(c), num(c / CHARS_PER_TOKEN)))
    total_chars = sum(c for _, c in weights)
    lines.append("  %-*s  %9s симв  %8s ток"
                 % (width, "итого", num(total_chars), num(total_chars / CHARS_PER_TOKEN)))
    return lines, evaluate_residency(weights, limit, core_limit, board_limit)


def skill_body_chars(path):
    # Резидентны только имя и описание (listing_chars), тело скилла платится
    # записью в момент вызова, и его вес это текст без шапки frontmatter.
    text = read_text(path)
    lines = text.split("\n")
    if lines and lines[0].strip() == "---":
        for i, ln in enumerate(lines[1:], start=1):
            if ln.strip() == "---":
                return len("\n".join(lines[i + 1:]))
    return len(text)


def evaluate_skill_body(name, chars, limit=SKILL_BODY_LIMIT):
    # Логика отделена от чтения файла, как и у карманов резидента: порог
    # проверяется без файловой системы.
    if chars > limit:
        return "тело скилла %s: %s символов, порог %s символов; резать скилл надвое" % (
            name, num(chars), num(limit))
    return None


def skill_findings(devkit, limit=SKILL_BODY_LIMIT):
    """Находки по телу скилла: та же болезнь роста, что и у резидента, в другом месте."""
    findings = []
    for p in skill_defs(devkit):
        f = evaluate_skill_body(p.parent.name, skill_body_chars(p), limit)
        if f:
            findings.append(f)
    return findings


def strip_hooks(settings):
    # Хуки машины на вес резидента не влияют, а под подменённым HOME их пути
    # ведут в никуда, и часть из них ещё и пишет файлы. Из слепка они снимаются,
    # чтобы замер ничего на машине не трогал.
    if not settings.is_file():
        return
    try:
        data = json.loads(read_text(settings))
    except ValueError:
        return
    if isinstance(data, dict) and data.pop("hooks", None) is not None:
        settings.write_text(json.dumps(data, ensure_ascii=False), encoding="utf-8")


def trust_dirs(config, dirs):
    # Незнакомая директория встречает сессию вопросом о доверии, а headless-
    # прогону отвечать на него нечем. Отметка ставится обеим директориям замера
    # разом, в слепке, до первого прогона.
    if not config.is_file():
        return
    try:
        data = json.loads(read_text(config))
    except ValueError:
        return
    if not isinstance(data, dict):
        return
    projects = data.setdefault("projects", {})
    if not isinstance(projects, dict):
        return
    for d in dirs:
        entry = projects.get(str(d))
        if not isinstance(entry, dict):
            entry = {}
        entry["hasTrustDialogAccepted"] = True
        entry["hasCompletedProjectOnboarding"] = True
        projects[str(d)] = entry
    config.write_text(json.dumps(data, ensure_ascii=False), encoding="utf-8")


def link_keychain(src, dst):
    # Авторизация Claude Code на macOS лежит в связке ключей, а ищет её клиент
    # от HOME. Под слепком без этого каталога прогон отвечает «Not logged in» и
    # замер не начинается вовсе. На других платформах каталога нет, и слепок
    # остаётся прежним.
    #
    # Симлинк это единственное место, где слепок смотрит на живой файл машины, и
    # риск тут назван, а не спрятан: освежая истекающий токен, клиент запишет
    # его в настоящую связку. Копия тоже рабочая (проверено: в той же
    # login-сессии скопированная связка открывается и вход из неё читается), но
    # она хуже с обеих сторон. На диске появился бы второй экземпляр всех
    # секретов пользователя, а не только входа в Claude. И освежённый токен из
    # копии уехал бы в мусор вместе с временной директорией, а что после
    # освежения станет со старым, решаем не мы. Через симлинк освежение доезжает
    # туда же, куда доезжает от обычного запуска claude: замер тут не делает
    # ничего, чего не делает сам клиент.
    keys = Path(src) / KEYCHAIN_DIR
    if not keys.is_dir():
        return
    link = Path(dst) / KEYCHAIN_DIR
    link.parent.mkdir(parents=True, exist_ok=True)
    if not os.path.lexists(str(link)):
        link.symlink_to(keys)


def build_home(src, dst, devkit, profile, layout):
    """Слепок ~/.claude во временном HOME, с раскладкой devkit или без неё.

    Пол замера (системный промпт харнеса и схемы тулов) зависит от машины:
    подключённые MCP-серверы приносят свои схемы. Прогон под пустым HOME мерил
    бы не ту машину, поэтому оба прогона идут под одним слепком, и отличается он
    ровно раскладкой devkit: глобальной точкой правил, определениями агентов и
    скиллами.
    """
    src, dst = Path(src), Path(dst)
    claude_src, claude_dst = src / ".claude", dst / ".claude"
    claude_dst.mkdir(parents=True, exist_ok=True)
    for item in sorted(claude_src.iterdir()) if claude_src.is_dir() else []:
        if item.name in SNAP_SKIP:
            continue
        try:
            if item.is_dir():
                shutil.copytree(item, claude_dst / item.name, symlinks=False,
                                ignore_dangling_symlinks=True)
            else:
                shutil.copyfile(item, claude_dst / item.name)
        except (OSError, shutil.Error):
            # Слепок снимается с живой машины, и часть файлов там читается не
            # всегда (сокеты, битые симлинки). Пропуск такого замеру не мешает:
            # оба прогона идут по одному слепку.
            continue
    config = src / ".claude.json"
    if config.is_file():
        shutil.copyfile(config, dst / ".claude.json")
    link_keychain(src, dst)
    strip_hooks(claude_dst / "settings.json")
    if layout:
        return dst
    global_file = profile.str_of("rules", "global_file")
    if global_file:
        point = under(dst, global_file)
        if point.exists():
            point.unlink()
    agents_dir = profile.str_of("delegate", "agents_dir")
    if agents_dir:
        for src_def in agent_defs(devkit):
            gone = under(dst, agents_dir) / src_def.name
            if gone.exists():
                gone.unlink()
    for src_skill in skill_defs(devkit):
        gone = claude_dst / "skills" / src_skill.parent.name
        if gone.is_dir():
            shutil.rmtree(gone)
    return dst


def usage_total(text):
    """Полный вход прогона: свежий вход плюс запись и чтение кеша.

    Резидент лежит в кешируемом префиксе, и его вес виден не в input_tokens, а
    в кеш-полях: считать одно из трёх значило бы мерить пустое место.
    """
    try:
        data = json.loads(text)
    except ValueError:
        raise WeighError("прогон вернул не JSON: %s" % text.strip()[:300])
    if isinstance(data, list):
        results = [d for d in data if isinstance(d, dict) and d.get("type") == "result"]
        data = results[-1] if results else {}
    usage = data.get("usage") if isinstance(data, dict) else None
    if not isinstance(usage, dict):
        raise WeighError("в ответе прогона нет usage: %s" % text.strip()[:300])
    try:
        return sum(int(usage.get(k) or 0) for k in USAGE_KEYS)
    except (TypeError, ValueError):
        raise WeighError("usage прогона не разобран: %s" % json.dumps(usage, ensure_ascii=False))


def run_once(claude, home, cwd, prompt, model):
    env = dict(os.environ, HOME=str(home))
    # Иначе клиент возьмёт конфиг мимо слепка, и раскладка окажется в обоих
    # прогонах.
    env.pop("CLAUDE_CONFIG_DIR", None)
    args = [claude, "-p", prompt, "--output-format", "json"]
    if model:
        args += ["--model", model]
    try:
        p = subprocess.run(args, cwd=str(cwd), env=env, capture_output=True,
                           text=True, timeout=RUN_TIMEOUT)
    except subprocess.TimeoutExpired:
        raise WeighError("прогон claude не уложился в %d секунд" % RUN_TIMEOUT)
    if p.returncode != 0:
        raise WeighError("claude вышел с кодом %d: %s"
                         % (p.returncode, (p.stderr or p.stdout).strip()[:300]))
    return usage_total(p.stdout)


def fill_probe(probe, root, devkit, profile):
    # Целевой прогон идёт не в самом чекауте, а в директории замера с той же
    # раскладкой правил: чекаут принёс бы в контекст ещё и git-статус с
    # листингом файлов, которых у базового прогона нет, и разница вобрала бы их.
    root, devkit = Path(root), Path(devkit)
    agents = root / rules.AGENTS_FILE
    if agents.is_file():
        shutil.copyfile(agents, probe / rules.AGENTS_FILE)
    if rules.mode_of(profile) != "import":
        # У embed-харнеса правила вклеены в сам AGENTS.md, тонкого файла нет.
        return
    board = (root / "docs" / "TASKS.md").exists()
    # Источники и глубина считаются для настоящего корня проекта: devkit себе
    # RULES.md не импортирует, и замер в чужой директории не должен это правило
    # переигрывать. А вот лежать файлы правил обязаны в самой директории замера,
    # и импорт на них идёт голым именем. Импорт наружу клиент не разворачивает
    # молча: правила до целевого прогона не доезжают, и замер выходит занижен
    # ровно на них.
    depth = fact_depth(root, devkit, profile, board)
    local = []
    for src in rules.rule_sources(devkit, root, board, depth):
        if src.is_file():
            shutil.copyfile(src, probe / src.name)
            local.append(probe / src.name)
    # Отличие от боевого тонкого файла остаётся, но только в длине строки
    # импорта: в devkit она и так локальная (@RULES.board.md), а у проекта с
    # соседним devkit вместо @../devkit/RULES.md выходит @RULES.md, дешевле на
    # десяток символов. Против семи тысяч токенов недоехавших правил это шум.
    text = rules.thin_text(profile, probe, devkit, board, embed=False, depth=depth,
                           sources=local)
    (probe / profile.str_of("rules", "file")).write_text(text, encoding="utf-8")


def clear_probe(probe):
    for item in probe.iterdir():
        if item.is_dir():
            shutil.rmtree(item)
        else:
            item.unlink()


def measure(root, devkit, findings, runs=RUNS, limit=LIMIT, prompt=PROMPT, model=""):
    if findings:
        for f in findings:
            print(f)
        sys.stderr.write("раскладка devkit на машине не сходится с чекаутом (находок: %d), "
                         "замер отменён: он мерил бы вчерашнюю раскладку и молчал бы об этом; "
                         "починка devkitctl doctor --fix\n" % len(findings))
        return 2
    claude = shutil.which("claude")
    if not claude:
        sys.stderr.write("claude не в PATH, живой замер гнать нечем\n")
        return 2
    if runs < 1:
        sys.stderr.write("повторов должно быть хотя бы один\n")
        return 2
    root, devkit = Path(root), Path(devkit)
    try:
        name, profile = active_profile(root, devkit)
        weights = pockets(root, devkit, profile)
    except WeighError as e:
        sys.stderr.write("%s\n" % e)
        return 2
    chars = sum(c for _, c in weights)
    print("харнес %s, режим правил %s, повторов %d" % (name, rules.mode_of(profile), runs))
    tmp = Path(tempfile.mkdtemp(prefix="devkit-weigh-"))
    diffs = []
    try:
        probe = tmp / "probe"
        probe.mkdir()
        homes = {}
        for layout, key in ((False, "home-base"), (True, "home-full")):
            homes[layout] = build_home(Path(os.path.expanduser("~")), tmp / key,
                                       devkit, profile, layout)
            trust_dirs(homes[layout] / ".claude.json", [probe])
        for i in range(runs):
            clear_probe(probe)
            base = run_once(claude, homes[False], probe, prompt, model)
            fill_probe(probe, root, devkit, profile)
            full = run_once(claude, homes[True], probe, prompt, model)
            diffs.append(full - base)
            print("прогон %d: без раскладки %s, с раскладкой %s, разница %s"
                  % (i + 1, num(base), num(full), num(full - base)))
    except WeighError as e:
        sys.stderr.write("%s\n" % e)
        return 2
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    measured = sum(diffs) / float(len(diffs))
    spread = max(diffs) - min(diffs)
    if measured <= 0:
        sys.stderr.write("разница вышла неположительной (%s): прогоны шли по одной раскладке "
                         "либо усилия харнеса разъехались, замеру верить нельзя\n" % num(measured))
        return 2
    print("замер: %s токенов, разброс %s (%s%%)"
          % (num(measured), num(spread), dec(100.0 * spread / measured)))
    width = max(len(label) for label, _ in weights)
    print("расчёт по карманам:")
    print("  %-*s  %9s  %8s" % (width, "карман", "символов", "токенов"))
    for label, c in weights:
        print("  %-*s  %9s  %8s" % (width, label, num(c), num(c / CHARS_PER_TOKEN)))
    print("  %-*s  %9s  %8s" % (width, "итого", num(chars), num(chars / CHARS_PER_TOKEN)))
    calc = chars / CHARS_PER_TOKEN
    print("коэффициент этого прогона: %s символа на токен (в расчёте %s)"
          % (dec(chars / measured), dec(CHARS_PER_TOKEN)))
    print("расчёт против замера: %s%s токенов (%s%%)"
          % ("+" if calc >= measured else "", num(calc - measured),
             dec(100.0 * (calc - measured) / measured)))
    if measured > limit:
        print("потолок резидента %s токенов превышен на %s" % (num(limit), num(measured - limit)))
        sys.stderr.write("резидент тяжелее потолка\n")
        return 1
    print("потолок резидента %s токенов, запас %s" % (num(limit), num(limit - measured)))
    return 0
