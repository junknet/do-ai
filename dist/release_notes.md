# do-ai v1.1.0

## 新功能

- **Windows 支持**: 使用 ConPTY API，支持 Windows Server 2019+ / Windows 10 1809+
- **跨平台架构**: 抽象 PTY 接口，平台无关的核心逻辑

## 改进

- 默认空闲时间改为 5 秒
- 注入消息增加交付提示

## 下载

| 平台 | 文件 |
|:---|:---|
| Linux x64 | `do-ai-linux-amd64` |
| Windows x64 | `do-ai-windows-amd64.exe` |

## 安装

**Linux (一键安装):**
```bash
curl -fsSL https://raw.githubusercontent.com/junknet/do-ai/main/install.sh | bash
```

**Windows:**
下载 `do-ai-windows-amd64.exe`，重命名为 `do-ai.exe`，放入 PATH 目录。
