---
title: do-ai：AI 监工，让 Agent 不摸鱼
author: GeekPwd
digest: AI Agent 空闲时自动注入提示，保持持续工作
---

## 什么是 do-ai？

**do-ai** 是一个轻量级的命令行工具，专为无人值守场景设计。它包裹你的 AI Agent（Claude Code、Codex、Gemini CLI），在检测到 Agent 空闲时自动注入预设提示，让 AI 持续工作。

## 核心特性

- **透明代理** — 不改变原有交互体验，所有输入输出原样透传
- **智能检测** — 通过分析终端输出判断 Agent 是否空闲
- **自动注入** — 空闲超过阈值自动发送预设提示词
- **跨平台** — 支持 Linux、macOS 和 Windows

## 快速开始

```bash
# 安装
curl -fsSL https://raw.githubusercontent.com/user/do-ai/main/install.sh | bash

# 使用：包裹 Claude Code
do-ai claude

# 使用：包裹 Codex
do-ai codex
```

## 配置示例

```yaml
idle: 3m
message_main: |
  继续执行当前任务，不要停下来等待确认。
message_calib: |
  检查当前进度，汇报完成情况。
```

## 为什么需要 do-ai？

AI Agent 在执行复杂任务时经常会：

1. **等待确认** — 每个步骤都暂停等待用户输入
2. **遇到歧义停下** — 不确定如何选择就停止工作
3. **完成子任务后空闲** — 忘记继续下一步

do-ai 解决了这些问题，让你可以安心离开，回来时任务已完成。

> 一行命令，解放双手。
