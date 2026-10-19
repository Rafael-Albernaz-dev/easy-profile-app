#!/usr/bin/env bash
set -euo pipefail
[[ "${EASYPROFILE_TEST_MODE:-}" == 1 ]]
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PERF_DIR="$(mktemp -d)"
trap 'rm -rf "$PERF_DIR"' EXIT
mkdir -m 700 "$PERF_DIR/runtime" "$PERF_DIR/state" "$PERF_DIR/config"
go build -o "$PERF_DIR/easyprofilectl" "${REPO_ROOT}/cmd/easyprofilectl"
dbus-run-session --config-file="${REPO_ROOT}/scripts/private-bus.conf" -- env \
    EASYPROFILE_PRIVATE_BUS=1 EASYPROFILE_PARENT_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-none}" \
    XDG_RUNTIME_DIR="$PERF_DIR/runtime" XDG_STATE_HOME="$PERF_DIR/state" XDG_CONFIG_HOME="$PERF_DIR/config" \
    GSETTINGS_BACKEND=memory GIO_USE_VFS=local \
    python3 "${REPO_ROOT}/scripts/measure-fake-daemon.py" "$PERF_DIR/easyprofilectl" "$PERF_DIR/performance.json"
