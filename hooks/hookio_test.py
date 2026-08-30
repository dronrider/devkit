#!/usr/bin/env python3
"""Таблица разборщиков: что разбор снимает с живых событий, как зовётся
протокол и откуда берётся путь индекса памяти. Тут же то, что общее у всех
проверок разом: отказ на незнакомом протоколе, прогон каждого живого образца
через каждую проверку и раскладка самого прогона тестов.
"""
import io
import json
import os
import subprocess
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
TOOLS = {
    "tool-done-bash.json": ("Bash", None),
    "tool-done-subagent.json": ("Bash", "general-purpose"),
}
SESSIONS = {
    "prompt-submit.json": (hookio.PROMPT_SUBMIT, "", None, None),
    "turn-done.json": (hookio.TURN_DONE, "", "Готово.", None),
    # Тип API-ошибки едет поводом; тире в тексте пишет сам харнес, в ожидании
    # оно стоит escape-последовательностью, чтобы не спорить с проверкой символов.
    "turn-failed.json": (hookio.TURN_FAILED, "server_error",
                         "API Error: Can't reach the API server \u2014 check "
                         "your internet or DNS (ENOTFOUND)", None),
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

    def test_tool_axis(self):
        for name, (tool, agent) in TOOLS.items():
            event = sample(name)
            got = hookio.parse_tool(hookio.DEFAULT, event)
            self.assertEqual((got.tool, got.agent), (tool, agent), name)
            # Сессия целиком: подхват сверяет её с именем витка, и обрезанный
            # ID такой сверки не выдержит.
            self.assertEqual(got.session, event["session_id"], name)
            self.assertEqual(len(got.session), 36, name)
            # Дерево берётся из события, а не из транскрипта, и у обоих образцов
            # это дерево задачи, в котором их сняли.
            self.assertEqual(got.cwd, event["cwd"], name)
            self.assertNotIn("/scratchpad/", got.cwd, name)

    def test_write_turn_is_a_tool_turn(self):
        # Запись файла это такой же завершённый ход, и реплика на нём доставляется
        # наравне с ходом Bash.
        for name in WRITES:
            got = hookio.parse_tool(hookio.DEFAULT, sample(name))
            self.assertEqual(got.tool, sample(name)["tool_name"], name)
            self.assertIsNone(got.agent, name)

    def test_unfinished_and_sessionwide_events_are_not_tool_turns(self):
        # Ход, который ещё не случился, и события сессии репликой не будят: у
        # первого нет результата, у вторых нет хода вовсе.
        for name in list(SESSIONS) + ["pre-tool-use-bash.json", "pre-tool-use-read.json"]:
            self.assertIsNone(hookio.parse_tool(hookio.DEFAULT, sample(name)), name)



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
                     lambda: hookio.parse_tool("codex", {}),
                     lambda: hookio.reply("codex"),
                     lambda: hookio.context("codex")):
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

    def test_the_chat_hook_sees_nothing_to_deliver_to(self):
        # Подхват реплики стоит на каждом ходе чужой работы: кривой вход это тихий
        # ноль, а не traceback посреди витка.
        self.assertIsNone(hookio.tool_event(hookio.DEFAULT, io.StringIO("не json")))

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


class TestContext(unittest.TestCase):
    """Второй канал: добавка контекста. Она приезжает к модели без рамки
    провала, поэтому ею говорят то, что ходу инструмента претензией не является,
    и живая реплика человека это ровно такой случай."""

    def test_addition_goes_out_as_one_json_record(self):
        out = io.StringIO()
        channel = hookio.Context("PostToolUse", out)
        # Ноль, а не двойка: ход остался удачным, повторять его незачем.
        self.assertEqual(channel.say("реплика человека"), 0)
        self.assertEqual(hookio.context(hookio.DEFAULT).code, 0)
        lines = out.getvalue().splitlines()
        self.assertEqual(len(lines), 1)
        self.assertEqual(json.loads(lines[0]),
                         {"hookSpecificOutput": {"hookEventName": "PostToolUse",
                                                 "additionalContext": "реплика человека"}})

    def test_russian_text_goes_out_as_it_was_written(self):
        # Экранированный JSON харнес разберёт, а вот человеку, который смотрит
        # ручной прогон хука, вместо реплики достанутся коды символов.
        out = io.StringIO()
        hookio.Context("PostToolUse", out).say("реплика")
        self.assertIn("реплика", out.getvalue())

    def test_the_two_channels_are_told_apart(self):
        # Находка и добавка это разные ответы на разное, и спутать их нельзя:
        # рамка «поправь то, что сделал» на ходе git push читается витком как
        # провал с побочными эффектами, а понятная реакция на провал это повтор.
        self.assertFalse(hasattr(hookio.reply(hookio.DEFAULT), "say"))
        self.assertFalse(hasattr(hookio.context(hookio.DEFAULT), "found"))


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


class TestTomlAt(unittest.TestCase):
    """Конфиги проверок читаются тем же парсером, что профили, и причина пустого
    разбора нужна текстом: доктор печатает её находкой."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "проба.toml")

    def write(self, body):
        with open(self.path, "w", encoding="utf-8") as f:
            f.write(body)
        return self.path

    def test_config_is_parsed(self):
        doc, why = hookio.toml_at(self.write('[prose]\nmode = "warn"\nmin = 5\n'))
        self.assertEqual(why, "")
        self.assertEqual(doc.str_of("prose", "mode"), "warn")

    def test_missing_file_names_the_path(self):
        doc, why = hookio.toml_at(self.path)
        self.assertIsNone(doc)
        self.assertIn(self.path, why)

    def test_broken_file_names_the_line(self):
        doc, why = hookio.toml_at(self.write("[prose\n"))
        self.assertIsNone(doc)
        self.assertIn("проба.toml:1", why)


# Проверки, зовущие разбор: у них общая таблица протоколов, и спрашивать её
# каждую по отдельности значит четырежды написать одно и то же.
CHECKS = ("check-symbols", "check-memory", "check-sensitive", "check-prose")

# Точка входа git своего модуля не имеет и проверяется оттуда, откуда её зовут
# по существу. Здесь только карта, чтобы потерянный хук был виден.
COVERED_ELSEWHERE = {
    "pre-commit": "check_symbols_test.py, check_sensitive_test.py, check_tests_test.py",
    "commit-msg": "check_commit_test.py",
}


def modules(suite):
    """Модули, которые discover действительно взял в прогон."""
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            for name in modules(item):
                yield name
        else:
            yield type(item).__module__


def run_check(tool, *args, **kw):
    return subprocess.run([sys.executable, os.path.join(HERE, tool + ".py")]
                          + list(args), capture_output=True, text=True, **kw)


class TestProtocolRefusal(unittest.TestCase):
    """Незнакомый протокол это отказ с внятной строкой, а не молчаливый
    пропуск: иначе опечатка в settings.json выключила бы проверку насовсем."""

    def test_every_check_names_the_reason(self):
        event = json.dumps({"tool_input": {"file_path": "x.md",
                                           "new_string": "текст"}})
        for tool in CHECKS:
            r = run_check(tool, "--hook", "кодекс", input=event)
            self.assertEqual(r.returncode, 2, tool)
            self.assertIn("не заведён", r.stderr, tool)


class TestSamplesThroughChecks(unittest.TestCase):
    """Каждый живой образец через каждую проверку. Разбор у них общий, поэтому
    непонятая форма события должна всплыть тут целиком, а не в одной проверке
    из трёх."""

    def test_every_sample_through_every_check(self):
        for name in sorted(os.listdir(DATA)):
            if not name.endswith(".json"):
                continue
            with open(os.path.join(DATA, name), encoding="utf-8") as f:
                text = f.read()
            for tool in CHECKS:
                r = run_check(tool, "--hook", "claude-code", input=text)
                self.assertIn(r.returncode, (0, 2), (tool, name, r.stderr))
                self.assertNotIn("Traceback", r.stderr, (tool, name))


class TestRunnerLayout(unittest.TestCase):
    """Раннер тестов хуков это сам unittest, и терять проверки он умеет молча:
    файл, чьё имя не годится в имя модуля (дефис), discover пропускает без
    единого слова, а прогон остаётся зелёным. Отсюда и обе проверки ниже."""

    def test_discover_takes_every_test_file(self):
        files = {n for n in os.listdir(HERE) if n.endswith("_test.py")}
        self.assertEqual(files, {m + ".py" for m in modules(
            unittest.TestLoader().discover(HERE, pattern="*_test.py"))})

    def test_every_hook_has_its_test(self):
        for name in sorted(os.listdir(HERE)):
            path = os.path.join(HERE, name)
            base, ext = os.path.splitext(name)
            if not os.path.isfile(path) or base.endswith("_test"):
                continue
            if ext not in (".py", ".sh", ""):
                continue
            if name in COVERED_ELSEWHERE:
                continue
            self.assertTrue(os.path.exists(
                os.path.join(HERE, base.replace("-", "_") + "_test.py")), name)


if __name__ == "__main__":
    unittest.main(verbosity=0)
