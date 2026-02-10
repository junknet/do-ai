#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_PATH="${ROOT_DIR}/tmp/do-ai-online-e2e"
BASE_URL="${DO_AI_ONLINE_RELAY_URL:-http://47.110.255.240:18787}"
TOKEN="${DO_AI_RELAY_TOKEN:-doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff}"
SESSION_NAME="online-e2e-$(date +%s)"
WORK_DIR="$(mktemp -d)"
MARKER="ONLINE_E2E_LINE_$(date +%s)"
CURL_OPTS=(--connect-timeout 5 --max-time 12 --retry 2 --retry-delay 1 --retry-connrefused)

cleanup() {
  set +e
  if [[ -n "${SESSION_PID:-}" ]]; then
    kill "${SESSION_PID}" >/dev/null 2>&1 || true
  fi
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "[ONLINE-E2E] check relay health @ ${BASE_URL}"
curl -fsS "${CURL_OPTS[@]}" "${BASE_URL}/healthz" >/dev/null

echo "[ONLINE-E2E] build do-ai"
cd "${ROOT_DIR}"
go build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./src

echo "[ONLINE-E2E] start do-ai session -> remote relay"
tail -f /dev/null | script -q -c "DO_AI_RELAY_URL=${BASE_URL} DO_AI_RELAY_TOKEN=${TOKEN} DO_AI_RELAY_INTERVAL=1s DO_AI_RELAY_PULL_INTERVAL=1s DO_AI_SESSION_PREFIX=do DO_AI_SESSION_NAME=${SESSION_NAME} DO_AI_IDLE=600s DO_AI_DEBUG=1 ${BIN_PATH} cat" /dev/null \
  >"${WORK_DIR}/session.log" 2>&1 &
SESSION_PID=$!

SESSION_ID=""
for _ in {1..50}; do
  SESSIONS_JSON="$(curl -fsS "${CURL_OPTS[@]}" "${BASE_URL}/api/v1/sessions" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
  SESSION_ID="$(jq -r ".sessions[]? | select(.session_name==\"${SESSION_NAME}\") | .session_id" <<<"${SESSIONS_JSON}" | head -n1)"
  if [[ -n "${SESSION_ID}" ]]; then
    break
  fi
  sleep 0.6
done
if [[ -z "${SESSION_ID}" ]]; then
  echo "[ONLINE-E2E] ERROR: session not found"
  echo "${SESSIONS_JSON:-{}}"
  cat "${WORK_DIR}/session.log" || true
  exit 1
fi

echo "[ONLINE-E2E] session_id=${SESSION_ID}"
echo "[ONLINE-E2E] send command marker=${MARKER}"
curl -fsS "${CURL_OPTS[@]}" -X POST "${BASE_URL}/api/v1/control/send" \
  -H "X-Relay-Token: ${TOKEN}" \
  -H 'Content-Type: application/json' \
  --data "{\"session_id\":\"${SESSION_ID}\",\"input\":\"${MARKER}\",\"submit\":true,\"source\":\"online-e2e\"}" \
  >/dev/null

FOUND="0"
for _ in {1..50}; do
  OUTPUT_JSON="$(curl -fsS "${CURL_OPTS[@]}" "${BASE_URL}/api/v1/output/list?session_id=${SESSION_ID}&tail=1&limit=80" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
  if jq -e --arg marker "${MARKER}" '(.events // []) | map(.text) | any(. == $marker)' <<<"${OUTPUT_JSON}" >/dev/null; then
    FOUND="1"
    break
  fi
  sleep 0.6
done

if [[ "${FOUND}" != "1" ]]; then
  echo "[ONLINE-E2E] ERROR: marker not found in output"
  echo "${OUTPUT_JSON:-{}}"
  cat "${WORK_DIR}/session.log" || true
  exit 1
fi

echo "[ONLINE-E2E] PASS"
