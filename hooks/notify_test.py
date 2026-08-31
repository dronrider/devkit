#!/usr/bin/env python3
"""Уведомитель сессии: разбор события, выбор бэкенда, окно троттлинга и фокус
окна юнитами, а следом прогон целиком, где хук зовётся процессом со своим HOME,
стабом отправителя и стабом опроса фокуса.
"""
import importlib.util
import io
import json
import os
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hookio  # noqa: E402  таблица разборщиков лежит рядом с уведомителем

spec = importlib.util.spec_from_file_location(
    "notify", os.path.join(os.path.dirname(os.path.abspath(__file__)), "notify.py"))
notify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notify)


def event(**kw):
    base = {"hook_event_name": "Notification", "cwd": "/Users/x/projects/devkit-dk-034",
            "session_id": "f07df579-b9ca"}
    base.update(kw)
    return base


def sess(**kw):
    """Событие claude-code разобранным, как его видит уведомитель: своего
    разбора у него больше нет, форма приходит из hookio."""
    return hookio.parse_session(hookio.DEFAULT, event(**kw))



# Маркеры заказа дашборда в окружении прогона уводят стенд: DEVKIT_NO_FOCUS
# гасит опрос фокуса, а DEVKIT_TASK дописывает задачу в заголовок. Набор падал
# ровно так, будучи запущен из сессии, поднятой дашбордом, и предмет проверки
# тут ни при чём. Окружение чистится на весь модуль, а не в каждом стенде:
# заводить эту оговорку заново на каждый новый стенд никто не станет.
DROP_ENV = ("DEVKIT_TASK", "DEVKIT_TMUX", "DEVKIT_NO_FOCUS")
_clean_env = None


def setUpModule():
    global _clean_env
    keep = {k: v for k, v in os.environ.items() if k not in DROP_ENV}
    _clean_env = mock.patch.dict(os.environ, keep, clear=True)
    _clean_env.start()


def tearDownModule():
    if _clean_env is not None:
        _clean_env.stop()


class TestParseEvent(unittest.TestCase):
    def test_action_reasons(self):
        for kind, label in (("permission_prompt", "нужно разрешение"),
                            ("agent_needs_input", "нужен ответ"),
                            ("elicitation_dialog", "диалог MCP"),
                            ("idle_prompt", "ждёт ввода")):
            key, title, body, level = notify.parse_event(
                sess(notification_type=kind, message="Claude ждёт"))
            self.assertEqual(key, kind)
            self.assertEqual(title, "devkit-dk-034: %s" % label)
            self.assertEqual(body, "Claude ждёт")
            # Сессия упёрлась в вопрос, и это повод подключиться, а не сводка.
            self.assertEqual(level, notify.LOUD)

    def test_silent_reasons(self):
        for kind in ("auth_success", "elicitation_complete", "elicitation_response", ""):
            self.assertIsNone(notify.parse_event(sess(notification_type=kind)))

    def test_turn_done_is_loud(self):
        key, title, body, level = notify.parse_event(sess(hook_event_name="Stop"))
        self.assertEqual((key, title, body), (notify.TURN_DONE,
                                              "devkit-dk-034: ход закончен", ""))
        self.assertEqual(level, notify.LOUD)

    def test_turn_done_title_says_what_happened(self):
        # Заголовок называет суть, а не служебное слово: «ход закончен» не
        # говорит человеку ничего (замечание пользователя по снимку). Вся суть
        # уехала в заголовок, и телом она вторым разом не повторяется.
        title, body = notify.parse_event(sess(
            hook_event_name="Stop",
            last_assistant_message="Готово, тесты зелёные\nдальше детали"))[1:3]
        self.assertEqual(title, "devkit-dk-034: Готово, тесты зелёные")
        # Суть уехала в заголовок, и телом она не повторяется: там подробности.
        self.assertEqual(body, "дальше детали")

    def test_turn_done_heading_keeps_the_details(self):
        # Реплика с заголовком markdown: сверху «Итог», разбор строкой ниже.
        title, body = notify.parse_event(sess(
            hook_event_name="Stop",
            last_assistant_message="## Итог\nСтрока DK-9 заведена и запушена."))[1:3]
        self.assertEqual(title, "devkit-dk-034: Итог")
        self.assertEqual(body, "Строка DK-9 заведена и запушена.")

    def test_turn_done_title_is_short_and_body_carries_the_rest(self):
        # Живой случай снимка: реплика с рангом, файлом и коммитом. В заголовок
        # едет первая часть предложения, остальное телом.
        said = ("Строка заведена и запушена: **DK-509**, тип `bug`, P2, ранг 36 "
                "(25+3+1+5+2), цена S, файл задачи `docs/tasks/DK-509.md`, "
                "коммит `6e2125b0`. `taskctl lint` молчит.")
        title, body = notify.parse_event(sess(
            hook_event_name="Stop", last_assistant_message=said))[1:3]
        self.assertEqual(title, "devkit-dk-034: Строка заведена и запушена")
        self.assertTrue(body.startswith("DK-509, тип bug"), body)
        for mark in ("*", "`"):
            self.assertNotIn(mark, title + body)
        self.assertLessEqual(len(body), notify.BODY_LIMIT)
        # Режется тело по концу предложения, а не посреди слова.
        self.assertTrue(body.endswith(".") or body.endswith("..."), body)

    def test_turn_done_empty_message_keeps_bare_title(self):
        # Пустая реплика оставляет прежний вид баннера: тело пустое, ломать
        # тут нечего.
        title, body = notify.parse_event(sess(
            hook_event_name="Stop", last_assistant_message=""))[1:3]
        self.assertEqual(title, "devkit-dk-034: ход закончен")
        self.assertEqual(body, "")

    def test_card_has_no_markup_in_any_reason(self):
        # Вид один на все роды: заголовок называет событие, тело идёт без
        # разметки. Реплику агент пишет markdown, и звёздочки с обратными
        # кавычками в карточке видны как есть (замечание пользователя).
        said = "Готово: **DK-9**, файл `docs/tasks/DK-9.md`, ссылка [доска](http://x)."
        cards = [
            notify.parse_event(sess(notification_type="permission_prompt", message=said)),
            notify.parse_event(sess(hook_event_name="Stop", last_assistant_message=said)),
            notify.parse_event(sess(hook_event_name="SubagentStop", agent_type="exec-low",
                                    last_assistant_message=said)),
            notify.parse_event(sess(hook_event_name="Stop", error="server_error",
                                    last_assistant_message=said)),
        ]
        for _, title, body, _ in cards:
            for mark in ("**", "`", "](", "[", "*"):
                self.assertNotIn(mark, title, title)
                self.assertNotIn(mark, body, body)
            self.assertIn("доска", title + body)
            self.assertLessEqual(len(body), notify.BODY_LIMIT)

    def test_body_is_cut_by_sentence(self):
        # Длинное тело режется по концу предложения, а не посреди слова.
        said = ("Первое предложение про строку доски. Второе предложение про файл "
                "задачи и коммит. Третье предложение про прогон, оно уже лишнее и "
                "в карточку не влезает никак, потому что тело кончается раньше.")
        body = notify.parse_event(sess(notification_type="idle_prompt", message=said))[2]
        self.assertLessEqual(len(body), notify.BODY_LIMIT)
        self.assertTrue(body.endswith("."), body)
        self.assertNotIn("Третье предложение", body)
        self.assertTrue(body.startswith("Первое предложение"), body)

    def test_title_names_the_task_of_the_order(self):
        # Груминг и конвейер дашборд поднимает в общем чекауте на main, задачу
        # там не называют ни имя дерева, ни ветка. Заказ её называет
        # переменной, и в заголовке она нужна: при пяти агентах «devkit» не
        # разобрать.
        with mock.patch.dict(os.environ, {"DEVKIT_TASK": "DK-509"}):
            title = notify.parse_event(sess(
                hook_event_name="Stop", cwd="/Users/x/projects/devkit",
                last_assistant_message="Строка заведена."))[1]
        self.assertEqual(title, "devkit (DK-509): Строка заведена")

    def test_turn_failed_is_loud_and_names_the_error(self):
        # Тип ошибки hookio кладёт поводом из поля error, текст последней
        # репликой: сюда доезжает только ход, у которого ретраи вотчдога
        # исчерпаны или ошибка неретраибельная, и молчать про такое нельзя.
        key, title, body, level = notify.parse_event(sess(
            hook_event_name="StopFailure", error="server_error",
            last_assistant_message="API Error: Unable to connect (ENOTFOUND)"))
        self.assertEqual(key, notify.TURN_FAILED)
        self.assertEqual(title, "devkit-dk-034: ход упал (server_error)")
        self.assertEqual(body, "API Error: Unable to connect (ENOTFOUND)")
        self.assertEqual(level, notify.LOUD)

    def test_turn_failed_without_error_type(self):
        # Событие без типа ошибки подписывается словом без уточнения, а не
        # пустыми скобками.
        title = notify.parse_event(sess(hook_event_name="StopFailure"))[1]
        self.assertEqual(title, "devkit-dk-034: ход упал")

    def test_other_events(self):
        # UserPromptSubmit разбором не проходит: он только снимает отметку
        # ожидания.
        for name in ("UserPromptSubmit", "PreToolUse", "SessionStart"):
            self.assertIsNone(notify.parse_event(sess(hook_event_name=name)))

    def test_subagent_takes_first_line(self):
        key, title, body, level = notify.parse_event(sess(
            hook_event_name="SubagentStop", agent_type="review-high",
            last_assistant_message="\n\nЗамечаний нет\nдалее детали\nи ещё"))
        self.assertEqual(key, "subagent_stop")
        self.assertEqual(title, "devkit-dk-034: субагент отработал")
        self.assertEqual(body, "review-high: Замечаний нет")
        # Субагент это рассказ о ходе работы, звать по нему некуда.
        self.assertEqual(level, notify.QUIET)

    def test_empty_body(self):
        body = notify.parse_event(sess(notification_type="idle_prompt", message=""))[2]
        self.assertEqual(body, "")
        body = notify.parse_event(sess(hook_event_name="SubagentStop",
                                       agent_type="exec-low"))[2]
        self.assertEqual(body, "exec-low")

    def test_body_is_cut(self):
        body = notify.parse_event(sess(notification_type="idle_prompt",
                                       message="ы" * 500))[2]
        self.assertEqual(len(body), notify.BODY_LIMIT)
        self.assertTrue(body.endswith("..."))

    def test_fields_of_wrong_type(self):
        # Поля события приходят от харнеса, и их форма это его дело, а не наше:
        # число вместо строки не должно ни ронять хук, ни съедать повод.
        key, title, body, _ = notify.parse_event(sess(
            notification_type="permission_prompt", message=42, cwd=7))
        self.assertEqual((key, title, body), ("permission_prompt", "сессия: нужно разрешение", "42"))
        body = notify.parse_event(sess(hook_event_name="SubagentStop",
                                       agent_type=1, last_assistant_message=None))[2]
        self.assertEqual(body, "1")

    def test_unhashable_reason(self):
        # Повод объектом по словарю поводов не ищется: разбор роняет TypeError,
        # и ловит его хук, а не разбор.
        with self.assertRaises(TypeError):
            notify.parse_event(sess(notification_type={"a": 1}))

    def test_title_without_cwd(self):
        title = notify.parse_event(sess(cwd="", notification_type="idle_prompt"))[1]
        self.assertEqual(title, "сессия: ждёт ввода")


class TestSessionTree(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def write(self, *lines):
        path = os.path.join(self.dir, "transcript.jsonl")
        with open(path, "w", encoding="utf-8") as f:
            f.write("".join(line + "\n" for line in lines))
        return path

    def test_worktree_of_subagent_gives_way_to_session(self):
        # Субагент ушёл в дерево задачи, окно сессии осталось на своём: цель
        # клика берётся из транскрипта, иначе клик открывает лишнее окно.
        path = self.write('{"type":"queue-operation","sessionId":"s1"}',
                          '{"type":"user","cwd":"/Users/x/projects/it-road-course"}')
        self.assertEqual(
            notify.session_tree(sess(transcript_path=path,
                                     cwd="/Users/x/projects/it-road-course-irc-75")),
            "/Users/x/projects/it-road-course")

    def test_broken_transcript_falls_back_to_cwd(self):
        # Транскрипта нет, он битый или cwd в нём не попался: цель собирается
        # из cwd события, как до появления разбора транскрипта.
        cases = (self.write("не json", '{"type":"user"}'),
                 self.write(*['{"type":"queue-operation"}'] * 40),
                 os.path.join(self.dir, "нет-такого.jsonl"), "", None, 7)
        for path in cases:
            self.assertEqual(
                notify.session_tree(sess(transcript_path=path, cwd="/p/dk")),
                "/p/dk", path)
        self.assertEqual(notify.session_tree(sess(transcript_path="", cwd=7)), "")

    def test_scan_stops_before_the_whole_file(self):
        # Транскрипт вырастает на десятки мегабайт, и читать его целиком ради
        # cwd хук не должен: cwd лежит в первых записях или не лежит вовсе.
        # Сорок служебных записей это заведомо дальше предела, и такой cwd уже
        # не берётся; число тут своё, из предела оно не считается, иначе тест
        # подстроился бы под любой предел.
        path = self.write(*(['{"type":"queue-operation"}'] * 40
                            + ['{"type":"user","cwd":"/p/поздно"}']))
        self.assertEqual(notify.session_tree(sess(transcript_path=path, cwd="/p/dk")),
                         "/p/dk")


class TestSessionLabel(unittest.TestCase):
    def test_worktree_shows_window_and_task(self):
        self.assertEqual(
            notify.session_label("/Users/x/projects/it-road-course-irc-75",
                                 "/Users/x/projects/it-road-course"),
            "it-road-course (irc-75)")

    def test_session_at_home_stays_short(self):
        self.assertEqual(notify.session_label("/p/devkit", "/p/devkit"), "devkit")
        self.assertEqual(notify.session_label("/p/devkit", ""), "devkit")
        self.assertEqual(notify.session_label("/p/devkit"), "devkit")

    def test_tree_beside_the_session(self):
        # Субагент ушёл не в worktree проекта, а куда-то ещё: имя показывается
        # целиком, отрезать от него нечего.
        self.assertEqual(notify.session_label("/tmp/проба", "/p/devkit"),
                         "devkit (проба)")
        self.assertEqual(notify.session_label("", "/p/devkit"), "devkit")
        self.assertEqual(notify.session_label("", ""), "сессия")

    def test_title_carries_both(self):
        title = notify.parse_event(
            sess(cwd="/p/it-road-course-irc-75", notification_type="permission_prompt"),
            "/p/it-road-course")[1]
        self.assertEqual(title, "it-road-course (irc-75): нужно разрешение")


class TestSessionLabelBranch(unittest.TestCase):
    """Метка задачи с ветки, когда имя дерева её не дало (сессия в основном
    чекауте, а не в linked worktree с ID в имени)."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def repo(self, branch, name="checkout"):
        path = os.path.join(self.dir, name)
        os.makedirs(os.path.join(path, ".git"))
        with open(os.path.join(path, ".git", "HEAD"), "w", encoding="utf-8") as f:
            f.write("ref: refs/heads/%s\n" % branch)
        return path

    def test_main_checkout_shows_branch(self):
        path = self.repo("dk-121")
        self.assertEqual(notify.session_label(path, path), "checkout (dk-121)")

    def test_root_omitted_still_reads_branch(self):
        # session_label(cwd) без root это self-test: дерево тогда единственное
        # известное, и ветку читаем прямо из него.
        path = self.repo("dk-121", "solo")
        self.assertEqual(notify.session_label(path), "solo (dk-121)")

    def test_main_and_master_are_not_a_task_label(self):
        for branch in ("main", "master"):
            path = self.repo(branch, branch)
            self.assertEqual(notify.session_label(path, path), branch)

    def test_no_repo_leaves_title_bare(self):
        path = os.path.join(self.dir, "plain")
        os.makedirs(path)
        self.assertEqual(notify.session_label(path, path), "plain")

    def test_detached_head_leaves_title_bare(self):
        path = os.path.join(self.dir, "detached")
        os.makedirs(os.path.join(path, ".git"))
        with open(os.path.join(path, ".git", "HEAD"), "w", encoding="utf-8") as f:
            f.write("abcdef0123456789\n")
        self.assertEqual(notify.session_label(path, path), "detached")

    def test_linked_worktree_gitdir_indirection_is_read(self):
        # У linked worktree .git это файл с gitdir на каталог с собственным
        # HEAD, а не каталог самим по себе.
        gitdir = os.path.join(self.dir, "common", "worktrees", "task")
        os.makedirs(gitdir)
        with open(os.path.join(gitdir, "HEAD"), "w", encoding="utf-8") as f:
            f.write("ref: refs/heads/dk-121\n")
        path = os.path.join(self.dir, "checkout")
        os.makedirs(path)
        with open(os.path.join(path, ".git"), "w", encoding="utf-8") as f:
            f.write("gitdir: %s\n" % gitdir)
        self.assertEqual(notify.session_label(path, path), "checkout (dk-121)")

    def test_worktree_name_still_wins_over_branch(self):
        # Имя дерева с ID задачи уже даёт метку, и до ветки дело не доходит:
        # обычный worktree задачи не должен зависеть от того, что лежит в HEAD.
        home = self.repo("main", "it-road-course")
        self.assertEqual(notify.session_label(
            os.path.join(self.dir, "it-road-course-irc-75"), home),
            "it-road-course (irc-75)")


class TestEventTarget(unittest.TestCase):
    """Задача и проект события полями: по ним лента дашборда ведёт к строке
    доски, и догадка по тексту баннера ей больше не нужна (DK-323)."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def repo(self, branch, name="checkout"):
        path = os.path.join(self.dir, name)
        os.makedirs(os.path.join(path, ".git"))
        with open(os.path.join(path, ".git", "HEAD"), "w", encoding="utf-8") as f:
            f.write("ref: refs/heads/%s\n" % branch)
        return path

    def test_worktree_of_the_subagent_names_both(self):
        self.assertEqual(
            notify.event_target("/p/it-road-course-irc-75", "/p/it-road-course"),
            ("IRC-75", "it-road-course"))

    def test_session_inside_the_worktree_still_names_the_project(self):
        # Окно сессии стоит на самом дереве задачи: проектом тут зовётся не
        # «devkit-dk-323», такого проекта у дашборда нет, а devkit.
        self.assertEqual(notify.event_target("/p/devkit-dk-323", "/p/devkit-dk-323"),
                         ("DK-323", "devkit"))

    def test_branch_gives_the_task_in_the_main_checkout(self):
        path = self.repo("dk-121", "devkit")
        self.assertEqual(notify.event_target(path, path), ("DK-121", "devkit"))

    def test_main_branch_leaves_the_task_empty(self):
        path = self.repo("main", "devkit")
        self.assertEqual(notify.event_target(path, path), ("", "devkit"))

    def test_tree_without_an_id_is_not_a_task(self):
        # Боковое дерево под второй харнес зовётся не по задаче, и выдумывать
        # ей ID не из чего: событие честно едет без задачи.
        self.assertEqual(notify.event_target("/p/devkit-glm", "/p/devkit"),
                         ("", "devkit"))

    def test_no_trees_name_nothing(self):
        self.assertEqual(notify.event_target("", ""), ("", ""))

    def test_task_id_takes_only_board_ids(self):
        self.assertEqual(notify.task_id("dk-323"), "DK-323")
        self.assertEqual(notify.task_id("XR-1"), "XR-1")
        for junk in ("main", "feature/x", "dk-", "-323", "", None):
            self.assertEqual(notify.task_id(junk), "", junk)


class TestClickTarget(unittest.TestCase):
    VSCODE = {"CLAUDE_CODE_ENTRYPOINT": "claude-vscode"}

    def test_worktree_becomes_url(self):
        self.assertEqual(notify.click_target(self.VSCODE, "/Users/x/projects/devkit-dk-047"),
                         ("-open", "vscode://file/Users/x/projects/devkit-dk-047"
                                   "?windowId=_blank"))

    def test_target_never_replaces_a_window(self):
        # Без windowId=_blank редактор открывает дерево в активном окне, а не в
        # своём: дерева нет ни в одном окне, значит под замену идёт то, где
        # сейчас работают. Параметр обязателен на любой цели vscode://.
        for cwd in ("/p/dk", "/Users/x/мои проекты/dk", "/p/dk?a=1"):
            flag, url = notify.click_target(self.VSCODE, cwd)
            self.assertEqual(flag, "-open")
            self.assertTrue(url.endswith("?windowId=_blank"), url)
            self.assertEqual(url.count("?"), 1, url)

    def test_awkward_path_is_quoted(self):
        # Пробелы и кириллица в пути ссылку не ломают: она уезжает аргументом,
        # но открывает её система, а не мы.
        self.assertEqual(notify.click_target(self.VSCODE, "/Users/x/мои проекты/dk"),
                         ("-open", "vscode://file/Users/x/%D0%BC%D0%BE%D0%B8%20"
                                   "%D0%BF%D1%80%D0%BE%D0%B5%D0%BA%D1%82%D1%8B/dk"
                                   "?windowId=_blank"))

    def test_vscode_without_cwd_activates_editor(self):
        self.assertEqual(notify.click_target(self.VSCODE, ""),
                         ("-activate", "com.microsoft.VSCode"))
        self.assertEqual(notify.click_target({"TERM_PROGRAM": "vscode"}, None),
                         ("-activate", "com.microsoft.VSCode"))

    def test_terminal_is_activated_whole(self):
        # Окно терминала по рабочему дереву не найти, поднимаем сам терминал.
        self.assertEqual(notify.click_target({"TERM_PROGRAM": "Apple_Terminal"}, "/p/dk"),
                         ("-activate", "com.apple.Terminal"))
        self.assertEqual(notify.click_target({"TERM_PROGRAM": "iTerm.app"}, "/p/dk"),
                         ("-activate", "com.googlecode.iterm2"))

    def test_cwd_of_wrong_type(self):
        # cwd приходит от харнеса, и не-строка тут не должна ронять сборку цели:
        # квотирование числа кинуло бы TypeError уже мимо разбора события.
        self.assertEqual(notify.click_target(self.VSCODE, 7),
                         ("-activate", "com.microsoft.VSCode"))
        self.assertEqual(notify.click_target(self.VSCODE, ["/a"]),
                         ("-activate", "com.microsoft.VSCode"))
        env = dict(self.VSCODE, DEVKIT_NOTIFY_OPEN="x-devkit://{cwd}")
        self.assertEqual(notify.click_target(env, 7), ("-open", "x-devkit://"))

    def test_unknown_terminal_has_no_target(self):
        self.assertIsNone(notify.click_target({"TERM_PROGRAM": "Hyper"}, "/p/dk"))
        self.assertIsNone(notify.click_target({}, "/p/dk"))

    def test_own_template(self):
        env = {"DEVKIT_NOTIFY_OPEN": "x-terminal://{cwd}", "TERM_PROGRAM": "vscode"}
        self.assertEqual(notify.click_target(env, "/p/dk"), ("-open", "x-terminal:///p/dk"))

    def test_empty_template_turns_click_off(self):
        env = {"DEVKIT_NOTIFY_OPEN": "", "CLAUDE_CODE_ENTRYPOINT": "claude-vscode"}
        self.assertIsNone(notify.click_target(env, "/p/dk"))

    def test_supports_click(self):
        self.assertTrue(notify.supports_click("/opt/homebrew/bin/terminal-notifier"))
        self.assertFalse(notify.supports_click("/usr/bin/osascript"))
        self.assertFalse(notify.supports_click(None))


class TestPickBackend(unittest.TestCase):
    def test_macos_prefers_terminal_notifier(self):
        # osascript есть на любой macOS, поэтому без предпочтения клик не
        # достался бы никому.
        self.assertEqual(notify.pick_backend({}, "darwin", lambda n: "/usr/bin/" + n),
                         "terminal-notifier")

    def test_macos_osascript(self):
        which = lambda n: "/usr/bin/osascript" if n == "osascript" else None
        self.assertEqual(notify.pick_backend({}, "darwin", which), "osascript")
        self.assertIsNone(notify.pick_backend({}, "darwin", lambda n: None))

    def test_linux_notify_send(self):
        self.assertEqual(notify.pick_backend({}, "linux", lambda n: "/usr/bin/" + n),
                         "notify-send")
        self.assertIsNone(notify.pick_backend({}, "linux", lambda n: None))

    def test_platform_without_backend(self):
        self.assertIsNone(notify.pick_backend({}, "win32", lambda n: "/bin/" + n))

    def test_override_beats_platform(self):
        env = {"DEVKIT_NOTIFY_BACKEND": "/tmp/stub"}
        self.assertEqual(notify.pick_backend(env, "darwin", lambda n: "/usr/bin/" + n),
                         "/tmp/stub")
        self.assertEqual(notify.pick_backend(env, "win32", lambda n: None), "/tmp/stub")

    def test_osascript_argv(self):
        argv = notify.backend_argv("/usr/bin/osascript", 'сессия "dk"', "тело",
                                   level=notify.QUIET)
        self.assertEqual(argv[:2], ["/usr/bin/osascript", "-e"])
        self.assertEqual(argv[2],
                         'display notification "тело" with title "сессия \\"dk\\""')
        # Группировки у display notification нет вовсе, весь уровень это звук.
        self.assertEqual(notify.backend_argv("/usr/bin/osascript", "з", "т")[2],
                         'display notification "т" with title "з" sound name "default"')

    def test_notify_send_argv(self):
        self.assertEqual(notify.backend_argv("/usr/bin/notify-send", "з", "т"),
                         ["/usr/bin/notify-send", "-u", "critical", "з", "т"])
        self.assertEqual(
            notify.backend_argv("/usr/bin/notify-send", "з", "т", level=notify.QUIET,
                                session="sess1"),
            ["/usr/bin/notify-send", "-u", "low",
             "-h", "string:x-canonical-private-synchronous:devkit-sess1", "з", "т"])

    def test_own_command_knows_no_level(self):
        # Своя команда зовётся как «команда заголовок тело» и на уровень не
        # смотрит: договор с ней старый.
        for level in (notify.LOUD, notify.QUIET):
            self.assertEqual(notify.backend_argv("/tmp/stub", "з", "т", level=level,
                                                 session="sess1"),
                             ["/tmp/stub", "з", "т"])

    def test_terminal_notifier_argv(self):
        target = ("-open", "vscode://file/p/dk")
        self.assertEqual(
            notify.backend_argv("/bin/terminal-notifier", "заголовок", "тело", target),
            ["/bin/terminal-notifier", "-title", "заголовок", "-message", "тело",
             "-sound", "default", "-open", "vscode://file/p/dk"])
        # Без цели уведомление всё равно уходит, просто не кликается.
        self.assertEqual(
            notify.backend_argv("/bin/terminal-notifier", "заголовок", "тело"),
            ["/bin/terminal-notifier", "-title", "заголовок", "-message", "тело",
             "-sound", "default"])
        # Пустое тело она показала бы пустой строкой.
        self.assertEqual(
            notify.backend_argv("/bin/terminal-notifier", "заголовок", "")[4], "заголовок")

    def test_terminal_notifier_collapses_background(self):
        # Фоновый идёт молча и с группой по сессии: новый баннер сессии
        # вытесняет её же предыдущий, а не ложится под него.
        argv = notify.backend_argv("/bin/terminal-notifier", "з", "т", None,
                                   notify.QUIET, "sess1")
        self.assertEqual(argv[5:], ["-group", "devkit-sess1"])
        self.assertNotIn("-sound", argv)
        # Сессии нет (аргументный режим), группа всё равно одна на devkit.
        self.assertEqual(notify.backend_argv("/bin/terminal-notifier", "з", "т", None,
                                             notify.QUIET, "-")[6], "devkit")

    def test_backends_without_click_ignore_target(self):
        target = ("-open", "vscode://file/p/dk")
        self.assertEqual(notify.backend_argv("/usr/bin/notify-send", "з", "т", target),
                         ["/usr/bin/notify-send", "-u", "critical", "з", "т"])
        self.assertEqual(len(notify.backend_argv("/usr/bin/osascript", "з", "т", target)), 3)


class TestThrottle(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def test_window_on_both_sides(self):
        now = 1000.0
        self.assertTrue(notify.allow("sess1", "subagent_stop", now, self.dir))
        self.assertFalse(notify.allow("sess1", "subagent_stop", now + 1, self.dir))
        self.assertFalse(notify.allow("sess1", "subagent_stop",
                                      now + notify.WINDOW - 0.5, self.dir))
        self.assertTrue(notify.allow("sess1", "subagent_stop",
                                     now + notify.WINDOW, self.dir))

    def test_turn_done_and_idle_share_the_key(self):
        # idle_prompt приходит примерно через минуту после конца хода, и это
        # тот же повод «сессия ждёт тебя»: своим ключом он повторил бы баннер.
        now = 1000.0
        self.assertTrue(notify.allow("sess1", notify.TURN_DONE, now, self.dir))
        self.assertFalse(notify.allow("sess1", "idle_prompt", now + 61, self.dir))
        self.assertFalse(notify.allow("sess1", "idle_prompt",
                                      now + notify.WAIT_WINDOW - 0.5, self.dir))
        self.assertTrue(notify.allow("sess1", "idle_prompt",
                                     now + notify.WAIT_WINDOW, self.dir))
        # Окно ожидания шире минуты, иначе idle_prompt проскакивал бы следом.
        self.assertGreater(notify.WAIT_WINDOW, 60)

    def test_turn_failed_key_is_its_own(self):
        # Баннер конца хода, ушедший только что, не должен глушить весть о том,
        # что следующий ход упал.
        now = 1000.0
        self.assertTrue(notify.allow("sess1", notify.TURN_DONE, now, self.dir))
        self.assertTrue(notify.allow("sess1", notify.TURN_FAILED, now + 1, self.dir))

    def test_user_input_lets_the_next_turn_ring(self):
        # Пользователь вернулся к сессии, и конец следующего хода это новый
        # повод позвать, а не повтор прошлого: общее с idle_prompt окно иначе
        # съедало бы второй длинный ход подряд.
        now = 1000.0
        self.assertTrue(notify.allow("sess1", notify.TURN_DONE, now, self.dir))
        notify.clear_wait("sess1", self.dir)
        self.assertTrue(notify.allow("sess1", notify.TURN_DONE, now + 1, self.dir))
        # Ввода не было, значит idle_prompt следом за концом хода по-прежнему
        # молчит: ради этого ключ и общий.
        self.assertFalse(notify.allow("sess1", "idle_prompt", now + 2, self.dir))

    def test_user_input_touches_only_waiting(self):
        now = 1000.0
        self.assertTrue(notify.allow("sess1", "subagent_stop", now, self.dir))
        self.assertTrue(notify.allow("sess2", notify.TURN_DONE, now, self.dir))
        notify.clear_wait("sess1", self.dir)
        self.assertFalse(notify.allow("sess1", "subagent_stop", now + 1, self.dir))
        self.assertFalse(notify.allow("sess2", "idle_prompt", now + 1, self.dir))
        # Состояния ещё нет (ввод пришёл первым событием сессии), и это не
        # повод падать.
        notify.clear_wait("sess3", self.dir)

    def test_reasons_do_not_mute_each_other(self):
        now = 1000.0
        self.assertTrue(notify.allow("sess1", "idle_prompt", now, self.dir))
        self.assertTrue(notify.allow("sess1", "permission_prompt", now, self.dir))
        self.assertTrue(notify.allow("sess1", "subagent_stop", now, self.dir))

    def test_sessions_do_not_mute_each_other(self):
        # Без session_id в ключе пять параллельных агентов слились бы в один.
        now = 1000.0
        self.assertTrue(notify.allow("sess1", "idle_prompt", now, self.dir))
        self.assertTrue(notify.allow("sess2", "idle_prompt", now, self.dir))

    def test_stale_state_is_swept(self):
        now = time.time()
        notify.allow("sess1", "idle_prompt", now - notify.STALE - 60, self.dir)
        stale = os.listdir(self.dir)
        self.assertEqual(len(stale), 1)
        notify.allow("sess2", "idle_prompt", now, self.dir)
        self.assertNotIn(stale[0], os.listdir(self.dir))


class Proc:
    """Ответ subprocess.run: живой osascript за границей опроса."""

    def __init__(self, returncode=0, stdout=""):
        self.returncode = returncode
        self.stdout = stdout


class TestFrontWindow(unittest.TestCase):
    def setUp(self):
        self.calls = []

    def ran(self, proc):
        def run(argv, **kw):
            self.calls.append((argv, kw))
            if isinstance(proc, BaseException):
                raise proc
            return proc
        return run

    def test_title_comes_from_system_events(self):
        title = notify.front_window(lambda n: "/usr/bin/" + n,
                                    self.ran(Proc(0, "Разбор задачи - devkit\n")))
        self.assertEqual(title, "Разбор задачи - devkit")
        argv, kw = self.calls[0]
        self.assertEqual(argv[:2], ["/usr/bin/osascript", "-e"])
        self.assertIn("System Events", argv[2])
        self.assertIn("front window", argv[2])
        # Опрос упирается и в диалог разрешения на управление компьютером: без
        # срока хук висел бы на нём весь ход.
        self.assertTrue(0 < kw.get("timeout", 0) <= 10, kw)

    def test_bytes_are_decoded(self):
        # Без text=True subprocess отдаёт байты, и заголовок с кириллицей
        # сравнивать с именем дерева было бы нечем.
        self.assertEqual(
            notify.front_window(lambda n: "/usr/bin/" + n,
                                self.ran(Proc(0, "Правка - дерево\n".encode("utf-8")))),
            "Правка - дерево")

    def test_platform_without_osascript_asks_nobody(self):
        # Не macOS: опрос не заводится вовсе, и конец хода зовёт как обычно.
        self.assertEqual(notify.front_window(lambda n: None, self.ran(Proc(0, "окно"))), "")
        self.assertEqual(self.calls, [])

    def test_refusal_gives_no_title(self):
        # Разрешения на управление компьютером нет, переднего окна нет,
        # osascript не запустился или не ответил в срок: всё это одно и то же.
        cases = (Proc(1, "ошибка"), Proc(0, ""), Proc(0, "  \n"),
                 OSError("нет такого файла"),
                 subprocess.TimeoutExpired("osascript", 5))
        for proc in cases:
            self.assertEqual(
                notify.front_window(lambda n: "/usr/bin/" + n, self.ran(proc)), "", proc)


class TestFocusState(unittest.TestCase):
    def state(self, title, tree="/p/devkit-dk-064", env=None):
        self.asked = 0

        def ask():
            self.asked += 1
            return title
        return notify.focus_state(tree, {} if env is None else env, ask)

    def test_session_window_in_front(self):
        # Имя рабочего дерева стоит хвостом заголовка после разделителя.
        # Второй разделитель снят с живого VS Code: он ставит длинное тире, и
        # тут оно собирается из кода, чтобы файл остался на клавиатурных
        # символах.
        for title in ("Правка notify.py - devkit-dk-064", "devkit-dk-064",
                      "notify.py %s devkit-dk-064" % chr(0x2014),
                      "  Правка notify.py - devkit-dk-064  "):
            self.assertEqual(self.state(title), notify.FOCUS_SESSION, title)

    def test_worktree_is_not_the_main_tree(self):
        # Дерево задачи и дерево проекта это разные окна: смотреть в одно из
        # них не значит смотреть в другое.
        self.assertEqual(self.state("Разбор задачи - devkit"), notify.FOCUS_OTHER)
        self.assertEqual(self.state("Правка - devkit-dk-064", "/p/devkit"),
                         notify.FOCUS_OTHER)
        # Хвост берётся целым словом, а не просто концом строки.
        self.assertEqual(self.state("Заметки - mydevkit", "/p/devkit"),
                         notify.FOCUS_OTHER)
        self.assertEqual(self.state("Правка - devkit-dk-064", "/p/dk-064"),
                         notify.FOCUS_OTHER)

    def test_someone_elses_window(self):
        self.assertEqual(self.state("Входящие - Почта"), notify.FOCUS_OTHER)

    def test_refusal_rings(self):
        # Спросить не удалось, значит зовём: тишина неотличима от штатной работы.
        self.assertEqual(self.state(""), notify.FOCUS_UNKNOWN)

    def test_session_without_tree_asks_nobody(self):
        # Дерева сессии нет, сравнивать заголовок не с чем: тратить на опрос
        # его 180 мс незачем.
        self.assertEqual(self.state("Правка - devkit-dk-064", ""), notify.FOCUS_UNKNOWN)
        self.assertEqual(self.asked, 0)

    def test_switch_turns_the_check_off(self):
        self.assertEqual(self.state("Правка - devkit-dk-064",
                                    env={"DEVKIT_NOTIFY_FOCUS": "off"}),
                         notify.FOCUS_OTHER)
        self.assertEqual(self.asked, 0)
        # Значение другое, значит проверка на месте: выключатель тут один.
        self.assertEqual(self.state("Правка - devkit-dk-064",
                                    env={"DEVKIT_NOTIFY_FOCUS": "on"}),
                         notify.FOCUS_SESSION)


class TestHookFocus(unittest.TestCase):
    """Хук целиком: событие на stdin, HOME во временной директории, опрос
    фокуса и отправка заглушены."""

    def setUp(self):
        self.home = tempfile.mkdtemp()

    def hook(self, event, title="Входящие - Почта", env=None):
        """Прогнать хук на событии. Возврат (заголовки уехавших уведомлений,
        сколько раз спрашивали фокус)."""
        sent, asked = [], []

        def ask():
            asked.append(title)
            return title

        def send(backend, title_, body, target=None, level=notify.LOUD, session=None):
            sent.append(title_)
            return 0

        environ = dict(os.environ, HOME=self.home, DEVKIT_NOTIFY_BACKEND="/bin/стаб")
        environ.update(env or {})
        with mock.patch.dict(os.environ, environ, clear=True), \
                mock.patch.object(notify, "front_window", ask, create=True), \
                mock.patch.object(notify, "send", send), \
                mock.patch.object(sys, "stdin", io.StringIO(json.dumps(event))):
            self.assertEqual(notify.run_hook("claude-code"), 0)
        return sent, len(asked)

    def journal(self):
        with open(os.path.join(self.home, ".devkit", "notify.log"),
                  encoding="utf-8") as f:
            return f.read()

    def test_short_turn_rings_from_someone_elses_window(self):
        # Короткий вопрос перед тем, как отойти: машина отработала за секунды,
        # но человек смотрит уже не сюда, и позвать его надо. Порог
        # длительности хода (DK-062) ровно тут и промахивался, поэтому ввод
        # пользователя идёт прямо перед концом хода.
        self.hook(event(hook_event_name="UserPromptSubmit", session_id="sess-short"))
        sent, asked = self.hook(event(hook_event_name="Stop", session_id="sess-short"))
        self.assertEqual(sent, ["devkit-dk-034: ход закончен"])
        self.assertEqual(asked, 1)

    def test_session_window_stays_quiet(self):
        sent, asked = self.hook(event(hook_event_name="Stop", session_id="sess-here"),
                                title="Правка notify.py - devkit-dk-034")
        self.assertEqual(sent, [])
        self.assertEqual(asked, 1)
        self.assertIn("повод turn_done уровень громкий бэкенд - цель - "
                      "задача DK-034 проект devkit пропуск: окно сессии в фокусе", self.journal())

    def test_main_tree_window_is_not_the_worktree(self):
        # Сессия сидит в worktree задачи, а впереди окно самого проекта: это
        # разные окна, и молчать тут не о чем.
        sent, _ = self.hook(event(hook_event_name="Stop", session_id="sess-tree"),
                            title="Разбор задачи - devkit")
        self.assertEqual(sent, ["devkit-dk-034: ход закончен"])

    def test_refusal_rings_and_leaves_a_line(self):
        sent, _ = self.hook(event(hook_event_name="Stop", session_id="sess-blind"),
                            title="")
        self.assertEqual(sent, ["devkit-dk-034: ход закончен"])
        # «Звонит всегда» разбирается по строке журнала, а не на глаз.
        self.assertIn("фокус не определился, зовём", self.journal())

    def test_switch_rings_without_asking(self):
        sent, asked = self.hook(event(hook_event_name="Stop", session_id="sess-off"),
                                title="Правка notify.py - devkit-dk-034",
                                env={"DEVKIT_NOTIFY_FOCUS": "off"})
        self.assertEqual(sent, ["devkit-dk-034: ход закончен"])
        self.assertEqual(asked, 0)

    def test_other_reasons_never_ask(self):
        # Лишний опрос на каждого субагента это те же 180 мс на пустом месте, а
        # запрос разрешения зовёт в любом случае: сессия на нём стоит намертво.
        for ev in (event(notification_type="permission_prompt", session_id="sess-perm"),
                   event(notification_type="agent_needs_input", session_id="sess-ask"),
                   event(notification_type="idle_prompt", session_id="sess-idle"),
                   event(hook_event_name="SubagentStop", session_id="sess-sub",
                         last_assistant_message="готово")):
            sent, asked = self.hook(ev, title="Правка notify.py - devkit-dk-034")
            self.assertEqual(len(sent), 1, ev)
            self.assertEqual(asked, 0, ev)

    def test_user_input_sends_nothing(self):
        sent, asked = self.hook(event(hook_event_name="UserPromptSubmit",
                                      session_id="sess-in"))
        self.assertEqual((sent, asked), ([], 0))


class TestSandboxReason(unittest.TestCase):
    def test_tmpdir_root_is_a_sandbox(self):
        d = tempfile.mkdtemp()
        reason = notify.sandbox_reason(os.path.join(d, "repo"), {"TMPDIR": d})
        self.assertIsNotNone(reason)
        self.assertIn("лежит под", reason)

    def test_usual_places_work_without_tmpdir(self):
        # Переменную перебивают, а песочницы mktemp -d всё равно лежат тут.
        for cwd in ("/tmp/board", "/private/tmp/board",
                    "/var/folders/ab/xy/T/tmp.X", "/private/var/folders/ab/xy/T/tmp.X"):
            self.assertIsNotNone(notify.sandbox_reason(cwd, {}), cwd)

    def test_real_root_is_kept(self):
        self.assertIsNone(notify.sandbox_reason("/Users/dev/projects/devkit", {}))

    def test_tmpdir_away_from_usual_places(self):
        # Ветка TMPDIR сама по себе: перебор одних TMP_ROOTS такой корень не
        # нашёл бы, а обычный mktemp -d лежит под /var/folders и ветку не
        # отличает.
        env = {"TMPDIR": "/opt/build/tmp"}
        reason = notify.sandbox_reason("/opt/build/tmp/repo", env)
        self.assertIsNotNone(reason)
        self.assertIn("/opt/build/tmp", reason)
        self.assertIsNone(notify.sandbox_reason("/opt/build/elsewhere", env))

    def test_symlinked_tmpdir_resolves_on_both_sides(self):
        # macOS отдаёт TMPDIR то симлинком (/var/...), то развёрнутым путём
        # (/private/var/...), и корень проекта приходит так же: совпадение
        # обязано находиться в обе стороны. Найденным корнем при этом должен
        # быть сам TMPDIR, а не задетый попутно /var/folders, на котором
        # лежит база теста.
        base = tempfile.mkdtemp()
        real = os.path.join(base, "real")
        link = os.path.join(base, "link")
        os.mkdir(real)
        os.symlink(real, link)
        resolved = os.path.realpath(real)
        for tmpdir, cwd in ((link, os.path.join(real, "repo")),
                            (real, os.path.join(link, "repo"))):
            reason = notify.sandbox_reason(cwd, {"TMPDIR": tmpdir})
            self.assertIsNotNone(reason, tmpdir)
            self.assertTrue(reason.endswith("лежит под %s" % resolved), reason)

    def test_prefix_ends_at_the_separator(self):
        # Сосед с общим началом имени это другая директория: /tmpfoo не /tmp,
        # scratchpad не scratch.
        self.assertIsNone(notify.sandbox_reason("/tmpfoo/board", {}))
        self.assertIsNone(notify.sandbox_reason(
            "/Users/dev/scratchpad", {"TMPDIR": "/Users/dev/scratch"}))

    def test_degenerate_inputs_stay_silent(self):
        # TMPDIR корнем файловой системы накрыл бы всё, пустой cwd сравнивать
        # не с чем: молчать в обе стороны, но не флажить настоящие корни.
        self.assertIsNone(notify.sandbox_reason("/Users/dev/p", {"TMPDIR": "/"}))
        self.assertIsNone(notify.sandbox_reason("", {}))
        self.assertIsNone(notify.sandbox_reason(None, {}))

    def test_tmp_root_itself_counts(self):
        d = tempfile.mkdtemp()
        self.assertIsNotNone(notify.sandbox_reason(d, {"TMPDIR": d}))


class TestTerminalSequence(unittest.TestCase):
    def test_leading_digit_is_pushed(self):
        # Тело OSC 9 с цифры харнес отбрасывает по своему белому списку.
        seq = notify.terminal_sequence("42-проект: ждёт ввода", "")
        self.assertTrue(seq.startswith("\033]9; 42-проект"))
        self.assertTrue(seq.endswith("\007"))
        self.assertTrue(notify.terminal_sequence("devkit: ждёт ввода", "текст")
                        .startswith("\033]9;devkit: ждёт ввода. текст"))


# --- Уведомитель целиком: хук как процесс, стаб вместо бэкенда, временный HOME.

NOTIFY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "notify.py")
DATA = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                    "testdata", "claude-code")
REASONS = ("permission_prompt", "agent_needs_input", "elicitation_dialog",
           "idle_prompt")
# Свой cwd для cli(), не унаследованный от процесса теста: без cwd подпроцесс
# notify.py получал бы рабочую директорию раннера, а в свежем дереве прогона
# слияния (DK-641) она сама лежит под TMPDIR и sandbox_reason читает её
# песочницей (DK-069), из-за чего deliver() молча не звался и sent() пустел
# (DK-677). Каталог python3 гарантированно лежит вне TMP_ROOTS.
CLI_CWD = os.path.dirname(sys.executable)


class HookCase(unittest.TestCase):
    """Обвязка прогона хука целиком: свой HOME, стаб отправителя, стаб опроса
    фокуса и своя системная часть PATH. Живой osascript тут не спрашивается:
    он отвечал бы тем, что открыто на машине в эту минуту."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.home = os.path.join(self.tmp, "home")
        os.makedirs(self.home)
        self.mark = os.path.join(self.tmp, "notify.mark")
        self.logfile = os.path.join(self.home, ".devkit", "notify.log")
        self.title = os.path.join(self.tmp, "focus.title")
        self.asked = os.path.join(self.tmp, "focus.mark")
        self.stub = self.script("notify-stub", "printf '%%s|%%s\\n' \"$1\" \"$2\" >> %s\n"
                                % shlex.quote(self.mark))
        # Стаб отправителя с кликом зовётся именно terminal-notifier: по имени
        # бэкенда уведомитель и решает, брать ли цель перехода.
        self.tn = self.script("terminal-notifier", "printf '%%s\\n' \"$*\" >> %s\n"
                              % shlex.quote(self.mark))
        self.osascript = self.script(
            "osascript",
            "printf '%%s\\n' \"$*\" >> %s\n[ -s %s ] || exit 1\n"
            "read -r title < %s && printf '%%s\\n' \"$title\"\n"
            % (shlex.quote(self.asked), shlex.quote(self.title),
               shlex.quote(self.title)))
        # Своя системная часть PATH: проверка «слать нечем» иначе держалась бы
        # на том, есть ли на этой машине osascript или notify-send.
        self.sysdir = os.path.join(self.tmp, "sys")
        os.makedirs(self.sysdir)
        for tool in ("sh", "python3"):
            found = shutil.which(tool)
            if found:
                os.symlink(found, os.path.join(self.sysdir, tool))

    def script(self, name, body):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\n" + body)
        os.chmod(path, 0o755)
        return path

    def hook(self, text, backend=None, focus=False, **env):
        """Событие на stdin, стаб вместо бэкенда. Фокус окна по умолчанию
        выключен: своя проверка фокуса идёт со стабом опроса."""
        env.setdefault("HOME", self.home)
        env.setdefault("DEVKIT_NOTIFY_BACKEND", backend or self.stub)
        if focus:
            env.setdefault("PATH", os.path.dirname(self.osascript)
                           + os.pathsep + self.sysdir)
        else:
            env.setdefault("DEVKIT_NOTIFY_FOCUS", "off")
        return subprocess.run([sys.executable, NOTIFY, "--hook", "claude-code"],
                              input=text, env=env, capture_output=True, text=True)

    def click(self, text, **env):
        """То же, но отправителем стаб terminal-notifier."""
        env.setdefault("CLAUDE_CODE_ENTRYPOINT", "claude-vscode")
        return self.hook(text, backend=self.tn, **env)

    def cli(self, *args, **env):
        env.setdefault("HOME", self.home)
        env.setdefault("DEVKIT_NOTIFY_BACKEND", self.stub)
        cwd = env.pop("cwd", None) or CLI_CWD
        return subprocess.run([sys.executable, NOTIFY] + list(args), env=env,
                              cwd=cwd, capture_output=True, text=True)

    def event(self, kind, reason="", session="sess", **extra):
        d = {"hook_event_name": kind, "notification_type": reason,
             "session_id": session, "cwd": "/p/devkit-dk-034",
             "message": "текст повода", "agent_type": "exec-low",
             "last_assistant_message": "первая строка\nвторая"}
        d.update(extra)
        return json.dumps(d)

    def sent(self):
        if not os.path.exists(self.mark):
            return []
        with open(self.mark, encoding="utf-8") as f:
            return [ln for ln in f.read().split("\n") if ln]

    def journal(self):
        if not os.path.exists(self.logfile):
            return ""
        with open(self.logfile, encoding="utf-8") as f:
            return f.read()

    def asked_size(self):
        """Сколько раз спрашивали фокус: файла нет, значит не спрашивали."""
        return os.path.getsize(self.asked) if os.path.exists(self.asked) else 0

    def clear(self):
        open(self.mark, "w").close()
        open(self.asked, "w").close()

    def look_at(self, window):
        with open(self.title, "w", encoding="utf-8") as f:
            f.write(window + "\n")


class TestHookReasons(HookCase):
    def test_every_reason_reaches_the_backend(self):
        # В заголовке видно, какая сессия зовёт.
        for reason in REASONS:
            self.clear()
            r = self.hook(self.event("Notification", reason, "sess-" + reason))
            self.assertEqual(r.returncode, 0, reason)
            self.assertTrue(self.sent()[0].startswith("devkit-dk-034: "),
                            (reason, self.sent()))

    def test_subagent_reaches_the_backend(self):
        r = self.hook(self.event("SubagentStop", session="sess-sub"))
        self.assertEqual(r.returncode, 0)
        self.assertIn("субагент отработал|exec-low: первая строка", self.sent()[0])

    def test_turn_done_is_loud(self):
        r = self.hook(self.event("Stop", session="sess-turn"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["devkit-dk-034: первая строка|вторая"])
        self.assertIn("повод turn_done уровень громкий", self.journal())

    def test_user_prompt_sends_nothing(self):
        r = self.hook(self.event("UserPromptSubmit", session="sess-in"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])

    def test_quiet_reasons_and_alien_events(self):
        for text in (self.event("Notification", "auth_success", "sess-quiet"),
                     self.event("PreToolUse", session="sess-quiet"),
                     "не json"):
            self.assertEqual(self.hook(text).returncode, 0, text)
        self.assertEqual(self.sent(), [])


class TestBannerLevel(HookCase):
    """Уровень доезжает до баннера: громкий со звуком, фоновый молча и с
    группой по сессии, чтобы новый баннер вытеснял её же предыдущий."""

    def test_loud_goes_with_sound_and_without_group(self):
        r = self.click(self.event("Notification", "permission_prompt", "sess-loud"))
        self.assertEqual(r.returncode, 0)
        line = self.sent()[0]
        self.assertIn("-sound default", line)
        self.assertNotIn("-group", line)

    def test_background_goes_muted_and_grouped(self):
        r = self.click(self.event("SubagentStop", session="sess-bg"))
        self.assertEqual(r.returncode, 0)
        line = self.sent()[0]
        self.assertIn("-group devkit-sess-bg", line)
        self.assertNotIn("-sound", line)
        self.assertIn("повод subagent_stop уровень фоновый", self.journal())


class TestHookFocusProcess(HookCase):
    """Звать о конце хода или молчать, решает фокус окна."""

    def test_session_window_in_focus_keeps_quiet(self):
        self.look_at("Правка notify.py - devkit-dk-034")
        r = self.hook(self.event("Stop", session="sess-focus"), focus=True)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])
        self.assertTrue(self.asked_size(), "фокус не спрашивался вовсе")
        self.assertIn("повод turn_done уровень громкий бэкенд - цель - "
                      "задача DK-034 проект devkit пропуск: окно сессии в фокусе", self.journal())

    def test_alien_window_gets_the_call(self):
        # Дерево проекта и дерево задачи это разные окна: сессия сидит в
        # devkit-dk-034, а заголовок кончается на devkit.
        self.look_at("Разбор задачи - devkit")
        r = self.hook(self.event("Stop", session="sess-away"), focus=True)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["devkit-dk-034: первая строка|вторая"])

    def test_silent_poll_gets_the_call(self):
        # Нет разрешения на управление компьютером или не macOS: зовём, и отказ
        # виден в журнале, иначе «звонит всегда» разбирать нечем.
        r = self.hook(self.event("Stop", session="sess-blind"), focus=True)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["devkit-dk-034: первая строка|вторая"])
        self.assertIn("фокус не определился, зовём", self.journal())

    def test_switch_off_skips_the_poll(self):
        self.look_at("Правка notify.py - devkit-dk-034")
        r = self.hook(self.event("Stop", session="sess-nofocus"), focus=True,
                      DEVKIT_NOTIFY_FOCUS="off")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["devkit-dk-034: первая строка|вторая"])
        self.assertEqual(self.asked_size(), 0)

    def test_poll_is_only_about_the_turn(self):
        # Лишний опрос на каждого субагента это его 180 мс на пустом месте, а
        # запрос разрешения зовёт в любом случае.
        self.look_at("Правка notify.py - devkit-dk-034")
        self.clear()
        for text in (self.event("Notification", "permission_prompt", "sess-fp"),
                     self.event("SubagentStop", session="sess-fs")):
            self.assertEqual(self.hook(text, focus=True).returncode, 0)
        self.assertEqual(len(self.sent()), 2, self.sent())
        self.assertEqual(self.asked_size(), 0)


class TestThrottleWindowProcess(HookCase):
    def test_idle_prompt_does_not_repeat_the_turn(self):
        # Конец хода и idle_prompt это один повод.
        for text in (self.event("Stop", session="sess-wait"),
                     self.event("Notification", "idle_prompt", "sess-wait")):
            self.assertEqual(self.hook(text).returncode, 0)
        self.assertEqual(len(self.sent()), 1, self.sent())

    def test_user_prompt_clears_the_mark(self):
        # Второй ход подряд снова звучит, хотя окно повода «сессия ждёт тебя»
        # ещё не вышло.
        for text in (self.event("Stop", session="sess-again"),
                     self.event("UserPromptSubmit", session="sess-again"),
                     self.event("Stop", session="sess-again")):
            self.assertEqual(self.hook(text).returncode, 0)
        self.assertEqual(len(self.sent()), 2, self.sent())

    def test_repeat_in_window_is_quiet_and_neighbour_is_not(self):
        text = self.event("Notification", "idle_prompt", "sess-window")
        self.hook(text)
        self.hook(text)
        self.assertEqual(len(self.sent()), 1, self.sent())
        self.assertIn("пропуск: повтор в окне", self.journal())
        self.hook(self.event("Notification", "idle_prompt", "sess-other"))
        self.assertEqual(len(self.sent()), 2, self.sent())
        # Журнал пишет сессию, повод, уровень и код возврата: жалоба «не
        # приходят» разбирается по нему, а «важное не отличается от фонового»
        # по уровню.
        # Сессия в журнале обрезана, отсюда и sess-win в ожидании.
        self.assertRegex(self.journal(), "сессия sess-win повод idle_prompt "
                                         "уровень громкий бэкенд .*код возврата: 0")


class TestBadEvent(HookCase):
    """Кривой вход любого вида это код 0 и строка в журнале, а не traceback:
    хук стоит в каждой сессии, и форма события целиком на стороне харнеса."""

    def test_json_not_an_object(self):
        for bad in ("42", "null", "[1,2]", '"строка"'):
            r = self.hook(bad)
            self.assertEqual(r.returncode, 0, bad)
            self.assertEqual(r.stderr, "", bad)
        self.assertIn("событие не разобрано: json не объектом", self.journal())

    def test_field_of_the_wrong_shape(self):
        r = self.hook(json.dumps({"hook_event_name": "Notification",
                                  "notification_type": {"a": 1},
                                  "session_id": "sess-bad"}))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(r.stderr, "")
        self.assertIn("сессия sess-bad повод - уровень - бэкенд - цель - задача - проект - "
                      "событие не разобрано: поля не той формы", self.journal())
        self.assertEqual(self.sent(), [])

    def test_numeric_body_keeps_the_reason(self):
        # Повод не съедается: тело собирается из того, что дали.
        r = self.hook(json.dumps({"hook_event_name": "Notification",
                                  "notification_type": "permission_prompt",
                                  "session_id": "sess-num", "cwd": "/p/devkit-dk-034",
                                  "message": 42}))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["devkit-dk-034: нужно разрешение|42"])


class TestClickTargetProcess(HookCase):
    def test_click_leads_to_the_calling_tree(self):
        # Без windowId=_blank редактор подменил бы деревом задачи то окно, в
        # котором сейчас работают.
        r = self.click(self.event("Notification", "idle_prompt", "sess-click"))
        self.assertEqual(r.returncode, 0)
        self.assertTrue(self.sent()[0].endswith(
            "-open vscode://file/p/devkit-dk-034?windowId=_blank"), self.sent())
        self.assertIn("цель vscode://file/p/devkit-dk-034?windowId=_blank "
                      "задача DK-034 проект devkit код возврата: 0", self.journal())

    def test_worktree_subagent_leads_to_the_session_window(self):
        # Субагент работает в дереве задачи, а окно сессии стоит на своём:
        # заголовок показывает оба.
        transcript = os.path.join(self.tmp, "transcript.jsonl")
        with open(transcript, "w", encoding="utf-8") as f:
            f.write('{"type":"queue-operation","sessionId":"s1"}\n'
                    '{"type":"user","cwd":"/p/devkit"}\n')
        r = self.click(json.dumps({"hook_event_name": "SubagentStop",
                                   "session_id": "sess-wt",
                                   "cwd": "/p/devkit-dk-059",
                                   "transcript_path": transcript,
                                   "agent_type": "exec-low",
                                   "last_assistant_message": "готово"}))
        self.assertEqual(r.returncode, 0)
        line = self.sent()[0]
        self.assertTrue(line.startswith("-title devkit (dk-059): субагент отработал"),
                        line)
        self.assertTrue(line.endswith("-open vscode://file/p/devkit?windowId=_blank"),
                        line)

    def test_backend_without_click_gets_no_target(self):
        r = self.hook(self.event("Notification", "idle_prompt", "sess-noclick"))
        self.assertEqual(r.returncode, 0)
        self.assertNotIn("open", self.sent()[0])
        self.assertRegex(self.journal(), "сессия sess-noc.* повод idle_prompt "
                                         "уровень громкий бэкенд .* цель - "
                                         "задача DK-034 проект devkit код возврата: 0")

    def test_cwd_not_a_string_falls_back(self):
        r = self.click(json.dumps({"hook_event_name": "Notification",
                                   "notification_type": "idle_prompt",
                                   "session_id": "sess-cwd", "cwd": 7,
                                   "message": "проба"}))
        self.assertEqual(r.returncode, 0)
        self.assertTrue(self.sent()[0].endswith("-activate com.microsoft.VSCode"),
                        self.sent())

    def test_own_target_wins(self):
        r = self.click(self.event("Notification", "idle_prompt", "sess-own"),
                       DEVKIT_NOTIFY_OPEN="x-devkit://{cwd}")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(self.sent()[0].endswith("-open x-devkit:///p/devkit-dk-034"),
                        self.sent())

    def test_empty_target_kills_the_click(self):
        r = self.click(self.event("Notification", "idle_prompt", "sess-nogo"),
                       DEVKIT_NOTIFY_OPEN="")
        self.assertEqual(r.returncode, 0)
        self.assertTrue(self.sent(), "уведомление с погашенным кликом не ушло вовсе")
        self.assertNotIn("open", self.sent()[0])


class TestNoBackend(HookCase):
    """Слать нечем: код 0, отказ в журнале и запасной путь через сам терминал."""

    def test_terminal_sequence_is_the_fallback(self):
        r = subprocess.run(
            [sys.executable, NOTIFY, "--hook", "claude-code"],
            input=self.event("Notification", "idle_prompt", "sess-none"),
            env={"HOME": self.home, "PATH": self.sysdir},
            capture_output=True, text=True)
        self.assertEqual(r.returncode, 0)
        self.assertIn("бэкенда нет", self.journal())
        self.assertIn("terminalSequence", r.stdout)


class TestSwitchOff(HookCase):
    """Выключатель гасит уведомитель целиком, в том числе аргументный режим."""

    def test_hook_and_arguments_are_both_off(self):
        r = self.hook(self.event("Notification", "idle_prompt", "sess-off"),
                      DEVKIT_NOTIFY_OFF="1")
        self.assertEqual(r.returncode, 0)
        r = self.cli("заголовок", "тело", DEVKIT_NOTIFY_OFF="1")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])


class TestDelegatedSubprocess(HookCase):
    """Подпроцесс делегирования: у его сессии никто не стоит, и хуки молчат."""

    def test_hook_is_silent_under_run_depth(self):
        r = self.hook(self.event("Stop", session="sess-run"),
                      DEVKIT_RUN_DEPTH="1")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [], "хук подпроцесса делегирования позвал наружу")
        # Молчание без следа неотличимо от сломанного канала, поэтому пропуск
        # называется в журнале.
        self.assertIn("подпроцесс делегирования", self.journal())

    def test_the_same_event_rings_without_it(self):
        # Красное на старом коде держится на этой паре: молчит от переменной, а
        # не вообще.
        r = self.hook(self.event("Stop", session="sess-plain"))
        self.assertEqual(r.returncode, 0)
        self.assertTrue(self.sent(), "без переменной конец хода перестал звонить")


class TestHookSandbox(HookCase):
    """Хук-путь фильтрует сессии из песочницы симметрично аргументному (DK-196):
    корень под TMPDIR это синтетическая доска обкатки сценария или вложенная
    headless-сессия из scratchpad, и живой баннер про неё ложный. Пропуск
    виден в журнале той же строкой, что и у аргументного пути."""

    def test_every_hook_reason_is_filtered(self):
        # Ни один повод хук-пути не звонит из песочницы: запрос разрешения,
        # ждёт ввода, конец хода и субагент проверяются по одному корню.
        # На старом коде фильтр песочницы стоял только на аргументном пути, и
        # каждый из этих поводов доходил до бэкенда.
        cases = (
            self.event("Notification", "permission_prompt", "sess-perm", cwd=self.tmp),
            self.event("Notification", "idle_prompt", "sess-idle", cwd=self.tmp),
            self.event("Stop", session="sess-turn", cwd=self.tmp),
            self.event("SubagentStop", session="sess-sub", cwd=self.tmp),
        )
        for text in cases:
            self.clear()
            r = self.hook(text)
            self.assertEqual(r.returncode, 0, text)
            self.assertEqual(self.sent(), [], text)
            self.assertIn("пропуск: песочница", self.journal())

    def test_sandbox_skip_keeps_reason_and_text(self):
        # Пропущенный баннер это про доставку: событие было, и повод с текстом
        # остаются в журнале, по ним лента дашборда хранит эту отправку.
        r = self.hook(self.event("Stop", session="sess-text", cwd=self.tmp))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])
        self.assertIn("повод turn_done уровень громкий", self.journal())
        self.assertIn("текст «%s: первая строка» «вторая»" % os.path.basename(self.tmp),
                      self.journal())

    def test_sandbox_skips_the_focus_poll(self):
        # Песочница отсекается до опроса фокуса: лишние 180 мс на корень, которого
        # через минуту нет, тратить незачем. Заголовок переднего окна тут ни при
        # чём: фокус не спрашивался вовсе.
        self.look_at("Правка notify.py - devkit-dk-034")
        r = self.hook(self.event("Stop", session="sess-nofocus", cwd=self.tmp),
                      focus=True)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])
        self.assertEqual(self.asked_size(), 0)
        self.assertIn("пропуск: песочница", self.journal())

    def test_real_root_still_rings_from_the_hook(self):
        # Симметрия: настоящий корень фильтром не зацеплен, баннер доезжает, как
        # и до правки. Ловит мутацию «sandbox_reason истинен всегда».
        r = self.hook(self.event("Stop", session="sess-real"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["devkit-dk-034: первая строка|вторая"])


class TestArgumentMode(HookCase):
    """Зовётся не только хуком, поэтому заголовок и тело идут прямо."""

    def test_title_and_body_reach_the_backend(self):
        r = self.cli("выкат", "прод обновлён")
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["выкат|прод обновлён"])
        self.assertIn("уровень громкий", self.journal())

    # regcheck:test-begin
    def test_reaches_the_backend_even_when_the_suite_itself_runs_from_tmp(self):
        # cli() раньше не задавал cwd подпроцессу и тот наследовал рабочую
        # директорию самого раннера тестов. В свежем дереве прогона слияния
        # (DK-641) раннер сам стоит под TMPDIR, sandbox_reason (DK-069) читал
        # это песочницей, deliver() не звался и sent() пустел (DK-677).
        cwd = os.getcwd()
        os.chdir(self.tmp)
        try:
            r = self.cli("выкат", "прод обновлён")
        finally:
            os.chdir(cwd)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), ["выкат|прод обновлён"])
    # regcheck:test-end

    def test_loud_by_default(self):
        r = self.cli("выкат", "прод обновлён", DEVKIT_NOTIFY_BACKEND=self.tn)
        self.assertEqual(r.returncode, 0)
        self.assertIn("-sound default", self.sent()[0])

    def test_quiet_lowers_the_level(self):
        r = self.cli("--quiet", "поезд собран", "три задачи",
                     DEVKIT_NOTIFY_BACKEND=self.tn)
        self.assertEqual(r.returncode, 0)
        self.assertTrue(self.sent()[0].startswith(
            "-title поезд собран -message три задачи -group devkit"), self.sent())

    def test_quiet_without_title_shows_help(self):
        self.assertEqual(self.cli("--quiet").returncode, 2)

    def test_call_from_a_sandbox_keeps_quiet(self):
        # Песочница вроде синтетической доски из обкатки сценария: живой баннер
        # про неё ложный (DK-069), и пропуск виден и в stdout, и в журнале.
        r = self.cli("tmp-доска", "XR-001 в Check", cwd=self.tmp)
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])
        self.assertIn("уведомление пропущено", r.stdout)
        self.assertIn("пропуск: песочница", self.journal())


class TestJournalText(unittest.TestCase):
    """Хвост строки журнала с текстом баннера: по нему лента дашборда
    показывает, о чём было уведомление, а строка без текста остаётся прежней."""

    def test_title_and_body_go_in_yolki(self):
        self.assertEqual(notify.log_text("цель DK-112: wait-human", "плановый стоп"),
                         " текст «цель DK-112: wait-human» «плановый стоп»")

    def test_no_text_leaves_the_line_as_before(self):
        self.assertEqual(notify.log_text("", ""), "")
        self.assertEqual(notify.log_text(None, None), "")

    def test_empty_body_keeps_the_pair(self):
        # Пара ёлочек всегда парой: разбор ищет второй кусок там же, где первый.
        self.assertEqual(notify.log_text("заголовок", ""), " текст «заголовок» «»")

    def test_own_yolki_do_not_break_the_tail(self):
        self.assertEqual(notify.log_text("строка «внутри»", "тело"),
                         " текст «строка <внутри>» «тело»")

    def test_line_breaks_collapse(self):
        self.assertEqual(notify.one_line("первая\nвторая   строка"), "первая вторая строка")


class TestReasonFromArguments(HookCase):
    """Повод аргументного вызова: без флага прочерк, как раньше, с флагом
    слово в журнале, по которому лента отличает стоп цикла от задачи."""

    def test_reason_reaches_the_journal(self):
        r = self.cli("--reason", "goal_stop", "цель XR-100: стоп", "витков 3")
        self.assertEqual(r.returncode, 0)
        self.assertIn("повод goal_stop", self.journal())
        self.assertIn("текст «цель XR-100: стоп» «витков 3»", self.journal())

    def test_without_the_flag_the_reason_stays_a_dash(self):
        r = self.cli("заголовок", "тело")
        self.assertEqual(r.returncode, 0)
        self.assertIn("повод - ", self.journal())

    def test_reason_goes_with_quiet(self):
        r = self.cli("--quiet", "--reason", "task_check", "XR-5 в Check", "проверка за тобой")
        self.assertEqual(r.returncode, 0)
        self.assertIn("повод task_check уровень фоновый", self.journal())

    def test_reason_without_a_word_is_red(self):
        r = self.cli("--reason")
        self.assertEqual(r.returncode, 2)
        self.assertIn("--reason ждёт повод", r.stderr)

    def test_sandbox_skip_keeps_the_reason_and_text(self):
        # Пропущенный баннер это про доставку: событие было, и в ленте оно
        # обязано остаться со своим поводом.
        r = self.cli("--reason", "run_stop", "стоп из дашборда", "сессия снята",
                     cwd=self.tmp, TMPDIR=self.tmp)
        self.assertEqual(r.returncode, 0)
        self.assertIn("повод run_stop", self.journal())
        self.assertIn("пропуск: песочница", self.journal())
        self.assertIn("текст «стоп из дашборда» «сессия снята»", self.journal())


class TestTargetInTheJournal(HookCase):
    """Задача и проект своими полями строки журнала: лента дашборда ведёт от
    события к строке доски по полю, а не по разбору текста (DK-323)."""

    def tree(self, name, branch=None):
        """Рабочее дерево прогона, при желании с веткой в HEAD."""
        path = os.path.join(self.tmp, name)
        os.makedirs(path, exist_ok=True)
        if branch:
            os.makedirs(os.path.join(path, ".git"), exist_ok=True)
            with open(os.path.join(path, ".git", "HEAD"), "w", encoding="utf-8") as f:
                f.write("ref: refs/heads/%s\n" % branch)
        return path

    def test_hook_takes_them_from_the_worktree(self):
        # Событие рождается в дереве задачи, и ID в тексте баннера не написан:
        # раньше лента такое событие показывала оторванным.
        r = self.hook(self.event("Stop", session="sess-turn"))
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача DK-034 проект devkit ", self.journal())

    def test_arguments_name_them_themselves(self):
        r = self.cli("--reason", "task_check", "--task", "XR-213",
                     "--project", "it-road-course", "XR-213 в Check", "проверка за тобой")
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача XR-213 проект it-road-course ", self.journal())

    def test_arguments_without_the_keys_read_the_tree(self):
        # Ключей нет, зато есть рабочая директория: уведомитель собирает поля
        # по ней тем же правилом, что и в хук-режиме.
        r = self.cli("выкат", "прод обновлён", cwd=self.tree("devkit", "dk-121"))
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача DK-121 проект devkit ", self.journal())

    def test_event_without_a_task_says_so_with_a_dash(self):
        r = self.cli("авария контура", "роутер DE не отвечает",
                     cwd=self.tree("devkit", "main"))
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача - проект devkit ", self.journal())

    def test_key_that_is_not_an_id_leaves_the_field_empty(self):
        # Назвали задачу словом, а не ID доски: в поле честный прочерк, лента
        # по нему никуда не ведёт и оторванным событие не выглядит.
        r = self.cli("--task", "цель", "стоп", "витков 3",
                     cwd=self.tree("devkit", "main"))
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача - проект devkit ", self.journal())

    def test_key_without_a_word_is_red(self):
        for flag, word in (("--task", "задачу"), ("--project", "проект")):
            r = self.cli(flag)
            self.assertEqual(r.returncode, 2, flag)
            self.assertIn("%s ждёт %s" % (flag, word), r.stderr)

    def test_sandbox_skip_keeps_the_fields(self):
        # Пропущенный баннер это про доставку: событие в ленте остаётся, и
        # вести от него есть куда.
        r = self.cli("--task", "XR-100", "--project", "demo", "стоп", "сессия снята",
                     cwd=self.tmp, TMPDIR=self.tmp)
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача XR-100 проект demo ", self.journal())

    def test_self_test_goes_without_a_task(self):
        r = self.cli("--self-test", cwd=self.tree("devkit", "main"))
        self.assertEqual(r.returncode, 0)
        self.assertIn("задача - проект devkit ", self.journal())


class TestSelfTest(HookCase):
    """Самопроверка говорит, чем именно послано, и краснеет, когда слать
    нечем."""

    def test_backend_is_named(self):
        r = self.cli("--self-test")
        self.assertEqual(r.returncode, 0)
        self.assertIn("послано через %s" % self.stub, r.stdout)

    def test_no_backend_is_red(self):
        r = subprocess.run([sys.executable, NOTIFY, "--self-test"],
                           env={"HOME": self.home, "PATH": self.sysdir},
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 1)
        self.assertIn("бэкенда уведомлений нет", r.stdout)


class TestLiveSamples(HookCase):
    """Образцы, снятые с работающего Claude Code, а не сочинённые."""

    def sample(self, name):
        with open(os.path.join(DATA, name), encoding="utf-8") as f:
            return f.read()

    def test_live_permission_prompt_is_filtered_as_sandbox(self):
        # Образец снят с живой сессии в scratchpad: cwd под /private/tmp, и это
        # ровно тот случай, ради которого на хук-пути стоит фильтр песочницы
        # (DK-196). Баннер про репозиторий, которого через минуту нет, ложный, и
        # пропуск виден строкой в журнале.
        r = self.hook(self.sample("notify-permission.json"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])
        self.assertIn("пропуск: песочница", self.journal())

    def test_live_permission_prompt_rings_from_a_real_root(self):
        # Тот же образец на настоящем корне звонит, как прежде: фильтр не зацепил
        # лишнего, и заголовок с телом доезжают до бэкенда. След scratchpad
        # убран из транскрипта, иначе session_tree вычитал бы его оттуда.
        event = json.loads(self.sample("notify-permission.json"))
        event["cwd"] = "/Users/x/projects/devkit"
        event["transcript_path"] = ""
        r = self.hook(json.dumps(event))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(),
                         ["devkit: нужно разрешение|Claude needs your permission"])

    def test_live_turn_failure_rings_from_a_real_root(self):
        # Образец StopFailure снят стендом DK-172 (ход убит обрывом DNS): с
        # настоящего корня упавший ход зовёт громко и называет тип ошибки, а
        # не тонет в тишине, неотличимой от штатной работы.
        event = json.loads(self.sample("turn-failed.json"))
        event["cwd"] = "/Users/x/projects/devkit"
        event["transcript_path"] = ""
        r = self.hook(json.dumps(event))
        self.assertEqual(r.returncode, 0)
        sent = self.sent()
        self.assertEqual(len(sent), 1, sent)
        self.assertTrue(sent[0].startswith("devkit: ход упал (server_error)|API Error:"), sent)

    def test_live_file_write_is_not_a_notification(self):
        r = self.hook(self.sample("write-memory-index.json"))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(self.sent(), [])

    def test_every_sample_goes_through_quietly(self):
        # Разбор у образцов общий, поэтому непонятая форма события должна
        # всплыть тут целиком, а не в одной проверке из четырёх.
        for name in sorted(os.listdir(DATA)):
            if not name.endswith(".json"):
                continue
            r = self.hook(self.sample(name))
            self.assertEqual(r.returncode, 0, (name, r.stderr))
            self.assertEqual(r.stderr, "", name)

    def test_unknown_protocol_is_named(self):
        r = subprocess.run([sys.executable, NOTIFY, "--hook", "кодекс"],
                           input=self.sample("turn-done.json"),
                           env={"HOME": self.home}, capture_output=True, text=True)
        self.assertEqual(r.returncode, 2)
        self.assertIn("не заведён", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
