#!/usr/bin/env python3
"""Тесты сборщика и выборки корпуса прозы.

Журнал сессии здесь синтетический: живые журналы машины лежат в домашнем
каталоге, меняются каждый день и содержат личные тексты, поэтому тест на них
и недетерминирован, и небезопасен."""
import io
import json
import os
import shutil
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

    def genre(self, name, title, bodies):
        path = os.path.join(self.dir, name + ".md")
        with open(path, "w", encoding="utf-8") as f:
            f.write("# %s\n\nВводная проза жанра, во фрагменты не едет.\n\n" % title)
            for i, body in enumerate(bodies, 1):
                f.write("## %d\nисточник: трекер, issues/%d\nроль: репортёр\n\n%s\n\n"
                        % (i, i, body))
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

    def test_фрагменты_только_из_журналов_и_трекеров(self):
        # Источников у корпуса два, и оба названы в цели DK-446: реплики
        # пользователя из журналов сессий и issue русскоязычных трекеров.
        # Тексты самого devkit сюда попадали, и приёмка DK-522 их отбраковала:
        # по сторожу прозы девять таких абзацев давали 30% довода в той же
        # фразе и 4,9 хвоста «а не» на 1000 слов, то есть агентскую колонку
        # замера. Без этой проверки следующая правка вернёт их молча.
        corpus = prose.read_corpus(os.path.join(prose.HERE, "corpus"))
        for genre, (_, fragments) in corpus.items():
            for fragment in fragments:
                источник = fragment["источник"]
                self.assertTrue(
                    источник.startswith("журнал сессии")
                    or источник.startswith("трекер"),
                    "%s: %s" % (genre, источник))

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
        # обходит стороной. Двенадцать фрагментов из 32 (lld 9, readme 5,
        # skill 6, task 12): совпадение двух подряд взятых наборов
        # маловероятно, и повтор прогона ловит вырождение выборки.
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
