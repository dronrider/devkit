#!/usr/bin/env python3
"""Обёртка-цепочка корп-клона: чужой хук переезжает в <хук>.chained, на его
место встаёт файл devkit с маркером в шапке, и оба зовутся на каждом коммите.

Стенд тот же, что у рубежа следов: боковая директория с доской и корп-клон с
редиректом на неё.
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import corp  # noqa: E402

DASH = "\u2014"  # длинное тире
ALIEN = "#!/bin/sh\necho чужой >> %s\nexit ${ALIEN_CODE:-0}\n"


class ChainCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.side = os.path.join(self.tmp, "proj-local")
        os.makedirs(os.path.join(self.side, "docs"))
        with open(os.path.join(self.side, "docs", "TASKS.md"), "w",
                  encoding="utf-8") as f:
            f.write("# проба: задачи (префикс XR)\n\n## In progress\n")
        self.repo = self.clone("corp", redirect=True)
        self.hooks = os.path.join(self.repo, ".git", "hooks")
        self.alien = os.path.join(self.tmp, "alien.log")
        self.put_alien("pre-commit")

    def clone(self, name, redirect=False):
        path = os.path.join(self.tmp, name)
        subprocess.run(["git", "init", "-q", path], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", path, "config", key, value], check=True)
        if redirect:
            subprocess.run(["git", "-C", path, "config", "devkit.local",
                            "../proj-local"], check=True)
        return path

    def put_alien(self, name):
        os.makedirs(self.hooks, exist_ok=True)
        path = os.path.join(self.hooks, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(ALIEN % self.alien)
        os.chmod(path, 0o755)

    def chain(self, name, hooks=None):
        return corp.install_chain(hooks or self.hooks, name, HERE)[0]

    def head(self, name):
        with open(os.path.join(self.hooks, name), encoding="utf-8") as f:
            return "".join([f.readline() for _ in range(3)])

    def stage(self, text, repo=None):
        repo = repo or self.repo
        with open(os.path.join(repo, "note.txt"), "w", encoding="utf-8") as f:
            f.write(text + "\n")
        subprocess.run(["git", "-C", repo, "add", "note.txt"], check=True)

    def commit(self, message, repo=None, **env):
        return subprocess.run(["git", "-C", repo or self.repo, "commit", "-qm",
                               message], env=dict(os.environ, **env),
                              capture_output=True, text=True).returncode

    def alien_calls(self):
        if not os.path.exists(self.alien):
            return 0
        with open(self.alien, encoding="utf-8") as f:
            return len([ln for ln in f.read().split("\n") if ln])


class TestInstall(ChainCase):
    def test_alien_hook_moves_aside(self):
        self.assertEqual(self.chain("pre-commit"), "установлена")
        self.assertTrue(os.access(os.path.join(self.hooks, "pre-commit.chained"),
                                  os.X_OK))
        self.assertIn("devkit-corp-chain", self.head("pre-commit"))
        self.assertEqual(self.chain("commit-msg"), "установлена")

    def test_second_run_changes_nothing(self):
        self.chain("pre-commit")
        self.assertEqual(self.chain("pre-commit"), "на месте")
        self.assertFalse(os.path.exists(
            os.path.join(self.hooks, "pre-commit.chained.chained")))
        with open(os.path.join(self.hooks, "pre-commit.chained"),
                  encoding="utf-8") as f:
            self.assertIn(self.alien, f.read())

    def test_alien_installer_overwrote_the_wrapper(self):
        # Перезаписанная обёртка теряет маркер, и повторная раскладка
        # разворачивает цепочку заново.
        self.chain("pre-commit")
        path = os.path.join(self.hooks, "pre-commit")
        with open(path, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\nexit 0\n")
        self.assertFalse(corp.is_chain(path))
        self.assertEqual(self.chain("pre-commit"), "установлена")
        self.assertIn("devkit-corp-chain", self.head("pre-commit"))


class TestChainedCommit(ChainCase):
    def setUp(self):
        super().setUp()
        self.chain("pre-commit")
        self.chain("commit-msg")

    def test_clean_commit_passes_and_calls_the_alien(self):
        self.stage("первая строка проекта")
        self.assertEqual(self.commit("feat: чистая правка"), 0)
        self.assertEqual(self.alien_calls(), 1)

    def test_trace_in_message_fails_the_commit(self):
        self.stage("вторая строка проекта")
        self.assertNotEqual(self.commit("feat: правка по XR-007"), 0)
        # Чужой хук при этом всё равно отработал.
        self.assertEqual(self.alien_calls(), 1)

    def test_alien_refusal_fails_the_commit(self):
        self.stage("первая строка проекта")
        self.assertNotEqual(self.commit("feat: чистая правка", ALIEN_CODE="1"), 0)

    def test_devkit_refusal_fails_the_commit(self):
        self.stage("строка с тире %s тут" % DASH)
        self.assertNotEqual(self.commit("feat: правка с тире"), 0)


class TestHomeRepo(ChainCase):
    """Домашний проект с той же обёрткой корп-проверок не замечает: без
    редиректа рубеж следов молчит."""

    def test_local_id_passes_without_redirect(self):
        home = self.clone("homerepo")
        hooks = os.path.join(home, ".git", "hooks")
        self.chain("pre-commit", hooks)
        self.chain("commit-msg", hooks)
        self.stage("правка по XR-007 в %s" % self.side, repo=home)
        self.assertEqual(self.commit("feat: правка по XR-007", repo=home), 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
