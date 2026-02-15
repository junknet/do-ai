# do-ai Flutter Terminal

do-ai Android 终端客户端（Flutter 版），用于共享查看 `do-ai` 会话并进行实时交互。

## 关键能力

- 实时会话：`/api/v1/output/ws` 增量流 + 快照兜底
- 终端模式：`Clean / Raw / Auto` 显式切换（默认 `Clean`）
- 多会话切换：会话标签页 + 长按关闭
- 工程化观测：结构化日志（含 `trace_id`、`render_mode`、`noise_drop_count`）

## 构建与安装

```bash
cd do_ai_terminal

# 使用本地 relay 构建 debug APK（推荐真机调试）
flutter build apk --debug \
  --dart-define=DO_AI_RELAY_URL=http://127.0.0.1:19797 \
  --dart-define=DO_AI_RELAY_TOKEN=doai-relay-v1-xxx \
  --dart-define=DO_AI_MOBILE_RENDER_MODE=clean \
  --dart-define=DO_AI_MOBILE_NOISE_PROFILE=gemini

adb install -r build/app/outputs/flutter-apk/app-debug.apk
```

## 渲染模式

- `DO_AI_MOBILE_RENDER_MODE=clean|raw|auto`
- `DO_AI_MOBILE_NOISE_PROFILE=gemini|default`

说明：
- `clean`：移动端默认，优先可读性
- `raw`：完整字节流回放，保留全部终端细节
- `auto`：先 raw，检测到噪声后自动切 clean

App 内可在终端页 Header 直接切换模式，且会持久化到本地。

## 真机 E2E

```bash
# 单轮真机回归
tests/e2e/gemini_appsync_realgui_e2e.sh

# 20 轮严格 SLA（默认 20，可改 DO_AI_E2E_ROUNDS）
DO_AI_E2E_ROUNDS=20 tests/e2e/gemini_appsync_sla_realgui.sh
```
