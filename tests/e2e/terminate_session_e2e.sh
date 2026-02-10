#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_PATH="${ROOT_DIR}/tmp/do-ai-terminate-e2e"
PORT="${DO_AI_E2E_PORT:-19789}"
TOKEN="doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff"
BASE_URL="http://127.0.0.1:${PORT}"
SESSION_NAME="terminate-e2e-session"
WORK_DIR="$(mktemp -d)"

cleanup() {
  set +e
  if [[ -n "${SESSION_PID:-}" ]]; then
    kill "${SESSION_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${RELAY_PID:-}" ]]; then
    kill "${RELAY_PID}" >/dev/null 2>&1 || true
  fi
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "[TERMINATE-E2E] build do-ai"
cd "${ROOT_DIR}"
go build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./src

echo "[TERMINATE-E2E] start relay @ ${BASE_URL}"
"${BIN_PATH}" relay --listen "127.0.0.1:${PORT}" --token "${TOKEN}" \
  >"${WORK_DIR}/relay.log" 2>&1 &
RELAY_PID=$!

for _ in {1..30}; do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -fsS "${BASE_URL}/healthz" >/dev/null

echo "[TERMINATE-E2E] start do-ai session (cat)"
tail -f /dev/null | script -q -c "DO_AI_RELAY_URL=${BASE_URL} DO_AI_RELAY_TOKEN=${TOKEN} DO_AI_RELAY_INTERVAL=1s DO_AI_RELAY_PULL_INTERVAL=1s DO_AI_SESSION_PREFIX=do DO_AI_SESSION_NAME=${SESSION_NAME} DO_AI_IDLE=600s DO_AI_DEBUG=1 ${BIN_PATH} cat" /dev/null \
  >"${WORK_DIR}/session.log" 2>&1 &
SESSION_PID=$!

SESSION_ID=""
for _ in {1..40}; do
  SESSION_ID="$(curl -fsS "${BASE_URL}/api/v1/sessions" -H "X-Relay-Token: ${TOKEN}" | jq -r ".sessions[] | select(.session_name==\"${SESSION_NAME}\") | .session_id" | head -n1)"
  if [[ -n "${SESSION_ID}" ]]; then
    break
  fi
  sleep 0.3
done

if [[ -z "${SESSION_ID}" ]]; then
  echo "[TERMINATE-E2E] ERROR: session not found"
  curl -fsS "${BASE_URL}/api/v1/sessions" -H "X-Relay-Token: ${TOKEN}" || true
  cat "${WORK_DIR}/session.log" || true
  exit 1
fi

echo "[TERMINATE-E2E] session_id=${SESSION_ID}"

curl -fsS -X POST "${BASE_URL}/api/v1/control/send" \
  -H "X-Relay-Token: ${TOKEN}" \
  -H 'Content-Type: application/json' \
  --data "{\"session_id\":\"${SESSION_ID}\",\"input\":\"\",\"submit\":false,\"action\":\"terminate\",\"source\":\"terminate-e2e\"}" \
  >/dev/null

FOUND_STOPPING="0"
for _ in {1..30}; do
  SESSIONS_JSON="$(curl -fsS "${BASE_URL}/api/v1/sessions?all=1" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
  if jq -e --arg sid "${SESSION_ID}" '.sessions // [] | any(.session_id == $sid and (.state == "stopping" or .state == "exited"))' <<<"${SESSIONS_JSON}" >/dev/null; then
    FOUND_STOPPING="1"
    break
  fi
  sleep 0.3
done

if [[ "${FOUND_STOPPING}" != "1" ]]; then
  echo "[TERMINATE-E2E] ERROR: session state not stopping/exited"
  echo "${SESSIONS_JSON:-{}}"
  cat "${WORK_DIR}/session.log" || true
  exit 1
fi

echo "[TERMINATE-E2E] PASS"
