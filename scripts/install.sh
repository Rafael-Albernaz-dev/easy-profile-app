#!/usr/bin/env bash
# Build/install only when explicitly invoked; never target production in tests.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
source "${SCRIPT_DIR}/release-common.sh"
check_release_test_mode
DRY_RUN=0
case "${1:-}" in --dry-run) DRY_RUN=1 ;; '') ;; *) echo 'usage: install.sh [--dry-run]' >&2; exit 2 ;; esac
[[ $# -le 1 ]]
BACKUP_FILE="${HOME}/.local/state/easyprofile/guards-backup.json"
verify_presets
KA_ACTIVE="$(systemd_state is-active ka1333-input-guard.service)"
KA_ENABLED="$(systemd_state is-enabled ka1333-input-guard.service)"
EE_ACTIVE="$(systemd_state is-active easyeffects-output-guard.service)"
EE_ENABLED="$(systemd_state is-enabled easyeffects-output-guard.service)"
validate_guard_states "$KA_ACTIVE" "$KA_ENABLED"
validate_guard_states "$EE_ACTIVE" "$EE_ENABLED"
APP_STATE="$(systemd_state is-active easyprofiled.service)"
case "$APP_STATE" in
    inactive|failed|unknown) ;;
    *) echo "Existing EasyProfile service is $APP_STATE; refusing to replace a running service." >&2; exit 1 ;;
esac
if [[ $DRY_RUN == 1 ]]; then
    echo "Dry-run: presets verified; guards: $KA_ACTIVE/$KA_ENABLED, $EE_ACTIVE/$EE_ENABLED. No writes or builds."
    exit 0
fi
[[ ! -e "$BACKUP_FILE" ]] || { echo "Existing rollback snapshot preserved: $BACKUP_FILE. Resolve it before a new cutover." >&2; exit 1; }
make -C "${REPO_ROOT}" build extension-pack
python3 "${SCRIPT_DIR}/release-state.py" save "$BACKUP_FILE" "$KA_ACTIVE" "$KA_ENABLED" "$EE_ACTIVE" "$EE_ENABLED"

mkdir -p "${HOME}/.local/bin" "${HOME}/.config/easyprofile" "${HOME}/.config/systemd/user"
install -m 0755 "${REPO_ROOT}/bin/easyprofilectl" "${HOME}/.local/bin/easyprofilectl"
if [[ ! -f "${HOME}/.config/easyprofile/config.json" ]]; then
    install -m 0644 "${REPO_ROOT}/config.example.json" "${HOME}/.config/easyprofile/config.json"
fi
install -m 0644 "${REPO_ROOT}/systemd/user/easyprofiled.service" "${HOME}/.config/systemd/user/easyprofiled.service"
EXT_SRC="${REPO_ROOT}/gnome-extension/easyprofile@gnome-shell"
EXT_DEST="${HOME}/.local/share/gnome-shell/extensions/easyprofile@gnome-shell"
mkdir -p "${EXT_DEST}/schemas"
cp "${EXT_SRC}/metadata.json" "${EXT_SRC}/extension.js" "${EXT_SRC}/dbus.js" "$EXT_DEST/"
cp "${EXT_SRC}/schemas/"*.xml "${EXT_DEST}/schemas/"
glib-compile-schemas "${EXT_DEST}/schemas"

on_cutover_error() {
    local failure=$?
    trap - ERR INT TERM
    echo 'Cutover failed; restoring saved guard state immediately.' >&2
    "${SCRIPT_DIR}/rollback.sh" || { echo 'ROLLBACK FAILED: inspect the saved snapshot and service errors.' >&2; exit 1; }
    exit "$failure"
}
# Every failure after the first guard mutation goes through rollback.
trap 'on_cutover_error' ERR
trap 'false' INT TERM
for unit in ka1333-input-guard.service easyeffects-output-guard.service; do
    systemctl --user stop "$unit"
    enabled="$(systemd_state is-enabled "$unit")"
    case "$enabled" in enabled|enabled-runtime) systemctl --user disable "$unit" ;; esac
done
mkdir -p "${HOME}/.local/share/dbus-1/services"
# Render an absolute fallback; D-Bus Exec does not expand systemd's %h.
python3 - "${REPO_ROOT}/systemd/user/org.easyprofile.Daemon.service" "${HOME}/.local/share/dbus-1/services/org.easyprofile.Daemon.service" <<'PY'
import pathlib, sys
source, target = map(pathlib.Path, sys.argv[1:])
home = str(pathlib.Path.home())
assert '\n' not in home and '"' not in home and '\\' not in home
target.write_text(source.read_text().replace('Exec=%h/.local/bin/easyprofilectl daemon --control',
    f'Exec="{home}/.local/bin/easyprofilectl" daemon --control --config "{home}/.config/easyprofile/config.json"'))
PY
systemctl --user daemon-reload
systemctl --user enable --now easyprofiled.service
healthy=0
health_deadline=$((SECONDS + 10))
for ((attempt=0; attempt<30; attempt++)); do
    if health_json="$(timeout 1s "${HOME}/.local/bin/easyprofilectl" status --json)"; then
        # Retry only transport/readiness. A received degraded/invalid reply rolls back immediately.
        printf '%s\n' "$health_json" | python3 "${SCRIPT_DIR}/release-state.py" health
        healthy=1
        break
    fi
    if ((SECONDS >= health_deadline)); then break; fi
    sleep 0.2
done
[[ $healthy == 1 ]]
gnome-extensions enable easyprofile@gnome-shell
verify_presets
trap - ERR INT TERM
echo 'Cutover complete: daemon protection confirmed; rollback snapshot preserved.'
