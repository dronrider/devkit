#!/usr/bin/env python3
"""Проверка сторожа DK-1126: разбор файла и режим --diff, обе формы порога
(Go и Python), гашение пометкой, отличие от легального срока ожидания.

Фикстуры ниже это не выдумка: `GO_RATIO`, `GO_ELAPSED`, `PY_CLOCK_ASSERT`
дословно повторяют форму строк, которые правили DK-746 и DK-1038 до фикса
(история коммитов a248cda5c и dfbf532cb), а `GO_SAFE_DEADLINE` и `GO_FACT`
повторяют формы, которые сторож обязан пропускать молча: срок ожидания
внешнего события и проверку по факту, пришедшую на смену стенному порогу.
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-wallclock-test.py")

# Форма до правки DK-746 (feed_test.go): разница двух замеров сравнивается с
# долей секунды напрямую.
GO_ELAPSED = (
    "\tif cold-plain > 400*time.Millisecond {\n"
    "\t\tt.Fatalf(\"холодная лента дороже голой на %v\", cold-plain)\n"
    "\t}\n"
)

# Отношение времён: тот же порок, отвергнутый кандидат из развилки DK-746
# («Отношение остаётся замером и на загруженной машине плывёт так же»).
GO_RATIO = "\tif warm > cold/5 || warm > 30*time.Millisecond {\n"

# Срок ожидания внешнего события: не измерение, а потолок подождать, тем же
# приёмом, что context.WithTimeout и time.After в select (разбор DK-764).
GO_SAFE_DEADLINE = (
    "\tctx, stop := context.WithTimeout(context.Background(), 90*time.Second)\n"
    "\tdefer stop()\n"
    "\tcase <-time.After(3 * time.Second):\n"
    "\t\tt.Fatal(\"канал молчит\")\n"
)

# Та же форма, но рядом с настоящим time.Since (askpass_test.go в живом
# прогоне сторожа держит их в одном файле): канал-приёмник не должен путаться
# с порогом только оттого, что замер есть где-то поблизости.
GO_SAFE_DEADLINE_NEAR_CLOCK = (
    "\tstart := time.Now()\n"
    "\telapsed := time.Since(start)\n"
    "\tselect {\n"
    "\tcase <-time.After(3 * time.Second):\n"
    "\t\tt.Fatal(\"канал молчит\")\n"
    "\tcase <-done:\n"
    "\t}\n"
    "\t_ = elapsed\n"
)

# Приём после правки DK-746: факт вместо стенных миллисекунд.
GO_FACT = (
    "\tif coldRead > whole/10 {\n"
    "\t\tt.Fatalf(\"холодная лента прочла %d байт из %d\", coldRead, whole)\n"
    "\t}\n"
)

# Голое сравнение двух измерений без всякого порога, найдено живым прогоном
# obeycheck (сценарий 55-fact-not-wallclock, обе раскладки, timeit-версия ниже
# как PY_TIMEIT_COMPARE): не порог и не отношение, а просто «второй должен
# быть быстрее первого».
GO_BARE_COMPARE = (
    "\tstart := time.Now()\n"
    "\tfirst := op()\n"
    "\tcold := time.Since(start)\n"
    "\tstart = time.Now()\n"
    "\tagain := op()\n"
    "\twarm := time.Since(start)\n"
    "\tif warm > cold {\n"
    "\t\tt.Fatalf(\"повтор дороже первого захода: %v против %v\", warm, cold)\n"
    "\t}\n"
)

# Те же имена (warm, cold), но без единого time.Since в файле: без живого
# замера рядом сторож судит не по одним именам переменных и молчит, иначе
# любой счётчик с именем «warm» краснил бы просто по совпадению слова.
GO_BARE_COMPARE_NO_CLOCK = "\tif warm > cold {\n\t\tt.Fatal(\"кэш не сработал\")\n\t}\n"

# time.Now() без единого time.Since рядом: голое «сейчас» ставит штамп много
# где (фикстура, дедлайн), не измерение интервала. main_test.go и feed_test.go
# в живом прогоне держат ровно такую пару: time.Now() как аргумент, cold/age
# в несвязанной строке неподалёку.
GO_NOW_ONLY = (
    "\tpath := writeSession(t, home, proj, \"\", \"aaa-1\", fixture, time.Now())\n"
    "\tif warm > cold {\n\t\tt.Fatal(\"кэш не сработал\")\n\t}\n"
)

# Форма до правки DK-1038 (chat_in_test.py): часы в одном методе, порог в
# другом, dataflow между ними для регулярки недоступен, поэтому сторож ищет
# часы по всему файлу, а не по строке находки.
PY_CLOCK_ASSERT = (
    "    def clock(self, argv, env, feed):\n"
    "        best = None\n"
    "        for _ in range(5):\n"
    "            at = time.time()\n"
    "            subprocess.run(argv)\n"
    "            best = time.time() - at if best is None else min(best, time.time() - at)\n"
    "        return best\n"
    "\n"
    "    def test_idle_turn_costs_about_the_interpreter_start(self):\n"
    "        idle = self.clock([sys.executable, HOOK], env, \"{}\")\n"
    "        self.assertLess(idle, bare * 3 + 0.05, \"холостой ход дороже\")\n"
)

# Приём после правки DK-1038: состав хода вместо секундомера, ни разу time.time.
PY_FACT = (
    "    def test_idle_turn_touches_nothing(self):\n"
    "        before = probe_calls()\n"
    "        run_hook()\n"
    "        self.assertEqual(probe_calls(), before, \"холостой ход что-то потрогал\")\n"
)

# Голый assert без unittest: тот же порог, что PY_CLOCK_ASSERT, только без
# класса TestCase (простой скрипт вроде testdata/project/test_tool.py).
PY_RAW_ASSERT = (
    "at = time.time()\n"
    "run_once()\n"
    "elapsed = time.time() - at\n"
    "assert elapsed < 0.05, \"слишком долго\"\n"
)

# Числовая граница без слова из семьи «elapsed/took/...»: обычная проверка
# значения, не порог по времени, даже когда где-то в файле есть time.time().
PY_RAW_ASSERT_UNRELATED = (
    "at = time.time()\n"
    "run_once()\n"
    "assert retry_count < 5, \"слишком много попыток\"\n"
)

# Дословная форма живой находки obeycheck (сценарий 55-fact-not-wallclock,
# раскладка без нового текста): два живых замера сравниваются напрямую, без
# всякого порога.
PY_BARE_COMPARE = (
    "start = time.perf_counter()\n"
    "result1 = total_cached(values)\n"
    "time1 = time.perf_counter() - start\n"
    "start = time.perf_counter()\n"
    "result2 = total_cached(values)\n"
    "time2 = time.perf_counter() - start\n"
    "assert time2 < time1, f\"второй вызов должен быть дешевле: {time2:.6f}\"\n"
)

# Та же находка, но через timeit (раскладка с новым текстом дала именно эту
# форму: текст не спас от голого сравнения, только сменил инструмент замера).
PY_TIMEIT_COMPARE = (
    "time_first = timeit.timeit(lambda: total_cached(large_list), number=1000)\n"
    "time_second = timeit.timeit(lambda: total_cached(large_list), number=1000)\n"
    "assert time_second < time_first, f\"повторный вызов дешевле: {time_first}\"\n"
)

# Опрос до срока, найден живым прогоном сторожа по репозиторию: условие цикла
# сравнивает time.time() с дедлайном, но это не измерение, а стандартный приём
# ожидания внешнего события (докстринг инструмента, тот же случай, что
# Go `time.Now().Before(deadline)`).
PY_POLL_LOOP = (
    "deadline = time.time() + 5\n"
    "while time.time() < deadline:\n"
    "    if condition_met():\n"
    "        break\n"
)

# «agentctl» начинается с «age», и живой прогон сторожа по
# quota_refresh_test.py поймал строку про запуск агента как «возраст».
PY_AGENTCTL_NEAR_CLOCK = (
    "started = time.time()\n"
    "self.stub(\"agentctl\", '#!/bin/sh\\necho \"$*\" >> \"%s\"\\n')\n"
)


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


class TestFileMode(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_go_elapsed_threshold_is_caught(self):
        path = self.write("feed_test.go", "package dashboard\n\n" + GO_ELAPSED)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn(path + ":3:", r.stdout)

    def test_go_ratio_threshold_is_caught(self):
        path = self.write("pulse_test.go", "package dashboard\n\n" + GO_RATIO)
        r = run(path)
        self.assertEqual(r.returncode, 1)

    def test_go_safe_deadline_passes(self):
        path = self.write("askpass_test.go", "package dashboard\n\n" + GO_SAFE_DEADLINE)
        self.assertEqual(run(path).returncode, 0)

    def test_go_fact_based_passes(self):
        path = self.write("feed_test.go", "package dashboard\n\n" + GO_FACT)
        self.assertEqual(run(path).returncode, 0)

    def test_go_bare_compare_with_clock_is_caught(self):
        path = self.write("cache_test.go", "package lib\n\n" + GO_BARE_COMPARE)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("warm > cold", r.stdout)

    def test_go_bare_compare_without_clock_passes(self):
        """Без единого time.Since в файле сторож судит не по одним именам:
        «warm»/«cold» сами по себе не улика."""
        path = self.write("cache_test.go", "package lib\n\n" + GO_BARE_COMPARE_NO_CLOCK)
        self.assertEqual(run(path).returncode, 0)

    def test_go_now_only_passes(self):
        """time.Now() без Since рядом не улика: голое «сейчас» ставит штамп
        много где, не измеряет интервал (main_test.go, feed_test.go)."""
        path = self.write("cache_test.go", "package lib\n\n" + GO_NOW_ONLY)
        self.assertEqual(run(path).returncode, 0)

    def test_go_safe_deadline_near_clock_passes(self):
        """Канал-приёмник рядом с настоящим time.Since (askpass_test.go) не
        превращается в порог только оттого, что часы есть поблизости."""
        path = self.write("askpass_test.go", "package dashboard\n\n" + GO_SAFE_DEADLINE_NEAR_CLOCK)
        self.assertEqual(run(path).returncode, 0)

    def test_go_far_bare_compare_passes(self):
        """Часы за пределами окна WINDOW не улика: cold/warm в одном тесте,
        time.Since в другом, за сто строк, не должны считаться той же
        находкой (feed_test.go, main_test.go в живом прогоне)."""
        far = "\n".join(["\t_ = i"] * 200)
        path = self.write("cache_test.go",
                          "package lib\n\nfunc a() {\n\tt0 := time.Since(x)\n\t_ = t0\n}\n"
                          "func b() {\n" + far + "\n" + GO_BARE_COMPARE_NO_CLOCK + "}\n")
        self.assertEqual(run(path).returncode, 0)

    def test_go_threshold_outside_test_file_is_ignored(self):
        """Сторож смотрит тестовые файлы: тот же порог в боевом коде (ретраи,
        деградация) не его дело, у него своё место разбора."""
        path = self.write("retry.go", "package lib\n\n" + GO_ELAPSED)
        self.assertEqual(run(path).returncode, 0)

    def test_go_threshold_with_mark_passes(self):
        marked = GO_ELAPSED.replace(
            "400*time.Millisecond {",
            "400*time.Millisecond { // стенной порог: временный, снят DK-746\n"
        )
        path = self.write("feed_test.go", "package dashboard\n\n" + marked)
        self.assertEqual(run(path).returncode, 0)

    def test_py_clock_and_assert_across_methods_is_caught(self):
        path = self.write("chat_in_test.py", "import time\n\n\nclass CostTest:\n" + PY_CLOCK_ASSERT)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("assertLess", r.stdout)

    def test_py_fact_based_passes(self):
        path = self.write("chat_in_test.py", "import time\n\n\nclass CostTest:\n" + PY_FACT)
        self.assertEqual(run(path).returncode, 0)

    def test_py_assert_without_clock_passes(self):
        """assertLess сам по себе не про время: числовые границы без
        time.time() в файле это обычная проверка значения, не порог."""
        path = self.write("budget_test.py",
                          "class Test:\n    def test_x(self):\n"
                          "        self.assertLess(spent, budget)\n")
        self.assertEqual(run(path).returncode, 0)

    def test_py_raw_assert_threshold_is_caught(self):
        """Простой скрипт без unittest (testdata/project/test_tool.py и
        похожие): голый assert с порогом ловится так же, как assertLess."""
        path = self.write("test_tool.py", PY_RAW_ASSERT)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("elapsed < 0.05", r.stdout)

    def test_py_raw_assert_unrelated_number_passes(self):
        path = self.write("test_tool.py", PY_RAW_ASSERT_UNRELATED)
        self.assertEqual(run(path).returncode, 0)

    def test_py_standalone_age_still_caught(self):
        """Само слово «age» (не «agentctl») остаётся уликой: сужение под
        agentctl не должно съесть образец DK-908 целиком."""
        path = self.write("test_tool.py",
                          "at = time.time()\nage = time.time() - at\n"
                          "assert age < 5\n")
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("age < 5", r.stdout)

    def test_py_bare_compare_is_caught(self):
        """Дословная живая находка obeycheck: два замера сравниваются
        напрямую, без порога вовсе."""
        path = self.write("test_tool.py", "import time\n" + PY_BARE_COMPARE)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("time2 < time1", r.stdout)

    def test_py_far_clock_passes(self):
        """time.time() ради штампа фикстуры за сотни строк от assertLess не
        улика (chat_in_test.py в живом прогоне сторожа): окно ограничивает
        находку соседством, а не фактом присутствия часов в файле."""
        far = "\n".join(["    pass"] * 200)
        text = ("def stamp():\n    return time.time()\n\n\n"
                + far + "\n\n\n"
                + "class Budget:\n    def test_x(self):\n"
                + "        self.assertLess(spent, budget)\n")
        path = self.write("budget_test.py", "import time\n\n\n" + text)
        self.assertEqual(run(path).returncode, 0)

    def test_py_timeit_compare_is_caught(self):
        path = self.write("test_tool.py", "import timeit\n" + PY_TIMEIT_COMPARE)
        r = run(path)
        self.assertEqual(r.returncode, 1)
        self.assertIn("time_second < time_first", r.stdout)

    def test_py_agentctl_name_not_duration_word_passes(self):
        """«agentctl» начинается с «age», но это не возраст: слово должно
        матчить «age» само по себе, не любой идентификатор с этим префиксом."""
        path = self.write("test_tool.py", "import time\n" + PY_AGENTCTL_NEAR_CLOCK)
        self.assertEqual(run(path).returncode, 0)

    def test_py_usage_word_not_duration_word_passes(self):
        """«usage» содержит «age» серединой слова, без границы перед ней, а
        рядом стоит «>» из перенаправления в shell-строке: живой прогон
        поймал ровно эту строку в quota_refresh_test.py."""
        path = self.write("test_tool.py",
                          "started = time.time()\n"
                          "cmd = 'echo \"панель /usage не узналась\" >&2\\n'\n")
        self.assertEqual(run(path).returncode, 0)

    def test_py_poll_loop_passes(self):
        """Опрос до срока это ожидание внешнего события, не порог: тот же
        случай, что Go `time.Now().Before(deadline)`."""
        path = self.write("test_tool.py", "import time\n" + PY_POLL_LOOP)
        self.assertEqual(run(path).returncode, 0)

    def test_py_threshold_with_mark_passes(self):
        marked = PY_CLOCK_ASSERT.replace(
            "0.05, \"холостой ход дороже\")",
            "0.05, \"холостой ход дороже\")  # стенной порог: временный, снят DK-1038\n"
        )
        path = self.write("chat_in_test.py", "import time\n\n\nclass CostTest:\n" + marked)
        self.assertEqual(run(path).returncode, 0)

    def test_non_test_python_file_is_ignored(self):
        path = self.write("budget.py", "import time\n" + PY_CLOCK_ASSERT)
        self.assertEqual(run(path).returncode, 0)


def added_lines(path, text):
    """file:line:text того же формата, что awk в pre-commit собирает из
    staged-диффа, а --diff читает со стандартного входа."""
    return "".join("%s:%d:%s\n" % (path, i, line)
                   for i, line in enumerate(text.splitlines(), 1))


class TestDiffMode(unittest.TestCase):
    def test_go_threshold_in_diff_is_caught(self):
        r = run("--diff", input=added_lines("tools/dashboard/feed_test.go", GO_ELAPSED))
        self.assertEqual(r.returncode, 1)
        self.assertIn("feed_test.go:1:", r.stdout)

    def test_go_fact_in_diff_passes(self):
        r = run("--diff", input=added_lines("tools/dashboard/feed_test.go", GO_FACT))
        self.assertEqual(r.returncode, 0)

    def test_py_clock_and_assert_split_across_diff_lines_is_caught(self):
        """Часы и порог из PY_CLOCK_ASSERT приходят в диффе разными строками
        (метод clock и метод теста), а группировка по файлу должна свести их
        воедино, как если бы оба метода читались из одного файла."""
        r = run("--diff", input=added_lines("hooks/chat_in_test.py", PY_CLOCK_ASSERT))
        self.assertEqual(r.returncode, 1)

    def test_unrelated_file_in_diff_passes(self):
        r = run("--diff", input=added_lines("docs/README.md", GO_ELAPSED))
        self.assertEqual(r.returncode, 0)

    def test_empty_diff_passes(self):
        self.assertEqual(run("--diff", input="").returncode, 0)


class TestPreCommit(unittest.TestCase):
    """Пятый рубеж коммита: staged-дифф идёт через настоящий pre-commit, а не
    через тул напрямую, тем же приёмом, что у соседних check_*_test.py."""

    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)

    def stage(self, name, text):
        path = os.path.join(self.repo, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        subprocess.run(["git", "-C", self.repo, "add", name], check=True)

    def hook(self, **env):
        return subprocess.run([os.path.join(HERE, "pre-commit")], cwd=self.repo,
                              env=dict(os.environ, **env),
                              capture_output=True, text=True)

    def test_new_test_with_threshold_is_caught(self):
        self.stage("feed_test.go", "package dashboard\n\n" + GO_ELAPSED)
        r = self.hook()
        self.assertEqual(r.returncode, 1)
        self.assertIn("стенным порогом", r.stderr)
        # ссылка на раздел бьёт по названию: ревью DK-1126 поймало опечатку,
        # где вместо нового раздела в подсказке стоял соседний «Откуда
        # берётся ожидание».
        self.assertIn("Факт вместо стенного времени", r.stderr)

    def test_new_test_with_fact_passes(self):
        self.stage("feed_test.go", "package dashboard\n\n" + GO_FACT)
        self.assertEqual(self.hook().returncode, 0)

    def test_marked_threshold_passes(self):
        marked = GO_ELAPSED.replace(
            "400*time.Millisecond {",
            "400*time.Millisecond { // стенной порог: временный, снят DK-746\n"
        )
        self.stage("feed_test.go", "package dashboard\n\n" + marked)
        self.assertEqual(self.hook().returncode, 0)

    def test_no_verify_bypasses_the_gate(self):
        self.stage("feed_test.go", "package dashboard\n\n" + GO_ELAPSED)
        r = subprocess.run(["git", "-C", self.repo, "commit", "-q", "--no-verify",
                            "-m", "test"], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
