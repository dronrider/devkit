#!/usr/bin/env python3
"""Сам стенд самопроверки: слепок копии devkit, которым сторож ловит утечку
состояния между тестами, и диспетчер заглушек бинарей.

Байткод python в слепок не входит. Копия devkit заводится из чекаута как есть, и
первый же прогон подпроцесса кладёт в неё `__pycache__`, которого в чекауте
могло не быть: на свежем worktree сторож краснел десятком тестов подряд, а на
машине, где кеш уже прогрет, тот же код был зелёным.
"""
import os
import subprocess
import unittest
from unittest import mock

from testenv import PY, SandboxCase, build, git, run, write


class FingerprintTest(SandboxCase):
    """Что слепок считает, а что пропускает."""

    def test_pycache_slepok_ne_dvigaet(self):
        pyc = self.box.dk / "tools" / "devkitctl" / "__pycache__" / "marker.cpython-99.pyc"
        before = self.box.fingerprint()
        write(pyc, "байткод")
        try:
            self.assertEqual(self.box.fingerprint(), before,
                             "байткод пишет python, а не тест: слепку он не содержание")
        finally:
            pyc.unlink()
            if not any(pyc.parent.iterdir()):
                pyc.parent.rmdir()

    def test_git_faylom_slepok_ne_dvigaet(self):
        """Вложенный worktree кладёт `.git` файлом-поинтером, не каталогом.
        Фильтр пропускает такое имя и среди файлов, не только среди каталогов
        (DK-1026): старый обход резал `.git` по любой части пути, а первый
        обход через `os.walk` пропускал только имена каталогов."""
        git_file = self.box.dk / "tools" / "devkitctl" / ".git"
        before = self.box.fingerprint()
        write(git_file, "gitdir: /anywhere\n")
        try:
            self.assertEqual(self.box.fingerprint(), before,
                             "`.git` файлом (указатель worktree) тоже пропускается")
        finally:
            git_file.unlink()

    def test_pravka_ishodnika_slepok_dvigaet(self):
        target = self.box.dk / "hooks" / "check-symbols.py"
        was = target.read_text(encoding="utf-8")
        before = self.box.fingerprint()
        write(target, was + "# правка теста\n")
        try:
            self.assertNotEqual(self.box.fingerprint(), before,
                                "правка файла devkit обязана быть видна слепку")
        finally:
            write(target, was)


class StubDispatcherTest(SandboxCase):
    """Заглушки бинарей на одном диспетчере.

    Первый exec свежего скрипта на macOS платит проверку подписи кода, и
    отдельный файл на каждую заглушку собирал секунды на первом докторе
    класса. Поэтому заглушки лежат симлинками на один диспетчер и разбирают
    своё имя по $0: имя отвечает строкой версии копии devkit, и доктор
    считает заглушки собранными этим же чекаутом.
    """

    def test_stubs_share_one_dispatcher(self):
        names = list(build.tools(self.box.dk)) + ["tmux"]
        links = {n: self.box.bin / n for n in names}
        for name, path in links.items():
            self.assertTrue(path.is_symlink(),
                            "%s не симлинк: заглушки обязаны делить один диспетчер" % name)
        targets = {p.resolve() for p in links.values()}
        self.assertEqual(len(targets), 1,
                         "заглушки смотрят на разные файлы: %s" % sorted(str(t) for t in targets))

    def test_stub_answers_its_version(self):
        for name in build.tools(self.box.dk):
            rc, out = run([str(self.box.bin / name), "--version"],
                          path=self.box.cleanpath)
            self.assertEqual(rc, 0, "%s не отвечает нулём" % name)
            self.assertEqual(out.strip(), self.box.version_line(name),
                             "%s отвечает чужой строкой версии" % name)



class TempContourTest(SandboxCase):

    def test_doctor_isolates_temp_and_honors_explicit_override(self):
        project = self.box.project("temp-project")
        rc, out = self.box.dkctl_run("new", "--no-board", "-C", str(project))
        self.assertEqual(rc, 0, out)
        outside = self.box.root / "outside"
        outside.mkdir()
        abandoned = outside / "shipctl-merge-other"
        abandoned.mkdir()
        tree = abandoned / "tree"
        rc, out = git(self.box.dk, "worktree", "add", "--detach", str(tree), "HEAD")
        self.assertEqual(rc, 0, out)
        self.addCleanup(git, self.box.dk, "worktree", "remove", "--force", str(tree))
        owner = subprocess.Popen([PY, "-c", "pass"])
        owner.wait()
        write(abandoned / "owner", "pid = %s\nroot = %s\ntree = %s\n"
              % (owner.pid, self.box.dk, tree))
        rc, out = self.box.doctor(project, env={"TMPDIR": str(outside)})
        self.assertEqual(rc, 1, out)
        self.assertIn("временных деревьев от упавших прогонов: 1 дерево", out)
        with mock.patch.dict(os.environ, {"TMPDIR": str(outside)}):
            rc, out = self.box.doctor(project)
        self.assertEqual(rc, 0, out)
        self.assertTrue(tree.is_dir(), "doctor без --fix не должен удалять дерево")


if __name__ == "__main__":
    unittest.main()
