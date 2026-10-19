#!/usr/bin/env bash
set -euo pipefail
: "${EASYPROFILE_TEST_MODE:?integration must run with test mode}"
[[ "${EASYPROFILE_TEST_MODE}" == 1 ]]
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Always create a new private bus, even when called from an existing test session.
exec dbus-run-session --config-file="${REPO_ROOT}/scripts/private-bus.conf" -- env EASYPROFILE_PRIVATE_BUS=1 EASYPROFILE_PARENT_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-none}" "${REPO_ROOT}/scripts/private-dbus-tests.sh"
