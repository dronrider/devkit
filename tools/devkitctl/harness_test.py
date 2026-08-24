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
        self.assertIn_("секция [embed-tool] проектному слою не положена, маппинг ярусов машинный", out,
                       "секция проектного слоя прошла молча")


if __name__ == "__main__":
    unittest.main()
