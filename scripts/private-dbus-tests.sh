#!/usr/bin/env bash
set -euo pipefail
[[ "${EASYPROFILE_TEST_MODE:-}" == 1 && "${EASYPROFILE_PRIVATE_BUS:-}" == 1 ]]
[[ -n "${EASYPROFILE_PARENT_BUS_ADDRESS:-}" && -n "${DBUS_SESSION_BUS_ADDRESS:-}" ]]
[[ "${DBUS_SESSION_BUS_ADDRESS}" != "${EASYPROFILE_PARENT_BUS_ADDRESS}" ]]
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"
TEST_DIR="$(mktemp -d)"
DAEMON_PID=""
cleanup() {
    if [[ -n "${DAEMON_PID}" ]]; then
        kill -TERM "${DAEMON_PID}" 2>/dev/null || true
        wait "${DAEMON_PID}" || true
    fi
    rm -rf "${TEST_DIR}"
}
trap cleanup EXIT
mkdir "${TEST_DIR}/deny"
cat > "${TEST_DIR}/deny/blocked" <<'BLOCK'
#!/bin/sh
printf '%s\n' "$0" >> "$EASYPROFILE_HOST_COMMAND_LOG"
exit 97
BLOCK
chmod +x "${TEST_DIR}/deny/blocked"
for tool in wpctl pw-cli pw-dump amixer easyeffects gsettings dconf pactl pw-metadata; do
    ln -s blocked "${TEST_DIR}/deny/${tool}"
done
export PATH="${TEST_DIR}/deny:${PATH}"
export EASYPROFILE_HOST_COMMAND_LOG="${TEST_DIR}/host-commands.log"
export GSETTINGS_BACKEND=memory
export GIO_USE_VFS=local
export XDG_STATE_HOME="${TEST_DIR}/state"
export XDG_CONFIG_HOME="${TEST_DIR}/config"
export XDG_RUNTIME_DIR="${TEST_DIR}/runtime"
mkdir -m 700 "$XDG_RUNTIME_DIR"
EASYPROFILE_DBUS_INTEGRATION=1 go test -race -v -count=1 ./internal/daemon -run TestDBusIntegration
CTL="${REPO_ROOT}/bin/easyprofilectl"
"${CTL}" daemon --fake > "${TEST_DIR}/daemon.log" 2>&1 &
DAEMON_PID=$!
for ((attempt=0; attempt<100; attempt++)); do
    if "${CTL}" status --json > "${TEST_DIR}/status.json" 2>/dev/null; then break; fi
    kill -0 "${DAEMON_PID}" 2>/dev/null || { cat "${TEST_DIR}/daemon.log"; exit 1; }
    sleep 0.03
done
if "${CTL}" daemon --fake > "${TEST_DIR}/second-daemon.log" 2>&1; then
    echo 'Second daemon unexpectedly acquired session lock.' >&2
    exit 1
fi
rg -q 'session lock' "${TEST_DIR}/second-daemon.log"
gjs -m scripts/test-gjs-proxy.js
RESULT="$("${CTL}" volume 50)"
rg -q '50%' <<< "${RESULT}"
RESULT="$("${CTL}" volume +2)"
rg -q '52%' <<< "${RESULT}"
RESULT="$("${CTL}" volume -2)"
rg -q '50%' <<< "${RESULT}"
RESULT="$("${CTL}" mic 90)"
rg -q '90%' <<< "${RESULT}"
RESULT="$("${CTL}" mic +2)"
rg -q '92%' <<< "${RESULT}"
RESULT="$("${CTL}" mic -2)"
rg -q '90%' <<< "${RESULT}"
"${CTL}" mute out
"${CTL}" mute mic
"${CTL}" panic
"${CTL}" panic
"${CTL}" listen on
RESULT="$("${CTL}" status --json)"
rg -q '"mic_monitoring":true' <<< "${RESULT}"
"${CTL}" listen off
RESULT="$("${CTL}" status --json)"
rg -q '"mic_monitoring":false' <<< "${RESULT}"
"${CTL}" route sp166
RESULT="$("${CTL}" preset list)"
rg -q 'SP166 - Uso diario aprovado' <<< "${RESULT}"
"${CTL}" route toggle
"${CTL}" preset 'AKG - Rock'
"${CTL}" reset
"${CTL}" status --json > "${TEST_DIR}/status.json"
python3 - "${TEST_DIR}/status.json" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
assert s['active_destination']['id']=='akg'
assert s['output_volume']==95 and s['mic_level']==100
assert s['mic_virtual_muted'] and s['mic_physical_muted'] and s['mic_privacy_muted']
assert s['active_preset']=='AKG K52 - Uso diario aprovado'
assert not s['last_error'], s
PY
if "${CTL}" route invalid >/dev/null 2>&1; then exit 1; fi
if "${CTL}" preset 'SP166 - Uso diario aprovado' >/dev/null 2>&1; then exit 1; fi
if "${CTL}" volume invalid >/dev/null 2>&1; then exit 1; fi
[[ ! -e "${EASYPROFILE_HOST_COMMAND_LOG}" ]] || { cat "${EASYPROFILE_HOST_COMMAND_LOG}"; exit 1; }
echo 'Private D-Bus + CLI/daemon integration passed; zero host audio commands.'
