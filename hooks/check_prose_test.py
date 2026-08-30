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
import re
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


def human_replies():
    """Реплики пользователя из корпуса эталонов одним куском.

    Опора калибровки после DK-533: фрагменты корпуса скилла prose, у которых
    в источнике стоит журнал сессии. Половина корпуса взята из трекеров, и
    чужая проза для проверки порогов пользователя не годится.
    """
    root = os.path.join(os.path.dirname(HERE), "kit", "skills", "prose", "corpus")
    out = []
    for name in sorted(os.listdir(root)):
        if not name.endswith(".md"):
            continue
        with open(os.path.join(root, name), encoding="utf-8") as f:
            text = f.read()
        for block in re.split(r"(?m)^## \d+\n", text)[1:]:
            head, _, body = block.partition("\n")
            if "журнал сессии" in head:
                out.append(re.sub(r"(?m)^роль:.*\n", "", body).strip())
    return "\n\n".join(out) + "\n"


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

    def test_full_form_not_x_but_y_is_not_argued(self):
        """Полная форма «не X, а Y» доводом не считается (DK-524).

        Замер DK-446 дал у неё 1,5 на тысячу слов у агентов против 2,3 у
        людей. Это лексика пользователя, и находка на ней означала бы, что
        сторож правит его же фразу.
        """
        _, v = prose.measure("Доску правит не редактор, а утилита. "
                             "Ранг считается формулой из четырёх слагаемых.\n")
        self.assertEqual(v["argued"], 0.0)
        self.assertEqual(v["but_not_tail"], 0.0)

    def test_colon_in_the_middle(self):
        t, v = prose.measure("Доска ведётся утилитой: строка не разъезжается. "
                             "Ранг считается формулой.\n")
        self.assertAlmostEqual(v["colon_mid"], 1000.0 / t.words, places=3)

    def test_tail_but_not(self):
        t, v = prose.measure("Доску правит утилита, а не редактор. "
                             "Ранг считается формулой.\n")
        self.assertAlmostEqual(v["but_not_tail"], 1000.0 / t.words, places=3)

    def test_tail_without_comma_is_a_conjunction(self):
        """«а не» без запятой это союз, а формы хвоста в нём нет (DK-533).

        Так пишут имя самой метрики и вопрос «а не проще ли»: находка на них
        считала бы разговор про сторожа поломкой текста.
        """
        _, v = prose.measure("Метрика хвост а не считает частоту формы. "
                             "Ранг считается формулой из четырёх слагаемых.\n")
        self.assertEqual(v["but_not_tail"], 0.0)

    def test_tail_in_quotes_is_a_citation(self):
        """Цитата в кавычках это чужая фраза, и шаблон автора в ней не меряется.

        Замер DK-526 дал пять находок хвоста на восьми текстах, и четыре из
        них были цитированием: имя метрики «хвост «..., а не Y»» и разобранная
        реплика пользователя.
        """
        _, v = prose.measure("Разбор назвал примету «хвост «..., а не Y»» "
                             "первой строкой отчёта. "
                             "Ранг считается формулой из четырёх слагаемых.\n")
        self.assertEqual(v["but_not_tail"], 0.0)

    def test_tail_in_backticks_is_a_citation(self):
        """Счётчик зовут и напрямую, и обратные апострофы он снимает сам.

        Разбор прозы меняет инлайн-код на слово CODE раньше счёта, так что
        через measure такая фраза не дошла бы до регулярки вовсе.
        """
        self.assertEqual(prose.tails("Ключ `хвост, а не Y` лежит в конфиге."), 0)

    def test_full_form_not_x_but_not_y_is_not_a_tail(self):
        """Полная форма «не X, а не Y» это лексика пользователя (DK-524)."""
        _, v = prose.measure("Доску правит не редактор, а не человек руками. "
                             "Ранг считается формулой из четырёх слагаемых.\n")
        self.assertEqual(v["but_not_tail"], 0.0)

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


class TestMachineLines(unittest.TestCase):
    """DK-550: машинные записи файла задачи не считаются прозой.

    К каждой машинной строке (источник в hooks/check-prose.py, комментарий
    у соответствующей регулярки) стоит парой похожая строка, которую написал
    человек: тот же префикс, но без хвоста, который строит код. Проверка
    держит оба конца: машинная строка мимо счёта, похожая строка в счёте.
    """

    STAGE = ("- Разработка: субагент opus/high по вердикту pick "
             "(квота: week_all 42%, снимок 3м назад), "
             "2026-08-27 13:53-14:08.")
    STAGE_LOOKALIKE = "- Разработка: сроки жмут, а бюджет невелик."

    RANK = "- Неопределённость 1: место известно, открыт был только выбор."
    RANK_LOOKALIKE = "- Неопределённость есть, но она невелика."

    ACCEPT_KIND = "- вид: mixed"
    ACCEPT_KIND_LOOKALIKE = "- Вид на будущее: доделать позже."

    ACCEPT_BARRIER = "- барьер «глаза»: вид экрана на телефоне"
    ACCEPT_BARRIER_LOOKALIKE = "- Барьер на глазах: пример виден сразу."

    ACCEPT_OUTCOME = ("  - headless-браузер с замером: годится, "
                       "ширины уходят агенту")
    ACCEPT_OUTCOME_LOOKALIKE = "- Отчёт готов: результат годится для показа."

    DEPLOY_MERGE = "- 2026-08-27 слито: 70b2db5d, 5d2e6e36, e35fb942"
    DEPLOY_MERGE_LOOKALIKE = "- Слияние прошло тяжело: конфликтов было пять."

    DEPLOY_SMOKE = "- smoke прогнан, 2026-08-27"
    DEPLOY_SMOKE_LOOKALIKE = "- Smoke прогнали, а число в лог не попало."

    MACHINE = (STAGE, RANK, ACCEPT_KIND, ACCEPT_BARRIER, ACCEPT_OUTCOME,
               DEPLOY_MERGE, DEPLOY_SMOKE)
    LOOKALIKE = (STAGE_LOOKALIKE, RANK_LOOKALIKE, ACCEPT_KIND_LOOKALIKE,
                 ACCEPT_BARRIER_LOOKALIKE, ACCEPT_OUTCOME_LOOKALIKE,
                 DEPLOY_MERGE_LOOKALIKE, DEPLOY_SMOKE_LOOKALIKE)

    def test_machine_lines_are_recognized(self):
        for line in self.MACHINE:
            self.assertTrue(prose.is_machine_line(line), line)

    def test_lookalike_lines_are_not_machine(self):
        for line in self.LOOKALIKE:
            self.assertFalse(prose.is_machine_line(line), line)

    def test_machine_lines_do_not_count_toward_metrics(self):
        body = "\n".join(("Раздел ведёт запись работы.",) + self.MACHINE) + "\n"
        t, v = prose.measure(body)
        self.assertEqual(len(t.sentences), 1)
        self.assertEqual(t.words, 4)
        self.assertEqual(v["colon_mid"], 0.0)

    def test_lookalike_lines_count_toward_metrics(self):
        body = "\n".join(self.LOOKALIKE) + "\n"
        t, v = prose.measure(body)
        self.assertEqual(len(t.sentences), 7)
        self.assertGreater(v["colon_mid"], 0.0)

    def test_outcome_indent_distinguishes_machine_from_example(self):
        """DK-550, замечание ревью: отступ, а не текст, несёт весь смысл.

        ACCEPTANCE.md приводит читателю тот же синтаксис «обход: исход,
        причина» верхним уровнем, примером, а не записью реального обхода.
        Настоящий обход стоит только вложенным пунктом под строкой барьера,
        ровно двумя пробелами, как того требует acceptBypassRe у taskctl.
        """
        nested = "  - headless-браузер с замером: годится, ширины уходят агенту"
        top_level = "- headless-браузер с замером: годится, ширины уходят агенту"
        self.assertTrue(prose.is_machine_line(nested))
        self.assertFalse(prose.is_machine_line(top_level))


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

    def test_proofread_corpus_is_skipped(self):
        """Сторожевой корпус вычитки полон плохих фраз нарочно (DK-524)."""
        config(self.conf, warn={"colon_mid": 5})
        for path in ("kit/skills/proofread/corpus.md",
                     "kit/skills/prose/corpus/task.md"):
            r = self.hook(path, COLONS)
            self.assertEqual(r.returncode, 0, path)
            self.assertEqual(r.stdout, "", path)

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

    def test_warning_stands_below_blocking(self):
        """Направление порогов: предупреждение ниже блокировки у всех метрик.

        Перекалибровка двигает числа, и порядок тут единственное, что её
        переживает: сравнявшись, пороги дали бы блокировку без предупреждения.
        """
        conf, gaps = prose.read_config(SHIPPED)
        self.assertEqual(gaps, [])
        for m in prose.METRICS:
            self.assertLess(conf.warn[m.key], conf.block[m.key], m.key)

    def test_shipped_thresholds_pass_human_text(self):
        """Калибровка: реплики пользователя из корпуса порогов не переходят.

        Опора сменилась в DK-533. Первым коммитом RULES.md пороги проверять
        нельзя: приёмка DK-522 отбраковала его как текст, писанный вместе с
        моделью. Порог, съехавший на прозу пользователя, ловит его же фразу.

        Метрика aphorism из проверки вынута. На этой выборке двадцать один
        абзац, шаг метрики 4,8%, а порог стоит на 2%: сойтись он тут не может
        ни при какой калибровке. Один абзац реплик и правда кончается на «то
        есть», разбор в файле задачи DK-533.
        """
        conf, gaps = prose.read_config(SHIPPED)
        self.assertEqual(gaps, [])
        _, values = prose.measure(human_replies())
        found = [f.metric.key for f in prose.findings(values, conf)
                 if f.metric.key != "aphorism"]
        self.assertEqual(found, [], values)


class TestChecklist(unittest.TestCase):
    """Сторож и вычитка называют одни и те же приметы (DK-524, DoD 4).

    Разъехавшись, они дают агенту два разных списка: сторож считает одно, а
    вычитка правит другое, и текст чинится по кругу.
    """

    def setUp(self):
        self.skill = os.path.join(os.path.dirname(HERE), "kit", "skills",
                                  "proofread", "SKILL.md")
        with open(self.skill, encoding="utf-8") as f:
            self.text = f.read()

    def test_skill_names_every_metric_key(self):
        for m in prose.METRICS:
            self.assertIn("`%s`" % m.key, self.text, m.key)

    def test_skill_points_are_numbered_by_the_guard_order(self):
        """Порядок пунктов чек-листа совпадает с порядком метрик сторожа."""
        seen = [m.key for m in prose.METRICS
                if "`%s`" % m.key in self.text]
        at = [self.text.index("`%s`" % k) for k in seen]
        self.assertEqual(at, sorted(at))
        self.assertEqual(len(seen), len(prose.METRICS))


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
