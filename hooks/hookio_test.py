#!/usr/bin/env python3
"""Юниты таблицы разборщиков: что разбор снимает с живых событий, как зовётся
протокол и откуда берётся путь индекса памяти. Прогон проверок целиком (каждый
образец через каждую проверку) живёт в test.sh.
"""
import io
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import hookio  # noqa: E402

DATA = os.path.join(HERE, "testdata", "claude-code")

# Что разбор обязан снять с образцов, снятых живьём с Claude Code. Ожидание тут
# выписано руками: посчитанное тем же разбором сошлось бы с любым его
# поведением.
WRITES = {
    "write-write.json": ("/scratchpad/note.md", ["первая строка"]),
    "write-edit.json": ("/scratchpad/note.md", ["вторая"]),
    "write-notebook.json": ("/cap/work/nb.ipynb", ["print(2)"]),
    "write-memory-index.json": ("/scratchpad/memory/MEMORY.md",
                                ["- [Проба](proba.md) - крючок\n"]),
}
SESSIONS = {
    "prompt-submit.json": (hookio.PROMPT_SUBMIT, "", None, None),
    "turn-done.json": (hookio.TURN_DONE, "", "Готово.", None),
    "subagent-done.json": (hookio.SUBAGENT_DONE, "", "4", "general-purpose"),
    "notify-permission.json": (hookio.NOTIFY, "permission_prompt",
                               "Claude needs your permission", None),
}


def sample(name):
    with open(os.path.join(DATA, name), encoding="utf-8") as f:
        return json.load(f)


class TestSamples(unittest.TestCase):
    def test_every_protocol_has_samples(self):
        # Форма события живёт на стороне инструмента, и разбор, проверенный по
        # памяти, закрепляет наше представление о ней вместо неё самой.
        for name in hookio.PROTOCOLS:
            d = os.path.join(HERE, "testdata", name)
            self.assertTrue(os.path.isdir(d), name)
            self.assertTrue([f for f in os.listdir(d) if f.endswith(".json")], name)

    def test_written_chunks(self):
        for name, (path_end, chunks) in WRITES.items():
            write = hookio.parse_write(hookio.DEFAULT, sample(name))
            self.assertTrue(write.path.endswith(path_end), (name, write.path))
            self.assertEqual(write.chunks, chunks, name)

    def test_write_is_not_a_session(self):
        # Запись файла ни о каком ожидании не говорит, и уведомителю с ней
        # делать нечего.
        for name in WRITES:
            self.assertIsNone(hookio.parse_session(hookio.DEFAULT, sample(name)), name)

    def test_session_axes(self):
        for name, (kind, reason, message, agent) in SESSIONS.items():
            sess = hookio.parse_session(hookio.DEFAULT, sample(name))
            self.assertEqual((sess.kind, sess.reason), (kind, reason), name)
            self.assertEqual(sess.message, message, name)
            self.assertEqual(sess.agent, agent, name)
            self.assertEqual(len(sess.session), 8, name)
            self.assertTrue(sess.cwd.endswith("/cap/work"), name)
            self.assertTrue(sess.transcript.endswith(".jsonl"), name)

    def test_session_is_not_a_write(self):
        for name in SESSIONS:
            self.assertIsNone(hookio.parse_write(hookio.DEFAULT, sample(name)), name)


class TestProtocolName(unittest.TestCase):
    def test_bare_hook_stays_claude_code(self):
        # Команды в settings.json на машинах прописаны голым --hook, и
        # обновление devkit их ломать не должно.
        self.assertEqual(hookio.DEFAULT, "claude-code")
        for argv in ([], [""], None):
            self.assertEqual(hookio.protocol(argv or []), "claude-code")

    def test_name_comes_from_the_argument(self):
        self.assertEqual(hookio.protocol(["codex"]), "codex")

    def test_unknown_protocol_names_the_known_ones(self):
        for call in (lambda: hookio.parse_write("codex", {}),
                     lambda: hookio.parse_session("codex", {}),
                     lambda: hookio.reply("codex")):
            with self.assertRaises(hookio.Unknown) as e:
                call()
            self.assertIn("claude-code", str(e.exception))


class TestBadEvent(unittest.TestCase):
    def test_reasons_are_told_apart(self):
        # Уведомитель пишет причину в журнал: «не json» и «json не объектом»
        # это разные жалобы на разное.
        for text, reason in (("не json", "не json"), ("[1,2]", "json не объектом"),
                             ("42", "json не объектом"), ('"строка"', "json не объектом")):
            with self.assertRaises(hookio.BadEvent) as e:
                hookio.load(io.StringIO(text))
            self.assertEqual(str(e.exception), reason, text)

    def test_checks_see_nothing_to_look_at(self):
        # Проверке текста причина ни к чему: смотреть нечего, и хук уходит нулём.
        self.assertIsNone(hookio.write_event(hookio.DEFAULT, io.StringIO("не json")))

    def test_event_without_tool_input(self):
        self.assertIsNone(hookio.parse_write(hookio.DEFAULT, {"tool_input": "строкой"}))
        self.assertIsNone(hookio.parse_write(hookio.DEFAULT, {}))


class TestReply(unittest.TestCase):
    def test_findings_go_to_the_agent(self):
        # У claude-code канал ответа это stderr с выходом 2: находки харнес
        # отдаёт агенту фидбеком, и правит он их сам.
        out = io.StringIO()
        reply = hookio.Reply(2, out)
        self.assertEqual(reply.found("находка\n"), 2)
        self.assertEqual(out.getvalue(), "находка\n")
        self.assertEqual(hookio.reply(hookio.DEFAULT).code, 2)


class TestMemoryIndex(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def profile(self, body):
        with open(os.path.join(self.dir, "проба.toml"), "w", encoding="utf-8") as f:
            f.write(body)
        return hookio.memory_index("проба", self.dir)

    def test_key_from_the_profile(self):
        self.assertEqual(hookio.memory_index("claude-code"), "/memory/MEMORY.md")
        self.assertEqual(self.profile('[hooks]\nmemory_index = "/mem/INDEX.md"\n'),
                         "/mem/INDEX.md")

    def test_tool_without_memory(self):
        # Ключа нет, значит памяти у инструмента нет, и линтовать нечего.
        self.assertEqual(self.profile('[hooks]\nprotocol = "проба"\n'), "")
        self.assertEqual(hookio.memory_index("нет-такого", self.dir), "")

    def test_broken_profile_is_not_a_crash(self):
        # Битый профиль это дело doctor, а не хука в каждой сессии.
        self.assertEqual(self.profile("[hooks\n"), "")


if __name__ == "__main__":
    unittest.main(verbosity=0)
