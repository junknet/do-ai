#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ROUND_SCRIPT="${ROOT_DIR}/tests/e2e/gemini_appsync_realgui_e2e.sh"
ROUNDS="${DO_AI_E2E_ROUNDS:-20}"
STOP_ON_FAIL="${DO_AI_E2E_STOP_ON_FAIL:-0}"
TS="$(date +%Y%m%d_%H%M%S)"
REPORT_DIR="${ROOT_DIR}/reports"
mkdir -p "${REPORT_DIR}"

SUMMARY_JSON="${REPORT_DIR}/gemini_appsync_sla_${TS}.json"
SUMMARY_MD="${REPORT_DIR}/DELIVERY_PRODUCT_READY_${TS}.md"
RUN_LIST="${REPORT_DIR}/gemini_appsync_sla_runs_${TS}.txt"

pass_count=0
fail_count=0
critical_sum=0
send_ok_count=0
noise_fail_count=0
render_mode_fail_count=0

echo "[]" > "${RUN_LIST}"

for i in $(seq 1 "${ROUNDS}"); do
  echo "[GEMINI-APPSYNC-SLA] round ${i}/${ROUNDS}"
  round_log="${REPORT_DIR}/gemini_appsync_sla_round_${TS}_$(printf "%02d" "${i}").log"
  if output="$("${ROUND_SCRIPT}" 2>&1)"; then
    echo "${output}" | tee "${round_log}" >/dev/null
  else
    echo "${output}" | tee "${round_log}" >/dev/null
  fi

  report_path="$(echo "${output}" | rg -o 'report=.*' | tail -n1 | cut -d= -f2- || true)"
  if [[ -z "${report_path}" || ! -f "${report_path}" ]]; then
    fail_count=$((fail_count + 1))
    if [[ "${STOP_ON_FAIL}" == "1" ]]; then
      break
    fi
    continue
  fi

  ok="$(jq -r '.ok' "${report_path}")"
  critical="$(jq -r '.checks.critical_count // 0' "${report_path}")"
  send_ok="$(jq -r '.checks.send_ok_observed // false' "${report_path}")"
  noise_ratio="$(jq -r '.checks.noise_ratio // 1' "${report_path}")"
  render_mode_ok="$(jq -r '.checks.render_mode_matched // false' "${report_path}")"

  if [[ "${ok}" == "true" ]]; then
    pass_count=$((pass_count + 1))
  else
    fail_count=$((fail_count + 1))
  fi
  critical_sum=$((critical_sum + critical))
  if [[ "${send_ok}" == "true" ]]; then
    send_ok_count=$((send_ok_count + 1))
  fi
  if awk "BEGIN{exit !(${noise_ratio} > 0.01)}"; then
    noise_fail_count=$((noise_fail_count + 1))
  fi
  if [[ "${render_mode_ok}" != "true" ]]; then
    render_mode_fail_count=$((render_mode_fail_count + 1))
  fi

  jq --arg p "${report_path}" '. + [$p]' "${RUN_LIST}" > "${RUN_LIST}.tmp"
  mv "${RUN_LIST}.tmp" "${RUN_LIST}"

  if [[ "${STOP_ON_FAIL}" == "1" && "${ok}" != "true" ]]; then
    break
  fi
done

total_runs=$((pass_count + fail_count))
if [[ "${total_runs}" -le 0 ]]; then
  echo "[GEMINI-APPSYNC-SLA] ERROR: 无有效轮次" >&2
  exit 1
fi

pass_rate="$(awk "BEGIN{printf \"%.4f\", ${pass_count}/${total_runs}}")"
input_success_rate="$(awk "BEGIN{printf \"%.4f\", ${send_ok_count}/${total_runs}}")"
garble_rate="$(awk "BEGIN{printf \"%.4f\", ${noise_fail_count}/${total_runs}}")"

jq -n \
  --arg ts "${TS}" \
  --argjson rounds "${ROUNDS}" \
  --argjson total_runs "${total_runs}" \
  --argjson pass_count "${pass_count}" \
  --argjson fail_count "${fail_count}" \
  --argjson critical_sum "${critical_sum}" \
  --argjson send_ok_count "${send_ok_count}" \
  --argjson noise_fail_count "${noise_fail_count}" \
  --argjson render_mode_fail_count "${render_mode_fail_count}" \
  --arg pass_rate "${pass_rate}" \
  --arg input_success_rate "${input_success_rate}" \
  --arg garble_rate "${garble_rate}" \
  --arg run_list "${RUN_LIST}" \
  '{
    ts: $ts,
    rounds_target: $rounds,
    rounds_completed: $total_runs,
    pass_count: $pass_count,
    fail_count: $fail_count,
    pass_rate: ($pass_rate|tonumber),
    input_success_rate: ($input_success_rate|tonumber),
    garble_rate: ($garble_rate|tonumber),
    critical_count_sum: $critical_sum,
    render_mode_fail_count: $render_mode_fail_count,
    run_report_list_json: $run_list
  }' > "${SUMMARY_JSON}"

{
  echo "# do-ai Gemini AppSync Product Delivery Report (${TS})"
  echo
  echo "## SLA Summary"
  echo "- rounds_target: ${ROUNDS}"
  echo "- rounds_completed: ${total_runs}"
  echo "- pass_count: ${pass_count}"
  echo "- fail_count: ${fail_count}"
  echo "- pass_rate: ${pass_rate}"
  echo "- input_success_rate: ${input_success_rate}"
  echo "- garble_rate(>1% noise): ${garble_rate}"
  echo "- critical_count_sum: ${critical_sum}"
  echo "- render_mode_fail_count: ${render_mode_fail_count}"
  echo
  echo "## Acceptance Gates"
  echo "- pass_rate == 1.0000: $([[ "${pass_count}" -eq "${total_runs}" ]] && echo PASS || echo FAIL)"
  echo "- input_success_rate == 1.0000: $([[ "${send_ok_count}" -eq "${total_runs}" ]] && echo PASS || echo FAIL)"
  echo "- garble_rate < 0.0100: $(awk "BEGIN{print (${garble_rate} < 0.01)?\"PASS\":\"FAIL\"}")"
  echo "- critical_count_sum == 0: $([[ "${critical_sum}" -eq 0 ]] && echo PASS || echo FAIL)"
  echo "- render_mode_fail_count == 0: $([[ "${render_mode_fail_count}" -eq 0 ]] && echo PASS || echo FAIL)"
  echo
  echo "## Artifacts"
  echo "- summary_json: ${SUMMARY_JSON}"
  echo "- run_report_list_json: ${RUN_LIST}"
} > "${SUMMARY_MD}"

echo "[GEMINI-APPSYNC-SLA] summary_json=${SUMMARY_JSON}"
echo "[GEMINI-APPSYNC-SLA] summary_md=${SUMMARY_MD}"
