# do-ai 当前版本已知风险 & 非目标问题（2026-02-08）

## 已知风险

1. **上游 CLI 行为差异风险（仍存在）**
   - `codex` / `claude` / `gemini` 在不同版本下对 Enter、Ctrl-Enter、CSI u 协议支持不一致。
   - 本版已对 `codex` 默认改为 `enter`（非 `enter+ctrl`），并保留 CR fallback；但无法保证未来上游版本完全一致。

2. **网络抖动导致线上 E2E 偶发超时**
   - `tests/e2e/relay_online_e2e.sh` 中曾出现一次 `curl timeout`，脚本最终重试后 `PASS`。
   - 说明线上链路可用，但公网质量波动会影响单次请求时延。

3. **终端控制序列噪声风险**
   - 在 TUI 程序中日志可见 ANSI/CSI 控制字符，排障时需结合 `DO_AI_DEBUG=1` 锚点日志阅读。

4. **极端短周期注入风险（已缓解）**
   - 在 `DO_AI_IDLE=1s` 压测下，`codex` 曾出现 TUI wrapping panic。
   - 已通过两项措施缓解：
     - wrap-sensitive 目标使用安全 ASCII 注入文本；
     - `codex/claude/gemini` 注入最小节流 `30s`。

4. **设备/环境依赖风险**
   - Android GUI 与 ADB 验证依赖在线设备或可用模拟器；无设备时无法完成当轮 GUI 证据采集。

## 非目标问题（当前版本）

1. **不保证对所有第三方 TUI 的“语义提交”适配**
   - 当前只保证通用 PTY 提交链路正确，非为单一厂商 TUI 做专属协议定制。

2. **不做浏览器/移动端全功能远控协议统一**
   - 本版本核心目标是命令行监工与 relay 提交可靠性，不扩展到完整远程桌面级交互。

3. **不覆盖离线全场景网络容灾策略**
   - 已具备基础重试与日志定位能力，但不在本轮引入复杂断网缓存回放机制。

## 关联证据

- 单测：`reports/enter_fix_unit_tests.log`
- 本地 auto-submit E2E：`reports/enter_fix_auto_submit_e2e.log`
- live codex 证据：`reports/cli_live_enter_proof.log`
- submit-only 边界：`reports/enter_submit_linux_proof3_output.json`
- soak 稳定性：`reports/cli_soak_consistency_summary_20260208.md`
