#!/usr/bin/env python3
"""Тесты сборщика и выборки корпуса прозы.

Журнал сессии здесь синтетический: живые журналы машины лежат в домашнем
каталоге, меняются каждый день и содержат личные тексты, поэтому тест на них
и недетерминирован, и небезопасен."""
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stdout, redirect_stderr

import prose

HUMAN = (
    "Смотри, тут вот какая штука выходит. Мы завели доску и сразу упёрлись в то, "
    "что строку задачи правит кто угодно и чем угодно, а инварианты никто не "
    "проверяет. Давай сделаем утилиту, которая одна умеет двигать статус, и "
    "запретим руками лезть в файл доски."
)
HUMAN2 = (
    "Не годится так. Ты в отчёте написал, что тесты зелёные, а прогон был на "
    "старом коде и краснота не проверялась вовсе. Перегони regcheck от коммита "
    "с фиксом и покажи мне вывод целиком, без пересказа своими словами."
)


С_ЗАГОЛОВКАМИ = (
    "Оставляю свои замечания по задаче, их накопилось много и все они про одно.\n"
    "## 1. Лента уведомлений\n"
    "Открывается прямо в основном меню, а привычное место для неё вверху "
    "страницы, значком колокольчика.\n"
    "## 2. Скорость\n"
    "Стартовая страница дашборда открывается несколько секунд, для "
    "современного приложения это просто недопустимо."
)


def entry(text, kind="user", **extra):
    rec = {
        "type": kind,
        "timestamp": "2026-08-20T10:00:00.000Z",
        "message": {"role": "user", "content": text},
    }
    rec.update(extra)
    return rec


def assistant_entry(text, ts):
    return {
        "type": "assistant",
        "timestamp": ts,
        "message": {"role": "assistant",
                    "content": [{"type": "text", "text": text}]},
    }


def tool_result_entry():
    return {
        "type": "user",
        "timestamp": "2026-08-20T10:00:01.000Z",
        "message": {
            "role": "user",
            "content": [{"type": "tool_result", "content": "ok", "tool_use_id": "t1"}],
        },
    }


class JournalCase(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="prose-test-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.project = os.path.join(self.root, "-Users-x-projects-devkit")
        os.makedirs(self.project)

    def write(self, name, records):
        path = os.path.join(self.project, name)
        with open(path, "w", encoding="utf-8") as f:
            for rec in records:
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        return path


class TestCollect(JournalCase):
    def test_собирает_реплики_человека(self):
        self.write("s1.jsonl", [entry(HUMAN), entry(HUMAN2)])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["kept"], 2)
        self.assertEqual([t for _, _, t in got], [HUMAN, HUMAN2])
        self.assertEqual(got[0][1], "2026-08-20")

    def test_служебные_вставки_отсеиваются(self):
        self.write("s1.jsonl", [
            entry("<command-name>taskctl</command-name>\n" + HUMAN),
            entry(HUMAN2 + "\n<system-reminder>помни про правила</system-reminder>"),
            entry("/board-task"),
            entry("https://github.com/dronrider/devkit"),
            entry(HUMAN),
        ])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["service"], 4)
        self.assertEqual([t for _, _, t in got], [HUMAN])

    def test_заготовки_стенда_и_запросы_хука_отсеиваются(self):
        # Заготовки obeycheck и вставки хука приходят ролью user и по длине
        # проходят все пороги: узнаются они только по началу строки.
        self.write("s1.jsonl", [
            entry("ЗАДАНИЕ: " + HUMAN),
            entry("ДИАЛОГ: " + HUMAN2),
            entry("ВОПРОС ПОЛЬЗОВАТЕЛЯ: " + HUMAN),
            entry("ОТВЕТ АССИСТЕНТА: " + HUMAN2),
            entry("[devkit-title] назови разговор тремя словами, вот его начало: "
                  + HUMAN),
            entry("Разговор завис на вопросе про доску, разбуди сессию и повтори "
                  "последний шаг, иначе очередь встанет и задача уедет в парковку."),
            entry(HUMAN),
        ])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["service"], 6)
        self.assertEqual([t for _, _, t in got], [HUMAN])

    def test_приписка_хука_режется_а_реплика_остаётся(self):
        # Приписку про план работ хук добавляет в хвост чужой реплики, и без
        # отреза её слова («веди», «массивом») лезут в верх словаря.
        tail = ("\n\nВеди план работ файлом ~/.devkit/plans/<ID сессии>.json: "
                "до первого шага положи туда список этапов задачи массивом "
                "объектов, по ходу помечай текущий пункт.")
        long_tail = ("\n\nДолгие дела (поиск по диску, сборка, установка) "
                     "гоняй фоном, чтобы ход не упирался в ожидание.")
        self.write("s1.jsonl", [entry(HUMAN + tail), entry(HUMAN2 + long_tail)])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["kept"], 2)
        self.assertEqual([t for _, _, t in got], [HUMAN, HUMAN2])
        self.assertEqual([w for w, _ in prose.dictionary(got, 1) if w == "массивом"], [])

    def test_перенос_черновика_ассистента_отсеивается(self):
        # Находка ревью DK-522. Ассистент в одном окне пишет промпт для
        # соседнего окна, человек переносит его copy-paste, и в журнале
        # второго окна текст лежит ролью user. Подпись у обеих записей одна,
        # а запись ассистента раньше на минуту.
        self.write("s1.jsonl", [assistant_entry(
            "Вот промт для агента в соседнем окне.\n\n" + HUMAN,
            "2026-08-11T16:08:33.000Z")])
        self.write("s2.jsonl", [
            entry(HUMAN, timestamp="2026-08-11T16:09:41.000Z"),
            entry(HUMAN2, timestamp="2026-08-11T16:20:00.000Z"),
        ])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["borrowed"], 1)
        self.assertEqual([t for _, _, t in got], [HUMAN2])

    def test_цитата_агента_позже_реплики_кандидата_не_режет(self):
        # Обратный случай той же пары. Агент цитирует реплику человека в
        # отчёте или в ревью. Отличает его только время, и без сверки времени
        # отсев убрал бы всё, что агенты когда-либо цитировали.
        self.write("s1.jsonl", [entry(HUMAN, timestamp="2026-08-11T16:09:41.000Z")])
        self.write("s2.jsonl", [assistant_entry(
            "Реплика пользователя, на которую я отвечаю:\n\n" + HUMAN,
            "2026-08-25T15:50:27.000Z")])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["borrowed"], 0)
        self.assertEqual([t for _, _, t in got], [HUMAN])

    def test_шаблонная_раздача_отсеивается(self):
        # Диспетчер собирает промпт по образцу и раздаёт его в новые окна,
        # человек только копирует. Словами ассистента такой текст в ленте не
        # звучит, и отпечаток переноса его не видит. Отличает шаблон повтор:
        # один текст в двух десятках журналов, меняется только ID задачи.
        шаблон = ("Проведи груминг %s. Вопросы задавай в этом же разговоре, "
                  "командой утилиты, и жди ответа в ней: вопросом заход не "
                  "кончай, а не дождавшись, отложи запись с причиной этой "
                  "задачи и её разбора.")
        for i, номер in enumerate(("DK-482", "DK-505", "DK-516"), 1):
            self.write("s%d.jsonl" % i, [entry(шаблон % номер)])
        self.write("s9.jsonl", [entry(HUMAN)])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["template"], 3)
        self.assertEqual([t for _, _, t in got], [HUMAN])

    def test_повтор_реплики_в_двух_журналах_шаблоном_не_считается(self):
        # Порог в три журнала стоит нарочно: одну и ту же мысль человек
        # переносит из окна в окно, и это ещё его текст.
        self.write("s1.jsonl", [entry(HUMAN)])
        self.write("s2.jsonl", [entry(HUMAN)])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["template"], 0)
        self.assertEqual([t for _, _, t in got], [HUMAN])

    def test_чужая_типографика_помечается_а_не_режется(self):
        # У человека нет ни длинного тире, ни лапок, ни многоточия одним
        # символом: правила запрещают, и клавиатурная привычка тоже. Модель
        # ставит их сама, и знак внутри реплики роли user это след вставки.
        # Отсеивать нельзя, знак мог приехать из скопированного пути, поэтому
        # кандидат помечается и решает человек.
        вставка = (HUMAN2 + " Практики \u2014 это INVEST, \u201cExample "
                   "Mapping\u201d и \u2026 дальше по списку.")
        self.write("s1.jsonl", [entry(HUMAN), entry(вставка)])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["kept"], 2)
        self.assertEqual(stat["typo"], 1)
        self.assertEqual(prose.typo_marks(HUMAN), [])
        self.assertEqual(prose.typo_marks(вставка),
                         ["длинное тире", "лапки", "многоточие одним символом"])

    def test_пометка_типографики_видна_в_выгрузке(self):
        # Пометку читает человек, и стоять она должна рядом с текстом.
        self.write("s1.jsonl", [entry(HUMAN + " Дальше \u2192 по списку.")])
        out_dir = os.path.join(self.root, "dump")
        candidates, _ = prose.collect(self.root, 25)
        prose.write_dump(out_dir, candidates, [])
        with open(os.path.join(out_dir, "replies.md"), encoding="utf-8") as f:
            выгрузка = f.read()
        self.assertIn("типографика: стрелка", выгрузка)
        # В тело кандидата пометка не едет, иначе она уходит в корпус вместе
        # с текстом.
        прочитано = prose.read_dump(os.path.join(out_dir, "replies.md"))
        self.assertEqual(len(прочитано), 1)
        self.assertTrue(прочитано[0][3].startswith("Смотри, тут вот какая штука"))
        self.assertNotIn("типографика", прочитано[0][3])

    def test_ответы_инструментов_и_мета_не_реплики(self):
        self.write("s1.jsonl", [
            tool_result_entry(),
            entry(HUMAN, isMeta=True),
            entry(HUMAN, kind="assistant"),
            entry(HUMAN),
        ])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["replies"], 1)
        self.assertEqual(len(got), 1)

    def test_короткие_и_нерусские_отсеиваются(self):
        self.write("s1.jsonl", [
            entry("да, давай"),
            entry("go test ./... GOWORK=off ok devkit/internal/frame 0.412s cached "
                  "no test files ok devkit/tools/taskctl 1.201s ok devkit/tools/shipctl"),
            entry(HUMAN),
        ])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["short"], 2)
        self.assertEqual(len(got), 1)

    def test_повтор_реплики_едет_один_раз(self):
        self.write("s1.jsonl", [entry(HUMAN)])
        self.write("s2.jsonl", [entry(HUMAN)])
        got, stat = prose.collect(self.root, 25)
        self.assertEqual(stat["journals"], 2)
        self.assertEqual(len(got), 1)

    def test_журналы_субагентов_не_берутся(self):
        # Ролью user в журнале субагента записан промпт диспетчера: это опять
        # текст агента, и в эталоне ему делать нечего.
        sub = os.path.join(self.project, "s1", "subagents")
        os.makedirs(sub)
        with open(os.path.join(sub, "agent-1.jsonl"), "w", encoding="utf-8") as f:
            f.write(json.dumps(entry(HUMAN2), ensure_ascii=False) + "\n")
        self.write("s1.jsonl", [entry(HUMAN)])
        got, _ = prose.collect(self.root, 25)
        self.assertEqual([t for _, _, t in got], [HUMAN])

    def test_битая_строка_журнала_не_валит_сборку(self):
        path = self.write("s1.jsonl", [entry(HUMAN)])
        with open(path, "a", encoding="utf-8") as f:
            f.write("{не json\n\n")
            f.write(json.dumps(entry(HUMAN2), ensure_ascii=False) + "\n")
        got, _ = prose.collect(self.root, 25)
        self.assertEqual(len(got), 2)

    def test_команда_в_обратных_кавычках_остаётся(self):
        # Раньше инлайн-код менялся на слово CODE, и метка оседала прямо в теле
        # кандидата («вопросы задавай командой CODE»). Имя команды это часть
        # речи человека, и фрагмент приходилось чинить руками по журналу.
        text = HUMAN + " Зови `taskctl ask DK-1 --question \"...\"` и жди ответа."
        self.write("s1.jsonl", [entry(text)])
        got, _ = prose.collect(self.root, 25)
        self.assertIn("`taskctl ask", got[0][2])
        self.assertNotIn("CODE", got[0][2])

    def test_заголовок_внутри_реплики_не_режет_кандидата(self):
        # Развёрнутая постановка человека идёт со своими заголовками markdown.
        # Выгрузка размечена такими же, и по одному «## N» она резалась посреди
        # текста: кандидат распадался надвое, а у второго куска пропадало
        # начало. Заголовок кандидата опознаётся вместе с именем журнала.
        self.write("s1.jsonl", [entry(С_ЗАГОЛОВКАМИ), entry(HUMAN2)])
        out_dir = os.path.join(self.root, "dump")
        candidates, _ = prose.collect(self.root, 25)
        prose.write_dump(out_dir, candidates, [])
        got = prose.read_dump(os.path.join(out_dir, "replies.md"))
        self.assertEqual(len(got), 2)
        self.assertEqual([n for n, _, _, _ in got], [1, 2])
        self.assertTrue(got[0][3].startswith("Оставляю свои замечания"))
        self.assertIn("## 2. Скорость", got[0][3])
        self.assertTrue(got[1][3].startswith("Не годится так."))

    def test_выгрузка_помнит_журнал_и_дату(self):
        self.write("s1.jsonl", [entry(HUMAN)])
        out_dir = os.path.join(self.root, "dump")
        candidates, _ = prose.collect(self.root, 25)
        prose.write_dump(out_dir, candidates, [])
        got = prose.read_dump(os.path.join(out_dir, "replies.md"))
        self.assertEqual(got[0][1], "s1.jsonl")
        self.assertEqual(got[0][2], "2026-08-20")

    def test_ограда_кода_режется_а_проза_вокруг_остаётся(self):
        text = HUMAN + "\n\n```\ngit -C /tmp log --oneline\n```\n\n" + HUMAN2
        self.write("s1.jsonl", [entry(text)])
        got, _ = prose.collect(self.root, 25)
        self.assertNotIn("git -C", got[0][2])
        self.assertIn("Смотри, тут вот какая штука", got[0][2])
        self.assertIn("Перегони regcheck", got[0][2])


class TestDictionary(JournalCase):
    def test_словарь_считает_реплики_а_не_вхождения(self):
        long_one = " ".join(["поезд"] * 40) + " " + HUMAN
        candidates = [("s1.jsonl", "2026-08-20", long_one),
                      ("s1.jsonl", "2026-08-20", HUMAN),
                      ("s1.jsonl", "2026-08-20", HUMAN2)]
        got = dict(prose.dictionary(candidates, 1))
        self.assertEqual(got["поезд"], 1)
        self.assertEqual(got["доску"], 2)

    def test_общие_слова_и_короткие_не_едут(self):
        candidates = [("s1.jsonl", "", "это уже тоже просто надо когда чтобы " + HUMAN)]
        got = dict(prose.dictionary(candidates, 1))
        for word in ("это", "уже", "тоже", "просто", "надо", "чтобы"):
            self.assertNotIn(word, got)
        self.assertIn("доску", got)

    def test_отсечка_по_числу_реплик(self):
        candidates = [("s1.jsonl", "", HUMAN), ("s1.jsonl", "", HUMAN2)]
        self.assertNotIn("доску", dict(prose.dictionary(candidates, 2)))
        self.assertIn("доску", dict(prose.dictionary(candidates, 1)))


class CorpusCase(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="prose-corpus-")
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)

    def genre(self, name, title, bodies, пометка=""):
        path = os.path.join(self.dir, name + ".md")
        mark = "пометка: %s\n" % пометка if пометка else ""
        with open(path, "w", encoding="utf-8") as f:
            f.write("# %s\n\nВводная проза жанра, во фрагменты не едет.\n\n" % title)
            for i, body in enumerate(bodies, 1):
                f.write("## %d\nисточник: трекер, issues/%d\nроль: репортёр\n%s\n%s\n\n"
                        % (i, i, mark, body))
        return path


class TestParse(CorpusCase):
    def test_читает_имя_жанра_шапку_и_тело(self):
        self.genre("task", "постановка", ["Первый абзац.\n\nВторой абзац."])
        title, fragments = prose.parse_genre(os.path.join(self.dir, "task.md"))
        self.assertEqual(title, "постановка")
        self.assertEqual(len(fragments), 1)
        self.assertEqual(fragments[0]["источник"], "трекер, issues/1")
        self.assertEqual(fragments[0]["роль"], "репортёр")
        self.assertEqual(fragments[0]["body"], "Первый абзац.\n\nВторой абзац.")

    def test_вводная_проза_фрагментом_не_считается(self):
        self.genre("task", "постановка", ["Тело."])
        _, fragments = prose.parse_genre(os.path.join(self.dir, "task.md"))
        self.assertEqual(len(fragments), 1)
        self.assertNotIn("Вводная проза", fragments[0]["body"])

    def test_двоеточие_в_теле_не_уезжает_в_шапку(self):
        self.genre("task", "постановка", ["Вот что вышло: доска встала.\n\nДальше."])
        _, fragments = prose.parse_genre(os.path.join(self.dir, "task.md"))
        self.assertTrue(fragments[0]["body"].startswith("Вот что вышло: доска встала."))
        self.assertNotIn("Вот что вышло", fragments[0])


class TestSample(CorpusCase):
    def setUp(self):
        super().setUp()
        self.genre("task", "постановка", ["Постановка %d." % i for i in range(1, 7)])
        self.genre("lld", "решение LLD", ["Решение %d." % i for i in range(1, 7)])

    def test_жанр_держится(self):
        picked = prose.sample(self.dir, "lld", 3, seed=1)
        self.assertEqual(len(picked), 3)
        self.assertEqual({g for g, _, _ in picked}, {"lld"})
        for _, _, fragment in picked:
            self.assertTrue(fragment["body"].startswith("Решение"))

    def test_без_жанра_берётся_из_всех(self):
        picked = prose.sample(self.dir, "", 12, seed=1)
        self.assertEqual(len(picked), 12)
        self.assertEqual({g for g, _, _ in picked}, {"task", "lld"})

    def test_два_захода_дают_разные_наборы(self):
        first = [f["body"] for _, _, f in prose.sample(self.dir, "", 3, seed=1)]
        second = [f["body"] for _, _, f in prose.sample(self.dir, "", 3, seed=2)]
        self.assertNotEqual(first, second)

    def test_один_и_тот_же_seed_повторяет_набор(self):
        first = [f["body"] for _, _, f in prose.sample(self.dir, "", 3, seed=7)]
        second = [f["body"] for _, _, f in prose.sample(self.dir, "", 3, seed=7)]
        self.assertEqual(first, second)

    def test_запрос_больше_корпуса_отдаёт_весь_корпус(self):
        picked = prose.sample(self.dir, "task", 99, seed=1)
        self.assertEqual(len(picked), 6)

    def test_неизвестный_жанр_пустой(self):
        self.assertEqual(prose.sample(self.dir, "readme", 3, seed=1), [])

    def test_вывод_называет_жанр_и_источник(self):
        text = prose.render(prose.sample(self.dir, "task", 2, seed=3))
        self.assertIn("## постановка (task)", text)
        self.assertIn("источник: трекер", text)

    def test_пометка_едет_вместе_с_фрагментом(self):
        """Оговорка вычитки адресована тому, кто читает выборку перед письмом.

        В корпусе резкие постановки помечены словами «резкость оценки, лексику
        не копировать». Потеряй render() это поле, и фрагмент приедет в
        контекст письма образцом целиком, вместе с бранью."""
        self.genre("readme", "вход README", ["Вход %d." % i for i in range(1, 4)],
                   пометка="резкость оценки, лексику не копировать")
        text = prose.render(prose.sample(self.dir, "readme", 3, seed=1))
        self.assertEqual(text.count("пометка: резкость оценки, лексику не копировать"), 3)

    def test_фрагмент_без_пометки_её_не_печатает(self):
        text = prose.render(prose.sample(self.dir, "task", 2, seed=3))
        self.assertNotIn("пометка:", text)


class TestБюджетВыборки(CorpusCase):
    """Фрагменты в корпусе разной длины, и четыре длинных подряд кладут в
    контекст письма простыню. Набор держит бюджет слов."""

    def setUp(self):
        super().setUp()
        длинный = " ".join(["слово"] * 300)
        короткие = [" ".join(["слово"] * 40) for _ in range(6)]
        self.genre("task", "постановка", [длинный] + короткие)

    def размеры(self, picked):
        return [len(prose.words(f["body"])) for _, _, f in picked]

    def test_длинный_фрагмент_вытесняет_короткие(self):
        for seed in range(10):
            picked = prose.sample(self.dir, "task", 4, seed, budget=100)
            слов = self.размеры(picked)
            if 300 in слов:
                self.assertEqual(len(picked), 1, seed)
            else:
                self.assertLessEqual(sum(слов), 100, seed)

    def test_короткие_добираются_до_потолка_числа(self):
        picked = prose.sample(self.dir, "task", 2, seed=3, budget=10000)
        self.assertEqual(len(picked), 2)

    def test_первый_фрагмент_едет_даже_длиннее_бюджета(self):
        for seed in range(5):
            picked = prose.sample(self.dir, "task", 4, seed, budget=10)
            self.assertEqual(len(picked), 1, seed)


class TestСитоРепозитория(unittest.TestCase):
    """Агент пишет текст в файл репозитория, человек копирует его оттуда в чат,
    и в журнале текст лежит ролью user. Дерево тут синтетическое: живой
    репозиторий меняется каждый коммит, и тест на нём недетерминирован."""

    СКИЛЛ = ("Шаг скилла. Вопросы задавай в этом же разговоре командой утилиты "
             "и жди ответа в ней, вопросом заход не кончай, а не дождавшись "
             "отложи запись с причиной.")

    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="prose-repo-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.write("kit/skills/board-groom/SKILL.md", self.СКИЛЛ)
        self.write("kit/skills/prose/corpus/task.md",
                   "Совсем другой текст корпуса про доску задач и её строки.")
        self.write("kit/skills/prose/dictionary.md",
                   "Словарь пользователя, собранный из тех же реплик корпуса.")

    def write(self, rel, text):
        path = os.path.join(self.root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_дословный_кусок_в_семь_слов_находится(self):
        index = prose.repo_index(self.root)
        hits = prose.borrowed_from_repo(
            index, "Вопросы задавай в этом же разговоре командой утилиты и жди "
                   "ответа в ней.")
        self.assertEqual(len(hits), 1)
        self.assertEqual(hits[0][1], os.path.join("kit", "skills", "board-groom",
                                                  "SKILL.md"))
        self.assertIn("вопросы задавай в этом же разговоре", hits[0][0])

    def test_совпадение_короче_порога_не_ловится(self):
        # Шесть общих слов это общее место, а не перенос.
        index = prose.repo_index(self.root)
        self.assertEqual(
            prose.borrowed_from_repo(index, "Вопросы задавай в этом же разговоре "
                                            "почтой."), [])

    def test_скилл_prose_в_сито_не_попадает(self):
        # Корпус сам состоит из проверяемых фрагментов, и без исключения каждый
        # совпал бы с собой. Словарь рядом собран из тех же реплик и давал
        # находку на каждую вторую, поэтому скилл исключён целиком.
        index = prose.repo_index(self.root)
        self.assertEqual(
            prose.borrowed_from_repo(index, "Совсем другой текст корпуса про "
                                            "доску задач и её строки."), [])
        self.assertEqual(
            prose.borrowed_from_repo(index, "Словарь пользователя, собранный из "
                                            "тех же реплик корпуса."), [])

    def test_фраза_разворачивается_обратно_в_текст_файла(self):
        # Отпечаток нормализован, а `git log -S` ищет литерал.
        chain = tuple("вопросы задавай в этом же разговоре командой".split())
        self.assertEqual(prose.raw_phrase(self.СКИЛЛ, chain),
                         "Вопросы задавай в этом же разговоре командой")

    def test_цитата_человека_в_файле_различается_датой(self):
        self.assertIs(prose.taken_from_repo("2026-08-17", "2026-08-19"), True)
        self.assertIs(prose.taken_from_repo("2026-08-19", "2026-08-17"), False)
        self.assertIsNone(prose.taken_from_repo("2026-08-19", "2026-08-19"))
        self.assertIsNone(prose.taken_from_repo("", "2026-08-19"))


class TestДатаФразы(unittest.TestCase):
    """Дата файла для вердикта груба: файл цели заведён раньше реплики, а абзац
    с ответами человека дописан в него позже. Дату берём у самой фразы."""

    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="prose-git-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.git("init", "-q")

    def git(self, *args, date=None):
        env = dict(os.environ)
        env.update({"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
                    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"})
        if date:
            env["GIT_AUTHOR_DATE"] = date
            env["GIT_COMMITTER_DATE"] = date
        subprocess.run(["git", "-C", self.root, "-c", "commit.gpgsign=false"]
                       + list(args), check=True, capture_output=True, env=env)

    def commit(self, text, date):
        with open(os.path.join(self.root, "цель.md"), "w", encoding="utf-8") as f:
            f.write(text)
        self.git("add", "цель.md")
        self.git("commit", "-qm", "правка", date=date)

    def test_дата_берётся_у_фразы_а_не_у_файла(self):
        self.commit("Заголовок цели и первый абзац разбора.\n", "2026-08-17T10:00:00")
        self.commit("Заголовок цели и первый абзац разбора.\n"
                    "Ответы пользователя: окно чатов открывается по клику на значок.\n",
                    "2026-08-19T10:00:00")
        self.assertEqual(prose.file_dates(self.root, "цель.md")[0], "2026-08-17")
        self.assertEqual(
            prose.phrase_date(self.root, "цель.md",
                              "окно чатов открывается по клику на значок"),
            "2026-08-19")

    def test_фразы_нет_в_истории_даты_нет(self):
        self.commit("Заголовок цели.\n", "2026-08-17T10:00:00")
        self.assertEqual(prose.phrase_date(self.root, "цель.md", "чего тут нет"), "")


class TestCli(JournalCase):
    def run_main(self, argv):
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = prose.main(argv)
        return code, out.getvalue(), err.getvalue()

    def test_collect_печатает_числа_и_кладёт_выгрузку(self):
        self.write("s1.jsonl", [entry(HUMAN), entry(HUMAN2), entry("да")])
        out_dir = os.path.join(self.root, "dump")
        code, out, _ = self.run_main([
            "collect", "--journals", self.root, "--min-hits", "1", "--out", out_dir])
        self.assertEqual(code, 0)
        self.assertIn("кандидатов: 2", out)
        self.assertIn("реплик роли user: 3", out)
        with open(os.path.join(out_dir, "replies.md"), encoding="utf-8") as f:
            self.assertIn("Смотри, тут вот какая штука", f.read())
        with open(os.path.join(out_dir, "dictionary.md"), encoding="utf-8") as f:
            self.assertIn("- доску", f.read())

    def test_collect_без_журналов_отдаёт_единицу(self):
        code, _, err = self.run_main(["collect", "--journals", os.path.join(self.root, "нет")])
        self.assertEqual(code, 1)
        self.assertIn("журналов не нашлось", err)

    def test_sample_печатает_фрагменты(self):
        corpus = os.path.join(self.root, "corpus")
        os.makedirs(corpus)
        with open(os.path.join(corpus, "task.md"), "w", encoding="utf-8") as f:
            f.write("# постановка\n\n## 1\nисточник: журнал\n\nТело фрагмента.\n")
        code, out, _ = self.run_main(["sample", "--corpus", corpus, "--count", "1", "--seed", "1"])
        self.assertEqual(code, 0)
        self.assertIn("Тело фрагмента.", out)

    def test_sample_на_пустом_корпусе_отдаёт_единицу(self):
        code, _, err = self.run_main(["sample", "--corpus", os.path.join(self.root, "нет")])
        self.assertEqual(code, 1)
        self.assertIn("нечего показать", err)


# Синтетика пятого сита. Первый текст держит все три приметы разом: двоеточие
# в середине фразы, довод «иначе» и хвост «а не». Второй везёт одну полную
# форму «не X, а Y» и больше ничего: это лексика пользователя, и ронять его
# сито не должно (`task` #21 живого корпуса). Третий чист.
ТРИ_ПРИМЕТЫ = ("Правило тут одно: доску правит утилита. "
               "В редактор ходить не надо, иначе строка разъедется. "
               "Правим строку, а не файл.")
ПОЛНАЯ_ФОРМА = ("Активацию должно запретить, а не заблокировать получение "
                "ресурсов. Пользователь открывает форму и видит список задач. "
                "Список берётся из памяти браузера. Ширина колонки считается "
                "по последней раскладке. Кнопка стоит справа сверху.")
ЧИСТЫЙ = ("Пользователь открывает форму и видит список задач. "
          "Список берётся из памяти браузера. "
          "Ширина колонки считается по последней раскладке.")


class TestСитоПрозы(CorpusCase):
    """Пятое сито: эталон человеческой прозы не может быть тем, что наш
    сторож прозы заворачивает как агентское. Четыре сита происхождения на
    этом тексте молчали, человек ловил его глазами трижды подряд (DK-522)."""

    def test_сторож_прочёлся(self):
        # Без хука сито молчит, и тогда все проверки ниже проходят пустыми.
        module, limits = prose.guard()
        self.assertIsNotNone(module, "hooks/check-prose.py не прочёлся")
        self.assertEqual(sorted(limits), sorted(prose.GUARD_METRICS))

    def test_три_приметы_помечаются(self):
        приметы = prose.agent_marks(ТРИ_ПРИМЕТЫ)
        self.assertEqual(len(приметы), 3, приметы)
        self.assertTrue(prose.agent_prose(ТРИ_ПРИМЕТЫ))

    def test_полная_форма_не_х_а_у_не_помечается(self):
        # Замер цели DK-446 относит полную форму к лексике пользователя: у
        # людей её 2,3 на тысячу слов против 1,5 у агентов. Одной приметы для
        # пометки мало ровно поэтому.
        приметы = prose.agent_marks(ПОЛНАЯ_ФОРМА)
        self.assertEqual([имя for имя, _, _ in приметы],
                         ["хвост «..., а не Y»"], приметы)
        self.assertFalse(prose.agent_prose(ПОЛНАЯ_ФОРМА))

    def test_чистый_текст_молчит(self):
        self.assertEqual(prose.agent_marks(ЧИСТЫЙ), [])
        self.assertFalse(prose.agent_prose(ЧИСТЫЙ))

    def test_prosecheck_печатает_помеченный_фрагмент(self):
        self.genre("task", "постановка", [ЧИСТЫЙ, ТРИ_ПРИМЕТЫ, ПОЛНАЯ_ФОРМА])
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = prose.main(["prosecheck", "--corpus", self.dir])
        text = out.getvalue()
        self.assertEqual(code, 1, text)
        self.assertIn("task #2", text)
        self.assertNotIn("task #1", text)
        self.assertNotIn("task #3", text)
        self.assertIn("проверено: 3, помечено: 1", text)

    def test_collect_считает_помеченных_и_пишет_строку_выгрузки(self):
        root = tempfile.mkdtemp(prefix="prose-guard-")
        self.addCleanup(shutil.rmtree, root, ignore_errors=True)
        project = os.path.join(root, "-Users-x-projects-devkit")
        os.makedirs(project)
        with open(os.path.join(project, "s1.jsonl"), "w", encoding="utf-8") as f:
            for текст in (ТРИ_ПРИМЕТЫ, ПОЛНАЯ_ФОРМА):
                f.write(json.dumps(entry(текст), ensure_ascii=False) + "\n")
        out_dir = os.path.join(root, "dump")
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = prose.main(["collect", "--journals", root, "--min-words", "5",
                               "--min-hits", "1", "--out", out_dir])
        self.assertEqual(code, 0, err.getvalue())
        self.assertIn("с агентской прозой, помечено: 1", out.getvalue())
        with open(os.path.join(out_dir, "replies.md"), encoding="utf-8") as f:
            выгрузка = f.read()
        self.assertIn("проза: довод в той же фразе", выгрузка)
        self.assertEqual(выгрузка.count("проза: "), 1)


class TestКорпусРепозитория(unittest.TestCase):
    """Корпус, который едет с devkit: у каждого жанра свой файл и не меньше
    трёх фрагментов, иначе выборка скилла письма выродится в один и тот же
    набор."""

    def test_четыре_жанра_по_три_фрагмента(self):
        corpus = prose.read_corpus(os.path.join(prose.HERE, "corpus"))
        self.assertEqual(sorted(corpus), ["lld", "readme", "skill", "task"])
        for genre, (title, fragments) in corpus.items():
            self.assertTrue(title, genre)
            self.assertGreaterEqual(len(fragments), 3, genre)
            for fragment in fragments:
                self.assertIn("источник", fragment)

    def test_источник_фрагмента_одного_из_четырёх_видов(self):
        # Источников у корпуса четыре. Два названы в цели DK-446, это реплики
        # пользователя из журналов сессий и issue русскоязычных трекеров. Два
        # назвал пользователь на приёмке DK-522: входные страницы чужих
        # проектов (жанру `readme` трекеры материала не дают) и первая версия
        # файла задачи DK-459, написанная им целиком.
        # Тексты devkit, написанные агентами, сюда попадали, и приёмка их
        # отбраковала: по сторожу прозы девять таких абзацев давали 30% довода
        # в той же фразе и 4,9 хвоста «а не» на 1000 слов, то есть агентскую
        # колонку замера. Файл задачи это исключение, проверенное поиском по
        # 418 первым версиям задач и 223 первым версиям черновиков. Без этой
        # проверки следующая правка вернёт агентские абзацы молча.
        виды = ("журнал сессии", "трекер", "страница проекта", "файл задачи")
        corpus = prose.read_corpus(os.path.join(prose.HERE, "corpus"))
        for genre, (_, fragments) in corpus.items():
            for fragment in fragments:
                источник = fragment["источник"]
                self.assertTrue(
                    any(источник.startswith(вид) for вид in виды),
                    "%s: %s" % (genre, источник))

    def test_переписанный_фрагмент_выведен_из_под_сверки(self):
        # Вердикт приёмки DK-522: манера цитат нужна, а предметность чужих
        # продуктов нет. Цитаты трекеров и страниц переписаны, и отпечаток
        # первых восьми слов на таком тексте происхождения уже не покажет.
        # Поле «обезличен» говорит это прямо. Реплике из журнала оно не
        # положено: она стоит дословно, и сверка сборщика по ней работает.
        corpus = prose.read_corpus(os.path.join(prose.HERE, "corpus"))
        for genre, (_, fragments) in corpus.items():
            for i, fragment in enumerate(fragments, 1):
                где = "%s #%d" % (genre, i)
                метка = fragment.get("обезличен")
                источник = fragment["источник"]
                if источник.startswith("страница проекта"):
                    self.assertEqual(метка, "да", где)
                    continue
                if метка is None:
                    continue
                self.assertEqual(метка, "да", где)
                self.assertFalse(источник.startswith("журнал сессии"), где)

    def test_имена_чужих_продуктов_не_вернулись(self):
        # Список это имена, что стояли в корпусе до правки. Агент читает
        # эталон прямо перед письмом и тащит из него слова вместе с манерой,
        # поэтому в наших текстах заводились Ванесса и EDT. Первая половина
        # списка снята с цитат трекеров, вторая с реплик пользователя, где
        # стояли чужой трекер, чужой набор скиллов и имена моделей. Правка
        # тут одна, а вернуть имя назад может любая следующая.
        имена = (
            r"Ванесс", r"VanessaExt", r"\bEDT\b", r"ИнтернетПочта",
            r"TestClient", r"OneScript", r"winow", r"Neochat", r"Matrix",
            r"libquotient", r"nginx", r"Django", r"Swagger", r"Telegram",
            r"T-Invest", r"Т-Инвестиции", r"\b1С\b", r"\bВК\b",
            r"воркспейс",
            r"\bJira\b", r"brainstorm", r"writing-plans", r"\bOpus\b",
            r"\bGLM\b", r"\bsonnet\b", r"\bFable\b", r"\bvscode\b",
            r"\bdevkit\b",
        )
        # Имя devkit ловится только в теле: корпус едет вместе с devkit в чужие
        # проекты, и эталон с его именем читается там как чужой. В шапке
        # фрагмента оно законно, поле «источник» везёт ссылку и коммит.
        # ID задачи тянет за собой чужой проект и устаревает вместе с ним. В
        # репликах пользователя он заменён словом «задача» или снят вовсе.
        # Регистр тут важен, иначе под шаблон уедут utf-8 и sha-1.
        ид = r"\b[A-Z]{2,4}-\d+\b"
        # Имя файла или страницы это адрес нашего дерева. В чужом проекте
        # такого файла нет, а эталон учит агента поминать его в тексте, и
        # взамен в корпусе стоит описание: «файл доски», «страница про форму
        # задачи». Имена утилит под шаблон не идут, они без расширения и едут
        # вместе с корпусом. Поля шапки шаблон не смотрит, там `источник`
        # везёт ссылку на страницу первоисточника вместе с её именем.
        файл = (r"\b[\w.-]*\w\.(?:md|py|go|toml|sh|json|jsonl|yaml|yml"
                r"|cfg|ini|txt)\b")
        corpus = prose.read_corpus(os.path.join(prose.HERE, "corpus"))
        for genre, (_, fragments) in corpus.items():
            for i, fragment in enumerate(fragments, 1):
                где = "%s #%d" % (genre, i)
                for имя in имена:
                    self.assertIsNone(
                        re.search(имя, fragment["body"], re.I),
                        "%s: %s" % (где, имя))
                self.assertIsNone(re.search(ид, fragment["body"]), где)
                self.assertIsNone(re.search(файл, fragment["body"]), где)

    def test_один_текст_не_стоит_в_двух_жанрах(self):
        # Одна и та же реплика стояла и в `task`, и в `readme` (находка
        # второго круга DK-522). Выборка из четырёх жанров показала бы её
        # дважды, а жанров в корпусе стало бы фактически три с половиной.
        corpus = prose.read_corpus(os.path.join(prose.HERE, "corpus"))
        места = {}
        for genre, (_, fragments) in corpus.items():
            for i, fragment in enumerate(fragments, 1):
                места.setdefault(prose.signature(fragment["body"]), []).append(
                    "%s #%d" % (genre, i))
        повторы = {k: v for k, v in места.items() if len(v) > 1}
        self.assertEqual(повторы, {})

    def test_два_запуска_без_seed_дают_разные_наборы(self):
        # Так скилл письма и зовут, без --seed. Одинаковая выборка на каждом
        # заходе сделала бы тексты однородными, а seed в тестах эту проверку
        # обходит стороной. Фрагментов в корпусе 66 (lld 18, readme 15, skill 9,
        # task 24), и набор режется бюджетом слов, поэтому сравниваются наборы,
        # а не их длина: совпадение двух подряд взятых маловероятно, и повтор
        # прогона ловит вырождение выборки.
        corpus = os.path.join(prose.HERE, "corpus")
        first = [f["body"] for _, _, f in prose.sample(corpus, "", 12, seed=None)]
        second = [f["body"] for _, _, f in prose.sample(corpus, "", 12, seed=None)]
        self.assertNotEqual(first, second)

    def test_выборка_по_живому_корпусу_разная(self):
        corpus = os.path.join(prose.HERE, "corpus")
        first = [f["body"] for _, _, f in prose.sample(corpus, "", 4, seed=1)]
        second = [f["body"] for _, _, f in prose.sample(corpus, "", 4, seed=2)]
        self.assertNotEqual(first, second)


if __name__ == "__main__":
    unittest.main()
