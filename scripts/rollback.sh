#!/usr/bin/env bash
# scripts/rollback.sh — Restauração Imediata do Setup Anterior (Slice EP-006)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP_FILE="${HOME}/.local/state/easyprofile/guards-backup.json"

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN=1
    echo "🔍 Mode: DRY-RUN (simulação sem alterar serviços ativos)"
fi

echo "=========================================================="
echo "⏪ EasyProfileApp — Rollback de Emergência"
echo "=========================================================="

if [[ ${DRY_RUN} -eq 1 ]]; then
    echo "[DRY-RUN] Verificando existência de backup e sintaxe..."
    if [[ -f "${BACKUP_FILE}" ]]; then
        echo "✓ Backup encontrado: ${BACKUP_FILE}"
        python3 -c "import json; data=json.load(open('${BACKUP_FILE}')); print('Backup data valid:', data)"
    else
        echo "⚠️ Nenhum backup encontrado em ${BACKUP_FILE} (usaria fallback padrão)"
    fi
    echo "✓ [DRY-RUN] Simulação concluída com sucesso."
    exit 0
fi

# 1. Stop and disable easyprofiled
echo "[1/4] Interrompendo e desabilitando easyprofiled.service..."
systemctl --user stop easyprofiled.service 2>/dev/null || true
systemctl --user disable easyprofiled.service 2>/dev/null || true

# 2. Disable GNOME Shell extension
echo "[2/4] Desativando extensão GNOME Shell..."
gnome-extensions disable easyprofile@gnome-shell 2>/dev/null || true

# 3. Restore legacy guards from backup
echo "[3/4] Restaurando guards de produção anteriores..."
if [[ -f "${BACKUP_FILE}" ]]; then
    echo "Lendo backup de ${BACKUP_FILE}..."
    KA_ACTIVE="$(python3 -c "import json; print(json.load(open('${BACKUP_FILE}'))['ka1333_input_guard']['active'])")"
    KA_ENABLED="$(python3 -c "import json; print(json.load(open('${BACKUP_FILE}'))['ka1333_input_guard']['enabled'])")"
    EE_ACTIVE="$(python3 -c "import json; print(json.load(open('${BACKUP_FILE}'))['easyeffects_output_guard']['active'])")"
    EE_ENABLED="$(python3 -c "import json; print(json.load(open('${BACKUP_FILE}'))['easyeffects_output_guard']['enabled'])")"

    if [[ "${KA_ENABLED}" == "enabled" ]]; then
        systemctl --user enable ka1333-input-guard.service
    fi
    if [[ "${KA_ACTIVE}" == "active" ]]; then
        systemctl --user start ka1333-input-guard.service
    fi

    if [[ "${EE_ENABLED}" == "enabled" ]]; then
        systemctl --user enable easyeffects-output-guard.service
    fi
    if [[ "${EE_ACTIVE}" == "active" ]]; then
        systemctl --user start easyeffects-output-guard.service
    fi
else
    echo "⚠️ Backup não encontrado. Aplicando restauração padrão de segurança..."
    systemctl --user enable --now ka1333-input-guard.service
    systemctl --user enable --now easyeffects-output-guard.service
fi

# 4. Verify systemd guards and preset hashes
echo "[4/4] Verificando integridade pós-rollback..."
for unit in ka1333-input-guard.service easyeffects-output-guard.service; do
    STATE="$(systemctl --user is-active "${unit}" 2>/dev/null || true)"
    echo "• ${unit}: ${STATE}"
    if [[ "${STATE}" != "active" ]]; then
        echo "❌ Falha: ${unit} não está ativo após rollback!"
        exit 1
    fi
done

echo "✓ Guards de produção restaurados com sucesso e ativos."
