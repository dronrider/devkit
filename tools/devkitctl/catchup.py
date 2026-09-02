"""Догон чекаута devkit до origin/main на старте сессии.

Клон devkit на машине двигают только релизные теги (`devkitctl update`), а
скиллы и определения субагентов это копии в каталогах харнесов, которые кладёт
`doctor --fix` из чекаута. На машине, где devkit правят веткой, main никто не
подтягивает руками, и правка со второй машины доезжает туда через раз.

Команда закрывает этот разрыв тем же приёмом, что `taskctl catchup` закрывает
отставание бокового дерева доски: SessionStart-хук зовёт её с --hook до того,
как сессия прочла из чекаута устаревшие правила. В режиме --hook каждый отказ
это молчание с кодом 0: хук стоит у всех сессий машины, и рассказывать в каждой,
почему подтягивать нечего, незачем. Без ключа та же развилка печатает причину,
и команду можно позвать руками, чтобы её увидеть.
"""
import contextlib
import io
import subprocess
import time
from pathlib import Path

import say
import update

# Ограничение частоты похода в сеть: fetch на каждом старте сессии стоил бы
# секунды на каждом окне, а правки со второй машины приезжают не чаще. Внутри
# окна отставание всё равно считается, по уже известному указателю.
FETCH_MAX_AGE = 10 * 60
# Потолок самого fetch: сеть бывает недоступна, и старт сессии не должен ждать
# сетевого таймаута git.
FETCH_TIMEOUT = 10
BRANCH = "main"
REMOTE = "origin"
# Каталоги, чья правка не действует сама по себе: скиллы, определения субагентов
# и хуки живут копиями в каталогах харнесов, утилиты бинарями в PATH, и
# раскладывает то и другое доктор.
RELAYOUT_DIRS = ("kit/", "hooks/", "tools/", "internal/")
COMMITS = ("коммит", "коммита", "коммитов")


def main_tree(devkit):
    """Основной чекаут, а не линкованное дерево ветки задачи.

    Дерево задачи стоит на своей ветке, догонять его до origin/main нельзя, и
    main в нём не чекаутится вовсе: она занята основным чекаутом.
    """
    rc, out = update.git(devkit, "rev-parse", "--git-common-dir")
    if rc != 0:
        return False
    common = Path(out.strip())
    if not common.is_absolute():
        common = Path(devkit) / common
    return common.resolve().parent == Path(devkit).resolve()


def has_origin(devkit):
    rc, out = update.git(devkit, "remote")
    return rc == 0 and REMOTE in out.split()


def fetch_fresh(devkit):
    """FETCH_HEAD моложе окна: за свежим указателем только что ходили."""
    path = update.fetch_head(devkit)
    if path is None or not path.exists():
        return False
    return time.time() - path.stat().st_mtime < FETCH_MAX_AGE


def fetch(devkit):
    """Сходить за origin/main. Отдаёт причину отказа либо пустую строку.

    Зовётся не через update.git: там таймаута нет, а без него оторванная сеть
    держит старт сессии столько, сколько отмерит сам git.
    """
    try:
        p = subprocess.run(["git", "-C", str(devkit), "fetch", REMOTE, BRANCH],
                           capture_output=True, text=True, timeout=FETCH_TIMEOUT)
    except subprocess.TimeoutExpired:
        return "%s не ответил за %d секунд" % (REMOTE, FETCH_TIMEOUT)
    if p.returncode != 0:
        return one_line(p.stderr + p.stdout) or "git fetch отказал"
    return ""


def one_line(text):
    """Многострочный отказ git одной строкой: имя файла у него уезжает на вторую,
    и первой строки мало, а старт сессии простыню читать не должен."""
    return " ".join(text.split())


def gap(devkit):
    """Расхождение с origin/main парой чисел: своих коммитов, чужих.

    Отдаёт None, когда указателя origin/main в клоне нет вовсе: считать нечего.
    """
    rc, out = update.git(devkit, "rev-list", "--left-right", "--count",
                         "HEAD...%s/%s" % (REMOTE, BRANCH))
    if rc != 0:
        return None
    parts = out.split()
    if len(parts) != 2:
        return None
    try:
        return int(parts[0]), int(parts[1])
    except ValueError:
        return None


def touched(devkit, old, new):
    """Тронутые диапазоном каталоги из тех, что раскладывает доктор."""
    rc, out = update.git(devkit, "diff", "--name-only", "%s..%s" % (old, new))
    if rc != 0:
        return False
    return any(name.startswith(RELAYOUT_DIRS) for name in out.splitlines())


def relayout(devkit, doctor):
    """Разложить свежий kit по харнесам и отдать строки «починено».

    Доктор рассказывает о себе печатью, и весь его рассказ на старте сессии был
    бы простынёй из находок про машину. Наружу отсюда идёт только сделанное.
    """
    buf = io.StringIO()
    try:
        with contextlib.redirect_stdout(buf):
            doctor(str(devkit), fix=True)
    except Exception as exc:  # noqa: BLE001
        # Подтяг уже случился, и его строка важнее упавшей раскладки: без неё
        # старт сессии не узнает ни о том, ни о другом.
        return ["devkit: раскладка не прошла: %s" % one_line(str(exc))]
    return [ln for ln in buf.getvalue().splitlines() if ln.startswith("починено")]


def _doctor():
    """Доктор из devkitctl, взятый в момент вызова: модуль зовёт этот, а
    импортирует его наоборот devkitctl, и импорт верхнего уровня замкнул бы их
    друг на друга."""
    import devkitctl
    return devkitctl.doctor


def run(devkit, hook=False, doctor=None):
    """Подтянуть чекаут devkit до origin/main и разложить свежий kit."""
    devkit = Path(devkit)
    out = []

    def quiet(reason):
        # Отказ гарда: хуку молчание, человеку причина.
        if not hook:
            out.append("devkit: %s" % reason)
        return _say(out)

    if not main_tree(devkit):
        return quiet("чекаут не основной, догонять дерево ветки нечем")
    branch = update.current_branch(devkit)
    if branch != BRANCH:
        return quiet("HEAD не на ветке %s (%s), подтягивать нечего"
                     % (BRANCH, update.checkout_mode(devkit)))
    if not has_origin(devkit):
        return quiet("remote %s не заведён" % REMOTE)
    if not fetch_fresh(devkit):
        reason = fetch(devkit)
        if reason:
            return quiet("за %s/%s не сходили: %s" % (REMOTE, BRANCH, reason))
    counts = gap(devkit)
    if counts is None:
        return quiet("указателя %s/%s в клоне нет" % (REMOTE, BRANCH))
    ours, theirs = counts
    if not theirs:
        if ours:
            return quiet("main впереди %s/%s на %s, подтягивать нечего"
                         % (REMOTE, BRANCH, say.counted(ours, COMMITS)))
        return quiet("main вровень с %s/%s" % (REMOTE, BRANCH))
    if ours:
        # Видимый сигнал, а не отказ гарда: чужая правка лежит в origin, а своя
        # незапушенная работа в main не даёт её взять, и молчание тут
        # читалось бы как исправный подтяг.
        out.append("devkit: main разошёлся с %s/%s, подтянуть нечего: своих %s, чужих %s"
                   % (REMOTE, BRANCH, say.counted(ours, COMMITS),
                      say.counted(theirs, COMMITS)))
        return _say(out)
    old = update.head_commit(devkit)
    rc, text = update.git(devkit, "merge", "--ff-only", "%s/%s" % (REMOTE, BRANCH))
    if rc != 0:
        out.append("devkit: main не подтянулся: %s"
                   % (one_line(text) or "git merge отказал"))
        return _say(out)
    new = update.head_commit(devkit)
    out.append("devkit подтянут до %s, %s" % (new[:7], say.counted(theirs, COMMITS)))
    if touched(devkit, old, new):
        out += relayout(devkit, doctor or _doctor())
    return _say(out)


def _say(lines):
    for line in lines:
        print(line)
    return 0
