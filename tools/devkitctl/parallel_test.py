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
import os
import queue
import shutil
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

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

    def test_go_raises_timeout_past_the_default(self):
        # Дефолтный потолок go test 10 минут пакет dashboard рядом с
        # остальными компонентами держит не всегда: тихая пропажа ключа
        # вернула бы этот срыв.
        for name, _, argv in parallel.components():
            if name.startswith("go:"):
                self.assertIn("-timeout=20m", argv,
                              "%s потерял -timeout=20m" % name)

    def test_go_components_leave_the_foreign_workspace(self):
        # Чужой go.work выше по дереву (находка DK-115) уводит go test из
        # модуля утилиты, поэтому глушить workspace обязан сам раннер: env
        # у go-компонента обязана нести GOWORK=off, у остальных компонентов
        # окружение остаётся родительским.
        env = parallel.command_env(["go", "test", "./..."])
        self.assertEqual(env["GOWORK"], "off")
        self.assertIsNone(parallel.command_env(["/usr/bin/python3", "-m", "unittest"]))


class BudgetTest(unittest.TestCase):
    """Один бюджет параллельности вместо трёх независимых потолков (DK-1123).

    Раздача долей проверяется арифметикой и собранными командами, а не
    стенным временем: под нагрузкой машины секунды растягиваются, а числа в
    argv и окружении нет.
    """

    def test_cpu_budget_uses_the_real_core_count(self):
        with mock.patch.object(parallel.os, "cpu_count", return_value=1):
            self.assertEqual(parallel.cpu_budget(), 1)

    def test_cpu_budget_falls_back_when_core_count_is_unknown(self):
        # os.cpu_count() отдаёт None на песочницах, где число ядер не узнать
        # (документированное поведение stdlib), и бюджет обязан не падать.
        with mock.patch.object(parallel.os, "cpu_count", return_value=None):
            self.assertEqual(parallel.cpu_budget(), 4)

    def test_share_holds_the_invariant_for_jobs_within_the_budget(self):
        # При jobs в пределах бюджета сумма долей занятых лайнов не обязана
        # превышать бюджет: это и есть развязка трёх умножавшихся потолков.
        # Доля раннера считается вместе со своим лайном.
        for budget in (1, 2, 4, 10, 17):
            for reserved in (0, parallel.runner_share(budget)):
                top = parallel.lane_count(budget, reserved)
                for jobs in range(1, top + 1):
                    share = parallel.component_share(jobs, budget, reserved)
                    lanes = jobs - 1 if reserved else jobs
                    self.assertGreaterEqual(share, 1)
                    self.assertLessEqual(reserved + max(0, lanes) * share, budget,
                                         "jobs=%d budget=%d reserved=%d share=%d"
                                         % (jobs, budget, reserved, share))

    def test_manual_jobs_past_the_lane_count_floors_at_one(self):
        # Ручной -j больше, чем оставил раннер, это осознанный выбор
        # исполнителя: доля падает к единице и ниже не идёт.
        self.assertEqual(parallel.component_share(50, 10, parallel.runner_share(10)), 1)

    def test_runner_takes_half_the_budget(self):
        # Сюита devkitctl это полюс прогона (1161-1205 с против 526-586 с у
        # следующего компонента, замер 2026-09-23), и один воркер свёл бы её к
        # последовательному прогону классов.
        self.assertEqual(parallel.runner_share(10), 5)
        self.assertEqual(parallel.runner_share(4), 2)
        self.assertEqual(parallel.runner_share(1), 1,
                         "на одном ядре раннеру достаётся хотя бы один воркер")

    def test_lanes_leave_room_for_the_runner_share(self):
        # Лайнов ровно столько, чтобы доля раннера и доли остальных лайнов
        # сложились в бюджет.
        for budget in (1, 2, 4, 10, 17):
            reserved = parallel.runner_share(budget)
            jobs = parallel.lane_count(budget, reserved)
            share = parallel.component_share(jobs, budget, reserved)
            self.assertGreaterEqual(jobs, 1)
            self.assertLessEqual(reserved + (jobs - 1) * share, budget,
                                 "budget=%d jobs=%d share=%d"
                                 % (budget, jobs, share))

    def test_lanes_without_a_runner_take_the_whole_budget(self):
        # Срез --only-go (тот же прогон в CI) делить с раннером нечего, и
        # лайнов там столько же, сколько ядер.
        for budget in (1, 2, 4, 10, 17):
            self.assertEqual(parallel.lane_count(budget), budget)

    def test_queue_puts_the_runner_first(self):
        # Лайнов меньше, чем компонентов, и раннер, взятый из очереди
        # последним, удлинил бы стену на всю свою длину.
        comps = [("go:a", ".", ["go"]), (parallel.RUNNER, ".", ["s"]),
                 ("hooks", ".", ["h"])]
        self.assertEqual([c[0] for c in parallel.queue_order(comps)],
                         [parallel.RUNNER, "go:a", "hooks"])
        self.assertEqual([c[0] for c in parallel.queue_order(comps[:1] + comps[2:])],
                         ["go:a", "hooks"],
                         "без раннера порядок остальных не трогается")

    def test_share_uses_the_whole_budget_when_jobs_is_one(self):
        # Один компонент за раз получает весь бюджет: доля не размазывается
        # там, где делить не на кого.
        self.assertEqual(parallel.component_share(1, 10), 10)

    def test_share_floors_at_one_when_jobs_outnumber_the_budget(self):
        # Ручной -j больше числа ядер это осознанный выбор исполнителя, а не
        # брешь раздачи: доля не растёт обратно, просто перестаёт делиться.
        self.assertEqual(parallel.component_share(50, 10), 1)

    def test_with_share_inserts_p_before_the_package_list(self):
        # После списка пакетов флаг -p ушёл бы аргументом go test, а не
        # потолком параллельности.
        argv = ["go", "test", "-count=1", "-timeout=20m", "./..."]
        got = parallel.with_share("go:taskctl", argv, 3)
        self.assertEqual(got, ["go", "test", "-count=1", "-timeout=20m",
                               "-p", "3", "./..."])

    def test_with_share_appends_j_for_devkitctl(self):
        argv = [sys.executable, "suite.py"]
        got = parallel.with_share("devkitctl", argv, 5)
        self.assertEqual(got, argv + ["-j", "5"])

    def test_with_share_leaves_other_components_alone(self):
        argv = [sys.executable, "check-skills.py"]
        self.assertEqual(parallel.with_share("check-skills", argv, 5), argv)

    def test_command_env_leaves_the_go_runtime_alone(self):
        # Постановка DK-1123 звала долю и переменной GOMAXPROCS, а замер
        # 2026-09-23 её отменил. Переменная это не потолок работы, а смена
        # режима рантайма: горутина, дождавшаяся своего подпроцесса, ждёт
        # освободившегося полюса вместо того, чтобы бежать рядом. Под полным
        # прогоном такая задержка красит гонку уборки TempDir в пакете
        # dashboard (пять красных прогонов из пяти при доле единица, шестой
        # при доле два, зелёный без переменной), а машине не сберегает
        # ничего: тесты го-модулей t.Parallel не зовут. Гонка живёт
        # черновиком DK-1136.
        with mock.patch.dict(parallel.os.environ, {"GOMAXPROCS": "7"}):
            env = parallel.command_env(["go", "test", "./..."])
        self.assertEqual(env["GOWORK"], "off")
        self.assertEqual(env.get("GOMAXPROCS"), "7",
                         "раннер своей доли в рантайм не ставит, а чужую "
                         "переменную оставляет как была")


class LiveBudgetTest(unittest.TestCase):
    """Живая раздача бюджета по факту идущих компонентов, без стенных секунд.

    Первая доработка по замечанию ревью DK-1123 считала живость по воркерам,
    ещё не нашедшим очередь пустой, и не меняла ничего: воркер объявляет
    очередь пустой ровно тогда, когда брать больше нечего, так что стартовать
    с уменьшившимся счётом было уже некому. Счёт идёт по компонентам,
    работающим прямо сейчас. Гонка настоящих потоков это не ловит
    детерминированно (какой воркер первым освободится - решает планировщик
    ОС), поэтому механика проверяется прямыми вызовами `pull` и `drop`, без
    единого потока.
    """

    def pending(self, *names):
        """Очередь компонентов на этих именах, argv стенду не нужен."""
        waiting = queue.Queue()
        for name in names:
            waiting.put((name, ".", ["true"]))
        return waiting

    def take(self, live, name, waiting=0):
        """Доля компоненту, за которым в очереди ждут ещё `waiting` штук."""
        tail = ["tail:%d" % i for i in range(waiting)]
        return live.pull(self.pending(name, *tail))[1]

    def test_runner_gets_its_reserved_share(self):
        live = parallel._Budget(10, 3, parallel.runner_share(10))
        self.assertEqual(self.take(live, parallel.RUNNER, 17), 5)

    def test_lanes_and_runner_add_up_to_the_budget(self):
        # Полный старт на десяти ядрах: раннер и два обычных лайна.
        reserved = parallel.runner_share(10)
        jobs = parallel.lane_count(10, reserved)
        live = parallel._Budget(10, jobs, reserved)
        given = [self.take(live, parallel.RUNNER, 17)]
        given += [self.take(live, "go:%d" % i, 12 - i) for i in range(jobs - 1)]
        self.assertEqual(given, [5, 1, 1, 1, 1, 1])
        self.assertLessEqual(sum(given), 10)

    def test_queue_tail_holds_the_early_share_down(self):
        # Свободные лайны займутся тут же, поэтому хвост очереди считается
        # будущими соседями: без этого первый же компонент забрал бы бюджет
        # целиком, а стартовавшие следом сложились бы с ним в перебор.
        live = parallel._Budget(10, parallel.lane_count(10), 0)
        self.assertEqual(self.take(live, "go:a", 5), 1)

    def test_share_grows_when_the_run_thins_out(self):
        reserved = parallel.runner_share(10)
        jobs = parallel.lane_count(10, reserved)
        live = parallel._Budget(10, jobs, reserved)
        self.take(live, parallel.RUNNER, 17)
        for i in range(jobs - 1):
            self.take(live, "go:%d" % i, 12 - i)
        for i in range(jobs - 1):
            live.drop("go:%d" % i)
        self.assertEqual(self.take(live, "hooks"), 5,
                         "раннер держит половину бюджета, вторая уходит соседу")
        live.drop("hooks")
        live.drop(parallel.RUNNER)
        self.assertEqual(self.take(live, "doctor"), 10,
                         "последний компонент на пустой очереди берёт весь бюджет")

    def test_share_never_drops_below_one(self):
        live = parallel._Budget(1, 6, 0)
        self.assertGreaterEqual(self.take(live, "go:a", 10), 1)

    def test_pull_on_the_empty_queue_gives_nothing(self):
        live = parallel._Budget(10, 3, 0)
        self.assertIsNone(live.pull(self.pending()))

    def test_neighbour_in_flight_holds_the_first_share_down(self):
        # Замечание ревью DK-1123. Снятие с очереди и счёт доли порознь
        # оставляли компонент в пути: из очереди вынут, в счёт работающих не
        # попал, хвоста очереди не занимает. Сосед, стартующий в эту щель,
        # брал долю как единственный на машине (`take("O2", 0)` -> 10), а
        # подоспевший следом добавлял к ней свою (`take("O1", 0)` -> 5), и
        # сумма выходила 15 при бюджете 10. Одним шагом такой щели нет.
        live = parallel._Budget(10, 3, 0)
        waiting = self.pending("O1", "O2")
        given = [live.pull(waiting)[1], live.pull(waiting)[1]]
        self.assertLessEqual(sum(given), 10,
                             "доли работающих компонентов не перебирают бюджет")
        self.assertEqual(given, [5, 5])

    def test_queue_is_drained_under_the_share_lock(self):
        # Щель закрыта ровно тем, что оба шага идут внутри одного замка:
        # разнесённые, они снова пустят соседа считать долю по очереди, из
        # которой компонент уже вынут.
        live = parallel._Budget(10, 3, 0)
        held = []

        class Watched(queue.Queue):

            def get_nowait(self):
                held.append(live._lock.locked())
                return queue.Queue.get_nowait(self)

        waiting = Watched()
        waiting.put(("go:a", ".", ["true"]))
        live.pull(waiting)
        self.assertEqual(held, [True],
                         "компонент снимается с очереди под замком счёта")

    def test_tail_order_keeps_the_invariant_under_the_runner(self):
        # Тот же перебор на боевой конфигурации: раннер уже держит половину
        # бюджета, первый сосед кончился, и хвост очереди разбирают двое.
        # Порознь это давало 5 плюс 5 плюс 2 при бюджете 10.
        reserved = parallel.runner_share(10)
        live = parallel._Budget(10, parallel.lane_count(10, reserved), reserved)
        waiting = self.pending(parallel.RUNNER, "go:a", "go:b", "go:c")
        runner = live.pull(waiting)[1]
        live.pull(waiting)
        live.drop("go:a")
        tail = [live.pull(waiting)[1], live.pull(waiting)[1]]
        self.assertLessEqual(runner + sum(tail), 10,
                             "доли работающих компонентов не перебирают бюджет")
        self.assertEqual([runner] + tail, [5, 2, 2])


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

    def test_share_reaches_go_and_runner_components(self):
        # Замечание ревью DK-1123: юниты закрывали with_share, command_env и
        # сам расчёт доли по отдельности, а связку «run_all раздаёт долю
        # настоящему го-компоненту и раннеру» не трогал никто. Прочие стенды
        # раннера гонят shell-компоненты, которых with_share не касается по
        # имени, и подмена доли на бюджет прошла бы мимо них.
        #
        # Стенд подставляет свой `go` первым в PATH: имя исполняемого файла
        # разбирается по PATH того самого окружения, что собирает
        # command_env, поэтому в файл уезжает и argv с флагом -p, и
        # окружение подпроцесса.
        self.grow("go", 'printf "%s GOWORK=%s GOMAXPROCS=[%s]\\n"'
                        ' "$*" "$GOWORK" "$GOMAXPROCS" >> calls')
        runner = self.grow(parallel.RUNNER, 'printf "suite %s\\n" "$*" >> calls')
        comps = [runner, ("go:x", ".", ["go", "test", "./..."])]
        path = str(self.dir) + os.pathsep + parallel.os.environ.get("PATH", "")
        with mock.patch.dict(parallel.os.environ, {"PATH": path},
                             clear=False) as _:
            parallel.os.environ.pop("GOMAXPROCS", None)
            outcomes, first = parallel.run_all(comps, 2, root=self.dir, budget=8)
        self.assertIsNone(first, [o[3] for o in outcomes])
        said = (self.dir / "calls").read_text(encoding="utf-8")
        # Бюджет 8 при двух лайнах: раннеру половина (4), го-компоненту
        # остаток на единственный обычный лайн (4). Ни доля по умолчанию (1),
        # ни бюджет целиком (8) на эти числа не похожи.
        self.assertIn("test -p 4 ./... GOWORK=off GOMAXPROCS=[]", said,
                      "доля не дошла до argv го-компонента либо раннер "
                      "полез в рантайм своей переменной")
        self.assertIn("suite -j 4", said, "доля не дошла до раннера")

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

    def test_only_go_narrows_the_list_to_go_modules(self):
        # CI гоняет именно этот срез (--only-go), и перечень обязан оставаться
        # тем же, что у go_tools: хранёный список руками уже расходился с
        # деревом молча, cmdout и secretctl выпали из CI незамеченными
        # (находка DK-508).
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = parallel.main(["--only-go", "--list"])
        self.assertEqual(rc, 0)
        names = [line.split()[0] for line in out.getvalue().splitlines()
                 if line.startswith("go:")]
        self.assertEqual(names, ["go:" + t for t in parallel.go_tools()])
        self.assertIn("go:cmdout", names)
        self.assertIn("go:secretctl", names)
        self.assertNotIn("hooks", out.getvalue())
        self.assertNotIn("doctor", out.getvalue())

    def test_list_shows_go_shares_matching_the_budget(self):
        # --list не гоняет ни одного подпроцесса, поэтому числа в собранных
        # командах реального перечня компонентов проверяются без стенного
        # времени.
        with mock.patch.object(parallel.os, "cpu_count", return_value=4):
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = parallel.main(["--only-go", "--list"])
        self.assertEqual(rc, 0)
        share = parallel.component_share(parallel.lane_count(4), 4)
        go_lines = [line for line in out.getvalue().splitlines()
                    if line.startswith("go:")]
        self.assertTrue(go_lines)
        for line in go_lines:
            self.assertIn("-p %d" % share, line, line)

    def test_list_shows_devkitctl_share_matching_the_budget(self):
        with mock.patch.object(parallel.os, "cpu_count", return_value=4):
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = parallel.main(["--list"])
        self.assertEqual(rc, 0)
        lines = out.getvalue().splitlines()
        runner = [line for line in lines if line.startswith("devkitctl ")]
        self.assertEqual(len(runner), 1)
        # Раннеру половина бюджета, обычному компоненту единица: равная доля
        # свела бы сюиту к одному воркеру и сделала полюсом её саму.
        self.assertIn("-j %d" % parallel.runner_share(4), runner[0])
        self.assertEqual(lines[0], runner[0],
                         "раннер обязан стоять первым в очереди")
        go_lines = [line for line in lines if line.startswith("go:")]
        self.assertTrue(go_lines)
        share = parallel.component_share(parallel.lane_count(4), 4,
                                         parallel.runner_share(4))
        for line in go_lines:
            self.assertIn("-p %d" % share, line, line)

    def test_explicit_j_flag_reaches_the_share(self):
        # Замечание ревью DK-1123: склейка args.jobs -> component_share была
        # проверена только на умолчании (jobs = число ядер по mock cpu_count),
        # явный -j никакой тест не трогал. Восемь ядер и -j 3 дают долю 2,
        # отличную и от доли на умолчании (1), и от доли budget=jobs (8) -
        # подмена jobs константой budget тут же ловится числом.
        with mock.patch.object(parallel.os, "cpu_count", return_value=8):
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = parallel.main(["-j", "3", "--only-go", "--list"])
        self.assertEqual(rc, 0)
        share = parallel.component_share(3, 8)
        self.assertEqual(share, 2, "доля обязана прийти именно от -j, не от бюджета")
        go_lines = [line for line in out.getvalue().splitlines()
                    if line.startswith("go:")]
        self.assertTrue(go_lines)
        for line in go_lines:
            self.assertIn("-p %d" % share, line, line)


class CiWorkflowTest(unittest.TestCase):
    """CI обязан гонять го-модули тем же раннером, что не хранит их список.

    Хранёный вручную перечень в `.github/workflows/ci.yml` уже расходился с
    деревом молча: `cmdout` и `secretctl` выпали из CI, оставшись зелёными без
    единого прогона (находка DK-508). Перечень поэтому не хранится в workflow
    вовсе, а вызывается флагом `--only-go`, чей состав уже сверен с
    `go_tools()` тестом выше.
    """

    def test_ci_calls_the_dynamic_go_runner(self):
        text = (parallel.ROOT / ".github" / "workflows" / "ci.yml").read_text(
            encoding="utf-8")
        self.assertIn("parallel.py --only-go", text,
                      "CI обязан гонять го-модули через раннер, а не "
                      "хранёный список")
        for tool in parallel.go_tools():
            self.assertNotIn("working-directory: tools/%s" % tool, text,
                              "го-модуль %s не должен возвращаться в CI "
                              "отдельным ручным шагом" % tool)


if __name__ == "__main__":
    unittest.main()
