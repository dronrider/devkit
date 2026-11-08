"""Проверка настоящего клиента через локальный API: DEVKIT_LIVE_RECOVERY=1."""
import fcntl
import json
import os
import pty
import select
import socket
import struct
import subprocess
import termios
import threading
import time
import tempfile
import unittest
import shutil
import re
import shlex
from pathlib import Path
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

@unittest.skipUnless(os.environ.get('DEVKIT_LIVE_RECOVERY') == '1' and shutil.which('claude'), 'нужны DEVKIT_LIVE_RECOVERY=1 и установленный claude')
class LiveRecoveryTest(unittest.TestCase):

    def test_truncated_response_continues_in_same_session(self):
        self.run_client("success")

    def test_outage_stops_after_three_attempts(self):
        self.run_client("exhausted")

    def test_credit_error_does_not_continue(self):
        self.run_client("credit")

    def run_client(self, scenario):
        scratch = Path(tempfile.mkdtemp(prefix='dk1129-live-'))
        self.addCleanup(shutil.rmtree, scratch, True)
        home_dir = scratch / 'home'
        config_dir = scratch / 'cfg'
        work_dir = Path(
            tempfile.mkdtemp(
                prefix='dk1129-live-',
                dir=Path(__file__).resolve().parents[1] / '.devkit',
            ),
        )
        self.addCleanup(shutil.rmtree, work_dir, True)
        for p in (home_dir, config_dir, work_dir):
            p.mkdir(exist_ok=True)
        (config_dir / '.claude.json').write_text(json.dumps({'hasCompletedOnboarding': True, 'customApiKeyResponses': {'approved': ['stub-key'], 'rejected': []}, 'theme': 'dark', 'projects': {str(work_dir): {'hasTrustDialogAccepted': True}}}))
        (scratch / 'hook.py').write_text('import sys,pathlib\np=pathlib.Path(' + repr(str(scratch / 'failure.json')) + ')\np.write_text(sys.stdin.read())\n')
        notify_command = shlex.join([
            "python3", str(Path(__file__).resolve().parent / "notify.py"),
            "--hook", "claude-code",
        ])
        notify_handler = {"type": "command", "command": notify_command}
        settings = {
            "crossSessionInbound": "accept",
            "hooks": {
                "StopFailure": [{"hooks": [
                    {"type": "command", "command": shlex.join([
                        "python3", str(scratch / "hook.py"),
                    ])},
                    notify_handler,
                ]}],
                "UserPromptSubmit": [{"hooks": [notify_handler]}],
                "Stop": [{"hooks": [notify_handler]}],
            },
        }
        (config_dir / "settings.json").write_text(json.dumps(settings))
        state = {'recover': False, 'posts': 0}

        class API(BaseHTTPRequestHandler):

            def log_message(self, *a):
                pass

            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{}')

            def do_POST(self):
                d = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                if 'count_tokens' in self.path:
                    b = b'{"input_tokens":100}'
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(b)))
                    self.end_headers()
                    self.wfile.write(b)
                    return
                state['posts'] += 1
                if scenario == "credit":
                    body = json.dumps({"type": "error", "error": {
                        "type": "billing_error", "message": "402 Insufficient balance"}}).encode()
                    self.send_response(402)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    return
                text = 'RECOVERED_DK1129' if state['recover'] else 'INCOMPLETE_DK1129'
                msg = {
                    'id': 'msg_probe',
                    'type': 'message',
                    'role': 'assistant',
                    'model': 'claude-sonnet-4-6',
                    'content': [{'type': 'text', 'text': text}],
                    'stop_reason': 'end_turn',
                    'stop_sequence': None,
                    'usage': {'input_tokens': 100, 'output_tokens': 10},
                }
                if not d.get('stream'):
                    if not state['recover']:
                        self.connection.shutdown(socket.SHUT_RDWR)
                        self.connection.close()
                        return
                    b = json.dumps(msg).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(b)))
                    self.end_headers()
                    self.wfile.write(b)
                    return
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Connection', 'close')
                self.end_headers()
                events = [
                    (
                        'message_start',
                        {
                            'type': 'message_start',
                            'message': dict(msg, content=[], stop_reason=None),
                        },
                    ),
                    (
                        'content_block_start',
                        {
                            'type': 'content_block_start',
                            'index': 0,
                            'content_block': {'type': 'text', 'text': ''},
                        },
                    ),
                    (
                        'content_block_delta',
                        {
                            'type': 'content_block_delta',
                            'index': 0,
                            'delta': {'type': 'text_delta', 'text': text},
                        },
                    ),
                ]
                if state['recover']:
                    events += [('content_block_stop', {'type': 'content_block_stop', 'index': 0}), ('message_delta', {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn', 'stop_sequence': None}, 'usage': {'output_tokens': 10}}), ('message_stop', {'type': 'message_stop'})]
                for kind, value in events:
                    self.wfile.write(('event: ' + kind + '\ndata: ' + json.dumps(value) + '\n\n').encode())
                    self.wfile.flush()
                    time.sleep(0.05)
                self.close_connection = True
        backend = scratch / 'notify-backend'
        notifications = scratch / "notifications"
        backend.write_text("#!/bin/sh\nprintf '%s\n' \"$*\" >> " + shlex.quote(str(notifications)) + "\n")
        backend.chmod(0o755)
        server = ThreadingHTTPServer(('127.0.0.1', 0), API)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 35, 120, 0, 0))
        env = {
            'HOME': str(home_dir),
            'PATH': os.environ['PATH'],
            'TERM': 'xterm-256color',
            'CLAUDE_CONFIG_DIR': str(config_dir),
            'ANTHROPIC_BASE_URL': 'http://127.0.0.1:' + str(server.server_port),
            'ANTHROPIC_API_KEY': 'stub-key',
            'CLAUDE_CODE_MAX_RETRIES': '0',
            'CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK': '1',
            'ANTHROPIC_MAX_RETRIES': '0',
            'DEVKIT_NOTIFY_BACKEND': str(backend),
            'DEVKIT_NOTIFY_FOCUS': 'off',
            'DISABLE_AUTOUPDATER': '1',
            'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC': '1',
        }
        p = subprocess.Popen(
            [
                shutil.which('claude'),
                '--model',
                'claude-sonnet-4-6',
                'Respond only with a short marker',
            ],
            cwd=work_dir,
            env=env,
            stdin=slave,
            stdout=slave,
            stderr=slave,
            start_new_session=True,
        )
        os.close(slave)
        end = time.monotonic() + 60
        sent = False
        raw = b''
        imports_answered = False
        proved = False
        posts_at_failure = None
        try:
            while time.monotonic() < end and p.poll() is None:
                r, _, _ = select.select([master], [], [], 0.3)
                if r:
                    try:
                        b = os.read(master, 65536)
                    except OSError:
                        break
                    raw += b
                    (scratch / 'terminal.log').write_bytes(raw)
                    if b'\x1b[6n' in b:
                        os.write(master, b'\x1b[1;1R')
                    plain = re.sub(b'\\x1b\\[[0-?]*[ -/]*[@-~]', b'', raw)
                    if not imports_answered and b'AllowexternalCLAUDE.mdfileimports?' in re.sub(b'\\s+', b'', plain):
                        time.sleep(0.7)
                        os.write(master, b'\r')
                        imports_answered = True
                if (scratch / 'failure.json').exists() and (not sent):
                    state['recover'] = scenario == 'success'
                    posts_at_failure = state['posts']
                    sent = True
                journal_path = home_dir / '.devkit/notify.log'
                if (sent and scenario == 'success' and b'RECOVERED_DK1129' in raw
                        and journal_path.exists()
                        and 'повод turn_done' in journal_path.read_text()):
                    proved = True
                    break
                if sent and notifications.exists() and scenario == "exhausted":
                    proved = True
                    break
                if sent and notifications.exists() and scenario == "credit":
                    self.assertEqual(state["posts"], posts_at_failure)
                    proved = True
                    break
        finally:
            (scratch / 'terminal.log').write_bytes(raw)
            p.terminate()
            try:
                p.wait(timeout=4)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
            server.shutdown()
            server.server_close()
            os.close(master)
            self.assertTrue(proved, 'client did not resume: ' + raw[-5000:].decode(errors='replace'))
            failure = json.loads((scratch / 'failure.json').read_text())
            self.assertEqual(failure['hook_event_name'], 'StopFailure')
            if scenario != "credit":
                self.assertIn('StreamTruncated', failure['last_assistant_message'])
            journal = (home_dir / '.devkit/notify.log').read_text()
            if scenario == "success":
                self.assertIn('продолжение 1/3', journal)
                self.assertNotIn('не доставлено', journal)
                self.assertNotIn('повод turn_failed', journal)
                states = list((home_dir / '.devkit/turn-recover').glob('*.json'))
                recovered_state = json.loads(states[0].read_text())
                self.assertEqual(recovered_state['tries'], 0)
                self.assertIs(recovered_state['managed'], False)
            elif scenario == "exhausted":
                self.assertIn('продолжение 3/3', journal)
                self.assertIn('Продолжение не помогло 3', notifications.read_text())
                states = list((home_dir / '.devkit/turn-recover').glob('*.json'))
                exhausted_state = json.loads(states[0].read_text())
                self.assertEqual(exhausted_state['tries'], 3)
                self.assertIs(exhausted_state['managed'], True)
            else:
                self.assertNotIn('продолжение', journal)
                self.assertFalse((home_dir / '.devkit/turn-recover').exists())
            print("Live CLI recovery scenario:", scenario)
if __name__ == '__main__':
    unittest.main()
