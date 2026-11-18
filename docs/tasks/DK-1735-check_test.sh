#!/bin/sh
set -eu
python3 - "$0" "${1:?Укажите существующий каталог вне системного temp}" <<'PY'
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

check = Path(sys.argv[1]).resolve().with_name('DK-1735-check.sh')
kit = check.parents[2]
parent = Path(sys.argv[2]).resolve()
assert parent.is_dir(), parent
assert not str(parent).startswith(('/tmp', '/private/tmp', '/var/folders/', '/private/var/folders/')), parent
probe = Path(tempfile.mkdtemp(prefix='dk1735-fixture-test-', dir=parent))
try:
    probe_home = probe / 'home'
    (probe_home / '.devkit').mkdir(parents=True)
    (probe_home / '.codex').mkdir()
    (probe_home / '.devkit/harness.local').write_text('enabled = ["codex"]\n\n[codex]\n')
    projects = probe / 'projects'
    projects.mkdir()
    bin_dir = probe / 'bin'
    bin_dir.mkdir()
    scripts = {
        'devkitctl': '#!/bin/sh\ncase "$*" in "test "*" -run ^TestAppChatPolicyInstalledArtifact$ -count=1 -v") exit 0;; *) echo "unexpected heavy command" >&2; exit 2;; esac\n',
        'dashboard': '#!/bin/sh\necho "fixture must not launch dashboard" >&2\nexit 2\n',
        'codex': '#!/bin/sh\necho "fixture must not launch Codex" >&2\nexit 2\n',
    }
    for name, body in scripts.items():
        path = bin_dir / name
        path.write_text(body)
        path.chmod(0o700)
    env = dict(os.environ, HOME=str(probe_home), DEVKIT_HOME=str(kit),
               PATH=str(bin_dir) + os.pathsep + os.environ['PATH'],
               DEVKIT_NOTIFY_OFF='1', DEVKIT_CHAT_SESSION_ID='',
               CLAUDE_CODE_SESSION_ID='', CODEX_THREAD_ID='', DEVKIT_TMUX='',
               DEVKIT_TASK='', DEVKIT_TASK_RUN_ID='')
    result = subprocess.run(['sh', str(check), str(projects)], input='\n',
                            env=env, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, timeout=60)
    assert result.returncode == 0, result.stdout
    marker = 'Синтетический проект: '
    generated = [line[len(marker):] for line in result.stdout.splitlines() if line.startswith(marker)]
    assert len(generated) == 1, result.stdout
    project = Path(generated[0]).resolve()
    assert project.parent.parent == projects, project
    result = subprocess.run(['taskctl', '-C', str(project), 'draft', '--prio', 'low',
                             'DK1735 fixture registration probe'], env=env,
                            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            timeout=30)
    assert result.returncode == 0, result.stdout
    assert (project / 'docs/tasks/drafts/ZZ-001.md').is_file(), result.stdout
    result = subprocess.run(['taskctl', '-C', str(project), 'draft', 'list'],
                            env=env, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, timeout=30)
    assert result.returncode == 0 and 'ZZ-001' in result.stdout, result.stdout
    print('PASS fixture: полный check.sh, отдельный HOME, настоящие git/taskctl, создан и прочитан ZZ-001')
    print('Подменён только тяжёлый artifact-шаг; модель/песочница/одобрение здесь не проверяются')
finally:
    assert probe.parent == parent and probe.name.startswith('dk1735-fixture-test-')
    shutil.rmtree(probe)
PY
