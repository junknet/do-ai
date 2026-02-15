# do-ai

一个“工程师式”前缀工具：以 **透明 TUI 代理** 的方式启动 Claude/Codex/Gemini 等 CLI，当 **连续 3 分钟无输出** 时，自动输入内置提示词（可通过 YAML 覆盖）。

## 设计目标

- **TUI 完整透传**：界面、颜色、快捷键保持不变
- **无人值守**：无输出 3 分钟即自动推进
- **绿色使用**：不替换系统命令、不修改 PATH，按需前缀调用

## 使用方法

```bash
# 进入任意 TUI（示例）
do-ai claude code
do-ai codex
do-ai gemini
```

也可直接在命令行第一个参数传入空闲时间（更简洁）：

```bash
do-ai 5s codex
do-ai 5min 10s codex
do-ai 2m30s codex
```

## 调试开关（可选）

当需要确认“3 分钟无人输出自动注入”是否触发时，可启用调试输出：

```bash
DO_AI_DEBUG=1 do-ai codex
```

触发注入时会在 stderr 打印：
```
[do-ai] 自动注入 YYYY-MM-DD HH:MM:SS
```

## 行为说明

- 仅在 **PTY 无输出 3 分钟** 时注入指令（忽略纯 ANSI 刷屏/空白输出）
- 默认注入内容为：内置提示词（可通过 YAML 配置覆盖，支持 `{LOCK_FILE}` 占位符）
- 运行时会在当前目录创建生命线文件：`.do-ai.lock`；删除后将停止自动注入
- 每 5 次注入会插入一次“校准提示”（`先输出当前计划(3-7条)和已完成清单，再继续执行下一条。`），可用 `DO_AI_CALIB_EVERY=0` 关闭或调整频率
- 默认自动提交：Linux/macOS 为 Enter/CR；Windows 为 Enter+Ctrl-Enter，并补偿 CR 5 次（更稳）。可用 `DO_AI_SUBMIT=0` 关闭；可选 `DO_AI_SUBMIT_MODE=enter|enter-lf|ctrl-enter|alt-enter|enter+ctrl|enter+alt|all` 调整。
- **清理残留输入**：Windows 默认在每次注入前执行 `ctrl-u` 5 次清理输入行；Linux/macOS 默认关闭。
- 可临时改成 10 秒触发（测试用）：`DO_AI_IDLE=10s do-ai codex`
- 不做提示词识别，不做语义判断，专注“无限继续”
- 内置 **DSR 兼容**：当终端未回传光标位置时，自动补发 `ESC[1;1R`，提升 Codex TUI 兼容性
## YAML 配置（可选）

支持通过 YAML 配置**默认空闲时间**与**默认注入文本**。若配置存在，则作为默认值；仍可被命令行与环境变量覆盖。

默认读取位置（按顺序）：
1. `./do-ai.yaml` 或 `./do-ai.yml`
2. `~/.config/do-ai/config.yaml` 或 `~/.config/do-ai/config.yml`
3. `~/.do-ai.yaml` 或 `~/.do-ai.yml`

字段说明：
- `idle`: 触发空闲时间（如 `3m`、`5min 10s`、`120`）
- `message_main`: 主注入文本
- `message_calib`: 校准提示文本（可选）

示例：`do-ai.yaml.example`

## Relay 远程看板（MVP）

支持把多台机器的 `do-ai` 会话状态汇总到公网 Relay，手机可直接打开网页查看，并按关键词/空闲阈值推送通知。

### 1) 启动 Relay 服务端

```bash
# 启动 HTTP 服务（含 Web 看板 + API）
do-ai relay --listen 0.0.0.0:8787
```

访问：
- `http://relay.junknets.com:18787/`（网页看板）
- `http://relay.junknets.com:18787/healthz`（健康检查）

### 2) 客户端上报（每台机器）

```bash
DO_AI_RELAY_URL=http://relay.junknets.com:18787 \
DO_AI_RELAY_TOKEN=doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff \
DO_AI_SESSION_PREFIX=do \
DO_AI_SESSION_NAME=codex-main \
do-ai codex
```

### 3) 通知（可选）

Relay 支持以下环境变量：
- `DO_AI_NOTIFY_WEBHOOK`：Webhook 地址（可逗号分隔）
- `DO_AI_TELEGRAM_BOT_TOKEN` + `DO_AI_TELEGRAM_CHAT_ID`：Telegram 推送
- `DO_AI_ALERT_IDLE_SECS` / `DO_AI_ALERT_KEYWORDS` / `DO_AI_ALERT_COOLDOWN`：告警规则

## Android Shared View（Flutter）

移动端共享终端位于 `do_ai_terminal/`，支持 `Clean/Raw/Auto` 渲染模式：

- `DO_AI_MOBILE_RENDER_MODE=clean|raw|auto`（默认 `clean`）
- `DO_AI_MOBILE_NOISE_PROFILE=gemini|default`（默认 `gemini`）

真机验收命令：

```bash
# 单轮回归
tests/e2e/gemini_appsync_realgui_e2e.sh

# 严格 SLA（默认 20 轮）
DO_AI_E2E_ROUNDS=20 tests/e2e/gemini_appsync_sla_realgui.sh
```

## 构建

```bash
go mod init do-ai
go get github.com/creack/pty golang.org/x/term
go build -trimpath -ldflags "-s -w" -o do-ai ./src
```

跨平台产物示例：

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/do-ai-linux-amd64 ./src

# Windows amd64
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/do-ai-windows-amd64.exe ./src
```

## 一键安装（推荐）

### 本地安装脚本

```bash
./install.sh
```

### 远程一键安装（GitHub）

```bash
curl -fsSL https://github.com/junknet/do-ai/releases/latest/download/install.sh | bash
```

> 说明：通过 `curl | bash` 安装会自动下载源码归档并本地编译。

> 说明：当前一键安装/卸载脚本面向 Linux/macOS；Windows 建议直接使用 `dist/do-ai-windows-amd64.exe` 并加入 PATH（例如 `C:\tools`）。

## 卸载

```bash
./uninstall.sh
```

远程卸载：

```bash
curl -fsSL https://github.com/junknet/do-ai/releases/latest/download/uninstall.sh | bash
```

## 依赖

- Go >= 1.20
- github.com/creack/pty
- golang.org/x/term

## 平台支持

- ✅ Linux
- ✅ Windows（ConPTY）
