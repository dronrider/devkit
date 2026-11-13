#!/usr/bin/env python3
"""Профили харнесов: разбор общих с agentctl фикстур, валидация профиля
сегодняшнего харнеса и сужение машинного списка проектным слоем.
"""
import unittest

from testenv import PY, DEVKIT_SRC, SandboxCase, harness, read, rules, run, write

# Подставной профиль для самопроверки: импортов инструмент не понимает, правила
# доезжают до него только вклейкой, остальных осей у него нет.
EMBED_TOOL = ('[detect]\n\n[rules]\nmode = "embed"\n\n[delegate]\nmode = "none"\n'
              '\n[hooks]\n\n[quota]\n')
# Профиль без ключа file в [rules]: валидацию он не проходит.
BROKEN = ('[detect]\n\n[rules]\nmode = "import"\n\n[delegate]\nmode = "none"\n'
          '\n[hooks]\n\n[quota]\n')


class ProfileParserTest(SandboxCase):
    """На каждый вход отчёт парсера сверяется побайтно с .expected, тот же файл
    сверяет Go-реализация (tools/agentctl/harness_test.go). Разъедься парсеры, и
    один и тот же профиль читался бы двумя утилитами по-разному.
    """

    def test_reports_match_expected(self):
        data = self.box.dk / "kit" / "harness" / "testdata"
        seen = 0
        for f in sorted(data.glob("*.toml")):
            seen += 1
            rc, out = run([PY, str(self.box.dk / "tools" / "devkitctl" / "harness.py"), str(f)])
            self.assertEqual(rc, 0, "парсер профилей упал на %s: %s" % (f.stem, out))
            want = read(data / (f.stem + ".expected"))
            self.assertEqual(out, want, "отчёт по %s разошёлся с ожидаемым" % f.stem)
        self.assertGreaterEqual(seen, 10, "фикстур парсера найдено %d, набор потерялся" % seen)


class ProfileValidationTest(SandboxCase):
    """Профиль сегодняшнего харнеса валиден, и доктор это проверяет на каждом
    прогоне: битый профиль молча выключил бы ось, а находится он тут.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.profile = cls.box.dk / "kit" / "harness" / "claude-code.toml"

    def test_healthy_profiles_are_silent(self):
        _, out = self.box.doctor(self.proj)
        self.assertNotIn_("профиль харнеса", out, "находка по исправным профилям")

    def test_broken_profile_is_named(self):
        keep = read(self.profile)
        write(self.profile, BROKEN)
        try:
            _, out = self.box.doctor(self.proj)
            self.assertIn_("профиль харнеса битый: claude-code.toml: [rules] нет ключа file", out,
                           "нет находки про битый профиль харнеса")
            # Следствие битого профиля называется отдельно: генерировать по нему
            # нечего, и молчать об этом нельзя, иначе правила просто перестали бы
            # доезжать.
            self.assertIn_("харнес claude-code включён, а его профиль не проходит валидацию", out,
                           "нет находки про генерацию по битому профилю")
        finally:
            write(self.profile, keep)


class AltSubProfileTest(unittest.TestCase):
    """Профиль второй подписки проходит свод и считает пути от машинного
    каталога (DK-180).

    Прибитый литералом путь тут был бы не косметикой: вторая подписка получила
    бы хозяйство в каталоге первой, то есть раскладку в чужой контур, а
    машинный ключ под подпроцесс всё равно нужен, и два пути разъехались бы.
    """

    MACHINE_PATHS = (("rules", "global_file"), ("hooks", "config"),
                     ("skills", "dir"), ("delegate", "agents_dir"))

    @classmethod
    def setUpClass(cls):
        path = DEVKIT_SRC / "kit" / "harness" / "glm-code.toml"
        cls.prof = harness.parse(path.name, read(path))
        harness.validate_profile(cls.prof)

    def test_machine_paths_count_from_home(self):
        for section, key in self.MACHINE_PATHS:
            spec = self.prof.str_of(section, key)
            self.assertTrue(spec, "профиль второй подписки не объявил [%s] %s" % (section, key))
            self.assertTrue(spec.startswith(rules.HOME_MARK),
                            "[%s] %s считается не от %s, а от %s: хозяйство уехало бы в чужой контур"
                            % (section, key, rules.HOME_MARK, spec))
            path, bad = rules.harness_path("glm-code", spec, {"glm-code": "/подписка"})
            self.assertEqual(bad, "", "путь [%s] %s не подставился: %s" % (section, key, bad))
            self.assertTrue(str(path).startswith("/подписка/"),
                            "путь [%s] %s ушёл мимо машинного каталога: %s" % (section, key, path))

    def test_subprocess_command_is_declared(self):
        # Спавна субагента снаружи у клиента нет, и без команды уехавшая ступень
        # не поднялась бы ничем.
        self.assertEqual(self.prof.str_of("delegate", "mode"), "cli",
                         "вторая подписка делегирует не командой")
        argv = self.prof.arr_of("delegate", "command")
        self.assertTrue(argv, "команда подпроцесса не объявлена")
        for mark in ("{prompt}", "{model}"):
            self.assertTrue([a for a in argv if mark in a],
                            "в команде подпроцесса нет %s: %s" % (mark, argv))

    def test_quota_is_swapped(self):
        # Остаток снимает сменный съёмщик из профиля: эндпоинт мониторинга
        # подписки отвечает тем же токеном, что клиент, и отдаёт оба окна
        # сразу. Пустая секция была бы молчащим корректором, а заявка без
        # script обещанием, которого профиль не держит.
        self.assertEqual(self.prof.str_of("quota", "snap"), "script",
                         "вторая подписка снимает остаток не сменным съёмщиком")
        self.assertEqual(self.prof.str_of("quota", "script"), "snap/glm-code.sh",
                         "съёмщик второй подписки объявлен не в snap/ профиля")
        self.assertTrue(self.prof.arr_of("quota", "buckets"),
                        "бакеты остатка не объявлены")

    def test_no_detect(self):
        # Изнутри сессии вторая подписка от первой переменными неотличима, и
        # объявленный детект дал бы неоднозначное совпадение сразу у двух
        # харнесов.
        self.assertEqual(self.prof.table("detect"), {},
                         "у второй подписки объявлен детект, а он неоднозначен по построению")


class OpencodeGoProfileTest(unittest.TestCase):
    """Профиль подписки opencode GO валиден с обеих сторон и считает машинные
    пути от {home} (питоновская половина пары, Go-половина в
    TestProfileOpencodeGo). Квоту профиль не объявляет: съёмщика остатка у
    подписки нет, а выдуманный скрипт обещал бы снимок, которого нечем снимать.
    """

    MACHINE_PATHS = (("rules", "global_file"), ("hooks", "config"),
                     ("skills", "dir"), ("delegate", "agents_dir"))

    @classmethod
    def setUpClass(cls):
        path = DEVKIT_SRC / "kit" / "harness" / "opencode-go.toml"
        cls.prof = harness.parse(path.name, read(path))
        harness.validate_profile(cls.prof)

    def test_machine_paths_count_from_home(self):
        for section, key in self.MACHINE_PATHS:
            spec = self.prof.str_of(section, key)
            self.assertTrue(spec, "профиль подписки не объявил [%s] %s" % (section, key))
            self.assertTrue(spec.startswith(rules.HOME_MARK),
                            "[%s] %s считается не от %s, а от %s: хозяйство уехало бы в чужой контур"
                            % (section, key, rules.HOME_MARK, spec))
            path, bad = rules.harness_path("opencode-go", spec, {"opencode-go": "/подписка"})
            self.assertEqual(bad, "", "путь [%s] %s не подставился: %s" % (section, key, bad))
            self.assertTrue(str(path).startswith("/подписка/"),
                            "путь [%s] %s ушёл мимо машинного каталога: %s" % (section, key, path))

    def test_ladder_is_two_deepseek(self):
        # Подписка отдаёт лёгкую flash и тяжёлую pro: нижние ярусы идёт flash,
        # верхние pro, и повторение значения это две модели, а не забытое.
        for tier, want in (("mini", "deepseek-v4.1-flash"), ("base", "deepseek-v4.1-flash"),
                           ("pro", "deepseek-v4-pro"), ("max", "deepseek-v4-pro")):
            self.assertEqual(self.prof.str_of("delegate", "map_" + tier), want,
                             "ярус %s маппится не в модель каталога подписки" % tier)

    def test_subprocess_command_is_declared(self):
        self.assertEqual(self.prof.str_of("delegate", "mode"), "cli",
                         "подписка делегирует не командой")
        argv = self.prof.arr_of("delegate", "command")
        self.assertTrue(argv, "команда подпроцесса не объявлена")
        joined = " ".join(argv)
        for mark in ("{prompt}", "{model}", "{effort}", "{session}"):
            self.assertIn(mark, joined, "в команде подпроцесса нет %s: %s" % (mark, argv))

    def test_head_rides_through_exec(self):
        # Голова поднимается через agentctl exec с явным именем подписки: без
        # имени exec поднял бы активный харнес, а подписка активной не
        # объявляется.
        client = " ".join(self.prof.arr_of("head", "client"))
        self.assertIn("agentctl exec --harness opencode-go", client,
                      "голова подписки идёт мимо agentctl exec с именем подписки: %s" % client)
        self.assertEqual(self.prof.str_of("head", "bin"), "claude",
                         "бинарь головы называет сам клиент, а не обёртка")

    def test_no_detect(self):
        # Изнутри сессии подписка от первой переменными неотличима, и
        # объявленный детект дал бы неоднозначное совпадение сразу у нескольких
        # харнесов.
        self.assertEqual(self.prof.table("detect"), {},
                         "у подписки объявлен детект, а он неоднозначен по построению")

    def test_quota_is_empty(self):
        self.assertFalse(self.prof.table("quota"),
                         "съёмщика остатка у подписки нет, выдуманного профилю не положено")


class CodexProfileTest(unittest.TestCase):
    """Профиль Codex валиден с обеих сторон и носит те ключи, на которых стоит
    его механика: детект по непустой CODEX_THREAD_ID, вклейка правил в
    AGENTS.md, делегирование командой codex exec, навыки в каталоге, который
    клиент подхватывает сам (питоновская половина пары, Go-половина в
    TestProfileCodex).
    """

    @classmethod
    def setUpClass(cls):
        path = DEVKIT_SRC / "kit" / "harness" / "codex.toml"
        cls.prof = harness.parse(path.name, read(path))
        harness.validate_profile(cls.prof)

    def test_detect_names_live_session(self):
        self.assertEqual(self.prof.str_of("detect", "env"), "CODEX_THREAD_ID",
                         "детект Codex идёт не по переменной живой сессии")
        self.assertEqual(self.prof.str_of("detect", "value"), "",
                         "[detect] value задан, а идентификатор сессии заранее не назван")
        self.assertEqual(self.prof.str_of("detect", "bin"), "codex")

    def test_rules_travel_embedded(self):
        self.assertEqual(self.prof.str_of("rules", "mode"), "embed",
                         "Codex импортов не разворачивает, правила едут только вклейкой")
        self.assertEqual(self.prof.str_of("rules", "file"), "",
                         "у embed-харнеса тонкого файла нет")

    def test_delegate_command_carries_marks(self):
        self.assertEqual(self.prof.str_of("delegate", "mode"), "cli")
        argv = self.prof.arr_of("delegate", "command")
        self.assertTrue(argv, "команда делегирования не объявлена")
        joined = " ".join(argv)
        for mark in ("{prompt}", "{model}", "{effort}", "workspace-write"):
            self.assertIn(mark, joined, "в команде делегирования нет %s: %s" % (mark, argv))
        for outsider in ("dangerously", "{session}"):
            self.assertNotIn(outsider, joined,
                             "в команде делегирования %s, а клиенту его не положено" % outsider)
        models = {"mini": "gpt-6-luna", "base": "gpt-6.1-terra",
                  "pro": "gpt-6.1-sol", "max": "gpt-6-astra"}
        for tier, model in models.items():
            self.assertEqual(self.prof.str_of("delegate", "map_" + tier), model,
                             "ярус %s маппится не в назначенную модель" % tier)

    def test_skills_lay_into_client_dir(self):
        self.assertEqual(self.prof.str_of("skills", "dir"), "~/.agents/skills",
                         "навыки кладутся не в каталог, который клиент сканирует сам")
        self.assertEqual(self.prof.str_of("skills", "discovery"), "auto",
                         "скан каталога навыков у клиента автоматический, указатель не нужен")
        self.assertEqual(self.prof.str_of("skills", "format"), "claude-code")

    def test_machine_axes_are_empty(self):
        # Секция [hooks] у Codex называет чужой протокол и пуста сверх него:
        # настроек хуков формата claude-code у клиента нет, и по ключу дашборд
        # узнаёт дорогу чата. Квоту снимает съёмщик из snap/codex.sh, и её
        # объявление живёт в профиле (DK-1706). Головы задач у клиента нет.
        self.assertEqual(self.prof.str_of("hooks", "protocol"), "codex",
                         "протокол чата Codex профилем не назван")
        quota = self.prof.table("quota")
        self.assertTrue(quota, "секция [quota] пуста, а остаток снимает snap/codex.sh")
        self.assertEqual(self.prof.str_of("quota", "snap"), "script")
        self.assertEqual(self.prof.str_of("quota", "script"), "snap/codex.sh")
        self.assertIsNone(self.prof.table("head"),
                          "интерактивной головы без хука конца хода профилю не положено")


class QuotaOptionalTest(unittest.TestCase):
    """Ключ optional секции [quota] валидируется синтетическим профилем:
    элементы обязаны стоять в buckets, а обязательный бакет необязательным
    стать не может. Зеркальная валидация agentctl покрыта своими тестами.
    """

    def validate(self, quota):
        text = ('[detect]\n[rules]\nmode = "embed"\n[delegate]\nmode = "none"\n'
                '[hooks]\nprotocol = "codex"\n[quota]\n'
                'snap = "script"\nscript = "snap/probe.sh"\n'
                'buckets = ["week_all", "window5h_all"]\nrequired = "week_all"\n'
                + quota +
                'spend_mini = ["week_all"]\nspend_base = ["week_all"]\n'
                'spend_pro = ["week_all"]\nspend_max = ["week_all"]\n')
        harness.validate_profile(harness.parse("probe.toml", text))

    def test_optional_bucket_passes(self):
        self.validate('optional = ["window5h_all"]\n')

    def test_optional_outside_buckets_refused(self):
        with self.assertRaises(harness.ProfileError) as ctx:
            self.validate('optional = ["month_plan"]\n')
        self.assertIn("optional", str(ctx.exception))

    def test_required_bucket_not_optional(self):
        with self.assertRaises(harness.ProfileError) as ctx:
            self.validate('optional = ["week_all", "window5h_all"]\n')
        self.assertIn("обязательный", str(ctx.exception))


class ProjectNarrowingTest(SandboxCase):
    """Проектный слой только сужает машинный список: профиль embed-tool на
    месте, но в машинном слое его нет, и сужением его не включить. Остаться
    совсем без харнесов это находка, а не тишина: правила иначе молча перестали
    бы доезжать.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("rproj")
        cls.box.dkctl_run("new", "--prefix", "RP", "-C", str(cls.proj))
        write(cls.box.dk / "kit" / "harness" / "embed-tool.toml", EMBED_TOOL)
        write(cls.proj / ".devkit" / "harness.local",
              'enabled = ["embed-tool"]\ndefault = "embed-tool"\n\n[embed-tool]\npro = "x"\n')

    def test_narrowing_to_disabled_harness(self):
        _, out = self.box.doctor(self.proj)
        self.assertIn_("включённых харнесов нет", out, "сужение до невключённого харнеса прошло молча")
        # Тексты те же, что у Go-стороны (tools/agentctl/harness.go, narrowByProject):
        # один и тот же проектный конфиг обе реализации разбирают одинаково и
        # говорят о нём одно и то же, иначе сужение чинилось бы по разным
        # подсказкам.
        self.assertIn_("embed-tool сужением не включить, в машинном слое его нет, пропущен", out,
                       "имя вне машинного слоя отброшено молча")
        self.assertIn_("claude-code сужен проектным слоем", out, "про суженный харнес доктор молчит")
        self.assertIn_("ключ default проектному слою не положен, понимается только enabled", out,
                       "лишний ключ проектного слоя прошёл молча")
        self.assertIn_("секция [embed-tool] проектному слою не положена, маппинг уровней машинный", out,
                       "секция проектного слоя прошла молча")


if __name__ == "__main__":
    unittest.main()
