#!/usr/bin/env python3
"""Тесты рубежа машинных строк файла задачи (DK-1058): check-machine-lines.py.

Каждая проверка ниже кладёт «до» в originalFile события и «после» на диск
(как это делает харнес до вызова хука), запускает хук отдельным процессом и
смотрит на выход: 2 и подсказка на stderr это находка, 0 без вывода это
молчание, 0 с additionalContext на stdout это «сравнивать не с чем».
"""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-machine-lines.py")

# Дефис в имени скрипта не даёт обычный import (тот же приём, что в
# check_prose_test.py и в самом check-machine-lines.py).
_spec = importlib.util.spec_from_file_location("check_machine_lines", TOOL)
cml = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cml)


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


def write_event(path, before, tool_name="Edit"):
    """Событие PostToolUse записи файла задачи. before=None значит
    originalFile в ответе харнеса пуст (новый файл)."""
    tr = {"filePath": path}
    if before is not None:
        tr["originalFile"] = before
    event = {"hook_event_name": "PostToolUse", "tool_name": tool_name,
             "tool_input": {"file_path": path}, "tool_response": tr}
    return json.dumps(event, ensure_ascii=False)


class TaskFileCase(unittest.TestCase):
    """Временный docs/tasks/<ID>.md: путь должен пройти TASK_FILE_RE, иначе
    хук на событие вообще не смотрит."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.tasks = os.path.join(self.tmp, "docs", "tasks")
        os.makedirs(self.tasks)
        self.file = os.path.join(self.tasks, "DK-TEST.md")

    def tearDown(self):
        for root, _, files in os.walk(self.tmp, topdown=False):
            for n in files:
                try:
                    os.remove(os.path.join(root, n))
                except OSError:
                    pass
            try:
                os.rmdir(root)
            except OSError:
                pass

    def hook(self, before, after, path=None, tool_name="Edit"):
        path = path or self.file
        with open(path, "w", encoding="utf-8") as f:
            f.write(after)
        return run("--hook", input=write_event(path, before, tool_name))


class TestScope(TaskFileCase):
    def test_new_file_without_original_is_context_not_block(self):
        r = self.hook(None, "# DK-TEST\n\nпроза\n")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("additionalContext", r.stdout)
        self.assertIn("не с чем сверить", r.stdout)

    def test_prose_edit_passes_silently(self):
        before = "## Что происходит\n\nстарый текст без машинных строк.\n"
        after = "## Что происходит\n\nновый текст, слова другие целиком.\n"
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")
        self.assertEqual(r.stderr, "")

    def test_added_stage_line_passes_silently(self):
        # Новый этап дописан утилитой (agentctl stage) поверх старого: счёт
        # формата растёт, а не падает, и находки быть не должно.
        before = ("## Ход работы\n\n"
                  "- Разработка: субагент, 2026-09-19 10:00-10:05.\n")
        after = before + "- Ревью: субагент, 2026-09-19 11:00-11:05.\n"
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_path_outside_docs_tasks_is_ignored(self):
        other = os.path.join(self.tmp, "note.md")
        r = self.hook("- Стенд без колонки", "- Стенд без колонки испорчено", path=other)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_draft_path_is_ignored(self):
        drafts = os.path.join(self.tasks, "drafts")
        os.makedirs(drafts, exist_ok=True)
        draft = os.path.join(drafts, "DK-TEST.md")
        r = self.hook("- Стенд: 1", "испорчено", path=draft)
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_non_write_tool_is_ignored(self):
        event = json.dumps({"hook_event_name": "PostToolUse", "tool_name": "Bash",
                            "tool_input": {"command": "echo hi"},
                            "tool_response": {"stdout": "hi"}})
        r = run("--hook", input=event)
        self.assertEqual(r.returncode, 0, r.stderr)


class TestSectionRegressions(TaskFileCase):
    """Один случай на раздел перечня machine-lines.md: строка, которую правка
    ломает так, что регулярка её больше не узнаёт, отбивается находкой."""

    def assertBroken(self, before, after, category):
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn(category, r.stderr)
        self.assertIn(self.file, r.stderr)

    def test_dk_460_stage_quota_truncated(self):
        # «Ход работы»: срезанная квота увела хвост строки этапа (дату, время
        # и точку) за пределы STAGE_LINE_RE, тот же регресс, что нашла DK-460.
        before = ("## Ход работы\n\n"
                  "- Разработка: субагент sonnet/high по вердикту pick "
                  "(квота: week_all 27%, снимок 25м назад, сдвига нет), "
                  "2026-09-19 19:54-19:54.\n")
        after = ("## Ход работы\n\n"
                "- Разработка: субагент sonnet/high по вердикту pick "
                "(квота: week_all\n")
        self.assertBroken(before, after, "ход работы: этап")

    def test_dk_1050_verdict_colon_removed(self):
        # «Ревью»: снятое двоеточие у головы вердикта, тот же регресс, что
        # нашла DK-1050. Голова может нести sha (DK-812 меняет
        # REVIEW_VERDICT_HEAD_RE рядом),
        # образец берём тем, что реально совпадает с regex на диске, а не
        # своим текстом.
        verdict_re = cml.check_prose.REVIEW_VERDICT_HEAD_RE
        good = None
        for candidate in ("- Вердикт: без замечаний до a1b2c3d. коротко.",
                          "- Вердикт: без замечаний. коротко."):
            if verdict_re.match(candidate):
                good = candidate
                break
        self.assertIsNotNone(good, "ни один образец не совпал с REVIEW_VERDICT_HEAD_RE")
        broken = good.replace("Вердикт:", "Вердикт", 1)
        before = "## Ревью\n\nУровень 1 до abc123:\n\n%s\n" % good
        after = "## Ревью\n\nУровень 1 до abc123:\n\n%s\n" % broken
        self.assertBroken(before, after, "ревью: вердикт")

    def test_accept_barrier_colon_removed(self):
        before = "## DoD\n\n- барьер «prompt-test»: требуется сценарий.\n"
        after = "## DoD\n\n- барьер «prompt-test» требуется сценарий.\n"
        self.assertBroken(before, after, "приёмка: барьер")

    def test_fork_head_colon_removed(self):
        before = "## Развилки\n\n- «формат»: откуда берутся регулярки?\n"
        after = "## Развилки\n\n- «формат» откуда берутся регулярки?\n"
        self.assertBroken(before, after, "развилка: голова")

    def test_deploy_merge_hash_dropped(self):
        before = "## Ход работы\n\n- 2026-09-19 слито: a1b2c3d\n"
        after = "## Ход работы\n\n- 2026-09-19 слито:\n"
        self.assertBroken(before, after, "выкат: слияние")

    def test_stand_colon_removed(self):
        before = "## Проверка\n\n- Стенд: сценарий 12, k=5, 4/5.\n"
        after = "## Проверка\n\n- Стенд сценарий 12, k=5, 4/5.\n"
        self.assertBroken(before, after, "проверка: стенд")

    def test_goal_lap_marker_dropped(self):
        before = "## Журнал\n\n- 2026-09-19 19:54-19:55, виток цели; continue\n"
        after = "## Журнал\n\n- 2026-09-19 19:54-19:55, виток цели\n"
        self.assertBroken(before, after, "журнал: виток")


class TestFindingsHelper(unittest.TestCase):
    """decreases() напрямую: полнее ловит расхождение счётчиков без накладных
    расходов подпроцесса, полезно при добавлении новых форматов."""

    def test_no_before_and_after_difference_means_no_decrease(self):
        text = "проза без машинных строк, дважды одна и та же.\n"
        self.assertEqual(cml.decreases(text, text), [])

    def test_removed_review_link_line_is_a_decrease(self):
        before = "MR: https://example.invalid/mr/1\n\nдальше текст.\n"
        after = "дальше текст.\n"
        found = dict((name, (was, now)) for name, was, now in cml.decreases(before, after))
        self.assertIn("что происходит: ссылка ревью", found)
        self.assertEqual(found["что происходит: ссылка ревью"], (1, 0))

    def test_removed_mr_fate_line_is_a_decrease(self):
        before = "## Ход работы\n\n- MR слит, 2026-09-19.\n"
        after = "## Ход работы\n\n"
        found = dict((name, (was, now)) for name, was, now in cml.decreases(before, after))
        self.assertIn("ход работы: судьба MR", found)


if __name__ == "__main__":
    unittest.main()
