"""Тесты стенда трёх ожиданий (DK-936).

Стенд гоняется по подставному PATH с настоящими бинарями, собранными из копии
devkit: заглушки ни доску не двигают, ни голову не поднимают, а проверяется
связка ожиданий в движении. Как и у SelfcheckTest, это тяжёлый класс сюиты,
бинари собираются один раз на класс.

Зелёный прогон тут один, а красных три: стенд обязан краснеть от отката любого
из трёх звеньев, и молчаливое «всё прошло» на сломанном ожидании было бы хуже
отсутствия стенда.
"""
import unittest

import testenv
import waitcheck
import watch
from testenv import SandboxCase, go_cache_env, run, write

TOOLS = ("taskctl", "shipctl", "agentctl", "regcheck")


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
        self.assertIn("проходов 5, из них ожиданий 4", out,
                      "исход воронки не назвал счёт проходов:\n%s" % out)
        self.assertIn("слияние %s подняло %s" % (waitcheck.DEP, waitcheck.ARMED), out,
                      "исход обхода не назвал взведённую строку:\n%s" % out)
        self.assertIn("тик добрал слияние %s" % waitcheck.HAND, out,
                      "исход тика не назвал пропущенное событие:\n%s" % out)

    def test_head_without_a_wait_mark_fails_the_stand(self):
        # Откат первого звена: голова кончает ход, не отметив ожидания. Воронка
        # оболочки снимает её на третьем проходе, как снимала до DK-899, и
        # стенд обязан выйти ненулевым кодом с названным ожиданием.
        was = waitcheck.wait_cmd
        waitcheck.wait_cmd = lambda proj: ["python3", "-c", "pass"]
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


if __name__ == "__main__":
    unittest.main()
