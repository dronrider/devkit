"""Тесты живого круга selfcheck (DK-158).

Круг гоняется по подставному PATH с настоящими бинарями, собранными из копии
devkit: заглушки доску и ветку не двигают, а проверяется связка в движении.
Как и у FreshConnectTest, это тяжёлый класс сюиты, бинари собираются один раз
на класс.
"""
import unittest
from pathlib import Path

import selfcheck
import testenv
from testenv import SandboxCase, go_cache_env, run, write

TOOLS = ("taskctl", "shipctl", "agentctl", "regcheck")


class SelfcheckTest(SandboxCase):
    """Живой круг связки: временный проект, строка, вердикт, ветка, слияние."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Автор коммитов: new делает первый коммит, а в чистом CI git его
        # не угадывает.
        write(cls.box.home / ".gitconfig", "[user]\n\tname = t\n\temail = t@t\n")
        # Бинари утилит собираются из копии devkit и встают вперёд заглушек
        # стенда. Обёртка devkitctl уже лежит в dkbin (стенд кладёт её с путём
        # к devkitctl.py копии), круг берёт её оттуда, как машина после установки.
        cls.bin = cls.box.root / "circlebin"
        cls.bin.mkdir()
        for tool in TOOLS:
            env = go_cache_env()
            env["GOWORK"] = "off"
            rc, out = run(["go", "build", "-o", str(cls.bin / tool), "."],
                          cwd=str(cls.box.dk / "tools" / tool), env=env)
            assert rc == 0, "%s не собрался: %s" % (tool, out)
        cls.path = "%s:%s:%s:%s" % (cls.bin, cls.box.dkbin, cls.box.bin, cls.box.sys)

    def run_selfcheck(self, where, path=None, sabotage=None):
        import selfcheck
        lines = []

        def cmd_run(args, cwd=None):
            if sabotage:
                sabotage(args, cwd)
            return testenv.run(args, cwd=cwd, path=path or self.path,
                               home=self.box.home)

        rc = selfcheck.main(cmd_run=cmd_run, where=where, log=lines.append)
        return rc, "\n".join(lines)

    def test_live_circle_passes_and_cleans_up(self):
        where = self.box.root / "circle"
        where.mkdir()
        rc, out = self.run_selfcheck(where)
        self.assertEqual(rc, 0, "живой круг не прошёл:\n%s" % out)
        # Доктор в стендовом окружении всегда называет находки раскладки
        # (rc=1), и круг их провалом не считает: проверяется, что шаг был и
        # находки перечислены в конце, а не строка «доктор: ок», которая на
        # реальной машине после установки одна и есть.
        self.assertIn("доктор:", out, "шаг доктора не назван в отчёте")
        for step in ("подключение", "строка на доске", "вердикт pick",
                     "ветка задачи", "сценарий задачи", "обкатка сценария",
                     "слияние с выкатом", "закрытие задачи"):
            self.assertIn("%s: ок" % step, out, "шаг «%s» не назван в отчёте" % step)
        self.assertIn("связка жива", out, "отчёт не назвал итог")
        # Уборка: место круга после него обязано быть таким же, как до. Слепок
        # в отчёте молчит об изменениях, а каталог проверяется напрямую: круг
        # не оставляет ни проекта, ни дерева задачи.
        self.assertNotIn("за кругом осталось", out,
                         "круг оставил за собой след: %s" % out)
        self.assertEqual(sorted(p.name for p in where.iterdir()), [],
                         "круг оставил за собой файлы: %s"
                         % [p.name for p in where.iterdir()])

    def test_missing_utility_fails_named_step(self):
        # taskctl нет в PATH: подключение и доктор прошли (обёртка devkitctl
        # и git на месте), а круг обязан упасть на шаге доски с выводом, не
        # молча и не «всё живо».
        where = self.box.root / "circle-notaskctl"
        where.mkdir()
        rc, out = self.run_selfcheck(where, path="%s:%s" % (self.box.dkbin, self.box.sys))
        self.assertEqual(rc, 1, "круг без taskctl прошёл:\n%s" % out)
        self.assertIn("строка на доске: не прошёл", out, "провал не назван шагом доски")
        self.assertIn("taskctl", out, "вывод провала не называет утилиту")
        self.assertNotIn("связка жива", out, "сломанная связка названа живой")

    def test_broken_path_fails_first_step_with_output(self):
        # Ни одной утилиты devkit в PATH: круг падает на первом же шаге и
        # печатает вывод, а не молчит.
        where = self.box.root / "circle-broken"
        where.mkdir()
        rc, out = self.run_selfcheck(where, path=str(self.box.sys))
        self.assertEqual(rc, 1, "круг со сломанным PATH прошёл:\n%s" % out)
        self.assertIn("доктор: не прошёл", out, "провал не назван первым шагом")
        self.assertIn("devkitctl", out, "вывод провала пуст")

    def test_silent_deploy_fails_merge_step(self):
        # Обман слияния, единственное место честности отчёта на перепроверке:
        # слияние отвечает «ок», а выкат метки не оставил. Такой круг обязан
        # ронять шаг слияния, а не называть связку живой. Выкат подменяется
        # пустой командой после того, как круг заполнил болванку deploy.local.
        where = self.box.root / "circle-silent-deploy"
        where.mkdir()
        real_stub = selfcheck.deploy_stub

        def silent_stub(root):
            real_stub(root)
            dep = Path(root) / ".devkit" / "deploy.local"
            dep.write_text("deploy = python3 -c pass\ntest = %s\nautonomous = true\n"
                           % selfcheck.TEST_CMD, encoding="utf-8")

        selfcheck.deploy_stub = silent_stub
        try:
            rc, out = self.run_selfcheck(where)
        finally:
            selfcheck.deploy_stub = real_stub
        self.assertEqual(rc, 1, "тихий выкат прошёл круг живым:\n%s" % out)
        self.assertIn("слияние с выкатом: не прошёл", out,
                      "обман слияния не назван шагом")
        self.assertIn("выкат не оставил метку", out, "провал не назвал причину")
        self.assertNotIn("связка жива", out, "несработавший выкат назван живым")

    def test_board_step_commits_task_file(self):
        # Коммит шага доски обязан брать docs/TASKS.md вместе с файлом задачи:
        # оставленный untracked файл отказывалось затирать слияние, и круг падал
        # на «слиянии с выкатом». Грязь спрашивается до слияния, на перехвате
        # вызова pick: строка и файл уже заведены, ветки ещё нет.
        where = self.box.root / "circle-untracked-file"
        where.mkdir()
        dirty = {}

        def watch_task_file(args, cwd):
            if args[:2] == ["agentctl", "-C"]:
                rc, out = testenv.run(
                    ["git", "-C", str(where / "proj"), "status", "--porcelain",
                     "--", "docs/tasks/%s.md" % selfcheck.TASK],
                    path=self.path, home=self.box.home)
                dirty["out"] = out.strip()

        rc, out = self.run_selfcheck(where, sabotage=watch_task_file)
        self.assertEqual(rc, 0, "живой круг не прошёл:\n%s" % out)
        self.assertEqual(dirty.get("out", ""), "",
                         "файл задачи не закоммичен шагом доски: %s"
                         % dirty.get("out", ""))

    def test_leftover_branch_reported(self):
        # Уборка судится веткам задачи в репозитории проекта, и смерть этой
        # проверки (вопрос не тому пути, пустой список на любом отказе) не
        # роняла бы ни один тест: саботаж оставляет лишнюю ветку и требует,
        # чтобы круг её назвал, а не молчал о чистоте.
        where = self.box.root / "circle-leftover-branch"
        where.mkdir()
        planted = {}

        def plant_extra_branch(args, cwd):
            # Ветка подкладывается один раз, на перехвате вызова слияния, до
            # его прогона: раньше репозитория проекта ещё нет, позже
            # круг снесёт проект.
            if args[:2] == ["shipctl", "-C"] and args[-1] == selfcheck.TASK \
                    and "merge" in args and "planted" not in planted:
                planted["planted"] = True
                testenv.run(["git", "-C", str(where / "proj"), "branch", "sc-002"],
                            path=self.path, home=self.box.home)

        rc, out = self.run_selfcheck(where, sabotage=plant_extra_branch)
        self.assertIn("за кругом осталась ветка задачи", out,
                      "лишняя ветка не названа в отчёте: %s" % out)


if __name__ == "__main__":
    unittest.main()
