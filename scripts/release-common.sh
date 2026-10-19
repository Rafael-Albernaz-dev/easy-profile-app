#!/usr/bin/env bash
# Shared validation and explicit state restoration for release scripts.
systemd_state() {
    local value
    value="$(systemctl --user "$1" "$2" 2>/dev/null || true)"
    value="${value//$'\r'/}"
    [[ "$value" != *$'\n'* ]] || { echo 'Ambiguous systemd response' >&2; return 1; }
    printf '%s\n' "${value:-unknown}"
}
verify_presets() {
    python3 "${SCRIPT_DIR}/release-state.py" hashes "${REPO_ROOT}/test/fixtures/preset-baseline.sha256"
}
# Test mode cannot invoke host service tools even if the wrong script is run.
check_release_test_mode() {
    [[ -z "${EASYPROFILE_TEST_MODE+x}" ]] && return 0
    python3 - "${EASYPROFILE_CUTOVER_FAKE_ROOT:-}" <<'PY'
import os, pathlib, shutil, sys
root = pathlib.Path(sys.argv[1]).resolve()
assert os.environ.get('EASYPROFILE_TEST_MODE') == '1'
assert str(root).startswith('/tmp/') and pathlib.Path.home().resolve().is_relative_to(root)
for tool in ('systemctl', 'make', 'gnome-extensions', 'glib-compile-schemas'):
    assert pathlib.Path(shutil.which(tool)).resolve().is_relative_to(root), tool + ' must be fake'
PY
}
validate_guard_states() {
    local active="$1" enabled="$2"
    [[ "$active" == active || "$active" == inactive ]] || { echo "Unstable/unknown guard state: $active" >&2; return 1; }
    case "$enabled" in
        enabled|enabled-runtime|disabled|masked|masked-runtime|static|indirect) ;;
        *) echo "Unsupported guard enablement: $enabled" >&2; return 1 ;;
    esac
}
restore_guard() {
    local unit="$1" active="$2" enabled="$3" failed=0
    # Explicit returns matter: callers aggregate failures instead of using errexit.
    case "$enabled" in
        enabled) systemctl --user unmask "$unit" || failed=1; systemctl --user enable "$unit" || failed=1 ;;
        enabled-runtime) systemctl --user unmask "$unit" || failed=1; systemctl --user disable "$unit" || failed=1; systemctl --user enable --runtime "$unit" || failed=1 ;;
        disabled|masked|masked-runtime) systemctl --user unmask "$unit" || failed=1; systemctl --user disable "$unit" || failed=1 ;;
        static|indirect) ;;
    esac
    if [[ "$active" == active ]]; then systemctl --user start "$unit" || failed=1; else systemctl --user stop "$unit" || failed=1; fi
    case "$enabled" in
        masked) systemctl --user mask "$unit" || failed=1 ;;
        masked-runtime) systemctl --user mask --runtime "$unit" || failed=1 ;;
    esac
    [[ "$(systemd_state is-active "$unit")" == "$active" ]] || failed=1
    [[ "$(systemd_state is-enabled "$unit")" == "$enabled" ]] || failed=1
    return "$failed"
}
