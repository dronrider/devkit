"""Тесты преобразования истории между Claude Code и Codex (DK-1624)."""
import json
import unittest

import history_adapter


def journal_line(event):
    return json.dumps(event, ensure_ascii=False)


class ForwardTest(unittest.TestCase):
    def test_full_journal(self):
        lines = [
            journal_line({"type": "system", "subtype": "init"}),
            journal_line({"type": "user", "message": {"role": "user",
                "content": [{"type": "text", "text": "user_fact = A1"}]}}),
            journal_line({"type": "assistant", "message":
                {"role": "assistant", "content": [
                    {"type": "text", "text": "Делаю."},
                    {"type": "tool_use", "id": "t1", "name": "Bash",
                     "input": {"command": "echo hi"}}]}}),
            journal_line({"type": "user", "message": {"role": "user",
                "content": [{"type": "tool_result", "tool_use_id": "t1",
                             "content": "hi"}]}}),
            journal_line({"type": "assistant", "message":
                {"role": "assistant", "content": [
                    {"type": "text", "text": "Готово."}]}}),
            journal_line({"type": "result", "subtype": "success"}),
        ]
        items = history_adapter.claude_journal_to_codex_items(lines)
        self.assertEqual(items, [
            {"type": "message", "role": "user",
             "content": [{"type": "input_text", "text": "user_fact = A1"}]},
            {"type": "message", "role": "assistant",
             "content": [{"type": "output_text", "text": "Делаю."}]},
            {"type": "function_call", "call_id": "t1", "name": "Bash",
             "arguments": json.dumps({"command": "echo hi"},
                                     ensure_ascii=False)},
            {"type": "function_call_output", "call_id": "t1", "output": "hi"},
            {"type": "message", "role": "assistant",
             "content": [{"type": "output_text", "text": "Готово."}]},
        ])

    def test_user_content_plain_string(self):
        line = journal_line({"type": "user", "message": {
            "role": "user", "content": "просто текст"}})
        items = history_adapter.claude_journal_to_codex_items([line])
        self.assertEqual(items, [
            {"type": "message", "role": "user",
             "content": [{"type": "input_text", "text": "просто текст"}]}])

    def test_tool_result_list_content(self):
        line = journal_line({"type": "user", "message": {"role": "user",
            "content": [{"type": "tool_result", "tool_use_id": "t2",
                         "content": [{"type": "text", "text": "часть 1"},
                                     {"type": "text", "text": "часть 2"}]}]}})
        items = history_adapter.claude_journal_to_codex_items([line])
        self.assertEqual(items, [
            {"type": "function_call_output", "call_id": "t2",
             "output": "часть 1\nчасть 2"}])

    def test_text_between_tool_calls_starts_new_message(self):
        line = journal_line({"type": "assistant", "message":
            {"role": "assistant", "content": [
                {"type": "text", "text": "до"},
                {"type": "tool_use", "id": "t3", "name": "Bash",
                 "input": {}},
                {"type": "text", "text": "после"}]}})
        items = history_adapter.claude_journal_to_codex_items([line])
        self.assertEqual([item["type"] for item in items],
                         ["message", "function_call", "message"])
        self.assertEqual(items[2]["content"][0]["text"], "после")


class ReverseTest(unittest.TestCase):
    SAMPLE = [
        {"type": "message", "role": "user",
         "content": [{"type": "input_text", "text": "user_fact = A1"}]},
        {"type": "message", "role": "assistant",
         "content": [{"type": "output_text", "text": "Делаю."}]},
        {"type": "function_call", "call_id": "t1", "name": "Bash",
         "arguments": json.dumps({"command": "echo hi"},
                                 ensure_ascii=False)},
        {"type": "function_call_output", "call_id": "t1", "output": "hi"},
    ]

    def test_exchange_kinds(self):
        exchange = history_adapter.codex_items_to_exchange(self.SAMPLE)
        self.assertEqual([step["kind"] for step in exchange],
                         ["text", "text", "tool_call", "tool_output"])
        self.assertEqual(exchange[0]["role"], "user")
        self.assertEqual(exchange[1]["role"], "assistant")
        self.assertEqual(exchange[2]["name"], "Bash")

    def test_stream_lines_structure(self):
        exchange = history_adapter.codex_items_to_exchange(self.SAMPLE)
        lines = [json.loads(line) for line in
                 history_adapter.exchange_to_claude_stream_lines(exchange)]
        self.assertEqual([event["type"] for event in lines],
                         ["user", "assistant", "assistant", "user"])
        self.assertEqual(lines[0]["message"]["content"][0]["text"],
                         "user_fact = A1")
        self.assertIn("uuid", lines[0])
        tool_use = lines[2]["message"]["content"][0]
        self.assertEqual(tool_use["type"], "tool_use")
        self.assertEqual(tool_use["id"], "t1")
        self.assertEqual(tool_use["input"], {"command": "echo hi"})
        tool_result = lines[3]["message"]["content"][0]
        self.assertEqual(tool_result["type"], "tool_result")
        self.assertEqual(tool_result["tool_use_id"], "t1")

    def test_roundtrip_returns_same_items(self):
        exchange = history_adapter.codex_items_to_exchange(self.SAMPLE)
        lines = history_adapter.exchange_to_claude_stream_lines(exchange)
        items = history_adapter.claude_journal_to_codex_items(lines)
        self.assertEqual(items, self.SAMPLE)


if __name__ == "__main__":
    unittest.main()
