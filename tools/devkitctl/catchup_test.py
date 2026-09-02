#!/usr/bin/env python3
"""Догон чекаута devkit до origin/main: гард, подтяг, расхождение и раскладка.

Сети тут нет: origin это голый репозиторий в той же временной директории, а
недоступным он становится переездом каталога. Доктор подменяется заглушкой,
живой прогон стоил бы минуты и трогал машину.
"""
import contextlib
import io
import os
import shutil
import subprocess
import tempfile
import time
import types
import unittest
from contextlib import contextmanager
from pathlib import Path

import catchup
import update
from testenv import git, write


class CatchupCase(unittest.TestCase):
    """Синтетический devkit: голый origin, клон на main и вторая машина рядом.

    Вторая машина это второй клон того же origin: она коммитит и пушит, после
    чего первый клон отстаёт ровно так, как отстаёт настоящий.
    """

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="devkitctl-catchup-test-"))
        self.origin = self.root / "origin.git"
        subprocess.run(["git", "init", "--bare", "-q", str(self.origin)], check=True)
        self.dk = self.clone("devkit")
        write(self.dk / "docs" / "readme.md", "первый\n")
        self.commit(self.dk, "init")
        git(self.dk, "push", "-q", "-u", "origin", "main")
        self.other = self.clone("other")
        self.fixed = []
        update.git_cache(False)

    def tearDown(self):
        shutil.rmtree(str(self.root), ignore_errors=True)

    def clone(self, name):
        path = self.root / name
        subprocess.run(["git", "clone", "-q", str(self.origin), str(path)],
                       check=True, capture_output=True)
        git(path, "config", "user.name", "t")
        git(path, "config", "user.email", "t@t")
        git(path, "checkout", "-q", "-B", "main")
        return path

    def commit(self, where, message):
        git(where, "add", "-A")
        git(where, "commit", "-qm", message)

    def push_from_other(self, path, text="чужое\n"):
        """Коммит второй машины: он уезжает в origin, и клон отстаёт на один."""
        git(self.other, "fetch", "-q", "origin", "main")
        git(self.other, "reset", "-q", "--hard", "origin/main")
        write(self.other / path, text)
        self.commit(self.other, "со второй машины")
        git(self.other, "push", "-q", "origin", "main")

    def doctor(self, start, fix=False):
        self.fixed.append((start, fix))
        print("починено: хуки харнеса разложены")
        print("находка про машину, которую старт сессии видеть не должен")
        return 0

    def run_catchup(self, hook=False, tree=None):
        return catchup.run(tree or self.dk, hook=hook, doctor=self.doctor)

    def head(self, where):
        return git(where, "rev-parse", "HEAD")[1].strip()

    def age_fetch_head(self):
        """Состарить FETCH_HEAD за окно частоты: дальше команда идёт в сеть."""
        path = update.fetch_head(self.dk)
        if not path.exists():
            path.write_text("", encoding="utf-8")
        when = time.time() - catchup.FETCH_MAX_AGE - 60
        os.utime(str(path), (when, when))

    @contextmanager
    def printed(self, expect=""):
        """Напечатанное командой: текст ложится в поле box после выхода."""
        buf = io.StringIO()
        box = types.SimpleNamespace(text="")
        with contextlib.redirect_stdout(buf):
            yield box
        box.text = buf.getvalue()
        if expect:
            self.assertIn(expect, box.text)


class GuardTest(CatchupCase):
    def test_worktree_of_a_branch_stays_silent(self):
        # Дерево задачи стоит на своей ветке, и подтягивать его до origin/main
        # нельзя: main занята основным чекаутом.
        tree = self.root / "task-tree"
        git(self.dk, "worktree", "add", "-q", "-b", "dk-1", str(tree))
        with self.printed("") as out:
            self.run_catchup(hook=True, tree=tree)
        self.assertEqual(out.text, "")
        with self.printed("чекаут не основной") as out:
            self.run_catchup(tree=tree)

    def test_detached_head_stays_silent(self):
        # Машина потребителя стоит на теге отвязанным HEAD, и её двигает
        # только devkitctl update.
        git(self.dk, "checkout", "-q", "--detach", "HEAD")
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")
        with self.printed("HEAD не на ветке main"):
            self.run_catchup()

    def test_other_branch_stays_silent(self):
        git(self.dk, "checkout", "-q", "-b", "side")
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")

    def test_without_origin_stays_silent(self):
        git(self.dk, "remote", "remove", "origin")
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")
        with self.printed("remote origin не заведён"):
            self.run_catchup()

    def test_unreachable_origin_is_silent_in_hook(self):
        # Оторванная сеть на старте сессии это молчание, а руками позванная
        # команда обязана сказать, почему не сходила. FETCH_HEAD убирается: с
        # молодым указателем в сеть не пошли бы вовсе, и проверять было бы
        # нечего.
        self.age_fetch_head()
        shutil.move(str(self.origin), str(self.root / "gone.git"))
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")
        # Провалившийся fetch всё равно двигает FETCH_HEAD, и следующий заход
        # внутри окна в сеть уже не пошёл бы.
        self.age_fetch_head()
        with self.printed("за origin/main не сходили") as out:
            self.run_catchup()
        self.assertIn("devkit:", out.text)

    def test_even_with_origin_stays_silent(self):
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")
        with self.printed("вровень с origin/main"):
            self.run_catchup()


class PullTest(CatchupCase):
    def test_behind_is_fast_forwarded_and_kit_is_laid_out(self):
        # Правка в kit/ это копии в каталогах харнесов, и сама по себе она не
        # действует: раскладывает её доктор.
        self.push_from_other("kit/skills/prose/SKILL.md")
        with self.printed("devkit подтянут до") as out:
            self.run_catchup(hook=True)
        self.assertEqual(self.head(self.dk), self.head(self.other))
        self.assertIn("1 коммит", out.text)
        self.assertIn("починено: хуки харнеса разложены", out.text)
        self.assertNotIn("находка про машину", out.text,
                         "находки доктора уехали в старт сессии")
        self.assertEqual(self.fixed, [(str(self.dk), True)])

    def test_hooks_change_also_calls_doctor(self):
        self.push_from_other("hooks/check-prose.py", "# правка сторожа\n")
        with self.printed("devkit подтянут до"):
            self.run_catchup(hook=True)
        self.assertEqual(len(self.fixed), 1, "правка hooks/ прошла мимо раскладки")

    def test_docs_change_does_not_call_doctor(self):
        # Дока действует чтением из чекаута, копий по харнесам у неё нет, и
        # доктор на каждый её коммит был бы прогоном на пустом месте.
        self.push_from_other("docs/readme.md", "второй\n")
        with self.printed("devkit подтянут до") as out:
            self.run_catchup(hook=True)
        self.assertEqual(self.fixed, [], "доктор позван на правке доки")
        self.assertNotIn("починено", out.text)

    def test_tools_change_also_calls_doctor(self):
        # Утилиты живут бинарями в PATH, их тоже пересобирает доктор.
        self.push_from_other("tools/taskctl/main.go", "// правка\n")
        with self.printed("devkit подтянут до"):
            self.run_catchup(hook=True)
        self.assertEqual(len(self.fixed), 1, "правка tools/ прошла мимо раскладки")

    def test_doctor_failure_keeps_the_pull_line(self):
        self.push_from_other("kit/skills/prose/SKILL.md")

        def broken(start, fix=False):
            raise RuntimeError("go не найден")

        with self.printed("devkit подтянут до") as out:
            catchup.run(self.dk, hook=True, doctor=broken)
        self.assertIn("раскладка не прошла: go не найден", out.text)

    def test_own_unpushed_commits_alone_are_quiet_in_hook(self):
        # Незапушенная своя работа при origin вровень это обычное состояние
        # машины разработки, и шуметь о ней на каждом старте незачем.
        write(self.dk / "docs" / "mine.md", "своё\n")
        self.commit(self.dk, "своя правка")
        self.age_fetch_head()
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")
        with self.printed("main впереди origin/main на 1 коммит"):
            self.run_catchup()

    def test_diverged_main_is_loud_in_hook(self):
        # Своя незапушенная работа в main: подтягивать нечего, и молчание тут
        # читалось бы как исправный подтяг.
        self.push_from_other("kit/skills/prose/SKILL.md")
        write(self.dk / "docs" / "mine.md", "своё\n")
        self.commit(self.dk, "своя правка")
        with self.printed("main разошёлся с origin/main") as out:
            self.run_catchup(hook=True)
        self.assertIn("своих 1 коммит", out.text)
        self.assertIn("чужих 1 коммит", out.text)
        self.assertEqual(self.fixed, [])

    def test_dirty_overlap_refuses_with_git_reason(self):
        # Грязный файл, который тронут и в чужом коммите: git отказывает сам, и
        # его причина едет в старт сессии целиком.
        self.push_from_other("docs/readme.md", "второй\n")
        write(self.dk / "docs" / "readme.md", "правлено на месте\n")
        with self.printed("main не подтянулся") as out:
            self.run_catchup(hook=True)
        self.assertIn("docs/readme.md", out.text)
        self.assertNotEqual(self.head(self.dk), self.head(self.other))


class FetchRateTest(CatchupCase):
    def test_fresh_fetch_head_skips_network(self):
        # Молодой FETCH_HEAD значит в сеть не ходить, но отставание по уже
        # известному указателю посчитать.
        self.push_from_other("kit/skills/prose/SKILL.md")
        git(self.dk, "fetch", "-q", "origin", "main")
        shutil.move(str(self.origin), str(self.root / "gone.git"))
        with self.printed("devkit подтянут до"):
            self.run_catchup(hook=True)
        self.assertEqual(self.head(self.dk), self.head(self.other))

    def test_stale_fetch_head_goes_to_network(self):
        self.push_from_other("docs/readme.md", "второй\n")
        self.age_fetch_head()
        with self.printed("devkit подтянут до"):
            self.run_catchup(hook=True)
        self.assertEqual(self.head(self.dk), self.head(self.other))

    def test_fresh_fetch_head_does_not_see_the_new_commit(self):
        # Обратная сторона окна: чужой коммит уже в origin, но клон о нём ещё не
        # спрашивал, и до конца окна он остаётся неизвестным.
        git(self.dk, "fetch", "-q", "origin", "main")
        self.push_from_other("kit/skills/prose/SKILL.md")
        with self.printed("") as out:
            self.run_catchup(hook=True)
        self.assertEqual(out.text, "")
        self.assertEqual(self.fixed, [])


if __name__ == "__main__":
    unittest.main(verbosity=0)
