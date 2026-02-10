#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_PATH="${ROOT_DIR}/tmp/do-ai-gemini-e2e"
PORT="${DO_AI_E2E_PORT:-19791}"
TOKEN="doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff"
BASE_URL="http://127.0.0.1:${PORT}"
SESSION_NAME="gemini-real-e2e-$(date +%s)"
WORK_DIR="$(mktemp -d)"
READY_TOKEN="TMUX_E2E_READY"
SECOND_TOKEN="TMUX_E2E_SECOND"

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

wait_for_session_id() {
  local sid=""
  for _ in {1..80}; do
    sid="$(curl -fsS "${BASE_URL}/api/v1/sessions?all=1" -H "X-Relay-Token: ${TOKEN}" | jq -r ".sessions[]? | select(.session_name==\"${SESSION_NAME}\") | .session_id" | head -n1)"
    if [[ -n "${sid}" ]]; then
      echo "${sid}"
      return 0
    fi
    sleep 0.4
  done
  return 1
}

wait_for_prompt_ready() {
  local sid="$1"
  local screen_json='{}'
  for _ in {1..150}; do
    screen_json="$(curl -fsS "${BASE_URL}/api/v1/output/screen?session_id=${sid}&limit=260" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
    echo "${screen_json}" > "${WORK_DIR}/screen_prompt.json"
    if jq -e '(.lines // []) | join("\n") | test("Type your message|/help|Tips for getting started")' <<<"${screen_json}" >/dev/null; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

send_prompt() {
  local sid="$1"
  local prompt="$2"
  local source="$3"
  curl -fsS -X POST "${BASE_URL}/api/v1/control/send" \
    -H "X-Relay-Token: ${TOKEN}" \
    -H 'Content-Type: application/json' \
    --data "{\"session_id\":\"${sid}\",\"input\":\"${prompt}\",\"submit\":true,\"source\":\"${source}\"}" \
    >/dev/null
}

wait_for_token() {
  local sid="$1"
  local token="$2"
  local screen_path="$3"
  local output_path="$4"
  local screen_json='{}'
  local output_json='{}'
  for _ in {1..140}; do
    screen_json="$(curl -fsS "${BASE_URL}/api/v1/output/screen?session_id=${sid}&limit=260" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
    output_json="$(curl -fsS "${BASE_URL}/api/v1/output/list?session_id=${sid}&tail=1&limit=240" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
    echo "${screen_json}" > "${screen_path}"
    echo "${output_json}" > "${output_path}"
    if jq -e --arg token "${token}" '(.lines // []) | join("\n") | contains($token)' <<<"${screen_json}" >/dev/null; then
      return 0
    fi
    if jq -e --arg token "${token}" '(.events // []) | map(.text) | join("\n") | contains($token)' <<<"${output_json}" >/dev/null; then
      return 0
    fi
    sleep 0.6
  done
  return 1
}

echo "[GEMINI-E2E] build do-ai"
cd "${ROOT_DIR}"
go build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./src

echo "[GEMINI-E2E] start relay @ ${BASE_URL}"
"${BIN_PATH}" relay --listen "127.0.0.1:${PORT}" --token "${TOKEN}" \
  >"${WORK_DIR}/relay.log" 2>&1 &
RELAY_PID=$!

for _ in {1..40}; do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -fsS "${BASE_URL}/healthz" >/dev/null

echo "[GEMINI-E2E] start do-ai + gemini interactive session"
tail -f /dev/null | script -q -c "DO_AI_RELAY_URL=${BASE_URL} DO_AI_RELAY_TOKEN=${TOKEN} DO_AI_RELAY_INTERVAL=1s DO_AI_RELAY_PULL_INTERVAL=1s DO_AI_SESSION_PREFIX=do DO_AI_SESSION_NAME=${SESSION_NAME} DO_AI_IDLE=600s DO_AI_DEBUG=1 ${BIN_PATH} 600s gemini -i \"请仅回复 ${READY_TOKEN}，然后等待我的下一条输入\"" /dev/null \
  >"${WORK_DIR}/session.log" 2>&1 &
SESSION_PID=$!

SESSION_ID="$(wait_for_session_id || true)"
if [[ -z "${SESSION_ID}" ]]; then
  echo "[GEMINI-E2E] ERROR: session not found"
  cat "${WORK_DIR}/session.log" || true
  exit 1
fi

echo "[GEMINI-E2E] session_id=${SESSION_ID}"

echo "[GEMINI-E2E] wait READY token from startup prompt"
if ! wait_for_token "${SESSION_ID}" "${READY_TOKEN}" "${WORK_DIR}/screen_ready.json" "${WORK_DIR}/output_ready.json"; then
  echo "[GEMINI-E2E] fallback: wait prompt + send READY prompt via relay"
  if ! wait_for_prompt_ready "${SESSION_ID}"; then
    echo "[GEMINI-E2E] ERROR: prompt not ready"
    cat "${WORK_DIR}/screen_prompt.json" || true
    cat "${WORK_DIR}/session.log" | tail -n 180 || true
    exit 1
  fi
  send_prompt "${SESSION_ID}" "请仅回复 ${READY_TOKEN}，然后等待我的下一条输入" "gemini-real-e2e-ready-fallback"
  if ! wait_for_token "${SESSION_ID}" "${READY_TOKEN}" "${WORK_DIR}/screen_ready.json" "${WORK_DIR}/output_ready.json"; then
    echo "[GEMINI-E2E] ERROR: ready token not found"
    cat "${WORK_DIR}/screen_ready.json" || true
    cat "${WORK_DIR}/output_ready.json" || true
    cat "${WORK_DIR}/session.log" | tail -n 220 || true
    exit 1
  fi
fi

echo "[GEMINI-E2E] READY token observed"

echo "[GEMINI-E2E] send second prompt via relay control"
send_prompt "${SESSION_ID}" "请仅回复 ${SECOND_TOKEN}" "gemini-real-e2e-second"

if ! wait_for_token "${SESSION_ID}" "${SECOND_TOKEN}" "${WORK_DIR}/screen_second.json" "${WORK_DIR}/output_second.json"; then
  echo "[GEMINI-E2E] ERROR: second token not found"
  cat "${WORK_DIR}/screen_second.json" || true
  cat "${WORK_DIR}/output_second.json" || true
  cat "${WORK_DIR}/session.log" | tail -n 240 || true
  exit 1
fi

echo "[GEMINI-E2E] SECOND token observed"

cp "${WORK_DIR}/relay.log" "${ROOT_DIR}/reports/gemini_real_e2e_relay.log"
cp "${WORK_DIR}/session.log" "${ROOT_DIR}/reports/gemini_real_e2e_session.log"
if [[ -f "${WORK_DIR}/screen_prompt.json" ]]; then
  cp "${WORK_DIR}/screen_prompt.json" "${ROOT_DIR}/reports/gemini_real_e2e_screen_prompt.json"
fi
cp "${WORK_DIR}/screen_ready.json" "${ROOT_DIR}/reports/gemini_real_e2e_screen_ready.json"
cp "${WORK_DIR}/screen_second.json" "${ROOT_DIR}/reports/gemini_real_e2e_screen_second.json"
cp "${WORK_DIR}/output_ready.json" "${ROOT_DIR}/reports/gemini_real_e2e_output_ready.json"
cp "${WORK_DIR}/output_second.json" "${ROOT_DIR}/reports/gemini_real_e2e_output_second.json"

printf "session_id=%s\nready_token=%s\nsecond_token=%s\n" "${SESSION_ID}" "${READY_TOKEN}" "${SECOND_TOKEN}" > "${ROOT_DIR}/reports/gemini_real_e2e_summary.txt"

echo "[GEMINI-E2E] PASS"
