"""Тесты форматтера native-проекции журнала (DK-1624).

Фикстуры повторяют форму ручной проекции диспетчера: seed-журнал новой
синтетической сессии с парой RESUME_PROBE_READY и публичная история с
текстом, tool_use, tool_result и служебными записями. Проверяются цепочка
UUID, сохранность блоков, исключение thinking и отказы на чужом seed,
неверном UUID, неверном каталоге и занятом имени файла.
"""
import io
import json
import shutil
import tempfile
import unittest
import uuid
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

import native_projection

SEED_UUID = "11111111-1111-4111-8111-111111111111"
CWD = "/tmp/dk1624-seed-project"


def write_seed(directory, prefix="", answer="RESUME_PROBE_READY."):
    """Seed-журнал одной пары в форме native-транскрипта Claude Code."""
    user_uuid = str(uuid.uuid4())
    records = [
        {"parentUuid": None, "isSidechain": False, "userType": "external",
         "cwd": CWD, "sessionId": SEED_UUID, "version": "2.1.312",
         "gitBranch": "dk-1624", "type": "user",
         "message": {"role": "user", "content": [
             {"type": "text", "text": prefix + native_projection.SEED_PROMPT}]},
         "uuid": user_uuid, "timestamp": "2026-11-06T18:00:00.000Z"},
        {"parentUuid": user_uuid, "isSidechain": False,
         "userType": "external", "cwd": CWD, "sessionId": SEED_UUID,
         "version": "2.1.312", "gitBranch": "dk-1624", "type": "assistant",
         "message": {"role": "assistant", "model": "glm-5.3", "content": [
             {"type": "text", "text": answer}]},
         "uuid": str(uuid.uuid4()), "timestamp": "2026-11-06T18:00:01.000Z"},
    ]
    path = Path(directory) / (SEED_UUID + ".jsonl")
    path.write_text("".join(json.dumps(record, ensure_ascii=False) + "\n"
                            for record in records), encoding="utf-8")
    return path


def history_events():
    return [
        {"type": "system", "subtype": "init"},
        {"type": "user", "message": {"role": "user", "content": [
            {"type": "text", "text": "user_fact = CEDAR-5297"}]}},
        {"type": "assistant", "message": {"role": "assistant", "content": [
            {"type": "thinking", "thinking": "размышление не переносится"},
            {"type": "text", "text": "Делаю."}]}},
        {"type": "assistant", "message": {"role": "assistant", "content": [
            {"type": "tool_use", "id": "probe_tool_1", "name": "Bash",
             "input": {"command": "printf historical-only"}}]}},
        {"type": "user", "message": {"role": "user", "content": [
            {"type": "tool_result", "tool_use_id": "probe_tool_1",
             "is_error": True, "content": "File has not been read yet"}]}},
        {"type": "assistant", "message": {"role": "assistant", "content": [
            {"type": "tool_use", "id": "codex_call_1", "name": "exec",
             "input": {"raw_input": "text(await tools.exec_command());"},
             "codex_type": "custom_tool_call"}]}},
        {"type": "user", "message": {"role": "user", "content": [
            {"type": "tool_result", "tool_use_id": "codex_call_1",
             "content": [{"type": "text", "text": "Script completed"}]}]}},
        {"type": "result", "subtype": "success"},
    ]


class Stand(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="dk1624_proj_"))
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)
        self.seed_path = write_seed(self.dir)
        self.history_path = self.dir / "history.jsonl"
        self.history_path.write_text(
            "".join(json.dumps(event, ensure_ascii=False) + "\n"
                    for event in history_events()), encoding="utf-8")

    def run_main(self, seed_uuid=SEED_UUID, cwd=CWD, seed=None,
                 history=None):
        argv = ["--seed", str(seed or self.seed_path),
                "--history", str(history or self.history_path),
                "--seed-uuid", seed_uuid, "--cwd", cwd,
                "--model", "glm-5.3"]
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = native_projection.main(argv)
        return code, out.getvalue(), err.getvalue()

    def journals(self):
        return sorted(path.name for path in self.dir.glob("*.jsonl"))


class ProjectionTest(Stand):
    def test_creates_journal_with_chain_and_preserved_blocks(self):
        seed_before = self.seed_path.read_bytes()
        code, out, err = self.run_main()
        self.assertEqual(code, 0, err)
        new_uuid = [line.split(": ")[1] for line in out.splitlines()
                    if line.startswith("native uuid: ")][0]
        target = self.dir / (new_uuid + ".jsonl")
        self.assertTrue(target.exists())
        self.assertEqual(set(self.journals()),
                         {"history.jsonl", SEED_UUID + ".jsonl",
                          new_uuid + ".jsonl"})
        # seed-журнал не тронут
        self.assertEqual(self.seed_path.read_bytes(), seed_before)
        records = [json.loads(line) for line in
                   target.read_text(encoding="utf-8").splitlines()]
        # seed-пара и шесть записей истории: thinking-запись исчезла
        self.assertEqual([record["type"] for record in records],
                         ["user", "assistant", "user", "assistant",
                          "assistant", "user", "assistant", "user"])
        self.assertIsNone(records[0]["parentUuid"])
        for previous, current in zip(records, records[1:]):
            self.assertEqual(current["parentUuid"], previous["uuid"])
        uuids = [record["uuid"] for record in records]
        self.assertEqual(len(uuids), len(set(uuids)))
        for record in records:
            self.assertEqual(record["sessionId"], new_uuid)
            self.assertFalse(record["isSidechain"])
            self.assertEqual(record["cwd"], CWD)
            self.assertEqual(record["version"], "2.1.312")
            self.assertEqual(record["gitBranch"], "dk-1624")
        stamps = [record["timestamp"] for record in records]
        self.assertEqual(stamps, sorted(stamps))
        self.assertEqual(len(stamps), len(set(stamps)))
        # seed-пара спроецирована точным вопросом и ответом
        self.assertEqual(records[0]["message"]["content"],
                         [{"type": "text", "text":
                           native_projection.SEED_PROMPT}])
        self.assertEqual(records[1]["message"]["content"],
                         [{"type": "text", "text": "RESUME_PROBE_READY."}])
        # тексты истории сохранены, thinking исключён
        self.assertEqual(records[2]["message"]["content"],
                         [{"type": "text", "text": "user_fact = CEDAR-5297"}])
        self.assertEqual(
            [block["type"] for block in records[3]["message"]["content"]],
            ["text"])
        self.assertEqual(records[3]["message"]["content"][0]["text"], "Делаю.")
        # tool_use с аргументами и полями custom-инструмента Codex
        bash_use = records[4]["message"]["content"][0]
        self.assertEqual(bash_use["input"],
                         {"command": "printf historical-only"})
        codex_use = records[6]["message"]["content"][0]
        self.assertEqual(codex_use["input"]["raw_input"],
                         "text(await tools.exec_command());")
        self.assertEqual(codex_use["codex_type"], "custom_tool_call")
        # tool_result с признаком ошибки и списочным содержимым
        self.assertTrue(records[5]["message"]["content"][0]["is_error"])
        self.assertEqual(records[7]["message"]["content"][0]["content"],
                         [{"type": "text", "text": "Script completed"}])
        # форма assistant-записи
        self.assertEqual(records[4]["message"]["id"],
                         "msg_" + records[4]["uuid"])
        self.assertEqual(records[4]["message"]["model"], "glm-5.3")
        self.assertEqual(records[4]["message"]["stop_reason"], "tool_use")
        self.assertEqual(records[3]["message"]["stop_reason"], "end_turn")
        self.assertEqual(records[4]["message"]["usage"],
                         {"input_tokens": 0, "output_tokens": 0})

    def test_cli_prefix_of_seed_question_not_copied(self):
        self.seed_path.unlink()
        self.seed_path = write_seed(
            self.dir, prefix="<command-message>serve</command-message>\n\n")
        code, out, err = self.run_main()
        self.assertEqual(code, 0, err)
        new_uuid = [line.split(": ")[1] for line in out.splitlines()
                    if line.startswith("native uuid: ")][0]
        target = self.dir / (new_uuid + ".jsonl")
        first = json.loads(
            target.read_text(encoding="utf-8").splitlines()[0])
        self.assertEqual(first["message"]["content"],
                         [{"type": "text", "text":
                           native_projection.SEED_PROMPT}])


class RefusalTest(Stand):
    def test_wrong_seed_uuid_refuses_without_files(self):
        code, out, err = self.run_main(seed_uuid=str(uuid.uuid4()))
        self.assertEqual(code, 2)
        self.assertIn("UUID", err)
        self.assertEqual(set(self.journals()),
                         {"history.jsonl", SEED_UUID + ".jsonl"})

    def test_wrong_cwd_refuses(self):
        code, out, err = self.run_main(cwd="/tmp/other-project")
        self.assertEqual(code, 2)
        self.assertIn("каталог", err)
        self.assertEqual(set(self.journals()),
                         {"history.jsonl", SEED_UUID + ".jsonl"})

    def test_foreign_conversation_in_seed_refuses(self):
        with self.seed_path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps({
                "type": "user", "cwd": CWD, "sessionId": SEED_UUID,
                "message": {"role": "user", "content": "лишняя реплика"},
            }, ensure_ascii=False) + "\n")
        code, out, err = self.run_main()
        self.assertEqual(code, 2)
        self.assertIn("не одна пара", err)
        self.assertEqual(set(self.journals()),
                         {"history.jsonl", SEED_UUID + ".jsonl"})

    def test_seed_without_marker_answer_refuses(self):
        self.seed_path.unlink()
        self.seed_path = write_seed(self.dir, answer="Готово.")
        code, out, err = self.run_main()
        self.assertEqual(code, 2)
        self.assertIn("RESUME_PROBE_READY", err)

    def test_unknown_history_type_refuses(self):
        stray = self.dir / "stray.jsonl"
        stray.write_text(json.dumps({"type": "stream_event"}) + "\n",
                         encoding="utf-8")
        code, out, err = self.run_main(history=stray)
        self.assertEqual(code, 2)
        self.assertIn("неизвестный тип", err)
        self.assertEqual(set(self.journals()),
                         {"history.jsonl", SEED_UUID + ".jsonl",
                          "stray.jsonl"})

    def test_existing_file_is_not_replaced(self):
        occupied = str(uuid.uuid4())
        target = self.dir / (occupied + ".jsonl")
        target.write_text("занято\n", encoding="utf-8")
        with self.assertRaises(native_projection.ProjectionError):
            native_projection.write_journal(
                self.seed_path, occupied, [{"type": "user"}])
        self.assertEqual(target.read_text(encoding="utf-8"), "занято\n")


if __name__ == "__main__":
    unittest.main()
