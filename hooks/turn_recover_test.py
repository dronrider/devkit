#!/usr/bin/env python3
"""Продолжение после обрыва: границы, гонки, лимит и подтверждение сокета."""
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import threading
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).parent))
import hookio
import turn_recover as recovery
import notify


def event(**kw):
    data = dict(kind=hookio.TURN_FAILED, reason="server_error", session="abcd1234",
                cwd="/work", transcript="", message="API Error: " + recovery.ERROR, agent=None)
    data.update(kw)
    return hookio.Session(**data)


class RecoveryTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.env = mock.patch.dict(os.environ, {"HOME": self.tmp.name}, clear=True)
        self.env.start()
        self.addCleanup(self.env.stop)
        self.row = dict(sessionId="abcd1234-full", pid=os.getpid(), kind="interactive",
                        config=self.tmp.name, updatedAt=1)

    def test_exact_error_only(self):
        with mock.patch.object(recovery, "peer", return_value=self.row):
            self.assertEqual(recovery.eligible(event()), self.row)
            self.assertEqual(recovery.eligible(event(message="API Error: Connection to the API was lost (StreamTruncated). Try again.")), self.row)
            for text in ("Insufficient balance", "401 Unauthorized", "529 Overloaded", "timed out", "bad request", "credit: " + recovery.ERROR):
                self.assertIsNone(recovery.eligible(event(message=text)))
            self.assertIsNone(recovery.eligible(event(agent="worker")))
            for key in ("DEVKIT_RUN_DEPTH", "DEVKIT_GOAL_TMUX", "DEVKIT_GOAL_SHELL", "DEVKIT_NOTIFY_OFF"):
                with mock.patch.dict(os.environ, {key: "1"}):
                    self.assertIsNone(recovery.eligible(event()))

    def test_registry_rejects_headless_and_ambiguous_prefix(self):
        directory = Path(self.tmp.name) / ".claude" / "sessions"
        directory.mkdir(parents=True)
        path = directory / "1.json"
        path.write_text(json.dumps(self.row))
        self.assertEqual(recovery.peer("abcd1234")["pid"], os.getpid())
        row = dict(self.row, kind="headless")
        path.write_text(json.dumps(row))
        self.assertIsNone(recovery.peer("abcd1234"))
        path.write_text(json.dumps(self.row))
        (directory / "2.json").write_text(json.dumps(dict(self.row, sessionId="abcd1234-other")))
        self.assertIsNone(recovery.peer("abcd1234"))

    def test_schedule_deduplicates_and_caps(self):
        with mock.patch.object(recovery, "peer", return_value=self.row), mock.patch.object(recovery.subprocess, "Popen") as popen:
            self.assertTrue(recovery.schedule(event()))
            self.assertTrue(recovery.schedule(event()))
            self.assertEqual(popen.call_count, 1)
            self.assertTrue(popen.call_args.kwargs["start_new_session"])
            recovery.reset(event(kind=hookio.PROMPT_SUBMIT))
            with recovery.locked("abcd1234") as state:
                state["tries"] = 3
            self.assertFalse(recovery.schedule(event()))
            recovery.reset(event(kind=hookio.TURN_DONE))
            self.assertTrue(recovery.schedule(event()))

    def test_launch_failure_does_not_claim_success(self):
        with mock.patch.object(recovery, "peer", return_value=self.row), mock.patch.object(recovery.subprocess, "Popen", side_effect=OSError("no")):
            self.assertFalse(recovery.schedule(event()))
        self.assertNotIn("nonce", json.loads(recovery.state_path("abcd1234").read_text()))

    def worker(self, result="socket", changed=False):
        with recovery.locked("abcd1234") as state:
            state.update(nonce="nonce", tries=1)
        data = dict(sess=event()._asdict(), peer=self.row, nonce="nonce", transcript=None, hook_pid=99999999)
        with mock.patch.object(recovery.time, "sleep"), mock.patch.object(recovery, "peer", return_value=self.row), mock.patch.object(recovery, "changed", return_value=[1, 2] if changed else None), mock.patch.object(recovery, "socket_send", return_value=result) as send, mock.patch.object(recovery, "tmux_send", return_value="tmux") as tmux, mock.patch.object(notify, "log") as log, mock.patch.object(notify, "deliver") as deliver:
            recovery.work(data)
            return send, tmux, log, deliver

    def test_success_logs_attempt_without_banner(self):
        send, tmux, log, deliver = self.worker()
        send.assert_called_once()
        tmux.assert_not_called()
        self.assertIn("2/3", log.call_args.args[3])
        deliver.assert_not_called()

    def test_ambiguous_delivery_never_falls_back(self):
        send, tmux, log, deliver = self.worker("ambiguous")
        tmux.assert_not_called()
        deliver.assert_called_once()

    def test_missing_socket_uses_tmux(self):
        send, tmux, log, deliver = self.worker("unavailable")
        tmux.assert_called_once()
        deliver.assert_not_called()

    def test_concurrent_user_input_cancels(self):
        send, tmux, log, deliver = self.worker(changed=True)
        send.assert_not_called()
        tmux.assert_not_called()

    def test_only_user_messages_change_fingerprint(self):
        transcript = Path(self.tmp.name) / "turn.jsonl"
        transcript.write_text('{"type":"user","uuid":"first"}\n')
        sess = event(transcript=str(transcript))
        self.assertEqual(recovery.changed(sess), "first")
        with transcript.open("a") as stream:
            stream.write('{"type":"system","uuid":"hook"}\n')
            stream.write('{"type":"assistant","uuid":"failed"}\n')
        self.assertEqual(recovery.changed(sess), "first")
        with transcript.open("a") as stream:
            stream.write('{"type":"user","uuid":"second"}\n')
        self.assertEqual(recovery.changed(sess), "second")

    def test_user_typing_blocks_tmux_fallback(self):
        from subprocess import CompletedProcess
        with mock.patch.object(recovery.subprocess, "run", return_value=CompletedProcess([], 0, "\u276f user is typing\n")) as call:
            self.assertEqual(recovery.tmux_send(dict(self.row, tmux="chat:@1.%1")), "unavailable")
            self.assertEqual(call.call_count, 1)

    def test_tmux_empty_prompt_sends_literal_then_enter(self):
        from subprocess import CompletedProcess
        with mock.patch.object(recovery.subprocess, "run", return_value=CompletedProcess([], 0, "\u276f\nhelp hint\n")) as call:
            self.assertEqual(recovery.tmux_send(dict(self.row, tmux="chat:@1.%1")), "tmux")
            self.assertEqual(call.call_args_list[1].args[0], ["tmux", "send-keys", "-t", "%1", "-l", recovery.MESSAGE])
            self.assertEqual(call.call_args_list[2].args[0][-1], "Enter")

    def test_socket_frame_and_ack(self):
        path = str(Path(self.tmp.name) / "peer.sock")
        (Path(self.tmp.name) / "settings.json").write_text('{"crossSessionInbound":"accept"}')
        server = socket.socket(socket.AF_UNIX)
        self.addCleanup(server.close)
        server.bind(path)
        server.listen()
        frames = []
        def receive():
            with server.accept()[0] as conn:
                data = b""
                while True:
                    part = conn.recv(4096)
                    if not part:
                        break
                    data += part
                frames.append(json.loads(data))
        worker = threading.Thread(target=receive)
        worker.start()
        self.assertEqual(recovery.socket_send(dict(self.row, messagingSocketPath=path)), "socket")
        worker.join(2)
        self.assertEqual(frames[0]["priority"], "next")
        self.assertIn(recovery.MESSAGE, frames[0]["message"]["content"])

    def test_notify_suppresses_only_scheduled_recovery(self):
        sess = event()
        with mock.patch.object(hookio, "session_event", return_value=sess), mock.patch.object(notify, "session_tree", return_value="/work"), mock.patch.object(notify, "event_target", return_value=(None, None)), mock.patch.object(notify, "sandbox_reason", return_value=None), mock.patch.object(recovery, "schedule", return_value=True), mock.patch.object(notify, "deliver") as deliver:
            self.assertEqual(notify.run_hook("claude-code"), 0)
            deliver.assert_not_called()

    def test_exhaustion_is_loud_and_mentions_three_attempts(self):
        with recovery.locked("abcd1234") as state:
            state["tries"] = 3
        with mock.patch.object(hookio, "session_event", return_value=event()), mock.patch.object(notify, "session_tree", return_value="/work"), mock.patch.object(notify, "event_target", return_value=(None, None)), mock.patch.object(notify, "sandbox_reason", return_value=None), mock.patch.object(recovery, "peer", return_value=self.row), mock.patch.object(recovery.subprocess, "Popen") as popen, mock.patch.object(notify, "allow", return_value=True), mock.patch.object(notify, "click_target", return_value=None), mock.patch.object(notify, "deliver", return_value=("stub", 0)) as deliver:
            notify.run_hook("claude-code")
            popen.assert_not_called()
            self.assertIn("3 раза", deliver.call_args.args[1])
            self.assertEqual(deliver.call_args.args[5], notify.LOUD)

    def test_manual_prompt_cancels_pending_worker(self):
        with recovery.locked("abcd1234") as state:
            state.update(nonce="pending", tries=2)
        recovery.reset(event(kind=hookio.PROMPT_SUBMIT))
        data = dict(sess=event()._asdict(), peer=self.row, nonce="pending", transcript=None, hook_pid=99999999)
        with mock.patch.object(recovery.time, "sleep"), mock.patch.object(recovery, "socket_send") as send:
            recovery.work(data)
            send.assert_not_called()
        self.assertEqual(json.loads(recovery.state_path("abcd1234").read_text())["tries"], 2)


if __name__ == "__main__":
    unittest.main()
