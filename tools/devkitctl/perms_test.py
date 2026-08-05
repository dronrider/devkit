#!/usr/bin/env python3
"""Права машинного контура: без них сессия, поднятая без человека, встаёт на
первом же отказе харнеса и молча не делает ничего. Раскладываются они тем же
порядком, что определения агентов, а рукописное в permissions --fix не трогает.
"""
import json
import os
import unittest

from testenv import PY, SandboxCase, perms, read, run, write


class MachinePermsTest(SandboxCase):
    """Перечень прав и его раскладка доктором."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.project("proj")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))
        cls.settings = cls.box.home / ".claude" / "settings.json"

    def test_machine_allow_is_not_empty(self):
        # Фикстура машинного контура берёт права из самого перечня, а не списком
        # руками: разойдись они, чистый проект краснел бы на исправном коде.
        self.assertTrue(list(perms.MACHINE_ALLOW))

    def test_missing_perms_are_found_and_fixed(self):
        data = json.loads(read(self.settings))
        keep = dict(data)
        data["permissions"]["allow"] = ["Bash(taskctl:*)", "Bash(своё правило пользователя:*)"]
        write(self.settings, json.dumps(data, ensure_ascii=False, indent=2))
        try:
            _, out = self.box.doctor(self.proj)
            self.assertIn_("не хватает прав машинного контура", out, "нет находки про нехватку прав")
            self.assertNotIn_("Bash(taskctl:*)", out, "уже выданное право попало в находку")
            self.assertIn_("doctor --fix", out, "находка про права не назвала команду починки")
            _, out = self.box.doctor(self.proj, "--fix")
            self.assertIn_("починено: права машинного контура дописаны", out, "--fix не разложил права")
            _, out = self.box.doctor(self.proj)
            self.assertNotIn_("прав машинного контура", out, "находка про права осталась после --fix")
            # Рукописное в настройках --fix не трогает и выданного не задваивает.
            after = json.loads(read(self.settings))
            allow = after["permissions"]["allow"]
            self.assertIn("Bash(своё правило пользователя:*)", allow, "рукописное правило потерялось")
            self.assertEqual(allow.count("Bash(taskctl:*)"), 1, "выданное правило дописано вторым разом")
            self.assertTrue(after["hooks"]["PostToolUse"], "хуки пользователя потерялись")
        finally:
            write(self.settings, json.dumps(keep, ensure_ascii=False, indent=1))


class PermsCliTest(SandboxCase):
    """Проверка прав прямо модулем: свой HOME, доктор для неё не нужен."""

    def setUp(self):
        self.phome = self.box.root / ("phome-" + self._testMethodName)
        (self.phome / ".claude").mkdir(parents=True)
        self.settings = self.phome / ".claude" / "settings.json"

    def pcli(self, *args):
        return run([PY, str(self.box.dk / "tools" / "devkitctl" / "perms.py")] + list(args),
                   home=self.phome)

    def test_tool_allowed_whole_covers_the_list(self):
        # Инструмент, разрешённый целиком, покрывает перечень: звать чинить
        # исправное не за чем.
        write(self.settings, '{"permissions": {"allow": ["Bash", "Read", "Edit", "Write"]}}\n')
        rc, out = self.pcli()
        self.assertEqual(rc, 0, "разрешённый целиком Bash не покрыл перечень: %s" % out)

    def test_ask_on_machine_right_is_a_finding(self):
        # Переспрос это находка, и снимать его --fix не берётся: записанное
        # руками важнее перечня.
        write(self.settings, '{"permissions": {"allow": ["Bash", "Read", "Edit", "Write"],'
                             ' "ask": ["Bash(git:*)"]}}\n')
        rc, out = self.pcli("--fix")
        self.assertEqual(rc, 1, "переспрос на праве машинного контура прошёл мимо проверки: %s" % out)
        self.assertIn_("permissions.ask", out, "находка не назвала, где право отобрано")
        self.assertIn('"ask"', read(self.settings), "--fix вычистил переспрос пользователя")

    def test_deny_stays_and_added_rules_go_last(self):
        # Запрет остаётся как записан, запрещённое право в allow не уезжает
        # (иначе allow и deny спорили бы друг с другом), а дописанное ложится в
        # конец, не двигая рукописного. Проверка позиционная: членства в списке
        # мало, порядок в настройках значащий.
        head = ["Bash(своё первое:*)", "Bash(taskctl:*)", "Bash(своё второе:*)"]
        deny = ["Bash(rm:*)", "Bash(git:*)"]
        write(self.settings, json.dumps({"permissions": {"allow": list(head), "deny": list(deny)}},
                                        ensure_ascii=False, indent=2))
        rc, out = self.pcli("--fix")
        self.assertEqual(rc, 1, "запрет на праве машинного контура прошёл мимо проверки: %s" % out)
        self.assertIn_("permissions.deny", out, "находка не назвала запрет пользователя")
        data = json.loads(read(self.settings))
        allow = data["permissions"]["allow"]
        self.assertEqual(allow[:3], head, "рукописные правила уехали с места")
        self.assertEqual(data["permissions"]["deny"], deny, "запрет пользователя изменён")
        added = [r for r in perms.MACHINE_ALLOW if r not in head and r != "Bash(git:*)"]
        self.assertEqual(allow[3:], added, "дописанное легло не в конец или не в порядке перечня")

    def test_machine_without_settings(self):
        # Машина, на которой прав ещё не раскладывали: находка с командой
        # починки, --fix её закрывает, повторный --fix настроек уже не трогает.
        rc, out = self.pcli()
        self.assertEqual(rc, 1, "машина без настроек харнеса прошла проверку прав: %s" % out)
        rc, out = self.pcli("--fix")
        self.assertEqual(rc, 0, "--fix не разложил права на машине без настроек: %s" % out)
        rc, out = self.pcli()
        self.assertEqual(rc, 0, "после --fix проверка прав всё ещё находит нехватку: %s" % out)
        once = read(self.settings)
        self.pcli("--fix")
        self.assertEqual(read(self.settings), once, "повторный --fix переписал настройки")

    def test_worktree_only_checks(self):
        # Рубеж основного чекаута, тот же, что у определений агентов: из
        # worktree ветки задачи права только сверяются. Видны они каждой сессии
        # на машине сразу, и выписывать их себе непроверенной веткой цикл не
        # должен даже случайно.
        findings, fixed = perms.check(str(self.settings), True, "/nowhere/main-devkit")
        self.assertFalse(fixed, "права дописаны с непроверенной ветки: %s" % (fixed,))
        self.assertTrue(findings)
        self.assertIn("из основного чекаута", findings[0])
        self.assertIn("/nowhere/main-devkit/tools/devkitctl/devkitctl.py doctor --fix", findings[0])
        self.assertFalse(os.path.exists(str(self.settings)), "настройки заведены с непроверенной ветки")

    def test_broken_settings_are_not_touched(self):
        # Битые настройки: чинить их автоматике нечем, и трогать файл она не
        # берётся.
        write(self.settings, '{"permissions": ')
        rc, out = self.pcli("--fix")
        self.assertEqual(rc, 1, "битые настройки прошли проверку прав: %s" % out)
        self.assertIn_("не разбираются", out, "находка не назвала беду настроек")
        self.assertEqual(read(self.settings), '{"permissions": ', "--fix переписал битые настройки")


if __name__ == "__main__":
    unittest.main()
