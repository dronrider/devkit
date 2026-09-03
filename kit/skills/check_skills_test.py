#!/usr/bin/env python3
"""Юниты самопроверки скиллов: каждая проверка на синтетическом каталоге
скиллов, без единого настоящего SKILL.md. Прогон на боевом kit/skills/ живёт в
самом check-skills.py (`python3 check-skills.py` без аргументов).
"""
import os
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import importlib.util
spec = importlib.util.spec_from_file_location(
    "check_skills", os.path.join(os.path.dirname(os.path.abspath(__file__)), "check-skills.py"))
check_skills = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check_skills)


FRONTMATTER = """---
name: %s
description: %s
---

%s
"""


class SkillTree(unittest.TestCase):
    """Каталог скиллов и правил во временной директории, по образцу
    kit/skills/ и корня репозитория рядом."""

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.here = os.path.join(self.root, "kit", "skills")
        os.makedirs(self.here)
        with open(os.path.join(self.root, "RULES.md"), "w", encoding="utf-8") as f:
            f.write("правила\n")
        with open(os.path.join(self.root, "RULES.board.md"), "w", encoding="utf-8") as f:
            f.write("правила доски\n")

    def add_skill(self, name, description="Звать, когда нужно.",
                 body="\n".join(["тело скилла"] * 15), frontmatter_name=None):
        d = os.path.join(self.here, name)
        os.makedirs(d)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write(FRONTMATTER % (frontmatter_name or name, description, body))

    def append(self, path, text):
        with open(os.path.join(self.root, path), "a", encoding="utf-8") as f:
            f.write(text)


class TestCheckSkills(SkillTree):
    def test_well_formed_skill_passes(self):
        self.add_skill("test-standard", body="RULES.md\n" + "\n".join(["тело"] * 12))
        fails, n = check_skills.check_skills(self.here)
        self.assertEqual(fails, [])
        self.assertEqual(n, 1)

    def test_missing_skill_md(self):
        os.makedirs(os.path.join(self.here, "empty-skill"))
        fails, n = check_skills.check_skills(self.here)
        self.assertEqual(n, 1)
        self.assertIn("empty-skill: нет SKILL.md, скилл сессия не подхватит", fails)

    def test_missing_frontmatter(self):
        d = os.path.join(self.here, "bare")
        os.makedirs(d)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("просто текст без frontmatter\n" + "тело\n" * 12)
        fails, _ = check_skills.check_skills(self.here)
        self.assertTrue(any("нет frontmatter" in f for f in fails))

    def test_name_mismatch(self):
        self.add_skill("board-task", frontmatter_name="board-taks")
        fails, _ = check_skills.check_skills(self.here)
        self.assertTrue(any("во frontmatter имя" in f for f in fails), fails)

    def test_empty_description(self):
        self.add_skill("goal-loop", description="")
        fails, _ = check_skills.check_skills(self.here)
        self.assertTrue(any("пустое описание" in f for f in fails), fails)

    def test_description_without_call_hint(self):
        self.add_skill("goal-start", description="Просто скилл без повода.")
        fails, _ = check_skills.check_skills(self.here)
        self.assertTrue(any("не говорит, когда скилл звать" in f for f in fails), fails)

    def test_description_accepts_english_form(self):
        self.add_skill("board-groom", description="Use this skill when grooming.")
        fails, _ = check_skills.check_skills(self.here)
        self.assertEqual(fails, [])

    def test_thin_body_fails(self):
        self.add_skill("test-standard", body="одна строка")
        fails, _ = check_skills.check_skills(self.here)
        self.assertTrue(any("тело скилла пустое" in f for f in fails), fails)

    def test_pycache_is_not_a_skill(self):
        os.makedirs(os.path.join(self.here, "__pycache__"))
        fails, n = check_skills.check_skills(self.here)
        self.assertEqual(n, 0)
        self.assertIn("скиллов не нашлось вовсе", fails)

    def test_no_skills_at_all(self):
        fails, n = check_skills.check_skills(self.here)
        self.assertEqual(n, 0)
        self.assertIn("скиллов не нашлось вовсе", fails)


class TestProceduralRules(SkillTree):
    def test_ok_when_rules_name_the_path(self):
        for name in ("board-task", "board-ship"):
            self.add_skill(name)
        self.add_skill("test-standard")
        self.append("RULES.board.md", "kit/skills/board-task/SKILL.md\nkit/skills/board-ship/SKILL.md\n")
        self.append("RULES.md", "kit/skills/test-standard/SKILL.md\n")
        fails = check_skills.check_procedural_rules(self.here, self.root)
        self.assertEqual(fails, [])

    def test_missing_skill_directory(self):
        self.add_skill("board-ship")
        self.append("RULES.board.md", "kit/skills/board-task/SKILL.md\nkit/skills/board-ship/SKILL.md\n")
        fails = check_skills.check_procedural_rules(self.here, self.root)
        self.assertTrue(any("board-task: процедура правил скиллом не заведена" in f for f in fails), fails)

    def test_rule_text_missing_path(self):
        for name in ("board-task", "board-ship", "test-standard"):
            self.add_skill(name)
        # RULES.board.md остаётся без указателей вовсе.
        fails = check_skills.check_procedural_rules(self.here, self.root)
        self.assertTrue(any("RULES.board.md не называет путь до скилла board-task" in f for f in fails), fails)
        self.assertTrue(any("RULES.md не называет путь до скилла test-standard" in f for f in fails), fails)


class TestGoalSplit(SkillTree):
    def add_goal_skill(self, name, extra="", mentions=""):
        d = os.path.join(self.here, name)
        os.makedirs(d)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: %s\ndescription: Звать, всегда.\n---\n\n%s\n%s\n"
                    % (name, mentions, extra))

    def test_split_kept_apart(self):
        self.add_goal_skill("goal-start", mentions="сосед goal-loop",
                            extra="## Разделы файла цели\n\nтекст")
        self.add_goal_skill("goal-loop", mentions="сосед goal-start",
                            extra="## Маркеры выхода\n\nтекст")
        fails = check_skills.check_goal_split(self.here)
        self.assertEqual(fails, [])

    def test_start_missing_sections(self):
        self.add_goal_skill("goal-start", mentions="goal-loop")
        self.add_goal_skill("goal-loop", mentions="goal-start", extra="## Маркеры выхода\n")
        fails = check_skills.check_goal_split(self.here)
        self.assertTrue(any("нет разделов файла цели" in f for f in fails), fails)

    def test_split_grown_back_together(self):
        self.add_goal_skill("goal-start", mentions="goal-loop",
                            extra="## Разделы файла цели\n\n## Виток\n")
        self.add_goal_skill("goal-loop", mentions="goal-start",
                            extra="## Маркеры выхода\n\n## Разделы файла цели\n")
        fails = check_skills.check_goal_split(self.here)
        self.assertTrue(any("тащит процедуру витка" in f for f in fails), fails)
        self.assertTrue(any("тащит постановку" in f for f in fails), fails)

    def test_missing_goal_skill(self):
        self.add_goal_skill("goal-loop", mentions="goal-start", extra="## Маркеры выхода\n")
        fails = check_skills.check_goal_split(self.here)
        self.assertTrue(any("goal-start: скилл режима цели не заведён" in f for f in fails), fails)


class TestSection(unittest.TestCase):
    """Вырезка раздела: на ней стоят проверки содержания, и обрезанный раздел
    выглядит так же, как целый."""

    TEXT = ("# Скилл\n\n## Первый\n\nтело первого\n\n```markdown\n## Внутри примера\n\n"
            "строка примера\n```\n\nхвост первого\n\n## Второй\n\nтело второго\n")

    def test_body_until_next_heading(self):
        self.assertIn("тело первого", check_skills.section(self.TEXT, "## Первый"))
        self.assertNotIn("тело второго", check_skills.section(self.TEXT, "## Первый"))

    def test_fenced_heading_is_not_the_end(self):
        body = check_skills.section(self.TEXT, "## Первый")
        self.assertIn("хвост первого", body)
        self.assertIn("## Внутри примера", body)

    def test_last_section_reaches_the_end(self):
        self.assertIn("тело второго", check_skills.section(self.TEXT, "## Второй"))

    def test_missing_heading_gives_nothing(self):
        self.assertEqual(check_skills.section(self.TEXT, "## Третий"), "")


class TestGoalCut(SkillTree):
    """DK-208: нарезка цели третьим скиллом, пробная нарезка в постановке,
    материализация списка кандидатов витком и сверка оценки с бюджетом."""

    # Пример списка идёт оградой с куском markdown внутри, как в боевом скилле:
    # свой «## Задачи цели» там лежит текстом примера, а не заголовком раздела.
    CUT = ("## Список кандидатов\n\nСтрока на кандидата:\n\n"
           "```markdown\n## Задачи цели\n\n- кандидат 1 (task, M, R=38). Первая задача.\n```\n\n"
           "Номер живёт до материализации.\n\n"
           "## Сверка оценки с остатком бюджета\n\nСумма цен против остатка.\n"
           "Порог в полтора раза, шире него выход wait-human.\n"
           "Чисел «Бюджета» цикл не правит ни в какую сторону.\n")
    START = "## Пробная нарезка\n\nСостав до старта цикла по скиллу goal-cut.\n"
    LOOP = "## Виток\n\nНарезка идёт по скиллу goal-cut.\n"

    def write_goal(self, cut=None, start=None, loop=None):
        for name, body in (("goal-cut", self.CUT if cut is None else cut),
                           ("goal-start", self.START if start is None else start),
                           ("goal-loop", self.LOOP if loop is None else loop)):
            if body == "":
                continue
            d = os.path.join(self.here, name)
            os.makedirs(d)
            with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
                f.write("---\nname: %s\ndescription: Звать, всегда.\n---\n\n%s\n" % (name, body))

    def test_three_skills_in_place(self):
        self.write_goal()
        self.assertEqual(check_skills.check_goal_cut(self.here), [])

    def test_cut_skill_absent(self):
        self.write_goal(cut="")
        self.assertEqual(check_skills.check_goal_cut(self.here),
                         ["goal-cut: скилл нарезки не заведён, правила состава цели терять некуда"])

    def test_cut_without_candidate_format(self):
        self.write_goal(cut=self.CUT.replace("## Список кандидатов", "## Состав"))
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("нет формата списка кандидатов" in f for f in fails), fails)

    def test_probe_section_lost(self):
        self.write_goal(start="## Порядок\n\nЗавести строку и файл, дальше goal-cut.\n")
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("нет пробной нарезки" in f for f in fails), fails)

    def test_probe_does_not_call_cut(self):
        self.write_goal(start="## Пробная нарезка\n\nПрикинуть состав своим порядком.\n")
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("пробная нарезка не зовёт goal-cut" in f for f in fails), fails)

    def test_turn_does_not_call_cut(self):
        self.write_goal(loop="## Виток\n\nНарезка своим порядком, кандидатов не знает.\n")
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("виток не зовёт goal-cut" in f for f in fails), fails)

    def test_check_section_lost(self):
        self.write_goal(cut="## Список кандидатов\n\n- кандидат 1 (task, M, R=38). Задача.\n")
        self.assertEqual(check_skills.check_goal_cut(self.here),
                         ["goal-cut: нет сверки оценки с бюджетом, "
                          "расхождение вылезет только на gate: over"])

    def test_check_without_marker(self):
        self.write_goal(cut=self.CUT.replace("выход wait-human", "нарезка едет дальше"))
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("не выходит маркером wait-human" in f for f in fails), fails)

    def test_check_without_threshold(self):
        self.write_goal(cut=self.CUT.replace("Порог в полтора раза, шире него ", ""))
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("ширина порога расхождения не названа" in f for f in fails), fails)

    def test_check_lets_budget_be_edited(self):
        self.write_goal(cut=self.CUT.replace(
            "Чисел «Бюджета» цикл не правит ни в какую сторону.",
            "Не хватило, значит поднять числа «Бюджета» самому."))
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("чисел «Бюджета» цикл не правит" in f for f in fails), fails)

    def test_next_section_ends_the_check(self):
        # Сверка кончается следующим заголовком: маркер из соседнего раздела за
        # неё не считается, иначе проверка держалась бы на слове где угодно.
        self.write_goal(cut="## Список кандидатов\n\n- кандидат 1 (task, M).\n\n"
                        "## Сверка оценки с остатком бюджета\n\nПорог в полтора раза, "
                        "чисел «Бюджета» цикл не правит.\n\n## Маркеры\n\nwait-human\n")
        fails = check_skills.check_goal_cut(self.here)
        self.assertTrue(any("не выходит маркером wait-human" in f for f in fails), fails)

    def test_example_does_not_cut_the_candidate_section(self):
        # Пример списка держит внутри ограды свой «## Задачи цели», и раздел
        # обязан дочитаться до конца: обрезанный на примере, он выглядит целым, а
        # всякая проверка его хвоста молча смотрит на первые пару строк.
        self.write_goal()
        body = check_skills.section(
            check_skills.read(os.path.join(self.here, "goal-cut", "SKILL.md")),
            "## Список кандидатов")
        self.assertIn("Номер живёт до материализации", body)
        self.assertNotIn("Сумма цен против остатка", body)

    def test_neighbours_absent_are_reported_elsewhere(self):
        # Постановки и витка нет вовсе: их пропажу называет check_goal_split, и
        # дублировать её здесь нечем.
        self.write_goal(start="", loop="")
        self.assertEqual(check_skills.check_goal_cut(self.here), [])


class TestGroom(SkillTree):
    def write_groom(self, body):
        d = os.path.join(self.here, "board-groom")
        os.makedirs(d)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: board-groom\ndescription: Звать, при разборе.\n---\n\n%s\n" % body)

    def test_full_groom_passes(self):
        self.write_groom(
            "## Входы\n\n- `/board-groom` один\n- `/board-groom` список\n- `/board-groom` весь\n\n"
            "## Исходы разбора\n\ntaskctl add --id, draft attach, draft defer, draft drop\n")
        fails = check_skills.check_groom(self.here)
        self.assertEqual(fails, [])

    def test_wrong_number_of_entries(self):
        self.write_groom("## Входы\n\n- `/board-groom` один\n\n## Исходы разбора\n\n"
                         "add --id, draft attach, draft defer, draft drop\n")
        fails = check_skills.check_groom(self.here)
        self.assertTrue(any("входов названо 1" in f for f in fails), fails)

    def test_missing_outcome_command(self):
        self.write_groom(
            "## Входы\n\n- `/board-groom` один\n- `/board-groom` список\n- `/board-groom` весь\n\n"
            "## Исходы разбора\n\nadd --id, draft attach, draft defer\n")
        fails = check_skills.check_groom(self.here)
        self.assertTrue(any("нет taskctl draft drop" in f for f in fails), fails)

    def test_groom_not_set_up(self):
        fails = check_skills.check_groom(self.here)
        self.assertIn("board-groom: скилл грумминга не заведён", fails)


class TestVerifyRunner(SkillTree):
    """DK-642: формулировка «прогоняет не автор правки» стоит в правилах доски
    и четырёх скиллах конвейера, и пропажа её из любого текста это находка."""

    def seed(self):
        for name in check_skills.VERIFY_TEXTS:
            self.add_skill(name, body="\n".join(
                ["сценарий " + check_skills.VERIFY_PHRASE] * 15))
        self.append("RULES.board.md", "сценарий %s\n" % check_skills.VERIFY_PHRASE)
        with open(os.path.join(self.root, "RULES.board.core.md"), "w",
                  encoding="utf-8") as f:
            f.write("ядро доски, сценарий %s\n" % check_skills.VERIFY_PHRASE)

    def test_phrase_everywhere_passes(self):
        self.seed()
        self.assertEqual(check_skills.check_verify_runner(self.here, self.root), [])

    def test_phrase_dropped_from_any_text_fails(self):
        self.seed()
        with open(os.path.join(self.here, "board-ship", "SKILL.md"), "w",
                  encoding="utf-8") as f:
            f.write(FRONTMATTER % ("board-ship", "Звать, когда нужно.",
                                   "\n".join(["сценарий гоняет кто-то"] * 15)))
        fails = check_skills.check_verify_runner(self.here, self.root)
        self.assertTrue(any("board-ship" in f for f in fails), fails)
        self.assertEqual(len(fails), 1, fails)

    def test_phrase_dropped_from_rules_fails(self):
        self.seed()
        with open(os.path.join(self.root, "RULES.board.md"), "w",
                  encoding="utf-8") as f:
            f.write("правила доски без формулировки\n")
        fails = check_skills.check_verify_runner(self.here, self.root)
        self.assertTrue(any("RULES.board.md" in f for f in fails), fails)

    def test_missing_text_named(self):
        self.seed()
        shutil.rmtree(os.path.join(self.here, "goal-loop"))
        fails = check_skills.check_verify_runner(self.here, self.root)
        self.assertTrue(any("goal-loop" in f and "текста нет" in f for f in fails), fails)

    def test_phrase_dropped_from_core_fails(self):
        self.seed()
        with open(os.path.join(self.root, "RULES.board.core.md"), "w",
                  encoding="utf-8") as f:
            f.write("ядро доски без формулировки\n")
        fails = check_skills.check_verify_runner(self.here, self.root)
        self.assertTrue(any("RULES.board.core.md" in f for f in fails), fails)


class TestTeam(SkillTree):
    NEIGHBOURS = "рядом board-task, board-ship и board-batch"
    CLASHES = "\n".join("**столкновение %d.** разбор" % i for i in range(1, 5))

    def write_team(self, body):
        d = os.path.join(self.here, "board-team")
        os.makedirs(d)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: board-team\ndescription: Звать, при нескольких руках.\n---\n\n%s\n" % body)

    def test_full_team_passes(self):
        self.write_team("%s\n\n## Захват задачи\n\ntaskctl move <ID> in-progress --push\n\n"
                        "## Столкновения\n\n%s\n" % (self.NEIGHBOURS, self.CLASHES))
        self.assertEqual(check_skills.check_team(self.here), [])

    def test_missing_capture_section(self):
        self.write_team("%s\n\n## Столкновения\n\n%s\n" % (self.NEIGHBOURS, self.CLASHES))
        fails = check_skills.check_team(self.here)
        self.assertTrue(any("нет раздела про захват" in f for f in fails), fails)

    def test_capture_without_push(self):
        self.write_team("%s\n\n## Захват задачи\n\ntaskctl move <ID> in-progress\n\n"
                        "## Столкновения\n\n%s\n" % (self.NEIGHBOURS, self.CLASHES))
        fails = check_skills.check_team(self.here)
        self.assertTrue(any("захват не объявляется пушем" in f for f in fails), fails)

    def test_lost_clash(self):
        three = "\n".join("**столкновение %d.** разбор" % i for i in range(1, 4))
        self.write_team("%s\n\n## Захват задачи\n\n--push\n\n## Столкновения\n\n%s\n"
                        % (self.NEIGHBOURS, three))
        fails = check_skills.check_team(self.here)
        self.assertTrue(any("разобрано столкновений 3" in f for f in fails), fails)

    def test_neighbour_not_named(self):
        self.write_team("рядом board-task и board-ship\n\n## Захват задачи\n\n--push\n\n"
                        "## Столкновения\n\n%s\n" % self.CLASHES)
        fails = check_skills.check_team(self.here)
        self.assertTrue(any("не называет соседа board-batch" in f for f in fails), fails)

    def test_team_not_set_up(self):
        fails = check_skills.check_team(self.here)
        self.assertIn("board-team: скилл командной работы не заведён", fails)


class TestProofread(SkillTree):
    """DK-184: скилл вычитки держит процедуру, пары, словарь и сторожевой
    корпус. Вычитка без материала бесполезна: пустой словарь помечает
    «поезд» как кандидат, а без корпуса правка пар уходит без страховки."""

    TERMS = ("поезд", "виток", "лестница", "накопитель",
             "сторожок", "заход", "ворота", "рубеж", "дорезка")
    PAIRS = "\n".join("## %d. пункт\n\nПлохо:\n\n> x\n\nХорошо:\n\n> y\n" % i
                       for i in range(1, 9))
    BAD_HALVES = "## Плохая половина\n\nне меньше 14 находок из 16\n"
    ETALON_HALVES = "## Эталонная половина\n\nне больше двух ложных\n"

    def write_proofread(self, pairs=None, dictionary=None, corpus=None, skill_body=None):
        d = os.path.join(self.here, "proofread")
        os.makedirs(d, exist_ok=True)
        body = skill_body or "\n".join(["тело скилла"] * 15)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: proofread\ndescription: Звать, при вычитке.\n---\n\n%s\n" % body)
        with open(os.path.join(d, "pairs.md"), "w", encoding="utf-8") as f:
            f.write(pairs if pairs is not None else self.PAIRS)
        with open(os.path.join(d, "dictionary.md"), "w", encoding="utf-8") as f:
            f.write(dictionary if dictionary is not None else " ".join(self.TERMS))
        with open(os.path.join(d, "corpus.md"), "w", encoding="utf-8") as f:
            f.write(corpus if corpus is not None else self.BAD_HALVES + "\n" + self.ETALON_HALVES)

    def test_full_proofread_passes(self):
        self.write_proofread()
        self.assertEqual(check_skills.check_proofread(self.here), [])

    def test_missing_skill_directory(self):
        fails = check_skills.check_proofread(self.here)
        self.assertIn("proofread: скилл вычитки не заведён", fails)

    def test_missing_pair_for_typology_point(self):
        pairs = "\n".join("## %d. пункт\n\nПлохо:\n\n> x\n\nХорошо:\n\n> y\n" % i
                          for i in range(1, 8))
        self.write_proofread(pairs=pairs)
        fails = check_skills.check_proofread(self.here)
        self.assertTrue(any("нет пары для пункта типологии 8" in f for f in fails), fails)

    def test_pairs_without_well_formed_block(self):
        pairs = "\n".join("## %d. пункт\n\nПлохо:\n\n> x\n" % i for i in range(1, 9))
        self.write_proofread(pairs=pairs)
        fails = check_skills.check_proofread(self.here)
        self.assertTrue(any("меньше восьми" in f for f in fails), fails)

    def test_dictionary_missing_term(self):
        self.write_proofread(dictionary="поезд виток лестница накопитель сторожок заход ворота рубеж")
        fails = check_skills.check_proofread(self.here)
        self.assertTrue(any("нет термина «дорезка»" in f for f in fails), fails)

    def test_corpus_missing_bad_half(self):
        self.write_proofread(corpus=self.ETALON_HALVES)
        fails = check_skills.check_proofread(self.here)
        self.assertTrue(any("нет плохой половины" in f for f in fails), fails)

    def test_corpus_missing_bad_threshold(self):
        corpus = "## Плохая половина\n\n## Эталонная половина\n\nне больше двух ложных\n"
        self.write_proofread(corpus=corpus)
        fails = check_skills.check_proofread(self.here)
        self.assertTrue(any("не зафиксирован порог плохой половины" in f for f in fails), fails)

    def test_corpus_missing_etalon_half(self):
        self.write_proofread(corpus=self.BAD_HALVES)
        fails = check_skills.check_proofread(self.here)
        self.assertTrue(any("нет эталонной половины" in f for f in fails), fails)


class TestRulesBacklink(SkillTree):
    def test_backlink_present(self):
        for name in ("board-task", "board-ship", "test-standard"):
            self.add_skill(name, body="выведен из RULES.board.md" if name != "test-standard"
                            else "выведен из RULES.md")
        fails = check_skills.check_rules_backlink(self.here)
        self.assertEqual(fails, [])

    def test_backlink_missing(self):
        self.add_skill("test-standard", body="без единой ссылки на правило")
        fails = check_skills.check_rules_backlink(self.here)
        self.assertTrue(any("test-standard: скилл не называет правило" in f for f in fails), fails)


class TestProse(SkillTree):
    """DK-523: скилл письма с горячим списком из пяти примет и четырьмя
    точками вызова. Три точки лежат файлами, четвёртая (правка README) названа
    в самом скилле."""

    HOT = "\n".join("%d. Примета %d." % (i, i) for i in range(1, 6))
    CALL = "python3 ~/projects/devkit/kit/skills/prose/prose.py sample --genre task"

    def add_prose(self, hot=None, readme=True):
        body = "## Горячий список\n\n%s\n\n## Кто зовёт\n\nПравка %s.\n" % (
            self.HOT if hot is None else hot, "README" if readme else "входной страницы")
        self.add_skill("prose", body=body)

    def add_points(self, groom=True, task=True, xhigh=True):
        self.add_skill("board-groom", body=self.CALL if groom else "без выборки")
        self.add_skill("board-task", body=self.CALL if task else "без выборки")
        d = os.path.join(self.root, "kit", "agents")
        os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, "exec-xhigh.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: exec-xhigh\ndescription: роль.\n---\n\n%s\n"
                    % (self.CALL if xhigh else "без выборки"))

    def test_полный_расклад_проходит(self):
        self.add_prose()
        self.add_points()
        self.assertEqual(check_skills.check_prose(self.root), [])

    def test_скилла_нет(self):
        fails = check_skills.check_prose(self.root)
        self.assertEqual(fails, ["prose: скилл письма не заведён"])

    def test_горячего_списка_нет(self):
        self.add_skill("prose", body="## Кто зовёт\n\nПравка README.\n")
        self.add_points()
        fails = check_skills.check_prose(self.root)
        self.assertTrue(any("нет горячего списка" in f for f in fails), fails)

    def test_примет_меньше_пяти(self):
        self.add_prose(hot="\n".join("%d. Примета %d." % (i, i) for i in range(1, 5)))
        self.add_points()
        fails = check_skills.check_prose(self.root)
        self.assertTrue(any("примет в горячем списке 4" in f for f in fails), fails)

    def test_readme_не_названа_точкой(self):
        self.add_prose(readme=False)
        self.add_points()
        fails = check_skills.check_prose(self.root)
        self.assertTrue(any("правка README не названа" in f for f in fails), fails)

    def test_точка_без_выборки(self):
        self.add_prose()
        self.add_points(groom=False)
        fails = check_skills.check_prose(self.root)
        self.assertEqual(fails, ["board-groom: не зовёт выборку эталонов, "
                                 "текст пишется без корпуса"])

    def test_каждая_точка_ловится_своя(self):
        self.add_prose()
        self.add_points(groom=False, task=False, xhigh=False)
        fails = check_skills.check_prose(self.root)
        self.assertEqual(len(fails), 3, fails)
        for who in ("board-groom", "board-task", "exec-xhigh"):
            self.assertTrue(any(f.startswith(who + ":") for f in fails), fails)

    def test_пропажа_файла_точки_не_дублируется(self):
        self.add_prose()
        fails = check_skills.check_prose(self.root)
        self.assertEqual(fails, [])


class TestRunAndMain(SkillTree):
    def test_run_reports_all_categories(self):
        # Пустой каталог валит сразу несколько проверок разом: свой скилл не
        # заведён, процедура не названа, разрез цели пуст, груминг не заведён.
        fails, n = check_skills.run(self.here, self.root)
        self.assertEqual(n, 0)
        self.assertTrue(any("board-task" in f for f in fails))
        self.assertTrue(any("goal-start" in f for f in fails))
        self.assertTrue(any("board-groom" in f for f in fails))
        self.assertTrue(any("board-team" in f for f in fails))


class TestBackgroundRule(SkillTree):
    """DK-165: правило «конец хода это возврат диспетчеру, фоновый прогон в
    foreground» во всех промптах исполнителей и ревьюверов, страховка диспетчера
    в board-batch и board-ship. Синтетическое дерево кладёт промпты в
    kit/agents/ и скиллы в kit/skills/ рядом с корнем."""

    PROMPTS = ("exec-low", "exec-medium", "exec-high", "exec-xhigh",
               "review-low", "review-medium", "review-high", "review-xhigh")

    def add_prompt(self, name, body):
        d = os.path.join(self.root, "kit", "agents")
        os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, name + ".md"), "w", encoding="utf-8") as f:
            f.write("---\nname: %s\ndescription: роль.\neffort: low\n---\n\n%s\n" % (name, body))

    def all_prompts(self, body):
        for name in self.PROMPTS:
            self.add_prompt(name, body)

    def test_passes_when_rule_everywhere(self):
        self.all_prompts("Конец хода это возврат диспетчеру. Ждать в foreground.")
        for skill in ("board-batch", "board-ship"):
            self.add_skill(skill, body="страховка: дождаться в foreground\n" + "\n".join(["тело"] * 12))
        self.assertEqual(check_skills.check_background_rule(self.root), [])

    def test_fails_when_prompt_misses_return_phrase(self):
        self.all_prompts("Ждать прогон в foreground.")
        self.assertEqual(check_skills.check_background_rule(self.root),
                         ["exec-low: не сказано, что конец хода это возврат диспетчеру",
                          "exec-medium: не сказано, что конец хода это возврат диспетчеру",
                          "exec-high: не сказано, что конец хода это возврат диспетчеру",
                          "exec-xhigh: не сказано, что конец хода это возврат диспетчеру",
                          "review-low: не сказано, что конец хода это возврат диспетчеру",
                          "review-medium: не сказано, что конец хода это возврат диспетчеру",
                          "review-high: не сказано, что конец хода это возврат диспетчеру",
                          "review-xhigh: не сказано, что конец хода это возврат диспетчеру"])

    def test_fails_when_prompt_misses_foreground(self):
        self.all_prompts("Конец хода это возврат диспетчеру.")
        self.assertTrue(all("не сказано ждать фоновый прогон в foreground" in f
                            for f in check_skills.check_background_rule(self.root)))

    def test_fails_when_prompt_absent(self):
        # Ни одного промпта нет: каждый сообщает об отсутствии.
        fails = check_skills.check_background_rule(self.root)
        self.assertEqual(len(fails), len(self.PROMPTS))
        self.assertTrue(all("промпт агента не найден" in f for f in fails))

    def test_fails_when_skill_misses_foreground(self):
        self.all_prompts("Конец хода это возврат диспетчеру. Ждать в foreground.")
        for skill in ("board-batch", "board-ship"):
            self.add_skill(skill, body="страховки нет, ждём как придётся\n" + "\n".join(["тело"] * 12))
        fails = check_skills.check_background_rule(self.root)
        self.assertEqual(len(fails), 2)
        self.assertTrue(any("board-batch: нет страховки диспетчера" in f for f in fails))
        self.assertTrue(any("board-ship: нет страховки диспетчера" in f for f in fails))

    def test_skill_missing_is_not_double_reported(self):
        # Скилл без SKILL.md отдельно ловится check_skills, здесь отрабатывает
        # молча: находка про страховку не дублирует пропажу скилла.
        self.all_prompts("Конец хода это возврат диспетчеру. Ждать в foreground.")
        self.assertEqual(check_skills.check_background_rule(self.root), [])


class TestSyncSpawn(SkillTree):
    """DK-314: спавн исполнителя и ревьювера назван синхронным вместе с
    причиной (headless-сессия дашборда кончает ход финальным текстом и добивает
    фоновые задачи через десять минут). DK-678 добавил к правилу рубеж, и скилл
    называет его вместе с причиной. Правило живёт в board-batch и board-ship,
    синтетический каталог кладёт их в kit/skills/ рядом с корнем."""

    REASON = ("headless-сессия добивает фоновое через десять минут, "
              "фоновый вызов отбивает рубеж check-background.py")

    def add_board_skills(self, body):
        for skill in ("board-batch", "board-ship"):
            self.add_skill(skill, body=body + "\n" + "\n".join(["тело"] * 12))

    def test_passes_when_sync_named_with_reason(self):
        self.add_board_skills("Спавн синхронный: " + self.REASON)
        self.assertEqual(check_skills.check_sync_spawn(self.root), [])

    def test_fails_when_spawn_not_named_sync(self):
        # Тексты до правки DK-314: спавн описан, синхронность не названа.
        self.add_board_skills("Спавн одним сообщением, чтобы субагенты шли параллельно")
        fails = check_skills.check_sync_spawn(self.root)
        self.assertEqual(len(fails), 6)
        self.assertTrue(any("board-batch: спавн субагента не назван синхронным" in f
                            for f in fails), fails)
        self.assertTrue(any("board-ship: спавн субагента не назван синхронным" in f
                            for f in fails), fails)

    def test_fails_when_reason_missing(self):
        self.add_board_skills("Спавн синхронный по рубежу check-background.py, так надо")
        fails = check_skills.check_sync_spawn(self.root)
        self.assertEqual(len(fails), 2)
        self.assertTrue(all("синхронный спавн назван без причины" in f for f in fails), fails)

    def test_fails_when_barrier_not_named(self):
        # DK-678: причина названа, рубеж нет. Текст с одной причиной переживёт
        # правку рубежа и разъедется с ней молча.
        self.add_board_skills("Спавн синхронный: headless-сессия добивает фоновое")
        fails = check_skills.check_sync_spawn(self.root)
        self.assertEqual(len(fails), 2)
        self.assertTrue(all("не назвал рубеж check-background.py" in f for f in fails), fails)

    def test_async_word_is_not_the_rule(self):
        # «Асинхронный» это отмена правила, а не его формулировка: подстрока
        # совпала бы, слово нет.
        self.add_board_skills("Спавн асинхронный: " + self.REASON)
        fails = check_skills.check_sync_spawn(self.root)
        self.assertEqual(len(fails), 2)
        self.assertTrue(all("не назван синхронным" in f for f in fails), fails)

    def test_skill_missing_is_not_double_reported(self):
        # Пропажу скилла отдельно ловит check_skills, здесь молчание.
        self.assertEqual(check_skills.check_sync_spawn(self.root), [])

    def test_run_reports_sync_spawn(self):
        self.add_board_skills("Спавн одним сообщением, чтобы субагенты шли параллельно")
        fails, _ = check_skills.run(self.here, self.root)
        self.assertTrue(any("спавн субагента не назван синхронным" in f for f in fails), fails)


class TestProofreadSpawn(SkillTree):
    """DK-548: вычитку делает субагент, и поднимает его тот, кто позвал скилл.
    Без этих слов сессия принимает скилл за фоновую задачу, отвечает «вычитка
    запущена» и кончает ход, оставив файл нетронутым."""

    GOOD = ("Субагента поднимает позвавший, синхронным спавном инструментом "
            "`Agent`, и ждёт отчёта в том же ходе.")

    def write_proofread(self, body):
        self.add_skill("proofread", body=body + "\n" + "\n".join(["тело"] * 12))

    def test_passes_when_caller_spawns_synchronously(self):
        self.write_proofread(self.GOOD)
        self.assertEqual(check_skills.check_proofread_spawn(self.here), [])

    def test_fails_when_spawn_not_named_sync(self):
        # Текст до правки DK-548: субагент назван, порядок его запуска нет.
        self.write_proofread("Вычитку гонит субагент со свежим контекстом, "
                             "инструмент `Agent`.")
        fails = check_skills.check_proofread_spawn(self.here)
        self.assertEqual(len(fails), 1)
        self.assertTrue(any("не назван синхронным" in f for f in fails), fails)

    def test_fails_when_tool_missing(self):
        self.write_proofread("Субагента поднимает позвавший, синхронным спавном.")
        fails = check_skills.check_proofread_spawn(self.here)
        self.assertEqual(len(fails), 1)
        self.assertTrue(any("не назван инструмент" in f for f in fails), fails)

    def test_async_word_is_not_the_rule(self):
        self.write_proofread("Спавн асинхронный, инструмент `Agent`.")
        fails = check_skills.check_proofread_spawn(self.here)
        self.assertEqual(len(fails), 1)
        self.assertTrue(any("не назван синхронным" in f for f in fails), fails)

    def test_skill_missing_is_not_double_reported(self):
        # Пропажу скилла ловит check_proofread, здесь молчание.
        self.assertEqual(check_skills.check_proofread_spawn(self.here), [])

    def test_run_reports_proofread_spawn(self):
        self.write_proofread("Вычитку гонит субагент со свежим контекстом.")
        fails, _ = check_skills.run(self.here, self.root)
        self.assertTrue(any("proofread: спавн субагента не назван синхронным" in f
                            for f in fails), fails)


class TestLiveReply(SkillTree):
    """DK-343: реплика человека доезжает до идущего витка, и правило реакции на
    неё тремя разрядами держится текстом goal-loop. Тут же зов оболочки: права
    машинного контура дают Bash(python3:*), под голый путь правила там нет, и
    headless-виток встаёт на запросе разрешения (LLD DK-136)."""

    LOOP = ("## Живая реплика\n\n"
            "Лежащую во «Входящих» строку вносит витку в контекст подхват hooks/chat-in.py.\n"
            "Убирает её запись «Журнала», разрядов у реплики три:\n\n"
            "- ответ и поправка действуют сразу;\n"
            "- стоп действует на границе шага, маркером wait-human;\n"
            "- смена направления ждёт конца задачи в конвейере.\n\n"
            "## Оболочка goal-run\n\n"
            "```bash\npython3 ~/projects/devkit/kit/skills/goal-loop/goal-run.py DK-100\n```\n")

    def write_loop(self, body=None):
        d = os.path.join(self.here, "goal-loop")
        os.makedirs(d)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: goal-loop\ndescription: Звать, всегда.\n---\n\n%s"
                    % (self.LOOP if body is None else body))

    def test_full_rule_passes(self):
        self.write_loop()
        self.assertEqual(check_skills.check_live_reply(self.here), [])

    def test_section_lost(self):
        # Текст до правки DK-343: про живую реплику в скилле нет ни слова.
        self.write_loop(self.LOOP.replace("## Живая реплика", "## Прочее"))
        fails = check_skills.check_live_reply(self.here)
        self.assertEqual(len(fails), 1)
        self.assertIn("нет раздела про живую реплику", fails[0])

    def test_reply_without_the_chat_hook(self):
        self.write_loop(self.LOOP.replace(
            "Лежащую во «Входящих» строку вносит витку в контекст подхват hooks/chat-in.py.",
            "Реплика лежит во «Входящих» до следующего витка."))
        fails = check_skills.check_live_reply(self.here)
        self.assertTrue(any("вносит в контекст витка подхват" in f for f in fails), fails)

    def test_immediate_grade_lost(self):
        self.write_loop(self.LOOP.replace("действуют сразу", "виток разберёт по месту"))
        fails = check_skills.check_live_reply(self.here)
        self.assertEqual(len(fails), 1, fails)
        self.assertIn("не названы действующими сразу", fails[0])

    def test_stop_grade_lost(self):
        self.write_loop(self.LOOP.replace("маркером wait-human", "как решит виток"))
        fails = check_skills.check_live_reply(self.here)
        self.assertEqual(len(fails), 1, fails)
        self.assertIn("стоп не выходит маркером wait-human", fails[0])

    def test_turn_grade_lost(self):
        self.write_loop(self.LOOP.replace("конца задачи в конвейере", "удобного момента"))
        fails = check_skills.check_live_reply(self.here)
        self.assertEqual(len(fails), 1, fails)
        self.assertIn("не ждёт конца задачи в конвейере", fails[0])

    def test_reply_without_journal(self):
        self.write_loop(self.LOOP.replace("запись «Журнала»", "запись витка"))
        fails = check_skills.check_live_reply(self.here)
        self.assertTrue(any("не оседает записью «Журнала»" in f for f in fails), fails)

    def test_shell_called_without_python3(self):
        # Текст до правки DK-343: оболочка звалась прямо путём из чекаута.
        self.write_loop(self.LOOP.replace(
            "python3 ~/projects/devkit", "~/projects/devkit"))
        fails = check_skills.check_live_reply(self.here)
        self.assertEqual(len(fails), 1)
        self.assertIn("зовётся путём без python3", fails[0])

    def test_shell_named_without_path_is_not_a_call(self):
        # Оболочка названа в прозе, а не позвана: находки тут нет.
        self.write_loop(self.LOOP + "\nВитки поднимает оболочка goal-run.py рядом.\n")
        self.assertEqual(check_skills.check_live_reply(self.here), [])

    def test_two_calls_give_one_finding(self):
        self.write_loop(self.LOOP.replace("python3 ~/projects/devkit", "~/projects/devkit")
                        + "\n```bash\n~/projects/devkit/kit/skills/goal-loop/goal-run.py DK-100 --say x\n```\n")
        self.assertEqual(len(check_skills.check_live_reply(self.here)), 1)

    def test_skill_missing_is_not_double_reported(self):
        # Пропажу goal-loop отдельно ловит check_goal_split, здесь молчание.
        self.assertEqual(check_skills.check_live_reply(self.here), [])

    def test_run_reports_live_reply(self):
        self.write_loop(self.LOOP.replace("## Живая реплика", "## Прочее"))
        fails, _ = check_skills.run(self.here, self.root)
        self.assertTrue(any("нет раздела про живую реплику" in f for f in fails), fails)


class ReviewSkill(SkillTree):
    """Скилл ревью: разделы, четыре уровня, три яруса, конфиг и определения."""

    SKILL = ("---\nname: review\ndescription: Ревью. Звать, когда ревьюят.\n---\n\n"
             "# Ревью\n\nбюджет в .devkit/review.conf\n\n"
             "рядом examples.md и threads.md\n\n## Вход\n\nпредмет\n\n## Порядок ревью\n\nшаги\n\n"
             "## Сколько ревью нужно\n\n| 0, пропуск |\n| 1, ворота |\n| 2, обычное |\n| 3, глубокое |\n\n"
             "## Вопросы по уровням\n\nвопросы\n\n"
             "## Замечания и три яруса\n\n- Блокирующее: правит.\n- Неблокирующее: отвечает.\n- Мелочь: сам.\n\n"
             "## Бюджет и стоп\n\nстоп\n\n## Разговор с автором\n\nкоротко\n\n"
             "## Ревью чужой задачи\n\nreview draft, потом вопрос человеку и парковка «ждёт "
             "подтверждения», за ней review publish\n\n"
             "## Отработка замечаний автором\n\nшаги\n\n## Второй круг\n\nдельта\n")
    CONF = ("level1 = 5 минут, 20 ходов\nlevel2 = 20 минут, 70 ходов\nlevel3 = 40 минут, 100 ходов\n"
            "critical_paths = tools\nchecks = 2: вопрос\npublish = confirm\npause = 20-60\n")
    AGENT = "---\nname: %s\neffort: high\n---\n\nТы ревьювер, читай скилл `review`.\n"
    EXEC = "---\nname: %s\neffort: high\n---\n\nТы исполнитель, замечания по разделу «Отработка замечаний автором».\n"

    def write_review(self, skill=None, conf=None, agent=None):
        d = os.path.join(self.here, "review")
        os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write(self.SKILL if skill is None else skill)
        for side in ("examples.md", "threads.md"):
            with open(os.path.join(d, side), "w", encoding="utf-8") as f:
                f.write("образцы\n")
        if conf is not False:
            os.makedirs(os.path.join(self.root, ".devkit"), exist_ok=True)
            with open(os.path.join(self.root, ".devkit", "review.conf"), "w", encoding="utf-8") as f:
                f.write(self.CONF if conf is None else conf)
        agents = os.path.join(self.root, "kit", "agents")
        os.makedirs(agents, exist_ok=True)
        for name in ("review-low", "review-medium", "review-high", "review-xhigh"):
            with open(os.path.join(agents, name + ".md"), "w", encoding="utf-8") as f:
                f.write((self.AGENT if agent is None else agent) % name)
        for name in ("exec-low", "exec-medium", "exec-high", "exec-xhigh"):
            with open(os.path.join(agents, name + ".md"), "w", encoding="utf-8") as f:
                f.write(self.EXEC % name)

    def test_full_review_passes(self):
        self.write_review()
        self.assertEqual(check_skills.check_review(self.here, self.root), [])

    def test_review_not_set_up(self):
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("не заведён" in f for f in fails), fails)

    def test_lost_section(self):
        self.write_review(skill=self.SKILL.replace("## Бюджет и стоп", "## Бюджет"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет раздела «Бюджет и стоп»" in f for f in fails), fails)

    def test_lost_level(self):
        self.write_review(skill=self.SKILL.replace("| 0, пропуск |\n", ""))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет уровня 0" in f for f in fails), fails)

    def test_lost_tier(self):
        self.write_review(skill=self.SKILL.replace("- Мелочь: сам.\n", ""))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("ярус «Мелочь» не описан" in f for f in fails), fails)

    def test_budget_hardcoded(self):
        self.write_review(skill=self.SKILL.replace(".devkit/review.conf", "20 минут"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("не отданы конфигу" in f for f in fails), fails)

    def test_conf_missing_and_key_lost(self):
        self.write_review(conf=False)
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("образца .devkit/review.conf" in f for f in fails), fails)
        self.write_review(conf=self.CONF.replace("critical_paths = tools\n", ""))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет ключа critical_paths" in f for f in fails), fails)

    def test_lost_foreign_review_section(self):
        self.write_review(skill=self.SKILL.replace("## Ревью чужой задачи", "## Чужое ревью"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет раздела «Ревью чужой задачи»" in f for f in fails), fails)

    def test_foreign_review_without_publish_step(self):
        # Раздел на месте, а команды публикации в нём нет: замечания опять
        # уедут в чужой MR разговором, мимо файла и мимо человека.
        self.write_review(skill=self.SKILL.replace("за ней review publish", "за ней публикация"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет «review publish»" in f for f in fails), fails)

    def test_foreign_review_without_parking(self):
        self.write_review(skill=self.SKILL.replace("«ждёт подтверждения»", "паузой"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет «ждёт подтверждения»" in f for f in fails), fails)

    def test_conf_without_publish_key(self):
        self.write_review(conf=self.CONF.replace("publish = confirm\n", ""))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет ключа publish" in f for f in fails), fails)

    def test_side_file_missing_or_unnamed(self):
        self.write_review()
        os.remove(os.path.join(self.here, "review", "threads.md"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("нет файла threads.md" in f for f in fails), fails)
        self.write_review(skill=self.SKILL.replace("рядом examples.md и threads.md", "рядом файлы"))
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("examples.md лежит рядом" in f for f in fails), fails)

    def test_executor_without_rework_section(self):
        self.write_review()
        with open(os.path.join(self.root, "kit", "agents", "exec-low.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: exec-low\neffort: low\n---\n\nисполнитель\n")
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("exec-low: исполнитель не знает" in f for f in fails), fails)

    def test_agent_not_calling_skill_or_forbidding_fix(self):
        self.write_review(agent="---\nname: %s\neffort: high\n---\n\nкод ревьювер не правит\n")
        fails = check_skills.check_review(self.here, self.root)
        self.assertTrue(any("не зовёт скилл review" in f for f in fails), fails)
        self.assertTrue(any("запрет править код" in f for f in fails), fails)


if __name__ == "__main__":
    unittest.main(verbosity=0)
