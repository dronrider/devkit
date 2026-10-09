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

from testenv import SandboxCase, fake_home

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
        """Перезапись дешевле: запрос не уходит, строка «истёк» у свежего решения."""
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self._write_stream("stream7", reqs)
        # Простой 3500s: продлений уже 14 по цене 1.4x против перезаписи 1.25x,
        # решение «истёк» при этом свежее и строку пишет.
        now = base.timestamp() + 3500
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

    def test_long_dead_stream_leaves_no_journal(self):
        """Давно остывший поток не пишет «истёк» на каждом обходе: строка
        отмечает момент истечения, а не каждое напоминание о мёртвом дереве."""
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self._write_stream("stream7d", reqs)

        def sender(session, model, root, **kw):
            return True

        cachekeep.scan_project(self.proj.resolve(), now=base.timestamp() + 7200,
                               home=self.box.home, sender=sender)
        cachekeep.scan_project(self.proj.resolve(), now=base.timestamp() + 9000,
                               home=self.box.home, sender=sender)
        content = (self.turns_log.read_text(encoding="utf-8")
                   if self.turns_log.exists() else "")
        self.assertNotIn("истёк", content,
                         "простой за окном истечения журнала не пишет")
        self.assertNotIn("продлил", content, "мёртвый поток не продлевается")

    def test_expiry_window_covers_the_crossover(self):
        """Окно истечения накрывает порог пересечения пятиминутного TTL."""
        self.assertEqual(cachekeep.expiry_window(300), 3660,
                         "окно это порог пересечения плюс один интервал")

    def test_expired_flag_only_for_fresh_expiry(self):
        """Признак «истёк» ставится у свежего решения, а не у каждого мёртвого
        потока: сводка по нему не помечает мёртвое дерево значимым на каждом тике."""
        self._reset_logs()
        base = datetime(2026, 10, 7, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", self._ts(base, 0)),
            make_raw(500, 199000, 30, 10, "m1", self._ts(base, 10)),
        ]
        self._write_stream("stream7e", reqs)

        def sender(session, model, root, **kw):
            return True

        got = cachekeep.scan_project(self.proj.resolve(), now=base.timestamp() + 3500,
                                     home=self.box.home, sender=sender)
        self.assertEqual(len(got), 1)
        self.assertTrue(got[0]["expired"],
                        "свежее решение не продлевать несёт признак «истёк»")
        self.assertFalse(got[0]["sent"], "запрос не ушёл, признак отправки пуст")
        self._reset_logs()
        self._write_stream("stream7e", reqs)
        got = cachekeep.scan_project(self.proj.resolve(), now=base.timestamp() + 7200,
                                     home=self.box.home, sender=sender)
        self.assertEqual(len(got), 1)
        self.assertFalse(got[0]["expired"],
                         "давно остывший поток признака «истёк» не несёт")
        self.assertFalse(got[0]["keep"], "решение по цене всё ещё «не продлевать»")

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
                cachekeep.journal_line("s1", True, 0, 10)
        self.assertIn("не записана", err.getvalue(),
                      "отказ записи уходит строкой stderr")


class CacheKeepHomesTest(SandboxCase):
    """Дома журналов по харнесам: обход машины, resume по дому, один журнал."""

    def setUp(self):
        super().setUp()
        self.proj = Path(self.box.root / "ckproj-homes")
        self.proj.mkdir(exist_ok=True)
        # Машинный слой стенда: включён glm-code с собственным домом, сток
        # читается тем же ~/ подставного дома.
        self.glm = self.box.home / "glm-home"
        self.conf = self.box.home / ".devkit" / "harness.local"
        self.conf.write_text(
            'enabled = ["glm-code"]\n\n[glm-code]\nhome = "%s"\n' % self.glm,
            encoding="utf-8")
        self.turns_log = self.box.home / ".devkit" / "turns.log"
        os.environ["DEVKIT_TURN_MARK_LOG"] = str(self.turns_log)
        self.slug = context.slug(self.proj.resolve())
        self.stock = self.box.home / ".claude" / "projects" / self.slug
        self.glm_logs = self.glm / "projects" / self.slug
        # Стенд общий на класс: потоки и журнал стираются на каждый тест,
        # иначе один тест видит дерево другого как своё.
        self._reset_logs()

    def _reset_logs(self):
        for d in (self.stock, self.glm_logs):
            if d.exists():
                for p in d.glob("*.jsonl"):
                    p.unlink()
        if self.turns_log.exists():
            self.turns_log.unlink()

    def tearDown(self):
        os.environ.pop("DEVKIT_TURN_MARK_LOG", None)
        super().tearDown()

    def _idle_stream(self, name):
        """Поток с простоем чуть больше пятиминутного TTL: продление выгодно.

        Имя файла несёт суффикс .jsonl: обход видит только журналы с ним.
        """
        base = datetime(2026, 10, 9, 12, 0, 0, tzinfo=timezone.utc)
        reqs = [
            make_raw(200000, 0, 50, 10, "m1", base.strftime("%Y-%m-%dT%H:%M:%S.000Z")),
            make_raw(500, 199000, 30, 10, "m1",
                     (base + timedelta(seconds=10)).strftime("%Y-%m-%dT%H:%M:%S.000Z")),
        ]
        write_stream(self.glm_logs / (name + ".jsonl"), reqs)
        return base

    def test_project_homes_see_stock_and_harness(self):
        """Обход видит сток-дом и дом включённого харнеса, у каждого свой resume."""
        with fake_home(self.box.home):
            entries = cachekeep.project_homes(
                self.proj.resolve(), machine_path=str(self.conf),
                profiles_dir=self.box.dk / "kit" / "harness")
        self.assertEqual([e["harness"] for e in entries],
                         ["claude-code", "glm-code"],
                         "сток первым, дальше дом включённого харнеса")
        self.assertEqual(entries[0]["dir"], self.stock,
                         "сток-дом это ~/.claude/projects/<слепок>")
        self.assertEqual(entries[1]["dir"], self.glm_logs,
                         "дом харнеса это <home>/projects/<слепок>, без .claude")
        self.assertEqual(entries[1]["resume"][:4],
                         ["agentctl", "exec", "--harness", "glm-code", "--"][:4],
                         "продление дома glm уходит клиентом glm через обёртку")
        self.assertEqual(entries[0]["resume"][0], "claude",
                         "сток-дом продлевается штатным клиентом без обёртки")

    def test_scan_all_sends_with_resume_of_the_house(self):
        """Продлевающий запрос уходит resume того харнеса, чей дом нашли."""
        base = self._idle_stream("glmstream")
        calls = []

        def sender(session, model, root, **kw):
            calls.append((session, kw.get("resume")))
            return True

        with fake_home(self.box.home):
            got = cachekeep.scan_all(
                self.proj.resolve(), now=base.timestamp() + 400, sender=sender,
                machine_path=str(self.conf),
                profiles_dir=self.box.dk / "kit" / "harness")
        glm = [d for d in got if d["harness"] == "glm-code"]
        self.assertEqual(len(glm), 1, "поток дома glm получил решение")
        self.assertEqual(
            calls,
            [("glmstream", cachekeep.harness_resume(
                "glm-code", self.box.dk / "kit" / "harness"))],
            "запрос ушёл resume профиля glm-code")
        content = self.turns_log.read_text(encoding="utf-8")
        self.assertIn("продлил", content, "ушедший запрос отмечен строкой")
        self.assertIn("харнес glm-code", content,
                      "строка журнала называет дом подписки")
        self.assertFalse((self.glm / ".devkit" / "turns.log").exists(),
                         "журнал отметок один на машину и в дом подписки не пишется")

    def test_max_idle_skips_stale_files(self):
        """Горизонт разбора пропускает поток, молчащий дольше него, до чтения."""
        base = self._idle_stream("oldstream")
        path = self.glm_logs / "oldstream.jsonl"
        old = base.timestamp() - 7 * 3600
        os.utime(str(path), (old, old))
        entry = {"harness": "glm-code", "dir": self.glm_logs,
                 "resume": cachekeep.DEFAULT_RESUME}
        # dry_run на обоих заходах: тест меряет горизонт разбора, а не отправку,
        # и настоящий клиент продления на стенде запускаться не должен.
        got = cachekeep.scan_home(entry, self.proj.resolve(),
                                  now=base.timestamp() + 400, dry_run=True,
                                  max_idle=6 * 3600)
        self.assertEqual(got, [], "файл за горизонтом не разбирается")
        got = cachekeep.scan_home(entry, self.proj.resolve(),
                                  now=base.timestamp() + 400, dry_run=True)
        self.assertEqual(len(got), 1, "без горизонта поток разбирается")

    def test_corpus_dirs_see_stock_and_harness(self):
        """Корни журналов всех домов: drain --all ходит и по дому подписки."""
        with fake_home(self.box.home):
            dirs = cachekeep.corpus_dirs(machine_path=str(self.conf),
                                         profiles_dir=self.box.dk / "kit" / "harness")
        self.assertIn(self.box.home / ".claude" / "projects", dirs,
                      "сток-корень в обходе")
        self.assertIn(self.glm / "projects", dirs,
                      "корень дома харнеса в обходе, без .claude")

    def test_dry_run_sends_nothing(self):
        """Сухой прогон принимает решения и не шлёт продлевающих запросов."""
        base = self._idle_stream("drystream")
        calls = []

        def sender(session, model, root, **kw):
            calls.append(session)
            return True

        entry = {"harness": "glm-code", "dir": self.glm_logs,
                 "resume": cachekeep.DEFAULT_RESUME}
        got = cachekeep.scan_home(entry, self.proj.resolve(),
                                  now=base.timestamp() + 400, sender=sender,
                                  dry_run=True)
        self.assertEqual(len(got), 1, "решение принято")
        self.assertFalse(got[0].get("sent"), "на сухом прогоне отправки нет")
        self.assertEqual(calls, [], "sender на сухом прогоне не звался")

    def test_cli_keys_dry_run_and_max_idle(self):
        """Ключи cachekeep --dry-run и --max-idle доходят до разбора."""
        self._idle_stream("clistream")
        rc, out = self.box.dkctl_run("cachekeep", "-C", str(self.proj),
                                     "--dry-run", "--max-idle", "60")
        self.assertIn(rc, (0, 2), "живой вызов с ключами упал: %s" % out)
        self.assertNotIn("Traceback", out, "вызов с ключами упал трейсбеком")

    def test_stats_context_and_drain_see_harness_home(self):
        """stats --context и drain читают дом подписки, а не только сток."""
        self._idle_stream("homestats")
        self.assertFalse((self.stock / "homestats.jsonl").exists(),
                         "сток на этом тесте пуст, обход обязан взять дом харнеса")
        rc, out = self.box.dkctl_run("stats", "--context", "-C", str(self.proj))
        self.assertEqual(rc, 0, "stats --context упал: %s" % out)
        self.assertIn("дом журналов харнеса glm-code", out,
                      "stats --context называет дом подписки")
        rc, out = self.box.dkctl_run("drain", "-C", str(self.proj))
        self.assertEqual(rc, 0, "drain упал: %s" % out)
        self.assertNotIn("Traceback", out, "drain упал трейсбеком")
        self.assertIn("вызовов", out, "drain разобрал журналы дома подписки")


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
