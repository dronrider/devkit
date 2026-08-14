#!/usr/bin/env python3
"""Полнота доки: компонент без описания это находка doctor (DK-071).

Проверка гоняется на синтетических репозиториях: rust-workspace с картой
архитектуры, go-проект без карты, запасной путь по каталогам и исключения
из describe.ignore. Живое дерево тут не годится по той же причине, что и
в тестах раскладки: оно меняется каждой задачей.
"""
import unittest

import describe
from testenv import SandboxCase, git, git_init, read, write

CARGO = ('[workspace]\n'
         'members = ["xr-core", "xr-hub", "xr-share", "xr-relay", "xr-util"]\n')
# Карта в стиле xr-proxy: раздел компонента это «### N.M имя: роль», состав
# собран таблицей со ссылкой на каталог.
MAP = ("# Архитектура\n\n"
       "## 3. Состав репозитория\n\n"
       "| Путь | Роль |\n"
       "|---|---|\n"
       "| [xr-core/](../xr-core/) | Ядро. |\n"
       "| [xr-hub/](../xr-hub/) | Control plane. |\n"
       "| [xr-share/](../xr-share/) | Агент файлообмена. |\n"
       "| [xr-relay/](../xr-relay/) | Транзит. |\n\n"
       "## 4. Компоненты\n\n"
       "### 4.1 xr-core: ядро\n\nТекст.\n\n"
       "### 4.7 xr-relay: слепой транзит\n\nТекст.\n")


def fake_proxy(root):
    """Синтетический rust-workspace: у core раздел в карте, у relay раздел
    и модульная дока, у share README, у hub нет ничего.
    """
    git_init(root)
    write(root / "Cargo.toml", CARGO)
    write(root / "docs" / "ARCHITECTURE.md", MAP)
    write(root / "xr-core" / "src" / "main.rs", "fn main() {}\n")
    write(root / "xr-relay" / "src" / "main.rs",
          "//! xr-relay: слепой транзит.\nfn main() {}\n")
    write(root / "xr-share" / "README.md", "# xr-share\n\nАгент.\n")
    write(root / "xr-share" / "src" / "main.rs", "fn main() {}\n")
    write(root / "xr-hub" / "src" / "main.rs", "mod api;\n")
    # В карте не назван вовсе: модульная дока без раздела это тоже описание.
    write(root / "xr-util" / "src" / "main.rs",
          "//! xr-util: мелкие общие функции.\nfn main() {}\n")
    git(root, "add", "-A")
    git(root, "commit", "-qm", "стенд")
    return root


class ManifestTest(SandboxCase):
    """Члены сборки из манифеста и три способа описать компонент."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = fake_proxy(cls.box.root / "proxy")

    def test_undescribed_component_is_a_finding(self):
        found = describe.check(self.proj)
        self.assertEqual(len(found), 1,
                         "находок %d, а не описан один xr-hub: %s" % (len(found), found))
        self.assertIn("xr-hub", found[0])
        self.assertNotIn("xr-relay", found[0])

    def test_members_come_from_the_manifest(self):
        self.assertEqual(describe.members_from_manifest(self.proj),
                         ["xr-core", "xr-hub", "xr-share", "xr-relay", "xr-util"],
                         "члены workspace прочитаны не из Cargo.toml")

    def test_heading_in_the_map_describes(self):
        self.assertEqual(describe.documented(self.proj, "xr-core", "xr-core"),
                         "заголовок в карте архитектуры")

    def test_readme_describes(self):
        self.assertEqual(describe.documented(self.proj, "xr-share", "xr-share"),
                         "README.md рядом с кодом")

    def test_root_module_doc_describes(self):
        self.assertEqual(describe.documented(self.proj, "xr-util", "xr-util"),
                         "модульная дока в src/main.rs")

    def test_table_of_contents_is_not_a_section(self):
        # Таблица состава ссылается на xr-hub, но раздела не даёт: находка
        # по xr-hub обязана гореть и после того, как таблица распознана.
        self.assertNotIn("xr-hub", describe.map_dirs(self.proj, "xr-hub"))

    def test_exception_silences_the_finding(self):
        write(self.proj / ".devkit" / "describe.ignore",
              "# генерённый кодогенератором\nxr-hub: кодогенерация\n")
        try:
            self.assertEqual(describe.check(self.proj), [],
                             "вычеркнутое имя всё ещё находка")
        finally:
            (self.proj / ".devkit" / "describe.ignore").unlink()

    def test_gradle_members(self):
        gradle = self.box.root / "gradle"
        gradle.mkdir()
        write(gradle / "settings.gradle",
              "include ':app', ':core'\ninclude ':net'\n")
        self.assertEqual(describe.members_from_manifest(gradle),
                         ["app", "core", "net"])

    def test_go_work_members(self):
        gowork = self.box.root / "gowork"
        gowork.mkdir()
        write(gowork / "go.work", "go 1.26\n\nuse (\n\t./a\n\t./b\n)\n")
        self.assertEqual(describe.members_from_manifest(gowork), ["a", "b"])


class FallbackTest(SandboxCase):
    """Репозиторий без манифеста: каталоги верхнего уровня с кодом."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = git_init(cls.box.root / "plain")
        write(cls.proj / "server" / "main.py", "print(1)\n")
        write(cls.proj / "web" / "app.js", "1;\n")
        write(cls.proj / "web" / "README.md", "# web\n\nВеб.\n")
        write(cls.proj / "vendor" / "lib" / "a.js", "1;\n")
        write(cls.proj / "docs" / "note.md", "# заметка\n")
        write(cls.proj / "target" / "debug" / "bin", "bytes\n")
        git(cls.proj, "add", "-A")
        git(cls.proj, "commit", "-qm", "стенд")

    def test_code_directories_are_components(self):
        self.assertEqual(describe.members_from_dirs(self.proj), ["server", "web"])

    def test_build_and_vendor_dirs_are_not_components(self):
        self.assertNotIn("vendor", describe.members_from_dirs(self.proj))
        self.assertNotIn("target", describe.members_from_dirs(self.proj))

    def test_undescribed_directory_is_a_finding(self):
        found = describe.check(self.proj)
        self.assertTrue([f for f in found if f.startswith("server:")],
                        "каталог без описания не назван: %s" % found)

    def test_repo_without_manifest_and_git_is_silent(self):
        outside = self.box.root / "notrepo"
        outside.mkdir()
        self.assertEqual(describe.check(outside), [],
                         "не репозиторий дал находки по чужому дереву")


class DoctorTest(SandboxCase):
    """Находка видна из самого doctor, а не только из модуля.

    Проект проходит через new: у него тогда есть AGENTS.md, хуки и
    гитигнорнутый .devkit, и краснота доктора говорит ровно про доку,
    а не про неподключённую обвязку.
    """

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = fake_proxy(cls.box.root / "doctor-proxy")
        cls.box.dkctl_run("new", "--no-board", "-C", str(cls.proj))

    def test_doctor_names_the_undescribed_component(self):
        rc, out = self.box.doctor(self.proj)
        self.assertEqual(rc, 1, "doctor не увидел неописанный компонент")
        self.assertIn("xr-hub", out)

    def test_doctor_passes_when_described(self):
        write(self.proj / "xr-hub" / "README.md", "# xr-hub\n\nControl plane.\n")
        try:
            rc, out = self.box.doctor(self.proj)
            self.assertEqual(rc, 0, "doctor нашёл находки на описанном: %s" % out)
        finally:
            (self.proj / "xr-hub" / "README.md").unlink()


if __name__ == "__main__":
    unittest.main()
