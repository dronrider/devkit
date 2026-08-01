#!/usr/bin/env python3
"""Юниты уведомителя: разбор события, выбор бэкенда, окно троттлинга.
Прогон целиком (стаб бэкенда, временный HOME, все поводы) живёт в test.sh.
"""
import importlib.util
import os
import tempfile
import time
import unittest

spec = importlib.util.spec_from_file_location(
    "notify", os.path.join(os.path.dirname(os.path.abspath(__file__)), "notify.py"))
notify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notify)


def event(**kw):
    base = {"hook_event_name": "Notification", "cwd": "/Users/x/projects/devkit-dk-034",
            "session_id": "f07df579-b9ca"}
    base.update(kw)
    return base


class TestParseEvent(unittest.TestCase):
    def test_action_reasons(self):
        for kind, label in (("permission_prompt", "нужно разрешение"),
                            ("agent_needs_input", "нужен ответ"),
                            ("elicitation_dialog", "диалог MCP"),
                            ("idle_prompt", "ждёт ввода")):
            key, title, body = notify.parse_event(
                event(notification_type=kind, message="Claude ждёт"))
            self.assertEqual(key, kind)
            self.assertEqual(title, "devkit-dk-034: %s" % label)
            self.assertEqual(body, "Claude ждёт")

    def test_silent_reasons(self):
        for kind in ("auth_success", "elicitation_complete", "elicitation_response", ""):
            self.assertIsNone(notify.parse_event(event(notification_type=kind)))

    def test_other_events(self):
        # Stop не подключается вовсе: уведомление на каждом ответе это фон.
        for name in ("Stop", "PreToolUse", "SessionStart"):
            self.assertIsNone(notify.parse_event(event(hook_event_name=name)))

    def test_subagent_takes_first_line(self):
        key, title, body = notify.parse_event(event(
            hook_event_name="SubagentStop", agent_type="review-high",
            last_assistant_message="\n\nЗамечаний нет\nдалее детали\nи ещё"))
        self.assertEqual(key, "subagent_stop")
        self.assertEqual(title, "devkit-dk-034: субагент отработал")
        self.assertEqual(body, "review-high: Замечаний нет")

    def test_empty_body(self):
        _, _, body = notify.parse_event(event(notification_type="idle_prompt", message=""))
        self.assertEqual(body, "")
        _, _, body = notify.parse_event(event(hook_event_name="SubagentStop",
                                              agent_type="exec-low"))
        self.assertEqual(body, "exec-low")

    def test_body_is_cut(self):
        _, _, body = notify.parse_event(event(notification_type="idle_prompt",
                                              message="ы" * 500))
        self.assertEqual(len(body), notify.BODY_LIMIT)
        self.assertTrue(body.endswith("..."))

    def test_fields_of_wrong_type(self):
        # Поля события приходят от харнеса, и их форма это его дело, а не наше:
        # число вместо строки не должно ни ронять хук, ни съедать повод.
        key, title, body = notify.parse_event(event(
            notification_type="permission_prompt", message=42, cwd=7))
        self.assertEqual((key, title, body), ("permission_prompt", "сессия: нужно разрешение", "42"))
        _, _, body = notify.parse_event(event(hook_event_name="SubagentStop",
                                              agent_type=1, last_assistant_message=None))
        self.assertEqual(body, "1")

    def test_unhashable_reason(self):
        # Повод объектом по словарю поводов не ищется: разбор роняет TypeError,
        # и ловит его хук, а не разбор.
        with self.assertRaises(TypeError):
            notify.parse_event(event(notification_type={"a": 1}))

    def test_title_without_cwd(self):
        _, title, _ = notify.parse_event(event(cwd="", notification_type="idle_prompt"))
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
            notify.session_tree({"transcript_path": path,
                                 "cwd": "/Users/x/projects/it-road-course-irc-75"}),
            "/Users/x/projects/it-road-course")

    def test_broken_transcript_falls_back_to_cwd(self):
        # Транскрипта нет, он битый или cwd в нём не попался: цель собирается
        # из cwd события, как до появления разбора транскрипта.
        cases = (self.write("не json", '{"type":"user"}'),
                 self.write(*['{"type":"queue-operation"}'] * 40),
                 os.path.join(self.dir, "нет-такого.jsonl"), "", None, 7)
        for path in cases:
            self.assertEqual(notify.session_tree({"transcript_path": path,
                                                  "cwd": "/p/dk"}), "/p/dk", path)
        self.assertEqual(notify.session_tree({"transcript_path": "", "cwd": 7}), "")

    def test_scan_stops_before_the_whole_file(self):
        # Транскрипт вырастает на десятки мегабайт, и читать его целиком ради
        # cwd хук не должен: cwd лежит в первых записях или не лежит вовсе.
        # Сорок служебных записей это заведомо дальше предела, и такой cwd уже
        # не берётся; число тут своё, из предела оно не считается, иначе тест
        # подстроился бы под любой предел.
        path = self.write(*(['{"type":"queue-operation"}'] * 40
                            + ['{"type":"user","cwd":"/p/поздно"}']))
        self.assertEqual(notify.session_tree({"transcript_path": path, "cwd": "/p/dk"}),
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
        _, title, _ = notify.parse_event(
            event(cwd="/p/it-road-course-irc-75", notification_type="permission_prompt"),
            "/p/it-road-course")
        self.assertEqual(title, "it-road-course (irc-75): нужно разрешение")


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

    def test_backend_argv(self):
        self.assertEqual(notify.backend_argv("/usr/bin/notify-send", "заголовок", "тело"),
                         ["/usr/bin/notify-send", "заголовок", "тело"])
        argv = notify.backend_argv("/usr/bin/osascript", 'сессия "dk"', "тело")
        self.assertEqual(argv[:2], ["/usr/bin/osascript", "-e"])
        self.assertEqual(argv[2],
                         'display notification "тело" with title "сессия \\"dk\\""')

    def test_terminal_notifier_argv(self):
        target = ("-open", "vscode://file/p/dk")
        self.assertEqual(
            notify.backend_argv("/bin/terminal-notifier", "заголовок", "тело", target),
            ["/bin/terminal-notifier", "-title", "заголовок", "-message", "тело",
             "-open", "vscode://file/p/dk"])
        # Без цели уведомление всё равно уходит, просто не кликается.
        self.assertEqual(
            notify.backend_argv("/bin/terminal-notifier", "заголовок", "тело"),
            ["/bin/terminal-notifier", "-title", "заголовок", "-message", "тело"])
        # Пустое тело она показала бы пустой строкой.
        self.assertEqual(
            notify.backend_argv("/bin/terminal-notifier", "заголовок", "")[4], "заголовок")

    def test_backends_without_click_ignore_target(self):
        target = ("-open", "vscode://file/p/dk")
        self.assertEqual(notify.backend_argv("/usr/bin/notify-send", "з", "т", target),
                         ["/usr/bin/notify-send", "з", "т"])
        self.assertEqual(len(notify.backend_argv("/usr/bin/osascript", "з", "т", target)), 3)


class TestThrottle(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def test_window_on_both_sides(self):
        now = 1000.0
        self.assertTrue(notify.allow("sess1", "idle_prompt", now, self.dir))
        self.assertFalse(notify.allow("sess1", "idle_prompt", now + 1, self.dir))
        self.assertFalse(notify.allow("sess1", "idle_prompt",
                                      now + notify.WINDOW - 0.5, self.dir))
        self.assertTrue(notify.allow("sess1", "idle_prompt",
                                     now + notify.WINDOW, self.dir))

    def test_reasons_do_not_mute_each_other(self):
        now = 1000.0
        self.assertTrue(notify.allow("sess1", "idle_prompt", now, self.dir))
        self.assertTrue(notify.allow("sess1", "permission_prompt", now, self.dir))

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


class TestTerminalSequence(unittest.TestCase):
    def test_leading_digit_is_pushed(self):
        # Тело OSC 9 с цифры харнес отбрасывает по своему белому списку.
        seq = notify.terminal_sequence("42-проект: ждёт ввода", "")
        self.assertTrue(seq.startswith("\033]9; 42-проект"))
        self.assertTrue(seq.endswith("\007"))
        self.assertTrue(notify.terminal_sequence("devkit: ждёт ввода", "текст")
                        .startswith("\033]9;devkit: ждёт ввода. текст"))


if __name__ == "__main__":
    unittest.main(verbosity=0)
