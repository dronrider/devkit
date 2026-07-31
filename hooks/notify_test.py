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

    def test_title_without_cwd(self):
        _, title, _ = notify.parse_event(event(cwd="", notification_type="idle_prompt"))
        self.assertEqual(title, "сессия: ждёт ввода")


class TestPickBackend(unittest.TestCase):
    def test_macos_osascript(self):
        self.assertEqual(notify.pick_backend({}, "darwin", lambda n: "/usr/bin/" + n),
                         "osascript")
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
