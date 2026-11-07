#!/usr/bin/env python3
"""Отцепленный съём снимка квоты на старте сессии. Настоящие tmux и claude тут
не поднимаются, вместо них заглушки: проверяется обвязка хука (условия запуска,
замок, журнал), а не съём панели, у него свои тесты.

Хук на sh по правилу языка там и остаётся, поэтому зовётся он как процесс, а
тест лежит рядом с ним.
"""
import os
import shutil
import shlex
import subprocess
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "quota-refresh.sh")

# Системная часть PATH подставная: проверка «инструментов нет» иначе держалась
# бы на том, чего нет в /usr/bin именно на этой машине.
SYS_TOOLS = ("sh", "dirname", "mkdir", "rmdir", "date", "find", "sleep",
             "wc", "tail", "mv", "mktemp", "rm")


def awaited(path, timeout=10.0):
    """Дождаться файла, который пишет фоновый процесс."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if os.path.exists(path) and os.path.getsize(path):
            return True
        time.sleep(0.05)
    return False


def read(path):
    if not os.path.exists(path):
        return ""
    with open(path, encoding="utf-8") as f:
        return f.read()


class TestQuotaRefresh(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.mark = os.path.join(self.tmp, "refresh.mark")
        self.home = os.path.join(self.tmp, "home")
        self.lock = os.path.join(self.home, ".devkit", "quota-refresh.lock")
        self.log = os.path.join(self.home, ".devkit", "quota-refresh.log")
        self.error = os.path.join(self.home, ".devkit", "quota-refresh.error")
        self.paused = os.path.join(self.tmp, "writer.paused")
        self.release = os.path.join(self.tmp, "writer.release")
        os.makedirs(self.home)
        self.sys = self.stub_dir("qsys")
        for tool in SYS_TOOLS:
            found = shutil.which(tool)
            if found:
                os.symlink(found, os.path.join(self.sys, tool))
        self.bin = self.stub_dir("qbin")
        for tool in ("tmux", "claude"):
            self.stub(tool, "#!/bin/sh\nexit 0\n")
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n' % self.mark)

    def stub_dir(self, name):
        path = os.path.join(self.tmp, name)
        os.makedirs(path)
        return path

    def stub(self, name, text):
        path = os.path.join(self.bin, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        os.chmod(path, 0o755)

    def run_hook(self, home=None, path=None, extra_env=None):
        env = {"HOME": home or self.home,
               "PATH": path or (self.bin + os.pathsep + self.sys)}
        env.update(extra_env or {})
        return subprocess.run(["sh", HOOK], env=env,
                              capture_output=True, text=True).returncode

    def stream_printf(self, fail=False):
        # printf имеет право писать частями даже за один вызов. Держим
        # писателя после отбивки, пока читатель проверяет опубликованный файл.
        code = r"""
printf() {
    case "$1:$2" in
    '%s\n:---'*)
        command printf '%.4s' "$2" || return
        command printf 'paused\n' > PAUSED || return
        while [ ! -f RELEASE ]; do sleep 0.005; done
        FINISH
        ;;
    *) command printf "$@" ;;
    esac
}
. "$0"
"""
        code = code.replace("PAUSED", shlex.quote(self.paused))
        code = code.replace("RELEASE", shlex.quote(self.release))
        finish = 'return 1' if fail else r'''command printf '%s\n' "${2#????}"'''
        code = code.replace("FINISH", finish)
        self.stub("sh", "#!/bin/sh\nexec %s -c %s \"$@\"\n" %
                  (shlex.quote(shutil.which("sh")), shlex.quote(code)))

    def resume_writer(self):
        with open(self.release, "w", encoding="utf-8") as f:
            f.write("release\n")

    def wait_unlocked(self, timeout=10.0):
        """Дождаться, пока фоновый прогон снимет замок: запись в журнал к
        этому моменту уже сделана, rmdir идёт последней строкой хука."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            if not os.path.isdir(self.lock):
                return True
            time.sleep(0.02)
        return False

    def test_snapshot_is_taken_and_logged(self):
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(awaited(self.mark), "панель не снималась вовсе")
        # Свежий снимок хук не переснимает: порог свежести живёт в agentctl.
        self.assertIn("--if-stale", read(self.mark))
        # Дожидаемся конца фоновой работы (замок снят), а не первого байта в
        # журнале: непустой файл ещё не значит дописанную запись (DK-457).
        self.assertTrue(self.wait_unlocked(), "журнала последнего запуска нет")
        self.assertIn("код возврата", read(self.log))

    def test_taken_lock_stops_the_second_run(self):
        # Иначе claude, поднятый в tmux, своим стартом дёрнул бы этот же хук и
        # увёл бы сессии в воронку.
        os.makedirs(self.lock)
        self.assertEqual(self.run_hook(), 0)
        time.sleep(1)
        self.assertEqual(read(self.mark), "")

    def test_harness_without_declared_quota_stays_quiet(self):
        # Вторая подписка едет с пустой секцией [quota], refresh на ней честно
        # отказывает, и журнал из одних отказов перестал бы отвечать на вопрос,
        # ради которого заведён.
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "у харнеса glm-code секция [quota] пуста, '
                              'снимать остаток нечем" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(awaited(self.mark), "хук даже не позвал refresh")
        self.assertTrue(self.wait_unlocked(), "замок не снялся")
        self.assertEqual(read(self.log), "", "отказ харнеса без квоты попал в журнал")

    def test_a_real_refusal_still_reaches_the_journal(self):
        # Молчание узкое: сюда попадает только харнес без объявленной квоты, а
        # незалогиненный клиент или неузнанная панель остаются в журнале.
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "панель /usage не узналась" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(self.wait_unlocked(), "журнала последнего запуска нет")
        self.assertIn("панель /usage не узналась", read(self.log))
        self.assertIn("код возврата: 1", read(self.log))

    def test_journal_entry_never_seen_half_written(self):
        self.stream_printf()
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "проверка гонки" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        try:
            self.assertTrue(awaited(self.paused), "писатель не дошел до отбивки")
            deadline = time.time() + 10.0
            saw_content = False
            while time.time() < deadline:
                text = read(self.log)
                if text:
                    saw_content = True
                    self.assertIn("код возврата", text,
                                 "журнал застигнут наполовину написанным: %r" % text)
                if not os.path.isdir(self.lock):
                    break
                self.resume_writer()
                time.sleep(0.005)
            self.assertTrue(saw_content, "журнал так и не появился")
        finally:
            self.resume_writer()
            self.assertTrue(self.wait_unlocked(), "замок не снялся")

    def test_journal_keeps_previous_runs_instead_of_overwriting(self):
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "первый отказ" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(self.wait_unlocked(), "замок не снялся после первого запуска")
        previous = read(self.log)

        self.stream_printf()
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "второй отказ" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        try:
            self.assertTrue(awaited(self.paused), "вторая запись не началась")
            self.assertEqual(read(self.log), previous, "читатель увидел часть второй записи")
            self.assertEqual(self.run_hook(), 0)
            self.assertEqual(len(read(self.mark).splitlines()), 2,
                             "при занятом замке начался еще один съем")
        finally:
            self.resume_writer()
            self.assertTrue(self.wait_unlocked(), "замок не снялся после второго запуска")

        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "третий отказ" >&2\nexit 1\n' % self.mark)
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(self.wait_unlocked(), "замок не снялся после третьего запуска")
        journal = read(self.log)
        for text in ("первый отказ", "второй отказ", "третий отказ"):
            self.assertIn(text, journal, "прежняя запись затерлась")
        self.assertEqual(journal.count("код возврата: 1"), 3,
                         "все допущенные запуски должны остаться в журнале")

    def test_journal_growth_is_capped(self):
        # Живой предел (100 КиБ / 500 строк) на тест не гоняем: оба порога
        # переопределимы переменной окружения ради скорости, приём тот же,
        # что у append_capped (hookio.py) для остальных журналов хуков.
        self.stub("agentctl", '#!/bin/sh\necho "$*" >> "%s"\n'
                              'echo "запись отказа" >&2\nexit 1\n' % self.mark)
        env_extra = {"QUOTA_REFRESH_LOG_LIMIT": "1", "QUOTA_REFRESH_LOG_KEEP": "5"}
        for _ in range(3):
            self.assertEqual(self.run_hook(extra_env=env_extra), 0)
            self.assertTrue(self.wait_unlocked(), "замок не снялся")

        previous = read(self.log)
        gate = ('#!/bin/sh\nif [ "$2" = "5" ]; then\n'
                'printf "paused\\n" > %s\n'
                'while [ ! -f %s ]; do sleep 0.005; done\nfi\n'
                'exec %s "$@"\n') % (shlex.quote(self.paused),
                                         shlex.quote(self.release),
                                         shlex.quote(shutil.which("tail")))
        self.stub("tail", gate)
        self.assertEqual(self.run_hook(extra_env=env_extra), 0)
        try:
            self.assertTrue(awaited(self.paused), "обрезка не началась")
            self.assertEqual(read(self.log), previous,
                             "необрезанный журнал доступен до публикации")
        finally:
            self.resume_writer()
            self.assertTrue(self.wait_unlocked(), "замок не снялся после обрезки")

        journal = read(self.log)
        lines = journal.splitlines()
        self.assertLessEqual(len(lines), 5, "журнал вырос сверх предела: %r" % lines)
        self.assertEqual(journal.count("код возврата"), 1,
                         "должна остаться только последняя запись")

    def test_journal_publish_failure_is_visible_and_recoverable(self):
        self.stub("agentctl", '#!/bin/sh\necho "первый отказ" >&2\nexit 1\n')
        self.assertEqual(self.run_hook(), 0)
        self.assertTrue(self.wait_unlocked(), "замок не снялся после первого запуска")
        state = os.path.dirname(self.log)
        foreign = os.path.join(state, "quota-refresh.foreign")
        with open(foreign, "w", encoding="utf-8") as f:
            f.write("чужой временный файл\n")
        self.stub("agentctl", '#!/bin/sh\necho "ответ с закрытыми данными" >&2\nexit 1\n')
        failures = (("mktemp", "подготовка", None),
                    ("tail", "чтение", "+1"),
                    ("sh", "запись", None),
                    ("wc", "размер", None),
                    ("tail", "обрезка", "5"),
                    ("mv", "публикация", None))
        for tool, phase, argument in failures:
            with self.subTest(phase=phase):
                previous = read(self.log)
                before = set(os.listdir(state))
                if tool == "sh":
                    self.stream_printf(fail=True)
                elif argument:
                    self.stub(tool, '#!/bin/sh\n[ "$2" != %s ] || exit 1\n'
                                    'exec %s "$@"\n' % (shlex.quote(argument),
                                                         shlex.quote(shutil.which(tool))))
                elif tool == "mv":
                    self.stub(tool, '#!/bin/sh\n[ "$2" != %s ] || exit 1\n'
                                    'exec %s "$@"\n' % (shlex.quote(self.log),
                                                         shlex.quote(shutil.which("mv"))))
                else:
                    self.stub(tool, "#!/bin/sh\nexit 1\n")
                extra = {"QUOTA_REFRESH_LOG_LIMIT": "1", "QUOTA_REFRESH_LOG_KEEP": "5"}
                self.assertEqual(self.run_hook(extra_env=extra), 0)
                try:
                    if tool == "sh":
                        self.assertTrue(awaited(self.paused), "запись не началась")
                        self.assertEqual(read(self.log), previous)
                finally:
                    self.resume_writer()
                    self.assertTrue(self.wait_unlocked(), "замок остался после отказа")
                    os.unlink(os.path.join(self.bin, tool))
                self.assertEqual(read(self.log), previous, "отказ повредил прежний журнал")
                self.assertEqual(read(self.error), "ошибка записи журнала: %s\n" % phase)
                self.assertEqual(set(os.listdir(state)), before | {"quota-refresh.error"},
                                 "после отказа остались временные файлы")
                self.assertEqual(self.run_hook(), 0)
                self.assertTrue(self.wait_unlocked(), "следующий запуск не завершился")
                self.assertFalse(os.path.exists(self.error), "ошибка не очищена после успеха")
                self.assertTrue(read(self.log).startswith(previous), "при повторе потерялась история")
                self.assertEqual(set(os.listdir(state)), before)

    def test_nothing_to_take_a_snapshot_with(self):
        # Уходим молча и следов не оставляем, ругаться на это дело devkitctl
        # doctor, а не каждой сессии.
        bare = os.path.join(self.tmp, "nohome")
        os.makedirs(bare)
        self.assertEqual(self.run_hook(home=bare, path=self.sys), 0)
        self.assertFalse(os.path.isdir(os.path.join(bare, ".devkit")))


if __name__ == "__main__":
    unittest.main(verbosity=0)
