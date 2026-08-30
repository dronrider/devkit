#!/usr/bin/env python3
"""Установка и обновление devkit: разбор режима чекаута, отказы, скачивание
релиза и подмена бинарей.

Гитхаб в тестах не участвует: релиз отдаёт локальный сервер на 127.0.0.1, а
адрес ему подставляется переменной DEVKIT_RELEASE_BASE. Бинари в тарболле это
скрипты, печатающие строку версии: проверяется тут не компилятор, а то, что
вокруг него. Живой прогон в чистом окружении с убранным из PATH go стоит
сценарием проверки задачи, тестом он не заменяется.
"""
import hashlib
import os
import shutil
import tempfile
import threading
import time
import unittest
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import build
import update
from testenv import (BINARY_STUB, PY, RELEASE_MTIME, executable, git, run, stub_release,
                     write)

TOOLS = ("alfactl", "betactl")
GO_MOD = "module github.com/dronrider/devkit/tools/%s\n\ngo 1.26\n"


def driver_text(mark):
    """Драйвер вместо настоящего devkitctl.py: он же цель самоперезапуска, поэтому
    обязан понимать --restarted и лежать там, куда показывает execv. Метка в нём
    говорит, чей код сейчас работает, ветки или тега.
    """
    return ("#!%s\n"
            "import os\n"
            "import sys\n"
            "here = os.path.dirname(os.path.abspath(__file__))\n"
            "sys.path.insert(0, here)\n"
            "import update\n"
            "\n"
            "print(%r)\n"
            "sys.exit(update.run(os.path.dirname(os.path.dirname(here)), True,\n"
            "                    pin='--pin' in sys.argv,\n"
            "                    restarted='--restarted' in sys.argv))\n" % (PY, mark))


class Requests(SimpleHTTPRequestHandler):
    seen = []

    def log_message(self, *a):
        pass

    def do_GET(self):
        Requests.seen.append(self.path)
        SimpleHTTPRequestHandler.do_GET(self)


class UpdateCase(unittest.TestCase):
    """Синтетический devkit: bare-origin, клон на ветке main, два релизных тега
    и локальный сервер релизов.

      commit1 -> v0.9.0     commit2 -> v0.10.0     commit3 -> main
    """

    NAMES = TOOLS

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-update-test-"))
        self.origin = self.root / "origin.git"
        run(["git", "init", "--bare", "-q", str(self.origin)])
        self.dk = self.root / "devkit"
        self.dk.mkdir()
        git(self.dk, "init", "-q")
        git(self.dk, "config", "user.name", "t")
        git(self.dk, "config", "user.email", "t@t")
        git(self.dk, "checkout", "-q", "-b", "main")
        for name in self.NAMES:
            write(self.dk / "tools" / name / "go.mod", GO_MOD % name)
        here = Path(__file__).resolve().parent
        self.driver = self.dk / "tools" / "devkitctl" / "devkitctl.py"
        self.driver.parent.mkdir(parents=True, exist_ok=True)
        for module in ("update.py", "build.py", "say.py"):
            shutil.copy(str(here / module), str(self.driver.parent / module))
        self.mark(self.driver, "код первого тега")
        self.commit("init")
        git(self.dk, "tag", "v0.9.0")
        self.mark(self.driver, "код тега")
        self.commit("второй")
        git(self.dk, "tag", "v0.10.0")
        self.tag_commit = self.short("HEAD")
        self.mark(self.driver, "код ветки")
        self.commit("третий")
        git(self.dk, "remote", "add", "origin", str(self.origin))
        git(self.dk, "push", "-q", "--tags", "origin", "main")

        self.dest = self.root / "bin"
        self.dest.mkdir()
        self.releases = self.root / "releases"
        stub_release(self.releases, "v0.10.0", self.tag_commit, self.NAMES)
        Requests.seen = []
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), self.handler())
        # poll_interval меньше секунды по умолчанию: shutdown ждёт конца цикла
        # опроса, и полсекунды на каждый из полусотни tearDown собирались в
        # десятки секунд сюиты, хотя запросов у теста единицы.
        threading.Thread(target=self.server.serve_forever,
                         kwargs={"poll_interval": 0.02}, daemon=True).start()
        self.saved = {k: os.environ.get(k) for k in
                      (update.BIN_ENV, update.RELEASE_ENV, "PATH")}
        os.environ[update.BIN_ENV] = str(self.dest)
        os.environ[update.RELEASE_ENV] = "http://127.0.0.1:%d" % self.server.server_port
        os.environ["PATH"] = "%s%s%s" % (self.dest, os.pathsep, os.environ["PATH"])
        self.said = []

    def handler(self):
        directory = str(self.releases)

        class Bound(Requests):
            def __init__(self, *a, **kw):
                SimpleHTTPRequestHandler.__init__(self, *a, directory=directory, **kw)
        return Bound

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        for k, v in self.saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        shutil.rmtree(str(self.root), ignore_errors=True)

    def mark(self, path, text):
        write(path, driver_text(text))
        os.chmod(str(path), 0o755)

    def commit(self, message):
        git(self.dk, "add", "-A")
        git(self.dk, "commit", "-qm", message)

    def short(self, rev):
        return git(self.dk, "rev-parse", "--short=12", rev)[1].strip()

    def log(self, line):
        self.said.append(str(line))

    def out(self):
        return "\n".join(self.said)

    def update(self, **kw):
        kw.setdefault("log", self.log)
        kw.setdefault("err", self.log)
        return update.run(self.dk, True, **kw)

    def head(self):
        return git(self.dk, "rev-parse", "HEAD")[1].strip()

    def installed(self):
        return sorted(p.name for p in self.dest.iterdir())


class PlatformTest(unittest.TestCase):
    """Разбор имени ассета по платформе."""

    def test_asset_per_pair(self):
        # Пары перечислены руками: имя, собранное тем же кодом, сойдётся при
        # любом шаблоне.
        for goos, goarch in (("darwin", "amd64"), ("darwin", "arm64"),
                             ("linux", "amd64"), ("linux", "arm64")):
            name, bad = update.asset_name("v0.2.0", (goos, goarch))
            self.assertIsNone(bad)
            self.assertEqual(name, "devkit-v0.2.0-%s-%s.tar.gz" % (goos, goarch))

    def test_host_pair_by_default(self):
        name, bad = update.asset_name("v0.2.0")
        self.assertIsNone(bad)
        self.assertEqual(name, build.ASSET % (("v0.2.0",) + build.host_pair()))

    def test_unknown_pair_is_named(self):
        # Молчаливой попытки скачать несуществующий ассет тут нет: отказ
        # называет пару, под которую релиза не выкладывается.
        name, bad = update.asset_name("v0.2.0", ("windows", "amd64"))
        self.assertIsNone(name)
        self.assertIn("windows/amd64", bad)
        self.assertIn("darwin/arm64", bad, "отказ не перечисляет пары релиза")


class BinDirTest(unittest.TestCase):
    """Выбор каталога назначения (решение 7): переменная, PATH, умолчание."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-bindir-test-"))
        self.saved = {k: os.environ.get(k) for k in (update.BIN_ENV, "HOME", "PATH")}
        os.environ["HOME"] = str(self.root)
        os.environ.pop(update.BIN_ENV, None)

    def tearDown(self):
        for k, v in self.saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        shutil.rmtree(str(self.root), ignore_errors=True)

    def test_named_directory_wins(self):
        os.environ[update.BIN_ENV] = str(self.root / "своё")
        os.environ["PATH"] = str(self.root / "go" / "bin")
        self.assertEqual(update.bin_dir(), self.root / "своё")

    def test_first_of_two_already_in_path(self):
        # Существование каталога тут ни при чём, меряется PATH: на живой машине
        # бинари лежат в ~/go/bin, и трогать их с места не за чем.
        (self.root / ".local" / "bin").mkdir(parents=True)
        os.environ["PATH"] = "/usr/bin:%s" % (self.root / "go" / "bin")
        self.assertEqual(update.bin_dir(), self.root / "go" / "bin")
        os.environ["PATH"] = "/usr/bin:%s" % (self.root / ".local" / "bin")
        self.assertEqual(update.bin_dir(), self.root / ".local" / "bin")

    def test_go_bin_wins_when_both_are_in_path(self):
        # Оба в PATH: выигрывает ~/go/bin, там уже лежат бинари живой машины.
        os.environ["PATH"] = "%s:%s" % (self.root / ".local" / "bin", self.root / "go" / "bin")
        self.assertEqual(update.bin_dir(), self.root / "go" / "bin")

    def test_machine_without_go_gets_local_bin(self):
        # Ни того, ни другого в PATH: имя ~/go/bin на машине без тулчейна
        # выглядело бы враньём.
        os.environ["PATH"] = "/usr/bin:/bin"
        self.assertEqual(update.bin_dir(), self.root / ".local" / "bin")

    def test_in_path_expands_home(self):
        os.environ["PATH"] = "~/go/bin:/usr/bin"
        self.assertTrue(update.in_path(self.root / "go" / "bin"))
        self.assertFalse(update.in_path(self.root / ".local" / "bin"))


class SumsTest(unittest.TestCase):
    """Сверка суммы: подменённый байт роняет установку."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-sums-test-"))
        self.asset = write(self.root / "devkit-v1.tar.gz", "ассет\n")

    def tearDown(self):
        shutil.rmtree(str(self.root), ignore_errors=True)

    def sums(self):
        return update.parse_sums("%s  %s\n" % (build.sha256(self.asset), self.asset.name))

    def test_good_sum_passes(self):
        self.assertEqual(update.verify(self.asset, self.sums()), "")

    def test_flipped_byte_stops_install(self):
        sums = self.sums()
        self.asset.write_bytes(b"\xff" + self.asset.read_bytes()[1:])
        bad = update.verify(self.asset, sums)
        self.assertIn("сумма", bad)
        self.assertIn(hashlib.sha256(self.asset.read_bytes()).hexdigest(), bad,
                      "находка не называет, что получили")

    def test_missing_line_stops_install(self):
        # Ассет есть, строки про него в SHA256SUMS нет: сверять нечем, и молча
        # ставить нельзя.
        self.assertIn("нет строки", update.verify(self.asset, {"чужой.tar.gz": "0"}))


class PathDivergenceTest(unittest.TestCase):
    """Сверка победителя PATH с только что положенной сборкой (DK-599)."""

    NAME = "alfactl"

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-divergence-test-"))
        self.dest = self.root / "bin"
        self.shadow = self.root / "shadow"
        self.saved = os.environ.get("PATH")
        os.environ["PATH"] = "%s%s%s" % (self.shadow, os.pathsep, self.dest)

    def tearDown(self):
        os.environ["PATH"] = self.saved
        shutil.rmtree(str(self.root), ignore_errors=True)

    def lay(self, where, version, commit):
        executable(where / self.NAME,
                   BINARY_STUB % build.version_line(self.NAME, version, commit))

    def expect(self):
        return {self.NAME: ("v1.0.0", "коммит-вершины")}

    def test_fresh_winner_is_silent(self):
        self.lay(self.dest, "v1.0.0", "коммит-вершины")
        self.assertEqual(update.path_divergence(self.dest, self.expect()), [])

    def test_shadowing_stale_copy_is_named(self):
        self.lay(self.dest, "v1.0.0", "коммит-вершины")
        self.lay(self.shadow, "v0.1.0", "чужое")
        found = update.path_divergence(self.dest, self.expect())
        self.assertEqual(len(found), 1, found)
        for word in (self.NAME, str(self.shadow / self.NAME), "v0.1.0", "чужое",
                     "v1.0.0", "коммит-вершины"):
            self.assertIn(word, found[0], "находка не называет %s" % word)

    def test_winner_without_version_is_a_finding(self):
        self.lay(self.dest, "v1.0.0", "коммит-вершины")
        executable(self.shadow / self.NAME, "#!/bin/sh\nexit 3\n")
        found = update.path_divergence(self.dest, self.expect())
        self.assertEqual(len(found), 1, found)
        self.assertIn("не отвечает", found[0])

    def test_name_missing_from_path_is_not_judged(self):
        # Каталог мимо PATH это находка раскладки, а не подмена: про него
        # говорят доктор и «осталось сделать» установки, сверка молчит.
        os.environ["PATH"] = str(self.shadow)
        self.lay(self.dest, "v1.0.0", "коммит-вершины")
        self.assertEqual(update.path_divergence(self.dest, self.expect()), [])


class GitReadCacheTest(UpdateCase):
    """Кеш git-чтений на прогон команды: доктор спрашивает одно и то же по
    многу раз, и каждый git это свой процесс. Проверяется всё водораздела:
    включённый кеш отвечает из памяти, выключенный ходит в git каждый раз, а
    меняющая команду кеш переживать не имеет права."""

    def setUp(self):
        super().setUp()
        self.addCleanup(update.git_cache, False)

    def test_read_is_answered_from_the_cache(self):
        update.git_cache(True)
        update.git(self.dk, "symbolic-ref", "--quiet", "--short", "HEAD")
        # Чтение мимо git: репозиторий меняется, а ответ остаётся прежним.
        git(self.dk, "checkout", "-q", "-b", "другая")
        self.assertEqual(update.current_branch(self.dk), "main")

    def test_disabled_cache_reads_fresh(self):
        update.git_cache(True)
        update.git(self.dk, "symbolic-ref", "--quiet", "--short", "HEAD")
        update.git_cache(False)
        git(self.dk, "checkout", "-q", "-b", "другая")
        self.assertEqual(update.current_branch(self.dk), "другая")

    def test_mutating_command_drops_the_cache(self):
        update.git_cache(True)
        update.git(self.dk, "tag", "--list", "v*", "--sort=-v:refname")
        # Fetch меняет известные клону теги, и ответ на «какой тег новее»
        # обязан читаться заново, а не из снимка до похода в origin.
        update.git(self.dk, "tag", "v0.11.0")
        self.assertEqual(update.latest_tag(self.dk), "v0.11.0")


class CheckoutModeTest(UpdateCase):
    """Разбор режима чекаута и мера своей работы."""

    def test_latest_tag_sorts_by_version(self):
        # По строке v0.9.0 было бы новее v0.10.0, а сортировка тут версионная.
        self.assertEqual(update.latest_tag(self.dk), "v0.10.0")

    def test_service_tags_do_not_count(self):
        # В devkit живёт служебный тег deployed, shipctl двигает его на каждом
        # выкате: релизными считаются только v*.
        git(self.dk, "tag", "-f", "deployed")
        self.assertEqual(update.latest_tag(self.dk), "v0.10.0")
        self.assertEqual(update.head_tag(self.dk), "")

    def test_branch_and_detached(self):
        self.assertEqual(update.current_branch(self.dk), "main")
        self.assertEqual(update.head_tag(self.dk), "")
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        self.assertEqual(update.current_branch(self.dk), "")
        self.assertEqual(update.head_tag(self.dk), "v0.10.0")

    def test_work_at_risk_measures(self):
        self.assertEqual(update.work_at_risk(self.dk), "")
        write(self.dk / "новый.txt", "неотслеживаемое\n")
        self.assertEqual(update.work_at_risk(self.dk), "",
                         "untracked переходу не мешает, git checkout его не теряет")
        write(self.dk / "tools" / TOOLS[0] / "go.mod", GO_MOD % TOOLS[0] + "// правка\n")
        self.assertIn("правлено отслеживаемое", update.work_at_risk(self.dk))

    def test_unpushed_is_seen_on_detached_head(self):
        # Мера считается без @{u}: у отвязанного HEAD upstream нет вовсе, и
        # проверка через него отказывала бы на машине потребителя каждый раз.
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        self.assertEqual(git(self.dk, "rev-parse", "@{u}")[0], 128,
                         "у отвязанного HEAD не должно быть upstream")
        self.assertEqual(update.work_at_risk(self.dk), "")
        write(self.dk / "tools" / TOOLS[0] / "go.mod", GO_MOD % TOOLS[0] + "// своё\n")
        self.commit("своя работа мимо origin")
        risky = update.work_at_risk(self.dk)
        self.assertIn("не достаёт ни одна ветка origin", risky)
        self.assertIn(": 1", risky, "отказ не называет, сколько коммитов на кону")

    def test_frozen_origin_main_does_not_stop_the_second_release_in_a_row(self):
        # DK-164: у настоящего клона потребителя origin/main обновляет только
        # клонирование, дальше update тянет исключительно теги. Второй переход
        # по тегам подряд не должен принимать разрыв со ставшей веткой origin
        # за собственную работу.
        customer = self.root / "клиент"
        run(["git", "clone", "--quiet", str(self.origin), str(customer)])
        git(customer, "checkout", "--detach", "--quiet", "v0.10.0")
        self.assertEqual(update.work_at_risk(customer), "",
                         "первый переход на релиз уже спотыкается, стенд не готов")
        # Выходит следующий релиз: коммит и тег на origin, ветку origin в
        # клоне потребителя это не трогает вовсе.
        self.mark(self.driver, "код тега 3")
        self.commit("четвёртый")
        git(self.dk, "tag", "v0.11.0")
        git(self.dk, "push", "-q", "--tags", "origin", "main")
        git(customer, "fetch", "--quiet", "--no-tags", "--prune", "origin", update.TAG_REFSPEC)
        git(customer, "checkout", "--detach", "--quiet", "v0.11.0")
        risky = update.work_at_risk(customer)
        self.assertEqual(risky, "",
                         "тег, приехавший фетчем тегов, принят за чужую работу: %s" % risky)


class ReleaseFindingsTest(UpdateCase):
    """Две находки доктора про релизы: точная (тег чекаута против новейшего в
    клоне) и косвенная (давно ли ходили за тегами). Обе считаются по локальному
    клону, сеть тут не участвует вовсе.
    """

    def touch_fetch(self, days=None):
        path = update.fetch_head(self.dk)
        if days is None:
            if path.exists():
                path.unlink()
            return path
        write(path, "")
        when = time.time() - days * 24 * 3600
        os.utime(str(path), (when, when))
        return path

    def test_mode_names_the_checkout(self):
        self.assertEqual(update.checkout_mode(self.dk), "на ветке main, впереди релиза v0.10.0")
        git(self.dk, "checkout", "--detach", "--quiet", "v0.9.0")
        self.assertEqual(update.checkout_mode(self.dk), "на релизе v0.9.0")
        git(self.dk, "checkout", "--quiet", "-b", "rel", "v0.10.0")
        self.assertEqual(update.checkout_mode(self.dk),
                         "на ветке rel, и на её вершине стоит релиз v0.10.0")
        git(self.dk, "checkout", "--detach", "--quiet", "main")
        self.assertIn("отвязан на коммите", update.checkout_mode(self.dk))

    def test_older_tag_is_named_exactly(self):
        self.touch_fetch(days=0)
        git(self.dk, "checkout", "--detach", "--quiet", "v0.9.0")
        said = "\n".join(update.check_release(self.dk))
        self.assertIn("стоит devkit v0.9.0, а вышел v0.10.0", said)
        self.assertIn("devkitctl update", said, "находка не называет команду обновления")

    def test_newest_tag_is_silent(self):
        self.touch_fetch(days=0)
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        self.assertEqual(update.check_release(self.dk), [],
                         "на новейшем теге доктору говорить не о чем")

    def test_on_release(self):
        # DK-163: doctor гасит по этому признаку находки, адресованные тому,
        # кто devkit правит. Признак взводится только на отвязанном HEAD,
        # который стоит ровно на релизном теге: тот же случай, который
        # checkout_mode различает как «на релизе».
        self.assertFalse(update.on_release(self.dk),
                         "на ветке впереди релиза признак не должен взводиться")
        git(self.dk, "checkout", "--detach", "--quiet", "v0.9.0")
        self.assertTrue(update.on_release(self.dk), "детач на релизном теге не распознан")
        git(self.dk, "checkout", "--quiet", "-b", "rel", "v0.10.0")
        self.assertFalse(update.on_release(self.dk),
                         "именованная ветка на вершине релиза не должна считаться отвязанным чекаутом")
        git(self.dk, "checkout", "--detach", "--quiet", "main")
        self.assertFalse(update.on_release(self.dk),
                         "отвязанный коммит без релизного тега это не релиз")

    def test_branch_has_no_exact_finding(self):
        # На машине разработчика чекаут стоит на ветке впереди тега, и «стоит X,
        # вышел Y» там неправда: тега на машине не стоит вовсе.
        self.touch_fetch(days=0)
        self.assertEqual(update.check_release(self.dk), [])

    def test_fetch_age_by_both_sides_of_the_threshold(self):
        self.touch_fetch(days=20)
        self.assertIn("не ходили 20 дней", "\n".join(update.check_release(self.dk)))
        self.touch_fetch(days=13)
        self.assertEqual(update.check_release(self.dk), [])

    def test_clone_that_never_fetched(self):
        # Свежий клон: FETCH_HEAD нет вовсе, и знание о релизах взять неоткуда.
        self.touch_fetch(days=None)
        self.assertIn("не ходили ни разу", "\n".join(update.check_release(self.dk)))

    def test_fetch_closes_the_loop(self):
        # Клон знает только v0.9.0 и стоит на нём: точной находки нет, а
        # косвенная зажглась. После похода за тегами они меняются местами, и
        # человек, спросивший подсказанной командой, узнаёт про вышедший релиз.
        clone = self.root / "clone"
        run(["git", "clone", "--quiet", "--no-tags", str(self.origin), str(clone)])
        git(clone, "fetch", "--quiet", "origin", "tag", "v0.9.0")
        git(clone, "checkout", "--detach", "--quiet", "v0.9.0")
        old = time.time() - 30 * 24 * 3600
        os.utime(str(update.fetch_head(clone)), (old, old))
        said = "\n".join(update.check_release(clone))
        self.assertIn("не ходили 30 дней", said)
        self.assertNotIn("а вышел", said, "клон не знает про v0.10.0, а точная находка зажглась")
        git(clone, "fetch", "--quiet", "--tags", "origin")
        said = "\n".join(update.check_release(clone))
        self.assertIn("стоит devkit v0.9.0, а вышел v0.10.0", said)
        self.assertNotIn("не ходили", said, "после похода за тегами косвенная находка осталась")


class RefusalTest(UpdateCase):
    """Отказы: чужое дерево, ветка без ключа, своя работа на кону."""

    def test_worktree_of_a_task_branch(self):
        rc = update.run(self.dk, False, log=self.log, err=self.log)
        self.assertEqual(rc, 2)
        self.assertIn("worktree ветки задачи", self.out())
        self.assertEqual(self.installed(), [], "с ветки задачи поставлены бинари")

    def test_branch_without_pin(self):
        head = self.head()
        self.assertEqual(self.update(), 2)
        self.assertIn("git pull", self.out())
        self.assertIn("doctor --fix", self.out())
        self.assertIn("--pin", self.out(), "отказ не называет ключ первой установки")
        self.assertEqual(self.head(), head, "чекаут ветки уведён отказавшей командой")
        self.assertEqual(self.installed(), [])

    def test_dirty_tracked_stops_pin(self):
        write(self.dk / "tools" / TOOLS[0] / "go.mod", GO_MOD % TOOLS[0] + "// правка\n")
        head = self.head()
        self.assertEqual(self.update(pin=True), 2)
        self.assertIn("правлено отслеживаемое", self.out())
        self.assertIn("git stash", self.out(), "отказ не называет, куда деть правку")
        self.assertEqual(self.head(), head)

    def test_unpushed_commit_stops_pin(self):
        write(self.dk / "tools" / TOOLS[0] / "go.mod", GO_MOD % TOOLS[0] + "// своё\n")
        self.commit("своя работа")
        head = self.head()
        self.assertEqual(self.update(pin=True), 2)
        self.assertIn("не достаёт ни одна ветка origin", self.out())
        self.assertEqual(self.head(), head)
        self.assertEqual(self.installed(), [])

    def test_manual_tag_does_not_launder_unpushed_work(self):
        # DK-164, второй круг ревью: тег, поставленный человеком руками поверх
        # невыложенного коммита, не должен подделывать собой источник из
        # origin. Имя нарочно совпадает с маской v*: голая маска без --prune
        # свежим fetch тут не спасает.
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        write(self.dk / "tools" / TOOLS[0] / "go.mod", GO_MOD % TOOLS[0] + "// своё\n")
        self.commit("своя работа поверх тега")
        git(self.dk, "tag", "v0.10.1-fake")
        head = self.head()
        self.assertEqual(self.update(), 2, self.out())
        self.assertIn("не достаёт ни одна ветка origin", self.out())
        self.assertEqual(self.head(), head, "отказавшая команда сдвинула чекаут")
        self.assertEqual(self.installed(), [], "отказавшая команда поставила бинари")

    def test_tag_outside_the_mask_does_not_launder_unpushed_work(self):
        # Вторая половина той же защиты: имя мимо маски v* фетч не пруннит, он
        # про refs/tags/v* и только. Держит тут маска --tags=v* у самой меры, и
        # без неё голый --tags засчитал бы «backup» за источник из origin.
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        write(self.dk / "tools" / TOOLS[0] / "go.mod", GO_MOD % TOOLS[0] + "// своё\n")
        self.commit("своя работа поверх тега")
        git(self.dk, "tag", "backup")
        head = self.head()
        self.assertEqual(self.update(), 2, self.out())
        self.assertIn("не достаёт ни одна ветка origin", self.out())
        self.assertEqual(self.head(), head, "отказавшая команда сдвинула чекаут")
        self.assertEqual(self.installed(), [], "отказавшая команда поставила бинари")

    def test_check_does_not_refuse_on_a_branch(self):
        # --check ничего не двигает, и отказывать ему на ветке не за чем: это
        # единственный способ узнать про вышедший релиз.
        self.assertEqual(self.update(check=True), 0)
        self.assertIn("ветка main", self.out())


class NoTagsTest(UpdateCase):
    """Тегов нет вовсе: до первого релиза команда обязана быть безвредной."""

    def setUp(self):
        super().setUp()
        for tag in ("v0.9.0", "v0.10.0"):
            git(self.dk, "tag", "-d", tag)
            git(self.dk, "push", "-q", "origin", ":refs/tags/%s" % tag)

    def test_nothing_moves_and_the_command_says_so(self):
        head = self.head()
        self.assertEqual(self.update(pin=True), 1)
        self.assertIn("релизов devkit ещё не выпускалось", self.out())
        self.assertEqual(self.head(), head, "без тегов чекаут всё-таки сдвинулся")
        self.assertEqual(self.installed(), [], "без тегов поставлены бинари")
        self.assertEqual(Requests.seen, [], "без тегов команда полезла в сеть за ассетами")

    def test_the_only_working_path_is_named(self):
        self.update(pin=True)
        self.assertIn("devkitctl.py build", self.out(),
                      "не назван единственный работающий пока путь")


class RestartTest(UpdateCase):
    """Самоперезапуск: вторую половину делает уже код тега."""

    def restarting(self, **kw):
        calls = []
        rc = self.update(pin=True, restart=lambda script, args: calls.append((script, args)), **kw)
        return rc, calls

    def test_second_half_goes_to_the_tag_code(self):
        rc, calls = self.restarting()
        self.assertEqual(rc, 0)
        self.assertEqual(len(calls), 1, "перезапуска не было: вторую половину делает старый код")
        script, args = calls[0]
        self.assertEqual(Path(script), self.dk / "tools" / "devkitctl" / "devkitctl.py")
        self.assertEqual(args, ["update", update.RESTART_FLAG])
        self.assertEqual(self.head(), self.short_full("v0.10.0"), "чекаут не переведён на тег")
        self.assertEqual(self.installed(), [],
                         "первая половина поставила бинари сама, перезапуск ни при чём")

    def short_full(self, rev):
        return git(self.dk, "rev-parse", rev + "^{commit}")[1].strip()

    def test_tag_older_than_the_command_is_done_in_place(self):
        # Тег, собранный до появления update, флага не понимает, и перезапуск в
        # него уронил бы обновление разбором аргументов уже после перевода
        # чекаута.
        write(self.driver, "# старый код без флага\n")
        (self.driver.parent / "update.py").unlink()
        self.commit("тег без update")
        git(self.dk, "tag", "-f", "v0.10.0")
        stub_release(self.releases, "v0.10.0", self.short("v0.10.0"), self.NAMES)
        self.mark(self.driver, "код ветки")
        self.commit("работа после тега")
        git(self.dk, "push", "-q", "-f", "--tags", "origin", "main")
        rc, calls = self.restarting()
        self.assertEqual(rc, 0)
        self.assertEqual(calls, [], "перезапуск ушёл в код, который про него не знает")
        self.assertIn("собран до появления update", self.out())
        self.assertEqual(self.installed(), sorted(TOOLS + (update.WRAPPER,)),
                         "вторая половина не отработала на месте")

    def test_restarted_half_skips_git(self):
        # Второй процесс шаги git не повторяет: он уже стоит на теге.
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        Requests.seen = []
        self.assertEqual(self.update(restarted=True), 0)
        self.assertEqual(sorted(self.installed()), sorted(TOOLS + (update.WRAPPER,)))
        self.assertNotIn("чекаут:", self.out(), "перезапущенная половина трогает чекаут")


class InstallTest(UpdateCase):
    """Скачивание, сверка суммы, подмена бинарей и обёртка."""

    def install(self):
        # Перезапуск в тесте холостой, и вторую половину зовёт этот же прогон:
        # так проверяются обе половины, а живой execv стоит отдельным тестом.
        restarted = []
        rc = self.update(pin=True, restart=lambda script, args: restarted.append(args))
        if rc == 0 and restarted:
            rc = self.update(restarted=True)
        return rc

    def test_binaries_land_and_say_the_tag(self):
        self.assertEqual(self.install(), 0, self.out())
        for name in TOOLS:
            path = self.dest / name
            self.assertTrue(os.access(str(path), os.X_OK), "%s не исполняемый" % name)
            self.assertEqual(update.binary_stamp(path), ("v0.10.0", self.tag_commit),
                             "%s назвал не ту версию" % name)
        self.assertIn("сумма сошлась", self.out())
        # Однотипное свёрнуто (DK-157): про две утилиты сказано одной строкой, с
        # числом и именами, а не строкой на каждую.
        self.assertIn("установлено 2 утилиты devkit v0.10.0 (%s) в %s: %s"
                      % (self.tag_commit, self.dest, ", ".join(TOOLS)), self.out(),
                      "строка про поставленные утилиты не свёрнута в одну")
        # Слова раскладки в выводе установки под запретом, и запрет держит их
        # оба сразу: убрать одно, а второе оставить, значит вернуть прежнюю речь
        # следующей же правкой (DK-157).
        for word in ("не стояло", "положено"):
            self.assertNotIn(word, self.out(),
                             "в выводе установки осталась раскладочная речь: %s" % word)
        self.assertEqual([p.name for p in self.dest.glob(update.TMP_PREFIX + "*")], [],
                         "временный каталог остался в каталоге назначения")
        self.assertGreater(os.path.getmtime(str(self.dest / TOOLS[0])), RELEASE_MTIME + 1,
                           "у поставленного бинаря время правки из тарболла, а не момент "
                           "установки: доктор посчитает его старее исходников")

    def test_replace_does_not_write_over_the_old_file(self):
        # Подмена именно переименованием: на linux запись в работающий файл даёт
        # ETXTBSY. Жёсткая ссылка на старый бинарь это и проверяет: после
        # переименования она держит старое содержимое.
        old = executable(self.dest / TOOLS[0],
                         BINARY_STUB % build.version_line(TOOLS[0], "v0.1.0", "старьё"))
        keep = self.dest.parent / "keep"
        os.link(str(old), str(keep))
        self.assertEqual(self.install(), 0, self.out())
        self.assertEqual(update.binary_stamp(self.dest / TOOLS[0]), ("v0.10.0", self.tag_commit))
        self.assertEqual(update.binary_stamp(keep), ("v0.1.0", "старьё"),
                         "старый бинарь переписан на месте, а не подменён переименованием")
        # Утилиты пришли с разных версий, и свёртка идёт по переходу: обновлённая
        # своей строкой, поставленная впервые своей.
        # Одна утилита числа не называет: «обновлена утилита», а не «обновлено 1».
        self.assertIn("обновлена утилита devkit с v0.1.0 (старьё) до v0.10.0 (%s) в %s: %s"
                      % (self.tag_commit, self.dest, TOOLS[0]), self.out(),
                      "нет строки про обновление с прежней версии")
        self.assertIn("установлена утилита devkit v0.10.0 (%s) в %s: %s"
                      % (self.tag_commit, self.dest, TOOLS[1]), self.out(),
                      "утилита, которой раньше не было, попала в строку про обновлённую")

    def test_wrapper_is_laid_next_to_the_binaries(self):
        self.assertEqual(self.install(), 0, self.out())
        wrapper = self.dest / update.WRAPPER
        self.assertTrue(os.access(str(wrapper), os.X_OK), "обёртка не исполняемая")
        text = wrapper.read_text(encoding="utf-8")
        self.assertIn(update.MARKER, text, "в обёртке нет пометы devkit:generated")
        self.assertIn(str(self.dk / "tools" / "devkitctl" / "devkitctl.py"), text,
                      "обёртка показывает не на этот чекаут")

    def test_second_run_says_there_is_nothing_to_do(self):
        self.assertEqual(self.install(), 0, self.out())
        Requests.seen = []
        self.said = []
        self.assertEqual(self.update(), 0)
        self.assertIn("обновлять нечего", self.out())
        self.assertEqual(Requests.seen, [], "на холостом ходу команда полезла в сеть за ассетами")

    def test_stale_binary_is_replaced_without_moving_the_checkout(self):
        # Чекаут на новейшем теге, а бинарь отвечает чужим коммитом: это не
        # холостой ход, ставить надо.
        self.assertEqual(self.install(), 0, self.out())
        executable(self.dest / TOOLS[0],
                   BINARY_STUB % build.version_line(TOOLS[0], "v0.1.0", "старьё"))
        self.said = []
        self.assertEqual(self.update(), 0, self.out())
        self.assertEqual(update.binary_stamp(self.dest / TOOLS[0]), ("v0.10.0", self.tag_commit))

    def test_shadowing_copy_fails_the_install(self):
        # Копию из чужого каталога, стоящего в PATH раньше каталога назначения,
        # установка не двигает: до DK-599 она молчала про такую подмену, и
        # командой на машине оставалась старая сборка.
        shadow = self.root / "shadow"
        executable(shadow / TOOLS[0],
                   BINARY_STUB % build.version_line(TOOLS[0], "v0.1.0", "чужое"))
        os.environ["PATH"] = "%s%s%s" % (shadow, os.pathsep, os.environ["PATH"])
        self.assertEqual(self.install(), 1, self.out())
        self.assertIn(TOOLS[0], self.out())
        self.assertIn(str(shadow / TOOLS[0]), self.out())
        self.assertEqual(update.binary_stamp(self.dest / TOOLS[0]),
                         ("v0.10.0", self.tag_commit),
                         "сверка PATH обязана идти после укладки, а не вместо неё")

    def test_flipped_byte_leaves_binaries_alone(self):
        asset, _ = update.asset_name("v0.10.0")
        path = self.releases / "v0.10.0" / asset
        path.write_bytes(b"\x00" + path.read_bytes()[1:])
        self.assertEqual(self.install(), 1)
        self.assertIn("сумма", self.out())
        self.assertEqual(self.installed(), [], "после подменённого байта бинари всё-таки встали")

    def test_assets_are_not_out_yet(self):
        # Щель между пушем тега и концом сборки релиза: тег виден сразу, ассеты
        # появляются через пару минут.
        shutil.rmtree(str(self.releases / "v0.10.0"))
        self.assertEqual(self.install(), 1)
        self.assertIn("релиз собирается", self.out())
        self.assertIn("v0.10.0", self.out())
        self.assertEqual(self.installed(), [])

    def test_no_network_for_assets(self):
        os.environ[update.RELEASE_ENV] = "http://127.0.0.1:1"
        self.assertEqual(self.install(), 1)
        self.assertIn("нужна сеть", self.out())
        self.assertEqual(self.installed(), [])

    def test_no_network_for_tags(self):
        git(self.dk, "remote", "set-url", "origin", str(self.root / "нет-такого.git"))
        head = self.head()
        self.assertEqual(self.update(pin=True), 1)
        self.assertIn("git fetch тегов не прошёл", self.out())
        self.assertEqual(self.head(), head, "после отказа fetch чекаут всё-таки сдвинулся")
        self.assertEqual(self.installed(), [])

    def test_diverged_deployed_tag_does_not_block_the_install(self):
        # deployed это служебный тег, shipctl двигает его на каждом выкате, и
        # на машине потребителя он расходится с origin уже после первого же
        # выката. Голый --tags такое расхождение принимал за отказ сети
        # («would clobber existing tag»), хотя релизные теги приехали.
        git(self.dk, "tag", "deployed")
        git(self.dk, "push", "-q", "origin", "deployed")
        git(self.dk, "tag", "-f", "deployed", "v0.9.0")
        self.assertEqual(self.install(), 0, self.out())
        self.assertNotIn("нужна сеть", self.out())
        self.assertEqual(sorted(self.installed()), sorted(TOOLS + (update.WRAPPER,)))

    def test_does_not_auto_follow_a_foreign_tag_on_a_known_commit(self):
        # То же автослежение и на полном update: посторонний тег на коммите,
        # уже известном клону (v0.10.0), не должен приезжать без --no-tags.
        git(self.dk, "tag", "other-tag", self.tag_commit)
        git(self.dk, "push", "-q", "origin", "other-tag")
        git(self.dk, "tag", "-d", "other-tag")
        self.assertEqual(self.install(), 0, self.out())
        self.assertNotIn("other-tag", git(self.dk, "tag", "--list")[1].splitlines(),
                         "чужой тег на известном коммите приехал в клон")


class TagOnTheBranchTipTest(UpdateCase):
    """Новейший тег стоит на вершине ветки: обычное состояние свежего клона.

    Тег ставится на голову main, и сразу после релиза переходить по коммиту
    некуда, а отвязывать чекаут всё равно надо: иначе машина потребителя
    остаётся на ветке, и следующий update отказывает ей как дереву
    разработчика.
    """

    def setUp(self):
        super().setUp()
        self.tip = "v0.11.0"
        git(self.dk, "tag", self.tip)
        git(self.dk, "push", "-q", "--tags", "origin", "main")
        self.tip_commit = self.short("HEAD")
        stub_release(self.releases, self.tip, self.tip_commit, self.NAMES)

    def test_pin_detaches_when_there_is_nowhere_to_move(self):
        self.assertEqual(update.head_tag(self.dk), self.tip,
                         "стенд не воспроизводит случай: тег не на вершине ветки")
        self.assertEqual(self.update(pin=True), 0, self.out())
        self.assertEqual(update.current_branch(self.dk), "",
                         "после --pin чекаут остался на ветке: %s" % self.out())
        self.assertEqual(update.head_tag(self.dk), self.tip)
        self.assertIn("чекаут devkit переведён с ветки main на релиз %s" % self.tip, self.out())
        self.assertEqual(sorted(self.installed()), sorted(TOOLS + (update.WRAPPER,)))

    def test_next_call_updates_instead_of_refusing(self):
        # Ради этого отвязывание и нужно: обещано, что дальше обновление идёт
        # голым update, а на ветке оно упёрлось бы в отказ для дерева
        # разработчика.
        self.update(pin=True)
        self.said = []
        self.assertEqual(self.update(), 0, self.out())
        self.assertIn("обновлять нечего", self.out())
        self.assertNotIn("дерево разработчика", self.out())

    def test_matching_binaries_do_not_cancel_the_pin(self):
        # Бинари уже отвечают нужным коммитом, а чекаут на ветке: холостым ходом
        # это не считается, отвязать всё равно надо.
        for name in TOOLS:
            executable(self.dest / name,
                       BINARY_STUB % build.version_line(name, self.tip, self.tip_commit))
        self.assertEqual(self.update(pin=True), 0, self.out())
        self.assertNotIn("обновлять нечего", self.out())
        self.assertEqual(update.current_branch(self.dk), "",
                         "совпавшие бинари отменили перевод чекаута на тег")

    def test_without_pin_it_still_refuses(self):
        # Замок на то, что правкой нельзя было сломать: тег на вершине ветки
        # разрешения не заменяет, и без ключа команда отказывает так же, как
        # любому дереву разработчика.
        head = self.head()
        self.assertEqual(self.update(), 2, self.out())
        self.assertIn("дерево разработчика devkit", self.out())
        self.assertIn("--pin", self.out(), "отказ не называет ключ первой установки")
        self.assertEqual(self.head(), head, "отказавшая команда сдвинула чекаут")
        self.assertEqual(update.current_branch(self.dk), "main",
                         "чекаут отвязан без разрешения")
        self.assertEqual(self.installed(), [], "отказавшая команда поставила бинари")

    def test_check_advises_the_pin(self):
        self.assertEqual(self.update(check=True), 0)
        out = self.out()
        self.assertIn("на её вершине стоит тег %s" % self.tip, out)
        self.assertIn("--pin", out, "рассказ не зовёт отвязать клон")
        self.assertNotIn("новее ничего нет", out,
                         "чекаут на ветке назван обновлённым, хотя он не отвязан")
        self.assertEqual(update.current_branch(self.dk), "main", "--check сдвинул чекаут")


class CheckTest(UpdateCase):
    """--check: рассказ без правки чекаута, бинарей и раскладки."""

    def test_tells_and_moves_nothing(self):
        head = self.head()
        self.assertEqual(self.update(check=True), 0)
        out = self.out()
        self.assertIn("ветка main", out)
        self.assertIn("новейший тег: v0.10.0", out)
        self.assertIn("--pin", out, "рассказ не называет, чем перевести клон на тег")
        self.assertEqual(self.head(), head, "--check сдвинул чекаут")
        self.assertEqual(self.installed(), [], "--check поставил бинари")
        self.assertEqual(Requests.seen, [], "--check ходил за ассетами")

    def test_tells_about_the_new_release_on_a_pinned_checkout(self):
        git(self.dk, "checkout", "--detach", "--quiet", "v0.9.0")
        self.assertEqual(self.update(check=True), 0)
        self.assertIn("чекаут: тег v0.9.0", self.out())
        self.assertIn("поставит devkitctl update: чекаут и бинари тега v0.10.0", self.out())

    def test_says_when_nothing_is_newer(self):
        git(self.dk, "checkout", "--detach", "--quiet", "v0.10.0")
        self.assertEqual(self.update(check=True), 0)
        self.assertIn("новее ничего нет", self.out())

    def test_brings_tags_into_the_clone(self):
        # Знание о вышедшем релизе неоткуда взять, кроме origin, и --check
        # складывает его в клон: доктор потом считает находку без сети.
        git(self.dk, "tag", "-d", "v0.10.0")
        self.assertEqual(update.latest_tag(self.dk), "v0.9.0")
        self.assertEqual(self.update(check=True), 0)
        self.assertEqual(update.latest_tag(self.dk), "v0.10.0",
                         "--check не принёс теги в клон")

    def test_survives_a_deployed_tag_that_diverged_from_origin(self):
        # deployed это служебный тег, shipctl двигает его на каждом выкате, и
        # на машине потребителя он расходится с origin уже после первого же
        # выката. Голый --tags такое расхождение принимал за отказ сети.
        git(self.dk, "tag", "deployed")
        git(self.dk, "push", "-q", "origin", "deployed")
        git(self.dk, "tag", "-f", "deployed", "v0.9.0")
        self.assertEqual(self.update(check=True), 0, self.out())
        self.assertNotIn("нужна сеть", self.out())
        self.assertIn("новейший тег: v0.10.0", self.out())

    def test_does_not_auto_follow_a_foreign_tag_on_a_known_commit(self):
        # У git есть автослежение за тегами: посторонний тег на коммите, уже
        # известном клону, приезжает при fetch даже с явным v*-рефспеком, если
        # не глушить автослежение --no-tags. Коммит v0.10.0 клону известен
        # заранее, и deployed на нём это обычный случай в devkit.
        git(self.dk, "tag", "other-tag", self.tag_commit)
        git(self.dk, "push", "-q", "origin", "other-tag")
        git(self.dk, "tag", "-d", "other-tag")
        self.assertEqual(self.update(check=True), 0, self.out())
        self.assertNotIn("other-tag", git(self.dk, "tag", "--list")[1].splitlines(),
                         "чужой тег на известном коммите приехал в клон")


class WrapperTest(UpdateCase):
    """Обёртка devkitctl и каталог назначения в PATH (решения 6 и 7)."""

    def test_text_is_three_lines_with_the_marker(self):
        # Ожидание выписано руками, а не собрано тем же шаблоном: обёртку читает
        # оболочка, и её три строки это контракт с ней.
        update.check_wrapper(self.dk, True)
        self.assertEqual((self.dest / update.WRAPPER).read_text(encoding="utf-8"),
                         '#!/bin/sh\n# devkit:generated\nexec python3 "%s" "$@"\n'
                         % (self.dk / "tools" / "devkitctl" / "devkitctl.py"))

    def test_laid_and_not_relaid(self):
        findings, fixed = update.check_wrapper(self.dk, True)
        self.assertEqual(findings, [])
        self.assertEqual(len(fixed), 1, fixed)
        findings, fixed = update.check_wrapper(self.dk, True)
        self.assertEqual((findings, fixed), ([], []), "обёртка перевыпускается на каждом прогоне")

    def test_finding_without_fix(self):
        findings, fixed = update.check_wrapper(self.dk, False)
        self.assertEqual(fixed, [])
        self.assertIn("нет обёртки devkitctl", findings[0])
        self.assertIn("doctor --fix", findings[0])

    def test_moved_checkout_reissues_the_wrapper(self):
        # Путь в обёртке зашит абсолютным, и после переезда чекаута она обязана
        # перевыпуститься.
        stale = update.WRAPPER_TEXT % (update.MARKER, "/старый/путь/devkitctl.py")
        write(self.dest / update.WRAPPER, stale)
        findings, _ = update.check_wrapper(self.dk, False)
        self.assertIn("показывает не на этот чекаут devkit", findings[0])
        findings, fixed = update.check_wrapper(self.dk, True)
        self.assertEqual(findings, [])
        self.assertEqual(len(fixed), 1, fixed)
        self.assertIn(str(self.dk), (self.dest / update.WRAPPER).read_text(encoding="utf-8"))

    def test_foreign_file_is_not_touched(self):
        # По помете видно, что файл наш, и чужой скрипт с тем же именем доктор
        # не затрёт.
        mine = "#!/bin/sh\necho чужое\n"
        write(self.dest / update.WRAPPER, mine)
        findings, fixed = update.check_wrapper(self.dk, True)
        self.assertEqual(fixed, [])
        self.assertIn("чужой devkitctl", findings[0])
        self.assertEqual((self.dest / update.WRAPPER).read_text(encoding="utf-8"), mine)

    def test_not_in_path_is_a_finding(self):
        os.environ["PATH"] = "/usr/bin:/bin"
        findings, _ = update.check_wrapper(self.dk, True)
        self.assertTrue(any("не в PATH" in f for f in findings), findings)
        self.assertTrue(any("export PATH" in f for f in findings),
                        "находка не даёт готовой строки для профиля")

    def test_worktree_does_not_lay_the_wrapper(self):
        findings, fixed = update.check_wrapper(self.dk, True, from_main=False)
        self.assertEqual(fixed, [])
        self.assertIn("worktree ветки задачи", findings[0])
        self.assertFalse((self.dest / update.WRAPPER).exists())


class CommandWiringTest(unittest.TestCase):
    """Команда подключена к devkitctl со всеми тремя ключами.

    Проверяется разбором аргументов, а не прогоном: настоящий update полез бы в
    git живого чекаута и в сеть. Лишний ключ роняет argparse до того, как дело
    дойдёт до работы, и по его жалобе видно, какие ключи он знает.
    """

    CTL = Path(__file__).resolve().parent / "devkitctl.py"
    DEVKIT = Path(__file__).resolve().parent.parent.parent

    def test_this_code_understands_its_own_restart(self):
        # Признак смотрят на чужом дереве (тег, на который перешли), и соврать
        # он может только в одну сторону: сказать «не понимает» про код, который
        # понимает. Тогда вторая половина остаётся старому коду молча, и ловится
        # это только на живом дереве, а не на фикстуре.
        self.assertTrue(update.understands_restart(self.DEVKIT),
                        "живой devkit не опознаёт собственный флаг перезапуска: "
                        "установка не будет перезапускаться в код тега")

    def test_flags_are_registered(self):
        rc, out = run([PY, str(self.CTL), "update", "--pin", "--check",
                       update.RESTART_FLAG, "--чужой"])
        self.assertEqual(rc, 2, out)
        self.assertIn("--чужой", out, "argparse жалуется не на тот ключ: %s" % out)
        for flag in ("--pin", "--check", update.RESTART_FLAG):
            self.assertNotIn("unrecognized arguments: %s" % flag, out,
                             "ключ %s не подключён к команде" % flag)


class LiveRestartTest(UpdateCase):
    """Живой самоперезапуск через os.execv: вторую половину делает код тега.

    Тут команда гоняется отдельным процессом, а не вызовом функции: os.execv в
    процессе теста заменил бы сам прогон, и проверить его иначе нечем. Метка в
    драйвере говорит, чей код работает, и по выводу видно оба процесса.
    """

    def test_new_code_finishes_the_work(self):
        rc, out = run([PY, str(self.driver), "update", "--pin"])
        self.assertEqual(rc, 0, out)
        self.assertIn("код ветки", out, "первую половину делал не код ветки")
        self.assertIn("код тега", out, "вторую половину делает не код тега")
        self.assertLess(out.index("код ветки"), out.index("код тега"),
                        "порядок половин перепутан")
        self.assertEqual(sorted(self.installed()), sorted(TOOLS + (update.WRAPPER,)))
        self.assertEqual(update.binary_stamp(self.dest / TOOLS[0]),
                         ("v0.10.0", self.tag_commit))


if __name__ == "__main__":
    unittest.main()
