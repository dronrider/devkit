#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
kit_root=$(git -C "$script_dir" rev-parse --show-toplevel)
: "${DASHBOARD_ACCEPT_BIN:=$(command -v dashboard)}"
: "${TASKCTL_ACCEPT_BIN:=$(command -v taskctl)}"
: "${DASHBOARD_CHROME:?Укажите браузер стенда в DASHBOARD_CHROME}"
export DASHBOARD_ACCEPT_BIN TASKCTL_ACCEPT_BIN DASHBOARD_CHROME
export GOWORK=off
test -x "$DASHBOARD_ACCEPT_BIN"
test -x "$TASKCTL_ACCEPT_BIN"
test -x "$DASHBOARD_CHROME"
node -e 'require.resolve("playwright")'
"$TASKCTL_ACCEPT_BIN" --version
"$DASHBOARD_ACCEPT_BIN" --version
check_log=$(mktemp)
trap 'rm -f "$check_log"' EXIT
check_code=0
devkitctl test "$kit_root/tools/dashboard" -run '^(TestDraftMetadataThroughTaskctl|TestDraftProposalEditingAndRejectThroughTaskctl|TestDraftConfirmThroughRealTaskctl|TestDraftConfirmBrowserRealProcesses|TestProposalBrowserRealProcesses|TestScenarioBrowserEmbedded)$' -v >"$check_log" 2>&1 || check_code=$?
cat "$check_log"
test "$check_code" -eq 0 || exit "$check_code"
for case_name in TestDraftMetadataThroughTaskctl TestDraftProposalEditingAndRejectThroughTaskctl TestDraftConfirmThroughRealTaskctl TestDraftConfirmBrowserRealProcesses TestProposalBrowserRealProcesses TestScenarioBrowserEmbedded; do
    if ! grep -Fq -- "--- PASS: $case_name (" "$check_log"; then
        printf 'Не пройдена обязательная проверка %s\n' "$case_name" >&2
        exit 1
    fi
done
