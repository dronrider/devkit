"""Тесты стенда трёх ожиданий (DK-936).

Стенд гоняется по подставному PATH с настоящими бинарями, собранными из копии
devkit: заглушки ни доску не двигают, ни голову не поднимают, а проверяется
связка ожиданий в движении. Как и у SelfcheckTest, это тяжёлый класс сюиты,
бинари собираются один раз на класс.

Зелёный прогон тут один, а красных три: стенд обязан краснеть от отката любого
из трёх звеньев, и молчаливое «всё прошло» на сломанном ожидании было бы хуже
отсутствия стенда.
"""
import ast
import json
import os
import shutil
import tempfile
import threading
import time
import unittest
from pathlib import Path

import testenv
import waitcheck
import watch
from testenv import SandboxCase, go_cache_env, run, write

TOOLS = ("taskctl", "shipctl", "agentctl", "regcheck")


def span_secs(span):
    """Срок отметки в секундах, разбор тех же видов, что принимает утилита."""
    unit = span[-1:]
    return int(span[:-1]) * {"s": 1, "m": 60, "h": 3600}[unit]


class WaitMarksTest(unittest.TestCase):
    """Форма отметок ожидания (DK-1694): событие вместо гонки с истечением
    срока.

    Синхронизация шла сроком в пять секунд, и под нагрузкой полного прогона
    оболочка считывала отметку позже срока: проход считался холостым, и стенд
    недосчитывался ожидания. Отметки разбираются из клиента, которого печатает
    stubs, без подъёма бинарей.
    """

    def marks(self):
        where = Path(tempfile.mkdtemp(prefix="waitcheck-marks-"))
        self.addCleanup(shutil.rmtree, str(where), True)
        waitcheck.stubs(where / "bin", where / "tmux", where / "режим",
                        where / "proj", where / "головы")
        text = (where / "bin" / "claude").read_text(encoding="utf-8")
        for ln in text.splitlines():
            if ln.startswith("MARKS = "):
                return ast.literal_eval(ln[len("MARKS = "):].strip())
            if ln.startswith("MARK = "):
                # прежняя форма: одна отметка на все проходы. Разбор нужен,
                # чтобы на базе тест падал поведением, а не отсутствием
                # списка.
                return [ast.literal_eval(ln[len("MARK = "):].strip())]
        self.fail("клиент стенда не получил список отметок")

    def test_marks_wait_for_unique_event_files(self):
        # Вид «есть» с абсолютным путём-событием на каждый проход: в кортеже
        # дедупликации различаются цели, и отметки одной секунды не считаются
        # дубликатами. Запас в одну отметку переживает потерю любой из них.
        marks = self.marks()
        self.assertEqual(len(marks), waitcheck.WAIT_PASSES + 1,
                         "запаса отметок нет")
        targets = []
        for argv in marks:
            self.assertEqual(argv[:3], ["agentctl", "wait", waitcheck.FUNNEL],
                             "отметка стоит не утилитой ожидания")
            self.assertEqual(argv[3], "есть",
                             "отметка кончается часом, а не событием")
            self.assertTrue(os.path.isabs(argv[4]),
                            "цель события не абсолютна: %s" % argv[4])
            targets.append(argv[4])
        self.assertEqual(len(set(targets)), len(targets),
                         "цели событий повторяются: %s" % targets)

    # Голодание читателя из журнала прогона (DK-1694): отметка легла в
    # 12:59:22, а оболочка под нагрузкой полного прогона считала её в
    # 12:59:28, на шестой секунде. Пятисекундный срок к тому чтению уже
    # истёк, и проход стал холостым.
    STARVED_READ_SECS = 6

    def test_mark_survives_the_starved_reader(self):
        # Срок страховки обязан переживать голодание читателя, случившееся
        # в журнале, и кончаться событием: тогда опоздавшая к сроку отметка
        # всё равно снимает ожидание проходом.
        marks = self.marks()
        argv = marks[0]
        i = argv.index("--until")
        self.assertGreater(span_secs(argv[i + 1]), self.STARVED_READ_SECS,
                           "отметка истекает до чтения при голодании "
                           "оболочки в шесть секунд")
        self.assertEqual(argv[3], "есть",
                          "голодевший читатель обязан выйти событием, а не "
                          "сроком")

    def test_watcher_ends_the_wait_by_the_event(self):
        # Стенд завершает ожидание созданием файла-события: нить наблюдателя
        # кладёт файл прохода, увидев свежую отметку. Проверяется без бинарей,
        # отметка лежит файлом того же вида, каким её кладёт agentctl wait.
        watcher = getattr(waitcheck, "wake_waits", None)
        if watcher is None:
            self.fail("нити наблюдателя нет: ожидание кончается часом, "
                      "а не событием")
        where = Path(tempfile.mkdtemp(prefix="waitcheck-wake-"))
        self.addCleanup(shutil.rmtree, str(where), True)
        events = where / "события"
        mark_path = where / "WT-001.json"
        mark_path.write_text(json.dumps({
            "task": waitcheck.FUNNEL, "kind": "есть",
            "target": str(events / "проход-1"),
            "since": "2026-11-12T12:59:22", "until": "2026-11-12T13:00:22",
            "note": "стенд ждёт соседа"}, ensure_ascii=False), encoding="utf-8")
        stop = threading.Event()
        thread = threading.Thread(target=watcher, args=(mark_path, stop),
                                  daemon=True)
        self.addCleanup(thread.join, 10)
        self.addCleanup(stop.set)
        thread.start()
        deadline = time.time() + 10
        while time.time() < deadline and not (events / "проход-1").exists():
            time.sleep(0.02)
        self.assertTrue((events / "проход-1").exists(),
                        "наблюдатель не положил файл-событие прохода")


class WaitcheckTest(SandboxCase):
    """Три ожидания на временном репозитории: воронка, обход, тик."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Автор коммитов: подключение делает первый коммит, а в чистом CI git
        # его не угадывает.
        write(cls.box.home / ".gitconfig", "[user]\n\tname = t\n\temail = t@t\n")
        cls.bin = cls.box.root / "standbin"
        cls.bin.mkdir()
        for tool in TOOLS:
            env = go_cache_env()
            env["GOWORK"] = "off"
            rc, out = run(["go", "build", "-o", str(cls.bin / tool), "."],
                          cwd=str(cls.box.dk / "tools" / tool), env=env)
            assert rc == 0, "%s не собрался: %s" % (tool, out)
        cls.path = "%s:%s:%s:%s" % (cls.bin, cls.box.dkbin, cls.box.bin, cls.box.sys)

    def stand(self, name, sabotage=None):
        """Прогон стенда в своём каталоге. Возврат это код и отчёт строками."""
        where = self.box.root / name
        where.mkdir()
        self.where = where
        lines = []

        def cmd_run(args, cwd=None, env=None):
            if sabotage:
                sabotage(args, cwd)
            return testenv.run(args, cwd=cwd, env=env, path=self.path,
                               home=self.box.home)

        rc = waitcheck.main(cmd_run=cmd_run, where=where, log=lines.append,
                            path=self.path, devkit=self.box.dk)
        return rc, "\n".join(lines)

    def test_three_waits_pass(self):
        rc, out = self.stand("stand-green")
        self.assertEqual(rc, 0, "три ожидания не прошли:\n%s" % out)
        for name in ("живое ожидание против воронки", "close и merge поднимают ждущих",
                     "тик добирает пропущенное"):
            self.assertIn("ожидание «%s»: ок" % name, out,
                          "ожидание «%s» не названо исходом:\n%s" % (name, out))
        self.assertIn("три ожидания прошли", out, "отчёт не назвал итог")
        # Каждая строка исхода говорит не «ок», а чем ожидание снялось: по
        # голому «ок» разбирать провал в ночном журнале нечем.
        self.assertIn("проходов 6, из них ожиданий 5", out,
                      "исход воронки не назвал счёт проходов:\n%s" % out)
        self.assertIn("слияние %s подняло %s" % (waitcheck.DEP, waitcheck.ARMED), out,
                      "исход обхода не назвал взведённую строку:\n%s" % out)
        self.assertIn("тик добрал слияние %s" % waitcheck.HAND, out,
                      "исход тика не назвал пропущенное событие:\n%s" % out)
        # Голова, пережившая свой шаг, идёт вторым git в том же репозитории:
        # `taskctl show` меряет возраст строки через `git status`, тот берёт
        # .git/index.lock, и следующий коммит стенда падает с «Another git
        # process seems to be running». Так прогон и флакал. Стенд ждёт конца
        # каждой поднятой головы, и к отчёту ни одного замка остаться не должно.
        self.assertNotIn("головы стенда не закончили", out,
                         "стенд отчитался при живых головах:\n%s" % out)
        self.assertEqual(waitcheck.locks(self.where / "home"), [],
                         "за стендом остался замок головы")

    def test_audit_and_merge_share_rules_source(self):
        # Отчёт аудита ревью и ворота слияния обязаны читать один чекаут
        # правил: инвентарь снимается у источника аудита, а слияние сверяет
        # отчёт по своему DEVKIT_HOME. Дописываю в копию стенда правило с
        # устойчивым ID, которого нет у чекаута, откуда запущен сам тест:
        # разошедшиеся источники обязаны уронить слияние на изменённом
        # источнике.
        rules = self.box.dk / "RULES.style.md"
        was = rules.read_text(encoding="utf-8")
        rules.write_text(was + "\n- **STYLE-990.** Синтетическое правило различения"
                         " источников аудита и слияния.\n", encoding="utf-8")
        try:
            rc, out = self.stand("stand-shared-rules")
        finally:
            rules.write_text(was, encoding="utf-8")
        self.assertEqual(rc, 0, "стенд не прошёл на едином источнике правил:\n%s" % out)

    def test_head_without_a_wait_mark_fails_the_stand(self):
        # Откат первого звена: голова кончает ход, не отметив ожидания. Воронка
        # оболочки снимает её на третьем проходе, как снимала до DK-899, и
        # стенд обязан выйти ненулевым кодом с названным ожиданием.
        was = waitcheck.wait_cmd
        waitcheck.wait_cmd = lambda proj, n, events: ["python3", "-c", "pass"]
        try:
            rc, out = self.stand("stand-nomark")
        finally:
            waitcheck.wait_cmd = was
        self.assertEqual(rc, 1, "голова без отметки ожидания прошла стенд:\n%s" % out)
        self.assertIn("ожидание «живое ожидание против воронки» не прошло", out,
                      "провал не назван ожиданием:\n%s" % out)
        self.assertNotIn("три ожидания прошли", out, "сломанное ожидание названо целым")

    def test_disarmed_row_fails_the_stand(self):
        # Откат второго звена: строку не взвели. Слияние предпосылки снимает с
        # неё ребро, а стартовать без взвода она не должна и не станет, и стенд
        # обязан назвать именно это ожидание.
        was = waitcheck.arm_row
        waitcheck.arm_row = lambda cmd_run, proj: (0, "взвод не поставлен")
        try:
            rc, out = self.stand("stand-disarmed")
        finally:
            waitcheck.arm_row = was
        self.assertEqual(rc, 1, "снятый взвод прошёл стенд:\n%s" % out)
        self.assertIn("ожидание «close и merge поднимают ждущих» не прошло", out,
                      "провал не назван ожиданием:\n%s" % out)
        self.assertIn(waitcheck.ARMED, out, "провал не назвал строку, оставшуюся стоять")

    def test_silent_tick_fails_the_stand(self):
        # Откат третьего звена: тик молчит. Событие прошло мимо close и merge,
        # добирать его больше некому, и стенд обязан покраснеть на тике, а не
        # объявить конвейер живым.
        was = watch.waiters
        watch.waiters = lambda root, call=None, taskctl=None: []
        try:
            rc, out = self.stand("stand-silent-tick")
        finally:
            watch.waiters = was
        self.assertEqual(rc, 1, "молчащий тик прошёл стенд:\n%s" % out)
        self.assertIn("ожидание «тик добирает пропущенное» не прошло", out,
                      "провал не назван ожиданием:\n%s" % out)
        self.assertIn(waitcheck.MISSED, out, "провал не назвал строку, оставшуюся стоять")

    def test_hand_merge_returns_to_start_branch(self):
        # Ручное слияние возвращается на ветку ствола, каким бы именем его ни
        # дал git: init.defaultBranch на машинах разнится, и хардкод main
        # ломал стенд там, где git кладёт master.
        proj = self.box.root / "handmerge"
        proj.mkdir()

        def cmd_run(args, cwd=None, env=None):
            return testenv.run(args, cwd=cwd, env=env, path=self.path,
                               home=self.box.home)

        rc, out = cmd_run(["git", "init", "-q", "-b", "zz-trunk", str(proj)])
        self.assertEqual(rc, 0, "репозиторий не завёлся: %s" % out)
        write(proj / "README.md", "стенд ручного слияния\n")
        cmd_run(["git", "-C", str(proj), "add", "--", "."])
        rc, out = cmd_run(["git", "-C", str(proj), "commit", "-q", "-m",
                           "chore: старт"])
        self.assertEqual(rc, 0, "первый коммит не прошёл: %s" % out)
        rc, out = waitcheck.hand_merge(cmd_run, proj, "WT-007")
        self.assertEqual(rc, 0, "ручное слияние не прошло:\n%s" % out)
        rc, out = cmd_run(["git", "-C", str(proj), "rev-parse",
                           "--abbrev-ref", "HEAD"])
        self.assertEqual(out.strip(), "zz-trunk",
                         "слияние не вернулось на ветку ствола: %s" % out)


if __name__ == "__main__":
    unittest.main()
