#!/usr/bin/env python3
"""Литеральные токены в allow-правилах settings.json: значение токена в теле
правила ездит в контекст модели той же строкой, что и любое разрешение, и цель
DK-207 держит их чистыми маской __TRACKED_VAR__. Доктор ловит остатки литералов
и резервные копии файла рядом.
"""
import json
import os
import unittest

from testenv import PY, SandboxCase, leak, read, run, write


class LeakCliTest(SandboxCase):
    """Сверка прямо модулем: свой HOME, доктор для неё не нужен."""

    def setUp(self):
        super().setUp()
        self.phome = self.box.root / ("leak-" + self._testMethodName)
        (self.phome / ".claude").mkdir(parents=True)
        self.settings = self.phome / ".claude" / "settings.json"

    def lcli(self, *args):
        return run([PY, str(self.box.dk / "tools" / "devkitctl" / "leak.py")] + list(args),
                   home=self.phome)

    def test_clean_settings_are_silent(self):
        # Маска в теле правила значением не считается: чистый файл молчит.
        write(self.settings, '{"permissions": {"allow": ['
                             '"Bash(curl -H \'Authorization: Bearer __TRACKED_VAR__\' example)", '
                             '"Bash(curl -H \'PRIVATE-TOKEN: __TRACKED_VAR__\' example)"]}}\n')
        rc, out = self.lcli()
        self.assertEqual(rc, 0, "маска сочлась литералом: %s" % out)
        self.assertNotIn_("doctor --fix", out, "чистый файл дал находку с командой починки")

    def test_bearer_literal_is_found_and_fixed(self):
        # Литерал после Bearer ловится, --fix сменяет его на маску, повторная
        # проверка молчит.
        write(self.settings, '{"permissions": {"allow": ['
                             '"Bash(curl -H \'Authorization: Bearer pat-jira\' example)"]}}\n')
        rc, out = self.lcli()
        self.assertEqual(rc, 1, "литерал Bearer прошёл мимо проверки: %s" % out)
        self.assertIn_("Authorization: Bearer", out, "находка не назвала заголовок токена")
        self.assertIn_("doctor --fix", out, "находка не назвала команду починки")
        rc, out = self.lcli("--fix")
        self.assertEqual(rc, 0, "--fix не закрыл литерал Bearer: %s" % out)
        self.assertIn_("__TRACKED_VAR__", read(self.settings), "маска не легла в правило")
        rc, out = self.lcli()
        self.assertEqual(rc, 0, "после --fix проверка всё ещё видит литерал: %s" % out)

    def test_gitlab_and_release_patterns_are_caught(self):
        # Заголовки GitLab PAT, релиз-конвейера и ключа подписи ловятся все,
        # а --fix разом сменяет их на маску.
        write(self.settings, '{"permissions": {"allow": ['
                             '"Bash(curl -H \'PRIVATE-TOKEN: glpat-x\' example)", '
                             '"Bash(RELEASE_GITLAB_TOKEN=rl-x ./ship.sh)", '
                             '"Bash(RELEASE_JIRA_TOKEN=rl-j ./ship.sh)", '
                             '"Bash(ORG_GRADLE_PROJECT_xrReleasePublicKey=\'key-base64==\' ./build.sh)"]}}\n')
        rc, out = self.lcli()
        self.assertEqual(rc, 1, "литералы GitLab и релиза прошли мимо: %s" % out)
        for header in ("PRIVATE-TOKEN", "RELEASE_GITLAB_TOKEN", "RELEASE_JIRA_TOKEN",
                       "xrReleasePublicKey"):
            self.assertIn_(header, out, "находка не назвала заголовок %s" % header)
        rc, out = self.lcli("--fix")
        self.assertEqual(rc, 0, "--fix не закрыл литералы: %s" % out)
        text = read(self.settings)
        self.assertEqual(text.count("__TRACKED_VAR__"), 4, "маска легла не во все правила")
        rc, out = self.lcli()
        self.assertEqual(rc, 0, "после --fix остались литералы: %s" % out)

    def test_fix_is_idempotent_on_mask(self):
        # На чистом файле --fix ничего не переписывает: маска маской и остаётся.
        body = ('{"permissions": {"allow": ['
                '"Bash(curl -H \'Authorization: Bearer __TRACKED_VAR__\' example)"]}}\n')
        write(self.settings, body)
        self.lcli("--fix")
        self.assertEqual(read(self.settings), body, "--fix переписал чистый файл")

    def test_backup_copies_are_flagged_and_removed(self):
        # Резервные копии .bak-* и .pre-* несут те же литералы, что и основной
        # файл: находка, и --fix их стирает.
        write(self.settings, '{"permissions": {"allow": []}}\n')
        bak = self.settings.parent / "settings.json.bak-dk062"
        pre = self.settings.parent / "settings.json.pre-dk113"
        write(bak, "старое")
        write(pre, "старое")
        rc, out = self.lcli()
        self.assertEqual(rc, 1, "резервные копии прошли мимо проверки: %s" % out)
        self.assertIn_("копии settings.json", out, "находка не назвала копии")
        self.assertIn_("doctor --fix", out, "находка не назвала команду починки")
        rc, out = self.lcli("--fix")
        self.assertEqual(rc, 0, "--fix не стёр копии: %s" % out)
        self.assertFalse(bak.exists(), ".bak-копия осталась после --fix")
        self.assertFalse(pre.exists(), ".pre-копия осталась после --fix")

    def test_unrelated_copy_is_kept(self):
        # Посторонний файл рядом (не .bak/.pre) проверку не касается: чинить
        # то, чего нет в перечне, автоматике не за чем.
        write(self.settings, '{"permissions": {"allow": []}}\n')
        keep = self.settings.parent / "settings.json.devkit-tmp"
        write(keep, "промежуточный")
        rc, out = self.lcli("--fix")
        self.assertEqual(rc, 0, "посторонний файл дал находку: %s" % out)
        self.assertTrue(keep.exists(), "--fix стёр посторонний файл")

    def test_machine_without_settings_is_silent(self):
        # Файла нет, и течь в нём нечему: проверка молчит, как perms.
        rc, out = self.lcli()
        self.assertEqual(rc, 0, "машина без настроек дала находку: %s" % out)

    def test_worktree_only_checks(self):
        # Рубеж основного чекаута, тот же, что у прав: с ветки задачи литералы
        # только сверяются, чинить зовут из основного чекаута.
        write(self.settings, '{"permissions": {"allow": ['
                             '"Bash(curl -H \'Authorization: Bearer pat-x\' example)"]}}\n')
        findings, fixed = leak.check(str(self.settings), True, "/nowhere/main-devkit")
        self.assertFalse(fixed, "литералы заменены с непроверенной ветки: %s" % (fixed,))
        self.assertTrue(findings)
        self.assertIn("из основного чекаута", findings[0])
        self.assertIn("/nowhere/main-devkit/tools/devkitctl/devkitctl.py doctor --fix", findings[0])
        self.assertIn("Authorization: Bearer pat-x", read(self.settings),
                      "с непроверенной ветки правило переписано")


class LeakDoctorTest(SandboxCase):
    """Литералы видны через доктора: та же находка, тот же --fix."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("leak-proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.settings = cls.box.home / ".claude" / "settings.json"

    def test_doctor_flags_and_fixes_literal(self):
        # Подсунутый в allow литерал проходит через доктора, а --fix его глушит.
        data = json.loads(read(self.settings))
        keep = dict(data)
        data["permissions"]["allow"].append(
            "Bash(curl -H 'Authorization: Bearer pat-doctor' example)")
        write(self.settings, json.dumps(data, ensure_ascii=False, indent=2))
        try:
            _, out = self.box.doctor(self.proj)
            self.assertIn_("Authorization: Bearer", out, "доктор не увидел литерал в allow")
            self.assertIn_("doctor --fix", out, "находка доктора не назвала команду починки")
            _, out = self.box.doctor(self.proj, "--fix")
            self.assertNotIn_("Authorization: Bearer", out,
                              "после --fix доктор всё ещё видит литерал")
            self.assertIn_("__TRACKED_VAR__", read(self.settings),
                           "доктор не положил маску в правило")
            _, out = self.box.doctor(self.proj)
            self.assertNotIn_("литерал", out, "после --fix находка про литерал осталась")
        finally:
            write(self.settings, json.dumps(keep, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    unittest.main()
