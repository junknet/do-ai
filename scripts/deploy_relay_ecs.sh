#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "用法: $0 <ecs_ssh> [listen_port]" >&2
  echo "示例: $0 root@47.110.255.240 18787" >&2
  exit 1
fi

ECS_SSH="$1"
LISTEN_PORT="${2:-18787}"
TOKEN="${DO_AI_RELAY_TOKEN:-doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_BIN="/tmp/do-ai-relay-build"
CONF_FILE="/tmp/do_ai_relay.conf"

echo "[1/5] 本地构建 do-ai..."
(
  cd "$ROOT_DIR"
  go build -trimpath -ldflags "-s -w" -o "$BUILD_BIN" ./src
)

echo "[2/5] 生成 supervisor 配置..."
cat > "$CONF_FILE" <<EOF
[program:do_ai_relay]
command=/opt/do-ai/do-ai relay --listen 0.0.0.0:${LISTEN_PORT} --token ${TOKEN}
directory=/opt/do-ai
autostart=true
autorestart=true
startsecs=2
startretries=10
user=root
stdout_logfile=/var/log/do-ai-relay.log
stdout_logfile_maxbytes=20MB
stdout_logfile_backups=5
stderr_logfile=/var/log/do-ai-relay.err.log
stderr_logfile_maxbytes=20MB
stderr_logfile_backups=5
environment=DO_AI_RELAY_STALE_SECONDS='20',DO_AI_ALERT_IDLE_SECS='180',DO_AI_ALERT_KEYWORDS='panic,error,exception,confirm,请选择,是否继续',DO_AI_ALERT_COOLDOWN='3m',DO_AI_NOTIFY_WEBHOOK='',DO_AI_TELEGRAM_BOT_TOKEN='',DO_AI_TELEGRAM_CHAT_ID=''
EOF

echo "[3/5] 上传到 ECS..."
scp -o BatchMode=yes "$BUILD_BIN" "${ECS_SSH}:/tmp/do-ai"
scp -o BatchMode=yes "$CONF_FILE" "${ECS_SSH}:/tmp/do_ai_relay.conf"

echo "[4/5] 远端安装 + supervisor 重载..."
ssh -o BatchMode=yes "$ECS_SSH" "set -e
mkdir -p /opt/do-ai
install -m 755 /tmp/do-ai /opt/do-ai/do-ai
cp /tmp/do_ai_relay.conf /etc/supervisor/conf.d/do_ai_relay.conf
supervisorctl reread
supervisorctl update
supervisorctl restart do_ai_relay || supervisorctl start do_ai_relay
sleep 1
supervisorctl status do_ai_relay
curl -fsS http://127.0.0.1:${LISTEN_PORT}/healthz
"

echo "[5/5] 完成"
echo "Relay URL: http://47.110.255.240:${LISTEN_PORT}"
echo "Relay Token: ${TOKEN}"
echo "客户端示例:"
echo "DO_AI_RELAY_URL=http://47.110.255.240:${LISTEN_PORT} DO_AI_RELAY_TOKEN=${TOKEN} do-ai codex"
