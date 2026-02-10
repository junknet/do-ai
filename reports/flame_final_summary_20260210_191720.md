### Root Cause
- tmux TUI 场景存在 DCS passthrough（ESC Ptmux;...ESC \）; relay screen parser 之前把整段 DCS 当控制序列丢弃，导致正文缺失，仅剩状态栏/碎片（如 updates）。
- 旧会话（历史进程）因旧 do-ai 二进制上报方式限制，仍可能只见状态栏；新会话在修复后可恢复完整多行。

### Key Fixes
- src/relay.go: 新增 decodeTmuxPassthrough，并在 applyChunk 处理 ESC P 分支时解包 tmux payload 后递归重放到 screen state。
- src/relay_test.go: 新增 TestDecodeTmuxPassthrough 与 TestRelayStoreScreenSnapshotSupportsTmuxDCS。
- src/main.go/src/main_test.go: 先前已修 ESC(B/ESC)0 吞并，避免 B 泄露。
- app/android-rn/App.tsx: 保留状态栏-only 兜底提示与节流 Ctrl+L 重绘触发，避免纯黑屏。

### Evidence
- 修复后新会话 API: reports/flame_dcs_newsession_screen_*.json（line_count=36, 含多行正文/TUI 片段）。
- 修复后真机截图: reports/flame_final_terminal_20260210_191513.png（R68 • 32，多行正文可见）。
- 兜底场景截图: reports/flame_dcs_terminal_20260210_190736.png（状态栏-only 提示）。
- 校验日志: reports/flame_final_validation_20260210_191640.log
