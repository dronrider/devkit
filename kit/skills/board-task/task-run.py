#!/usr/bin/env python3
"""Оболочка конвейера задачи: поднимает голову проходами, пока строка задачи не
закрыта. Живучесть конвейера от живучести сессии тут не зависит, как и у цикла
цели (goal-run.py): состояние задачи целиком лежит на диске, доска, git и дерево
задачи, а оболочка только зовёт следующий проход и решает, когда перестать
звать.

  task-run.py <ID> [-C <корень проекта>] [--order <заказ первого прохода>]
              [--again <заказ следующих>] [--passes N] [--project <имя>]
              -- <команда клиента без -p>

Заводится оболочка тем, что головой конвейера была печатная сессия: конец её
хода это выход процесса, и всё, чего голова ждала, гибнет вместе с ней. Так
01.09 встали обе сессии конвейера, DK-655 на исполнителе и DK-658 на фоновом
прогоне, а окна tmux просто пропали с экрана. Рубеж синхронности (DK-678,
починка признака в DK-691) отбивает саму отдачу работы в фон, а тут закрыт
второй случай: работа отдана наружу законно (человеку вопросом, сроку паузой)
либо голова кончила ход раньше, чем задачу, и подъём следующего прохода не
должен стоить человеку ручного присмотра.

Заказ проходу оболочка не сочиняет: слова приходят флагами от того, кто её
позвал, и лежат в одном месте (у дашборда это runPrompt). Умолчание держится
только на случай ручного запуска из терминала.

Проход кончается выходом клиента, каким бы он ни был: код возврата тут не
вердикт, вердикт это статус строки на доске. Закрытая задача останавливает
цикл, запаркованная тоже (её ждёт человек), а незакрытая поднимает следующий
проход, и строка о выходе головы уходит в журнал утилит .devkit/log.

Коды возврата: 0 штатный стоп (задача закрыта, запаркована или ждёт приёмки),
1 стоп оболочки (проходы исчерпаны, воронка), 2 ошибка вызова или окружения.
"""
import os
import shlex
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
NOTIFIER = os.path.normpath(os.path.join(HERE, "..", "..", "..", "hooks", "notify.py"))
# Проходов подряд, после которых оболочка встаёт сама. Потолок нужен не от
# осторожности: голова, которая выходит и не двигает строку, крутила бы цикл до
# конца квоты, а разбирать такое человеку надо со свежей задачей, а не с сотней
# проходов позади.
PASS_LIMIT = 6
# Пустой проход это выход головы без движения строки и быстрее этого срока:
# сессия, упавшая на подъёме (нет логина, кончилась квота, чужой флаг), уходит
# за секунды, и долбить в неё шесть раз незачем.
IDLE_SECONDS = 90
IDLE_LIMIT = 3
# Пауза между проходами: обрыв на стороне API её переживает, а человек за
# экраном успевает прочесть строку о выходе головы.
PASS_PAUSE = 10
PAUSE_ENV = "DEVKIT_TASK_PASS_PAUSE"
STAMP = "%Y-%m-%dT%H:%M:%S"
# Статусы, на которых цикл кончается сам. Архив это закрытая задача, blocked
# парковка: и в том, и в другом случае поднимать голову больше не за чем.
DONE = "архиве"
PARKED = "blocked"

USAGE = __doc__


def die(text, code=2):
    sys.stderr.write("task-run: %s\n" % text)
    sys.exit(code)


def parse_args(argv):
    """Разбор своими руками, а не argparse: хвост после `--` это чужая команда
    целиком, включая её собственные `--`, и отдавать её разбор библиотеке
    нельзя."""
    if "--" in argv:
        cut = argv.index("--")
        head, client = argv[:cut], argv[cut + 1:]
    else:
        head, client = argv, []
    if not head or head[0].startswith("-"):
        die(USAGE)
    opts = {"id": head[0], "proj": os.getcwd(), "order": "", "again": "",
            "passes": PASS_LIMIT, "project": "", "client": client}
    rest = head[1:]
    keys = {"-C": "proj", "--order": "order", "--again": "again",
            "--project": "project", "--passes": "passes"}
    while rest:
        key = rest[0]
        if key not in keys:
            die("неизвестный ключ %s\n\n%s" % (key, USAGE))
        if len(rest) < 2:
            die("ключу %s нужно значение" % key)
        opts[keys[key]] = rest[1]
        rest = rest[2:]
    try:
        opts["passes"] = max(1, int(opts["passes"]))
    except ValueError:
        die("--passes ждёт число, а пришло %s" % opts["passes"])
    if not opts["order"]:
        opts["order"] = "Продолжай выполнение " + opts["id"]
    if not opts["again"]:
        opts["again"] = opts["order"]
    if not opts["client"]:
        opts["client"] = ["claude", "--permission-mode", "auto"]
    opts["proj"] = os.path.abspath(opts["proj"])
    if not os.path.isdir(opts["proj"]):
        die("корень проекта %s не каталог" % opts["proj"])
    if not opts["project"]:
        opts["project"] = os.path.basename(opts["proj"])
    return opts


class Pipeline:
    def __init__(self, opts):
        self.id = opts["id"]
        self.proj = opts["proj"]
        self.project = opts["project"]
        self.order = opts["order"]
        self.again = opts["again"]
        self.passes = opts["passes"]
        self.client = opts["client"]
        # Имя окна везёт та же переменная, которой поднятая сессия называет себя
        # в реестре: своего имени у оболочки нет, она живёт внутри этого окна.
        self.sess = os.environ.get("DEVKIT_TMUX", "") or "без окна"

    # -- состояние доски ----------------------------------------------------

    def show(self):
        """Строка задачи словами taskctl. Пусто значит, что спросить не вышло, и
        такой ответ разбирается наверху отдельно: слепая оболочка не должна
        поднимать голову вслепую."""
        try:
            p = subprocess.run(["taskctl", "show", self.id], cwd=self.proj,
                               capture_output=True, text=True)
        except OSError:
            return ""
        if p.returncode != 0:
            return ""
        return p.stdout

    def status(self, said):
        """Секция строки из первого слова после «в». Формат этот taskctl печатает
        человеку («DK-691 в in-progress», «DK-678 в архиве (закрыта ...)»), и
        другого машинного ответа про секцию у него нет."""
        first = (said or "").split("\n")[0].strip()
        mark = " в "
        if mark not in first:
            return ""
        return first.split(mark, 1)[1].split()[0].strip()

    def waits_user(self, said, sect):
        """Проверенная задача с пользовательской приёмкой: закрывает её человек
        с экрана, и голова тут ничего не сделает."""
        return sect == "check" and "пользовательск" in said

    # -- голос наружу -------------------------------------------------------

    def say(self, msg):
        sys.stdout.write("%s task-run %s: %s\n" % (time.strftime(STAMP), self.id, msg))
        sys.stdout.flush()

    def log(self, text, code):
        """Строка в журнал утилит .devkit/log, тем же форматом, каким пишут туда
        taskctl и agentctl. Естественный выход головы до этой задачи не писал
        никто: 39 строк «agentctl exec 0» с 26.08 не отличались друг от друга и
        не говорили ни про задачу, ни про её статус."""
        dirp = os.path.join(self.proj, ".devkit")
        if not os.path.isdir(dirp):
            return
        try:
            with open(os.path.join(dirp, "log"), "a", encoding="utf-8") as f:
                f.write("%s\ttask-run\t%s\t%d\n" % (time.strftime(STAMP), text, code))
        except OSError as e:
            self.say("строка журнала не записана (%s)" % e)

    def shout(self, reason, title, text):
        """Позвать человека уведомителем. Стоп конвейера молчать не должен: окно
        закрылось, а с экрана дашборда это неотличимо от штатного конца."""
        if not os.path.isfile(NOTIFIER):
            self.say("уведомитель не нашёлся (%s), стоп остался без строки в ленте" % NOTIFIER)
            return
        cmd = ["python3", NOTIFIER, "--reason", reason, "--task", self.id,
               "--project", self.project, title, text]
        try:
            subprocess.run(cmd, cwd=self.proj, capture_output=True, text=True)
        except OSError as e:
            self.say("уведомление не отправлено (%s)" % e)

    def stop(self, code, sect, why, reason="", loud=True):
        self.say("стоп: %s (задача в %s)" % (why, sect or "неизвестно где"))
        self.log("конвейер %s встал: %s, %s в %s" % (self.sess, why, self.id, sect or "неизвестно где"), code)
        if loud:
            self.shout(reason or "run_stop",
                       "%s: %s конвейер встал" % (self.project, self.id), why)
        sys.exit(code)

    # -- проход -------------------------------------------------------------

    def run_pass(self, order):
        """Один подъём головы. Вывод клиента идёт в панель как есть, не через
        оболочку: панель сессии читают глазами, и прятать ход работы в буфер
        оболочки значило бы гасить экран на всё время прохода."""
        cmd = list(self.client) + ["-p", order]
        self.say("проход поднят: %s" % " ".join(shlex.quote(c) for c in cmd[:-1]))
        started = time.time()
        try:
            p = subprocess.run(cmd, cwd=self.proj)
        except OSError as e:
            die("клиент не поднялся (%s): %s" % (e, " ".join(self.client)))
        return p.returncode, time.time() - started

    def pause(self):
        try:
            return max(0, int(os.environ.get(PAUSE_ENV, "")))
        except ValueError:
            return PASS_PAUSE

    def run(self):
        idle = 0
        for n in range(1, self.passes + 1):
            said = self.show()
            if not said:
                die("taskctl не сказал про %s ничего: доски тут нет либо строка "
                    "пропала, поднимать голову вслепую нельзя" % self.id)
            sect = self.status(said)
            if sect == DONE:
                self.stop(0, sect, "задача закрыта", loud=False)
            if sect == PARKED:
                self.stop(0, sect, "задача запаркована и ждёт человека", reason="wait_human")
            if self.waits_user(said, sect):
                self.stop(0, sect, "задача ждёт приёмки человеком", reason="task_check")
            code, spent = self.run_pass(self.order if n == 1 else self.again)
            after = self.status(self.show()) or sect
            # Строка о выходе головы пишется всегда, а не только на аварии: до
            # этой задачи естественный выход не писал никто, и пропажу окна
            # разбирали по тому, что успел сделать покойник.
            self.log("выход головы конвейера %s, %s в %s, проход %d из %d, %d с"
                     % (self.sess, self.id, after, n, self.passes, int(spent)), code)
            self.say("голова вышла кодом %d за %d с, задача в %s" % (code, int(spent), after))
            if after == DONE:
                self.stop(0, after, "задача закрыта", loud=False)
            if after == PARKED:
                self.stop(0, after, "задача запаркована и ждёт человека", reason="wait_human")
            # Воронка: голова выходит быстро и строку не двигает. Обычно это
            # сломанное окружение (нет логина, кончилась квота, чужой флаг), и
            # шесть попыток тут ничем не лучше трёх.
            if after == sect and spent < IDLE_SECONDS:
                idle += 1
                self.say("проход %d прошёл вхолостую (%d с, строка на месте), подряд таких %d из %d"
                         % (n, int(spent), idle, IDLE_LIMIT))
                if idle >= IDLE_LIMIT:
                    self.stop(1, after, "%d прохода подряд вхолостую, конвейер жжёт бюджет" % idle)
            else:
                idle = 0
            if n < self.passes:
                time.sleep(self.pause())
        self.stop(1, self.status(self.show()), "проходы исчерпаны (%d), задача не закрыта" % self.passes)


def main(argv):
    if not argv or argv[0] in ("-h", "--help"):
        sys.stdout.write(USAGE)
        return 0
    Pipeline(parse_args(argv)).run()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
