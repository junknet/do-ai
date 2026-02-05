# do-ai 安装与使用（客户化流程）

> 适用范围：Claude / Codex / Gemini TUI
> 
> 目标：**3 分钟无人输出自动注入**，保持 TUI 原样体验（前缀包裹，非 alias）。

---

## 1. 快速安装

### 1.1 构建

```bash
cd /home/junknet/Desktop/do-ai

go build -trimpath -ldflags "-s -w" -o do-ai ./src
```

### 1.2 一键安装（推荐）

```bash
./install.sh
```

远程一键安装：

```bash
curl -fsSL https://github.com/junknet/do-ai/releases/latest/download/install.sh | bash
```

> 说明：通过 `curl | bash` 安装会自动下载源码归档并本地编译。

### 1.3 验证可执行

```bash
./do-ai --help 2>/dev/null || true
```

### 1.4 卸载

```bash
./uninstall.sh
```

远程卸载：

```bash
curl -fsSL https://github.com/junknet/do-ai/releases/latest/download/uninstall.sh | bash
```

---

## 2. 使用方式（**只用前缀，不用 alias**）

### 2.1 Claude

```bash
./do-ai claude code
```

### 2.2 Codex

```bash
./do-ai codex
```

### 2.3 Gemini

```bash
./do-ai gemini
```

> 行为说明：只要连续 3 分钟没有“可见文本输出”，do-ai 自动注入（可通过 YAML 配置覆盖）：
> 
> **继续按当前计划推进，高ROI优先；如计划缺失，先快速补计划再执行；不新增范围，不重复提问。**

也可直接在命令行第一个参数传入空闲时间（更简洁）：

```bash
./do-ai 5s codex
./do-ai 5min 10s codex
./do-ai 2m30s codex
```

### 2.4 YAML 配置（可选）

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

---

## 3. 客户化流程（推荐使用节奏）

### 3.1 在场交互（人工主控）

1. 启动 TUI：`./do-ai claude code` / `./do-ai codex` / `./do-ai gemini`
2. 正常与 TUI 交互
3. 若无输出持续 3 分钟，do-ai 会自动注入“继续推进”指令

### 3.2 离开托管（无人值守）

1. 启动 TUI：`./do-ai codex`
2. 不做任何输入，离开即可
3. do-ai 每 3 分钟自动注入推进语句，持续推进（含周期性“校准提示”）

### 3.3 调试验证（仅需要时）

```bash
DO_AI_DEBUG=1 ./do-ai codex
```
触发注入时会在 stderr 打印：
```
[do-ai] 自动注入 YYYY-MM-DD HH:MM:SS
```

快速测试（10 秒触发）：

```bash
DO_AI_IDLE=10s ./do-ai codex
```

---

## 4. 设计要点（已客户化）

- **无 alias**：按你的要求，仅用前缀包裹
- **透明 TUI**：保留所有颜色、光标、布局和快捷键
- **Codex 兼容**：内置 DSR 回写，避免光标位置读取失败
- **刷屏不干扰**：忽略纯 ANSI 刷屏输出，保证 3 分钟 idle 能触发
- **自动校准**：默认每 5 次注入插入一次“计划/已完成清单”提示（`先输出当前计划(3-7条)和已完成清单，再继续执行下一条。`），可用 `DO_AI_CALIB_EVERY=0` 关闭或调整频率
- **自动提交**：Linux/macOS 默认 Enter/CR；Windows 默认 Enter+Ctrl-Enter，并补偿 CR 5 次（更稳）。可用 `DO_AI_SUBMIT=0` 关闭；可选 `DO_AI_SUBMIT_MODE=enter|enter-lf|ctrl-enter|alt-enter|enter+ctrl|enter+alt|all` 调整。
- **清理残留输入**：Windows 默认在每次注入前执行 `ctrl-u` 5 次清理输入行；Linux/macOS 默认关闭。

---

## 5. 常见问题

### Q1：为什么 3 分钟没有自动注入？
A：如果界面持续有“可见文本输出”，计时会重置。仅在**持续无可见文本输出**时触发。

### Q2：Codex 启动时报光标错误？
A：已加入 DSR 回写兼容，若仍异常，建议在本地真实终端运行。

### Q3：能否改成别的注入语句？
A：可以，修改 `src/main.go` 中 `autoMessageMain` / `autoMessageCalib`，重新编译。

---

## 6. 快速检查清单（交付给客户）

- [ ] 直接使用 `./do-ai <cli>` 前缀启动
- [ ] 3 分钟无输出自动注入成功
- [ ] Claude / Codex / Gemini 三端均可正常进入 TUI

---

## 平台支持

- ✅ Linux（当前仅提供 Linux 版本）
