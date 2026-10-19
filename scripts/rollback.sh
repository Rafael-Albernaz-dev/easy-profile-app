#!/usr/bin/env bash
# Restore precisely the recorded state; never invent an enable/start fallback.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
source "${SCRIPT_DIR}/release-common.sh"
check_release_test_mode
BACKUP_FILE="${HOME}/.local/state/easyprofile/guards-backup.json"
case "${1:-}" in --dry-run) DRY_RUN=1 ;; '') DRY_RUN=0 ;; *) echo 'usage: rollback.sh [--dry-run]' >&2; exit 2 ;; esac
[[ $# -le 1 ]]
# Validate before touching services; a missing/corrupt backup authorizes no changes.
SAVED="$(python3 "${SCRIPT_DIR}/release-state.py" read "$BACKUP_FILE")"
if [[ $DRY_RUN == 1 ]]; then
    verify_presets
    printf 'Dry-run: validated rollback snapshot; no writes.\n%s\n' "$SAVED"
    exit 0
fi
# Remove activation first, closing the race with another D-Bus client.
failed=0
rm -f "${HOME}/.local/share/dbus-1/services/org.easyprofile.Daemon.service" || failed=1
systemctl --user stop easyprofiled.service || failed=1
systemctl --user disable easyprofiled.service || failed=1
[[ "$(systemd_state is-active easyprofiled.service)" == inactive ]] || failed=1
gnome-extensions disable easyprofile@gnome-shell || failed=1
while IFS=$'\t' read -r unit active enabled; do
    restore_guard "$unit" "$active" "$enabled" || failed=1
done <<< "$SAVED"
verify_presets || failed=1
[[ $failed == 0 ]] || { echo 'Rollback incomplete; snapshot retained for recovery.' >&2; exit 1; }
echo 'Rollback verified: exact prior guard states, no D-Bus activation file, preset hashes intact.'
