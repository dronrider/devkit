"""Связи задач, названные прозой в файлах задач, сверяются с доской.

Груминг находит вход задачи в другой задаче и записывает его фразой в файл
задачи. Без маркера «после» у строки связи для диспетчера нет, и задача
берётся в работу раньше своей предпосылки (DK-168: живой случай DK-112
назвал входом DK-117, связь появилась на доске только со второй попытки).
Доктор закрывает дыру находкой: вход назван, а маркера нет ни у одной
из двух строк.
"""

import re
from pathlib import Path

# Строка доски разбирается без taskctl: бинаря на машине может не быть, а
# доктору нужен один заголовок строки. Суффиксы снимаются в порядке taskctl
# (dep.go, splitTitle): «[после ...]» стоит раньше хвостов приёмки, провала
# и блокировки, поэтому маркер оказывается в конце ячейки лишь после их снятия.
ROW_RE = re.compile(r"^\|\s*([A-Z]+-\d+)\s*\|([^|]*)")
DEP_SUF_RE = re.compile(r"\s*\[после ([^\]|]+)\]\s*$")
SUFFIX_RES = (
    re.compile(r"\s*\[блок: [^|\[]*\]\s*$"),
    re.compile(r"\s*\[провал: [^|\[]*\]\s*$"),
    re.compile(r"\s*\[приёмка: (agent|mixed|user)\]\s*$"),
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


def check(root):
    """Находки doctor: вход назван, а маркера «после» нет ни у одной строки.

    Ребро проверяется в обе стороны: фраза называет входом как чужой результат
    («её результат нужен как вход», маркер у своей строки), так и свой чужой
    задаче («читает как вход», маркер у названной), и правильная сторона
    выбирается по смыслу фразы, а не по маске.
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
    return findings
