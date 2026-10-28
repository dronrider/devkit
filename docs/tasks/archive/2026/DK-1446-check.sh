#!/bin/sh
set -eu

kit_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../../.." && pwd)
: "${DASHBOARD_ACCEPT_BIN:=$(command -v dashboard)}"
: "${TASKCTL_ACCEPT_BIN:=$(command -v taskctl)}"
: "${DASHBOARD_CHROME:?Укажите браузер стенда в DASHBOARD_CHROME}"
export DASHBOARD_ACCEPT_BIN TASKCTL_ACCEPT_BIN DASHBOARD_CHROME
test -x "$DASHBOARD_ACCEPT_BIN"
test -x "$TASKCTL_ACCEPT_BIN"
node -e 'require.resolve("playwright")'
exec devkitctl test "$kit_root/tools/dashboard" -run '^TestScenario(BrowserEmbedded|Panel|Say)' -v
