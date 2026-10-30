#!/usr/bin/env python3
"""Проверка установленного guard и восстановления без платного клиента."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--devkit', required=True)
    args = ap.parse_args()
    devkit = Path(args.devkit).resolve()
    taskctl = shutil.which('taskctl')
    if not taskctl:
        raise SystemExit('taskctl отсутствует в PATH')
    print('taskctl:', taskctl, flush=True)
    subprocess.run([taskctl, '--version'], check=True, timeout=10)
    with tempfile.TemporaryDirectory(prefix='dk1512-check-') as tmp:
        root = Path(tmp)
        os.environ['HOME'] = tmp
        os.environ['DEVKIT_NOTIFY_OFF'] = '1'
        (root/'docs/tasks').mkdir(parents=True)
        (root/'docs/TASKS.md').write_text('# Задачи (префикс DK)\n')
        (root/'docs/tasks/DK-1.md').write_text('# DK-1: открытый стенд\n')
        subprocess.run([taskctl, '-C', tmp, 'guard', 'DK-1'], check=True, timeout=10)
        path = devkit/'kit/skills/board-task/task-run.py'
        spec = importlib.util.spec_from_file_location('task_run_check', path)
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        old = 'cf892058-d1fb-470f-8e41-4140cc0953f1'
        client = root/'client.py'
        client.write_text('import sys\nsys.exit(37 if sys.argv[1] == "'+old+'" else 0)\n')
        opts = mod.parse_args(['DK-1', '-C', tmp, '--', sys.executable, str(client), old])
        pipe = mod.Pipeline(opts)
        pipe.sid = old
        pipe.client_session = old
        pipe.session_template = json.dumps(['{session}'])
        pipe.raise_head('первый запуск')
        pipe.head.wait(timeout=10)
        try:
            pipe.check_started()
        except SystemExit as exc:
            assert exc.code == 37, exc.code
        else:
            raise AssertionError('ранний выход не распознан')
        assert '37' in Path(pipe.startup_log()).read_text()
        pipe.renew_session()
        assert pipe.client[-1] != old
        tail = Path(pipe.rotate_tail())
        tail.write_text('проверить текущую карточку и продолжить\n')
        assert str(tail) in pipe.initial_order()
        pipe.raise_head(pipe.initial_order())
        assert pipe.head.wait(timeout=10) == 0
        assert tail.exists()
        print('PASS: guard установленной утилиты, код отказа, новый ID, сохранённый хвост')


if __name__ == '__main__':
    main()
