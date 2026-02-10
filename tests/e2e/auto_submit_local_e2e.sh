#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_PATH="${ROOT_DIR}/tmp/do-ai-auto-submit-e2e"
WORK_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "[AUTO-SUBMIT-E2E] build do-ai"
cd "${ROOT_DIR}"
go build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./src

echo "[AUTO-SUBMIT-E2E] run do-ai auto-kick + read gate"
if ! timeout 25s script -q -c "DO_AI_IDLE=1s DO_AI_DEBUG=1 ${BIN_PATH} sh -lc 'read -r line; echo AUTO_SUBMIT_OK; exit 0'" /dev/null >"${WORK_DIR}/run.log" 2>&1; then
  echo "[AUTO-SUBMIT-E2E] ERROR: do-ai command timed out or failed"
  cat "${WORK_DIR}/run.log" || true
  exit 1
fi

if ! rg -q "AUTO_SUBMIT_OK" "${WORK_DIR}/run.log"; then
  echo "[AUTO-SUBMIT-E2E] ERROR: AUTO_SUBMIT_OK not found"
  cat "${WORK_DIR}/run.log" || true
  exit 1
fi

echo "[AUTO-SUBMIT-E2E] PASS"

