#!/usr/bin/env bash
set -euo pipefail
export EASYPROFILE_TEST_MODE=1
exec "$(dirname "${BASH_SOURCE[0]}")/dbus-integration.sh" "$@"
