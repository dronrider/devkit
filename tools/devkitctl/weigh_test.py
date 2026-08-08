#!/usr/bin/env python3
"""Вес резидента: пороги карманов, живой замер и находки доктора по ним.

Настоящих сессий самопроверка не поднимает, вместо claude в PATH лежит скрипт,
который отдаёт usage по той раскладке, какую видит в HOME и в рабочей
директории: ровно за неё платит и настоящий клиент. Пол 30 000, глобальная
точка 620, определения агентов 830, скиллы 540, цепочка правил проекта 12 300,
вместе целевой прогон это 44 290 из сценария.
"""
import os
import re
import shutil
import unittest
from pathlib import Path

from testenv import (PY, SandboxCase, executable, fake_home, git_init, harness, read, weigh,
                     write)

CLIENT = '''#!%s
import json
import os
import sys

calls = os.environ["WEIGH_CALLS"]
with open(calls, "a", encoding="utf-8") as f:
    f.write(os.getcwd() + "\\n")
home, cwd = os.environ["HOME"], os.getcwd()
t = 30000
t += 620 if os.path.isfile(os.path.join(home, ".claude", "CLAUDE.md")) else 0
t += 830 if os.path.isfile(os.path.join(home, ".claude", "agents", "exec-high.md")) else 0
t += 540 if os.path.isdir(os.path.join(home, ".claude", "skills", "board-batch")) else 0
# Цепочка правил проекта стоит по каждому файлу, который тонкий импортирует, и
# только пока файл лежит в рабочей директории: импорт наружу настоящий клиент не
# разворачивает, и платить за него не за что. Нарезанное ядро дешевле полного
# текста, как дешевле оно и в жизни.
price = {"AGENTS.md": 1400, "RULES.core.md": 300, "RULES.md": 10900, "RULES.board.md": 7000}
thin = os.path.join(cwd, "CLAUDE.md")
if os.path.isfile(thin):
    imports = os.environ.get("WEIGH_IMPORTS")
    for ln in open(thin, encoding="utf-8").read().split("\\n"):
        if not ln.startswith("@"):
            continue
        name = ln[1:]
        if imports:
            with open(imports, "a", encoding="utf-8") as f:
                f.write(name + "\\n")
        if "/" in name or not os.path.isfile(os.path.join(cwd, name)):
            continue
        t += price.get(name, 0)
# Вход гуляет от прогона к прогону, и разброс в выводе должен браться из
# настоящих чисел: каждый следующий вызов дороже предыдущего на свой квадрат.
n = len(open(calls, encoding="utf-8").read().split("\\n")) - 1
t += n * n * 10
print(json.dumps({"type": "result", "usage": {"input_tokens": 4,
                                              "cache_creation_input_tokens": 20000,
                                              "cache_read_input_tokens": t - 20004}}))
''' % PY

FLAT_CLIENT = '''#!%s
import json
import os
with open(os.environ["WEIGH_CALLS"], "a", encoding="utf-8") as f:
    f.write(os.getcwd() + "\\n")
print(json.dumps({"type": "result", "usage": {"input_tokens": 4,
                                              "cache_creation_input_tokens": 20000,
                                              "cache_read_input_tokens": 9996}}))
''' % PY

BROKEN_CLIENT = '''#!%s
import os
with open(os.environ["WEIGH_CALLS"], "a", encoding="utf-8") as f:
    f.write(os.getcwd() + "\\n")
print("ничего похожего на json")
''' % PY


def fmt(n):
    return "{:,}".format(int(n)).replace(",", " ")


def listing(paths):
    """Длина листинга: имя и описание каждого определения, как их видит харнес."""
    n = 0
    for p in paths:
        parts = read(p).split("---\n")
        head = parts[1] if len(parts) > 1 else ""
        for key in ("name", "description"):
            m = re.search(r"^%s: ?(.*)$" % key, head, re.M)
            n += len(m.group(1)) if m else 0
    return n


class ThresholdsTest(unittest.TestCase):
    """DK-029: пороги веса резидента (карман ядра, карман ядра доски, общий
    потолок) и тела скилла. Логика на пороги (evaluate_residency,
    evaluate_skill_body) отделена от сбора карманов с диска, поэтому юниты
    гоняются прямо на списке карманов, без раскладки на диске.
    """

    CLEAN = [("глобальная точка ~/.claude/CLAUDE.md", 300),
             ("RULES.core.md (импорт глобальной точки)", 5500),
             ("AGENTS.md проекта", 400),
             ("тонкий CLAUDE.md", 80),
             ("RULES.board.core.md (импорт тонкого файла)", 1500),
             ("листинг определений kit/agents/", 800),
             ("листинг скиллов", 900)]
    KW = dict(limit=6500, core_limit=5500, board_limit=1500)

    def bump(self, weights, label, delta):
        return [(l, c + delta if l == label else c) for l, c in weights]

    def test_on_the_budget_border(self):
        # На границе бюджета символ в символ находки нет: порог строгий
        # «больше», не «больше либо равно». Без карманов (харнес выключен)
        # проверять нечего.
        self.assertEqual(weigh.evaluate_residency(self.CLEAN, **self.KW), [])
        self.assertEqual(weigh.evaluate_residency([], **self.KW), [])

    def test_core_over_the_threshold(self):
        # Находка называет карман, вес в символах и токенах, порог и разбивку
        # (DK-029, сценарий проверки, шаг 2).
        found = weigh.evaluate_residency(
            self.bump(self.CLEAN, "RULES.core.md (импорт глобальной точки)", 1), **self.KW)
        self.assertEqual(len(found), 1, found)
        f = found[0]
        self.assertIn("ядро", f)
        self.assertIn("RULES.core.md (импорт глобальной точки)", f)
        self.assertIn("5 501 символ", f)
        self.assertIn("порог 5 500 символов", f)
        self.assertIn("разбивка:", f)
        self.assertIn("RULES.board.core.md", f)

    def test_board_core_over_the_threshold(self):
        # Ядро доски на символ выше порога: своя находка, не путается с ядром.
        found = weigh.evaluate_residency(
            self.bump(self.CLEAN, "RULES.board.core.md (импорт тонкого файла)", 1), **self.KW)
        self.assertEqual(len(found), 1, found)
        f = found[0]
        self.assertIn("ядро доски", f)
        self.assertIn("RULES.board.core.md (импорт тонкого файла)", f)
        self.assertIn("1 501 символ", f)
        self.assertIn("порог 1 500 символов", f)

    def test_total_over_the_ceiling(self):
        # Оба кармана в норме, а сумма выше общего потолка: находка про итог,
        # карманов по отдельности не касается.
        found = weigh.evaluate_residency(self.bump(self.CLEAN, "листинг скиллов", 100000), **self.KW)
        self.assertEqual(len(found), 1, found)
        self.assertIn("вес резидента devkit:", found[0])
        self.assertIn("потолок 6 500 токенов", found[0])

    def test_three_reasons_give_three_findings(self):
        messy = self.bump(self.CLEAN, "RULES.core.md (импорт глобальной точки)", 1)
        messy = self.bump(messy, "RULES.board.core.md (импорт тонкого файла)", 1)
        messy = self.bump(messy, "листинг скиллов", 100000)
        self.assertEqual(len(weigh.evaluate_residency(messy, **self.KW)), 3)

    def test_project_pocket_on_the_border(self):
        # Проектная часть это AGENTS.md проекта плюс тонкий файл, и считается она
        # суммой: два кармана порознь укладываются, вместе нет. Порог строгий
        # «больше», как и у карманов devkit (DK-190).
        self.assertEqual(weigh.evaluate_project_residency(self.CLEAN, project_limit=480), [])
        self.assertEqual(weigh.evaluate_project_residency([], project_limit=480), [])
        found = weigh.evaluate_project_residency(self.CLEAN, project_limit=479)
        self.assertEqual(len(found), 1, found)
        self.assertIn("проектная часть (AGENTS.md проекта, тонкий CLAUDE.md)", found[0])
        self.assertIn("480 символов", found[0])
        self.assertIn("порог 479 символов", found[0])
        self.assertIn("разбивка:", found[0])

    def test_project_pocket_ignores_devkit_pockets(self):
        # Разбухли карманы devkit, а не проекта: проекту чинить нечего, и
        # проектной находки нет.
        messy = self.bump(self.CLEAN, "RULES.core.md (импорт глобальной точки)", 100000)
        messy = self.bump(messy, "листинг скиллов", 100000)
        self.assertEqual(weigh.evaluate_project_residency(messy, project_limit=6000), [])

    def test_embedded_rules_are_not_the_project_pocket(self):
        # Вклейка правил лежит в AGENTS.md, но текст в ней devkit: карман
        # отдельный, и в проектный порог он не идёт.
        with_block = self.CLEAN + [("вклейка правил в AGENTS.md", 40000)]
        self.assertEqual(weigh.evaluate_project_residency(with_block, project_limit=6000), [])

    def test_skill_body(self):
        # На пороге молчит, на символ выше предлагает резать надвое.
        self.assertIsNone(weigh.evaluate_skill_body("test-skill", 9000, limit=9000))
        f = weigh.evaluate_skill_body("test-skill", 9001, limit=9000)
        self.assertIsNotNone(f)
        self.assertIn("test-skill", f)
        self.assertIn("9 001 символ", f)
        self.assertIn("порог 9 000 символов", f)
        self.assertIn("резать скилл надвое", f)

    def test_default_skill_body_limit(self):
        # Порог по умолчанию (DK-118): тот же потолок, что у резидента, только в
        # символах и округлённый вверх до сотен. Считается он от LIMIT, а не от
        # сегодняшних тел, поэтому и проверяется через LIMIT: разъехавшиеся числа
        # иначе заметит только читатель README.
        want = -(-round(weigh.LIMIT * weigh.CHARS_PER_TOKEN) // 100) * 100
        self.assertEqual(weigh.SKILL_BODY_LIMIT, want)
        self.assertEqual(want, 16000)
        self.assertIsNone(weigh.evaluate_skill_body("test-skill", weigh.SKILL_BODY_LIMIT))
        f = weigh.evaluate_skill_body("test-skill", weigh.SKILL_BODY_LIMIT + 1)
        self.assertIsNotNone(f)
        self.assertIn("порог 16 000 символов", f)


class KeychainTest(SandboxCase):
    """Связка ключей в слепке: без неё клиент под подменённым HOME отвечает «Not
    logged in», и замер не начинается вовсе. Гоняется на выдуманных HOME, чтобы
    обе ветки проверялись на любой платформе, а не только там, где связка есть.
    """

    def test_keychain_is_linked_not_copied(self):
        work = self.box.root / "keys"
        profile = harness.parse("cc.toml",
                                read(self.box.dk / "kit" / "harness" / "claude-code.toml"))
        src = work / "src"
        keys = src / weigh.KEYCHAIN_DIR
        (src / ".claude").mkdir(parents=True)
        keys.mkdir(parents=True)
        write(keys / "login.keychain-db", "не связка, а её место")
        # Связка на месте: в слепке симлинк на неё, и авторизация читается с
        # машины. Копия тут была бы и лишней, и опасной: связка живая.
        with fake_home(src):
            link = Path(weigh.build_home(str(src), str(work / "full"), str(self.box.dk),
                                         profile, True)) / weigh.KEYCHAIN_DIR
        self.assertTrue(link.is_symlink(), str(link))
        self.assertEqual(os.path.realpath(str(link)), os.path.realpath(str(keys)))
        self.assertTrue((link / "login.keychain-db").is_file())
        # Связки нет (не macOS): слепок остаётся прежним, пустого Library в нём
        # не заводится.
        bare = work / "bare"
        (bare / ".claude").mkdir(parents=True)
        with fake_home(bare):
            dst = Path(weigh.build_home(str(bare), str(work / "nokeys"), str(self.box.dk),
                                        profile, True))
        self.assertFalse((dst / "Library").exists())


class MeasureTest(SandboxCase):
    """Живой замер резидента подложным клиентом. Проверки идут по одной
    раскладке, как их гонял sh-раннер: собирать её на каждый шаг дорого, поэтому
    в именах порядковый номер. Копию devkit шаги трогают только на время своей
    проверки и возвращают как было, и сверка слепка это подтверждает.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        cls.wtmp = box.root / "weigh"
        cls.whome = cls.wtmp / "home"
        (cls.whome / ".claude" / "agents").mkdir(parents=True)
        (cls.whome / ".claude" / "skills").mkdir(parents=True)
        for f in (box.dk / "kit" / "agents").glob("*.md"):
            shutil.copy(str(f), str(cls.whome / ".claude" / "agents" / f.name))
        for d in (box.dk / "kit" / "skills").iterdir():
            if d.is_dir():
                shutil.copytree(str(d), str(cls.whome / ".claude" / "skills" / d.name))
        write(cls.whome / ".claude" / "CLAUDE.md",
              "# Глобальные правила\n\n@~/.claude/CLAUDE_RULES.md\n")
        os.symlink(str(box.dk / "RULES.md"), str(cls.whome / ".claude" / "CLAUDE_RULES.md"))
        write(cls.whome / ".claude" / "settings.json", '{"hooks": {"Stop": []}}\n')
        cls.proj = git_init(cls.wtmp / "proj")
        rc, out = box.dkctl_run("new", "--no-board", "-C", str(cls.proj), home=cls.whome)
        assert rc == 0, "weigh: проект не подключился: %s" % out
        cls.thin_gen = read(cls.proj / "CLAUDE.md")
        cls.calls = cls.wtmp / "calls"
        cls.imports = cls.wtmp / "imports"
        cls.wbin = cls.wtmp / "bin"
        cls.wbin.mkdir()
        cls.client = cls.wbin / "claude"
        executable(cls.client, CLIENT)
        cls.wpath = "%s:%s" % (cls.wbin, box.sys)
        # Ожидаемые числа считаются в самой самопроверке: длина каждого кармана
        # и сумма по ним. Разъедься список карманов с дизайном, и сумма
        # разойдётся тоже.
        cls.wagents = listing(sorted((box.dk / "kit" / "agents").glob("*.md")))
        cls.wskills = listing(sorted((box.dk / "kit" / "skills").glob("*/SKILL.md")))
        cls.wtotal = (len(read(cls.whome / ".claude" / "CLAUDE.md"))
                      + len(read(box.dk / "RULES.md"))
                      + len(read(cls.proj / "AGENTS.md"))
                      + len(read(cls.proj / "CLAUDE.md"))
                      + cls.wagents + cls.wskills)

    def weigh_run(self, *args, **kw):
        write(self.calls, "")
        env = {"WEIGH_CALLS": self.calls}
        if kw.pop("imports", False):
            write(self.imports, "")
            env["WEIGH_IMPORTS"] = self.imports
        root = kw.pop("root", self.proj)
        return self.box.dkctl_run("weigh", "-C", str(root), *args,
                                  home=self.whome, path=kw.pop("path", self.wpath), env=env)

    def calls_made(self):
        return [ln for ln in read(self.calls).split("\n") if ln]

    def test_01_base_run(self):
        rc, out = self.weigh_run("--limit", "20000", imports=True)
        self.assertEqual(rc, 0, "weigh не прошёл при потолке выше замера: %s" % out)
        # Файлы правил лежат в самой директории замера, и тонкий файл зовёт их
        # голым именем: импорт наружу клиент не разворачивает, и правила до
        # целевого прогона не доезжают вовсе.
        names = [ln for ln in read(self.imports).split("\n") if ln]
        self.assertIn("RULES.md", names, "тонкий файл замера зовёт правила не голым именем")
        self.assertIn("AGENTS.md", names, "тонкий файл замера не зовёт AGENTS.md")
        self.assertFalse([n for n in names if "/" in n], "тонкий файл замера импортирует наружу")
        # Базовый прогон идёт под слепком без раскладки devkit: пол и ничего
        # сверх него. Пропусти сборщик слепка любую из трёх частей раскладки, и
        # число вырастет.
        self.assertIn_("прогон 1: без раскладки 30 010, с раскладкой 44 330, разница 14 320", out,
                       "первая пара прогонов посчитана не так")
        self.assertRegex(out, r"прогон 3: .*разница 14 400", "третья пара прогонов посчитана не так")
        self.assertIn_("замер: 14 360 токенов, разброс 80 (0,56%)", out, "замер или разброс не те")
        self.assertEqual(len(self.calls_made()), 6, "три повтора это шесть прогонов")
        self.assertNotIn(str(self.proj), read(self.calls),
                         "замер гонял прогон прямо в чекауте проекта")
        # Карманы: файл считается один раз, даже когда приезжает двумя дорогами
        # (глобальной точкой и импортом тонкого файла).
        self.assertEqual(len([ln for ln in out.split("\n") if "RULES.md" in ln]), 1,
                         "RULES.md посчитан не одним карманом: %s" % out)
        self.assertRegex(out, r"глобальная точка ~/\.claude/CLAUDE\.md .*%s"
                         % fmt(len(read(self.whome / ".claude" / "CLAUDE.md"))),
                         "карман глобальной точки посчитан не так")
        self.assertRegex(out, r"итого .*%s" % fmt(self.wtotal), "итог по карманам не сошёлся")
        self.assertRegex(out, r"листинг определений kit/agents/ .*%s" % fmt(self.wagents),
                         "листинг агентов не сошёлся")
        self.assertRegex(out, r"листинг скиллов .*%s" % fmt(self.wskills),
                         "листинг скиллов не сошёлся")
        # Коэффициент этого прогона и расхождение расчёта с замером считаются от
        # тех же чисел: символы карманов делятся на замер, расчёт идёт по
        # коэффициенту дизайна.
        coef = ("%.2f" % (self.wtotal / 14360.0)).replace(".", ",")
        self.assertIn_("коэффициент этого прогона: %s символа на токен" % coef, out,
                       "коэффициент не сошёлся")
        calc = int(round(self.wtotal / 2.45 - 14360))
        self.assertRegex(out, r"расчёт против замера: .*%s токенов" % fmt(calc),
                         "расхождение расчёта с замером не сошлось")

    def test_02_limit(self):
        # Потолок: равный замеру не превышен, на токен ниже уже превышен, и это
        # код 1.
        rc, out = self.weigh_run("--limit", "14360")
        self.assertEqual(rc, 0, "потолок вровень с замером принят за превышение: %s" % out)
        self.assertIn_("потолок резидента 14 360 токенов, запас 0", out,
                       "нет строки про запас до потолка")
        rc, out = self.weigh_run("--limit", "14359")
        self.assertEqual(rc, 1, "замер выше потолка не дал кода 1: %s" % out)
        self.assertIn_("потолок резидента 14 359 токенов превышен на 1", out,
                       "нет строки про превышение потолка")

    def test_03_default_limit(self):
        # Без --limit берётся тот потолок, что в бюджете дизайна, и сегодняшний
        # резидент выше него. Дефолт тут не украшение, этим же порогом доктор
        # будет красить находку.
        rc, out = self.weigh_run()
        self.assertEqual(rc, 1, "замер выше потолка по умолчанию не дал кода 1: %s" % out)
        self.assertIn_("потолок резидента 6 500 токенов превышен на 7 860", out,
                       "потолок по умолчанию не 6 500 токенов из бюджета дизайна")

    def test_04_single_run(self):
        # Повторов один: разброса нет, а прогонов ровно два.
        rc, out = self.weigh_run("--runs", "1", "--limit", "20000")
        self.assertEqual(rc, 0, "weigh с одним повтором не прошёл: %s" % out)
        self.assertIn_("замер: 14 320 токенов, разброс 0", out, "одиночный повтор посчитан не так")
        self.assertEqual(len(self.calls_made()), 2, "один повтор это два прогона")

    def test_05_ninth_agent_grows_the_listing(self):
        # Листинг растёт ровно на имя и описание, и вместе с ним растёт итог.
        # Иначе экономия перетекала бы в карман, который никто не мерит.
        desc = "описание" * 25
        probe = "---\nname: probe-agent\ndescription: %s\neffort: low\n---\n\nтело определения\n" % desc
        write(self.box.dk / "kit" / "agents" / "probe-agent.md", probe)
        write(self.whome / ".claude" / "agents" / "probe-agent.md", probe)
        try:
            _, out = self.weigh_run("--limit", "20000")
            grown = self.wagents + 11 + len(desc)
            self.assertRegex(out, r"листинг определений kit/agents/ .*%s" % fmt(grown),
                             "листинг агентов не вырос на новое определение")
            self.assertRegex(out, r"итого .*%s" % fmt(self.wtotal + 11 + len(desc)),
                             "итог по карманам не вырос на новое определение")
        finally:
            (self.box.dk / "kit" / "agents" / "probe-agent.md").unlink()
            (self.whome / ".claude" / "agents" / "probe-agent.md").unlink()

    def test_06_stale_layout_refuses(self):
        # Мерить по вчерашней раскладке значит соврать молча: замер отказан,
        # находка названа, ни одного прогона не заказано.
        moved = self.wtmp / "board-groom"
        shutil.move(str(self.whome / ".claude" / "skills" / "board-groom"), str(moved))
        try:
            rc, out = self.weigh_run("--limit", "20000")
            self.assertEqual(rc, 2, "weigh мерил по несвежей раскладке: %s" % out)
            self.assertRegex(out, r"не разложен скилл в[^\n]*board-groom",
                             "отказ не называет находку раскладки")
            self.assertIn_("замер отменён", out, "отказ не объяснён")
            self.assertEqual(self.calls_made(), [], "при отказе всё же гонялись прогоны")
        finally:
            shutil.move(str(moved), str(self.whome / ".claude" / "skills" / "board-groom"))

    def test_07_handwritten_thin_file_refuses(self):
        # Тронутый руками тонкий файл это чужая раскладка, и замер по ней тоже
        # не о чем.
        write(self.proj / "CLAUDE.md", self.thin_gen + "@../nope.md\n")
        try:
            rc, out = self.weigh_run("--limit", "20000")
            self.assertEqual(rc, 2, "weigh мерил по правленому руками тонкому файлу: %s" % out)
            self.assertEqual(self.calls_made(), [],
                             "при отказе по файлам правил всё же гонялись прогоны")
        finally:
            write(self.proj / "CLAUDE.md", self.thin_gen)

    def test_08_no_client_in_path(self):
        # Гнать замер нечем, и это не находка веса, а отказ.
        rc, out = self.weigh_run(path=str(self.box.sys))
        self.assertEqual(rc, 2, "weigh без claude в PATH не отказался: %s" % out)
        self.assertIn_("claude не в PATH", out, "отказ без claude не объяснён")

    def test_09_zero_runs(self):
        # Мерить нечего, и это отказ, а не деление на ноль.
        rc, out = self.weigh_run("--runs", "0")
        self.assertEqual(rc, 2, "weigh с нулём повторов не отказался: %s" % out)

    def test_10_embed_harness_pockets(self):
        # Правила вклеены в сам AGENTS.md, и он посчитан целиком, только двумя
        # карманами: рукописная часть проекта отдельно, вклейка отдельно (по
        # проектной части считается порог, а текст вклейки не проектный, DK-190).
        # Тонкого файла и отдельных файлов правил у такого харнеса нет: сложи их
        # сверху вклейки, и вес правил уехал бы в сумму дважды.
        profile = self.box.dk / "kit" / "harness" / "embed-tool.toml"
        write(profile, '[detect]\n\n[rules]\nmode = "embed"\n\n[delegate]\nmode = "none"\n'
                       '\n[hooks]\n\n[quota]\n')
        write(self.whome / ".devkit" / "harness.local", 'enabled = ["embed-tool"]\n')
        plain = read(self.proj / "AGENTS.md")
        try:
            self.box.dkctl_run("doctor", "--fix", "-C", str(self.proj),
                               home=self.whome, path=self.wpath)
            self.assertRegex(read(self.proj / "AGENTS.md"), r"(?m)^<!-- devkit:rules begin",
                             "правила не вклеились в AGENTS.md embed-харнеса")
            with fake_home(self.whome):
                _, profile_obj = weigh.active_profile(str(self.proj), str(self.box.dk))
                pockets = weigh.pockets(str(self.proj), str(self.box.dk), profile_obj)
            labels = dict(pockets)
            self.assertEqual(labels.get("AGENTS.md проекта", 0)
                             + labels.get("вклейка правил в AGENTS.md", 0),
                             len(read(self.proj / "AGENTS.md")),
                             "AGENTS.md с вклейкой посчитан не целиком: %s" % (pockets,))
            # Рукописная часть это файл до вклейки плюс пустая строка, которую
            # генератор ставит перед маркером начала.
            self.assertEqual(labels.get("AGENTS.md проекта"), len(plain.rstrip("\n")) + 2,
                             "в проектный карман попал текст вклейки: %s" % (pockets,))
            self.assertFalse([l for l in labels if "RULES" in l],
                             "файлы правил посчитаны сверх вклейки: %s" % (pockets,))
            self.assertFalse([l for l in labels if "тонкий" in l],
                             "у embed-харнеса нашёлся тонкий файл: %s" % (pockets,))
            self.assertFalse([l for l in labels if "листинг определений" in l],
                             "харнесу без своих субагентов посчитаны определения агентов")
            self.assertEqual(labels.get("листинг скиллов"), self.wskills,
                             "листинг скиллов у embed-харнеса не сошёлся: %s" % (pockets,))
        finally:
            profile.unlink()
            (self.whome / ".devkit" / "harness.local").unlink()
            write(self.proj / "AGENTS.md", plain)
            write(self.proj / "CLAUDE.md", self.thin_gen)

    def test_11_board_project(self):
        # В замер едут оба файла правил, и второй это тот самый RULES.board.md,
        # из-за которого замер занижался. Пропусти его копию в директорию
        # замера, и правила доски снова не доедут до целевого прогона.
        bproj = git_init(self.wtmp / "bproj")
        (bproj / "docs").mkdir(exist_ok=True)
        rc, out = self.box.dkctl_run("new", "--no-board", "-C", str(bproj), home=self.whome)
        self.assertEqual(rc, 0, "weigh: проект с доской не подключился: %s" % out)
        write(bproj / "docs" / "TASKS.md", "# Задачи\n\nПрефикс: WG\n")
        self.box.dkctl_run("doctor", "--fix", "-C", str(bproj), home=self.whome, path=self.wpath)
        self.assertRegex(read(bproj / "CLAUDE.md"), r"(?m)^@.*RULES\.board\.md$",
                         "тонкий файл проекта с доской не зовёт правила доски")
        rc, out = self.weigh_run("--runs", "1", "--limit", "30000", root=bproj, imports=True)
        self.assertEqual(rc, 0, "weigh не прошёл на проекте с доской: %s" % out)
        names = [ln for ln in read(self.imports).split("\n") if ln]
        self.assertIn("RULES.board.md", names, "правила доски не доехали до директории замера")
        self.assertFalse([n for n in names if "/" in n], "проект с доской импортирует наружу")
        self.assertIn_("замер: 21 320 токенов", out, "правила доски не попали в замер")
        self.assertIn_("RULES.board.md (импорт тонкого файла)", out,
                       "правила доски не посчитаны карманом")

    def test_12_cut_core(self):
        # Резидентно то, что импортирует тонкий файл, а не полный текст. Глубину
        # claude-code объявляет ядром, и как только RULES.core.md появляется, за
        # импортом переезжает и карман.
        core = self.box.dk / "RULES.core.md"
        write(core, "ядро правил, короткий текст\n")
        try:
            self.box.dkctl_run("doctor", "--fix", "-C", str(self.proj),
                               home=self.whome, path=self.wpath)
            self.assertIn("RULES.core.md", read(self.proj / "CLAUDE.md"),
                          "тонкий файл не переехал на нарезанное ядро")
            rc, out = self.weigh_run("--runs", "1", "--limit", "20000")
            self.assertEqual(rc, 0, "weigh не прошёл на нарезанном ядре: %s" % out)
            self.assertRegex(out, r"RULES\.core\.md \(импорт тонкого файла\) .*%s"
                             % fmt(len(read(core))), "карман считает не нарезанное ядро")
            self.assertNotRegex(out, r"(?m)^  RULES\.md \(импорт",
                                "в карманах остался полный текст правил")
            # Под ту же глубину собирается и тонкий файл директории замера, иначе
            # целевой прогон платил бы за полный текст, а расчёт считал бы ядро.
            self.assertIn_("замер: 3 720 токенов", out,
                           "директория замера собрана не под глубину проекта")
            # residency_findings читает карманы с диска и применяет ту же
            # пороговую логику, юниты которой прогнаны выше. Машина под этот
            # шаг своя, разложенная под уже нарезанное ядро: HOME замера нарочно
            # тяжёлый (полный текст правил симлинком), и мерить порог по нему
            # значило бы мерить фикстуру, а не раскладку.
            rhome = self.box.make_home(self.wtmp / "resid-home")
            with fake_home(rhome):
                found = weigh.residency_findings(str(self.proj), str(self.box.dk))
            self.assertEqual(found, [], "нарезанное ядро проекта без доски дало находку веса")
            write(core, "ядро правил, %s\n" % ("я" * 5600))
            with fake_home(rhome):
                found = weigh.residency_findings(str(self.proj), str(self.box.dk))
            self.assertTrue([f for f in found if "вес резидента, ядро (RULES.core.md" in f],
                            "разбухшее ядро на диске не дало находки: %s" % (found,))
        finally:
            core.unlink()
            self.box.dkctl_run("doctor", "--fix", "-C", str(self.proj),
                               home=self.whome, path=self.wpath)
            self.assertNotIn("RULES.core.md", read(self.proj / "CLAUDE.md"),
                             "тонкий файл не вернулся на полный текст правил")

    def test_13_skill_findings(self):
        # Тело скилла выше порога называет скилл и предлагает резать надвое
        # (DK-029, сценарий проверки, шаг 4), тело в норме молчит про него.
        probe = self.box.dk / "kit" / "skills" / "oversized-probe"
        write(probe / "SKILL.md",
              "---\nname: oversized-probe\ndescription: тестовый скилл.\n---\n" + "т" * 16500)
        try:
            with fake_home(self.whome):
                found = weigh.skill_findings(str(self.box.dk))
            self.assertTrue([f for f in found if "тело скилла oversized-probe: 16 500 символов, "
                                                 "порог 16 000 символов; резать скилл надвое" in f],
                            "разбухший скилл на диске не дал находки: %s" % (found,))
            self.assertFalse([f for f in found if "тело скилла board-groom" in f],
                             "тело скилла в пределах порога дало находку")
        finally:
            shutil.rmtree(str(probe))

    def test_14_live_skills_are_under_the_limit(self):
        # Живые скиллы репозитория под своим порогом (DK-118). Проверка синтетики
        # выше говорит, что находка работает, а эта что чинить по ней нечего:
        # иначе доктор в чекауте devkit красный всегда, и его находки перестают
        # что-либо значить.
        with fake_home(self.whome):
            found = weigh.skill_findings(str(self.box.dk))
        self.assertEqual(found, [], "скиллы репозитория выше порога тела")

    def test_15_zero_difference(self):
        # Оба прогона пары стоят одинаково: разница нулевая, мерить было нечего.
        # Печатать по такой разнице карманы и коэффициент значило бы выдать шум
        # за замер.
        executable(self.client, FLAT_CLIENT)
        try:
            rc, out = self.weigh_run("--limit", "20000")
            self.assertEqual(rc, 2, "нулевая разница принята за замер: %s" % out)
            self.assertIn_("разница вышла неположительной", out, "нулевая разница не объяснена")
            self.assertNotIn_("коэффициент этого прогона", out,
                              "по нулевой разнице всё же посчитан коэффициент")
        finally:
            executable(self.client, CLIENT)

    def test_16_broken_client_answer(self):
        # Замер падает вслух, а не выдаёт разницу из нулей.
        executable(self.client, BROKEN_CLIENT)
        try:
            rc, out = self.weigh_run("--limit", "20000")
            self.assertEqual(rc, 2, "weigh проглотил ответ клиента без usage: %s" % out)
            self.assertIn_("не JSON", out, "поломка прогона не названа")
        finally:
            executable(self.client, CLIENT)


class DoctorResidencyTest(SandboxCase):
    """Порог веса резидента и тела скилла в докторе (DK-029). Карманы общие для
    всех проектов devkit, находка не проектная, и doctor() отдаёт её только для
    самого чекаута devkit (root совпадает с DEVKIT), не для каждого
    подключённого проекта. Фикстура это отдельная копия devkit, которая сама
    себе и DEVKIT команды, и проверяемый ею проект: своя, чтобы мутации карманов
    (ядро, ядро доски, тело скилла) не задевали остальные проверки.
    """

    SKILL = "---\nname: tiny-skill\ndescription: тестовый скилл.\n---\n"

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        box = cls.box
        cls.rdk = box.root / "resid" / "devkit"
        cls.rdk.mkdir(parents=True)
        for d in ("tools", "kit", "hooks"):
            shutil.copytree(str(box.dk / d), str(cls.rdk / d))
        # Дока других утилит доктору тут не нужна, а её битые ссылки на docs/lld
        # были бы шумом поверх карманов резидента, которые и проверяются.
        for md in cls.rdk.rglob("*.md"):
            rel = md.relative_to(cls.rdk).parts
            if rel[:2] not in (("kit", "skills"), ("kit", "agents")):
                md.unlink()
        shutil.rmtree(str(cls.rdk / "kit" / "skills"))
        write(cls.rdk / "kit" / "skills" / "tiny-skill" / "SKILL.md", cls.SKILL + "т" * 200)
        write(cls.rdk / "RULES.core.md", "# ядро\n\nтекст ядра.\n")
        write(cls.rdk / "RULES.board.core.md", "# ядро доски\n\nтекст ядра доски.\n")
        write(cls.rdk / "RULES.md", "# правила\n")
        write(cls.rdk / "RULES.board.md", "# правила доски\n")
        write(cls.rdk / "AGENTS.md", "# devkit\n\nтестовый проект.\n")
        git_init(cls.rdk)
        write(cls.rdk / "docs" / "TASKS.md", "# Задачи\n\nПрефикс: RD\n")
        cls.rdkctl = cls.rdk / "tools" / "devkitctl" / "devkitctl.py"
        cls.rdhome = box.root / "resid" / "home"
        box.make_home(cls.rdhome)
        for name in list((cls.rdhome / ".claude" / "skills").iterdir()):
            shutil.rmtree(str(name))
        shutil.copytree(str(cls.rdk / "kit" / "skills" / "tiny-skill"),
                        str(cls.rdhome / ".claude" / "skills" / "tiny-skill"))
        box.global_rules(cls.rdhome, cls.rdk)
        cls.rddoc("--fix")
        write(cls.rdk / ".devkit" / "deploy.local", "deploy = echo ok\ntest = echo ok\n")

    @classmethod
    def rddoc(cls, *args):
        return cls.box.dkctl_run("doctor", *(list(args) + ["-C", str(cls.rdk)]),
                                 dkctl=cls.rdkctl, home=cls.rdhome)

    def machine_skill(self, text):
        write(self.rdk / "kit" / "skills" / "tiny-skill" / "SKILL.md", text)
        write(self.rdhome / ".claude" / "skills" / "tiny-skill" / "SKILL.md", text)

    def test_1_clean_checkout_prints_the_pockets(self):
        # Доктор печатает вес по карманам и находок по весу не даёт, код
        # возврата 0 (DK-029, сценарий проверки, шаг 1).
        rc, out = self.rddoc()
        self.assertEqual(rc, 0, "чистый самопроверочный чекаут devkit дал находки: %s" % out)
        self.assertRegex(out, r"(?m)^вес резидента devkit по карманам",
                         "doctor не печатает вес резидента")
        self.assertIn_("RULES.core.md", out, "в таблице карманов нет ядра")
        self.assertRegex(out, r"(?m)^  итого", "в таблице карманов нет итоговой строки")

    def test_2_oversized_skill_body(self):
        self.machine_skill(self.SKILL + "т" * 16500)
        rc, out = self.rddoc()
        self.assertEqual(rc, 1, "разбухший скилл не поднял код возврата: %s" % out)
        self.assertIn_("тело скилла tiny-skill: 16 500 символов, порог 16 000 символов; "
                       "резать скилл надвое", out, "нет находки про разбухшее тело скилла")
        self.machine_skill(self.SKILL + "т" * 200)
        rc, out = self.rddoc()
        self.assertEqual(rc, 0, "возврат тела скилла к норме не очистил находку: %s" % out)

    def test_3_oversized_core(self):
        write(self.rdk / "RULES.core.md", "# ядро\n\n" + "я" * 5600)
        try:
            _, out = self.rddoc()
            self.assertIn_("вес резидента, ядро (RULES.core.md", out,
                           "разбухшее ядро не дало находки")
        finally:
            write(self.rdk / "RULES.core.md", "# ядро\n\nтекст ядра.\n")

    def test_4_oversized_board_core(self):
        # Находка не путает ядро доски с ядром.
        write(self.rdk / "RULES.board.core.md", "# ядро доски\n\n" + "д" * 1600)
        try:
            _, out = self.rddoc()
            self.assertIn_("вес резидента, ядро доски (RULES.board.core.md", out,
                           "разбухшее ядро доски не дало находки")
        finally:
            write(self.rdk / "RULES.board.core.md", "# ядро доски\n\nтекст ядра доски.\n")

    def test_5_total_over_the_ceiling(self):
        # Оба кармана в бюджете, а сумма выше общего потолка (разбухла листингом
        # определений kit/agents/): находка про итог, ядра она не касается.
        probe = "---\nname: probe-agent\ndescription: %s\neffort: low\n---\n\nтело\n" % ("о" * 15000)
        write(self.rdk / "kit" / "agents" / "probe-agent.md", probe)
        write(self.rdhome / ".claude" / "agents" / "probe-agent.md", probe)
        try:
            _, out = self.rddoc()
            self.assertRegex(out, r"(?m)^вес резидента devkit: .* токенов",
                             "разбухший общий вес не дал находки")
            self.assertNotIn_("вес резидента, ядро ", out,
                              "общий потолок задел карман ядра, хотя тот в бюджете")
        finally:
            (self.rdk / "kit" / "agents" / "probe-agent.md").unlink()
            (self.rdhome / ".claude" / "agents" / "probe-agent.md").unlink()
        rc, out = self.rddoc()
        self.assertEqual(rc, 0, "возврат листинга агентов к норме не очистил находку: %s" % out)

    def test_6_devkit_pockets_are_not_a_project_finding(self):
        # Доктор подключённого проекта печатает вес его резидента (карманы те же,
        # смотрит на них проект), а находок devkit не выдаёт: ни разбухшего тела
        # скилла, ни его карманов. Чинить их в проекте нечем (DK-029, DK-190).
        self.machine_skill(self.SKILL + "т" * 16500)
        try:
            other = git_init(self.box.root / "resid" / "otherproj")
            _, out = self.box.dkctl_run("doctor", "-C", str(other),
                                        dkctl=self.rdkctl, home=self.rdhome)
            self.assertIn_("вес резидента проекта otherproj по карманам", out,
                           "доктор проекта не напечатал вес его резидента")
            self.assertNotIn_("тело скилла", out,
                              "доктор чужого проекта нашёл разбухший скилл devkit")
            self.assertNotIn_("вес резидента devkit", out,
                              "доктор чужого проекта судит карманы devkit")
        finally:
            self.machine_skill(self.SKILL + "т" * 200)

    def test_7_heavy_project_rules(self):
        # Тяжёлая проектная часть резидента: рукописный тонкий файл проекта,
        # оставшийся от подключения до переезда на AGENTS.md. Находка называет
        # карман, его вес и порог, а тонкая раскладка про вес молчит (DK-190).
        proj = git_init(self.box.root / "resid" / "heavyproj")
        thin = proj / "CLAUDE.md"
        write(thin, "# правила проекта\n\nкороткий текст\n")
        _, out = self.box.dkctl_run("doctor", "-C", str(proj),
                                    dkctl=self.rdkctl, home=self.rdhome)
        self.assertNotIn_("вес резидента, проектная часть", out,
                          "тонкая раскладка проекта дала находку веса")
        write(thin, "# правила проекта\n\n" + "п" * 6000)
        _, out = self.box.dkctl_run("doctor", "-C", str(proj),
                                    dkctl=self.rdkctl, home=self.rdhome)
        self.assertIn_("вес резидента, проектная часть (тонкий CLAUDE.md): 6 019 символов", out,
                       "разбухший файл правил проекта не дал находки веса")
        self.assertIn_("порог 6 000 символов", out, "находка веса проекта не назвала порог")


if __name__ == "__main__":
    unittest.main()
