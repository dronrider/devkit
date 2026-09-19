#!/usr/bin/env python3
"""Тесты рубежа машинных строк файла задачи (DK-1058): check-machine-lines.py.

Каждая проверка ниже кладёт «до» в originalFile события и «после» на диск
(как это делает харнес до вызова хука), запускает хук отдельным процессом и
смотрит на выход: 2 и подсказка на stderr это находка, 0 без вывода это
молчание, 0 с additionalContext на stdout это добавка без блокировки.
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
        # Новый этап дописан утилитой (agentctl stage) поверх старого: старая
        # строка осталась дословно, а появление новой находкой не считается.
        before = ("## Ход работы\n\n"
                  "- Разработка: субагент, 2026-09-19 10:00-10:05.\n")
        after = before + "- Ревью: субагент, 2026-09-19 11:00-11:05.\n"
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_reordered_lines_pass_silently(self):
        # Перестановка строк без смены текста: то же множество строк в другом
        # порядке, находки быть не должно (DK-1058, замечание ревью 1).
        stage = "- Разработка: субагент, 2026-09-19 10:00-10:05.\n"
        mr = "- MR слит, 2026-09-19.\n"
        before = "## Ход работы\n\n" + stage + mr
        after = "## Ход работы\n\n" + mr + stage
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_trailing_space_added_passes_silently(self):
        # Замечание ревью 3: ключ множества у форматов без сырого вида был
        # сырой строкой, и добавленный хвостовой пробел на неизменном
        # содержимом читался как «строка пропала», хотя формат тот же.
        line = ("- Обкатка: 2026-09-19, свежее дерево abc1234, сценарий "
               "def5678, шагов 14")
        before = "## Проверка\n\n%s\n" % line
        after = "## Проверка\n\n%s \n" % line
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_trailing_space_added_on_crlf_file_passes_silently(self):
        # Тот же случай на файле с переносами CRLF: `\r` читается с диска
        # универсальным переводом строк (`open(..., encoding="utf-8")` без
        # `newline=`) и в сравниваемую строку не попадает вовсе, хвостовой
        # пробел остаётся тем же случаем, что и на файле с `\n`.
        line = ("- Обкатка: 2026-09-19, свежее дерево abc1234, сценарий "
               "def5678, шагов 14")
        before = "## Проверка\r\n\r\n%s\r\n" % line
        after = "## Проверка\r\n\r\n%s \r\n" % line
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout, "")

    def test_fork_sub_indent_change_is_still_a_finding(self):
        # Отступ слева у подстроки развилки значим (DK-550: отличает вложенный
        # пункт от примера тем же синтаксисом верхним уровнем), рубеж
        # снятие пробелов справа не распространяет на левый отступ.
        before = "## Развилки\n\n- «формат»: вопрос\n  - решает: человек\n"
        after = "## Развилки\n\n- «формат»: вопрос\n    - решает: человек\n"
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn("развилка: подстрока", r.stderr)

    def test_path_outside_docs_tasks_is_ignored(self):
        other = os.path.join(self.tmp, "note.md")
        r = self.hook("- Стенд: 1", "- Стенд без колонки испорчено", path=other)
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


class TestCheckProseUnavailable(unittest.TestCase):
    """Замечание ревью 2 (неблокирующее): загрузка check-prose.py обёрнута,
    и её отсутствие не роняет хук трассировкой."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.tasks = os.path.join(self.tmp, "docs", "tasks")
        os.makedirs(self.tasks)
        self.file = os.path.join(self.tasks, "DK-TEST.md")
        with open(self.file, "w", encoding="utf-8") as f:
            f.write("проза\n")
        # Своя копия дерева hooks/ без check-prose.py: hookio.py и сам хук
        # нужны, а файл-источник форматов отсутствует нарочно.
        self.tree = tempfile.mkdtemp()
        for name in ("hookio.py", "check-machine-lines.py"):
            with open(os.path.join(HERE, name), encoding="utf-8") as src:
                content = src.read()
            with open(os.path.join(self.tree, name), "w", encoding="utf-8") as dst:
                dst.write(content)
        self.tool = os.path.join(self.tree, "check-machine-lines.py")

    def tearDown(self):
        for d in (self.tmp, self.tree):
            for root, _, files in os.walk(d, topdown=False):
                for n in files:
                    try:
                        os.remove(os.path.join(root, n))
                    except OSError:
                        pass
                try:
                    os.rmdir(root)
                except OSError:
                    pass

    def test_missing_check_prose_is_a_context_note_not_a_crash(self):
        event = write_event(self.file, "старое")
        r = subprocess.run([sys.executable, self.tool, "--hook"], input=event,
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("Traceback", r.stderr)
        self.assertIn("additionalContext", r.stdout)
        self.assertIn("не выполнена", r.stdout)


class TestReviewerCases(TaskFileCase):
    """Пять случаев, которые ревью на дереве задачи прогнало вручную и нашло
    молчащими у сверки по счёту: смена значения внутри строки при целом
    формате проходила без находки. Каждый ловится точным сравнением строк по
    множеству с учётом кратности."""

    def assertBroken(self, before, after, category, quoted):
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn(category, r.stderr)
        self.assertIn(quoted, r.stderr)

    def test_case1_stage_quota_value_changed_tail_intact(self):
        line = ("- Разработка: субагент по вердикту pick (квота: week_all "
               "27%, снимок 25м назад, сдвига нет), 2026-09-19 19:54-19:54.")
        broken = line.replace("27%", "3%")
        self.assertBroken("## Ход работы\n\n%s\n" % line,
                          "## Ход работы\n\n%s\n" % broken,
                          "ход работы: этап", line)

    def test_case2_verdict_sha_and_tail_swapped(self):
        verdict_re = cml.check_prose.REVIEW_VERDICT_HEAD_RE
        good = None
        for candidate in ("- Вердикт: без замечаний до a1b2c3d. коротко.",
                          "- Вердикт: без замечаний. коротко."):
            if verdict_re.match(candidate):
                good = candidate
                break
        self.assertIsNotNone(good, "ни один образец не совпал с REVIEW_VERDICT_HEAD_RE")
        broken = good.replace("коротко.", "подменено, sha другой.")
        self.assertBroken("## Ревью\n\n%s\n" % good, "## Ревью\n\n%s\n" % broken,
                          "ревью: вердикт", good)

    def test_case3_rehearsal_commit_and_steps_swapped(self):
        line = ("- Обкатка: 2026-09-19, свежее дерево abc1234, сценарий "
               "def5678, шагов 14")
        broken = line.replace("abc1234", "zzzzzzz").replace("шагов 14", "шагов 99")
        self.assertBroken("## Проверка\n\n%s\n" % line,
                          "## Проверка\n\n%s\n" % broken,
                          "проверка: обкатка", line)

    def test_case4_review_finding_essence_rewritten(self):
        line = ("- [возврат: реализация, 2026-09-19] блокирует: сверка "
               "считает число строк, а не текст : исправлено")
        broken = ("- [возврат: реализация, 2026-09-19] блокирует: всё "
                 "отлично, замечаний нет : исправлено")
        self.assertBroken("## Ревью\n\n%s\n" % line, "## Ревью\n\n%s\n" % broken,
                          "ревью: замечание", line)

    def test_case5_mr_fate_date_swapped_one_to_one_count(self):
        line = "- MR слит, 2026-09-10."
        broken = "- MR слит, 2026-09-19."
        self.assertBroken("## Ход работы\n\n%s\n" % line,
                          "## Ход работы\n\n%s\n" % broken,
                          "ход работы: судьба MR", line)


class TestSectionRegressions(TaskFileCase):
    """По одному случаю на оставшиеся разделы перечня machine-lines.md,
    строка убрана целиком (не просто изменена внутри формата)."""

    def assertBroken(self, before, after, category):
        r = self.hook(before, after)
        self.assertEqual(r.returncode, 2, r.stderr)
        self.assertIn(category, r.stderr)
        self.assertIn(self.file, r.stderr)

    def test_dk_460_stage_line_dropped(self):
        # Регресс DK-460 в исходной формулировке: строка этапа пропадает
        # целиком (а не просто теряет число, см. TestReviewerCases выше).
        before = ("## Ход работы\n\n"
                  "- Разработка: субагент sonnet/high по вердикту pick "
                  "(квота: week_all 27%, снимок 25м назад, сдвига нет), "
                  "2026-09-19 19:54-19:54.\n")
        after = "## Ход работы\n\n"
        self.assertBroken(before, after, "ход работы: этап")

    def test_accept_barrier_dropped(self):
        before = "## DoD\n\n- барьер «prompt-test»: требуется сценарий.\n"
        after = "## DoD\n\n"
        self.assertBroken(before, after, "приёмка: барьер")

    def test_fork_head_dropped(self):
        before = "## Развилки\n\n- «формат»: откуда берутся регулярки?\n"
        after = "## Развилки\n\n"
        self.assertBroken(before, after, "развилка: голова")

    def test_deploy_merge_dropped(self):
        before = "## Ход работы\n\n- 2026-09-19 слито: a1b2c3d\n"
        after = "## Ход работы\n\n"
        self.assertBroken(before, after, "выкат: слияние")

    def test_stand_dropped(self):
        before = "## Проверка\n\n- Стенд: сценарий 12, k=5, 4/5.\n"
        after = "## Проверка\n\n"
        self.assertBroken(before, after, "проверка: стенд")

    def test_goal_lap_dropped(self):
        before = "## Журнал\n\n- 2026-09-19 19:54-19:55, виток цели; continue\n"
        after = "## Журнал\n\n"
        self.assertBroken(before, after, "журнал: виток")

    def test_goal_snapshot_dropped(self):
        before = "## Журнал\n\n- снимок 2026-08-03T12:00: week_all 12%, week_max 4%\n"
        after = "## Журнал\n\n"
        self.assertBroken(before, after, "журнал: снимок")

    def test_review_link_dropped(self):
        before = "MR: https://example.invalid/mr/1\n\nдальше текст.\n"
        after = "дальше текст.\n"
        self.assertBroken(before, after, "что происходит: ссылка ревью")


class TestMissingHelper(unittest.TestCase):
    """missing() напрямую: полнее ловит расхождение без накладных расходов
    подпроцесса, полезно при добавлении новых форматов."""

    def test_no_difference_means_nothing_missing(self):
        text = "проза без машинных строк, дважды одна и та же.\n"
        self.assertEqual(cml.missing(text, text), [])

    def test_reorder_of_identical_multiset_is_not_missing(self):
        a = "- Стенд: раз.\n- Стенд: два.\n"
        b = "- Стенд: два.\n- Стенд: раз.\n"
        self.assertEqual(cml.missing(a, b), [])

    def test_duplicate_line_removed_once_counts_as_one_missing(self):
        before = "- Стенд: раз.\n- Стенд: раз.\n"
        after = "- Стенд: раз.\n"
        found = cml.missing(before, after)
        self.assertEqual(len(found), 1)
        name, line, was, still = found[0]
        self.assertEqual((name, line, was, still), ("проверка: стенд", "- Стенд: раз.", 2, 1))

    def test_added_distinct_line_is_not_missing(self):
        before = "- Стенд: раз.\n"
        after = before + "- Стенд: два.\n"
        self.assertEqual(cml.missing(before, after), [])

    def test_trailing_whitespace_key_ignores_space_and_cr(self):
        # Ключ множества для форматов без сырого вида это ln.rstrip(): пробел
        # и `\r` на конце не делают строку другой (замечание ревью 3).
        before = "- Стенд: раз.\n"
        for after in ("- Стенд: раз. \n", "- Стенд: раз.\r\n", "- Стенд: раз. \r\n"):
            self.assertEqual(cml.missing(before, after), [], repr(after))

    def test_raw_category_key_keeps_left_indent_significant(self):
        # У «сырых» категорий ключ это исходная строка целиком: отступ слева
        # остаётся значимым, снятие пробелов справа его не заслоняет.
        before = "  - решает: человек\n"
        after = "    - решает: человек\n"
        found = cml.missing(before, after)
        self.assertEqual(len(found), 1)
        name, line, was, still = found[0]
        self.assertEqual((name, line, was, still),
                        ("развилка: подстрока", "  - решает: человек", 1, 0))


if __name__ == "__main__":
    unittest.main()
