#!/usr/bin/env python3
"""Замер расхода контекста (команда drain) на фикстуре с известным составом
вызовов.

Фикстура собирается под подставным HOME в директории слепка пути проекта, как
журналы харнеса: каждая пара tool_use/tool_result заведена так, чтобы пройти
по всем веткам разбора (Bash по головной команде, sed правки против чтения,
чтения целиком против куска, повторные чтения, жирный вывод, отказ
разрешения). Ожидания выписаны руками из этого состава, а не сняты с замера.
"""
import json
import re
import unittest

from testenv import SandboxCase, context, drain, write

# Состав вызовов фикстуры и ожидания по нему:
#   Bash grep -rn foo src        -> 6 символов, головная grep
#   Bash sed -i 's/a/b/' f.txt   -> 0 символов, правка файла
#   Bash sed -n 5p f.txt         -> 5 символов, чтение фрагмента
#   Bash cat big.txt             -> 25000 символов, shell read, жирный
#   Read /a.txt целиком          -> 10 символов
#   Read /a.txt целиком (повтор) -> 10 символов, повторное чтение
#   Read /b.txt limit=5 (кусок)  -> 5 символов
#   Bash ls (отказ разрешения)   -> 1 символ, is_error с permission
CALLS = [
    ("b1", "Bash", {"command": "grep -rn foo src"}, "foo:1\n"),
    ("b2", "Bash", {"command": "sed -i 's/a/b/' f.txt"}, ""),
    ("b3", "Bash", {"command": "sed -n 5p f.txt"}, "line\n"),
    ("b4", "Bash", {"command": "cat big.txt"}, "x" * 25000),
    ("r1", "Read", {"file_path": "/a.txt"}, "0123456789"),
    ("r2", "Read", {"file_path": "/a.txt"}, "0123456789"),
    ("r3", "Read", {"file_path": "/b.txt", "limit": 5}, "12345"),
    ("b5", "Bash", {"command": "ls"}, "permission denied"),
]
USAGE = {"input_tokens": 1, "output_tokens": 1,
         "cache_creation_input_tokens": 10, "cache_read_input_tokens": 10}


def fixture_jsonl():
    """Журнал одной сессии: восемь пар вызов-результат с известным расходом."""
    lines = []
    for tid, tool, inp, result in CALLS:
        lines.append(json.dumps(
            {"type": "assistant", "requestId": "r_" + tid,
             "message": {"content": [{"type": "tool_use", "id": tid,
                                      "name": tool, "input": inp}],
                         "usage": USAGE}}))
        is_error = tid == "b5"
        lines.append(json.dumps(
            {"type": "user",
             "message": {"role": "user", "content": [
                 {"type": "tool_result", "tool_use_id": tid,
                  "is_error": is_error, "content": result}]}}))
    # Битая строка и запись без usage пропускаются, как в настоящем журнале.
    lines.append("битая строка, её пропускают")
    lines.append(json.dumps({"type": "summary", "summary": "без usage"}))
    return "\n".join(lines) + "\n"


class CmdKeysTest(unittest.TestCase):
    """Разбор Bash по головной команде: чистая функция, проверяется мутациями."""

    def test_single_and_pipeline(self):
        self.assertEqual(drain.cmd_keys("grep -rn foo src"), ["grep"])
        # Объём вешается на головную команду, хвост конвейера не множит счёт.
        # git это мультикоманда, «git log» уточняется до двух слов.
        self.assertEqual(drain.cmd_keys("git log | head"), ["git log", "head"])

    def test_multicommand_refinement(self):
        # «go test» и «go build» это разные вызовы, уточнение второго слова.
        self.assertEqual(drain.cmd_keys("go test ./..."), ["go test"])
        self.assertEqual(drain.cmd_keys("go build"), ["go build"])

    def test_assignment_and_cd_skipped(self):
        # Присваивание и навигация не содержательная команда.
        self.assertEqual(drain.cmd_keys("FOO=bar grep x"), ["grep"])
        self.assertEqual(drain.cmd_keys("cd /path && make"), ["make"])

    def test_empty(self):
        self.assertEqual(drain.cmd_keys(""), [])
        self.assertEqual(drain.cmd_keys(None), [])


class SedReadKindTest(unittest.TestCase):
    def test_sed_inplace_vs_print(self):
        self.assertEqual(drain._sed_kind("sed -i 's/a/b/' f"), "правка файла (-i)")
        self.assertEqual(drain._sed_kind("sed -n 5p f"), "чтение фрагмента (-n Np)")
        self.assertEqual(drain._sed_kind("sed 's/a/b/'"), "фильтр в конвейере")

    def test_read_kind(self):
        self.assertEqual(drain._read_kind({"limit": 5}), "кусок (limit/offset)")
        self.assertEqual(drain._read_kind({"offset": 10}), "кусок (limit/offset)")
        self.assertEqual(drain._read_kind({"pages": "1"}), "страницы PDF")
        self.assertEqual(drain._read_kind({}), "файл целиком")


class DrainReportTest(SandboxCase):

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cls.proj = cls.box.root / "drainproj"
        cls.proj.mkdir()
        slug = context.slug(cls.proj.resolve())
        logs = cls.box.home / ".claude" / "projects" / slug
        logs.mkdir(parents=True)
        write(logs / "session.jsonl", fixture_jsonl())
        cls.rc, cls.out = cls.box.dkctl_run("drain", "-C", str(cls.proj))

    def matches(self, pattern, why):
        self.assertTrue(re.search(pattern, self.out), "%s: %s" % (why, self.out))

    def test_exit_code_and_header(self):
        self.assertEqual(self.rc, 0, "drain упал с ошибкой: %s" % self.out)
        self.assertIn_("сессий: 1, вызовов инструментов: 8", self.out,
                       "одна сессия и восемь вызовов")

    def test_tool_table(self):
        # Bash: 5 вызовов, Read: 3 вызова; всего 8.
        self.matches(r"Bash *5 *62\.5%", "Bash должен быть 5 вызовов (62.5%)")
        self.matches(r"Read *3 *37\.5%", "Read должен быть 3 вызова (37.5%)")

    def test_bash_head_commands(self):
        # grep, sed (дважды головная), cat, ls -- по головной команде.
        self.matches(r"grep *в *1 вызовах \(20\.0%\)", "grep в 1 вызове из 5 (20%)")
        self.matches(r"sed *в *2 вызовах \(40\.0%\).*головной в *2",
                     "sed в 2 вызовах (40%), головная в 2")
        self.matches(r"cat *в *1 вызовах", "cat в 1 вызове")

    def test_sed_breakdown(self):
        # Правка файла и чтение фрагмента -- по одному, объём правки 0.
        self.matches(r"правка файла \(-i\).*1 \(50\.0%\).*вывод *0",
                     "правка файла: 1 (50%), вывод 0")
        self.matches(r"чтение фрагмента \(-n Np\).*1 \(50\.0%\).*вывод *5",
                     "чтение фрагмента: 1 (50%), вывод 5")

    def test_read_whole_vs_chunk(self):
        # Два чтения целиком и один кусок; повторное чтение /a.txt учтено.
        self.matches(r"Read, файл целиком *2 \(66\.7%\)", "целиком: 2 (66.7%)")
        self.matches(r"Read, кусок \(limit/offset\) *1 \(33\.3%\)", "кусок: 1 (33.3%)")
        self.assertIn_("всего Read: 3, из них повторных внутри сессии: 1 (33.3%)",
                       self.out, "одно повторное чтение из трёх")

    def test_shell_read(self):
        # cat big.txt прочитан средствами shell: 25000 символов.
        self.matches(r"shell, cat.*1.*вывод *25\.0K", "shell cat: 1 вызов, 25.0K")

    def test_fat_tail(self):
        # Один вызов тяжелее 20K (cat big.txt): 1 из 8 = 12.5%, почти весь вывод.
        self.matches(r"тяжелее 20K символов: 1 \(12\.50% вызовов, 99\.8% всего вывода\)",
                     "жирный хвост: 12.5% вызовов, 99.8% вывода")
        self.matches(r"25\.0K *Bash", "вершина хвоста -- 25.0K вывод Bash")

    def test_permission_denial(self):
        self.assertIn_("=== ОТКАЗЫ РАЗРЕШЕНИЙ ===", self.out,
                       "отказ разрешения должен попасть в отдельный раздел")
        self.matches(r"Bash *1", "отказ один, по инструменту Bash")

    def test_tokens(self):
        # Восемь assistant-записей с одинаковым usage склеились: без дедупа,
        # как в разовом скрипте, каждая копия учитывается.
        # cache_creation = 8 * 10 = 80, input = 8 * 1 = 8.
        self.matches(r"cache_creation_input_tokens.*80 ", "cache_creation должно быть 80")
        self.matches(r"input_tokens.*8 ", "input_tokens должно быть 8")

    def test_missing_logs(self):
        empty = self.box.root / "emptyproj"
        empty.mkdir()
        rc, out = self.box.dkctl_run("drain", "-C", str(empty))
        self.assertEqual(rc, 2, "drain без журналов должен вернуть код 2")
        self.assertIn_("журналы сессий не найдены", out,
                       "drain без журналов должен сказать это словами")
        self.assertNotIn_("Traceback", out, "drain без журналов упал трейсбеком")

    def test_empty_log_directory(self):
        eproj = self.box.root / "eproj2"
        eproj.mkdir()
        (self.box.home / ".claude" / "projects" / context.slug(eproj.resolve())).mkdir(parents=True)
        rc, out = self.box.dkctl_run("drain", "-C", str(eproj))
        self.assertEqual(rc, 2, "drain на пустой директории журналов должен вернуть код 2")
        self.assertIn_("нет вызовов инструментов", out,
                       "drain на пустой директории должен сказать это словами")


if __name__ == "__main__":
    unittest.main()
