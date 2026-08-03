#!/usr/bin/env python3
"""Юниты уведомителя: разбор события, выбор бэкенда, окно троттлинга, фокус
окна. Прогон целиком (стаб бэкенда, временный HOME, все поводы) живёт в test.sh.
"""
import importlib.util
import io
import json
import os
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
                      "пропуск: окно сессии в фокусе", self.journal())

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


if __name__ == "__main__":
    unittest.main(verbosity=0)
