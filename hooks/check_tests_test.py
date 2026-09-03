#!/usr/bin/env python3
"""Проверка правила «Тесты обязательны» на рубеже коммита: код без теста это
находка, код с тестом нет, дока сама по себе тоже нет. Отсюда же проверяется
третий рубеж pre-commit и осознанный обход DEVKIT_NO_TESTS, который гасит эту
проверку и только её.
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-tests.py")
DASH = "\u2014"  # длинное тире


def run(*args, **kw):
    return subprocess.run([sys.executable, TOOL] + list(args),
                          capture_output=True, text=True, **kw)


class TestCheckTests(unittest.TestCase):
    def test_code_without_test_is_caught(self):
        r = run("pkg/ops.go")
        self.assertEqual(r.returncode, 1)
        self.assertIn("regcheck", r.stdout)

    def test_code_with_test_passes(self):
        self.assertEqual(run("pkg/ops.go", "pkg/ops_test.go").returncode, 0)

    def test_test_in_tests_dir_counts(self):
        self.assertEqual(run("pkg/ops.py", "tests/test_ops.py").returncode, 0)

    def test_fixture_in_testdata_counts(self):
        self.assertEqual(run("hooks/check-x.py",
                             "hooks/testdata/sample.json").returncode, 0)

    def test_docs_alone_pass(self):
        self.assertEqual(run("docs/README.md", "docs/TASKS.md").returncode, 0)

    def test_empty_list_passes(self):
        self.assertEqual(run("--stdin", input="").returncode, 0)

    def test_stdin_catches_code_without_test(self):
        self.assertEqual(run("--stdin", input="pkg/ops.go\n").returncode, 1)


class TestShebang(unittest.TestCase):
    """Файл без расширения читается по шебангу: утилиты devkit и сами хуки
    живут так, и без этого правка хука за код не считалась бы."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        os.makedirs(os.path.join(self.tmp, "wt"))
        self.write("wt/tool", "#!/bin/sh\necho x\n")
        self.write("wt/NOTES", "просто заметка\n")

    def write(self, name, text):
        with open(os.path.join(self.tmp, name), "w", encoding="utf-8") as f:
            f.write(text)

    def test_script_with_shebang_is_code(self):
        self.assertEqual(run("wt/tool", cwd=self.tmp).returncode, 1)

    def test_plain_text_is_not_code(self):
        self.assertEqual(run("wt/NOTES", cwd=self.tmp).returncode, 0)


class TestInlineRustTests(unittest.TestCase):
    """Rust держит тесты в том же файле, что и код. Отдельного тестового файла
    у такой правки нет, и находка тут считается по добавленным строкам
    staged-диффа, а не по имени."""

    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)

    def stage(self, name, text):
        with open(os.path.join(self.repo, name), "w", encoding="utf-8") as f:
            f.write(text)
        subprocess.run(["git", "-C", self.repo, "add", name], check=True)

    def test_code_with_inline_test_passes(self):
        self.stage("lib.rs", "pub fn f() -> u8 { 1 }\n\n"
                             "#[cfg(test)]\nmod tests {\n"
                             "    #[test]\n    fn f_returns_one() { assert_eq!(super::f(), 1); }\n}\n")
        self.assertEqual(run("--stdin", input="lib.rs\n", cwd=self.repo).returncode, 0)

    def test_async_inline_test_counts(self):
        self.stage("lib.rs", "pub async fn f() {}\n\n"
                             "#[cfg(test)]\nmod tests {\n"
                             "    #[tokio::test]\n    async fn f_runs() { super::f().await; }\n}\n")
        self.assertEqual(run("--stdin", input="lib.rs\n", cwd=self.repo).returncode, 0)

    def test_async_inline_test_with_args_counts(self):
        """У атрибута бывают аргументы (flavor, worker_threads), и метка без
        закрытой скобки обязана их накрывать: коммит с таким тестом не ложный
        отказ «без единого теста»."""
        self.stage("lib.rs", "pub async fn f() {}\n\n"
                             "#[cfg(test)]\nmod tests {\n"
                             "    #[tokio::test(flavor = \"multi_thread\", worker_threads = 4)]\n"
                             "    async fn f_runs() { super::f().await; }\n}\n")
        self.assertEqual(run("--stdin", input="lib.rs\n", cwd=self.repo).returncode, 0)

    def test_code_without_inline_test_is_still_caught(self):
        self.stage("lib.rs", "pub fn f() -> u8 { 1 }\n")
        self.assertEqual(run("--stdin", input="lib.rs\n", cwd=self.repo).returncode, 1)

    def test_old_test_module_untouched_is_caught(self):
        """Тесты в файле были и раньше, а правка их не тронула: правило требует
        теста на саму правку, поэтому это находка."""
        self.stage("lib.rs", "pub fn f() -> u8 { 1 }\n\n"
                             "#[cfg(test)]\nmod tests {\n"
                             "    #[test]\n    fn f_returns_one() { assert_eq!(super::f(), 1); }\n}\n")
        subprocess.run(["git", "-C", self.repo, "commit", "-q", "--no-verify",
                        "-m", "init"], check=True)
        self.stage("lib.rs", "pub fn f() -> u8 { 2 }\n\n"
                             "#[cfg(test)]\nmod tests {\n"
                             "    #[test]\n    fn f_returns_one() { assert_eq!(super::f(), 1); }\n}\n")
        self.assertEqual(run("--stdin", input="lib.rs\n", cwd=self.repo).returncode, 1)

    def test_other_language_ignores_inline_marks(self):
        """Признак инлайнового теста читается только у тех расширений, где так
        пишут: питон с комментарием про #[test] остаётся находкой."""
        self.stage("lib.py", "# было бы #[test], да не тут\ndef f():\n    return 1\n")
        self.assertEqual(run("--stdin", input="lib.py\n", cwd=self.repo).returncode, 1)


class TestPreCommit(unittest.TestCase):
    """Третий рубеж коммита: правка кода без теста краснеет, с тестом
    зеленеет, а обход через DEVKIT_NO_TESTS пропускает только эту проверку."""

    def setUp(self):
        self.repo = os.path.join(tempfile.mkdtemp(), "repo")
        subprocess.run(["git", "init", "-q", self.repo], check=True)
        for key, value in (("user.name", "t"), ("user.email", "t@t")):
            subprocess.run(["git", "-C", self.repo, "config", key, value], check=True)
        self.stage("lib.py", "def f():\n    return 1\n")

    def stage(self, name, text):
        with open(os.path.join(self.repo, name), "w", encoding="utf-8") as f:
            f.write(text)
        subprocess.run(["git", "-C", self.repo, "add", name], check=True)

    def hook(self, **env):
        return subprocess.run([os.path.join(HERE, "pre-commit")], cwd=self.repo,
                              env=dict(os.environ, **env),
                              capture_output=True, text=True).returncode

    def test_code_without_test_is_caught(self):
        self.assertEqual(self.hook(), 1)

    def test_bypass_lets_it_through(self):
        self.assertEqual(self.hook(DEVKIT_NO_TESTS="1"), 0)

    def test_code_with_test_passes(self):
        self.stage("test_lib.py", "def test_f():\n    assert True\n")
        self.assertEqual(self.hook(), 0)

    def test_docs_only_commit_passes(self):
        subprocess.run(["git", "-C", self.repo, "rm", "-q", "--cached", "lib.py"],
                       check=True)
        self.stage("README.md", "текст доки\n")
        self.assertEqual(self.hook(), 0)

    def test_bypass_keeps_the_other_lines_of_defence(self):
        self.stage("README.md", "текст с тире %s\n" % DASH)
        self.assertEqual(self.hook(DEVKIT_NO_TESTS="1"), 1)


if __name__ == "__main__":
    unittest.main(verbosity=0)
