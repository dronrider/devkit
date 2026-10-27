"""Связи задач, названные прозой в файлах задач, сверяются с доской.

Груминг находит вход задачи в другой задаче и записывает его фразой в файл
задачи. Без маркера «после» у строки связи для диспетчера нет, и задача
берётся в работу раньше своей предпосылки (DK-168: живой случай DK-112
назвал входом DK-117, связь появилась на доске только со второй попытки).
Доктор закрывает дыру находкой: вход назван, а маркера нет ни у одной
из двух строк.

Второй рубеж про состав цели (DK-994): приписка и удаление снимают черновик,
а упомянутый в чужих текстах ID остаётся и в разделе «Задачи цели» читается
членом состава, которого нет. Экран цели подписывает его незаведённой
строкой, а прогресс цели занижен. Членом считается ID со скобкой судьбы,
как в разборе дашборда, и находка зовётся на члена без строки доски, строки
архива и файла черновика.
"""

import re
from pathlib import Path

# Строка доски разбирается без taskctl: бинаря на машине может не быть, а
# доктору нужен один заголовок строки. Суффиксы снимаются в порядке taskctl
# (dep.go, splitTitle): «[после ...]» стоит раньше хвостов долга, взвода,
# срока, приёмки, провала и блокировки, поэтому маркер оказывается в конце ячейки
# лишь после снятия всех хвостов. Пропущенный в наборе хвост прячет маркер
# целиком, и находка про названный вход слепнет на живой строке.
ROW_RE = re.compile(r"^\|\s*([A-Z]+-\d+)\s*\|([^|]*)")
DEP_SUF_RE = re.compile(r"\s*\[после ([^\]|]+)\]\s*$")
SUFFIX_RES = (
    re.compile(r"\s*\[блок: [^|\[]*\]\s*$"),
    re.compile(r"\s*\[провал: [^|\[]*\]\s*$"),
    re.compile(r"\s*\[приёмка: (agent|mixed|user)\]\s*$"),
    re.compile(r"\s*\[срок: [0-9]{4}-[0-9]{2}-[0-9]{2}\]\s*$"),
    re.compile(r"\s*\[взвод(?:: [^|\][]+)?\]\s*$"),
    re.compile(r"\s*\[долг\]\s*$"),
)

# Фраза, называющая вход, опознаётся по двум устойчивым формам: «как вход»
# (живой случай DK-112: «её результат нужен как вход») и «вход этой задачи».
# Маска дальше не расширяется: «вход разговора» и «вход в проект» это точка
# входа, а не зависимость, «входит в цель» это глагол.
INPUT_PHRASE_RE = re.compile(r"как вход\b|вход этой задачи")
ID_RE = re.compile(r"\b[A-Z]+-\d+\b")

# Цитата в «ёлочках» не называет вход: журнал задачи цитирует чужие фразы
# (находки doctor, разборы), и маска внутри цитаты бьёт файл задачи по нему
# самому. Настоящие формулировки в корпусе стоят вне кавычек.
QUOTE_RE = re.compile(r"«[^«»]*»")

# Раздел состава живёт в файле цели, и находка про фантома читает его тем же
# заголовком, что дашборд и журнал витка.
GOAL_TASKS_HEADER = "## Задачи цели"


def row_deps(title):
    """Список зависимостей из ячейки заголовка, разбор как в taskctl."""
    for suf in SUFFIX_RES:
        title = suf.sub("", title)
    m = DEP_SUF_RE.search(title)
    if not m:
        return []
    return [d.strip() for d in m.group(1).split(",") if d.strip()]


def board_rows(board_path):
    """Живые строки доски: ID и его список «после», архив не читается."""
    rows = {}
    for ln in board_path.read_text(encoding="utf-8", errors="replace").splitlines():
        m = ROW_RE.match(ln)
        if m:
            rows[m.group(1)] = row_deps(m.group(2))
    return rows


def named_inputs(text, own, rows):
    """Живые задачи, названные входом в каком-нибудь абзаце файла задачи.

    Абзац, а не строка: проза переносится, и фраза про вход часто едет
    строкой ниже названного ID. Живые только: вход из закрытой задачи
    диспетчеру уже не мешает. Цитаты в «ёлочках» вырезаются до поиска:
    пересказ чужой фразы это не называние входа.
    """
    named = set()
    for para in re.split(r"\n\s*\n", text):
        bare = QUOTE_RE.sub(" ", para)
        if not INPUT_PHRASE_RE.search(bare):
            continue
        named.update(tid for tid in ID_RE.findall(bare) if tid != own and tid in rows)
    return named


def goal_units(text):
    """Юниты раздела «Задачи цели»: пункт списка со своими переносами и
    отдельный абзац прозы. Состав пишется и списком, и абзацем, и построчный
    разбор терял бы половину членов на переносах, поэтому строки склеиваются.
    """
    units, cur = [], []
    for ln in text.splitlines():
        t = ln.strip()
        if not t:
            if cur:
                units.append(" ".join(cur))
                cur = []
            continue
        if t.startswith("- "):
            if cur:
                units.append(" ".join(cur))
                cur = []
        cur.append(t)
    if cur:
        units.append(" ".join(cur))
    return units


def goal_members(text, own):
    """ID-члены состава из текста файла цели по правилу дашборда: член это ID,
    открывающий пункт списка либо названный со скобкой судьбы, включая
    перечисление через запятую перед общими скобками. Упомянутые вскользь ID
    («ждёт BD-7», «остаётся черновиком BD-8 вне цели») не члены, и фантомом
    они не становятся. ID самой цели пропускается: раздел ссылается и на неё.
    """
    members = set()
    for unit in goal_units(text):
        hits = [(m.start(), m.end()) for m in ID_RE.finditer(unit)]
        member = [False] * len(hits)
        # Справа налево: перечисление узнаёт членство у правого соседа.
        for i in range(len(hits) - 1, -1, -1):
            start, end = hits[i]
            tail = unit[end:].lstrip()
            if (unit.startswith("- ") and start == 2) or tail.startswith("("):
                member[i] = True
            elif tail.startswith(",") and i + 1 < len(hits) and member[i + 1]:
                member[i] = True
        for (start, end), is_member in zip(hits, member):
            if is_member and unit[start:end] != own:
                members.add(unit[start:end])
    return members


def goal_findings(root, rows):
    """Находки doctor: член состава цели, под которым нет ни строки доски, ни
    строки архива, ни файла черновика (DK-994). Приписанный или удалённый
    черновик оставляет ID в разделе «Задачи цели», экран цели читает его
    задачей и прогресс врёт, а без находки мёртвая ссылка живёт, сколько
    живёт файл цели.
    """
    tasks_dir = Path(root) / "docs" / "tasks"
    archived = set()
    archive_path = Path(root) / "docs" / "TASKS-archive.md"
    if archive_path.is_file():
        for ln in archive_path.read_text(encoding="utf-8", errors="replace").splitlines():
            m = ROW_RE.match(ln)
            if m:
                archived.add(m.group(1))
    drafts = {p.stem for p in (tasks_dir / "drafts").glob("*.md")}
    findings = []
    for path in sorted(tasks_dir.glob("*.md")):
        text = path.read_text(encoding="utf-8", errors="replace")
        head, _, body = text.partition("\n" + GOAL_TASKS_HEADER + "\n")
        if not _:
            continue
        section = body.split("\n## ", 1)[0]
        for tid in sorted(goal_members(section, path.stem)):
            if tid in rows or tid in archived or tid in drafts:
                continue
            findings.append("docs/tasks/%s.md: %s названа в составе цели, а строки нет "
                            "ни на доске, ни в архиве, ни в накопителе; переписать "
                            "упоминание на новый адрес либо снять его"
                            % (path.stem, tid))
    return findings


def check(root):
    """Находки doctor: вход назван, а маркера «после» нет ни у одной строки;
    член состава цели без строки, архива и черновика.

    Ребро проверяется в обе стороны: фраза называет входом как чужой результат
    («её результат нужен как вход», маркер у своей строки), так и свой чужой
    задаче («читает как вход», маркер у названной), и правильная сторона
    выбирается по смыслу фразы, а не по маске. Состав цели проверяется тем же
    списком находок: переписывать его команда не берётся, места и члены
    называются, а правит текст грумер.
    """
    board_path = Path(root) / "docs" / "TASKS.md"
    if not board_path.is_file():
        return []
    rows = board_rows(board_path)
    tasks_dir = Path(root) / "docs" / "tasks"
    findings = []
    for own in sorted(rows):
        path = tasks_dir / ("%s.md" % own)
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        for other in sorted(named_inputs(text, own, rows)):
            if other in rows[own] or own in rows[other]:
                continue
            findings.append("docs/tasks/%s.md: %s названа входом, а маркера «после» нет "
                            "ни у одной из строк; поставить связь taskctl dep add"
                            % (own, other))
    findings += goal_findings(root, rows)
    return findings
