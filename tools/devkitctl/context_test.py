#!/usr/bin/env python3
"""Разбор журналов сессий: формы служебной реплики, разбор потока и
stats --context на фикстуре с известной подписью перезаписи префикса.
Журналы харнес кладёт по слепку пути проекта, поэтому фикстура
раскладывается под подставным HOME в директорию слепка временного проекта.
"""
import json
import re
import shutil
import unittest
from pathlib import Path

from testenv import HERE, SandboxCase, context, git_init, write

LOG = ("2026-07-29T01:02:41\tshipctl\tmerge\t0\n"
       "2026-07-29T01:02:40\ttaskctl\tmove\t0\n"
       "2026-07-29T01:02:40\ttaskctl\tmove\t1\n")


def assistant_raw(read, ts):
    """Сырая запись ответа с usage для журнала потока."""
    return {
        "type": "assistant",
        "requestId": "req_%s" % ts,
        "message": {
            "model": "m1",
            "usage": {
                "cache_creation_input_tokens": 0,
                "cache_read_input_tokens": read,
                "input_tokens": 20,
                "output_tokens": 5,
            },
        },
        "timestamp": ts,
        "uuid": "u_%s" % ts,
    }


def user_rec(content, ts):
    """Сырая запись реплики человека для журнала потока."""
    return {
        "type": "user",
        "message": {"role": "user", "content": content},
        "timestamp": ts,
        "uuid": "up_%s" % ts,
    }


class ServiceReplyTest(unittest.TestCase):
    """Формы служебной реплики и разбор потока вокруг неё."""

    def test_service_reply_forms(self):
        """Формы служебной реплики те же, что узнаёт лента дашборда: дословный
        текст и старая рамка без слов человека. Цитата в словах человека
        продлением не считается, иначе разговор о пинге выпадал из разбора."""
        prompt = context.CACHEKEEP_PROMPT
        self.assertTrue(context.is_service_reply(prompt),
                        "дословный текст реплики служебный")
        self.assertTrue(context.is_service_reply("  %s\n" % prompt),
                        "обрамление пробелами не прячет дословный текст")
        frame = ("<devkit-cachekeep>Служебное продление кеша сессии."
                 " Молчи.</devkit-cachekeep>")
        self.assertTrue(context.is_service_reply(frame),
                        "старая рамка без слов человека служебная")
        quoted = "как быть с «%s» в чате?" % prompt
        self.assertFalse(context.is_service_reply(quoted),
                         "цитата в словах человека остаётся разговором")
        self.assertFalse(context.is_service_reply(frame + " и вопрос"),
                         "рамка со словами человека остаётся разговором")
        self.assertFalse(context.is_service_reply("обычная реплика"),
                         "обычная реплика не служебная")
        self.assertFalse(context.is_service_reply(None),
                         "отсутствие текста не служебная реплика")

    def test_empty_user_between_pair_keeps_service_drop(self):
        """Пустая реплика между служебным ходом и его ответом пару не сбивает:
        незначимая строка content не перекрывает last_user, и real_requests
        выкидывает служебный ход (запись одна вместо двух)."""
        recs = [
            user_rec("обычный вопрос", "2026-10-12T09:59:00.000Z"),
            assistant_raw(190000, "2026-10-12T09:59:01.000Z"),
            user_rec(context.CACHEKEEP_PROMPT, "2026-10-12T10:00:00.000Z"),
            user_rec("", "2026-10-12T10:00:01.000Z"),
            assistant_raw(199500, "2026-10-12T10:00:02.000Z"),
        ]
        path = Path(HERE / "testdata" / "ctx-empty-user.jsonl")
        try:
            with open(path, "w", encoding="utf-8") as f:
                for r in recs:
                    f.write(json.dumps(r, ensure_ascii=False) + "\n")
            reqs = context.real_requests(path)
            self.assertEqual(len(reqs), 1,
                             "служебный ход с пустой репликой между пары должен выпасть")
        finally:
            if path.exists():
                path.unlink()


class ContextStatsTest(SandboxCase):

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = Path(cls.box.root / "cproj")
        cls.proj.mkdir()
        slug = context.slug(cls.proj.resolve())
        cls.slug = slug
        logs = cls.box.home / ".claude" / "projects" / slug
        (logs / "aaaabbbb" / "subagents").mkdir(parents=True)
        shutil.copy(str(HERE / "testdata" / "session.jsonl"), str(logs / "aaaabbbb.jsonl"))
        shutil.copy(str(HERE / "testdata" / "session-subagent.jsonl"),
                    str(logs / "aaaabbbb" / "subagents" / "agent-cccc1111.jsonl"))
        cls.rc, cls.out = cls.box.dkctl_run("stats", "--context", "-C", str(cls.proj))

    def matches(self, pattern, why):
        self.assertTrue(re.search(pattern, self.out), "%s: %s" % (why, self.out))

    def test_slug_and_exit_code(self):
        self.assertTrue(self.slug, "слепок пути проекта не посчитался")
        self.assertEqual(self.rc, 0, "stats --context упал с ошибкой: %s" % self.out)

    def test_rewrites(self):
        # Число перезаписей: одна, вторая запись того же requestId склеена, а
        # запрос с большой записью при выросшем чтении перезаписью не считается.
        self.assertIn_("перезаписи префикса после старта: 1 из 9 запросов", self.out,
                       "stats --context должен насчитать одну перезапись из 9 запросов")
        self.assertIn_("записано 30 000 токенов", self.out,
                       "цена перезаписи должна быть 30 000 токенов")
        self.assertIn_("запрос 4 из 7: записано 30 000, чтение просело 21 000 -> 5 000", self.out,
                       "строка перезаписи не сошлась")

    def test_start_breakdown(self):
        # Разбивка старта: префиксы первых запросов обоих потоков, 20 003 и 8 002.
        self.matches(r"старт *28 005 *\(27%\)", "старт должен быть 28 005 (27%)")
        self.matches(r"история *75 300 *\(73%\)", "история должна быть 75 300 (73%)")
        self.matches(r"чтение *154 500", "чтение должно быть 154 500")
        self.assertIn_("потоков 2 (из них субагентских 1), запросов 9", self.out,
                       "потоки и запросы не сошлись")
        self.matches(r"Bash *60", "объём результатов Bash должен быть 60 символов")
        self.matches(r"Read *10", "объём результатов Read должен быть 10 символов")

    def test_without_session_logs(self):
        # Там, где журналов сессий нет: внятное сообщение и код 2, а не трейсбек.
        empty = self.box.root / "empty"
        empty.mkdir()
        rc, out = self.box.dkctl_run("stats", "--context", "-C", str(empty))
        self.assertEqual(rc, 2, "stats --context без журналов сессий должен вернуть код 2")
        self.assertIn_("журналы сессий не найдены", out,
                       "stats --context без журналов должен сказать это словами")
        self.assertNotIn_("Traceback", out, "stats --context без журналов упал трейсбеком")

    def test_empty_log_directory(self):
        # Пустая директория журналов: тоже сообщение, а не деление на ноль.
        eproj = self.box.root / "eproj"
        eproj.mkdir()
        (self.box.home / ".claude" / "projects" / context.slug(eproj.resolve())).mkdir(parents=True)
        rc, out = self.box.dkctl_run("stats", "--context", "-C", str(eproj))
        self.assertEqual(rc, 2, "stats --context на пустой директории журналов должен вернуть код 2")
        self.assertIn_("нет запросов с расходом", out,
                       "stats --context на пустой директории должен сказать это словами")

    def test_bare_stats_keeps_its_table(self):
        # Голый stats печатает прежнюю таблицу запусков, а не разбивку сессий.
        sproj = git_init(self.box.root / "sproj")
        write(sproj / ".devkit" / "log", LOG)
        _, out = self.box.dkctl_run("stats", "-C", str(sproj))
        self.assertIn_("итого", out, "голый stats должен печатать таблицу запусков")
        self.assertNotIn_("перезаписи", out, "голый stats не должен печатать разбивку сессий")


if __name__ == "__main__":
    unittest.main()
