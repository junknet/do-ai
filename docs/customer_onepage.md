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
curl -fsSL https://raw.githubusercontent.com/junknet/do-ai/main/install.sh | bash
```

### 卸载

```bash
./uninstall.sh
```

远程卸载：

```bash
curl -fsSL https://raw.githubusercontent.com/junknet/do-ai/main/uninstall.sh | bash
```

---

## 2. 使用（仅前缀包裹）

```bash
./do-ai claude code
./do-ai codex
./do-ai gemini
```

**规则**：连续 3 分钟无可见输出 → 自动输入：

```
自主决策，按照业务需求高roi继续推进
```

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

---

## 5. 常见问题

**Q：为什么没触发？**
A：界面若持续有“可见文本输出”，计时会重置。只有真正 3 分钟静默才触发。

---

## 平台支持

- ✅ Linux（当前仅提供 Linux 版本）
