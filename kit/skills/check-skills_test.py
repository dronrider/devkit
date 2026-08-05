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


if __name__ == "__main__":
    unittest.main(verbosity=0)
