#!/usr/bin/env python3
"""Проверка сторожа прозы: счёт метрик, пороги предупреждения и блокировки,
чистый текст, отсутствие конфига и режимы разбора (файл, staged-дифф, хук).

Пороги в тестах свои: боевые числа живут в kit/prose.toml и меняются
перекалибровкой, а тест должен падать от правки счёта, а не от правки порога.
Проверяется отдельно и сам боевой конфиг: ключи метрик в нём и в коде обязаны
сойтись, иначе сторож молчит на живой машине.
"""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-prose.py")
SHIPPED = os.path.join(os.path.dirname(HERE), "kit", "prose.toml")


def load():
    spec = importlib.util.spec_from_file_location("check_prose", TOOL)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, HERE)
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.pop(0)
    return mod


prose = load()

# Пороги, при которых не срабатывает ничего: тест опускает нужный ключ сам.
HIGH = {"sentence_len": 900, "long_sentences": 900, "argued": 900,
        "colon_mid": 900, "but_not_tail": 900, "aphorism": 900}

# Восемь коротких фраз без единой приметы: доводов нет, двоеточий нет,
# противопоставлений нет, конец абзаца не обобщает.
CLEAN = (
    "Дашборд показывает список задач. Строка задачи хранит ранг и статус. "
    "Утилита пишет доску одной командой. Ветку заводит отдельный шаг. "
    "Выкат идёт после слияния. Тесты гоняются до коммита. "
    "Отчёт печатается в конце прогона. Человек читает отчёт и решает сам.\n"
)
# Тот же объём, но с двоеточием-доводом в каждой второй фразе.
COLONS = (
    "Дашборд показывает список задач: строка задачи хранит ранг и статус. "
    "Утилита пишет доску одной командой: ветку заводит отдельный шаг. "
    "Выкат идёт после слияния: тесты гоняются до коммита. "
    "Отчёт печатается в конце прогона: человек читает отчёт и решает сам.\n"
)


def config(path, mode="warn", min_words=30, suffixes=None, warn=None, block=None):
    """Конфиг порогов на диск. Всё, чего тест не назвал, поднято за облака."""
    lines = ["[prose]", 'mode = "%s"' % mode, "min_words = %d" % min_words,
             "suffixes = [%s]" % ", ".join('"%s"' % s
                                           for s in (suffixes or [".md"]))]
    for section, given in (("warn", warn), ("block", block)):
        lines.append("[%s]" % section)
        table = dict(HIGH)
        table.update(given or {})
        lines += ["%s = %d" % (k, v) for k, v in table.items()]
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    return path


def run(*args, **kw):
    env = dict(os.environ)
    env.update(kw.pop("env", {}))
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, env=env, **kw)


def hook_event(path, text):
    return json.dumps({"tool_input": {"file_path": path, "new_string": text}})


class TestMetrics(unittest.TestCase):
    """Счёт метрик на записанном фрагменте: числа берутся из текста, а не из
    прошлого прогона, поэтому в тесте они посчитаны руками."""

    def test_sentence_length_counts_words(self):
        t, v = prose.measure("Доска ведётся утилитой. Ранг считается формулой "
                             "из четырёх слагаемых.\n")
        self.assertEqual(t.words, 9)
        self.assertEqual(len(t.sentences), 2)
        self.assertEqual(v["sentence_len"], 4.5)

    def test_long_sentence_share(self):
        long = "Слово " + " ".join(["слово"] * 29) + "."
        _, v = prose.measure("Строка задачи короткая. %s\n" % long)
        self.assertEqual(v["long_sentences"], 50.0)

    def test_argued_counts_reason_in_same_phrase(self):
        _, v = prose.measure("Доска ведётся утилитой, чтобы строка не разъезжалась. "
                             "Ранг считается формулой из четырёх слагаемых.\n")
        self.assertEqual(v["argued"], 50.0)

    def test_colon_in_the_middle(self):
        t, v = prose.measure("Доска ведётся утилитой: строка не разъезжается. "
                             "Ранг считается формулой.\n")
        self.assertAlmostEqual(v["colon_mid"], 1000.0 / t.words, places=3)

    def test_tail_but_not(self):
        t, v = prose.measure("Доску правит утилита, а не редактор. "
                             "Ранг считается формулой.\n")
        self.assertAlmostEqual(v["but_not_tail"], 1000.0 / t.words, places=3)

    def test_aphorism_at_the_end_of_paragraph(self):
        text = ("Доска ведётся утилитой. Строка задачи хранит ранг и статус. "
                "Выкат идёт после слияния. Это и есть весь порядок работы.\n\n"
                "Ветку заводит отдельный шаг. Тесты гоняются до коммита. "
                "Отчёт печатается в конце прогона. Человек читает его сам.\n")
        _, v = prose.measure(text)
        self.assertEqual(v["aphorism"], 50.0)

    def test_code_and_tables_are_not_prose(self):
        raw = ("| метрика | число |\n| --- | --- |\n"
               "```\nx = 1 + 2\n```\n"
               "Доска ведётся утилитой.\n")
        t, _ = prose.measure(raw)
        self.assertEqual(len(t.sentences), 1)


class TestHook(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "prose.toml")

    def hook(self, path, text):
        return run("--hook", env={prose.CONFIG_ENV: self.conf},
                   input=hook_event(path, text))

    def test_clean_text_says_nothing(self):
        config(self.conf, warn={"colon_mid": 5})
        r = self.hook("docs/tasks/DK-001.md", CLEAN)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr, "")

    def test_warn_threshold_adds_context(self):
        config(self.conf, warn={"colon_mid": 5})
        r = self.hook("docs/tasks/DK-001.md", COLONS)
        self.assertEqual(r.returncode, 0)
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("двоеточие в середине фразы", said)
        self.assertIn("предупреждение", said)
        self.assertIn("docs/tasks/DK-001.md", said)

    def test_block_threshold_blocks_the_write(self):
        config(self.conf, mode="block", warn={"colon_mid": 5},
               block={"colon_mid": 10})
        r = self.hook("docs/tasks/DK-001.md", COLONS)
        self.assertEqual(r.returncode, 2)
        self.assertIn("блокировка", r.stderr)
        self.assertIn("как переписать", r.stderr)

    def test_warn_mode_does_not_block_over_block_threshold(self):
        config(self.conf, mode="warn", warn={"colon_mid": 5},
               block={"colon_mid": 10})
        r = self.hook("docs/tasks/DK-001.md", COLONS)
        self.assertEqual(r.returncode, 0)
        self.assertIn("блокировка", r.stdout)

    def test_short_fragment_is_not_measured(self):
        config(self.conf, min_words=500, warn={"colon_mid": 5})
        r = self.hook("docs/tasks/DK-001.md", COLONS)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")

    def test_code_file_is_not_measured(self):
        config(self.conf, warn={"colon_mid": 5})
        r = self.hook("tools/taskctl/board.go", COLONS)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")

    def test_testdata_snapshot_is_skipped(self):
        config(self.conf, warn={"colon_mid": 5})
        r = self.hook("hooks/testdata/claude-code/sample.md", COLONS)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")

    def test_without_config_hook_keeps_quiet(self):
        r = self.hook("docs/tasks/DK-001.md", COLONS)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr, "")


class TestModes(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "prose.toml")

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_file_mode_prints_six_metrics(self):
        config(self.conf)
        path = self.write("DK-001.md", CLEAN)
        r = run(path, env={prose.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 0)
        for m in prose.METRICS:
            self.assertIn(m.title, r.stdout)

    def test_file_mode_returns_one_on_block(self):
        config(self.conf, mode="block", block={"colon_mid": 10})
        path = self.write("DK-001.md", COLONS)
        r = run(path, env={prose.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("порог блокировки", r.stdout)

    def test_diff_mode_groups_added_lines_by_file(self):
        config(self.conf, mode="block", warn={"colon_mid": 5},
               block={"colon_mid": 10})
        added = "".join("docs/tasks/DK-001.md:%d:%s\n" % (i + 1, ln)
                        for i, ln in enumerate(COLONS.splitlines()))
        added += "tools/taskctl/board.go:1:x := 1\n"
        r = run("--diff", env={prose.CONFIG_ENV: self.conf}, input=added)
        self.assertEqual(r.returncode, 1)
        self.assertIn("docs/tasks/DK-001.md", r.stdout)
        self.assertNotIn("board.go", r.stdout)

    def test_diff_mode_warning_does_not_fail_the_commit(self):
        config(self.conf, warn={"colon_mid": 5}, block={"colon_mid": 10})
        added = "".join("docs/tasks/DK-001.md:%d:%s\n" % (i + 1, ln)
                        for i, ln in enumerate(COLONS.splitlines()))
        r = run("--diff", env={prose.CONFIG_ENV: self.conf}, input=added)
        self.assertEqual(r.returncode, 0)
        self.assertIn("двоеточие в середине фразы", r.stdout)


class TestConfig(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "prose.toml")

    def test_missing_config_is_a_finding(self):
        r = run("--config", env={prose.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("порогов не прочесть", r.stdout)
        self.assertIn(self.conf, r.stdout)

    def test_missing_metric_key_is_named(self):
        config(self.conf)
        with open(self.conf, encoding="utf-8") as f:
            text = f.read()
        text = "\n".join(ln for ln in text.split("\n")
                         if not ln.startswith("colon_mid"))
        with open(self.conf, "w", encoding="utf-8") as f:
            f.write(text)
        r = run("--config", env={prose.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("colon_mid", r.stdout)

    def test_unknown_mode_is_a_finding(self):
        config(self.conf, mode="кричать")
        r = run("--config", env={prose.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("mode", r.stdout)

    def test_shipped_config_is_whole(self):
        r = run("--config")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn(SHIPPED, r.stdout)

    def test_shipped_thresholds_pass_human_text(self):
        """Калибровка: первый коммит RULES.md порогов не переходит.

        Это человеческий текст пользователя, и находка на нём означала бы, что
        пороги съехали на его же прозу, а не на агентский шаблон.
        """
        p = subprocess.run(["git", "-C", os.path.dirname(HERE), "show",
                            "7ccc03f8:RULES.md"], capture_output=True, text=True)
        if p.returncode != 0:
            self.skipTest("нет истории репозитория: %s" % p.stderr.strip())
        conf, gaps = prose.read_config(SHIPPED)
        self.assertEqual(gaps, [])
        _, values = prose.measure(p.stdout)
        self.assertEqual(prose.findings(values, conf), [])


class TestPreCommit(unittest.TestCase):
    """Четвёртый рубеж коммита: та же проверка по добавленным строкам."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "prose.toml")
        self.repo = os.path.join(self.tmp, "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)
        os.makedirs(os.path.join(self.repo, "docs", "tasks"))

    def stage(self, text):
        with open(os.path.join(self.repo, "docs", "tasks", "DK-001.md"), "w",
                  encoding="utf-8") as f:
            f.write(text)
        subprocess.run(["git", "-C", self.repo, "add", "docs/tasks/DK-001.md"],
                       check=True)

    def hook(self):
        env = dict(os.environ)
        env[prose.CONFIG_ENV] = self.conf
        return subprocess.run([os.path.join(HERE, "pre-commit")], cwd=self.repo,
                              capture_output=True, text=True, env=env)

    def test_block_threshold_stops_the_commit(self):
        config(self.conf, mode="block", warn={"colon_mid": 5},
               block={"colon_mid": 10})
        self.stage(COLONS)
        r = self.hook()
        self.assertEqual(r.returncode, 1)
        self.assertIn("агентский шаблон", r.stderr)

    def test_warning_lets_the_commit_through(self):
        config(self.conf, warn={"colon_mid": 5}, block={"colon_mid": 10})
        self.stage(COLONS)
        r = self.hook()
        self.assertEqual(r.returncode, 0)
        self.assertIn("двоеточие в середине фразы", r.stderr)

    def test_clean_prose_passes(self):
        config(self.conf, mode="block", warn={"colon_mid": 5},
               block={"colon_mid": 10})
        self.stage(CLEAN)
        r = self.hook()
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")


if __name__ == "__main__":
    unittest.main()
