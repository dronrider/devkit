#!/usr/bin/env python3
"""Сборка бинарей devkit: список утилит из дерева, версия из git, тарболлы
релиза и обе самопроверки собранного.

Дерево тут синтетическое, а go заглушка (testenv.GO_STUB): настоящая сборка
шести модулей на четырёх парах стоила бы минуты на каждом прогоне, а проверяется
здесь не компилятор, а то, что вокруг него. Живой прогон релизной сборки стоит
сценарием проверки задачи, тестом он не заменяется.
"""
import hashlib
import os
import shutil
import tarfile
import tempfile
import unittest
from pathlib import Path

import build
from testenv import GO_STUB, executable, git, git_init, run, write

TOOLS = ("alfactl", "betactl")
GO_MOD = "module github.com/dronrider/devkit/tools/%s\n\ngo 1.26\n"
MAIN_GO = "package main\n\nfunc main() {}\n"


class BuildCase(unittest.TestCase):
    """Синтетический чекаут devkit: два каталога с go.mod, один без, git с тегом."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-build-test-"))
        self.dk = git_init(self.root / "devkit")
        for name in TOOLS:
            write(self.dk / "tools" / name / "go.mod", GO_MOD % name)
            write(self.dk / "tools" / name / "main.go", MAIN_GO)
        # Каталог без go.mod это python-часть devkit: в PATH она не ставится и в
        # список сборки попадать не должна.
        write(self.dk / "tools" / "devkitctl" / "devkitctl.py", "# python\n")
        git(self.dk, "add", "-A")
        git(self.dk, "commit", "-qm", "init")
        git(self.dk, "tag", "v9.9.9")
        self.bin = self.root / "bin"
        executable(self.bin / "go", GO_STUB)
        self.runlog = self.root / "runlog"
        self.env = {k: os.environ.get(k) for k in
                    ("PATH", "SANDBOX", "GO_STUB_RUNLOG", "GO_STUB_NO_FLAG", "GO_STUB_NO_STAMP")}
        os.environ["PATH"] = "%s%s%s" % (self.bin, os.pathsep, os.environ["PATH"])
        os.environ["SANDBOX"] = str(self.root)
        os.environ["GO_STUB_RUNLOG"] = str(self.runlog)
        for k in ("GO_STUB_NO_FLAG", "GO_STUB_NO_STAMP"):
            os.environ.pop(k, None)

    def tearDown(self):
        for k, v in self.env.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        shutil.rmtree(str(self.root), ignore_errors=True)

    def ran(self):
        """Что релизная сборка исполняла живьём."""
        if not self.runlog.is_file():
            return []
        return [ln for ln in self.runlog.read_text(encoding="utf-8").split("\n") if ln]

    def cross_pair(self):
        """Пара, которой на этой машине нет: её проверяют байтами, а не запуском."""
        return [p for p in build.PLATFORMS if p != build.host_pair()][0]


class ListAndStampTest(BuildCase):

    def test_tools_come_from_the_tree(self):
        # Список сборки выводится из дерева, а не из константы в коде: седьмая
        # утилита попадает в релиз тем, что появилась.
        self.assertEqual(build.tools(self.dk), sorted(TOOLS))
        write(self.dk / "tools" / "gammactl" / "go.mod", GO_MOD % "gammactl")
        self.assertEqual(build.tools(self.dk), sorted(TOOLS + ("gammactl",)),
                         "новый каталог с go.mod не попал в список сборки")

    def test_stamp_says_release_and_development(self):
        rc, head = run(["git", "-C", str(self.dk), "rev-parse", "--short=12", "HEAD"])
        self.assertEqual(rc, 0)
        self.assertEqual(build.stamp(self.dk), ("v9.9.9", head.strip()),
                         "на теге версия обязана быть самим тегом")
        write(self.dk / "tools" / TOOLS[0] / "main.go", MAIN_GO + "\n// правка\n")
        git(self.dk, "commit", "-qam", "правка")
        version, commit = build.stamp(self.dk)
        self.assertTrue(version.startswith("v9.9.9-1-g"),
                        "после тега версия обязана говорить, что бинарь не релизный: %s" % version)
        self.assertNotEqual(commit, head.strip(), "коммит не обновился")

    def test_stamp_ignores_service_tags(self):
        # В самом devkit живёт служебный тег deployed, shipctl двигает его на
        # каждом выкате. Версию дают только релизные теги, иначе бинарь назвался
        # бы именем тега выката.
        write(self.dk / "tools" / TOOLS[0] / "main.go", MAIN_GO + "\n// выкат\n")
        git(self.dk, "commit", "-qam", "выкат")
        git(self.dk, "tag", "-f", "deployed")
        version, _ = build.stamp(self.dk)
        self.assertTrue(version.startswith("v9.9.9-1-g"),
                        "версия посчиталась по служебному тегу: %s" % version)

    def test_stamp_outside_git_falls_back(self):
        # Распакованный архив или чужое дерево: собрать можно, а версии там нет,
        # и умолчания те же, что зашиты в исходник утилиты.
        bare = self.root / "bare"
        (bare / "tools").mkdir(parents=True)
        self.assertEqual(build.stamp(bare), ("dev", "unknown"))

    def test_nothing_to_build(self):
        bare = self.root / "bare"
        (bare / "tools").mkdir(parents=True)
        self.assertIn("собирать нечего", " ".join(build.local(bare, self.root / "out")))
        self.assertIn("собирать нечего", " ".join(build.release(bare, self.root / "dist")))


class LocalBuildTest(BuildCase):

    def test_binaries_carry_the_stamp(self):
        out = self.root / "gobin"
        version, commit = build.stamp(self.dk)
        self.assertEqual(build.local(self.dk, out, log=lambda *a: None), [])
        for name in TOOLS:
            rc, said = run([str(out / name), "--version"])
            self.assertEqual(rc, 0, said)
            self.assertEqual(said.strip(), build.version_line(name, version, commit),
                             "собранный %s печатает не то, что в него зашивали" % name)

    def test_run_check_catches_missing_flag(self):
        # Утилита, забывшая version.go, роняет сборку: проверяется это запуском,
        # и это единственное место, где такая пропажа видна.
        os.environ["GO_STUB_NO_FLAG"] = TOOLS[1]
        findings = build.local(self.dk, self.root / "gobin", log=lambda *a: None)
        self.assertEqual(len(findings), 1, findings)
        self.assertIn(TOOLS[1], findings[0])
        self.assertIn("ждали от --version", findings[0])
        self.assertIn(build.version_line(TOOLS[1], *build.stamp(self.dk)), findings[0],
                      "находка не называет строку, которую ждали")


class ReleaseTest(BuildCase):

    def release(self, out=None):
        out = out or self.root / "dist"
        return build.release(self.dk, out, log=lambda *a: None), Path(out)

    def test_assets_and_sums(self):
        # Пары перечислены руками, а не взяты из build.PLATFORMS: список,
        # посчитанный тем же кодом, сойдётся и с выпавшей парой.
        version, _ = build.stamp(self.dk)
        findings, out = self.release()
        self.assertEqual(findings, [])
        assets = []
        for goos, goarch in (("darwin", "amd64"), ("darwin", "arm64"),
                             ("linux", "amd64"), ("linux", "arm64")):
            asset = out / ("devkit-%s-%s-%s.tar.gz" % (version, goos, goarch))
            self.assertTrue(asset.is_file(), "нет тарболла %s" % asset.name)
            with tarfile.open(str(asset)) as tf:
                self.assertEqual(sorted(tf.getnames()), sorted(TOOLS),
                                 "внутри тарболла обязаны плоско лежать все утилиты релиза")
            assets.append(asset)
        sums = (out / build.SUMS).read_text(encoding="utf-8")
        self.assertEqual(sorted(ln.split("  ")[1] for ln in sums.split("\n") if ln),
                         sorted(a.name for a in assets), "SHA256SUMS не про те файлы")
        for asset in assets:
            self.assertIn("%s  %s" % (hashlib.sha256(asset.read_bytes()).hexdigest(), asset.name),
                          sums, "сумма %s не сходится" % asset.name)
        self.assertEqual([p.name for p in out.iterdir() if p.is_dir()], [],
                         "промежуточный каталог сборки остался в каталоге назначения")

    def test_only_host_pair_is_executed(self):
        host = build.host_pair()
        if host not in build.PLATFORMS:
            self.skipTest("пара этой машины не из четырёх релизных")
        self.release()
        ran = self.ran()
        self.assertEqual(len(ran), len(TOOLS),
                         "живьём обязана исполняться ровно одна пара, своя: %s" % ran)
        for line in ran:
            self.assertIn("%s-%s" % host, line, "исполнена чужая пара: %s" % line)

    def test_byte_check_catches_missing_stamp(self):
        # Несуществующий символ в -ldflags -X линковщик молча пропускает, и на
        # кросс-собранном бинаре ловится это только байтами.
        goos, goarch = self.cross_pair()
        os.environ["GO_STUB_NO_STAMP"] = "%s/%s" % (goos, goarch)
        version, _ = build.stamp(self.dk)
        findings, out = self.release()
        self.assertEqual(len(findings), len(TOOLS), findings)
        for f in findings:
            self.assertIn("%s/%s" % (goos, goarch), f, "находка не называет пару")
            self.assertIn("нет зашитой версии", f)
        self.assertFalse((out / (build.ASSET % (version, goos, goarch))).exists(),
                         "тарболл собран из бинарей без версии")
        self.assertEqual(len([p for p in out.glob("devkit-*.tar.gz")]), len(build.PLATFORMS) - 1,
                         "остальные пары обязаны собраться")
        self.assertIn(build.SUMS, [p.name for p in out.iterdir()],
                      "суммы по собравшимся парам не посчитаны")


if __name__ == "__main__":
    unittest.main()
