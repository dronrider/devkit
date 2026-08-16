#!/usr/bin/env python3
"""Тесты генератора карты проекта (DK-375).

Тесты пирамидой по разделу «Тесты» LLD DK-194: синтетика для генератора,
описания, маркер и свежесть, индекс решений, тонкий файл.
"""
import os
import re
import tempfile
import unittest
from pathlib import Path
import sys

import codemap

# Импортируем check_map_freshness из devkitctl
sys.path.insert(0, str(Path(__file__).parent))
import devkitctl


class TestFirstSentence(unittest.TestCase):
    """Тесты извлечения первого предложения."""

    def test_single_sentence(self):
        """Одиночное предложение возвращается целиком."""
        text = "Это первое предложение."
        self.assertEqual(codemap.first_sentence(text), "Это первое предложение.")

    def test_multiple_sentences(self):
        """Несколько предложений, возвращается первое."""
        text = "Первое предложение. Второе предложение. Третье."
        self.assertEqual(codemap.first_sentence(text), "Первое предложение.")

    def test_text_without_ending(self):
        """Текст без точки в конце."""
        text = "Текст без знака препинания в конце"
        self.assertEqual(codemap.first_sentence(text), "Текст без знака препинания в конце")

    def test_question_mark(self):
        """Предложение с вопросительным знаком."""
        text = "Это вопрос? А это ответ."
        self.assertEqual(codemap.first_sentence(text), "Это вопрос?")

    def test_exclamation_mark(self):
        """Предложение с восклицательным знаком."""
        text = "Внимание! Это важно."
        self.assertEqual(codemap.first_sentence(text), "Внимание!")

    def test_empty_text(self):
        """Пустой текст."""
        self.assertEqual(codemap.first_sentence(""), "")


class TestComponentDetection(unittest.TestCase):
    """Тесты обнаружения компонентов."""

    def setUp(self):
        """Создаём временный каталог для тестов."""
        self.temp_dir = tempfile.mkdtemp()
        self.root = Path(self.temp_dir)

    def tearDown(self):
        """Удаляем временный каталог."""
        import shutil
        shutil.rmtree(self.temp_dir)

    def test_cargo_workspace_members(self):
        """Манифест Cargo даёт членов workspace."""
        # Создаём Cargo.toml с членами workspace
        cargo_toml = self.root / "Cargo.toml"
        cargo_toml.write_text('[workspace]\nmembers = ["crates/hub", "crates/relay"]', encoding="utf-8")

        members = codemap.members_from_manifest(self.root)
        self.assertIsNotNone(members)
        self.assertEqual(sorted(members), ["crates/hub", "crates/relay"])

    def test_go_work_modules(self):
        """Манифест go.work даёт модули."""
        # Создаём go.work с модулями
        go_work = self.root / "go.work"
        go_work.write_text('go 1.21\n\nuse (\n\t./tools/agentctl\n\t./tools/dashboard\n)', encoding="utf-8")

        members = codemap.members_from_manifest(self.root)
        self.assertIsNotNone(members)
        self.assertEqual(sorted(members), ["tools/agentctl", "tools/dashboard"])

    def test_fallback_to_dirs(self):
        """При отсутствии манифеста используются каталоги с кодом."""
        # Создаём каталоги с кодом
        (self.root / "tools").mkdir()
        (self.root / "tools" / "util1.py").write_text("# code", encoding="utf-8")
        (self.root / "internal").mkdir()
        (self.root / "internal" / "util2.go").write_text("// code", encoding="utf-8")

        members = codemap.members_from_dirs(self.root)
        self.assertEqual(sorted(members), ["internal", "tools"])

    def test_excluded_directories(self):
        """Каталоги из FALLBACK_SKIP исключаются."""
        # Создаём каталоги, которые должны быть исключены
        (self.root / ".git").mkdir()
        (self.root / ".git" / "file").write_text("content", encoding="utf-8")
        (self.root / "node_modules").mkdir()
        (self.root / "node_modules" / "package").write_text("code", encoding="utf-8")
        (self.root / "scripts").mkdir()
        (self.root / "scripts" / "script.sh").write_text("#!/bin/sh", encoding="utf-8")

        # Создаём каталог с кодом, который должен быть включён
        (self.root / "src").mkdir()
        (self.root / "src" / "main.rs").write_text("fn main() {}", encoding="utf-8")

        members = codemap.members_from_dirs(self.root)
        self.assertEqual(members, ["src"])

    def test_exceptions_file(self):
        """Файл exceptions вычёркивает компоненты."""
        # Создаём файл исключений
        exceptions_file = self.root / ".devkit" / "describe.ignore"
        exceptions_file.parent.mkdir(parents=True, exist_ok=True)
        exceptions_file.write_text("vendor: сторонние библиотеки\nbuild: сборочные артефакты", encoding="utf-8")

        exceptions = codemap.exceptions(self.root)
        self.assertEqual(exceptions, {"vendor": "сторонние библиотеки", "build": "сборочные артефакты"})

    def test_package_manifest_at_depth(self):
        """Пакетный манифест на глубине закрывает каталог, внутрь не раскрывается."""
        # Создаём структуру tools/<утилита>/go.mod
        tools_dir = self.root / "tools"
        tools_dir.mkdir()

        # Создаём несколько Go-утилит с манифестами
        for util in ["agentctl", "dashboard", "taskctl"]:
            util_dir = tools_dir / util
            util_dir.mkdir()
            (util_dir / "go.mod").write_text(f"module {util}\n\ngo 1.21", encoding="utf-8")
            (util_dir / "main.go").write_text(f"package main\n\nfunc main() {{}}", encoding="utf-8")

        # Добавляем подкаталог внутри утилиты - он не должен попасть в карту
        (tools_dir / "agentctl" / "subdir").mkdir()
        (tools_dir / "agentctl" / "subdir" / "sub.go").write_text("package sub", encoding="utf-8")

        members = codemap.members_from_package_manifests(self.root)
        self.assertEqual(sorted(members), ["tools/agentctl", "tools/dashboard", "tools/taskctl"])

    def test_mixed_workspace_and_package(self):
        """Смешанный случай: манифест в корне плюс пакетный на глубине."""
        # Создаём Cargo workspace с одним членом и отдельный пакет
        cargo_toml = self.root / "Cargo.toml"
        cargo_toml.write_text('[workspace]\nmembers = ["crates/core"]', encoding="utf-8")

        # Создаём член workspace
        core_dir = self.root / "crates" / "core"
        core_dir.mkdir(parents=True)
        (core_dir / "Cargo.toml").write_text('[package]\nname = "core"', encoding="utf-8")
        (core_dir / "src").mkdir()
        (core_dir / "src" / "lib.rs").write_text("pub fn init() {}", encoding="utf-8")

        # Создаём отдельный пакет вне workspace
        util_dir = self.root / "tools"
        util_dir.mkdir()
        (util_dir / "Cargo.toml").write_text('[package]\nname = "util"', encoding="utf-8")
        (util_dir / "src").mkdir()
        (util_dir / "src" / "main.rs").write_text("fn main() {}", encoding="utf-8")

        members = codemap.members_from_package_manifests(self.root)
        self.assertIn("crates/core", members)
        self.assertIn("tools", members)

    def test_package_manifest_deeper_than_two_levels(self):
        """Пакетный манифест глубже двух уровней."""
        # Создаём структуру src/lib/parser с go.mod
        parser_dir = self.root / "src" / "lib" / "parser"
        parser_dir.mkdir(parents=True)
        (parser_dir / "go.mod").write_text("module parser", encoding="utf-8")
        (parser_dir / "parser.go").write_text("package parser", encoding="utf-8")

        members = codemap.members_from_package_manifests(self.root)
        self.assertEqual(members, ["src/lib/parser"])

    def test_fallback_to_dirs_after_package_step(self):
        """При отсутствии пакетных манифестов используется запасной путь."""
        # Создаём структуру без пакетных манифестов
        (self.root / "tools").mkdir()
        (self.root / "tools" / "util1.py").write_text("# code", encoding="utf-8")
        (self.root / "internal").mkdir()
        (self.root / "internal" / "util2.go").write_text("// code", encoding="utf-8")

        members = codemap.members_from_package_manifests(self.root)
        # Должен вернуть None, чтобы следующий шаг каскада использовал dirs
        self.assertIsNone(members)

    def test_devkit_like_structure(self):
        """Devkit-подобная структура: tools/<утилита> с go.mod, internal, hooks."""
        # Создаём структуру как в devkit
        tools_dir = self.root / "tools"
        tools_dir.mkdir()

        # Восемь Go-утилит с манифестами
        go_utils = ["agentctl", "cmdout", "dashboard", "obeycheck", "regcheck",
                    "secretctl", "shipctl", "taskctl", "trackctl"]
        for util in go_utils:
            util_dir = tools_dir / util
            util_dir.mkdir()
            (util_dir / "go.mod").write_text(f"module {util}\n\ngo 1.21", encoding="utf-8")
            (util_dir / "main.go").write_text("package main\n\nfunc main() {}", encoding="utf-8")
            # README с описанием
            (util_dir / "README.md").write_text(f"# {util}: тестовая утилита\n\nОписание.", encoding="utf-8")

        # devkitctl на python без манифеста (не должен попасть в этот тест)
        # devkitctl_dir = tools_dir / "devkitctl"
        # devkitctl_dir.mkdir()
        # (devkitctl_dir / "__init__.py").write_text('"""devkitctl: обвязка проекта."""', encoding="utf-8")

        # internal, hooks
        (self.root / "internal").mkdir()
        (self.root / "internal" / "frame.go").write_text("package internal", encoding="utf-8")
        (self.root / "hooks").mkdir()
        (self.root / "hooks" / "check.sh").write_text("#!/bin/sh", encoding="utf-8")

        # kit/skills и kit/harness с кодом
        (self.root / "kit").mkdir()
        skills_dir = self.root / "kit" / "skills"
        skills_dir.mkdir(parents=True)
        (skills_dir / "SKILL.md").write_text("# Навык", encoding="utf-8")
        (skills_dir / "skill.py").write_text("# code", encoding="utf-8")

        harness_dir = self.root / "kit" / "harness"
        harness_dir.mkdir(parents=True)
        (harness_dir / "config.toml").write_text("# config", encoding="utf-8")

        # kit/agents и kit/templates без кода - не должны попасть
        (self.root / "kit" / "agents").mkdir()
        (self.root / "kit" / "templates").mkdir()

        members_package = codemap.members_from_package_manifests(self.root)
        package_tools = [f"tools/{util}" for util in go_utils]
        self.assertEqual(sorted(members_package), sorted(package_tools))

        # Шаг 3 с исключением пакетных манифестов находит internal, hooks, kit/skills
        members_dirs = codemap.members_from_dirs(self.root, exclude_packages=members_package)
        expected_dirs = ["internal", "hooks", "kit/skills"]
        self.assertEqual(sorted(members_dirs), sorted(expected_dirs))

    def test_toplevel_only_mutation_fails(self):
        """Мутация 'вернуть только каталоги верхнего уровня' падает на devkit-подобной синтетике."""
        # Создаём devkit-подобную структуру
        tools_dir = self.root / "tools"
        tools_dir.mkdir()

        # Go-утилита с манифестом на глубине
        util_dir = tools_dir / "agentctl"
        util_dir.mkdir()
        (util_dir / "go.mod").write_text("module agentctl", encoding="utf-8")
        (util_dir / "main.go").write_text("package main", encoding="utf-8")
        (util_dir / "README.md").write_text("# agentctl: утилита", encoding="utf-8")

        # internal с кодом
        (self.root / "internal").mkdir()
        (self.root / "internal" / "code.go").write_text("package internal", encoding="utf-8")

        # Правильное поведение: шаг 2 находит tools/agentctl
        members = codemap.members_from_package_manifests(self.root)
        self.assertIn("tools/agentctl", members)

        # Мутация: если бы вернулись только каталоги верхнего уровня (tools, internal)
        # то tools/agentctl был бы потерян, и карта была бы неполной


class TestDescriptionExtraction(unittest.TestCase):
    """Тесты извлечения описаний компонентов."""

    def setUp(self):
        """Создаём временный каталог для тестов."""
        self.temp_dir = tempfile.mkdtemp()
        self.root = Path(self.temp_dir)

    def tearDown(self):
        """Удаляем временный каталог."""
        import shutil
        shutil.rmtree(self.temp_dir)

    def test_rust_module_doc(self):
        """Модульная дока Rust (//!) возвращается."""
        # Создаём структуру Rust-крейта
        crate_dir = self.root / "crates" / "hub"
        crate_dir.mkdir(parents=True)
        (crate_dir / "src").mkdir(parents=True)
        (crate_dir / "Cargo.toml").write_text('[package]\nname = "hub"', encoding="utf-8")
        (crate_dir / "src" / "lib.rs").write_text('//! Hub: центральный узел сети\n\npub fn init() {}', encoding="utf-8")

        description = codemap.describe_component(self.root, "hub", "crates/hub")
        self.assertEqual(description, "Hub: центральный узел сети")

    def test_python_docstring(self):
        """Модульная дока Python (docstring) возвращается."""
        # Создаём Python-модуль
        pkg_dir = self.root / "tools" / "devkitctl"
        pkg_dir.mkdir(parents=True)
        # Добавляем новую строку, чтобы docstring был многострочным
        (pkg_dir / "__init__.py").write_text('"""devkitctl: обвязка devkit в проекте одной командой.\n\nОписание."""', encoding="utf-8")

        description = codemap.describe_component(self.root, "devkitctl", "tools/devkitctl")
        self.assertEqual(description, "devkitctl: обвязка devkit в проекте одной командой.")

    def test_readme_first_line(self):
        """Первая строка README с срезом заголовка."""
        # Создаём компонент с README
        comp_dir = self.root / "tools" / "agentctl"
        comp_dir.mkdir(parents=True)
        (comp_dir / "README.md").write_text('# agentctl: вердикты моделей и делегирование субагентам\n\nОписание...', encoding="utf-8")

        description = codemap.describe_component(self.root, "agentctl", "tools/agentctl")
        self.assertEqual(description, "вердикты моделей и делегирование субагентам")

    def test_readme_simple_header(self):
        """README с простым заголовком."""
        comp_dir = self.root / "tools" / "dashboard"
        comp_dir.mkdir(parents=True)
        (comp_dir / "README.md").write_text('# dashboard панель наблюдения\n\nТекст...', encoding="utf-8")

        description = codemap.describe_component(self.root, "dashboard", "tools/dashboard")
        self.assertEqual(description, "панель наблюдения")

    def test_component_without_description(self):
        """Компонент без описания в карту не едет."""
        # Создаём компонент без описания
        comp_dir = self.root / "tools" / "empty"
        comp_dir.mkdir(parents=True)

        description = codemap.describe_component(self.root, "empty", "tools/empty")
        self.assertIsNone(description)

    def test_architecture_map_role(self):
        """Описание из таблицы состава ARCHITECTURE.md."""
        # Создаём ARCHITECTURE.md с таблицей состава
        arch_md = self.root / "docs" / "ARCHITECTURE.md"
        arch_md.parent.mkdir(parents=True, exist_ok=True)
        arch_md.write_text('''# Состав проекта

| Компонент | Роль |
|-----------|------|
| [xr-hub](../crates/hub/) | центральный узел сети |
| [xr-relay](../crates/relay/) | транзитный прокси |
''', encoding="utf-8")

        # Создаём компоненты
        hub_dir = self.root / "crates" / "hub"
        hub_dir.mkdir(parents=True)
        (hub_dir / "Cargo.toml").write_text('[package]\nname = "xr-hub"', encoding="utf-8")

        description = codemap.describe_component(self.root, "xr-hub", "crates/hub")
        self.assertEqual(description, "центральный узел сети")


class TestMapGeneration(unittest.TestCase):
    """Тесты генерации карты."""

    def setUp(self):
        """Создаём временный каталог для тестов."""
        self.temp_dir = tempfile.mkdtemp()
        self.root = Path(self.temp_dir)

    def tearDown(self):
        """Удаляем временный каталог."""
        import shutil
        shutil.rmtree(self.temp_dir)

    def test_map_structure(self):
        """Карта содержит маркер, заголовок и компоненты."""
        # Создаём компоненты с документацией
        tools_dir = self.root / "tools"
        tools_dir.mkdir()
        (tools_dir / "util1.py").write_text('# util1: первая утилита\npass', encoding="utf-8")
        (tools_dir / "README.md").write_text('# util1: первая утилита\n\nОписание...', encoding="utf-8")

        internal_dir = self.root / "internal"
        internal_dir.mkdir()
        (internal_dir / "util.go").write_text('package internal', encoding="utf-8")  # Файл с кодом
        (internal_dir / "doc.go").write_text('// Package internal: второй пакет\npackage internal', encoding="utf-8")

        marker, body, hash_val = codemap.generate_map(self.root)

        # Проверяем маркер
        self.assertTrue(marker.startswith("<!-- devkit:generated map body="))
        self.assertTrue(marker.endswith(" -->"))

        # Проверяем, что тело содержит компоненты
        self.assertIn("tools:", body)
        self.assertIn("internal:", body)
        self.assertIn("первая утилита", body)
        self.assertIn("второй пакет", body)

        # Проверяем индекс решений
        self.assertIn("# Решения по docs/lld", body)

    def test_component_sorting(self):
        """Компоненты сортируются по пути."""
        # Создаём компоненты в случайном порядке с документацией
        # Используем только fallback-механизм для простоты
        zeta_dir = self.root / "zeta"
        zeta_dir.mkdir()
        (zeta_dir / "code.go").write_text('package zeta', encoding="utf-8")  # Файл с кодом
        (zeta_dir / "README.md").write_text('# zeta: третий компонент\n\nОписание...', encoding="utf-8")

        alpha_dir = self.root / "alpha"
        alpha_dir.mkdir()
        (alpha_dir / "code.py").write_text('pass', encoding="utf-8")  # Файл с кодом
        (alpha_dir / "README.md").write_text('# alpha: первый компонент\n\nОписание...', encoding="utf-8")

        beta_dir = self.root / "beta"
        beta_dir.mkdir()
        (beta_dir / "code.rs").write_text('fn main() {}', encoding="utf-8")  # Файл с кодом
        (beta_dir / "README.md").write_text('# beta: второй компонент\n\nОписание...', encoding="utf-8")

        marker, body, hash_val = codemap.generate_map(self.root)

        # Проверяем порядок сортировки
        lines = body.split("\n")
        component_lines = [l for l in lines if ": " in l and not l.startswith("#")]

        # Проверяем, что есть все компоненты
        self.assertTrue(any("alpha:" in l for l in component_lines))
        self.assertTrue(any("beta:" in l for l in component_lines))
        self.assertTrue(any("zeta:" in l for l in component_lines))

        # Находим индексы компонентов
        alpha_idx = next(i for i, l in enumerate(component_lines) if "alpha:" in l)
        beta_idx = next(i for i, l in enumerate(component_lines) if "beta:" in l)
        zeta_idx = next(i for i, l in enumerate(component_lines) if "zeta:" in l)

        self.assertLess(alpha_idx, beta_idx)
        self.assertLess(beta_idx, zeta_idx)

    def test_component_without_description_excluded(self):
        """Компонент без описания исключается из карты."""
        # Создаём компонент с описанием через README
        with_desc_dir = self.root / "with_desc"
        with_desc_dir.mkdir()
        (with_desc_dir / "code.py").write_text('# код', encoding="utf-8")  # Файл с кодом для детекции
        (with_desc_dir / "README.md").write_text('# with_desc: компонент с описанием\n\nОписание...', encoding="utf-8")

        # Создаём компонент без описания
        without_desc_dir = self.root / "without_desc"
        without_desc_dir.mkdir()
        (without_desc_dir / "f.rs").write_text('fn main() {}', encoding="utf-8")

        marker, body, hash_val = codemap.generate_map(self.root)

        # Проверяем, что компонент с описанием есть, а без описания нет
        self.assertIn("with_desc:", body)
        self.assertNotIn("without_desc:", body)


class TestLldIndex(unittest.TestCase):
    """Тесты индекса решений из docs/lld."""

    def setUp(self):
        """Создаём временный каталог для тестов."""
        self.temp_dir = tempfile.mkdtemp()
        self.root = Path(self.temp_dir)

    def tearDown(self):
        """Удаляем временный каталог."""
        import shutil
        shutil.rmtree(self.temp_dir)

    def test_solution_parsing(self):
        """Заголовки «Решение N» извлекаются правильно."""
        # Создаём docs/lld с документами
        lld_dir = self.root / "docs" / "lld"
        lld_dir.mkdir(parents=True)

        lld_file = lld_dir / "DK-139-languages.md"
        lld_file.write_text('''# DK-139: Языки и границы

## Решение 1. Язык выбирается по тому, что работает до сборки

Текст решения...

## Решение 2. Граница sh: сотая строка и своя функция

Текст решения...

## Откуда брать остаток

Это не решение.
''', encoding="utf-8")

        solutions = codemap.parse_lld_index(self.root)

        # Проверяем, что извлечены только решения
        self.assertEqual(len(solutions), 2)
        self.assertEqual(solutions[0], ("DK-139", 1, "Язык выбирается по тому, что работает до сборки"))
        self.assertEqual(solutions[1], ("DK-139", 2, "Граница sh: сотая строка и своя функция"))

    def test_solution_sorting(self):
        """Решения сортируются по документу и номеру."""
        # Создаём несколько документов с решениями
        lld_dir = self.root / "docs" / "lld"
        lld_dir.mkdir(parents=True)

        lld1 = lld_dir / "DK-100-lld.md"
        lld1.write_text('''## Решение 3. Третье решение

## Решение 1. Первое решение
''', encoding="utf-8")

        lld2 = lld_dir / "DK-139-languages.md"
        lld2.write_text('''## Решение 2. Второе решение

## Решение 1. Первое решение DK-139
''', encoding="utf-8")

        solutions = codemap.parse_lld_index(self.root)
        solutions.sort()  # Сортировка как в генераторе

        # Проверяем порядок: DK-100.1, DK-100.3, DK-139.1, DK-139.2
        self.assertEqual(solutions[0], ("DK-100", 1, "Первое решение"))
        self.assertEqual(solutions[1], ("DK-100", 3, "Третье решение"))
        self.assertEqual(solutions[2], ("DK-139", 1, "Первое решение DK-139"))
        self.assertEqual(solutions[3], ("DK-139", 2, "Второе решение"))

    def test_archive_excluded(self):
        """Архив задач не индексируется."""
        lld_dir = self.root / "docs" / "lld"
        lld_dir.mkdir(parents=True)

        # Создаём файл архива
        archive = lld_dir / "TASK-archive.md"
        archive.write_text('## Решение 1. Архивное решение', encoding="utf-8")

        # Создаём обычный файл
        normal = lld_dir / "DK-100-lld.md"
        normal.write_text('## Решение 1. Обычное решение', encoding="utf-8")

        solutions = codemap.parse_lld_index(self.root)

        # Проверяем, что индексируется только обычное решение
        self.assertEqual(len(solutions), 1)
        self.assertEqual(solutions[0], ("DK-100", 1, "Обычное решение"))


class TestMapFreshness(unittest.TestCase):
    """Тесты свежести карты (DK-375)."""

    def setUp(self):
        """Создаём временный каталог для тестов."""
        self.temp_dir = tempfile.mkdtemp()
        self.root = Path(self.temp_dir)

    def tearDown(self):
        """Удаляем временный каталог."""
        import shutil
        shutil.rmtree(self.temp_dir)

    def test_freshness_cycle(self):
        """Цикл свежести: генерация -> молчание -> правка -> находка -> фикс -> молчание."""
        # 1. Создаём компоненты с правильным форматом описания
        tools_dir = self.root / "tools"
        tools_dir.mkdir()
        (tools_dir / "util1.py").write_text('# util1: первая утилита\npass', encoding="utf-8")
        (tools_dir / "README.md").write_text('# util1: первая утилита\n\nОписание...', encoding="utf-8")

        # 2. Генерируем карту
        devkitctl.run_map(str(self.root), write=True)
        map_path = self.root / "docs" / "map.md"
        self.assertTrue(map_path.exists())

        # 3. Doctor молчит (карта свежая)
        findings, fixed = devkitctl.check_map_freshness(str(self.root), fix=False)
        map_findings = [f for f in findings if "карта" in f]
        self.assertEqual(len(map_findings), 0, "Doctor должен молчать на свежей карте: %s" % map_findings)

        # 4. Правим README компонента (изменяем описание)
        (tools_dir / "README.md").write_text('# util1: изменённое описание утилиты\n\nНовое описание...', encoding="utf-8")

        # 5. Doctor находит расхождение (хеш разошёлся после правки)
        findings, fixed = devkitctl.check_map_freshness(str(self.root), fix=False)
        map_findings = [f for f in findings if "карта" in f]
        self.assertGreater(len(map_findings), 0, "Doctor должен найти расхождение после правки: %s" % map_findings)

        # 6. Doctor --fix чинит
        findings, fixed = devkitctl.check_map_freshness(str(self.root), fix=True)
        self.assertGreater(len(fixed), 0, "Doctor должен зафиксить исправление")

        # 7. Doctor снова молчит
        findings, fixed = devkitctl.check_map_freshness(str(self.root), fix=False)
        map_findings = [f for f in findings if "карта" in f]
        self.assertEqual(len(map_findings), 0, "Doctor должен молчать после исправления")


if __name__ == "__main__":
    unittest.main()
