#!/usr/bin/env bash
# Local remediation gates; audio mutations are blocked and IPC uses a private bus.
set -euo pipefail
export EASYPROFILE_TEST_MODE=1
export GSETTINGS_BACKEND=memory

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

echo "=========================================================="
echo "🛡️  RUNNING EASYPROFILEAPP QUALITY GATES (SECURITY REMEDIATION)"
echo "=========================================================="

# -----------------------------------------------------------------------------
# GATE 1: Static Analysis, Formatting & Binary Compilation
# -----------------------------------------------------------------------------
echo ""
echo "[Gate 1/6] Checking Go formatting, go vet, staticcheck linter, and compilation..."
UNFORMATTED="$(gofmt -s -l cmd internal scripts)"
if [ -n "${UNFORMATTED}" ]; then
    echo "❌ Error: Unformatted Go files detected:"
    echo "${UNFORMATTED}"
    exit 1
fi

go vet ./...
STATICCHECK="${STATICCHECK:-/home/toru/go/bin/staticcheck}"
if [[ ! -x "${STATICCHECK}" ]]; then
    echo "staticcheck is required; set STATICCHECK to its installed executable."
    exit 1
fi
"${STATICCHECK}" ./...
mkdir -p "${REPO_ROOT}/bin"
go build -race -o "${REPO_ROOT}/bin/easyprofilectl" ./cmd/easyprofilectl
echo "✓ Go formatting, go vet, staticcheck, and compilation passed."

# -----------------------------------------------------------------------------
# GATE 2: Forbidden Patterns & Git Hygiene Guardrail
# -----------------------------------------------------------------------------
echo ""
echo "[Gate 2/6] Auditing forbidden mutation patterns and git hygiene..."

go run scripts/check-source-safety.go

# Git hygiene: verify private governance files are NOT tracked
TRACKED_PRIVATE="$(git ls-files | grep -E '(^|/)(AGENTS\.md|GEMINI\.md|agents\.md|gemini\.md|\.agents(/|$))' || true)"
if [ -n "${TRACKED_PRIVATE}" ]; then
    echo "❌ Error: Private governance files tracked in git:"
    echo "${TRACKED_PRIVATE}"
    exit 1
fi
for file in .gitignore "$(git rev-parse --git-path info/exclude)"; do
    for pattern in AGENTS.md agents.md GEMINI.md gemini.md .agents/; do
        grep -Fxq "$pattern" "$file"
    done
done
echo "✓ Zero forbidden shell patterns and git hygiene verified."

# -----------------------------------------------------------------------------
# GATE 3: Production Audio Guards & Preset Hashes Integrity
# -----------------------------------------------------------------------------
echo ""
echo "[Gate 3/6] Verifying production guards are active and preset hashes unchanged..."

# Check active status of systemd guards
for unit in ka1333-input-guard.service easyeffects-output-guard.service; do
    STATE="$(systemctl --user is-active "${unit}" 2>/dev/null || true)"
    if [ "${STATE}" != "active" ]; then
        echo "❌ Error: Production guard ${unit} is not active (state: ${STATE})."
        exit 1
    fi
done

# Check preset baseline hashes
BASELINE_FILE="${REPO_ROOT}/test/fixtures/preset-baseline.sha256"
if [ ! -f "${BASELINE_FILE}" ]; then
    echo "❌ Error: Preset baseline hash file missing: ${BASELINE_FILE}"
    exit 1
fi

while read -r expected_hash filepath; do
    actual_path="${filepath/#\~/$HOME}"
    if [ ! -f "${actual_path}" ]; then
        echo "❌ Error: Preset file missing: ${actual_path}"
        exit 1
    fi
    actual_hash="$(sha256sum "${actual_path}" | awk '{print $1}')"
    if [ "${actual_hash}" != "${expected_hash}" ]; then
        echo "❌ Error: Preset hash mismatch on ${actual_path}: expected ${expected_hash}, got ${actual_hash}"
        exit 1
    fi
done < "${BASELINE_FILE}"

# Validate GNOME Extension schema compilation & syntax
EXT_DIR="${REPO_ROOT}/gnome-extension/easyprofile@gnome-shell"
glib-compile-schemas --strict --dry-run "${EXT_DIR}/schemas"
for js in "${EXT_DIR}"/*.js; do node --check "${js}"; done
python3 - "${EXT_DIR}" <<'PYMETADATA'
import json,sys,xml.etree.ElementTree as ET
from pathlib import Path
root=Path(sys.argv[1]);metadata=json.loads((root/'metadata.json').read_text())
schema=ET.parse(next((root/'schemas').glob('*.xml'))).getroot().find('schema').attrib['id']
assert metadata.get('settings-schema')==schema, 'metadata must bind Extension.getSettings() to schema'
assert '46' in metadata['shell-version']
PYMETADATA
make extension-pack
CONTENTS="$(unzip -Z1 "${REPO_ROOT}/dist/easyprofile@gnome-shell.shell-extension.zip")"
for entry in metadata.json extension.js dbus.js schemas/org.gnome.shell.extensions.easyprofile.gschema.xml; do
    grep -Fxq "${entry}" <<< "${CONTENTS}"
done
echo "✓ Production guards active, preset hashes intact, and extension schema/syntax valid."

# -----------------------------------------------------------------------------
# GATE 4: Hermetic Unit & Anti-Regression Tests (-race)
# -----------------------------------------------------------------------------
echo ""
echo "[Gate 4/6] Running hermetic unit tests with race detector (EASYPROFILE_TEST_MODE=1)..."
go test -race -v ./...
python3 scripts/test-cutover.py
node scripts/test-extension.cjs
echo "✓ Hermetic unit tests passed with race detector."

# -----------------------------------------------------------------------------
# GATE 5: CLI Subcommand Execution (Read-Only Inspection)
# -----------------------------------------------------------------------------
echo ""
echo "[Gate 5/6] Private D-Bus integration (fakes), CLI and read-only inspection..."
./scripts/dbus-integration.sh
INSPECT_OUT="$("${REPO_ROOT}/bin/easyprofilectl" inspect --json --pw-dump-file "${REPO_ROOT}/test/fixtures/pw-dump-live-2026-10-18.json" --preset-dir '' --offline)"
if ! grep -q '"card_found": true' <<< "${INSPECT_OUT}"; then
    echo "❌ Error: easyprofilectl inspect did not find integrated card in fixture."
    exit 1
fi
echo "✓ CLI read-only inspection verified."

# -----------------------------------------------------------------------------
# GATE 6: Captured Dump Parse (<5ms), Fake CLI p95 (<15ms), RSS (<15MB), Idle CPU
# -----------------------------------------------------------------------------
echo ""
echo "[Gate 6/6] Parse <5ms; fake CLI p95 <15ms, RSS <15MB, idle CPU <1%/60s..."
BENCHMARK="$(go test -run '^$' -bench '^BenchmarkParseGraph$' -benchmem ./internal/inspect)"
printf '%s\n' "${BENCHMARK}"
awk '/^BenchmarkParseGraph/ { for(i=1;i<=NF;i++) if($i=="ns/op") { found=1; if($(i-1)>=5000000) exit 1 } } END { if(!found) exit 1 }' <<< "${BENCHMARK}"
./scripts/performance-check.sh
for unit in ka1333-input-guard.service easyeffects-output-guard.service; do
    [[ "$(systemctl --user is-active "${unit}")" == active ]]
done
python3 scripts/release-state.py hashes "$BASELINE_FILE"
echo "✓ Measured fake budgets passed; production guards and presets remain intact. Real-adapter budgets require human QA."

echo ""
echo "=========================================================="
echo "🎉 ALL 6 QUALITY GATES PASSED FOR LOCAL SECURITY REMEDIATION!"
echo "=========================================================="
