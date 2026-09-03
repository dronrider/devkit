#!/usr/bin/env python3
"""Раннер компонентов: перечень, параллель и стоп по первому провалу.

Живые компоненты для проверки раннера тяжёлые, поэтому здесь синтетический
каталог с короткими скриптами: проверяется сам раннер, а не компоненты поверх
него. На провале занятые компоненты убиваются по группе процессов, и стенд
следит за этим по факту: долгий скрипт метит свой конец файлом, и стенд ждёт
отсутствия метки, а не стенных секунд, которые растягиваются под нагрузкой
соседних прогонов и красили тест при живом инварианте (находка DK-635).
"""
import contextlib
import io
import shutil
import tempfile
import time
import unittest
from pathlib import Path

import parallel


class Stand(unittest.TestCase):
    """Синтетические компоненты в своей директории: скрипт на компонент."""

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="devkitctl-parallel-test-"))

    def tearDown(self):
        shutil.rmtree(str(self.dir), ignore_errors=True)

    def grow(self, name, body):
        """Пишет исполняемый скрипт и отдаёт компонент на нём."""
        path = self.dir / name
        path.write_text("#!/bin/sh\n" + body, encoding="utf-8")
        path.chmod(0o755)
        return name, ".", [str(path)]


class ComponentsTest(unittest.TestCase):

    def test_list_covers_the_checkout(self):
        go_names = ["go:" + t for t in parallel.go_tools()]
        names = [name for name, _, _ in parallel.components()]
        self.assertEqual(names[:len(go_names)], go_names)
        # cmdout выпадал из прежнего ручного списка (находка DK-367): без
        # него местный прогон молча не покрывал модуль.
        self.assertIn("go:cmdout", go_names)
        for name in ("hooks", "devkitctl", "skills", "check-skills",
                     "check-exec-bit", "doctor"):
            self.assertIn(name, names)
        skills = parallel.skill_suites()
        self.assertIn("goal-loop", skills)
        self.assertIn("prose", skills)
        for name in skills:
            self.assertIn(name, names)
        self.assertEqual(len(names), len(go_names) + 6 + len(skills))

    def test_skill_suites_discovers_by_test_file_not_by_hand(self):
        # Перечень каталогов со скилловыми тестами хранился в коде руками, и
        # тесты нового скилла не гонялись бы, пока кто-то не вспомнит про
        # раннер. Открытие по факту `*_test.py` ловит любой новый скилл и не
        # берёт скилл без тестов.
        root = Path(tempfile.mkdtemp(prefix="devkitctl-skill-suites-test-"))
        self.addCleanup(shutil.rmtree, str(root), True)
        for name in ("zeta", "alpha"):
            d = root / "kit" / "skills" / name
            d.mkdir(parents=True)
            (d / (name + "_test.py")).write_text("", encoding="utf-8")
            (d / (name + "2_test.py")).write_text("", encoding="utf-8")
        (root / "kit" / "skills" / "no-tests").mkdir(parents=True)
        (root / "kit" / "skills" / "check_skills_test.py").write_text("", encoding="utf-8")
        self.assertEqual(parallel.skill_suites(root), ["alpha", "zeta"])

    def test_go_tools_discovers_by_gomod_not_by_hand(self):
        # Прежний список хранился в коде руками и разошёлся с деревом молча
        # (cmdout выпал). Открытие по факту go.mod ловит любой новый модуль
        # без правки списка и не путает каталог без go.mod с модулем.
        root = Path(tempfile.mkdtemp(prefix="devkitctl-go-tools-test-"))
        self.addCleanup(shutil.rmtree, str(root), True)
        for name in ("zeta", "alpha", "beta"):
            d = root / "tools" / name
            d.mkdir(parents=True)
            (d / "go.mod").write_text("module example.com/" + name + "\n\ngo 1.26\n",
                                       encoding="utf-8")
        (root / "tools" / "not-a-module").mkdir(parents=True)
        self.assertEqual(parallel.go_tools(root), ["alpha", "beta", "zeta"])

    def test_exec_bit_checker_is_in_the_chain(self):
        # Чекер бита выполнения шёл отдельным шагом старой цепочки test и
        # обязан переехать в раннер, иначе замена цепочки тихо теряла его
        # локальный прогон (замечание ревью DK-347).
        comp = {name: (rel, argv) for name, rel, argv in parallel.components()}
        self.assertIn("check-exec-bit", comp)
        rel, argv = comp["check-exec-bit"]
        self.assertEqual(rel, "hooks")
        self.assertEqual(argv[-1], "check-exec-bit.py")

    def test_go_stays_with_count_one(self):
        # -count=1 это требование DoD цели: кэш тестового прогона go обязан
        # молчать, и тихая пропажа ключа из перечня вернула бы кэш.
        for name, _, argv in parallel.components():
            if name.startswith("go:"):
                self.assertIn("-count=1", argv,
                              "%s потерял -count=1" % name)

    def test_go_components_leave_the_foreign_workspace(self):
        # Чужой go.work выше по дереву (находка DK-115) уводит go test из
        # модуля утилиты, поэтому глушить workspace обязан сам раннер: env
        # у go-компонента обязана нести GOWORK=off, у остальных компонентов
        # окружение остаётся родительским.
        env = parallel.command_env(["go", "test", "./..."])
        self.assertEqual(env["GOWORK"], "off")
        self.assertIsNone(parallel.command_env(["/usr/bin/python3", "-m", "unittest"]))


class RunAllTest(Stand):

    def test_green_components_all_finish(self):
        comps = [self.grow("a.sh", "exit 0"),
                 self.grow("b.sh", "sleep 1; exit 0")]
        outcomes, first = parallel.run_all(comps, 2, root=self.dir)
        self.assertIsNone(first)
        self.assertEqual([o[1] for o in outcomes], [0, 0])

    def test_failing_component_names_itself(self):
        comps = [self.grow("fail.sh", "echo boom; exit 3")]
        outcomes, first = parallel.run_all(comps, 1, root=self.dir)
        self.assertEqual(first, "fail.sh")
        self.assertEqual(outcomes[0][1], 3)
        self.assertIn("boom", outcomes[0][3], "вывод провала обязан доходить целиком")

    def test_first_failure_stops_the_rest(self):
        # Занятый на провале компонент убивается по группе процессов: долгий
        # скрипт обязан не дожить до своей метки, иначе стоп по первому
        # провалу молча выродился в «догнать всё и потом ответить». Факт
        # смерти ловится меткой, а не стенными секундами: run_all не
        # возвращается, пока процесс не завершится (сам ли, по стопу ли), так
        # что метка правдиво отличает «убит рано» от «дожил до конца», сколько
        # бы секунд на это ни ушло. Секунды растягиваются под нагрузкой
        # соседних прогонов и красили тест, хотя инвариант держался (находка
        # DK-635).
        comps = [self.grow("fail.sh", "sleep 1; echo boom; exit 3"),
                 self.grow("long.sh", "sleep 30; touch done; exit 0")]
        marker = self.dir / "done"
        outcomes, first = parallel.run_all(comps, 2, root=self.dir)
        self.assertEqual(first, "fail.sh")
        self.assertFalse(marker.exists(), "долгий компонент не был остановлен")
        self.assertEqual([o[0] for o in outcomes], ["fail.sh"],
                         "остановленный компонент не обязан попадать в итог")

    def test_parallel_components_meet_each_other(self):
        # Факт параллели ловится встречей компонентов, а не временем: каждый
        # скрипт ставит свою метку и ждёт меток остальных. На восьми воркерах
        # все восемь встречаются и выходят, а последовательный прогон зависает
        # на первом же скрипте до его предела ожидания и валит компонент.
        # Абсолютное время тут не при чём: машина бывает загружена чужими
        # прогонами вплоть до насыщения, и время растягивается, а встреча
        # меток остаётся.
        count, comps = 8, []
        for i in range(count):
            body = ("touch mark.%d\n"
                    "n=0\n"
                    "while [ $(ls mark.* 2>/dev/null | wc -l) -lt %d ]; do\n"
                    "  n=$((n+1)); [ $n -gt 300 ] && exit 1; sleep 0.1\n"
                    "done\n" % (i, count))
            comps.append(self.grow("p%d.sh" % i, body))
        outcomes, first = parallel.run_all(comps, 8, root=self.dir)
        self.assertIsNone(first, "компонент не дождался встречи: прогон не параллельный")
        self.assertEqual(len(outcomes), count)

    def test_kill_reaches_the_subprocesses(self):
        # Стоп бьёт по группе процессов, а не по самому питону: субпроцесс
        # с таймером обязан умереть вместе с компонентом, не дожив до метки.
        # Проверка по файлу-метке, а не по хвостам ps: искать чужой сон по
        # шаблону в общей таблице процессов хрупко к соседним прогонам.
        comp = self.grow("kids.sh", "(sleep 30; touch done) &\nwait\n")
        marker = self.dir / "done"
        _, first = parallel.run_all([comp,
                                     self.grow("fail.sh", "sleep 1; exit 2")],
                                    2, root=self.dir)
        self.assertEqual(first, "fail.sh")
        time.sleep(0.5)
        self.assertFalse(marker.exists(),
                         "субпроцесс пережил стоп: SIGTERM ушёл только питону компонента")

    def test_unstartable_component_fails_the_run(self):
        # Компонент с несуществующим cwd не может стартовать, и провал старта
        # обязан красить прогон, а не молча выпадать из итога: зелёное
        # «Ran N of M» без одного компонента неотличимо от полного прогона
        # (замечание ревью DK-347). Прогон в один воркер, чтобы зелёный
        # компонент успел попасть в итог до провала соседа.
        comps = [self.grow("a.sh", "exit 0"),
                 ("lost.sh", "no-such-dir", [str(self.dir / "a.sh")])]
        outcomes, first = parallel.run_all(comps, 1, root=self.dir)
        self.assertEqual(first, "lost.sh")
        self.assertEqual([o[0] for o in outcomes], ["a.sh", "lost.sh"])
        self.assertIn("старт компонента не удался", outcomes[1][3],
                      "трейсбек старта обязан доходить до итога")

    def test_missing_binary_fails_the_run(self):
        # Несуществующий бинарник это тот же провал старта: переименованный
        # каталог утилиты или убранный скрипт обязаны валить прогон.
        comps = [("ghost.sh", ".", [str(self.dir / "ghost.sh")])]
        outcomes, first = parallel.run_all(comps, 1, root=self.dir)
        self.assertEqual(first, "ghost.sh")
        self.assertIn("FileNotFoundError", outcomes[0][3])

    def test_term_proof_component_is_killed_harder(self):
        # Компонент с игнорирующим TERM лидером группы обязан умереть по
        # KILL: стоп по первому провалу иначе виснет на упрямом подпроцессе.
        # Факт смерти ловится меткой, а не стенными секундами (тот же довод,
        # что у test_first_failure_stops_the_rest): под нагрузкой соседних
        # прогонов секунды растягиваются далеко за прежний потолок 10, хотя
        # подпроцесс всё равно гибнет по KILL, не дожив до метки (находка
        # DK-635).
        stubborn = "#!/bin/sh\ntrap '' TERM\n(sleep 30; touch done) &\nwait\n"
        path = self.dir / "stubborn.sh"
        path.write_text(stubborn, encoding="utf-8")
        path.chmod(0o755)
        marker = self.dir / "done"
        _, first = parallel.run_all([("stubborn.sh", ".", [str(path)]),
                                     self.grow("fail.sh", "sleep 1; exit 2")],
                                    2, root=self.dir)
        self.assertEqual(first, "fail.sh")
        time.sleep(0.5)
        self.assertFalse(marker.exists(), "субпроцесс упрямого компонента дожил до метки")


class MainTest(Stand):

    def run_main(self, comps):
        out = io.StringIO()
        was = parallel.components
        try:
            parallel.components = lambda: comps
            with contextlib.redirect_stdout(out):
                rc = parallel.main([])
        finally:
            parallel.components = was
        return rc, out.getvalue()

    def test_green_run_exits_zero(self):
        rc, out = self.run_main([self.grow("ok.sh", "exit 0")])
        self.assertEqual(rc, 0)
        self.assertIn("Ran 1 of 1 components", out)
        self.assertIn("OK", out)

    def test_failure_prints_output_and_exits_nonzero(self):
        rc, out = self.run_main([self.grow("bad.sh", "echo boom; exit 4")])
        self.assertEqual(rc, 1)
        self.assertIn("FAILED (first=bad.sh)", out)
        self.assertIn("boom", out, "вывод провалившегося компонента печатается целиком")

    def test_list_names_the_components(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = parallel.main(["--list"])
        self.assertEqual(rc, 0)
        self.assertIn("go:taskctl", out.getvalue())
        self.assertIn("компонентов: %d" % len(parallel.components()), out.getvalue())

    def test_unstartable_component_exits_nonzero(self):
        # Полный проход через main: провал старта валит прогон кодом 1, а не
        # зелёным итогом с числом меньше полного.
        rc, out = self.run_main([("lost.sh", "no-such-dir", ["./a.sh"])])
        self.assertEqual(rc, 1)
        self.assertIn("FAILED (first=lost.sh)", out)
        self.assertIn("Ran 1 of 1 components", out)


if __name__ == "__main__":
    unittest.main()
