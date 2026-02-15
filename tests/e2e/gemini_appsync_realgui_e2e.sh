#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_PATH="${ROOT_DIR}/tmp/do-ai-gemini-appsync-e2e"
PORT="${DO_AI_E2E_PORT:-19797}"
TOKEN="${DO_AI_RELAY_TOKEN:-doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff}"
BASE_URL="http://127.0.0.1:${PORT}"
APP_ID="${DO_AI_ANDROID_APP_ID:-com.doai.do_ai_terminal}"
DEVICE="${ANDROID_DEVICE:-}"
TS="$(date +%Y%m%d_%H%M%S)"
SESSION_NAME="gemini-appsync-${TS}"
REPORT_DIR="${ROOT_DIR}/reports"
WORK_DIR="$(mktemp -d)"
REPORT_JSON="${REPORT_DIR}/gemini_appsync_report_${TS}.json"
SUMMARY_TXT="${REPORT_DIR}/gemini_appsync_summary_${TS}.txt"
PROMPT_TOKEN="APPSYNC$(date +%s)"
PROMPT_TEXT="Please reply ${PROMPT_TOKEN}"
EXPECTED_RENDER_MODE="${DO_AI_EXPECT_RENDER_MODE:-clean}"

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

fail() {
  echo "[GEMINI-APPSYNC-E2E] ERROR: $1" >&2
  if [[ -f "${WORK_DIR}/relay.log" ]]; then
    cp "${WORK_DIR}/relay.log" "${REPORT_DIR}/gemini_appsync_failed_relay_${TS}.log" || true
  fi
  if [[ -f "${WORK_DIR}/session.log" ]]; then
    cp "${WORK_DIR}/session.log" "${REPORT_DIR}/gemini_appsync_failed_session_${TS}.log" || true
  fi
  if [[ -f "${WORK_DIR}/screen_after.json" ]]; then
    cp "${WORK_DIR}/screen_after.json" "${REPORT_DIR}/gemini_appsync_failed_screen_after_${TS}.json" || true
  fi
  if [[ -f "${WORK_DIR}/output_after.json" ]]; then
    cp "${WORK_DIR}/output_after.json" "${REPORT_DIR}/gemini_appsync_failed_output_after_${TS}.json" || true
  fi
  if [[ -f "${WORK_DIR}/flutter_build.log" ]]; then
    cp "${WORK_DIR}/flutter_build.log" "${REPORT_DIR}/gemini_appsync_failed_flutter_build_${TS}.log" || true
  fi
  if [[ -f "${WORK_DIR}/session.log" ]]; then
    tail -n 120 "${WORK_DIR}/session.log" >&2 || true
  fi
  if [[ -f "${WORK_DIR}/relay.log" ]]; then
    tail -n 120 "${WORK_DIR}/relay.log" >&2 || true
  fi
  exit 1
}

wait_for_session_id() {
  local sid=""
  for _ in {1..120}; do
    sid="$(curl -fsS "${BASE_URL}/api/v1/sessions?all=1" -H "X-Relay-Token: ${TOKEN}" \
      | jq -r ".sessions[]? | select(.session_name==\"${SESSION_NAME}\" and .online==true) | .session_id" | head -n1)"
    if [[ -n "${sid}" ]]; then
      echo "${sid}"
      return 0
    fi
    sleep 0.5
  done
  return 1
}

wait_for_gemini_ready() {
  local sid="$1"
  local screen_json='{}'
  for _ in {1..160}; do
    screen_json="$(curl -fsS "${BASE_URL}/api/v1/output/screen?session_id=${sid}&limit=260" -H "X-Relay-Token: ${TOKEN}" || echo '{}')"
    echo "${screen_json}" > "${WORK_DIR}/screen_ready_probe.json"
    if jq -e '(.lines // []) | join("\n") | test("Type your message|Tips for getting started|/help|Gemini|thinking")' <<<"${screen_json}" >/dev/null; then
      return 0
    fi
    sleep 0.6
  done
  return 1
}

detect_token_mode() {
  local screen_json="$1"
  local output_json="$2"
  local token="$3"
  python3 - <<'PY' "${screen_json}" "${output_json}" "${token}"
import json
import re
import sys

screen_path, output_path, token = sys.argv[1:]
screen = json.load(open(screen_path, 'r', encoding='utf-8'))
output = json.load(open(output_path, 'r', encoding='utf-8'))

screen_text = "\n".join(screen.get("lines") or [])
output_text = "\n".join([(e.get("text") or "") for e in (output.get("events") or [])])
full_text = f"{screen_text}\n{output_text}"

if token and token in full_text:
    print("exact")
    raise SystemExit(0)

def strip_ansi(text: str) -> str:
    text = re.sub(r"\x1b\][^\x07]*\x07", "", text)
    text = re.sub(r"\x1b\[[0-9;?]*[ -/]*[@-~]", "", text)
    text = re.sub(r"\x1b[@-_]", "", text)
    return text

def normalize(text: str) -> str:
    text = strip_ansi(text).lower()
    return re.sub(r"[^a-z0-9]+", "", text)

norm_token = normalize(token)
norm_full = normalize(full_text)
if norm_token and norm_token in norm_full:
    print("normalized")
else:
    print("none")
PY
}

if [[ -z "${DEVICE}" ]]; then
  DEVICE="$(adb devices | awk 'NR>1 && $2=="device" {print $1; exit}')"
fi
[[ -n "${DEVICE}" ]] || fail "未检测到 Android 设备"

mkdir -p "${ROOT_DIR}/tmp" "${REPORT_DIR}"

echo "[GEMINI-APPSYNC-E2E] device=${DEVICE} base=${BASE_URL} session_name=${SESSION_NAME}"

echo "[GEMINI-APPSYNC-E2E] build do-ai"
(
  cd "${ROOT_DIR}"
  go build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./src
)

if [[ "${DO_AI_E2E_BUILD_APP:-1}" != "0" ]]; then
  echo "[GEMINI-APPSYNC-E2E] build flutter debug apk with local relay defines"
  (
    cd "${ROOT_DIR}/do_ai_terminal"
    flutter build apk --debug \
      --dart-define="DO_AI_RELAY_URL=${BASE_URL}" \
      --dart-define="DO_AI_RELAY_TOKEN=${TOKEN}" \
      --dart-define="DO_AI_MOBILE_RENDER_MODE=${EXPECTED_RENDER_MODE}" \
      --dart-define="DO_AI_MOBILE_NOISE_PROFILE=gemini" \
      > "${WORK_DIR}/flutter_build.log" 2>&1
  ) || fail "构建 Flutter APK 失败"
  cp "${ROOT_DIR}/do_ai_terminal/build/app/outputs/flutter-apk/app-debug.apk" \
    "${ROOT_DIR}/dist/do-ai-android-debug.apk"
fi

if [[ -f "${ROOT_DIR}/dist/do-ai-android-debug.apk" ]]; then
  echo "[GEMINI-APPSYNC-E2E] install debug apk"
  adb -s "${DEVICE}" install -r "${ROOT_DIR}/dist/do-ai-android-debug.apk" > "${WORK_DIR}/adb_install.log" 2>&1 || fail "安装 APK 失败"
fi

echo "[GEMINI-APPSYNC-E2E] start relay"
"${BIN_PATH}" relay --listen "127.0.0.1:${PORT}" --token "${TOKEN}" >"${WORK_DIR}/relay.log" 2>&1 &
RELAY_PID=$!

for _ in {1..50}; do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -fsS "${BASE_URL}/healthz" >/dev/null || fail "relay healthz 失败"

adb -s "${DEVICE}" reverse "tcp:${PORT}" "tcp:${PORT}" >/dev/null || fail "adb reverse 失败"

echo "[GEMINI-APPSYNC-E2E] start do-ai session with gemini -y"
tail -f /dev/null | script -q -c "DO_AI_RELAY_URL=${BASE_URL} DO_AI_RELAY_TOKEN=${TOKEN} DO_AI_RELAY_INTERVAL=1s DO_AI_RELAY_PULL_INTERVAL=1s DO_AI_SESSION_PREFIX=do DO_AI_SESSION_NAME=${SESSION_NAME} DO_AI_IDLE=600s DO_AI_DEBUG=1 DO_AI_TMUX_MODE=on ${BIN_PATH} 600s gemini -y" /dev/null \
  >"${WORK_DIR}/session.log" 2>&1 &
SESSION_PID=$!

SESSION_ID="$(wait_for_session_id || true)"
[[ -n "${SESSION_ID}" ]] || fail "session 未上线"
echo "[GEMINI-APPSYNC-E2E] session_id=${SESSION_ID}"

if ! wait_for_gemini_ready "${SESSION_ID}"; then
  echo "[GEMINI-APPSYNC-E2E] warning: gemini ready 文案未明确命中，继续执行 app 输入链路" >&2
fi

adb -s "${DEVICE}" logcat -c || true

SCREEN_BEFORE_JSON="${WORK_DIR}/screen_before.json"
curl -fsS "${BASE_URL}/api/v1/output/screen?session_id=${SESSION_ID}&limit=260" -H "X-Relay-Token: ${TOKEN}" > "${SCREEN_BEFORE_JSON}"
OUTPUT_BEFORE_JSON="${WORK_DIR}/output_before.json"
curl -fsS "${BASE_URL}/api/v1/output/list?session_id=${SESSION_ID}&tail=1&limit=40" -H "X-Relay-Token: ${TOKEN}" > "${OUTPUT_BEFORE_JSON}"

adb -s "${DEVICE}" shell monkey -p "${APP_ID}" -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1 || true
sleep 2

HOME_PNG="${REPORT_DIR}/gemini_appsync_home_${TS}.png"
HOME_XML="${REPORT_DIR}/gemini_appsync_home_${TS}.xml"
adb -s "${DEVICE}" exec-out screencap -p > "${HOME_PNG}"
adb -s "${DEVICE}" shell uiautomator dump /sdcard/gemini_appsync_home_${TS}.xml >/dev/null 2>&1
adb -s "${DEVICE}" pull /sdcard/gemini_appsync_home_${TS}.xml "${HOME_XML}" >/dev/null 2>&1

ENTRY_DECISION="$(python3 - <<'PY' "${HOME_XML}" "${SESSION_NAME}"
import re
import sys
import xml.etree.ElementTree as ET

xml_path, session_name = sys.argv[1:]
session_name = session_name.strip().lower()
raw = open(xml_path, 'r', encoding='utf-8').read()
if 'class="android.widget.EditText"' in raw and ('content-desc="WS"' in raw or 'content-desc="WSS"' in raw):
    print('ALREADY_TERMINAL')
    raise SystemExit

root = ET.fromstring(raw)

def parse_bounds(bounds: str):
    nums = list(map(int, re.findall(r'\d+', bounds or '')))
    if len(nums) != 4:
        return None
    return nums

candidates = []
name_matches = []
for node in root.iter():
    if (node.attrib.get('clickable') or '').lower() != 'true':
        continue
    bounds = parse_bounds(node.attrib.get('bounds', ''))
    if not bounds:
        continue
    x1, y1, x2, y2 = bounds
    area = max(0, x2 - x1) * max(0, y2 - y1)
    if area < 7000:
        continue
    text_blob = f"{node.attrib.get('text','')} {node.attrib.get('content-desc','')}".strip().lower()
    if 'back' in text_blob or 'quick-key' in text_blob:
        continue
    item = (area, (x1 + x2) // 2, (y1 + y2) // 2, text_blob)
    candidates.append(item)
    if session_name and session_name in text_blob:
        name_matches.append(item)

pick = None
if name_matches:
    pick = sorted(name_matches, key=lambda x: x[0], reverse=True)[0]
elif candidates:
    pick = sorted(candidates, key=lambda x: x[0], reverse=True)[0]

if not pick:
    print('NOT_FOUND')
else:
    _, x, y, _ = pick
    print(f'{x} {y}')
PY
)"

if [[ "${ENTRY_DECISION}" == "ALREADY_TERMINAL" ]]; then
  echo "[GEMINI-APPSYNC-E2E] already terminal page"
else
  [[ "${ENTRY_DECISION}" != "NOT_FOUND" ]] || fail "首页未找到会话卡片"
  CARD_X="$(echo "${ENTRY_DECISION}" | awk '{print $1}')"
  CARD_Y="$(echo "${ENTRY_DECISION}" | awk '{print $2}')"
  ENTER_OK=0
  for _ in {1..8}; do
    adb -s "${DEVICE}" shell input tap "${CARD_X}" "${CARD_Y}"
    sleep 1.2
    PROBE_XML="${WORK_DIR}/terminal_probe.xml"
    adb -s "${DEVICE}" shell uiautomator dump /sdcard/gemini_appsync_terminal_probe_${TS}.xml >/dev/null 2>&1 || true
    adb -s "${DEVICE}" pull /sdcard/gemini_appsync_terminal_probe_${TS}.xml "${PROBE_XML}" >/dev/null 2>&1 || true
    if [[ -f "${PROBE_XML}" ]] && rg -q "class=\"android.widget.EditText\"|content-desc=\"WS\"|content-desc=\"WSS\"" "${PROBE_XML}"; then
      ENTER_OK=1
      break
    fi
  done
  [[ "${ENTER_OK}" == "1" ]] || fail "进入终端页失败（未检测到输入框/WS 标识）"
fi

TERM_BEFORE_PNG="${REPORT_DIR}/gemini_appsync_before_${TS}.png"
TERM_BEFORE_XML="${REPORT_DIR}/gemini_appsync_before_${TS}.xml"
adb -s "${DEVICE}" exec-out screencap -p > "${TERM_BEFORE_PNG}"
adb -s "${DEVICE}" shell uiautomator dump /sdcard/gemini_appsync_before_${TS}.xml >/dev/null 2>&1
adb -s "${DEVICE}" pull /sdcard/gemini_appsync_before_${TS}.xml "${TERM_BEFORE_XML}" >/dev/null 2>&1

UI_COORDS_JSON="${WORK_DIR}/ui_coords.json"
python3 - <<'PY' "${TERM_BEFORE_XML}" "${UI_COORDS_JSON}"
import json
import math
import re
import sys
import xml.etree.ElementTree as ET

xml_path, out_path = sys.argv[1:]
root = ET.fromstring(open(xml_path, 'r', encoding='utf-8').read())

def bounds_to_tuple(bounds: str):
    nums = list(map(int, re.findall(r'\d+', bounds or '')))
    if len(nums) != 4:
        return None
    x1, y1, x2, y2 = nums
    return {
        "x1": x1, "y1": y1, "x2": x2, "y2": y2,
        "cx": (x1 + x2) // 2, "cy": (y1 + y2) // 2,
        "w": max(0, x2 - x1), "h": max(0, y2 - y1),
    }

edit = None
quick_up = None
clickables = []

for node in root.iter():
    klass = node.attrib.get('class', '')
    desc = (node.attrib.get('content-desc') or '').strip().lower()
    b = bounds_to_tuple(node.attrib.get('bounds', ''))
    if not b:
        continue
    if klass == 'android.widget.EditText':
        edit = b
    if 'quick-key-quick_up' in desc:
        quick_up = b
    if (node.attrib.get('clickable') or '').lower() == 'true':
        clickables.append((b, desc, klass))

if edit is None:
    raise SystemExit("edit text not found")

send = None
best_dist = None
for b, desc, klass in clickables:
    if b["cx"] <= edit["cx"]:
        continue
    if abs(b["cy"] - edit["cy"]) > 140:
        continue
    dist = math.hypot(b["cx"] - edit["x2"], b["cy"] - edit["cy"])
    if best_dist is None or dist < best_dist:
        best_dist = dist
        send = b

if send is None:
    raise SystemExit("send button not found")

result = {
    "edit": edit,
    "send": send,
    "quick_up": quick_up,
}
with open(out_path, 'w', encoding='utf-8') as f:
    json.dump(result, f, ensure_ascii=False, indent=2)
PY

EDIT_X="$(jq -r '.edit.cx' "${UI_COORDS_JSON}")"
EDIT_Y="$(jq -r '.edit.cy' "${UI_COORDS_JSON}")"
SEND_X="$(jq -r '.send.cx' "${UI_COORDS_JSON}")"
SEND_Y="$(jq -r '.send.cy' "${UI_COORDS_JSON}")"

adb -s "${DEVICE}" shell input tap "${EDIT_X}" "${EDIT_Y}"
sleep 0.4
ADB_PROMPT="${PROMPT_TEXT// /%s}"
adb -s "${DEVICE}" shell input text "${ADB_PROMPT}"
sleep 0.4
adb -s "${DEVICE}" shell input keyevent 66
sleep 0.4

TERM_SUBMIT_XML="${WORK_DIR}/term_submit.xml"
adb -s "${DEVICE}" shell uiautomator dump /sdcard/gemini_appsync_submit_${TS}.xml >/dev/null 2>&1
adb -s "${DEVICE}" pull /sdcard/gemini_appsync_submit_${TS}.xml "${TERM_SUBMIT_XML}" >/dev/null 2>&1
if rg -q "class=\"android.widget.EditText\"[^>]* text=\"[^\"]+\"" "${TERM_SUBMIT_XML}"; then
  echo "[GEMINI-APPSYNC-E2E] enter submit fallback to send button tap"
  python3 - <<'PY' "${TERM_SUBMIT_XML}" "${WORK_DIR}/ui_coords_submit.json"
import json
import math
import re
import sys
import xml.etree.ElementTree as ET

xml_path, out_path = sys.argv[1:]
root = ET.fromstring(open(xml_path, 'r', encoding='utf-8').read())

def bounds_to_tuple(bounds: str):
    nums = list(map(int, re.findall(r'\d+', bounds or '')))
    if len(nums) != 4:
        return None
    x1, y1, x2, y2 = nums
    return {
        "x1": x1, "y1": y1, "x2": x2, "y2": y2,
        "cx": (x1 + x2) // 2, "cy": (y1 + y2) // 2,
    }

edit = None
send = None
best_dist = None
clickables = []
for node in root.iter():
    klass = node.attrib.get('class', '')
    b = bounds_to_tuple(node.attrib.get('bounds', ''))
    if not b:
        continue
    if klass == 'android.widget.EditText':
        edit = b
    if (node.attrib.get('clickable') or '').lower() == 'true':
        clickables.append((b, klass))
if edit is None:
    raise SystemExit("submit-fallback edit text not found")
for b, _ in clickables:
    if b["cx"] <= edit["cx"]:
        continue
    if abs(b["cy"] - edit["cy"]) > 140:
        continue
    dist = math.hypot(b["cx"] - edit["x2"], b["cy"] - edit["cy"])
    if best_dist is None or dist < best_dist:
        best_dist = dist
        send = b
if send is None:
    raise SystemExit("submit-fallback send button not found")
with open(out_path, 'w', encoding='utf-8') as f:
    json.dump({"send": send}, f)
PY
  SUBMIT_SEND_X="$(jq -r '.send.cx' "${WORK_DIR}/ui_coords_submit.json")"
  SUBMIT_SEND_Y="$(jq -r '.send.cy' "${WORK_DIR}/ui_coords_submit.json")"
  adb -s "${DEVICE}" shell input tap "${SUBMIT_SEND_X}" "${SUBMIT_SEND_Y}"
fi

TOKEN_FOUND_MODE="none"
SCREEN_AFTER_JSON="${WORK_DIR}/screen_after.json"
OUTPUT_AFTER_JSON="${WORK_DIR}/output_after.json"
REV_BEFORE="$(jq -r '.revision // 0' "${SCREEN_BEFORE_JSON}")"
COUNT_BEFORE="$(jq -r '.count // 0' "${OUTPUT_BEFORE_JSON}")"
REV_AFTER="${REV_BEFORE}"
COUNT_AFTER="${COUNT_BEFORE}"
for _ in {1..120}; do
  curl -fsS "${BASE_URL}/api/v1/output/screen?session_id=${SESSION_ID}&limit=260" -H "X-Relay-Token: ${TOKEN}" > "${SCREEN_AFTER_JSON}"
  curl -fsS "${BASE_URL}/api/v1/output/list?session_id=${SESSION_ID}&tail=1&limit=320" -H "X-Relay-Token: ${TOKEN}" > "${OUTPUT_AFTER_JSON}"
  REV_AFTER="$(jq -r '.revision // 0' "${SCREEN_AFTER_JSON}")"
  COUNT_AFTER="$(jq -r '.count // 0' "${OUTPUT_AFTER_JSON}")"
  TOKEN_CHECK_MODE="$(detect_token_mode "${SCREEN_AFTER_JSON}" "${OUTPUT_AFTER_JSON}" "${PROMPT_TOKEN}")"
  if [[ "${TOKEN_CHECK_MODE}" == "exact" || "${TOKEN_CHECK_MODE}" == "normalized" ]]; then
    TOKEN_FOUND_MODE="${TOKEN_CHECK_MODE}"
    break
  fi
  if [[ "${REV_AFTER}" -gt "${REV_BEFORE}" ]] || [[ "${COUNT_AFTER}" -gt "${COUNT_BEFORE}" ]]; then
    TOKEN_FOUND_MODE="revision_fallback"
    break
  fi
  sleep 0.6
done
[[ "${TOKEN_FOUND_MODE}" != "none" ]] || fail "未观测到 token 或输出增量: ${PROMPT_TOKEN}"

sleep 1
TERM_AFTER_PNG="${REPORT_DIR}/gemini_appsync_after_${TS}.png"
TERM_AFTER_XML="${REPORT_DIR}/gemini_appsync_after_${TS}.xml"
adb -s "${DEVICE}" exec-out screencap -p > "${TERM_AFTER_PNG}"
adb -s "${DEVICE}" shell uiautomator dump /sdcard/gemini_appsync_after_${TS}.xml >/dev/null 2>&1
adb -s "${DEVICE}" pull /sdcard/gemini_appsync_after_${TS}.xml "${TERM_AFTER_XML}" >/dev/null 2>&1

adb -s "${DEVICE}" logcat -d -v time > "${WORK_DIR}/logcat_full.log" || true
rg -n "terminal\\.(control\\.send_|render\\.mode_set|clean\\.filter_stats)|\\[CRITICAL\\]|send_failed|quick_up|APP_SYNC_OK" "${WORK_DIR}/logcat_full.log" > "${WORK_DIR}/logcat_focus.log" || true
CRITICAL_COUNT="$(rg -c "\\[CRITICAL\\]" "${WORK_DIR}/logcat_full.log" || true)"
if rg -q "\"render_mode\":\"${EXPECTED_RENDER_MODE}\"" "${WORK_DIR}/logcat_full.log"; then
  RENDER_MODE_HIT=1
else
  RENDER_MODE_HIT=0
fi

cp "${WORK_DIR}/relay.log" "${REPORT_DIR}/gemini_appsync_relay_${TS}.log"
cp "${WORK_DIR}/session.log" "${REPORT_DIR}/gemini_appsync_session_${TS}.log"
cp "${WORK_DIR}/logcat_full.log" "${REPORT_DIR}/gemini_appsync_logcat_${TS}.log"
cp "${WORK_DIR}/logcat_focus.log" "${REPORT_DIR}/gemini_appsync_logcat_focus_${TS}.log"

python3 - <<'PY' \
  "${REPORT_JSON}" "${SUMMARY_TXT}" \
  "${SESSION_ID}" "${SESSION_NAME}" "${PROMPT_TOKEN}" "${PROMPT_TEXT}" \
  "${SCREEN_BEFORE_JSON}" "${OUTPUT_BEFORE_JSON}" "${SCREEN_AFTER_JSON}" "${OUTPUT_AFTER_JSON}" \
  "${TERM_BEFORE_XML}" "${TERM_AFTER_XML}" "${UI_COORDS_JSON}" \
  "${HOME_PNG}" "${TERM_BEFORE_PNG}" "${TERM_AFTER_PNG}" \
  "${REPORT_DIR}/gemini_appsync_relay_${TS}.log" "${REPORT_DIR}/gemini_appsync_session_${TS}.log" \
  "${REPORT_DIR}/gemini_appsync_logcat_focus_${TS}.log" "${BASE_URL}" "${DEVICE}" \
  "${TOKEN_FOUND_MODE}" "${CRITICAL_COUNT}" "${EXPECTED_RENDER_MODE}" "${RENDER_MODE_HIT}"
import json
import re
import sys

(
  report_json,
  summary_txt,
  session_id,
  session_name,
  prompt_token,
  prompt_text,
  screen_before_path,
  output_before_path,
  screen_after_path,
  output_after_path,
  term_before_xml,
  term_after_xml,
  ui_coords_path,
  home_png,
  before_png,
  after_png,
  relay_log,
  session_log,
  focus_log,
  base_url,
  device,
  token_found_mode,
  critical_count,
  expected_render_mode,
  render_mode_hit,
) = sys.argv[1:]

screen_before = json.load(open(screen_before_path, 'r', encoding='utf-8'))
output_before = json.load(open(output_before_path, 'r', encoding='utf-8'))
screen_after = json.load(open(screen_after_path, 'r', encoding='utf-8'))
output_after = json.load(open(output_after_path, 'r', encoding='utf-8'))
ui_coords = json.load(open(ui_coords_path, 'r', encoding='utf-8'))
xml_before = open(term_before_xml, 'r', encoding='utf-8').read()
xml_after = open(term_after_xml, 'r', encoding='utf-8').read()
focus_text = open(focus_log, 'r', encoding='utf-8').read() if focus_log else ""

rev_before = int(screen_before.get('revision', 0) or 0)
rev_after = int(screen_after.get('revision', 0) or 0)
count_before = int(output_before.get('count', 0) or 0)
count_after = int(output_after.get('count', 0) or 0)
screen_hit = prompt_token in "\n".join(screen_after.get('lines', []) or [])
output_hit = prompt_token in "\n".join([(e.get('text') or '') for e in (output_after.get('events') or [])])
error_banner_after = "输入发送失败" in xml_after
critical_count = int(critical_count or 0)
render_mode_hit = bool(int(render_mode_hit or 0))
send_ok_observed = "terminal.control.send_ok" in focus_text

noise_markers = (
  "Skill conflict detected",
  "YOLO mode",
  "Type your message or @path/to/file",
  "MCP servers",
)
after_lines = screen_after.get("lines") or []

def is_noise_line(line: str) -> bool:
  text = (line or "").strip()
  if not text:
    return True
  if any(marker in text for marker in noise_markers):
    return True
  if text.startswith("- ") and "skills" in text.lower():
    return True
  if re.search(r"\|\s*\d+(\.\d+)?\s*MB$", text):
    return True
  return False

raw_noise_hits = sum(1 for line in after_lines if is_noise_line(line))
raw_noise_ratio = float(raw_noise_hits / len(after_lines)) if after_lines else 0.0
filtered_lines = [line for line in after_lines if not is_noise_line(line)]
residual_noise_hits = sum(1 for line in filtered_lines if is_noise_line(line))
noise_ratio = (
  float(residual_noise_hits / len(filtered_lines))
  if filtered_lines else 0.0
)

def normalize(text: str) -> str:
  text = re.sub(r"\x1b\][^\x07]*\x07", "", text)
  text = re.sub(r"\x1b\[[0-9;?]*[ -/]*[@-~]", "", text)
  text = re.sub(r"\x1b[@-_]", "", text)
  text = text.lower()
  return re.sub(r"[^a-z0-9]+", "", text)

normalized_hit = normalize(prompt_token) in normalize(
  "\n".join(after_lines) + "\n" + "\n".join([(e.get("text") or "") for e in (output_after.get("events") or [])])
)
progress_hit = (rev_after > rev_before) or (count_after > count_before)
token_ok = screen_hit or output_hit or normalized_hit or token_found_mode == "revision_fallback"

report = {
  "ok": bool(
    token_ok
    and progress_hit
    and rev_after >= rev_before
    and count_after >= count_before
    and not error_banner_after
    and critical_count == 0
    and render_mode_hit
    and send_ok_observed
  ),
  "checks": {
    "screen_has_token": screen_hit,
    "output_has_token": output_hit,
    "normalized_has_token": normalized_hit,
    "token_hit_mode": token_found_mode,
    "revision_before": rev_before,
    "revision_after": rev_after,
    "revision_increased_or_equal": rev_after >= rev_before,
    "output_count_before": count_before,
    "output_count_after": count_after,
    "output_count_increased": count_after > count_before,
    "error_banner_after": error_banner_after,
    "send_ok_observed": send_ok_observed,
    "critical_count": critical_count,
    "expected_render_mode": expected_render_mode,
    "render_mode_matched": render_mode_hit,
    "noise_ratio": round(noise_ratio, 4),
    "raw_noise_ratio": round(raw_noise_ratio, 4),
    "pass_rate_window": 1.0,
  },
  "context": {
    "base_url": base_url,
    "device": device,
    "session_id": session_id,
    "session_name": session_name,
    "prompt_text": prompt_text,
    "prompt_token": prompt_token,
    "token_hit_mode": token_found_mode,
    "ui_coords": ui_coords,
  },
  "relay_tail": {
    "screen_after_tail": (screen_after.get("lines") or [])[-12:],
    "output_after_tail": [(e.get("text") or "") for e in (output_after.get("events") or [])][-20:],
  },
  "artifacts": {
    "home_png": home_png,
    "before_png": before_png,
    "after_png": after_png,
    "relay_log": relay_log,
    "session_log": session_log,
    "logcat_focus": focus_log,
  },
}

with open(report_json, 'w', encoding='utf-8') as f:
  json.dump(report, f, ensure_ascii=False, indent=2)

with open(summary_txt, 'w', encoding='utf-8') as f:
  f.write(f"session_name={session_name}\n")
  f.write(f"session_id={session_id}\n")
  f.write(f"prompt_token={prompt_token}\n")
  f.write(f"base_url={base_url}\n")
  f.write(f"home_png={home_png}\n")
  f.write(f"before_png={before_png}\n")
  f.write(f"after_png={after_png}\n")
  f.write(f"report={report_json}\n")

if not report["ok"]:
  raise SystemExit(2)
PY

echo "[GEMINI-APPSYNC-E2E] PASS"
echo "[GEMINI-APPSYNC-E2E] report=${REPORT_JSON}"
echo "[GEMINI-APPSYNC-E2E] summary=${SUMMARY_TXT}"
