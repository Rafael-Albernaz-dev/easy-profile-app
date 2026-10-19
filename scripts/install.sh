#!/usr/bin/env bash
# scripts/install.sh — Cutover seguro para EasyProfileApp (Slice EP-006)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN=1
    echo "🔍 Mode: DRY-RUN (simulação sem alterar serviços ativos)"
fi

echo "=========================================================="
echo "📦 EasyProfileApp — Instalador & Cutover Seguro"
echo "=========================================================="

# 1. Preflight check: Build binary and check preset hashes
echo "[1/6] Executando pre-flight de compilação e integridade..."
make -C "${REPO_ROOT}" build extension-pack

BASELINE_FILE="${REPO_ROOT}/test/fixtures/preset-baseline.sha256"
while read -r expected_hash filepath; do
    actual_path="${filepath/#\~/$HOME}"
    if [[ ! -f "${actual_path}" ]]; then
        echo "❌ Error: Preset file missing: ${actual_path}"
        exit 1
    fi
    actual_hash="$(sha256sum "${actual_path}" | awk '{print $1}')"
    if [[ "${actual_hash}" != "${expected_hash}" ]]; then
        echo "❌ Error: Preset hash mismatch on ${actual_path}"
        exit 1
    fi
done < "${BASELINE_FILE}"
echo "✓ Presets intactos e artefatos compilados."

# 2. Capture prior state of legacy guards
echo "[2/6] Gravando snapshot do estado prévio dos guards de produção..."
BACKUP_DIR="${HOME}/.local/state/easyprofile"
BACKUP_FILE="${BACKUP_DIR}/guards-backup.json"
mkdir -p "${BACKUP_DIR}"

KA_ACTIVE="$(systemctl --user is-active ka1333-input-guard.service 2>/dev/null || echo "inactive")"
KA_ENABLED="$(systemctl --user is-enabled ka1333-input-guard.service 2>/dev/null || echo "disabled")"
EE_ACTIVE="$(systemctl --user is-active easyeffects-output-guard.service 2>/dev/null || echo "inactive")"
EE_ENABLED="$(systemctl --user is-enabled easyeffects-output-guard.service 2>/dev/null || echo "disabled")"

cat > "${BACKUP_FILE}" <<EOF
{
  "backup_timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "ka1333_input_guard": {
    "active": "${KA_ACTIVE}",
    "enabled": "${KA_ENABLED}"
  },
  "easyeffects_output_guard": {
    "active": "${EE_ACTIVE}",
    "enabled": "${EE_ENABLED}"
  }
}
EOF
echo "✓ Estado anterior salvo em ${BACKUP_FILE}:"
cat "${BACKUP_FILE}"

if [[ ${DRY_RUN} -eq 1 ]]; then
    echo "✓ [DRY-RUN] Simulação concluída com sucesso antes de tocar nos serviços."
    exit 0
fi

# 3. Install binary, configuration and systemd service
echo "[3/6] Instalando binário, configuração e unidade systemd..."
mkdir -p "${HOME}/.local/bin" "${HOME}/.config/easyprofile" "${HOME}/.config/systemd/user"
install -m 0755 "${REPO_ROOT}/bin/easyprofilectl" "${HOME}/.local/bin/easyprofilectl"

if [[ ! -f "${HOME}/.config/easyprofile/config.json" ]]; then
    install -m 0644 "${REPO_ROOT}/config.example.json" "${HOME}/.config/easyprofile/config.json"
    echo "✓ Configuração instalada em ~/.config/easyprofile/config.json"
else
    echo "✓ Configuração existente preservada em ~/.config/easyprofile/config.json"
fi

install -m 0644 "${REPO_ROOT}/systemd/user/easyprofiled.service" "${HOME}/.config/systemd/user/easyprofiled.service"
mkdir -p "${HOME}/.local/share/dbus-1/services"
install -m 0644 "${REPO_ROOT}/systemd/user/org.easyprofile.Daemon.service" "${HOME}/.local/share/dbus-1/services/org.easyprofile.Daemon.service"

# 4. Install GNOME Shell extension
echo "[4/6] Instalando extensão GNOME Shell 46..."
EXT_SRC="${REPO_ROOT}/gnome-extension/easyprofile@gnome-shell"
EXT_DEST="${HOME}/.local/share/gnome-shell/extensions/easyprofile@gnome-shell"
mkdir -p "${EXT_DEST}/schemas"
cp "${EXT_SRC}/metadata.json" "${EXT_DEST}/"
cp "${EXT_SRC}/extension.js" "${EXT_DEST}/"
cp "${EXT_SRC}/dbus.js" "${EXT_DEST}/"
cp "${EXT_SRC}/schemas/"*.xml "${EXT_DEST}/schemas/"
glib-compile-schemas "${EXT_DEST}/schemas"
gnome-extensions enable easyprofile@gnome-shell || true

# 5. Safe Cutover of legacy guards
echo "[5/6] Executando cutover suave dos guards antigos..."
if [[ "${KA_ACTIVE}" == "active" ]]; then
    systemctl --user stop ka1333-input-guard.service || true
fi
if [[ "${KA_ENABLED}" == "enabled" ]]; then
    systemctl --user disable ka1333-input-guard.service || true
fi

if [[ "${EE_ACTIVE}" == "active" ]]; then
    systemctl --user stop easyeffects-output-guard.service || true
fi
if [[ "${EE_ENABLED}" == "enabled" ]]; then
    systemctl --user disable easyeffects-output-guard.service || true
fi

# 6. Enable and start easyprofiled.service
echo "[6/6] Ativando e iniciando o serviço easyprofiled..."
systemctl --user daemon-reload
systemctl --user enable --now easyprofiled.service

# Verification health check
echo "⏳ Verificando prontidão do daemon..."
sleep 1.5
if "${HOME}/.local/bin/easyprofilectl" status; then
    echo "🎉 Cutover concluído com sucesso! EasyProfileApp está ativo."
else
    echo "⚠️ Daemon não respondeu prontamente. Executando rollback automático..."
    "${SCRIPT_DIR}/rollback.sh"
    exit 1
fi
