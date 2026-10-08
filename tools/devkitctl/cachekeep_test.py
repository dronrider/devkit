#!/usr/bin/env python3
"""Стенд cachekeep: синтетический поток с простоями, решения и журнал.

Механику держит стенд с синтетическим потоком (барьер «событие» DK-1312):
падение перезаписи видно на настоящем заходе, а здесь проверяется логика
решений, цены и строки журнала.
"""
import json
import os
import unittest
from datetime import datetime, timezone, timedelta
from pathlib import Path

from testenv import SandboxCase

import cachekeep
import context


def make_req(write, read, input_t, output, model, ts):
    """Запрос в разобранном виде, как отдаёт context.requests()."""
    return {
        "write": write, "read": read, "input": input_t,
        "output": output, "model": model, "ts": ts,
    }


def make_raw(write, read, input_t, output, model, ts):
    """Сырая запись JSONL для записи в файл журнала."""
    return {
        "type": "assistant",
        "requestId": "req_%s" % ts,
        "message": {
            "model": model,
            "usage": {
                "cache_creation_input_tokens": write,
                "cache_read_input_tokens": read,
                "input_tokens": input_t,
                "output_tokens": output,
            },
        },
        "timestamp": ts,
        "uuid": "u_%s" % ts,
    }


def write_stream(path, reqs):
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        for r in reqs:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")


class CacheKeepLogicTest(unittest.TestCase):
    """Чистая логика решений: цены, порог пересечения, число продлений."""

    def test_pings_needed_short_idle(self):
        self.assertEqual(cachekeep.pings_needed(100, 300), 0,
                         "простой меньше TTL не требует продления")

    def test_pings_needed_one_cycle(self):
        self.assertEqual(cachekeep.pings_needed(350, 300), 1,
                         "простой чуть больше одного TTL требует одного продления")

    def test_pings_needed_many_cycles(self):
        n = cachekeep.pings_needed(1500, 300)
        self.assertGreaterEqual(n, 3,
                                "простой в пять TTL требует нескольких продлений")
        self.assertLessEqual(n, 6,
                             "продлений не больше, чем интервалов с запасом")

    def test_keepalive_cheaper_for_short_idle(self):
        self.assertTrue(cachekeep.should_keepalive(500000, 350, 300),
                        "короткий простой: поддержание дешевле перезаписи")

    def test_keepalive_cheaper_at_crossover(self):
        keep = cachekeep.keepalive_cost(500000, 2500, 300)
        rewrite = cachekeep.rewrite_cost(500000, 300)
        self.assertLess(keep, rewrite,
                        "на пороге пересечения поддержание ещё дешевле")

    def test_rewrite_cheaper_for_long_idle(self):
        self.assertFalse(cachekeep.should_keepalive(500000, 7200, 300),
                         "длинный простой: перезапись дешевле поддержания")

    def test_idle_within_ttl_is_free(self):
        self.assertTrue(cachekeep.should_keepalive(500000, 100, 300),
                        "простой внутри TTL: кеш жив, поддержание не нужно")

    def test_rewrite_cost_long_ttl(self):
        self.assertEqual(cachekeep.rewrite_cost(100000, 3600), 200000,
                         "часовой TTL: перезапись стоит 2x")

    def test_rewrite_cost_short_ttl(self):
        self.assertEqual(cachekeep.rewrite_cost(100000, 300), 125000,
                         "пятиминутный TTL: перезапись стоит 1.25x")


class CacheKeepStreamTest(SandboxCase):
    """Решения по синтетическому потоку: тайминги, TTL, строки журнала."""

    def setUp(self):
        super().setUp()
        self.proj = Path(self.box.root / "ckproj")
        self.proj.mkdir(exist_ok=True)
        self.logs = self.box.home / ".claude" / "projects" / context.slug(self.proj.resolve())
        self.logs.mkdir(parents=True, exist_ok=True)
        self.turns_log = self.box.home / ".devkit" / "turns.log"
        os.environ["DEVKIT_TURN_MARK_LOG"] = str(self.turns_log)

    def tearDown(self):
        os.environ.pop("DEVKIT_TURN_MARK_LOG", None)
        super().tearDown()

    def _reset_logs(self):
        """Стереть потоки и журнал: стенд общий на класс, scan_project видит всех."""
        if self.logs.exists():
            for p in self.logs.glob("*.jsonl"):
                p.unlink()
        if self.turns_log.exists():
            self.turns_log.unlink()

    def _write_stream(self, name, reqs):
        path = self.logs / (name + ".jsonl")
        write_stream(path, reqs)
        return path

    def _ts(self, base, offset_seconds):
        dt = base + timedelta(seconds=offset_seconds)
        return dt.strftime("%Y-%m-%dT%H:%M:%S.000Z")

    def test_decide_stream_short_idle(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 99000, 30, 10, "m1", self._ts(base, 10)),
        ]
        path = self._write_stream("stream1", reqs)
        now = base.timestamp() + 60
        d = cachekeep.decide_stream(path, now=now)
        self.assertIsNotNone(d, "решение по потоку должно быть")
        self.assertTrue(d["keep"], "короткий простой: кеш жив, продлевать нечего")
        self.assertEqual(d["ttl"], 300, "TTL по умолчанию пятиминутный")

    def test_decide_stream_idle_over_ttl(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        path = self._write_stream("stream2", reqs)
        now = base.timestamp() + 400
        d = cachekeep.decide_stream(path, now=now)
        self.assertIsNotNone(d, "решение по потоку должно быть")
        self.assertTrue(d["keep"], "простой 400s: поддержание дешевле перезаписи")
        self.assertGreaterEqual(d["pings"], 1, "для простоя больше TTL нужно продление")

    def test_decide_stream_long_idle_rewrite_cheaper(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        path = self._write_stream("stream3", reqs)
        now = base.timestamp() + 7200
        d = cachekeep.decide_stream(path, now=now)
        self.assertIsNotNone(d, "решение по потоку должно быть")
        self.assertFalse(d["keep"], "длинный простой: перезапись дешевле")

    def test_journal_line_written(self):
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 99000, 30, 10, "m1", self._ts(base, 10)),
        ]
        path = self._write_stream("stream4", reqs)
        now = base.timestamp() + 400
        cachekeep.scan_project(self.proj.resolve(), now=now, home=self.box.home,
                               sender=lambda *a, **k: True)
        self.assertTrue(self.turns_log.exists(), "журнал должен быть записан")
        content = self.turns_log.read_text(encoding="utf-8")
        self.assertIn("кэш", content, "строка журнала должна нести слово кэш")
        self.assertIn("сессия stream4", content, "строка журнала должна называть сессию")
        self.assertIn("продлил", content, "ушедший запрос даёт строку «продлил»")

    def test_detect_ttl_short(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_req(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_req(500, 99000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self.assertEqual(cachekeep.detect_ttl(reqs), 300,
                         "без пауз TTL пятиминутный")

    def test_detect_ttl_long(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_req(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_req(500, 99000, 30, 10, "m1", self._ts(base, 10)),
            make_req(90000, 500, 30, 10, "m1", self._ts(base, 3700)),
        ]
        self.assertEqual(cachekeep.detect_ttl(reqs), 3600,
                         "пауза между пятиминутным и часовым: часовой TTL")

    def test_rewrite_causes_attribution(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_req(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_req(500, 99000, 30, 10, "m1", self._ts(base, 10)),
            make_req(90000, 500, 30, 10, "m1", self._ts(base, 400)),
            make_req(80000, 100, 30, 10, "m2", self._ts(base, 410)),
        ]
        causes = context.rewrite_causes(reqs)
        self.assertEqual(causes.get(2), "простой",
                         "перезапись после простоя 390s это простой")
        self.assertEqual(causes.get(3), "модель",
                         "перезапись при смене модели это модель")

    def test_rewrite_causes_head(self):
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_req(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_req(500, 99000, 30, 10, "m1", self._ts(base, 10)),
            make_req(80000, 100, 30, 10, "m1", self._ts(base, 15)),
        ]
        causes = context.rewrite_causes(reqs)
        self.assertEqual(causes.get(2), "голова",
                         "перезапись без простоя и без смены модели это голова")

    def test_report_shows_causes(self):
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(100000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 99000, 30, 10, "m1", self._ts(base, 10)),
            make_raw(90000, 500, 30, 10, "m1", self._ts(base, 400)),
        ]
        self._write_stream("stream5", reqs)
        out = []
        class W:
            def write(self, s):
                out.append(s)
        context.report(self.logs, W())
        text = "".join(out)
        self.assertIn("причины:", text, "отчёт должен называть причины перезаписей")
        self.assertIn("простой", text, "причина простой должна быть в отчёте")

    def test_keepalive_sent_on_keep(self):
        """Решение «продлевать» и простой длиннее TTL уводят запрос, и строка
        журнала «продлил» пишется только о реально ушедшей работе."""
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self._write_stream("stream6", reqs)
        now = base.timestamp() + 400
        calls = []

        def sender(session, model, root, **kw):
            calls.append((session, model))
            return True

        cachekeep.scan_project(self.proj.resolve(), now=now, home=self.box.home,
                               sender=sender)
        self.assertEqual(calls, [("stream6", "m1")],
                         "запрос уходит один раз, с моделью потока")
        content = self.turns_log.read_text(encoding="utf-8")
        self.assertIn("продлил", content, "ушедший запрос даёт строку «продлил»")

    def test_no_send_when_rewrite_cheaper(self):
        """Длинный простой: перезапись дешевле, запрос не уходит, строка «истёк»."""
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self._write_stream("stream7", reqs)
        now = base.timestamp() + 7200
        calls = []

        def sender(session, model, root, **kw):
            calls.append(session)
            return True

        cachekeep.scan_project(self.proj.resolve(), now=now, home=self.box.home,
                               sender=sender)
        self.assertEqual(calls, [], "запрос не уходит, когда перезапись дешевле")
        content = self.turns_log.read_text(encoding="utf-8")
        self.assertIn("истёк", content, "решение не продлевать видно строкой «истёк»")
        self.assertNotIn("продлил", content, "«продлил» без работы не пишется")

    def test_no_journal_when_send_fails(self):
        """Отказ отправки не даёт строки «продлил»: молчание за работу не считается."""
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self._write_stream("stream8", reqs)
        now = base.timestamp() + 400

        def sender(session, model, root, **kw):
            return False

        cachekeep.scan_project(self.proj.resolve(), now=now, home=self.box.home,
                               sender=sender)
        content = self.turns_log.read_text(encoding="utf-8") if self.turns_log.exists() else ""
        self.assertNotIn("продлил", content,
                         "не ушедший запрос не заявляет работу строкой «продлил»")

    def test_keepalive_argv_fills_model(self):
        argv = cachekeep.keepalive_argv("S1", "mimo-v2.6-pro")
        self.assertIn("S1", argv, "resume несёт сессию")
        self.assertIn("mimo-v2.6-pro", argv, "resume несёт модель, а не {model}")
        self.assertNotIn("{model}", argv, "литеральный {model} до текста зова не доезжает")
        self.assertNotIn("{session}", argv, "литеральный {session} до текста зова не доезжает")
        self.assertIn("-p", argv, "короткий запрос приставлен хвостом")
        self.assertEqual(argv[-1], cachekeep.KEEPALIVE_PROMPT,
                         "короткий запрос последним аргументом")

    def test_keepalive_argv_wrapped_harness(self):
        resume = ["agentctl", "exec", "--harness", "mimo", "--",
                  "claude", "--resume", "{session}", "--model", "{model}"]
        argv = cachekeep.keepalive_argv("S2", "m1", resume=resume)
        self.assertEqual(argv[:5], ["agentctl", "exec", "--harness", "mimo", "--"],
                         "обёртка сохраняется")
        self.assertIn("m1", argv, "модель подставлена")
        self.assertEqual(argv[-2:], ["-p", cachekeep.KEEPALIVE_PROMPT],
                         "-p хвостом уходит клиенту после --")

    def test_journal_error_is_visible(self,):
        """Отказ записи журнала виден stderr, а не глотается (замечание 7)."""
        import io
        import unittest.mock
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        err = io.StringIO()
        with unittest.mock.patch("builtins.open", side_effect=OSError("диск полон")):
            with unittest.mock.patch("sys.stderr", err):
                cachekeep.journal_line("s1", True, 0, 10, home=self.box.home)
        self.assertIn("не записана", err.getvalue(),
                      "отказ записи уходит строкой stderr")


class HarnessResumeModelTest(unittest.TestCase):
    """Во всех профилях resume несёт --model: смена модели не переписывает префикс."""

    PROFILES = ["claude-code.toml", "glm-code.toml", "routerai.toml", "mimo.toml"]

    def test_resume_has_model(self):
        kit = Path(__file__).resolve().parent.parent.parent / "kit" / "harness"
        for name in self.PROFILES:
            path = kit / name
            text = path.read_text(encoding="utf-8")
            in_head = False
            for line in text.splitlines():
                if line.strip() == "[head]":
                    in_head = True
                elif line.startswith("[") and in_head:
                    in_head = False
                if in_head and "resume" in line and "=" in line:
                    self.assertIn("--model", line,
                                  "%s: resume должен нести --model" % name)
                    self.assertIn("{model}", line,
                                  "%s: resume должен подставлять {model}" % name)

    def test_resume_argv_fills_model(self):
        """argv ResumeCommand несёт модель, а не литеральный {model} (замечание 3)."""
        kit = Path(__file__).resolve().parent.parent.parent / "kit" / "harness"
        for name in self.PROFILES:
            path = kit / name
            text = path.read_text(encoding="utf-8")
            resume = None
            in_head = False
            for line in text.splitlines():
                if line.strip() == "[head]":
                    in_head = True
                elif line.startswith("[") and in_head:
                    in_head = False
                if in_head and "resume" in line and "=" in line:
                    raw = line.split("=", 1)[1].strip()
                    resume = [p.strip().strip('"') for p in raw.strip("[]").split(",")]
                    break
            self.assertIsNotNone(resume, "%s: resume в [head] не найден" % name)
            argv = cachekeep.keepalive_argv("SID", "the-model", resume=resume)
            joined = " ".join(argv)
            self.assertIn("the-model", joined,
                          "%s: argv должен нести подставленную модель" % name)
            self.assertNotIn("{model}", joined,
                             "%s: литеральный {model} не должен оставаться в argv" % name)
            self.assertIn("SID", joined,
                          "%s: argv должен нести подставленную сессию" % name)


if __name__ == "__main__":
    unittest.main()
