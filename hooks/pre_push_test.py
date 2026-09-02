#!/usr/bin/env python3
"""Рубеж пуша: ручной пуш пользователя рубеж не видит, пуш из сессии агента
отбивается с разбором, разрешение от утилит devkit проходит, а пуш, где в
диффе только доска, проходит и без разрешения (DK-119). Своего модуля у хука
нет, проверяется он как процесс, которым его зовёт git; калитка доски
проверяется на синтетическом репозитории, ссылки на который хук получает
строками stdin, как при настоящем пуше.

TestBoardGate гоняет калитку без shipctl в PATH: рубеж откатывается к
цельнодиапазонному правилу «весь диапазон это доска». TestBoardGateWithShipctl
(DK-602) собирает shipctl и кладёт его в PATH: калитка судит по коммитам,
и код с легитимным ID задачи проходит вместе с доской в одном диапазоне.
"""
import os
import subprocess
import tempfile
import unittest

HOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pre-push")
SESSION = ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CURSOR_AGENT")
ZERO = "0" * 40


def run(stdin="", cwd=None, **env):
    clean = {k: v for k, v in os.environ.items() if k not in SESSION}
    clean.pop("DEVKIT_PUSH_OK", None)
    # PATH зачищен до /usr/bin:/bin: без этого хук нашёл бы уже установленный
    # в системе shipctl (машина разработчика devkit ставит его в ~/go/bin) и
    # тесты калитки без shipctl зависели бы от того, что стоит на хосте.
    # TestBoardGateWithShipctl передаёт свой PATH явно, он в env и перебивает
    # это значение при слиянии ниже.
    clean["PATH"] = "/usr/bin:/bin"
    return subprocess.run([HOOK, "origin", "git@example.com:x.git"],
                          env=dict(clean, **env), input=stdin, cwd=cwd,
                          capture_output=True, text=True)


class TestPrePush(unittest.TestCase):
    def test_user_push_is_not_seen(self):
        self.assertEqual(run().returncode, 0)

    def test_agent_push_is_refused_with_advice(self):
        r = run(CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        self.assertIn("DEVKIT_PUSH_OK", r.stderr)

    def test_devkit_permission_passes(self):
        self.assertEqual(run(CLAUDECODE="1", DEVKIT_PUSH_OK="1").returncode, 0)


class TestBoardGate(unittest.TestCase):
    """Калитка доски: движение вперёд от известного ремоута с диффом только по
    docs/TASKS.md, docs/TASKS-archive.md и docs/tasks/ проходит, всё остальное
    отбивается как раньше."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = self.tmp.name
        self.git("init", "-q", "-b", "main")
        os.makedirs(os.path.join(self.repo, "docs", "tasks"))
        os.makedirs(os.path.join(self.repo, "tools"))
        self.base = self.commit("база", {
            "docs/TASKS.md": "| доска |\n",
            "docs/TASKS-archive.md": "| архив |\n",
            "docs/tasks/DK-001.md": "# DK-001\n",
            "tools/app.txt": "код\n",
        })

    def git(self, *args):
        env = dict(os.environ,
                   GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null",
                   GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        return subprocess.run(["git", "-C", self.repo] + list(args), env=env,
                              capture_output=True, text=True, check=True)

    def commit(self, msg, files):
        for path, text in files.items():
            with open(os.path.join(self.repo, path), "w") as f:
                f.write(text)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", msg)
        return self.git("rev-parse", "HEAD").stdout.strip()

    def push(self, local, remote, extra="", **env):
        line = "refs/heads/main %s refs/heads/main %s\n" % (local, remote)
        return run(stdin=line + extra, cwd=self.repo, **env)

    def test_board_only_commit_passes(self):
        head = self.commit("правка задачи", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 0)

    def test_board_and_archive_range_passes(self):
        self.commit("доска", {"docs/TASKS.md": "| доска 2 |\n"})
        head = self.commit("архив", {"docs/TASKS-archive.md": "| архив 2 |\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 0)

    def test_mixed_diff_is_refused(self):
        head = self.commit("доска и код", {
            "docs/tasks/DK-001.md": "# DK-001\nход\n",
            "tools/app.txt": "код 2\n",
        })
        r = self.push(head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)

    def test_code_commit_in_range_is_refused(self):
        self.commit("код", {"tools/app.txt": "код 2\n"})
        head = self.commit("доска", {"docs/TASKS.md": "| доска 2 |\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 1)

    def test_rename_of_code_into_board_is_refused(self):
        # Без --no-renames дифф печатает у переименования только путь
        # назначения, и перенос кода в docs/tasks/ сходил бы за доску.
        self.git("mv", "tools/app.txt", "docs/tasks/app.md")
        self.git("commit", "-q", "-m", "перенос кода в доску")
        head = self.git("rev-parse", "HEAD").stdout.strip()
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 1)

    def test_lookalike_path_is_refused(self):
        # Без якоря $ в шаблоне docs/TASKS.md.bak сошёл бы за доску.
        head = self.commit("подделка", {"docs/TASKS.md.bak": "мусор\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 1)

    def test_new_branch_is_refused(self):
        head = self.commit("доска", {"docs/TASKS.md": "| доска 2 |\n"})
        self.assertEqual(self.push(head, ZERO, CLAUDECODE="1").returncode, 1)

    def test_branch_deletion_is_refused(self):
        self.assertEqual(self.push(ZERO, self.base, CLAUDECODE="1").returncode, 1)

    def test_unknown_remote_sha_is_refused(self):
        head = self.commit("доска", {"docs/TASKS.md": "| доска 2 |\n"})
        self.assertEqual(self.push(head, "1" * 40, CLAUDECODE="1").returncode, 1)

    def test_diverged_remote_is_refused(self):
        head = self.commit("доска", {"docs/TASKS.md": "| доска 2 |\n"})
        self.git("checkout", "-q", "-b", "side", self.base)
        remote = self.commit("уехавший ремоут", {"docs/TASKS.md": "| чужое |\n"})
        self.assertEqual(self.push(head, remote, CLAUDECODE="1").returncode, 1)

    def test_second_ref_with_code_is_refused(self):
        board = self.commit("доска", {"docs/TASKS.md": "| доска 2 |\n"})
        code = self.commit("код", {"tools/app.txt": "код 2\n"})
        extra = "refs/heads/dev %s refs/heads/dev %s\n" % (code, board)
        self.assertEqual(self.push(board, self.base, extra, CLAUDECODE="1").returncode, 1)

    def test_user_push_with_mixed_diff_is_not_seen(self):
        head = self.commit("код", {"tools/app.txt": "код 2\n"})
        self.assertEqual(self.push(head, self.base).returncode, 0)


REPO_ROOT = os.path.dirname(os.path.dirname(HOOK))


class TestBoardGateWithShipctl(unittest.TestCase):
    """DK-602: с shipctl в PATH калитка судит диапазон по коммитам, а не
    целиком (shipctl push --check-only, tools/shipctl/push.go). Код-коммит
    с ID задачи не из Backlog проходит вместе с доской в одном диапазоне,
    голый код без ID и код с ID из Backlog отбиваются, как и раньше."""

    @classmethod
    def setUpClass(cls):
        cls.bindir = tempfile.mkdtemp()
        r = subprocess.run(
            ["go", "build", "-o", os.path.join(cls.bindir, "shipctl"), "."],
            cwd=os.path.join(REPO_ROOT, "tools", "shipctl"),
            env=dict(os.environ, GOWORK="off"),
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            raise unittest.SkipTest("shipctl не собрался: %s" % r.stderr)

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = self.tmp.name
        self.git("init", "-q", "-b", "main")
        board = (
            "# Тест: задачи (префикс DK)\n\n"
            "## In progress\n\n"
            "| ID | Задача | Тип | P | R | Ссылка |\n"
            "|----|--------|-----|---|---|--------|\n"
            "| DK-001 | В работе | task | P1 | 55 (50+0+0+5+0) |  |\n\n"
            "## Check\n\nНет.\n\n"
            "## Backlog\n\n"
            "| ID | Задача | Тип | P | R | Ссылка |\n"
            "|----|--------|-----|---|---|--------|\n"
            "| DK-002 | Задел | task | P3 | 10 (0+5+0+0+5) |  |\n\n"
            "## Blocked\n\nНет.\n"
        )
        self.base = self.commit("база", {
            "docs/TASKS.md": board,
            "docs/TASKS-archive.md": "| архив |\n",
            "docs/tasks/DK-001.md": "# DK-001\n",
            "tools/app.txt": "код\n",
        })

    def git(self, *args):
        env = dict(os.environ,
                   GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null",
                   GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        return subprocess.run(["git", "-C", self.repo] + list(args), env=env,
                              capture_output=True, text=True, check=True)

    def commit(self, msg, files):
        for path, text in files.items():
            full = os.path.join(self.repo, path)
            os.makedirs(os.path.dirname(full), exist_ok=True)
            with open(full, "w") as f:
                f.write(text)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", msg)
        return self.git("rev-parse", "HEAD").stdout.strip()

    def push(self, local, remote, extra="", **env):
        line = "refs/heads/main %s refs/heads/main %s\n" % (local, remote)
        env["PATH"] = self.bindir + os.pathsep + os.environ.get("PATH", "")
        return run(stdin=line + extra, cwd=self.repo, **env)

    def test_mixed_range_with_legit_id_passes(self):
        code = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        head = self.commit("docs(tasks): DK-001 ход", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 0)

    def test_code_with_backlog_id_is_refused(self):
        head = self.commit("feat: DK-002 задел", {"tools/app.txt": "правка\n"})
        r = self.push(head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)

    def test_bare_code_without_id_is_still_refused(self):
        head = self.commit("код без задачи", {"tools/app.txt": "правка\n"})
        r = self.push(head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)

    def test_pure_board_still_passes(self):
        head = self.commit("docs(tasks): DK-001 ход", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=0)
