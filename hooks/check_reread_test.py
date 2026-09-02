#!/usr/bin/env python3
"""Рубеж повторных чтений (DK-146): PreToolUse-хук на Read отвечает
подсказкой вместо содержимого, когда файл уже прочитан в этой сессии и не
менялся с тех пор. Первый Read проходит нетронутым, чтение после правки файла
проходит тоже, чтение другого окна (другие offset/limit/pages) того же файла
не считается повтором.

История чтений своя (~/.devkit/reread/<контекст>.json), неизменность файла
проверяется парой mtime_ns + size. Контекст это не сессия: субагент делит
session_id с диспетчером, но своё окно чтения у него отдельное (DK-608).
Развилки и их обоснование в docs/tasks/DK-146.md, docs/tasks/DK-608.md и шапке
check-reread.py."""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-reread.py")
SAMPLE = os.path.join(HERE, "testdata", "claude-code", "pre-tool-use-read.json")
SUB_SAMPLE = os.path.join(HERE, "testdata", "claude-code",
                          "pre-tool-use-read-subagent.json")

# Дефис в имени скрипта не годится для import, поэтому модуль грузится по пути.
_spec = importlib.util.spec_from_file_location("check_reread", TOOL)
check_reread = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(check_reread)


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def read_event(path, agent=None, **window):
    """Событие PreToolUse на Read по пути и параметрам окна. Параметры окна
    опускаются, если их не передали: тогда их нет и в событии, которое
    разбирает хук. agent это роль субагента: харнес кладёт agent_id в событие
    только под ним, у хода самой сессии этого поля нет вовсе."""
    ti = {"file_path": path}
    ti.update(window)
    event = {"session_id": "s1",
             "hook_event_name": "PreToolUse",
             "tool_name": "Read",
             "tool_input": ti}
    if agent:
        event["agent_id"] = agent
    return json.dumps(event)


class RereadCase(unittest.TestCase):
    """Базовая фикстура: временный каталог с файлом, отдельный каталог
    состояния, путь к каталогу состояния передаётся хуку флагом --state, чтобы
    тесты не лазили в настоящий ~/.devkit/reread."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.state = tempfile.mkdtemp()
        self.file = os.path.join(self.tmp, "note.md")
        with open(self.file, "w", encoding="utf-8") as f:
            f.write("первая строка\n")

    def tearDown(self):
        for d in (self.tmp, self.state):
            for root, _, files in os.walk(d, topdown=False):
                for n in files:
                    try:
                        os.remove(os.path.join(root, n))
                    except OSError:
                        pass
                try:
                    os.rmdir(root)
                except OSError:
                    pass

    def hook(self, event, session="s1"):
        if isinstance(event, str):
            event = event.replace('"s1"', '"%s"' % session, 1)
        return run("--hook", "--state", self.state, input=event)

    def touch(self, mtime=None):
        """Обновить mtime файла, чтобы имитировать правку. Современные ФС имеют
        наносекундное разрешение, поэтому usleep'ом промежутка не делаем, а
        пишем в файл и выставляем mtime явно, когда нужен уникальный."""
        with open(self.file, "a", encoding="utf-8") as f:
            f.write(" правка\n")
        if mtime is not None:
            os.utime(self.file, (mtime, mtime))


class TestFirstRead(RereadCase):
    def test_first_read_passes_and_records(self):
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)
        # Состояние записано: в каталоге один файл на сессию.
        self.assertEqual(os.listdir(self.state), ["s1.json"])
        with open(os.path.join(self.state, "s1.json"), encoding="utf-8") as f:
            data = json.load(f)
        self.assertEqual(len(data), 1)
        # Ключ окна несёт путь и пустые offset/limit/pages.
        key = list(data)[0]
        self.assertIn(self.file, key)
        self.assertIn("null", key)


class TestRepeatDetected(RereadCase):
    def test_unchanged_file_second_read_is_hint(self):
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn(self.file, r.stderr)
        self.assertIn("DK-146", r.stderr)

    def test_hint_names_the_window(self):
        # Подсказка отличает «то же окно» от «другой кусок того же файла»: без
        # этого агент не понял бы, как обойти рубеж, если ему правда нужен другой
        # кусок.
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file))
        self.assertIn("offset", r.stderr)
        self.assertIn("limit", r.stderr)

    def test_hint_has_trailing_newline(self):
        # Без него harness склеивает подсказку со следующим выводом, и в
        # транскрипте она читается частью чужой строки.
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file))
        self.assertTrue(r.stderr.endswith("\n"), repr(r.stderr))

    def test_third_read_still_hint_until_edit(self):
        # Состояние не двигается на подсказке: до правки файла сколько угодно
        # повторов режутся одинаково.
        self.hook(read_event(self.file))
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)


class TestAfterEdit(RereadCase):
    def test_edit_invalidates_state(self):
        # Правка файла меняет mtime, и следующее чтение проходит как первое.
        self.hook(read_event(self.file))
        self.touch()
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_after_edit_repeat_is_hint_again(self):
        # После правки и первого перечитывания состояние снова актуально, и
        # второй раз тот же файл повтор.
        self.hook(read_event(self.file))
        self.touch()
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stderr)

    def test_only_mtime_change_is_enough(self):
        # touch без правки меняет mtime, и хук считает файл «другим»: это
        # сознательное ограничение (см. шапку), и тест держит контракт. Крайне
        # узкий случай, вреда от пропуска нет.
        self.hook(read_event(self.file))
        os.utime(self.file, (time.time() + 1, time.time() + 1))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestWindows(RereadCase):
    """Окно это (path, offset, limit, pages). Чтение другим окном того же
    файла не повтор: режь его, и агент теряет доступ к куску, который ему
    правда нужен."""

    def test_different_offset_is_not_repeat(self):
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file, offset=10))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_different_limit_is_not_repeat(self):
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file, limit=5))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_different_pages_is_not_repeat(self):
        self.hook(read_event(self.file, pages="1-3"))
        r = self.hook(read_event(self.file, pages="4-5"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_same_window_twice_is_hint(self):
        self.hook(read_event(self.file, offset=2, limit=3))
        r = self.hook(read_event(self.file, offset=2, limit=3))
        self.assertEqual(r.returncode, 2, r.stderr)

    def test_full_then_partial_is_not_repeat(self):
        # Сначала полный файл, потом его кусок. Частый сценарий из замера
        # DK-148: когда окно другое, повтор не ловим, хотя файл уже видели.
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file, offset=0, limit=1))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_partial_then_full_is_not_repeat(self):
        # Симметрично: кусок сначала, полный потом. Полный файл в контексте
        # мог появиться и не через Read, поэтому полным чтением агент свою
        # задачу решает.
        self.hook(read_event(self.file, offset=0, limit=1))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)


class TestSessions(RereadCase):
    def test_different_sessions_isolated(self):
        # Сессии параллельны, и повтор в одной не виден другой: что прочитано в
        # сессии A, в сессии B подсказкой не режется.
        self.hook(read_event(self.file), session="aaa")
        r = self.hook(read_event(self.file), session="bbb")
        self.assertEqual(r.returncode, 0, r.stderr)
        # Каждой сессии свой файл состояния.
        self.assertEqual(sorted(os.listdir(self.state)), ["aaa.json", "bbb.json"])

    def test_missing_session_passes(self):
        # Без session_id историю не привязать, и рубить нельзя: пусть Read
        # проходит как есть. Событие из тестдиректории сессию несёт, поэтому
        # тут явный JSON без неё.
        event = json.dumps({"hook_event_name": "PreToolUse",
                            "tool_name": "Read",
                            "tool_input": {"file_path": self.file}})
        r = run("--hook", "--state", self.state, input=event)
        self.assertEqual(r.returncode, 0, r.stderr)


class TestSubagentContext(RereadCase):
    """Субагент делит session_id с диспетчером, а контекст у него свой и
    пустой: прочитанное диспетчером в нём не лежит, и подсказка вместо
    содержимого режет ему нужный источник (DK-608). Различает контексты поле
    agent_id, которое есть в событии только под субагентом."""

    def test_subagent_reads_what_dispatcher_read(self):
        # Регрессия DK-608: диспетчер прочитал файл, субагент читает его
        # первый раз в своём контексте и получает содержимое, а не подсказку.
        self.hook(read_event(self.file))
        r = self.hook(read_event(self.file, agent="a1"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_repeat_inside_subagent_is_hint(self):
        # Рубеж внутри одного контекста стоит как стоял: то же окно тем же
        # субагентом это повтор.
        self.hook(read_event(self.file, agent="a1"))
        r = self.hook(read_event(self.file, agent="a1"))
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertIn(self.file, r.stderr)

    def test_two_subagents_isolated(self):
        # Два субагента одной сессии читают независимо: контекст у каждого свой.
        self.hook(read_event(self.file, agent="a1"))
        r = self.hook(read_event(self.file, agent="a2"))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_dispatcher_after_subagent_passes(self):
        # Обратный ход: субагент прочитал, у диспетчера файла в контексте нет.
        self.hook(read_event(self.file, agent="a1"))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_dispatcher_repeat_still_hint(self):
        # Чужие контексты состояние диспетчера не затирают: его собственный
        # повтор режется по-прежнему.
        self.hook(read_event(self.file))
        self.hook(read_event(self.file, agent="a1"))
        r = self.hook(read_event(self.file))
        self.assertEqual(r.returncode, 2, r.stdout)

    def test_state_files_named_by_context(self):
        # Диспетчеру файл по сессии, субагенту по сессии с приписанным
        # agent_id через точку.
        self.hook(read_event(self.file))
        self.hook(read_event(self.file, agent="a1"))
        self.assertEqual(sorted(os.listdir(self.state)), ["s1.a1.json", "s1.json"])

    def test_agent_id_with_separator_stays_in_state_dir(self):
        # Имя файла собирается из значений события, и разделитель пути в них
        # не должен уводить запись из каталога состояния.
        r = self.hook(read_event(self.file, agent="../beyond"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(os.listdir(self.state), ["s1.___beyond.json"])


class TestBadInput(RereadCase):
    def test_bad_json_passes(self):
        r = run("--hook", "--state", self.state, input="not json")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_missing_tool_input_passes(self):
        r = run("--hook", "--state", self.state,
                input=json.dumps({"session_id": "s1", "hook_event_name": "PreToolUse",
                                  "tool_name": "Read"}))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_non_read_tool_passes(self):
        # Матчер стоит на Read, но плохой вход не должен ронять хук.
        r = self.hook(json.dumps({"session_id": "s1",
                                  "hook_event_name": "PreToolUse",
                                  "tool_name": "Bash",
                                  "tool_input": {"command": "ls"}}))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_missing_file_passes(self):
        # Файла нет, Read сам скажет, и подсказка тут лишняя.
        r = self.hook(read_event(os.path.join(self.tmp, "absent.md")))
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_no_args_prints_doc(self):
        r = run()
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("PreToolUse", r.stderr)


class TestSampleEvent(RereadCase):
    """Живой снимок события из testdata разбирается хуком как есть: форма
    события на стороне инструмента, и в образце хранится её реальный вид, а
    не пересозданный по памяти."""

    def sample_on(self, path, name):
        """Образец с подставленным путём читаемого файла: сам путь в снимке
        частный (/private/tmp/proj/note.md), и файла там нет."""
        with open(name, encoding="utf-8") as f:
            event = json.load(f)
        event["tool_input"]["file_path"] = path
        return json.dumps(event)

    def test_sample_records_state(self):
        # Путь в образце частный (/private/tmp/proj/note.md), и файла там нет:
        # хук уходит нулём, не падая на плохом пути.
        with open(SAMPLE, encoding="utf-8") as f:
            text = f.read()
        r = run("--hook", "--state", self.state, input=text)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("Traceback", r.stderr)

    def test_live_subagent_sample_reads_after_dispatcher(self):
        # Живой снимок субагента и он же без пары полей, которые харнес кладёт
        # только под субагентом: так выглядел бы тот же ход у самого
        # диспетчера. Всё остальное, включая session_id и transcript_path, у
        # них общее, и второе чтение всё равно проходит (DK-608).
        with open(SUB_SAMPLE, encoding="utf-8") as f:
            sub = json.load(f)
        sub["tool_input"]["file_path"] = self.file
        plain = dict(sub)
        del plain["agent_id"], plain["agent_type"]
        r = run("--hook", "--state", self.state, input=json.dumps(plain))
        self.assertEqual(r.returncode, 0, r.stderr)
        r = run("--hook", "--state", self.state, input=json.dumps(sub))
        self.assertEqual(r.returncode, 0, r.stderr)
        # Пара сверяется и на обратном ходе: повтор у диспетчера режется, то
        # есть первый ход состояние записал и тест не проходит вхолостую.
        r = run("--hook", "--state", self.state, input=json.dumps(plain))
        self.assertEqual(r.returncode, 2, r.stdout)

    def test_live_samples_share_session_and_transcript(self):
        # Снимки сверяются на то, ради чего взяты: сессия и транскрипт у
        # диспетчера с субагентом одни, различает их только agent_id.
        with open(SAMPLE, encoding="utf-8") as f:
            plain = json.load(f)
        with open(SUB_SAMPLE, encoding="utf-8") as f:
            sub = json.load(f)
        self.assertNotIn("agent_id", plain)
        self.assertTrue(sub["agent_id"])
        self.assertEqual(sub["session_id"],
                         os.path.basename(sub["transcript_path"])[:-len(".jsonl")])


class TestSweep(unittest.TestCase):
    """Брошенное состояние старше порога убирается тем же проходом, что и
    читает."""

    def setUp(self):
        self.state = tempfile.mkdtemp()

    def tearDown(self):
        for root, _, files in os.walk(self.state, topdown=False):
            for n in files:
                try:
                    os.remove(os.path.join(root, n))
                except OSError:
                    pass
            try:
                os.rmdir(root)
            except OSError:
                pass

    def test_old_state_is_pruned(self):
        old = os.path.join(self.state, "old.json")
        new = os.path.join(self.state, "new.json")
        for path in (old, new):
            with open(path, "w", encoding="utf-8") as f:
                f.write("{}")
        # Старый файл сдвигаем за порог, новый оставляем в окне.
        stale = check_reread.STALE + 10
        old_mtime = time.time() - stale
        os.utime(old, (old_mtime, old_mtime))
        check_reread.sweep(self.state, time.time())
        self.assertFalse(os.path.exists(old), "sweep не убрал старый файл")
        self.assertTrue(os.path.exists(new), "sweep снёс свежий файл")

    def test_non_json_kept(self):
        # Файлы не .json уборка не трогает, чтобы не снести соседние каталоги
        # состояния с другим назначением (хотя их тут и не лежит).
        mark = os.path.join(self.state, "lock")
        with open(mark, "w", encoding="utf-8") as f:
            f.write("")
        stale = check_reread.STALE + 10
        old = time.time() - stale
        os.utime(mark, (old, old))
        check_reread.sweep(self.state, time.time())
        self.assertTrue(os.path.exists(mark))


class TestUnknownProtocol(unittest.TestCase):
    def test_unknown_protocol_refuses(self):
        # Незнакомый протокол это отказ кодом 2, а не молчаливый пропуск:
        # опечатка в settings.json иначе выключила бы рубеж насовсем.
        event = json.dumps({"session_id": "s1", "tool_input": {"file_path": "/tmp"}})
        r = run("--hook", "кодекс", input=event)
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("не заведён", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
