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
import json
import os
import shutil
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
        # DK-763: отказ печатает агенту только то, что он может сделать сам,
        # человеческий обход из текста убран.
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)
        self.assertNotIn("--no-verify", r.stderr)

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
            full = os.path.join(self.repo, path)
            os.makedirs(os.path.dirname(full), exist_ok=True)
            with open(full, "w") as f:
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

    def test_corp_nested_board_passes(self):
        """DK-796: боковая директория контура лежит подкаталогом репозитория,
        доска проекта это <проект>/docs/TASKS.md с привязкой tracker.local.
        Коммит доски подкаталога проходит без shipctl в PATH."""
        corp = self.commit("доска authn", {
            "authn/docs/TASKS.md": "# доска authn\n",
            "authn/docs/tasks/AU-001.md": "# AU-001\n",
            "authn/.devkit/tracker.local": "repo = ../../authn\n",
        })
        head = self.commit("chore(authn): доска проекта", {
            "authn/docs/TASKS.md": "# доска authn\nход\n",
        })
        self.assertEqual(self.push(head, corp, CLAUDECODE="1").returncode, 0)

    def test_corp_nested_task_file_passes(self):
        corp = self.commit("файл задачи authn", {
            "authn/docs/TASKS.md": "# доска authn\n",
            "authn/docs/tasks/CA-001.md": "# CA-001\n",
            "authn/.devkit/tracker.local": "repo = ../../authn\n",
        })
        head = self.commit("docs(tasks): CA-001 ход", {
            "authn/docs/tasks/CA-001.md": "# CA-001\nход\n",
        })
        self.assertEqual(self.push(head, corp, CLAUDECODE="1").returncode, 0)

    def test_corp_nested_archive_passes(self):
        """DK-796: архив боковой директории идёт тем же путём, что доска и
        файлы задач, от корня доски проекта."""
        corp = self.commit("архив authn", {
            "authn/docs/TASKS.md": "# доска authn\n",
            "authn/docs/TASKS-archive.md": "| архив authn |\n",
            "authn/.devkit/tracker.local": "repo = ../../authn\n",
        })
        head = self.commit("архив authn ход", {
            "authn/docs/TASKS-archive.md": "| архив authn 2 |\n",
        })
        self.assertEqual(self.push(head, corp, CLAUDECODE="1").returncode, 0)

    def test_nested_stand_board_still_refused(self):
        """DK-796: вложенный docs/TASKS.md без привязки корп-контура (стенд
        внутри обычного проекта) за доску не сходит и отбивается как раньше."""
        head = self.commit("правка стенда", {
            "tools/obeycheck/testdata/project/docs/TASKS.md": "# стенд\n",
        })
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 1)


REPO_ROOT = os.path.dirname(os.path.dirname(HOOK))


class TestBoardGateWithShipctl(unittest.TestCase):
    """DK-602: с shipctl в PATH калитка судит диапазон по коммитам, а не
    целиком (shipctl push --check-only, tools/shipctl/push.go). Код-коммит
    с ID задачи не из Backlog проходит вместе с доской в одном диапазоне,
    а после DK-1675 ещё и с полным актуальным аудитом ревью за ярлыком в
    заметке. Голый код без ID и код с ID из Backlog отбиваются, как и раньше."""

    @classmethod
    def setUpClass(cls):
        cls.bindir = tempfile.mkdtemp()
        cls.addClassCleanup(shutil.rmtree, cls.bindir, ignore_errors=True)
        for tool in ("shipctl", "taskctl"):
            r = subprocess.run(
                ["go", "build", "-o", os.path.join(cls.bindir, tool), "."],
                cwd=os.path.join(REPO_ROOT, "tools", tool),
                env=dict(os.environ, GOWORK="off"),
                capture_output=True, text=True,
            )
            if r.returncode != 0:
                raise unittest.SkipTest("%s не собрался: %s" % (tool, r.stderr))
        # Дерево общих правил для снимка аудита (DK-1675): живой чекаут devkit
        # брал бы RULES с машины, а ворот сверяет их снимок с зафиксированным
        # в отчёте моментом ревью. Состав тот же, что у fakeDevkit у тестов
        # shipctl: три правила с ID и оболочка конвейера как маркер дома.
        cls.standards = tempfile.mkdtemp()
        cls.addClassCleanup(shutil.rmtree, cls.standards, ignore_errors=True)
        for name, text in {
            "RULES.architecture.md": "# Architecture\n\n- **ARCH-001.** Keep the data boundary explicit.\n",
            "RULES.style.md": "# Style\n\n- **STYLE-001.** Name values by purpose.\n",
            "RULES.md": "# Rules\n\n## Тесты обязательны\n\n- **TEST-001.** Exercise changed behavior.\n",
            os.path.join("kit", "skills", "board-task", "task-run.py"): "import sys\n",
        }.items():
            full = os.path.join(cls.standards, name)
            os.makedirs(os.path.dirname(full), exist_ok=True)
            with open(full, "w") as f:
                f.write(text)

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

    def note(self, line):
        """След ревью git-заметкой на HEAD: так его пишет taskctl review level
        в репозитории без доски, строка «Ярлык: ...» под следом называет отчёт
        аудита, и по ним обоим ворот пропускает код."""
        self.git("notes", "--ref=review", "add", "-f", "-m", line, "HEAD")

    def audit(self, label, base):
        """Полный актуальный аудит ревью (DK-1675): снимок правил и кода
        пишет taskctl review audit, выводы по правилам фикстура закрывает
        сама, как их закрывает ревьювер. Доска на время вызова прячется:
        у репозитория с доской taskctl кладёт отчёт в docs/tasks/, а ворот
        по заметке ревью читает .devkit/review-audits/."""
        board = os.path.join(self.repo, "docs", "TASKS.md")
        with open(board) as f:
            saved = f.read()
        os.remove(board)
        try:
            r = subprocess.run(
                [os.path.join(self.bindir, "taskctl"), "review", "audit", label,
                 "--base", base, "-C", self.repo],
                env=dict(os.environ, GIT_CONFIG_GLOBAL="/dev/null",
                         GIT_CONFIG_SYSTEM="/dev/null",
                         DEVKIT_HOME=self.standards,
                         PATH=self.bindir + os.pathsep + os.environ.get("PATH", "")),
                capture_output=True, text=True)
        finally:
            with open(board, "w") as f:
                f.write(saved)
        self.assertEqual(r.returncode, 0, r.stderr)
        audit_dir = os.path.join(self.repo, ".devkit", "review-audits")
        reports = [n for n in os.listdir(audit_dir) if n.endswith(".json")]
        self.assertEqual(len(reports), 1, reports)
        path = os.path.join(audit_dir, reports[0])
        with open(path) as f:
            report = json.load(f)
        report["architecture"] = {
            "goal": "проверить правку кода",
            "decision": "правка проходит калитку пуша",
            "boundaries": "синтетический репозиторий, внешних данных нет",
            "alternative": "оставить прежнее поведение",
            "justification": "фикстуре нужно изменённое поведение",
            "result": "checked",
            "evidence": ["правка локальна и не выходит за рамки фикстуры"],
        }
        for rule in report["rules"]:
            rule["applicability"] = "applicable"
            rule["result"] = "checked"
            rule["reason"] = "правило разобрано по фикстурной правке"
            rule["evidence"] = [{"object": "правка фикстуры",
                                 "location": "tools/app.txt",
                                 "observation": "поведение просмотрено для ворот пуша"}]
        with open(path, "w") as f:
            json.dump(report, f, ensure_ascii=False, indent=2)
            f.write("\n")

    def test_mixed_range_with_legit_id_passes(self):
        self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        head = self.commit("docs(tasks): DK-001 ход", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.audit("external-review", self.base)
        self.note("Уровень 1 до %s: рутина\nЯрлык: external-review" % head[:7])
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1", DEVKIT_HOME=self.standards).returncode, 0)

    def test_note_without_audit_label_is_refused(self):
        """DK-1675: строка уровня без ярлыка отчёта код не пускает, ворот
        требует полный актуальный аудит, а не только уровень тщательности."""
        self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        head = self.commit("docs(tasks): DK-001 ход", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.note("Уровень 1 до %s: рутина" % head[:7])
        r = self.push(head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)

    def test_stale_audit_is_refused(self):
        """Код, дописанный после аудита, отчётом не покрыт: снимок не
        сходится, и ворот отбивает пуш до обновления отчёта."""
        self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        self.commit("docs(tasks): DK-001 ход", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.audit("external-review", self.base)
        head = self.commit("feat: DK-001 ещё правка", {"tools/app.txt": "правка 2\n"})
        self.note("Уровень 1 до %s: рутина\nЯрлык: external-review" % head[:7])
        r = self.push(head, self.base, CLAUDECODE="1", DEVKIT_HOME=self.standards)
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)

    def test_code_without_review_trace_is_refused(self):
        """Код с легитимным ID задачи, но без следа ревью, калитка не пускает:
        ID говорит, чья это правка, а не то, что её кто-то читал."""
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push(head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        # DK-763: код без следа ревью тоже не подсказывает обход рубежа.
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)
        self.assertNotIn("--no-verify", r.stderr)

    def test_review_gate_lifted_by_permission(self):
        """Прямая команда пользователя снимает ворот следа ревью вместе с
        рубежом пуша: рубеж уходит нулём раньше, чем зовёт shipctl."""
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push(head, self.base, CLAUDECODE="1", DEVKIT_PUSH_OK="1")
        self.assertEqual(r.returncode, 0)

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
        # DK-763: код-коммит без ID тоже отбивается без подсказки обхода.
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)
        self.assertNotIn("--no-verify", r.stderr)

    def test_pure_board_still_passes(self):
        head = self.commit("docs(tasks): DK-001 ход", {"docs/tasks/DK-001.md": "# DK-001\nход\n"})
        self.assertEqual(self.push(head, self.base, CLAUDECODE="1").returncode, 0)

    # DK-1324: автономный режим. deploy.local читает shipctl от корня
    # репозитория, pre-push тем же shipctl судит диапазон.
    def autonomous(self):
        os.makedirs(os.path.join(self.repo, ".devkit"), exist_ok=True)
        with open(os.path.join(self.repo, ".devkit", "deploy.local"), "w") as f:
            f.write("autonomous = true\n")

    def push_line(self, local_ref, local_sha, remote_ref, remote_sha, **env):
        """Строка stdin в том виде, в каком её шлёт git: local_ref local_sha
        remote_ref remote_sha. Пара HEAD:main нужна, чтобы судить пушимую
        ссылку, а не текущую ветку (DK-1324)."""
        line = "%s %s %s %s\n" % (local_ref, local_sha, remote_ref, remote_sha)
        env["PATH"] = self.bindir + os.pathsep + os.environ.get("PATH", "")
        return run(stdin=line, cwd=self.repo, **env)

    def push_ref(self, ref, local, remote, **env):
        return self.push_line("refs/heads/%s" % ref, local,
                              "refs/heads/%s" % ref, remote, **env)

    def test_autonomous_task_branch_bare_push_passes(self):
        """Автономность и ветка задачи: голый git push кода с легитимным ID
        проходит pre-push без DEVKIT_PUSH_OK, следа ревью не требуется."""
        self.autonomous()
        self.git("checkout", "-q", "-b", "dk-001")
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push_ref("dk-001", head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_autonomous_main_bare_push_still_refused(self):
        """Main остаётся вопросом человеку даже при autonomous = true."""
        self.autonomous()
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push_ref("main", head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)
        self.assertNotIn("--no-verify", r.stderr)

    def test_autonomous_bare_code_without_id_still_refused(self):
        """Голый git push не расширяется: код без ID задачи отбивается и в
        автономном режиме, калитка пропускает только легитимный код ветки."""
        self.autonomous()
        self.git("checkout", "-q", "-b", "dk-001")
        head = self.commit("код без задачи", {"tools/app.txt": "правка\n"})
        r = self.push_ref("dk-001", head, self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)

    def test_autonomous_head_main_push_refused(self):
        """DK-1324: пуш HEAD:main из-под ветки задачи обходом не идёт, судит
        пушимую ссылка: main без следа ревью отбивается."""
        self.autonomous()
        self.git("checkout", "-q", "-b", "dk-001")
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push_line("HEAD", head, "main", self.base, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)
        self.assertNotIn("--no-verify", r.stderr)

    def test_autonomous_new_task_branch_first_push_passes(self):
        """DK-1324: первый пуш новой ветки задачи (нулевой remote_sha) в
        автономном режиме проходит pre-push: нулевой remote судит сама
        калитка, раньше хук отбивал его до shipctl."""
        self.autonomous()
        self.git("checkout", "-q", "-b", "dk-001")
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push_ref("dk-001", head, ZERO, CLAUDECODE="1")
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_new_task_branch_refused_without_autonomy(self):
        """DK-1324: без autonomous = true первый пуш новой ветки отбивается
        как раньше."""
        self.git("checkout", "-q", "-b", "dk-001")
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push_ref("dk-001", head, ZERO, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)
        self.assertNotIn("DEVKIT_PUSH_OK", r.stderr)
        self.assertNotIn("--no-verify", r.stderr)

    def test_autonomous_new_foreign_branch_first_push_refused(self):
        """DK-1324: первый пуш чужой ветки в автономном режиме отбивается,
        обход открывает только ветка задачи."""
        self.autonomous()
        self.git("checkout", "-q", "-b", "feature")
        head = self.commit("feat: DK-001 правка", {"tools/app.txt": "правка\n"})
        r = self.push_ref("feature", head, ZERO, CLAUDECODE="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("taskctl", r.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=0)
