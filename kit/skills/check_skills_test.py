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


class TestGoalCut(SkillTree):
    """DK-208: нарезка цели третьим скиллом, пробная нарезка в постановке,
    материализация списка кандидатов витком и сверка оценки с бюджетом."""

    CUT = ("## Список кандидатов\n\n- кандидат 1 (task, M, R=38). Первая задача.\n\n"
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
    фоновые задачи через десять минут). Правило живёт в board-batch и
    board-ship, синтетический каталог кладёт их в kit/skills/ рядом с корнем."""

    REASON = "headless-сессия добивает фоновое через десять минут"

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
        self.assertEqual(len(fails), 4)
        self.assertTrue(any("board-batch: спавн субагента не назван синхронным" in f
                            for f in fails), fails)
        self.assertTrue(any("board-ship: спавн субагента не назван синхронным" in f
                            for f in fails), fails)

    def test_fails_when_reason_missing(self):
        self.add_board_skills("Спавн синхронный, так надо")
        fails = check_skills.check_sync_spawn(self.root)
        self.assertEqual(len(fails), 2)
        self.assertTrue(all("синхронный спавн назван без причины" in f for f in fails), fails)

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


if __name__ == "__main__":
    unittest.main(verbosity=0)
