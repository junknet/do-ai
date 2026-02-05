# do-ai 一页使用说明（客户交付版）

> 目的：TUI 无人值守，**3 分钟无输出自动推进**

---

## 1. 安装

```bash
cd /home/junknet/Desktop/do-ai

go build -trimpath -ldflags "-s -w" -o do-ai ./src
```

### 一键安装（推荐）

```bash
./install.sh
```

远程一键安装：

```bash
curl -fsSL https://github.com/junknet/do-ai/releases/latest/download/install.sh | bash
```

> 说明：通过 `curl | bash` 安装会自动下载源码归档并本地编译。

### 卸载

```bash
./uninstall.sh
```

远程卸载：

```bash
curl -fsSL https://github.com/junknet/do-ai/releases/latest/download/uninstall.sh | bash
```

---

## 2. 使用（仅前缀包裹）

```bash
./do-ai claude code
./do-ai codex
./do-ai gemini
```

也可直接在命令行第一个参数传入空闲时间（更简洁）：

```bash
./do-ai 5s codex
./do-ai 5min 10s codex
./do-ai 2m30s codex
```

**规则**：连续 3 分钟无可见输出 → 自动输入（可通过 YAML 配置覆盖）：

```
继续按当前计划推进，高ROI优先；如计划缺失，先快速补计划再执行；不新增范围，不重复提问。
```

并且每 5 次注入会插入一次“校准提示”：

```
先输出当前计划(3-7条)和已完成清单，再继续执行下一条。
```

## 2.1 YAML 配置（可选）

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

## 3. 推荐流程

**在场交互**
1) 用上面命令进入 TUI
2) 正常交互即可

**离开托管**
1) 启动 TUI 后离开
2) do-ai 每 3 分钟自动推进

---

## 4. 验证（可选）

```bash
DO_AI_DEBUG=1 ./do-ai codex
```
触发注入会打印：
```
[do-ai] 自动注入 YYYY-MM-DD HH:MM:SS
```

快速测试（10 秒触发）：

```bash
DO_AI_IDLE=10s ./do-ai codex
```

> 默认自动提交：Linux/macOS 为 Enter/CR；Windows 为 Enter+Ctrl-Enter，并补偿 CR 5 次（更稳）。如需关闭：`DO_AI_SUBMIT=0`；可选 `DO_AI_SUBMIT_MODE=enter|enter-lf|ctrl-enter|alt-enter|enter+ctrl|enter+alt|all` 调整。
> 清理残留输入：Windows 默认在每次注入前执行 `ctrl-u` 5 次清理输入行；Linux/macOS 默认关闭。

---

## 5. 常见问题

**Q：为什么没触发？**
A：界面若持续有“可见文本输出”，计时会重置。只有真正 3 分钟静默才触发。

---

## 平台支持

- ✅ Linux（当前仅提供 Linux 版本）
