#!/usr/bin/env python3
"""Проверка сторожа калек: находка на каждом слове боевого списка, молчание на
коде, словаре и чистом тексте, режимы разбора (файл, хук, конфиг).

Список в тестах свой там, где проверяется механика, и боевой там, где
проверяется его состав: слова в kit/prose.toml подбираются разбором, а
механика от их состава не зависит.
"""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-calque.py")
SHIPPED = os.path.join(os.path.dirname(HERE), "kit", "prose.toml")
DICT = os.path.join(os.path.dirname(HERE), "kit", "skills", "proofread",
                    "dictionary.md")


def load():
    spec = importlib.util.spec_from_file_location("check_calque", TOOL)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, HERE)
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.pop(0)
    return mod


calque = load()

WORDS = {
    "splice": "сплайс*, splice -> склейка",
    "production": "продакшн*, продакшен*, production -> боевой контур",
}


def config(path, mode="warn", suffixes=None, words=None):
    lines = ["[calque]", 'mode = "%s"' % mode,
             "suffixes = [%s]" % ", ".join('"%s"' % s
                                           for s in (suffixes or [".md"]))]
    lines.append("[calque-words]")
    for key, value in (WORDS if words is None else words).items():
        lines.append('%s = "%s"' % (key, value))
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


def shipped():
    conf, gaps = calque.read_config(SHIPPED)
    assert conf is not None, gaps
    return conf


def hits(text, conf=None, terms=None):
    """Найденные написания списком: текст находки без обвязки отчёта."""
    found = calque.findings(text, conf or shipped(),
                            calque.dictionary_terms() if terms is None else terms)
    return [f.text for f in found]


class TestWords(unittest.TestCase):
    """Боевой список: каждое слово разбора DK-1022 находится, и находится в
    том написании, в каком оно встретилось живьём."""

    def test_every_word_of_the_list_is_found(self):
        for text in ("Прогон идёт в тестовом регионе.",
                     "Потолок сплайса стоит в конфиге.",
                     "Файл уезжает на продакшн.",
                     "Правка едет на продакшен.",
                     "Ошибка вылезла на production.",
                     "Флаг включает inline вместо файла.",
                     "Инлайн собирается на лету.",
                     "Производственный контур ждёт выката."):
            self.assertTrue(hits(text), text)

    def test_compound_with_a_hyphen_is_found(self):
        self.assertEqual(hits("Отдельно живёт production-часть сборки."),
                         ["production"])

    def test_latin_word_inside_another_word_is_not_a_hit(self):
        self.assertEqual(hits("Слово reproduction калькой не считается."), [])

    def test_replacement_comes_with_the_finding(self):
        found = calque.findings("Потолок сплайса стоит в конфиге.", shipped(),
                                set())
        self.assertEqual(found[0].article.replace, "склейка")


class TestSilence(unittest.TestCase):
    def test_backticks_are_code(self):
        self.assertEqual(hits("Флаг `--inline` берёт файл теста."), [])

    def test_fenced_block_is_code(self):
        text = "Пример прогона:\n\n```\nregcheck --inline src/lib.rs\n```\n"
        self.assertEqual(hits(text), [])

    def test_dictionary_term_is_not_a_calque(self):
        conf, _ = calque.read_config(config(
            os.path.join(tempfile.mkdtemp(), "c.toml"),
            words={"train": "поезд* -> связка задач"}))
        self.assertEqual(hits("Поезд собирается из задач.", conf, set()),
                         ["Поезд"])
        self.assertEqual(hits("Поезд собирается из задач.", conf,
                              calque.dictionary_terms()), [])

    def test_hyphen_compound_outside_the_list_is_quiet(self):
        self.assertEqual(hits("Tmux-сессия и git-хуки лежат рядом."), [])

    def test_map_words_are_quiet(self):
        self.assertEqual(hits("Общий go-модуль каркаса и web-дашборд."), [])

    def test_clean_text_is_quiet(self):
        self.assertEqual(hits("Доска ведётся утилитой, а ветку заводит shipctl."),
                         [])


class TestDictionary(unittest.TestCase):
    def test_terms_are_read_from_the_shipped_dictionary(self):
        terms = calque.dictionary_terms()
        self.assertIn("поезд", terms)
        self.assertNotIn("термин", terms)

    def test_missing_dictionary_is_not_a_failure(self):
        self.assertEqual(calque.dictionary_terms("/нет/такого/файла.md"), set())


class TestLines(unittest.TestCase):
    def test_line_number_survives_stripped_code(self):
        text = "Заголовок\n\n```\ncode\n```\n\nПотолок сплайса стоит тут.\n"
        found = calque.findings(text, shipped(), set())
        self.assertEqual([f.line for f in found], [7])

    def test_frontmatter_is_not_prose(self):
        text = "---\ntitle: сплайс\n---\n\nЧистая строка.\n"
        self.assertEqual(hits(text), [])


class TestHook(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "calque.toml")

    def hook(self, path, text):
        return run("--hook", env={calque.CONFIG_ENV: self.conf},
                   input=hook_event(path, text))

    def test_finding_comes_back_as_context(self):
        config(self.conf)
        r = self.hook("docs/tasks/DK-001.md", "Потолок сплайса стоит в конфиге.")
        self.assertEqual(r.returncode, 0)
        said = json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn("сплайса", said)
        self.assertIn("склейка", said)
        self.assertIn("docs/tasks/DK-001.md", said)

    def test_clean_text_says_nothing(self):
        config(self.conf)
        r = self.hook("docs/tasks/DK-001.md", "Доска ведётся утилитой.")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")

    def test_block_mode_stops_the_write(self):
        config(self.conf, mode="block")
        r = self.hook("docs/tasks/DK-001.md", "Потолок сплайса стоит в конфиге.")
        self.assertEqual(r.returncode, 2)
        self.assertIn("сплайса", r.stderr)

    def test_code_file_is_not_measured(self):
        config(self.conf)
        r = self.hook("tools/taskctl/board.go", "// потолок сплайса")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")

    def test_testdata_snapshot_is_skipped(self):
        config(self.conf)
        r = self.hook("hooks/testdata/claude-code/sample.md", "Потолок сплайса.")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")

    def test_without_config_hook_keeps_quiet(self):
        r = self.hook("docs/tasks/DK-001.md", "Потолок сплайса стоит в конфиге.")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr, "")


class TestModes(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "calque.toml")

    def write(self, name, text):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_file_mode_prints_the_line_and_the_replacement(self):
        config(self.conf)
        path = self.write("a.md", "Первая строка.\nПотолок сплайса стоит тут.\n")
        r = run(path, env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 0)
        self.assertIn("2: «сплайса», замена: склейка", r.stdout)

    def test_file_mode_returns_one_in_block_mode(self):
        config(self.conf, mode="block")
        path = self.write("a.md", "Потолок сплайса стоит тут.\n")
        r = run(path, env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)

    def test_clean_file_says_so(self):
        config(self.conf)
        path = self.write("a.md", "Доска ведётся утилитой.\n")
        r = run(path, env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 0)
        self.assertIn("калек нет", r.stdout)


class TestConfig(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "calque.toml")

    def test_missing_config_is_a_finding(self):
        r = run("--config", env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("списка калек не прочесть", r.stdout)

    def test_empty_list_is_a_finding(self):
        config(self.conf, words={})
        r = run("--config", env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("calque-words", r.stdout)

    def test_article_without_a_replacement_is_named(self):
        config(self.conf, words={"splice": "сплайс*"})
        r = run("--config", env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("splice", r.stdout)

    def test_unknown_mode_is_a_finding(self):
        config(self.conf, mode="кричать")
        r = run("--config", env={calque.CONFIG_ENV: self.conf})
        self.assertEqual(r.returncode, 1)
        self.assertIn("mode", r.stdout)

    def test_shipped_config_is_whole(self):
        r = run("--config")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn(SHIPPED, r.stdout)


if __name__ == "__main__":
    unittest.main()
