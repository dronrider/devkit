#!/usr/bin/env python3
"""Сам стенд самопроверки: слепок копии devkit, которым сторож ловит утечку
состояния между тестами.

Байткод python в слепок не входит. Копия devkit заводится из чекаута как есть, и
первый же прогон подпроцесса кладёт в неё `__pycache__`, которого в чекауте
могло не быть: на свежем worktree сторож краснел десятком тестов подряд, а на
машине, где кеш уже прогрет, тот же код был зелёным.
"""
import unittest

from testenv import SandboxCase, write


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


if __name__ == "__main__":
    unittest.main()
