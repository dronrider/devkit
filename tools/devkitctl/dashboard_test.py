"""Тесты носителя дашборда: launchd-агент, находки доктора и живость по
/healthz. Запускатель и поход за /healthz подставные, launchd не трогается."""
import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import dashboard
import testenv


class Fake:
    """Подставной запускатель: помнит вызовы и отвечает заданным кодом."""

    def __init__(self, code=0, out=""):
        self.calls = []
        self.code = code
        self.out = out

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        return subprocess.CompletedProcess(argv, self.code, self.out, None)

    def argv_with(self, needle):
        return [a for a in self.calls if any(needle in str(x) for x in a)]


class Printer(Fake):
    """Подставной launchctl, у которого `print` отдаёт заданный путь plist:
    так выглядит агент, взведённый чужим домом."""

    def __init__(self, path, code=0):
        super().__init__(code=code)
        self.path = path

    def __call__(self, argv, **kw):
        self.calls.append(list(argv))
        if "print" in argv:
            out = ("gui/501/x = {\n\tactive count = 1\n\tpath = %s\n"
                   "\tstate = running\n}" % self.path)
            return subprocess.CompletedProcess(argv, 0, out, None)
        return subprocess.CompletedProcess(argv, self.code, self.out, None)


def ok_fetch(port):
    return json.dumps({"ok": True, "version": "dashboard dev", "projects": 1,
                       "errors": []})


def no_wait(home):
    """Стенды прогона install/reload секрет не ждут: он тут не рождается
    вовсе, потому что launchctl подставной."""
    return ""


class Stand(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="dashboard-test-"))
        self.addCleanup(shutil.rmtree, str(self.dir), True)
        self.home = self.dir / "home"
        (self.home / ".devkit").mkdir(parents=True)
        self.binary = self.dir / "bin" / "dashboard"
        self.binary.parent.mkdir(parents=True)
        self.binary.write_text("#!/bin/sh\n", encoding="utf-8")
        self.plist = self.home / "Library" / "LaunchAgents" / ("%s.plist" % dashboard.LABEL)

    def check(self, fix=False, call=None, platform="darwin", binary=True,
              fetch=ok_fetch, from_main=True, machine=None, waiter=no_wait,
              tty=None, agent=None):
        # Дом стенда объявляется домом машины: без этого проверка шла бы по
        # ветке подставного дома (DK-588) и launchd не трогала бы вовсе.
        # waiter по умолчанию не ждёт: реальное ожидание секрета разбирает
        # LoginLineTest своим стендом, а тут оно било бы по каждому прогону
        # install/reload пятисекундной паузой. tty/agent по умолчанию не
        # подменяются: под тестовым раннером stdout и так не терминал, и
        # тесты, которым нужно значение секрета в строке, подменяют явно.
        call = Fake() if call is None else call
        which = (lambda name: str(self.binary)) if binary else (lambda name: None)
        f, d = dashboard.check(fix=fix, main=self.dir / "devkit", from_main=from_main,
                               home=self.home, platform=platform,
                               call=call, which=which, fetch=fetch,
                               machine=self.home if machine is None else machine,
                               waiter=waiter, tty=tty, agent=agent)
        return f, d, call


class AgentTest(Stand):
    def test_missing_binary_is_a_finding(self):
        f, d, call = self.check(binary=False)
        self.assertEqual(len(f), 1, f)
        self.assertIn("devkitctl update", f[0])
        self.assertEqual(d, [])
        self.assertEqual(call.calls, [])

    def test_missing_agent_is_a_finding(self):
        f, d, _ = self.check()
        self.assertEqual(len(f), 1, f)
        self.assertIn("doctor --fix", f[0])
        self.assertEqual(d, [])

    def test_fix_installs_and_loads(self):
        f, d, call = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertTrue(self.plist.exists(), "агент не положен")
        text = self.plist.read_text(encoding="utf-8")
        self.assertIn("<string>%s</string>" % self.binary, text)
        self.assertIn("<string>serve</string>", text)
        self.assertIn("<key>KeepAlive</key><true/>", text)
        self.assertIn("<key>EnvironmentVariables</key>", text)
        self.assertIn("<key>PATH</key><string>%s" % self.binary.parent, text)
        self.assertTrue(call.argv_with("bootstrap"), "launchd не позван: %s" % call.calls)

    def test_agent_pointing_elsewhere(self):
        self.check(fix=True)
        old = self.plist.read_text(encoding="utf-8").replace(str(self.binary), "/старый/бинарь")
        self.plist.write_text(old, encoding="utf-8")
        f, _, _ = self.check()
        self.assertIn("/старый/бинарь", f[0])
        f, d, _ = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn(str(self.binary), self.plist.read_text(encoding="utf-8"))

    def test_agent_path_parts(self):
        # PATH агента: каталог бинаря первым, системные пути launchd следом,
        # без дублей, когда бинарь и так лежит в системном каталоге.
        path = dashboard.agent_path(str(self.binary))
        parts = path.split(":")
        self.assertEqual(parts[0], str(self.binary.parent))
        for p in dashboard.SYSTEM_PATH:
            self.assertIn(p, parts)
        dup = dashboard.agent_path("/usr/bin/dashboard").split(":")
        self.assertEqual(len(dup), len(set(dup)), "дубли в PATH агента: %s" % dup)
        self.assertEqual(dup[0], "/usr/bin")

    def test_agent_path_includes_existing_local_bin(self):
        # claude на прод-машине стоит символьной ссылкой в ~/.local/bin
        # (DK-247), и agent_path берёт его тем же правилом, что и брю: по
        # наличию каталога, а не срезом живого PATH.
        fake = self.dir / "fakehome-with-local-bin"
        (fake / ".local" / "bin").mkdir(parents=True)
        with testenv.fake_home(fake):
            path = dashboard.agent_path(str(self.binary))
        self.assertIn(str(fake / ".local" / "bin"), path.split(":"))

    def test_agent_path_skips_missing_local_bin(self):
        fake = self.dir / "fakehome-without-local-bin"
        fake.mkdir()
        with testenv.fake_home(fake):
            path = dashboard.agent_path(str(self.binary))
        self.assertNotIn(".local", path)

    def test_old_agent_without_path_is_rewritten(self):
        # Агент, положенный до EnvironmentVariables, это находка, и --fix
        # переписывает его: иначе дефект PATH пережил бы доводку.
        self.check(fix=True)
        text = self.plist.read_text(encoding="utf-8")
        head, tail = text.split("  <key>EnvironmentVariables</key>\n", 1)
        old = head + tail.split("  </dict>\n", 1)[1]
        self.plist.write_text(old, encoding="utf-8")
        f, d, _ = self.check()
        self.assertEqual(len(f), 1, f)
        f, d, _ = self.check(fix=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("<key>EnvironmentVariables</key>",
                      self.plist.read_text(encoding="utf-8"))

    def test_old_agent_without_local_bin_is_rewritten(self):
        # Агент, положенный до появления ~/.local/bin в PATH (DK-247), это
        # находка, и --fix переписывает его тем же правилом, что и старый
        # агент вовсе без EnvironmentVariables.
        fake = self.dir / "fakehome-rewrite-local-bin"
        (fake / ".local" / "bin").mkdir(parents=True)
        local_bin = str(fake / ".local" / "bin")
        with testenv.fake_home(fake):
            self.check(fix=True)
            text = self.plist.read_text(encoding="utf-8")
            self.assertIn(local_bin, text)
            old = text.replace(":" + local_bin, "")
            self.assertNotIn(local_bin, old)
            self.plist.write_text(old, encoding="utf-8")
            f, d, _ = self.check()
            self.assertEqual(len(f), 1, f)
            f, d, _ = self.check(fix=True)
            self.assertEqual(f, [])
            self.assertEqual(len(d), 1, d)
            self.assertIn(local_bin, self.plist.read_text(encoding="utf-8"))

    def test_fix_from_worktree_refuses(self):
        # На машину едет только проверенное: с worktree ветки задачи plist не
        # кладётся, доводка отсылает в основной чекаут.
        f, d, call = self.check(fix=True, from_main=False)
        self.assertEqual(d, [])
        self.assertIn("основного чекаута", f[0])
        self.assertFalse(self.plist.exists())
        self.assertEqual(call.calls, [])

    def test_installed_but_not_loaded(self):
        self.check(fix=True)
        dead = Fake(code=113)
        f, d, _ = self.check(call=dead)
        self.assertEqual(len(f), 1, f)
        self.assertIn("не поднят", f[0])
        self.assertEqual(d, [])

    def test_launchctl_refusal_is_named(self):
        broken = Fake(code=5, out="Bootstrap failed")
        f, d, _ = self.check(fix=True, call=broken)
        self.assertEqual(len(f), 1, f)
        self.assertIn("не взял", f[0])
        self.assertEqual(d, [])

    def test_other_platform_quiet_without_use(self):
        f, d, _ = self.check(platform="linux", binary=False)
        self.assertEqual((f, d), ([], []))

    def test_other_platform_names_systemd_when_used(self):
        f, d, _ = self.check(platform="linux")
        self.assertEqual(len(f), 1, f)
        self.assertIn("systemd", f[0])


class ForeignHomeTest(Stand):
    """Доводка под подставным домом: DK-588, метка агента одна на машину."""

    def foreign(self):
        return self.dir / "machine-home"

    def test_fix_lays_plist_but_leaves_launchd_alone(self):
        f, d, call = self.check(fix=True, machine=self.foreign())
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("подставном доме", d[0])
        self.assertIn(str(self.home), d[0])
        self.assertEqual(call.calls, [], "launchd тронут из подставного дома")
        self.assertTrue(self.plist.exists(), "plist в подставной дом не лёг")

    def test_ready_home_is_quiet_and_silent_about_machine(self):
        # Разложенный подставной дом ничего не говорит про службы машины: там
        # поднят агент пользователя, а не тот, что описан этим plist.
        self.check(fix=True, machine=self.foreign())
        f, d, call = self.check(fix=True, machine=self.foreign())
        self.assertEqual((f, d), ([], []))
        self.assertEqual(call.calls, [])


class HijackTest(Stand):
    """Находка перехваченного агента: plist в доме совпадает с эталоном, а
    поднят launchd чужим файлом (DK-588)."""

    def thief_path(self):
        return "/private/tmp/чужая-сессия/Library/LaunchAgents/%s.plist" % dashboard.LABEL

    def test_agent_from_another_plist_is_a_finding(self):
        self.check(fix=True)
        thief = Printer(self.thief_path())
        f, d, _ = self.check(call=thief)
        self.assertEqual(len(f), 1, f)
        self.assertIn(self.thief_path(), f[0])
        self.assertEqual(d, [])

    def test_fix_takes_the_agent_back(self):
        self.check(fix=True)
        thief = Printer(self.thief_path())
        f, d, _ = self.check(fix=True, call=thief)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn(self.thief_path(), d[0])
        self.assertTrue(thief.argv_with("bootstrap"),
                        "агента не вернули: %s" % thief.calls)

    def test_own_plist_is_quiet(self):
        self.check(fix=True)
        own = Printer(str(self.plist))
        f, d, _ = self.check(call=own)
        self.assertEqual((f, d), ([], []))


class ProbeTest(Stand):
    def loaded_check(self, fetch):
        (self.home / ".devkit" / "dashboard.local").write_text(
            "root = /x\ntoken = abc\n", encoding="utf-8")
        self.check(fix=True)
        return self.check(fetch=fetch)

    def test_healthy_dashboard_is_quiet(self):
        f, d, _ = self.loaded_check(ok_fetch)
        self.assertEqual((f, d), ([], []))

    def test_no_token_skips_the_probe(self):
        # Root в конфиг кладёт уже ensure_conf при взводе, а секрета там ещё
        # нет: значит сервер ни разу не стартовал (секрет кладёт только сам
        # serve), и стучаться в /healthz некуда.
        self.check(fix=True)
        conf = self.home / ".devkit" / "dashboard.local"
        self.assertIn("root = ", conf.read_text(encoding="utf-8"))
        self.assertNotIn("token", conf.read_text(encoding="utf-8"))

        def boom(port):
            raise AssertionError("probe не должен ходить в сеть без конфига")
        f, d, _ = self.check(fetch=boom)
        self.assertEqual((f, d), ([], []))

    def test_dead_healthz_is_a_finding(self):
        def dead(port):
            raise OSError("connection refused")
        f, _, _ = self.loaded_check(dead)
        self.assertEqual(len(f), 1, f)
        self.assertIn("/healthz", f[0])
        self.assertIn("dashboard.log", f[0])

    def test_config_errors_are_named(self):
        def errs(port):
            return json.dumps({"ok": True, "errors": ["в конфиге нет ни одной строки root"]})
        f, _, _ = self.loaded_check(errs)
        self.assertEqual(len(f), 1, f)
        self.assertIn("root", f[0])

    def test_foreign_answer_is_a_finding(self):
        f, _, _ = self.loaded_check(lambda port: "<html>чужой сервер</html>")
        self.assertEqual(len(f), 1, f)
        self.assertIn("не своим /healthz", f[0])


class ConfTest(Stand):
    def test_port_default_and_override(self):
        self.assertEqual(dashboard.conf_port(self.home), dashboard.DEFAULT_PORT)
        conf = self.home / ".devkit" / "dashboard.local"
        conf.write_text("root = /x\nport = 7200\n", encoding="utf-8")
        self.assertEqual(dashboard.conf_port(self.home), 7200)
        conf.write_text("port = мусор\n", encoding="utf-8")
        self.assertEqual(dashboard.conf_port(self.home), dashboard.DEFAULT_PORT)

    def test_probe_uses_configured_port(self):
        conf = self.home / ".devkit" / "dashboard.local"
        conf.write_text("port = 7300\n", encoding="utf-8")
        seen = []

        def spy(port):
            seen.append(port)
            return ok_fetch(port)

        dashboard.probe(self.home, spy)
        self.assertEqual(seen, [7300])


class EnsureConfTest(Stand):
    """Строка root по умолчанию (DK-822): доктор заводит конфиг сам, вместо
    молчаливого «нет ни одной строки root» на первом же /healthz."""

    def conf(self):
        return self.home / ".devkit" / "dashboard.local"

    def test_missing_conf_gets_default_root(self):
        main = self.dir / "projects" / "devkit"
        main.mkdir(parents=True)
        dashboard.ensure_conf(self.home, main)
        text = self.conf().read_text(encoding="utf-8")
        self.assertEqual(text, "root = %s\n" % main.resolve().parent)
        self.assertEqual(oct(self.conf().stat().st_mode & 0o777), oct(0o600))

    def test_existing_conf_is_untouched(self):
        self.conf().parent.mkdir(parents=True, exist_ok=True)
        self.conf().write_text("addr = 127.0.0.1\n", encoding="utf-8")
        dashboard.ensure_conf(self.home, self.dir / "projects" / "devkit")
        self.assertEqual(self.conf().read_text(encoding="utf-8"), "addr = 127.0.0.1\n")

    def test_check_fix_writes_root_before_arming(self):
        # До взвода агента: written раньше плиста, поэтому первый же старт
        # serve видит готовый root, а не пустой список.
        f, d, call = self.check(fix=True)
        self.assertEqual(f, [])
        text = self.conf().read_text(encoding="utf-8")
        self.assertEqual(text, "root = %s\n" % (self.dir / "devkit").resolve().parent)

    def test_check_without_fix_leaves_conf_alone(self):
        self.check(fix=False)
        self.assertFalse(self.conf().exists(), "без --fix конфиг заводиться не должен")


class LoginLineTest(Stand):
    """Адрес и токен в хвосте update/doctor --fix (DK-822): токен печатается,
    только когда родился на этом же прогоне, и только человеку за терминалом
    вне агентской сессии (замечание ревью: секрет не должен ехать в контекст
    и транскрипт агента, doctor --fix зовут и агенты)."""

    def test_had_token_is_quiet_about_the_secret(self):
        def boom(home):
            raise AssertionError("не должен ждать старый токен")
        line = dashboard.login_line(self.home, had_token=True, waiter=boom)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, line)
        self.assertNotIn("токен", line)

    def test_human_terminal_sees_the_value(self):
        # Терминал человека (isatty), не агентская сессия: значение печатается.
        line = dashboard.login_line(self.home, had_token=False,
                                    waiter=lambda h: "секрет123", tty=True, agent=False)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, line)
        self.assertIn("секрет123", line)

    def test_agent_terminal_hides_the_value(self):
        # Та же tty, но сессия агентская (CLAUDECODE=1 в окне): вывод всё
        # равно едет в контекст модели, значение прятать.
        line = dashboard.login_line(self.home, had_token=False,
                                    waiter=lambda h: "секрет123", tty=True, agent=True)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, line)
        self.assertNotIn("секрет123", line, "агентская сессия увидела значение секрета")
        self.assertIn("dashboard.local", line)
        self.assertIn("dashboard secret", line)

    def test_non_terminal_hides_the_value_and_is_not_silent(self):
        # Вывод не в терминал (перенаправлен, захвачен подпроцессом): значение
        # прячется, но строка не пустая, место и способ посмотреть названы.
        line = dashboard.login_line(self.home, had_token=False,
                                    waiter=lambda h: "секрет123", tty=False, agent=False)
        self.assertTrue(line, "вывод не в терминал не должен молчать")
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, line)
        self.assertNotIn("секрет123", line, "не-терминал увидел значение секрета")
        self.assertIn("dashboard.local", line)
        self.assertIn("dashboard secret", line)

    def test_timed_out_wait_still_prints_the_address(self):
        line = dashboard.login_line(self.home, had_token=False, waiter=lambda h: "",
                                    tty=True, agent=False)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, line)
        self.assertIn("dashboard secret", line)

    def test_default_tty_and_agent_read_real_environment(self):
        # Без подмены login_line спрашивает настоящие sys.stdout.isatty() и
        # CLAUDECODE: под тестовым раннером stdout не терминал, значит
        # значение прячется само, без явного tty=False.
        line = dashboard.login_line(self.home, had_token=False, waiter=lambda h: "секрет123")
        self.assertNotIn("секрет123", line)

    def test_wait_for_token_polls_without_real_sleep(self):
        # serve дописывает секрет не мгновенно (KeepAlive поднимает процесс
        # чуть позже bootstrap): подставной sleep сам кладёт файл на втором
        # тике, вместо того чтобы ждать секунды по-настоящему.
        conf = self.home / ".devkit" / "dashboard.local"
        conf.parent.mkdir(parents=True, exist_ok=True)
        conf.write_text("root = /x\n", encoding="utf-8")
        ticks = []

        def fake_sleep(seconds):
            ticks.append(seconds)
            if len(ticks) == 2:
                conf.write_text("root = /x\ntoken = abc\n", encoding="utf-8")

        got = dashboard.wait_for_token(self.home, timeout=10, poll=0.01, sleep=fake_sleep)
        self.assertEqual(got, "abc")
        self.assertEqual(len(ticks), 2)

    def test_wait_for_token_gives_up_after_timeout(self):
        calls = []

        def fake_sleep(seconds):
            calls.append(seconds)

        got = dashboard.wait_for_token(self.home, timeout=0.05, poll=0.01, sleep=fake_sleep)
        self.assertEqual(got, "")
        self.assertTrue(calls, "ожидание не сделало ни одного тика")


class AgenticSessionTest(unittest.TestCase):
    def test_claudecode_env_is_agentic(self):
        self.assertTrue(dashboard.agentic_session({"CLAUDECODE": "1"}))

    def test_other_or_missing_env_is_not_agentic(self):
        self.assertFalse(dashboard.agentic_session({}))
        self.assertFalse(dashboard.agentic_session({"CLAUDECODE": "0"}))
        self.assertFalse(dashboard.agentic_session({"CLAUDECODE": "true"}))


class InstallLoginTest(Stand):
    """Полный ход через check(): адрес в хвосте первой установки и на
    последующих здоровых прогонах, без реального ожидания секрета."""

    def test_fresh_install_names_address_and_token(self):
        # Терминал человека вне агентской сессии: значение секрета видно.
        f, d, call = self.check(fix=True, waiter=lambda h: "новый-секрет",
                                tty=True, agent=False)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, d[0])
        self.assertIn("новый-секрет", d[0])

    def test_fresh_install_in_agent_session_hides_the_value(self):
        # Тот же взвод агента launchd, но сессия агентская: значение секрета
        # не должно уехать в вывод инструмента, который читает модель.
        f, d, call = self.check(fix=True, waiter=lambda h: "новый-секрет",
                                tty=True, agent=True)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, d[0])
        self.assertNotIn("новый-секрет", d[0], "агентская сессия увидела значение секрета")
        self.assertIn("dashboard secret", d[0])

    def test_fresh_install_without_terminal_hides_the_value(self):
        f, d, call = self.check(fix=True, waiter=lambda h: "новый-секрет",
                                tty=False, agent=False)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, d[0])
        self.assertNotIn("новый-секрет", d[0], "не-терминал увидел значение секрета")
        self.assertIn("dashboard secret", d[0])

    def test_steady_state_prints_one_address_line(self):
        (self.home / ".devkit" / "dashboard.local").write_text(
            "root = /x\ntoken = abc\n", encoding="utf-8")
        self.check(fix=True)
        f, d, _ = self.check(fix=True, fetch=ok_fetch)
        self.assertEqual(f, [])
        self.assertEqual(len(d), 1, d)
        self.assertIn("http://localhost:%d/login" % dashboard.DEFAULT_PORT, d[0])
        self.assertNotIn("abc", d[0], "старый токен не должен печататься заново")

    def test_steady_state_without_fix_is_quiet(self):
        (self.home / ".devkit" / "dashboard.local").write_text(
            "root = /x\ntoken = abc\n", encoding="utf-8")
        self.check(fix=True)
        f, d, _ = self.check(fetch=ok_fetch)
        self.assertEqual((f, d), ([], []))


if __name__ == "__main__":
    unittest.main()
