#!/bin/sh
set -eu

source_root=$(pwd)
source_script="$source_root/docs/tasks/archive/2026/DK-1446-check.sh"
if ! test -f "$source_script"; then
    source_script="$source_root/docs/tasks/DK-1446-check.sh"
fi
stand=$(mktemp -d)
trap 'rm -rf "$stand"' EXIT HUP INT TERM
mkdir -p "$stand/project/docs/tasks/archive/2026" "$stand/bin"
cp "$source_script" "$stand/project/docs/tasks/archive/2026/DK-1446-check.sh"
cat > "$stand/bin/node" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$stand/bin/devkitctl" <<'SH'
#!/bin/sh
set -eu
test "$1" = test
if test "$2" != "$EXPECTED_TARGET"; then
    printf 'target mismatch: expected %s, got %s\n' "$EXPECTED_TARGET" "$2" >&2
    exit 1
fi
printf 'archive target verified: %s\n' "$2"
SH
chmod +x "$stand/bin/node" "$stand/bin/devkitctl"
PATH="$stand/bin:$PATH" DASHBOARD_ACCEPT_BIN="$stand/bin/node" \
TASKCTL_ACCEPT_BIN="$stand/bin/node" DASHBOARD_CHROME="$stand/bin/node" \
EXPECTED_TARGET="$stand/project/tools/dashboard" \
sh "$stand/project/docs/tasks/archive/2026/DK-1446-check.sh"
