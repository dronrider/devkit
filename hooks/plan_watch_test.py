#!/usr/bin/env python3
"""Самопроверка сторожа плана: сдача сессии, чей план разошёлся с работой
(DK-609), и сдача сессии, у которой плана нет вовсе (DK-978). Прогон идёт
подпроцессом с живым образцом события конца хода, как в settings.json, а
план, транскрипт, журнал отметок и конфиг порогов подставляются каталогом
теста: в живой ~/.devkit прогон не пишет.
"""
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "plan-watch.py")
DATA = os.path.join(HERE, "testdata", "claude-code")

SESSION = "71fda467-4c7c-448e-8b92-3a9b7ded782a"
SHORT = SESSION[:8]
HOUR = 3600.0

CONFIG = """[plan]
step_hours = 3
done_turns = 2
still_turns = 10
"""


def sample(name):
    with open(os.path.join(DATA, name), encoding="utf-8") as f:
        return json.load(f)


def plan(*pairs):
    return [{"text": text, "state": state} for text, state in pairs]


class Stand(object):
    """Дом сторожа на время теста: каталог планов, журнал отметок, конфиг."""

    def __init__(self, tmp):
        self.tmp = tmp
        self.plans = os.path.join(tmp, "plans")
        os.makedirs(self.plans)
        self.turns = os.path.join(tmp, "turns.log")
        self.config = os.path.join(tmp, "plan.toml")
        self.log = os.path.join(tmp, "plan-watch.log")
        with open(self.config, "w", encoding="utf-8") as f:
            f.write(CONFIG)

    def lay_plan(self, items, age=0.0, label=""):
        """План сессии файлом. С меткой имя выходит таким же, как у agentctl
        plan с флагом --label."""
        name = "%s-sub-%s.json" % (SESSION, label) if label else "%s.json" % SESSION
        path = os.path.join(self.plans, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(items, f, ensure_ascii=False)
        when = time.time() - age
        os.utime(path, (when, when))
        return path

    def lay_turns(self, count, since=60.0):
        """Столько ходов сессии, доработанных после правки плана."""
        with open(self.turns, "w", encoding="utf-8") as f:
            for i in range(count):
                when = time.strftime("%Y-%m-%dT%H:%M:%S",
                                     time.localtime(time.time() - since + 1 + i))
                f.write("%s сессия %s ход кончен повод - дерево /tmp\n" % (when, SHORT))

    def lay_transcript(self, turns):
        """Транскрипт сессии файлом: turns это список признаков «в ходе был
        вызов инструмента» по одному на ход, ходы порезаны настоящими
        репликами человека. Путь к файлу возвращается для события."""
        path = os.path.join(self.tmp, "transcript.jsonl")
        lines = []
        for i, used_tools in enumerate(turns):
            lines.append(json.dumps({"type": "user", "message": {
                "role": "user", "content": "реплика человека %d" % i}}, ensure_ascii=False))
            if used_tools:
                lines.append(json.dumps({"type": "assistant", "message": {
                    "role": "assistant", "content": [
                        {"type": "tool_use", "name": "Bash", "input": {}}]}}, ensure_ascii=False))
                # эхо результата инструмента: не реплика человека, границей хода не служит
                lines.append(json.dumps({"type": "user", "message": {
                    "role": "user", "content": [{"type": "tool_result", "content": "ок"}]}},
                    ensure_ascii=False))
            lines.append(json.dumps({"type": "assistant", "message": {
                "role": "assistant", "content": [{"type": "text", "text": "ответ %d" % i}]}},
                ensure_ascii=False))
        with open(path, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
        return path

    def run(self, event=None, config=None, transcript=None, extra_env=None):
        """Прогон хука подпроцессом. Возврат (код, разобранная сдача)."""
        event = event or sample("turn-done.json")
        event["session_id"] = SESSION
        if transcript is not None:
            event["transcript_path"] = transcript
        env = dict(os.environ,
                   DEVKIT_PLAN_DIR=self.plans,
                   DEVKIT_TURN_MARK_LOG=self.turns,
                   DEVKIT_PLAN_CONFIG=config or self.config,
                   DEVKIT_PLAN_WATCH_LOG=self.log)
        env.pop("DEVKIT_PLAN_WATCH_OFF", None)
        env.update(extra_env or {})
        p = subprocess.run([sys.executable, HOOK, "--hook"], input=json.dumps(event),
                           capture_output=True, text=True, env=env)
        said = json.loads(p.stdout) if p.stdout.strip() else {}
        return p.returncode, said

    def said_log(self):
        if not os.path.exists(self.log):
            return ""
        with open(self.log, encoding="utf-8") as f:
            return f.read()


class TestWatch(unittest.TestCase):
    def stand(self):
        tmp = tempfile.TemporaryDirectory(prefix="plan-watch-")
        self.addCleanup(tmp.cleanup)
        return Stand(tmp.name)

    def test_plan_that_answers_the_work_is_left_alone(self):
        """Свежий план с идущим пунктом сторожа не касается: блокировать тут
        нечего, и сессия уходит спать своим чередом."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "in_progress")), age=600.0)
        s.lay_turns(1)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {})
        self.assertIn("план отвечает делу", s.said_log())

    def test_log_entries_land_on_separate_lines(self):
        """Журнал сторожа читается построчно (`tail`, grep по времени), и
        запись без перевода строки в конце склеивает соседние заходы в одну
        нечитаемую строку (ревью DK-609)."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "in_progress")), age=600.0)
        s.lay_turns(1)
        s.run()
        s.run()
        lines = [ln for ln in s.said_log().splitlines() if ln.strip()]
        self.assertEqual(len(lines), 2, "две сдачи журнала легли не двумя строками: %r" % lines)

    def test_old_running_step_is_handed_over(self):
        """Первый признак: пункт помечен идущим, а план не менялся дольше
        порога. Ровно так и висели два шага находки DK-609."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "in_progress")), age=4 * HOUR)
        s.lay_turns(1, since=4 * HOUR - 60)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")
        self.assertIn("«правка»", said.get("reason", ""))
        self.assertIn("agentctl plan set", said.get("reason", ""))

    def test_closed_plan_at_a_working_session_is_handed_over(self):
        """Второй признак: все пункты закрыты, а сессия работает дальше. Ход,
        которым закрылся последний пункт, сторожа не будит."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "completed")), age=600.0)
        s.lay_turns(1)
        code, said = s.run()
        self.assertEqual(said, {}, "один ход поверх закрытого плана это ещё не расхождение")

        s.lay_turns(3)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")
        self.assertIn("все пункты плана закрыты", said.get("reason", ""))

    def test_plan_standing_for_many_turns_is_handed_over(self):
        """Третий признак: план не менялся заданное число ходов, а незакрытые
        пункты в нём есть."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "pending")), age=600.0)
        s.lay_turns(9)
        code, said = s.run()
        self.assertEqual(said, {}, "девять ходов это ещё не порог")

        s.lay_turns(12)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")
        self.assertIn("не менялся 12 ходов", said.get("reason", ""))

    def test_turns_of_another_session_are_not_counted(self):
        """Журнал отметок общий на машину, и чужие ходы в счёт не идут."""
        s = self.stand()
        s.lay_plan(plan(("правка", "pending")), age=600.0)
        with open(s.turns, "w", encoding="utf-8") as f:
            for i in range(20):
                when = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(time.time() - 300 + i))
                f.write("%s сессия cfe5c063 ход кончен повод - дерево /tmp\n" % when)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_turns_before_the_plan_are_not_counted(self):
        """Ходы, кончившиеся до правки плана, план не трогали и в счёт не
        идут: иначе свежий план сдавался бы стоячим."""
        s = self.stand()
        s.lay_plan(plan(("правка", "pending")), age=60.0)
        s.lay_turns(20, since=6 * HOUR)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_session_without_a_plan_is_left_alone(self):
        """Сессию без плана сторожит не эта проверка: тут ему сверять нечего."""
        s = self.stand()
        s.lay_turns(20)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_continued_turn_is_skipped(self):
        """Ход, продолженный стоп-хуком, сторож пропускает: второй заход
        закрутил бы сессию в цикле."""
        s = self.stand()
        s.lay_plan(plan(("правка", "in_progress")), age=4 * HOUR)
        s.lay_turns(1, since=4 * HOUR - 60)
        event = sample("turn-done.json")
        event["stop_hook_active"] = True
        code, said = s.run(event)
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_subagent_stop_is_not_the_end_of_a_turn(self):
        s = self.stand()
        s.lay_plan(plan(("правка", "in_progress")), age=4 * HOUR)
        code, said = s.run(sample("subagent-done.json"))
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_missing_config_keeps_the_watch_quiet(self):
        """Порогов в коде нет вовсе, и без конфига сторож молчит. Молчание это
        видно в его журнале: иначе оно неотличимо от неподключённого хука."""
        s = self.stand()
        s.lay_plan(plan(("правка", "in_progress")), age=4 * HOUR)
        s.lay_turns(1, since=4 * HOUR - 60)
        code, said = s.run(config=os.path.join(s.tmp, "нет.toml"))
        self.assertEqual(code, 0)
        self.assertEqual(said, {})
        self.assertIn("пропуск", s.said_log())

    def test_own_config_holds_the_thresholds(self):
        """Пороги правятся конфигом, а не кодом: свой конфиг двигает порог, и
        ход сторожа меняется вместе с ним."""
        s = self.stand()
        s.lay_plan(plan(("правка", "pending")), age=600.0)
        s.lay_turns(4)
        code, said = s.run()
        self.assertEqual(said, {}, "при пороге в десять ходов четыре это тишина")

        loose = os.path.join(s.tmp, "loose.toml")
        with open(loose, "w", encoding="utf-8") as f:
            f.write("[plan]\nstep_hours = 3\ndone_turns = 2\nstill_turns = 3\n")
        code, said = s.run(config=loose)
        self.assertEqual(said.get("decision"), "block")

    def test_broken_event_does_not_break_the_session(self):
        s = self.stand()
        s.lay_plan(plan(("правка", "in_progress")), age=4 * HOUR)
        env = dict(os.environ, DEVKIT_PLAN_DIR=s.plans, DEVKIT_PLAN_CONFIG=s.config,
                   DEVKIT_PLAN_WATCH_LOG=s.log, DEVKIT_TURN_MARK_LOG=s.turns)
        env.pop("DEVKIT_PLAN_WATCH_OFF", None)
        p = subprocess.run([sys.executable, HOOK, "--hook"], input="не json",
                           capture_output=True, text=True, env=env)
        self.assertEqual(p.returncode, 0)
        self.assertEqual(p.stdout.strip(), "")

    def test_plan_written_with_a_label_is_the_plan_of_the_session(self):
        """Сессия, писавшая план с меткой, пока agentctl plan отбивала
        безымянную запись, файла без метки не имеет вовсе. Сторож, глядящий
        только на него, молчал бы там, где находка DK-609 и случилась."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "in_progress")),
                   age=4 * HOUR, label="dk609")
        s.lay_turns(1, since=4 * HOUR - 60)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")
        self.assertIn("«правка»", said.get("reason", ""))

    def test_the_freshest_labelled_plan_wins(self):
        """Меток у сессии бывает несколько, а план сессии один: берётся тот,
        который правился последним."""
        s = self.stand()
        s.lay_plan(plan(("старая работа", "in_progress")), age=9 * HOUR, label="old")
        s.lay_plan(plan(("разведка", "completed"), ("правка", "pending")),
                   age=600.0, label="new")
        s.lay_turns(1)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {}, "сторож взял устаревший план вместо свежего")

    def test_subagent_plan_does_not_wake_the_watch(self):
        """У сессии со своим планом файлы с меткой это планы субагентов.
        Брошенный идущим план мёртвого субагента обычное дело, и ругаться на
        него каждым ходом диспетчера сторож не должен."""
        s = self.stand()
        s.lay_plan(plan(("разведка", "completed"), ("правка", "in_progress")), age=600.0)
        s.lay_plan(plan(("чужая правка", "in_progress")), age=9 * HOUR, label="dk900")
        s.lay_turns(1)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_neighbour_file_is_not_a_plan(self):
        """Маска планов субагента отделяет метку дефисом, и сосед вроде
        <ID>-submit.json под неё не попадает."""
        s = self.stand()
        with open(os.path.join(s.plans, "%s-submit.json" % SESSION), "w",
                  encoding="utf-8") as f:
            json.dump(plan(("чужое", "in_progress")), f, ensure_ascii=False)
        s.lay_turns(20)
        code, said = s.run()
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_no_plan_second_turn_with_tools_is_handed_over(self):
        """Четвёртый признак (DK-978): плана нет вовсе, а второй ход подряд
        идёт с вызовами инструментов. Это буквально порог DK-881 «длиннее
        одного хода»."""
        s = self.stand()
        tr = s.lay_transcript([True, True])
        code, said = s.run(transcript=tr)
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")
        self.assertIn("плана", said.get("reason", ""))
        self.assertIn("agentctl plan set", said.get("reason", ""))
        self.assertIn("своего плана нет", s.said_log())

    def test_skill_result_does_not_fake_a_second_turn(self):
        """Результат инструмента Skill харнес кладёт не блоком tool_result, как
        у прочих инструментов, а тем же блоком text, что и у живой реплики
        человека, и вдобавок isMeta. Разбор по одной строке контента путал его
        со второй репликой, и один ответ с вызовом Skill внутри читался как
        два хода (живая находка на реальном транскрипте)."""
        s = self.stand()
        path = os.path.join(s.tmp, "skill-transcript.jsonl")
        lines = [
            {"type": "user", "message": {"role": "user", "content": "вопрос"}},
            {"type": "assistant", "message": {"role": "assistant", "content": [
                {"type": "tool_use", "name": "Skill", "input": {"skill": "board-chat"}}]}},
            {"type": "user", "isMeta": True, "message": {"role": "user", "content": [
                {"type": "text", "text": "Base directory for this skill: ..."}]}},
            {"type": "assistant", "message": {"role": "assistant", "content": [
                {"type": "tool_use", "name": "Bash", "input": {}}]}},
            {"type": "user", "message": {"role": "user", "content": [
                {"type": "tool_result", "content": "ок"}]}},
            {"type": "assistant", "message": {"role": "assistant", "content": [
                {"type": "text", "text": "ответ"}]}},
        ]
        with open(path, "w", encoding="utf-8") as f:
            f.write("\n".join(json.dumps(x, ensure_ascii=False) for x in lines) + "\n")
        code, said = s.run(transcript=path)
        self.assertEqual(code, 0)
        self.assertEqual(said, {}, "эхо инструмента Skill сочтено вторым ходом")

    def test_no_plan_single_turn_with_tools_is_left_alone(self):
        """Один ответ, пусть и с вызовами инструментов, это ещё не «длиннее
        одного хода», второго хода тут просто не было."""
        s = self.stand()
        tr = s.lay_transcript([True])
        code, said = s.run(transcript=tr)
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_no_plan_first_turn_without_tools_is_left_alone(self):
        """Второй ход с инструментами, а первый прошёл без них: подряд тут
        только один ход из двух, порог не достигнут."""
        s = self.stand()
        tr = s.lay_transcript([False, True])
        code, said = s.run(transcript=tr)
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_no_plan_check_is_skipped_when_own_plan_exists(self):
        """Свой файл плана есть, и до признака «плана нет» дело не доходит:
        сессию сверяют старые три признака расхождения."""
        s = self.stand()
        s.lay_plan(plan(("правка", "pending")), age=600.0)
        tr = s.lay_transcript([True, True])
        code, said = s.run(transcript=tr)
        self.assertEqual(code, 0)
        self.assertEqual(said, {})

    def test_no_plan_labelled_subagent_file_still_counts_as_no_plan(self):
        """Случай chat-54: план положил только субагент под своей меткой, у
        родителя своего плана нет. own_plan() взял бы файл субагента, а этот
        признак его не считает."""
        s = self.stand()
        s.lay_plan(plan(("чужая работа", "in_progress")), age=600.0, label="dk900")
        tr = s.lay_transcript([True, True])
        code, said = s.run(transcript=tr)
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")

    def test_child_session_env_does_not_exempt_from_no_plan_check(self):
        """Предмет DK-852: клиент 2.1.261 ставит CLAUDE_CODE_CHILD_SESSION=1
        каждому вызову Bash, у головы разговора и у субагента одинаково. Пока
        признак «плана нет» пропускал такую сессию, на этом клиенте он не
        срабатывал ни у кого."""
        s = self.stand()
        tr = s.lay_transcript([True, True])
        code, said = s.run(transcript=tr, extra_env={"CLAUDE_CODE_CHILD_SESSION": "1"})
        self.assertEqual(code, 0)
        self.assertEqual(said.get("decision"), "block")

    def test_off_switch_keeps_the_watch_quiet(self):
        s = self.stand()
        s.lay_plan(plan(("правка", "in_progress")), age=4 * HOUR)
        env = dict(os.environ, DEVKIT_PLAN_DIR=s.plans, DEVKIT_PLAN_CONFIG=s.config,
                   DEVKIT_PLAN_WATCH_LOG=s.log, DEVKIT_TURN_MARK_LOG=s.turns,
                   DEVKIT_PLAN_WATCH_OFF="1")
        event = sample("turn-done.json")
        event["session_id"] = SESSION
        p = subprocess.run([sys.executable, HOOK, "--hook"], input=json.dumps(event),
                           capture_output=True, text=True, env=env)
        self.assertEqual(p.returncode, 0)
        self.assertEqual(p.stdout.strip(), "")


if __name__ == "__main__":
    unittest.main()
