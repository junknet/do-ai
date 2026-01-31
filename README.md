# do-ai

一个“监工式”前缀工具：以 **透明 TUI 代理** 的方式启动 Claude/Codex/Gemini 等 CLI，当 **连续 3 分钟无输出** 时，自动输入：

```
继续按当前计划推进，高ROI优先；如计划缺失，先快速补计划再执行；不新增范围，不重复提问。
```

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
- 注入内容固定为：`继续按当前计划推进，高ROI优先；如计划缺失，先快速补计划再执行；不新增范围，不重复提问。`
- 每 5 次注入会插入一次“校准提示”（`先输出当前计划(3-7条)和已完成清单，再继续执行下一条。`），可用 `DO_AI_CALIB_EVERY=0` 关闭或调整频率
- 默认自动提交（Enter+Ctrl+Enter）。可用 `DO_AI_SUBMIT=0` 关闭；可选 `DO_AI_SUBMIT_MODE=enter|ctrl-enter|alt-enter|enter+ctrl|enter+alt|all` 调整。
- 可临时改成 10 秒触发（测试用）：`DO_AI_IDLE=10s do-ai codex`
- 不做提示词识别，不做语义判断，专注“无限继续”
- 内置 **DSR 兼容**：当终端未回传光标位置时，自动补发 `ESC[1;1R`，提升 Codex TUI 兼容性

## 构建

```bash
go mod init do-ai
go get github.com/creack/pty golang.org/x/term
go build -trimpath -ldflags "-s -w" -o do-ai ./src
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

- ✅ Linux（目前只提供 Linux 版本）
