#!/usr/bin/env python3
"""Почтальон devkit: донести реплику человека до идущего витка цели.

Реплику с экрана чата дашборд кладёт строкой в раздел «Входящие» файла цели, и
до этого хука её читал только следующий виток. Между двумя витками проходят
десятки минут, а человек, написавший «стой, не туда», всё это время ждёт.
Почтальон стоит на завершённом ходе инструмента, читает почту сессии и вносит
лежащие строки прямо в контекст идущего витка каналом добавки. Решение целиком
в docs/lld/DK-136-live-chat.md, раздел «Устройство почты».

Режим один:
  inbox.py --hook [протокол]   событие читается со stdin и разбирается по имени
                               протокола таблицей hookio.py (голый --hook это
                               claude-code)

Адрес ищется в три шага. Реестр целей ~/.devkit/goals/*.watch называет цель, её
файл и корень; дерево хода берётся из cwd события и сверяется с корнями по
границе пути; сессия сверяется с именем витка из файла session в каталоге
замка, пока замок держит живая оболочка. Ход субагента не получает почты вовсе:
реплика адресована витку, а не тому, кого он позвал.

Живость цели решает, доставлять ли, и признаки идут от точного к грубому. Сам
факт хода значит живую сессию, живой pid в замке значит ведомую цель, а в
режиме чата остаётся метка движения (seen в записи реестра и последняя чужая
строка журнала цикла) с порогом в три часа. Отметку stopped почтальон не читает
ни в одном режиме: сторожок меряет ею движение дерева, а не цели, и у живого
витка, висящего на ожидании исполнителей, она стоит наравне с брошенным.

Отметки доставки лежат в .devkit/goal-<ID>.mail рядом с журналом цикла, а не в
файле цели: docs/tasks пушится коммитом на каждую правку, и хук, дописывающий
файл цели на ходе инструмента, наплодил бы коммитов посреди витка. Ключ отметки
это строка «Входящих» целиком, живёт отметка, пока лежит её строка, и пишется
до доставки: реплика, доставленная в тут же упавшую сессию, полежит во
«Входящих» до следующего витка, а реплика, доставляемая на каждом ходе по
кругу, сожгла бы бюджет цели. Ящик общий с ключом --ask оболочки, который
спрашивает человека и читает те же «Входящие», поэтому запись идёт под flock
соседнего файла .devkit/goal-<ID>.mail.lock: не взявший замок почтальон уходит
тихим нолём, лежащую строку доставит сосед.

Любая беда за развилкой цели (битая запись реестра, нечитаемый файл цели, не
пишется отметка) это тихий ноль и строка в свой журнал ~/.devkit/inbox.log: хук
стоит на каждом ходе чужой работы, и ронять её ему нельзя, а молчать про
непонятое значит держать дыру незаметной.

Переменные окружения:
  DEVKIT_INBOX_TRACE=1   строка в журнал на каждый путь мимо корней целей и на
                         каждый промах имени сессии. Без ключа они молчат:
                         таких ходов на машине десятки за час, и поток строк
                         поверх обещанной дешевизны хука топит нужную строку.
                         Жалоба «почта не доезжает вот из этой сессии»
                         разбирается нарочным прогоном с ключом
"""
import collections
import fcntl
import os
import subprocess
import sys
import time

import hookio

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GOAL_RUN = os.path.join(ROOT, "kit", "skills", "goal-loop", "goal-run.py")

STAMP = "%Y-%m-%dT%H:%M:%S"
# Порог метки движения цели, три часа. Судить «идёт ли виток прямо сейчас» порог
# не годится: самый длинный наблюдавшийся виток шёл час, а между двумя
# поворотами внутри витка проходит и сорок минут. Он ограничивает окно
# брошенной цели, и в этом вся его работа.
MOVED = 3 * 3600
INBOX_HEADER = "## Входящие"
# Подпись дашборда в строке «Входящих»: по ней из строки достаётся текст
# реплики. Рукописная строка идёт в журнал цикла как есть.
FROM_DASHBOARD = ", из дашборда: "
# Начало строки доставки в журнале цикла. Оно же признак своей строки: журнал
# это одна из двух меток живости, а свои строки почтальон не должен считать
# движением цели.
SAY_WORD = "почта"
TEXT_LIMIT = 200
TRACE_ENV = "DEVKIT_INBOX_TRACE"

# Отметка доставки: время, сессия и доставленная строка «Входящих» целиком.
# Ключ это строка, а не текст сообщения: тот же текст, посланный через час
# заново, иначе нашёл бы старую отметку и не доехал бы вовсе.
Mark = collections.namedtuple("Mark", "stamp session line")


def tracing(env=None):
    env = os.environ if env is None else env
    return bool((env.get(TRACE_ENV) or "").strip())


def log_path():
    return os.path.join(os.path.expanduser("~"), ".devkit", "inbox.log")


def log(session, goal, reason):
    """Строка в свой журнал: время, сессия, цель и повод. Поводы перечислены
    поимённо в README, и все они за развилкой «дерево сошлось с корнем цели»,
    так что строк тут ровно столько, сколько на машине живых целей."""
    hookio.append_capped(log_path(), "%s сессия %s цель %s %s\n"
                         % (time.strftime(STAMP), session or "-", goal or "-", reason))


def read_kv(path):
    """Запись реестра целей ключами и значениями. Формат тот же «ключ =
    значение», каким её пишет гейт бюджета (watchRegister, tools/agentctl/watch.go)."""
    data = {}
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            body = f.read()
    except OSError:
        return data
    for line in body.split("\n"):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if sep:
            data[key.strip()] = val.strip()
    return data


def goals():
    """Цели под надзором с их корнями. Корень считается от поля file, а не
    берётся из root: file гейт приводит к абсолютному пути при записи, а root
    пишет как пришёл, и относительный путь под сверку с cwd не годится."""
    d = os.path.join(os.path.expanduser("~"), ".devkit", "goals")
    try:
        names = sorted(os.listdir(d))
    except OSError:
        return []
    out = []
    for name in names:
        if not name.endswith(".watch"):
            continue
        entry = read_kv(os.path.join(d, name))
        goal, path = entry.get("goal", ""), entry.get("file", "")
        if not goal or not os.path.isabs(path):
            continue
        # Файл цели это <корень>/docs/tasks/<ID>.md, отсюда и три шага вверх.
        entry["root"] = os.path.dirname(os.path.dirname(os.path.dirname(path)))
        out.append(entry)
    return out


def border(cwd, root):
    """Путь лежит в корне по границе пути. Подстрокой сюда попало бы соседнее
    дерево задачи, чьё имя начинается с имени корня, а ход исполнителя под
    адрес цели попадать не должен."""
    root = root.rstrip(os.sep)
    return cwd == root or cwd.startswith(root + os.sep)


def under(cwd, root):
    """Дерево хода лежит в корне цели. Сверка сперва текстовая, обе стороны как
    есть, и на живой машине она сходится сразу: корень цели это дерево в
    ~/projects. За ней идёт вторая, по realpath, и нужна она стендам: на macOS
    /tmp и /var/folders это симлинки в /private, харнес приносит cwd уже
    развёрнутым, а запись реестра держит корень таким, каким его назвал вызов.
    Без второй сверки цель, чей корень лежит под временным каталогом, почты не
    получает вовсе, и живой прогон это показал первым же ходом."""
    if not cwd or not root:
        return False
    if border(cwd, root):
        return True
    return border(os.path.realpath(cwd), os.path.realpath(root))


def inbox_lines(doc):
    """Строки раздела «Входящие»: реплики, которых виток ещё не подхватил.
    Разбор тот же, что у дашборда (inboxLines, tools/dashboard/messages.go)."""
    out, inside = [], False
    for line in doc.split("\n"):
        line = line.rstrip(" ")
        if line == INBOX_HEADER:
            inside = True
            continue
        if not inside:
            continue
        if line.startswith("## "):
            break
        if line.startswith("- "):
            out.append(line[2:])
    return out


def read_marks(path):
    """Отметки доставки. Запись это две строки: «время сессия» и сама
    доставленная строка. Строка держится отдельной, потому что в ней живут
    пробелы, ёлочки и что угодно ещё, что написал человек."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            rows = f.read().split("\n")
    except OSError:
        return []
    out = []
    for i in range(0, len(rows) - 1, 2):
        head, line = rows[i].strip(), rows[i + 1]
        if not head or not line:
            continue
        stamp, _, session = head.partition(" ")
        out.append(Mark(stamp, session.strip() or "-", line))
    return out


def marks_body(marks):
    return "".join("%s %s\n%s\n" % (m.stamp, m.session or "-", m.line) for m in marks)


def write_marks(path, marks):
    """Файл отметок переписывается целиком: горизонт у отметки тот же, что у её
    строки «Входящих», значит записью решается и то, какие отметки остаются."""
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(marks_body(marks))
    os.replace(tmp, path)


def take_mail_lock(path):
    """Замок отметок на дескрипторе соседнего файла. Своё имя ему нужно ради
    сверки inode: файл отметок пересоздаётся на каждой записи, и замок на нём
    самом разъезжался бы с путём. None значит, что пишет сосед: ждать своей
    очереди ходу инструмента незачем, лежащую строку доставит он."""
    try:
        fd = os.open(path, os.O_CREAT | os.O_RDWR, 0o644)
    except OSError:
        return None
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if os.fstat(fd).st_ino == os.stat(path).st_ino:
            return fd
    except OSError:
        pass
    os.close(fd)
    return None


def shell_lock(root, goal):
    """Оболочка цели: жива ли она и какому витку выдано имя. Живость меряется
    тем же kill -0 по pid, каким меряет её сама оболочка (lock_busy,
    kit/skills/goal-loop/goal-run.py), так что замок убитой оболочки читается
    как её отсутствие, а не как чужая цель."""
    d = os.path.join(root, ".devkit", "goal-%s.lock" % goal)
    try:
        with open(os.path.join(d, "pid"), encoding="utf-8") as f:
            pid = f.read().strip()
        os.kill(int(pid), 0)
    except (OSError, ValueError):
        return False, ""
    try:
        with open(os.path.join(d, "session"), encoding="utf-8") as f:
            return True, f.read().strip()
    except OSError:
        return True, ""


def stamp_at(text):
    """Время из метки. None значит, что метки нет: отсутствующий файл и пустое
    поле это именно отсутствие, а не нулевое время, иначе цель, которую ведут
    первый раз, оказалась бы протухшей с рождения."""
    try:
        return time.mktime(time.strptime((text or "").strip(), STAMP))
    except (ValueError, OverflowError):
        return None


def journal_at(path):
    """Время последней чужой строки журнала цикла. Чужая тут значит не своя:
    строку доставки почтальон кладёт в тот же журнал, и движением цели она не
    считается, иначе он держал бы цель живой сам за себя."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            rows = f.read().split("\n")
    except OSError:
        return None
    for line in reversed(rows):
        head, _, rest = line.strip().partition(" ")
        if rest.startswith(SAY_WORD):
            continue
        at = stamp_at(head)
        if at is not None:
            return at
    return None


def moved_at(entry, root, goal):
    """Когда цель двигалась в последний раз. Меток две, берётся свежая, и дыры
    у них разные: seen в записи реестра пишет гейт бюджета в начале каждого
    витка и закрывает холодный старт цели, которую ведут в чате впервые, а
    журнал цикла двигается всю дорогу и закрывает виток длиннее порога, где
    seen уже протух."""
    marks = [stamp_at(entry.get("seen", "")),
             journal_at(os.path.join(root, ".devkit", "goal-%s.log" % goal))]
    marks = [m for m in marks if m is not None]
    return max(marks) if marks else None


def ask_until(root, goal):
    """Срок признака ожидания .devkit/goal-<ID>.ask. Пока он не вышел, ящик
    читает ключ --ask оболочки: виток задал человеку прямой вопрос и ждёт
    ответа сам, вторым читателем почтальону там делать нечего. Файла нет или
    срок не разобрался, значит никто не ждёт."""
    try:
        with open(os.path.join(root, ".devkit", "goal-%s.ask" % goal), encoding="utf-8") as f:
            return stamp_at(f.readline())
    except OSError:
        return None


def short(text, limit=TEXT_LIMIT):
    text = " ".join((text or "").split())
    return text if len(text) <= limit else text[:limit] + "..."


def said(line):
    """Что человек написал: подпись дашборда из строки убирается, рукописная
    строка идёт как есть."""
    _, sep, text = line.partition(FROM_DASHBOARD)
    return text if sep else line


def voice(lines):
    """Строка доставки для журнала цикла. Начинается словом «почта»: журнал
    смотрят панелью tmux и tail'ом, и человек видит доставку там же, где смотрит
    ход витка."""
    said_all = ["«%s»" % short(said(line)) for line in lines]
    if len(said_all) == 1:
        return "%s: витку доставлена реплика %s" % (SAY_WORD, said_all[0])
    return "%s: витку доставлены реплики %s" % (SAY_WORD, "; ".join(said_all))


def letter(goal, lines):
    """Текст добавки контекста. Рамки провала у него нет: реплика человека это
    не претензия к ходу инструмента, и повторять ход витку незачем."""
    body = "\n".join("- %s" % line for line in lines)
    return ("Живая реплика человека по цели %s, доставлена посреди витка каналом чата devkit:\n"
            "%s\n"
            "Разряд реплики и реакцию на неё виток выбирает по скиллу goal-loop; во «Входящих» "
            "файла цели строка остаётся до записи витка." % (goal, body))


def say(root, goal, session, lines):
    """Строка доставки в журнал цикла тем же ключом --say, каким пишет ход сам
    виток: формат журнала остаётся в одном месте. Вывод оболочки забирается
    себе, и это не мелочь реализации: --say печатает записанную строку в свой
    stdout, а на stdout у почтальона лежит его единственный ответ, и приехавшая
    туда строка журнала встала бы перед JSON, который читает харнес."""
    try:
        p = subprocess.run(["python3", GOAL_RUN, goal, "-C", root, "--say", voice(lines)],
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    except OSError as e:
        log(session, goal, "строку доставки не записать в журнал цикла: %s" % e)
        return
    if p.returncode != 0:
        log(session, goal, "оболочка не записала строку доставки, код %d: %s"
            % (p.returncode, short(p.stdout)))


def pending(entry, mail):
    """Сколько во «Входящих» лежит непомеченных строк. Читается это без замка:
    решается тут строка в журнал, а не доставка, и устаревшее чтение стоит
    лишней строки, а не потерянной почты."""
    try:
        with open(entry["file"], encoding="utf-8", errors="replace") as f:
            lines = inbox_lines(f.read())
    except OSError:
        return 0
    known = {m.line for m in read_marks(mail)}
    return len([line for line in lines if line not in known])


def miss(entry, turn, named, mail):
    """Промах имени сессии. Под живым замком это обычный ход соседнего окна
    корня, и таких за час десятки, поэтому сам по себе строки он не стоит.
    Строку он получает, когда во «Входящих» есть что доставлять: почта есть,
    адресата нет, и это ровно та жалоба, с которой человек приходит. Названы в
    строке обе стороны, чтобы стык «ID из --session-id доезжает до события»
    разбирался по журналу, а не по догадкам."""
    n = pending(entry, mail)
    if not n and not tracing():
        return
    log(turn.session, entry["goal"],
        "отказ: почта адресована витку %s, а ход идёт сессией %s, непомеченных строк %d"
        % (named or "-", turn.session or "-", n))


def serve(entry, turn, now):
    """Почта одной цели: текст добавки либо None, когда доставлять нечего или
    некому."""
    goal, root = entry["goal"], entry["root"]
    mail = os.path.join(root, ".devkit", "goal-%s.mail" % goal)
    live, named = shell_lock(root, goal)
    if live:
        # Под оболочкой адресат один, виток из файла session. Тут не смотрится
        # никаких меток времени: мерка грубее его не должна перебивать точный
        # признак.
        if not named or turn.session != named:
            miss(entry, turn, named, mail)
            return None
    else:
        at = moved_at(entry, root, goal)
        if at is None:
            log(turn.session, goal, "отказ: в записи реестра нет ни одной метки движения, "
                                    "запись битая")
            return None
        if now - at > MOVED:
            # Цель брошена: работу по ней никто не ведёт, и реплика подождёт
            # человека, а не уедет постороннему окну корня. Строки такому
            # отказу не положено, за неё отвечает дашборд состоянием «ждёт
            # витка», но нарочный разбор её получает.
            if tracing():
                log(turn.session, goal, "отказ: цель не двигалась %d мин при пороге %d мин"
                    % ((now - at) / 60, MOVED / 60))
            return None
    until = ask_until(root, goal)
    if until is not None and until > now:
        log(turn.session, goal, "отказ: ящик держит вопрос витка до %s, отвечает на него --ask"
            % time.strftime(STAMP, time.localtime(until)))
        return None
    fd = take_mail_lock(mail + ".lock")
    if fd is None:
        return None
    try:
        try:
            with open(entry["file"], encoding="utf-8", errors="replace") as f:
                lines = inbox_lines(f.read())
        except OSError as e:
            log(turn.session, goal, "отказ: файла цели не прочитать: %s" % e)
            return None
        marks = read_marks(mail)
        # «Входящие» читаются раньше отметок: отметка, чьей строки там больше
        # нет, не считается вовсе и в новый файл не переносится, поэтому файл
        # не растёт и отдельная уборка ему не нужна.
        lying = set(lines)
        kept = [m for m in marks if m.line in lying]
        known = {m.line for m in kept}
        fresh = [line for line in lines if line not in known]
        if not fresh and len(kept) == len(marks):
            return None
        stamp = time.strftime(STAMP, time.localtime(now))
        # Отметка встаёт до доставки, а не после. Размен сознательный и в
        # пользу потери: доставленная в упавшую сессию реплика лежит во
        # «Входящих» до следующего витка, а доставляемая по кругу превратила бы
        # виток в мельницу.
        try:
            write_marks(mail, kept + [Mark(stamp, turn.session or "-", line) for line in fresh])
        except OSError as e:
            log(turn.session, goal, "отказ: отметку доставки не записать: %s" % e)
            return None
    finally:
        os.close(fd)
    if not fresh:
        return None
    say(root, goal, turn.session, fresh)
    log(turn.session, goal, "доставлено строк %d, первая «%s»"
        % (len(fresh), short(said(fresh[0]))))
    return letter(goal, fresh)


def run_hook(protocol, now=None):
    entries = goals()
    if not entries:
        # Ранний выход до чтения stdin: целей под надзором нет, разбирать
        # событие незачем. Им же держится цена хука на машине, где цикл цели не
        # идёт вовсе, а хук стоит на каждом ходе инструмента.
        return 0
    turn = hookio.tool_event(protocol)
    if turn is None or not turn.cwd:
        return 0
    if turn.agent:
        # Ход субагента: реплика адресована витку, а не тому, кого он позвал.
        # География закрывает не всех (Explore и proofread виток зовёт по
        # своему же дереву), а в событии хода субагента есть роль, и отсев ею
        # честнее.
        return 0
    now = time.time() if now is None else now
    letters, found = [], False
    for entry in entries:
        # Записей на одно дерево бывает и несколько, когда в работе две цели
        # разом: имя сессии разводит витки под оболочкой, а витку в чате
        # достаётся почта обеих, и это одно и то же окно человека.
        if not under(turn.cwd, entry["root"]):
            continue
        found = True
        text = serve(entry, turn, now)
        if text:
            letters.append(text)
    if not found:
        # Путь мимо корней целей это любая чужая сессия машины, включая
        # песочницу харнеса. Тут нет своего правила про песочницу: песочный
        # путь под корень цели не ложится никогда, а лишняя проверка сломала бы
        # стенд, чей корень лежит под временным каталогом.
        if tracing():
            log(turn.session, "-", "путь %s мимо корней целей" % turn.cwd)
        return 0
    if not letters:
        return 0
    return hookio.context(protocol).say("\n\n".join(letters))


def main(argv):
    if not argv or argv[0] != "--hook":
        sys.stderr.write(__doc__)
        return 2
    try:
        return run_hook(hookio.protocol(argv[1:]))
    except hookio.Unknown as e:
        sys.stderr.write("inbox: %s\n" % e)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
