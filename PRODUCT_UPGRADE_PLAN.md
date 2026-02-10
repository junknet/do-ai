# 产品级终端方案 - 完整升级计划

**目标**：打造商业级、Termius 品质的终端应用

---

## 当前问题诊断

### 1. Android App 渲染问题
- **现状**：简单文本渲染 (`<Text>{line.text}</Text>`)
- **缺陷**：无法处理 xterm/ANSI 转义序列（光标、颜色、清屏、TUI）
- **影响**：运行 vim、htop 等 TUI 应用时显示混乱

### 2. do-ai 架构依赖
- **现状**：依赖 tmux 进行会话管理
- **优点**：会话持久化、多客户端共享、输出缓冲
- **缺点**：
  - 需要额外安装 tmux（非绿色）
  - 跨平台支持复杂（Windows 需要 WSL）
  - tmux 本身的性能和兼容性问题

---

## 产品级解决方案

### 方案 A：完整 xterm 引擎（推荐）

#### Android App 端

**选择 1：react-native-xterm.js（成熟）**
```bash
npm install react-native-xterm react-native-webview
```

优点：
- 完整的 xterm.js 引擎（100% VT100/xterm 兼容）
- 成熟稳定，广泛使用
- 支持所有 ANSI 特性（颜色、光标、TUI）
- 性能优化（Canvas/WebGL 渲染）

缺点：
- 基于 WebView，有一定性能开销
- 包体积增加 ~1MB

**选择 2：原生 xterm 引擎（最优，需开发）**
```bash
# 使用 libvterm 或自研
```

优点：
- 纯原生渲染，性能最优
- 完全控制，可深度优化
- 包体积小

缺点：
- 开发周期长（2-4 周）
- 需要维护 C++/Java/Kotlin 代码

#### do-ai 服务端（去 tmux 依赖）

**核心改造**：实现纯 Go 的会话管理器

```go
// 新架构
type SessionManager struct {
    sessions map[string]*Session
    mu       sync.RWMutex
}

type Session struct {
    ID         string
    PTY        *os.File        // 直接管理 PTY
    Buffer     *ScreenBuffer   // 实现类似 tmux 的缓冲
    Parser     *ANSIParser     // ANSI 解析器
    Clients    []*Client       // 多客户端支持
}

// 关键功能
1. PTY 直接管理（无 tmux）
2. 屏幕缓冲（circular buffer + screen state）
3. ANSI 解析（github.com/creack/pty + 自研 parser）
4. 多客户端广播
5. 会话持久化（可选：serialize 到磁盘）
```

**依赖库**：
- `github.com/creack/pty`：PTY 创建和管理
- `github.com/hinshun/vt10x`：VT100 终端模拟器（可选）
- 自研：ScreenBuffer + SessionManager

---

### 方案 B：轻量级方案（快速）

#### Android App 端

使用 `react-native-ansi-styled-text`：
- 支持 ANSI 颜色
- 不支持光标控制（TUI 仍有问题）
- 开发周期：1-2 天

#### do-ai 服务端

保持 tmux，但优化：
- 检测 tmux 是否可用
- 自动回退到直连 PTY
- 提供轻量级模式（无缓冲）

---

## 推荐实施路线

### 第一阶段：快速可用（1 周）
1. App：集成 react-native-xterm + WebView
2. 服务端：保持 tmux，优化回退逻辑
3. 测试：vim、htop、claude 等 TUI 应用

### 第二阶段：去 tmux 依赖（2-3 周）
1. 实现 Go SessionManager
2. 实现 ScreenBuffer（循环缓冲 + 快照）
3. 实现 ANSI Parser（基于 vt10x 或自研）
4. 多客户端广播机制
5. E2E 测试覆盖

### 第三阶段：性能优化（2 周）
1. App：考虑原生 xterm 引擎
2. 服务端：零拷贝优化、增量更新
3. 压力测试：大量输出、多会话并发
4. 内存和 CPU profiling

---

## 技术对比

| 特性 | 当前方案 | 方案 A（完整） | 方案 B（轻量） |
|:---|:---:|:---:|:---:|
| TUI 支持 | ❌ | ✅ | ⚠️ |
| ANSI 颜色 | ❌ | ✅ | ✅ |
| 光标控制 | ❌ | ✅ | ❌ |
| tmux 依赖 | ✅ | ❌ | ✅ |
| 包体积 | 小 | +1MB | +200KB |
| 开发周期 | - | 3-4 周 | 1 周 |
| 性能 | 低 | 高 | 中 |
| 商业级 | ❌ | ✅ | ⚠️ |

---

## 实施建议

**推荐路线**：方案 A（分阶段）

1. **立即**（1-2 天）：App 集成 react-native-xterm
2. **本周**（5 天）：完成 E2E 测试，解决 TUI 渲染问题
3. **下周开始**：并行开发 Go SessionManager（去 tmux）
4. **2 周后**：完整替换 tmux，纯 Go 实现
5. **1 个月内**：达到产品级质量

**投入**：
- 前端：2-3 天（WebView + xterm.js）
- 后端：10-15 天（SessionManager + ANSI Parser）
- 测试：5 天（E2E + 压力测试）
- **总计**：3-4 周全职开发

**产出**：
- ✅ 完整 TUI 支持（vim、htop、tmux 内嵌等）
- ✅ 去 tmux 依赖，单一二进制部署
- ✅ 商业级性能和稳定性
- ✅ Termius 级别的用户体验

---

*Generated: 2026-02-09*
