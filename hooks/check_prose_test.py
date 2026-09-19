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

    DEPLOY_PENDING = "- 2026-08-27 выкачено, перевод в Check отбит: ворота ждут регрессии"
    DEPLOY_PENDING_LOOKALIKE = "- Перевод в Check отбит без выката: ворота придирчивы."

    DEPLOY_MOVE_DONE = "- 2026-08-27 перевод в Check доведён"
    DEPLOY_MOVE_DONE_LOOKALIKE = "- Перевод в Check доведён без выката, запись ошибочна."

    FORK_HEAD = "- «кусок-3»: заводить его строкой сейчас или ждать повода?"

    STAND = ("- Стенд: 2026-09-18 00:33, дерево a02dde6c, предмет a4677514, "
             "база старый, ярус mini, k 3, сценарии 25-stand-trace, зачтён.")
    STAND_LOOKALIKE = "- Стенд теста дал брак: числа не сошлись."

    STAND_FAIL = ("- Стенд не зачтён: 2026-09-17 01:08, дерево 0dbbeb8f, "
                  "предмет 3df66c98, база старый, ярус mini, k 3, "
                  "сценарии 45-code-name-calque, ворота закрыты.")

    REHEARSAL = ("- Обкатка: 2026-09-18 20:14, свежее дерево 366418d8729d, "
                 "сценарий 8a0b80d4, временный HOME, утилит дерева 11, "
                 "шагов 5, все зелёные.")
    REHEARSAL_LOOKALIKE = "- Обкатка прошла гладко: все сценарии сошлись."

    REHEARSAL_FAIL = ("- Обкатка не зачтена: 2026-09-18 20:14, "
                       "свежее дерево 366418d8729d, сценарий 8a0b80d4, "
                       "временный HOME, утилит дерева 11, шагов 5, "
                       "1 красный.")

    PROOFREAD = "- Вычитка: 2 файла, 5 правок, 1 пометка, 2026-08-12."
    PROOFREAD_LOOKALIKE = "- Вычитка заняла час: правок вышло немного."

    PROSE_MARK = ("- Сторож прозы: docs/tasks/DK-001.md, двоеточие в "
                  "середине фразы 105,3 при пороге 5, оставлено: причина.")
    PROSE_MARK_LOOKALIKE = "- Сторож прозы работал долго: результат оказался спорным."

    EXCEPTION = ("- Исключение: обкатка (сценарий обкатан зелёным на main "
                 "2c5a691d после слияния, вывод в «Проверке»)")
    EXCEPTION_LOOKALIKE = "- Исключение из правила: сроки поджимают, а бюджет мал."

    RETURN = "- Возврат: постановка, проверка, 2026-09-05, прод падает на старте."
    RETURN_LOOKALIKE = "- Возврат к вопросу: тема не закрыта, а решения нет."

    MACHINE = (STAGE, RANK, ACCEPT_KIND, ACCEPT_BARRIER, ACCEPT_OUTCOME,
               DEPLOY_MERGE, DEPLOY_SMOKE, DEPLOY_PENDING, DEPLOY_MOVE_DONE,
               FORK_HEAD, STAND, STAND_FAIL, REHEARSAL, REHEARSAL_FAIL,
               PROOFREAD, PROSE_MARK, EXCEPTION, RETURN)
    LOOKALIKE = (STAGE_LOOKALIKE, RANK_LOOKALIKE, ACCEPT_KIND_LOOKALIKE,
                 ACCEPT_BARRIER_LOOKALIKE, ACCEPT_OUTCOME_LOOKALIKE,
                 DEPLOY_MERGE_LOOKALIKE, DEPLOY_SMOKE_LOOKALIKE,
                 DEPLOY_PENDING_LOOKALIKE, DEPLOY_MOVE_DONE_LOOKALIKE,
                 STAND_LOOKALIKE, REHEARSAL_LOOKALIKE, PROOFREAD_LOOKALIKE,
                 PROSE_MARK_LOOKALIKE, EXCEPTION_LOOKALIKE, RETURN_LOOKALIKE)

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
        self.assertEqual(len(t.sentences), len(self.LOOKALIKE))
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


class TestForkLines(unittest.TestCase):
    """DK-887: перечень развилок, который пишет `taskctl decide`, не проза.

    Подстроки развилки держат отступ в два пробела (forkIndent в
    internal/taskform/forks.go), ровно как обход приёмки у ACCEPT_OUTCOME_RE:
    тот же отступ без него это не запись, а пример в чужой прозе.
    """

    SUB_LINES = (
        "  - решает: человек",
        "  - решает: исполнитель",
        "  - рекомендация: построчные регулярки по прецеденту DK-550",
        "  - равенство: обе оценки держат один и тот же довод",
        "  - вариант: строкой сейчас",
        "  - решено человеком 2026-09-09: завести строкой сейчас",
        "  - решено агентом 2026-09-19: построчные регулярки",
        "  - оставлена человеком 2026-09-09",
        "  - оставлена человеком 2026-09-09: причина передачи",
    )

    def test_fork_head_is_machine(self):
        self.assertTrue(prose.is_machine_line(
            "- «кусок-3»: заводить его строкой сейчас или ждать повода?"))

    def test_fork_sub_lines_are_machine(self):
        for line in self.SUB_LINES:
            self.assertTrue(prose.is_machine_line(line), line)

    def test_fork_sub_indent_distinguishes_record_from_example(self):
        nested = "  - решает: человек"
        top_level = "- решает: человек"
        self.assertTrue(prose.is_machine_line(nested))
        self.assertFalse(prose.is_machine_line(top_level))

    def test_fork_question_and_recommendation_do_not_count(self):
        """Сюжет DK-396: развилка с длинной рекомендацией красила метрику
        «абзац кончается обобщением» ложно, потому что перечень считался
        прозой целиком, от головы до подстрок."""
        body = "\n".join((
            "- «кусок-3»: нарезка цели оставила кусок в накопителе, заводить "
            "его строкой сейчас или ждать повода, значит это и есть развилка?",
            "  - решает: человек",
            "  - рекомендация: завести строкой сейчас, это и есть дешёвый шаг",
            "  - вариант: строкой сейчас",
            "  - решено человеком 2026-09-09: завести строкой сейчас",
        )) + "\n"
        t, v = prose.measure(body)
        self.assertEqual(len(t.sentences), 0)
        self.assertEqual(t.words, 0)
        self.assertEqual(v["aphorism"], 0.0)


class TestReviewHeads(unittest.TestCase):
    """DK-887: у строки уровня и вердикта машинная только голова.

    Суть проверки, которую пишет ревьювер, остаётся под сторожем не хуже
    отдельного замечания (tools/taskctl/review.go): DK-1050 показал цену
    того, что ревьювер сам правит голову руками, спасаясь от находки.
    """

    def test_review_level_head_is_stripped_reason_stays(self):
        line = "Уровень 2 до aac93b6: неопределённость 1, риск: невелик"
        self.assertFalse(prose.is_machine_line(line))
        self.assertEqual(prose.strip_machine_head(line),
                          "неопределённость 1, риск: невелик")

    def test_review_verdict_head_is_stripped_note_stays(self):
        line = "- Вердикт: без замечаний. DoD сверен построчно: всё учтено."
        self.assertFalse(prose.is_machine_line(line))
        self.assertEqual(prose.strip_machine_head(line),
                          "- DoD сверен построчно: всё учтено.")

    def test_review_verdict_head_with_sha_is_stripped_note_stays(self):
        """DK-812: второй и следующие круги несут в голове sha, до которого
        стоял проверенный код, а суть проверки остаётся той же строкой."""
        line = "- Вердикт: без замечаний до e4f5a6b. Второй круг сверен."
        self.assertFalse(prose.is_machine_line(line))
        self.assertEqual(prose.strip_machine_head(line),
                          "- Второй круг сверен.")

    def test_review_verdict_with_sha_without_note_leaves_nothing_to_count(self):
        t, v = prose.measure("- Вердикт: без замечаний до e4f5a6b.\n")
        self.assertEqual(len(t.sentences), 0)
        self.assertEqual(t.words, 0)

    def test_review_verdict_without_note_leaves_nothing_to_count(self):
        t, v = prose.measure("- Вердикт: без замечаний.\n")
        self.assertEqual(len(t.sentences), 0)
        self.assertEqual(t.words, 0)

    def test_review_verdict_note_keeps_own_colon_under_count(self):
        """Голова не даёт двоеточие в счёт, а двоеточие самой сути ревьювера
        считается как прежде: ровно та развилка, которую сломал DK-1050."""
        body = "- Вердикт: без замечаний. DoD сверен построчно: всё учтено.\n"
        t, v = prose.measure(body)
        self.assertEqual(len(t.sentences), 1)
        self.assertGreater(v["colon_mid"], 0.0)


class TestArchiveFixtures(unittest.TestCase):
    """DK-887, DoD: фикстуры это копии архивных файлов задач.

    На DK-396 доля «абзац кончается обобщением» с разделом «Развилки» и без
    него обязана совпасть: до правки перечень развилок считался прозой и
    красил метрику ложно (задача, «Что происходит»).
    """

    DIR = os.path.join(HERE, "testdata", "dk-887")

    def read(self, name):
        with open(os.path.join(self.DIR, name), encoding="utf-8") as f:
            return f.read()

    def test_dk396_aphorism_share_same_with_and_without_forks(self):
        raw = self.read("DK-396.md")
        without_forks = re.sub(r"(?ms)^## Развилки\n.*?(?=^## )", "", raw)
        self.assertNotEqual(raw, without_forks)
        t_with, v_with = prose.measure(raw)
        t_without, v_without = prose.measure(without_forks)
        self.assertEqual(v_with["aphorism"], v_without["aphorism"])
        self.assertEqual(t_with.words, t_without.words)

    def test_dk631_review_verdict_note_still_counted(self):
        """DK-631, черновик DK-1050: тот же файл несёт «Исключение»,
        «Стенд»/«Обкатка» и большой вердикт разом, разбор не должен падать и
        не должен молчать про сам вердикт."""
        raw = self.read("DK-631.md")
        t, v = prose.measure(raw)
        self.assertGreater(t.words, 0)
        self.assertGreater(v["colon_mid"], 0.0)

    def test_dk894_parses_without_error(self):
        raw = self.read("DK-894.md")
        t, v = prose.measure(raw)
        self.assertGreater(t.words, 0)


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


class TestFindingText(unittest.TestCase):
    """Текст находки: чем берётся порог и куда пишется пометка (DK-606.2)."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.conf = os.path.join(self.tmp, "prose.toml")

    def said(self, path, text):
        r = run("--hook", env={prose.CONFIG_ENV: self.conf},
                input=hook_event(path, text))
        self.assertEqual(r.returncode, 0)
        return json.loads(r.stdout)["hookSpecificOutput"]["additionalContext"]

    def test_finding_tells_to_keep_the_claim(self):
        config(self.conf, warn={"colon_mid": 5})
        said = self.said("docs/tasks/DK-001.md", COLONS)
        self.assertIn("перестройкой фразы с тем же утверждением", said)
        self.assertIn("подгонять смысл под порог нельзя", said)

    def test_finding_carries_a_filled_mark_for_the_stage_section(self):
        config(self.conf, warn={"colon_mid": 5})
        said = self.said("docs/tasks/DK-001.md", COLONS)
        self.assertIn("«Ход работы»", said)
        self.assertIn("- Сторож прозы: docs/tasks/DK-001.md, "
                      "двоеточие в середине фразы ", said)
        self.assertIn(" при пороге 5, оставлено: <причина>.", said)

    def test_every_finding_gets_its_own_mark(self):
        config(self.conf, warn={"colon_mid": 5, "argued": 20})
        said = self.said("docs/tasks/DK-001.md", COLONS)
        marks = [ln for ln in said.split("\n")
                 if ln.startswith("- Сторож прозы: ")]
        self.assertEqual(len(marks), 2)
        self.assertIn("довод в той же фразе", marks[0])
        self.assertIn("двоеточие в середине фразы", marks[1])

    def test_text_outside_a_task_reports_the_same_line(self):
        config(self.conf, warn={"colon_mid": 5})
        said = self.said("README.md", COLONS)
        self.assertIn("в отчёт захода", said)
        self.assertIn("- Сторож прозы: README.md, ", said)


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
