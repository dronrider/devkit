#!/usr/bin/env python3
"""Настройки пользователя машины: разбор конфига, текст страницы, находка
доктора про незаданный род и доставка страницы до сессии.
"""
import shutil
import tempfile
import unittest
from pathlib import Path

from testenv import PY, SandboxCase, read, run, user, without_pockets, write


class GenderConfigTest(unittest.TestCase):
    """Чтение и запись `~/.devkit/user.local`. Свой HOME на каждый случай:
    настоящая машина род уже задала, и тест судил бы по ней."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp(prefix="devkit-user-"))
        self.addCleanup(shutil.rmtree, str(self.home), True)
        self.conf = self.home / ".devkit" / "user.local"
        self.page = self.home / ".devkit" / "user.md"

    def test_no_config_is_no_gender(self):
        self.assertEqual(user.gender(self.home), "")

    def test_gender_read_back(self):
        write(self.conf, "gender = женский\n")
        self.assertEqual(user.gender(self.home), "женский")

    def test_unknown_value_reads_as_unset(self):
        # Значение не из пары это тот же незаданный род: угадывать по опечатке
        # нечего, и находка доктора назовёт команду.
        write(self.conf, "gender = ж\n")
        self.assertEqual(user.gender(self.home), "")

    def test_set_keeps_neighbour_keys(self):
        write(self.conf, "name = t\ngender = мужской\n")
        user.set_gender("женский", self.home)
        self.assertEqual(read(self.conf), "name = t\ngender = женский\n")
        self.assertEqual(user.gender(self.home), "женский")

    def test_set_writes_page_next_to_config(self):
        user.set_gender("женский", self.home)
        self.assertEqual(read(self.page), user.page_text("женский"))
        self.assertIn("женском", read(self.page))
        self.assertIn("проверила", read(self.page))

    def test_set_refuses_a_third_value(self):
        with self.assertRaises(ValueError):
            user.set_gender("средний", self.home)
        self.assertFalse(self.conf.exists(), "конфиг написан значением не из пары")

    def test_page_without_gender_asks_for_it(self):
        text = user.page_text("")
        self.assertIn("не задан", text)
        self.assertIn(user.SET_HINT, text)
        self.assertNotIn("проверила", text)
        self.assertNotIn("проверил»", text)


class GenderCheckTest(unittest.TestCase):
    """Находка доктора: род не назван, страница не написана или разошлась."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp(prefix="devkit-user-check-"))
        self.addCleanup(shutil.rmtree, str(self.home), True)
        self.page = self.home / ".devkit" / "user.md"

    def test_unset_gender_is_a_finding_with_the_command(self):
        findings, fixed = user.check(False, self.home)
        self.assertTrue([f for f in findings if "род первого лица не задан" in f], findings)
        self.assertTrue([f for f in findings if user.SET_HINT in f],
                        "находка не назвала команду: %s" % (findings,))

    def test_fix_writes_the_page_but_not_the_gender(self):
        findings, fixed = user.check(True, self.home)
        self.assertTrue(self.page.is_file(), "страница не написана")
        self.assertTrue([d for d in fixed if "страница рода первого лица" in d], fixed)
        # Род человек называет сам, и --fix его не выдумывает: находка остаётся.
        self.assertTrue([f for f in findings if "род первого лица не задан" in f], findings)
        self.assertEqual(user.gender(self.home), "")

    def test_set_gender_leaves_the_doctor_silent(self):
        user.set_gender("женский", self.home)
        findings, fixed = user.check(False, self.home)
        self.assertEqual(findings, [])
        self.assertEqual(fixed, [])

    def test_page_out_of_sync_with_the_config(self):
        user.set_gender("женский", self.home)
        write(self.page, user.page_text("мужской"))
        findings, _ = user.check(False, self.home)
        self.assertTrue([f for f in findings if "разошлась с конфигом" in f], findings)
        user.check(True, self.home)
        self.assertEqual(read(self.page), user.page_text("женский"))

    def test_hand_written_page_is_restored(self):
        user.set_gender("мужской", self.home)
        write(self.page, "# моё\n")
        findings, _ = user.check(False, self.home)
        self.assertTrue(findings, "правка страницы руками прошла молча")


class GenderCommandTest(unittest.TestCase):
    """Команда `devkitctl user`: печать заданного и запись нового."""

    def setUp(self):
        self.home = Path(tempfile.mkdtemp(prefix="devkit-user-cmd-"))
        self.addCleanup(shutil.rmtree, str(self.home), True)

    def dkctl(self, *args):
        return run([PY, str(Path(user.__file__).parent / "devkitctl.py")] + list(args),
                   env={"HOME": str(self.home)})

    def test_prints_unset_and_the_command(self):
        rc, out = self.dkctl("user")
        self.assertEqual(rc, 1, out)
        self.assertIn("не задан", out)
        self.assertIn(user.SET_HINT, out)

    def test_sets_and_prints_back(self):
        rc, out = self.dkctl("user", "--gender", "женский")
        self.assertEqual(rc, 0, out)
        self.assertEqual(user.gender(self.home), "женский")
        rc, out = self.dkctl("user")
        self.assertEqual(rc, 0, out)
        self.assertIn("женский", out)

    def test_third_value_refused(self):
        rc, out = self.dkctl("user", "--gender", "средний")
        self.assertEqual(rc, 2, out)
        self.assertEqual(user.gender(self.home), "")


class GenderReachesTheSessionTest(SandboxCase):
    """Доставка: страницу зовёт импортом глобальная точка, и доктор с --fix
    доводит машину до состояния, в котором род доезжает до каждой сессии."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("uproj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))

    def setUp(self):
        super().setUp()
        # Правки идут по подставному HOME, а он у класса общий: род возвращается
        # на место после каждого случая, иначе соседний тест судит по чужому.
        self.addCleanup(self.box.user_page, self.box.home)

    def test_global_point_imports_the_user_page(self):
        text = read(Path(self.box.home) / ".claude" / "CLAUDE.md")
        self.assertIn("@%s" % user.PAGE, text,
                      "глобальная точка не зовёт страницу настроек пользователя")

    def test_doctor_fix_writes_the_page(self):
        page = Path(self.box.home) / ".devkit" / "user.md"
        page.unlink()
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 1, without_pockets(out))
        self.assertIn("страница рода первого лица", without_pockets(out))
        rc, out = self.box.doctor(self.proj, "--fix")
        self.assertEqual(rc, 0, without_pockets(out))
        self.assertEqual(read(page), user.page_text("мужской"))

    def test_unset_gender_is_a_machine_finding(self):
        (Path(self.box.home) / ".devkit" / "user.local").unlink()
        rc, out = self.box.doctor(self.proj, "--fix")
        self.assertEqual(rc, 1, without_pockets(out))
        self.assertIn("машина: род первого лица не задан", without_pockets(out))
        self.assertIn(user.SET_HINT, out)

    def test_missing_page_is_not_blamed_on_devkit(self):
        # Импорт в пустоту генератор правил не считает своей находкой: про тот же
        # пробел говорит страница, и второй голос уводил бы на переехавший devkit.
        (Path(self.box.home) / ".devkit" / "user.md").unlink()
        rc, out = self.box.doctor(self.proj)
        self.assertNotIn("не разворачивается", without_pockets(out))


if __name__ == "__main__":
    unittest.main()
