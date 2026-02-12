package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultIdleTimeout     = 3 * time.Minute
	autoMessageMain        = "继续执行，别停，别问。我当前离线（睡觉中）。你是【被监工的执行 Agent】，继续执行当前任务，无需等待确认，可以直接执行 shell。本次运行的生命线文件（sentinel）为：{LOCK_FILE}。【硬性约束（必须遵守）】1) 以下内容是行为约束而非建议，必须严格执行。2) 禁止新增需求、禁止扩大范围、禁止结构性重构、禁止“顺手优化”。3) 只允许工程事项：Bug/崩溃/正确性、可观测性（日志/监控）、测试缺口、阻塞级性能问题。4) 每个事项必须形成闭环：修改→验证→证据（可复现）。无证据不得宣称完成。【持续推进规则（不是只做一件）】持续循环：发现工程事项→按 ROI 排序→只推进当前 ROI 最高的一项→完成闭环并产出证据→重新评估 ROI→推进下一项。任一时刻只能推进一项，完成后必须重新排序。【产品级就绪标准（必须全部满足）】PR1 核心主流程端到端稳定运行，无阻断级错误或崩溃。PR2 关键路径具备可定位失败原因的日志/观测信息。PR3 至少执行过 1 个失败/异常/边界场景，并能通过证据定位。PR4 关键路径无未处理异常、崩溃或已知数据不一致问题。PR5 已写出当前版本【已知风险 & 非目标问题】清单。【停止与退出（硬停止）】当且仅当以上 PR 条件全部满足时，必须按顺序执行：1) 明确声明：当前状态 =【产品级就绪】。2) 输出：交付摘要 + 证据位置 + 已知风险 & 非目标问题。3) 立即执行 shell 删除生命线文件：rm -f \"{LOCK_FILE}\"（Windows PowerShell：Remove-Item -Force \"{LOCK_FILE}\"）。4) 删除完成后，立刻停止所有输出、分析和推进，结束运行。【注意】删除生命线文件之后，禁止再做任何补充、总结或顺手检查。"
	autoMessageCalib       = "输出目标+3–7条计划+已完成；按ROI重排并写出下一条唯一任务与预期证据。"
	dsrRequest             = "\x1b[6n"
	dsrReply               = "\x1b[1;1R"
	dsrDelay               = 50 * time.Millisecond
	tailSize               = 32
	windowsPreInputRepeat  = 5
	windowsSubmitRepeat    = 5
	nonWindowsSubmitRepeat = 1
	defaultLockFileName    = ".do-ai.lock"
	wrapSensitiveMinIdle   = 30 * time.Second
	relayTerminateSentinel = "__DO_AI_TERMINATE__"
)

var submitTargetCommand string

func main() {
	exitCode := 0
	defer func() { os.Exit(exitCode) }()

	args := os.Args[1:]
	if len(args) > 0 && strings.EqualFold(args[0], "relay") {
		exitCode = runRelayServer(args[1:])
		return
	}
	if len(args) > 0 && isHelpArg(args[0]) {
		printUsage()
		return
	}
	cfg, _ := loadConfig()
	messageMain := autoMessageMain
	messageCalib := autoMessageCalib
	if cfg.MessageMain != "" {
		messageMain = cfg.MessageMain
	}
	if cfg.MessageCalib != "" {
		messageCalib = cfg.MessageCalib
	}
	configIdle := defaultIdleTimeout
	if d, ok := parseIdleString(cfg.Idle); ok && d > 0 {
		configIdle = d
	}
	var idleOverride *time.Duration
	if d, consumed, ok := parseIdleArg(args); ok {
		idleOverride = &d
		args = args[consumed:]
	}

	if len(args) > 0 && isHelpArg(args[0]) {
		printUsage()
		return
	}
	if len(args) < 1 {
		printUsage()
		exitCode = 2
		return
	}
	submitTargetCommand = filepath.Base(args[0])
	rawArgs := append([]string(nil), args...)
	debug := os.Getenv("DO_AI_DEBUG") == "1"

	lockPath, err := lockFilePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误: 无法获取当前目录:", err)
		exitCode = 2
		return
	}
	if err := os.WriteFile(lockPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "错误: 无法创建生命线文件:", err)
		exitCode = 2
		return
	}
	messageMain = strings.ReplaceAll(messageMain, "{LOCK_FILE}", lockPath)
	messageCalib = strings.ReplaceAll(messageCalib, "{LOCK_FILE}", lockPath)

	if !isTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "错误: 需要在真实终端中运行")
		exitCode = 2
		return
	}

	execArgs, err := resolveExecutionArgs(rawArgs, debug)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		exitCode = 2
		return
	}
	cmd := exec.Command(execArgs[0], execArgs[1:]...)
	cmd.Env = normalizedRuntimeEnv(os.Environ(), rawArgs[0])

	reporter := newRelayReporter(rawArgs, lockPath, debug)
	startedAt := time.Now().Unix()
	var lastText atomic.Value
	lastText.Store("")

	ptmx, err := startPTY(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		exitCode = 1
		return
	}
	defer func() { _ = ptmx.Close() }()

	// 进入 Raw 模式，保证 TUI 完整透传
	oldState, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法进入 raw 模式:", err)
		exitCode = 1
		return
	}
	defer func() { _ = oldState.restore() }()

	// 窗口大小变化同步 (Unix: SIGWINCH, Windows: polling)
	_ = setupWinchHandler(ptmx)

	// 输出与注入的时间戳
	lastOutput := time.Now().UnixNano()
	lastKick := int64(0)
	atomic.StoreInt64(&lastOutput, lastOutput)
	atomic.StoreInt64(&lastKick, lastKick)

	dsr := newDSRController(ptmx.AsWriter())

	// 读取子进程输出 → 原样写回终端
	go func() {
		buf := make([]byte, 32*1024)
		var tail []byte
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				out := buf[:n]
				_, _ = os.Stdout.Write(out)
				nowTS := time.Now().Unix()
				reporter.ReportOutputChunk(out, nowTS, debug)
				if summary := summarizeOutputChunk(out); summary != "" {
					lastText.Store(summary)
				}
				if isMeaningfulOutput(out) {
					atomic.StoreInt64(&lastOutput, time.Now().UnixNano())
				}
				if containsDSRRequest(tail, out) {
					dsr.Request()
				}
				tail = updateTail(tail, out, tailSize)
			}
			if err != nil {
				return
			}
		}
	}()

	// 用户输入 → 透传给子进程
	go func() {
		buf := make([]byte, 1024)
		var tail []byte
		var prevInputByte byte
		hasPrevInputByte := false
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in := buf[:n]
				normalized, nextPrev, nextHasPrev, converted := normalizeStdinInput(in, prevInputByte, hasPrevInputByte)
				prevInputByte = nextPrev
				hasPrevInputByte = nextHasPrev
				in = normalized
				if debug && converted > 0 {
					fmt.Fprintf(os.Stderr, "[do-ai] stdin Enter 归一化: converted=%d bytes=%d\n", converted, len(in))
				}

				if hasDSRReply(tail, in) {
					dsr.Cancel()
				}
				tail = updateTail(tail, in, tailSize)
				_, _ = ptmx.Write(in)
			}
			if err != nil {
				return
			}
		}
	}()

	// 等待子进程结束
	waitCh := make(chan int, 1)
	go func() {
		code, _ := ptmx.Wait()
		waitCh <- code
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	kickCount := uint64(0)
	lockStopped := false
	calibEvery := calibEveryFromEnv()
	idle := idleFromEnv(configIdle)
	if idleOverride != nil {
		idle = *idleOverride
	}
	submitDelay := submitDelayFromEnv()
	fallbackDelay := fallbackDelayFromEnv()
	fallbackWindow := fallbackWindowFromEnv()

	for {
		select {
		case code := <-waitCh:
			reporter.ReportSync(relayHeartbeat{
				SessionID:    reporter.SessionID,
				SessionName:  reporter.SessionName,
				Prefix:       reporter.Prefix,
				Host:         reporter.Host,
				CWD:          reporter.CWD,
				Command:      reporter.Command,
				PID:          reporter.PID,
				State:        "exited",
				ExitCode:     &code,
				StartedAt:    startedAt,
				UpdatedAt:    time.Now().Unix(),
				LastOutputAt: time.Unix(0, atomic.LoadInt64(&lastOutput)).Unix(),
				LastKickAt:   time.Unix(0, atomic.LoadInt64(&lastKick)).Unix(),
				IdleSeconds:  int64(time.Since(time.Unix(0, atomic.LoadInt64(&lastOutput))).Seconds()),
				KickCount:    kickCount,
				LastText:     valueFromAtomicString(&lastText),
			}, debug)
			exitCode = code
			return
		case <-ticker.C:
			now := time.Now()
			lastOutAt := time.Unix(0, atomic.LoadInt64(&lastOutput))
			lastKickAt := time.Unix(0, atomic.LoadInt64(&lastKick))
			sinceOut := now.Sub(lastOutAt)
			sinceKick := now.Sub(lastKickAt)
			kickIdle := effectiveIdleThreshold(idle, submitTargetCommand)

			if !lockStopped && !fileExists(lockPath) {
				lockStopped = true
				if debug {
					fmt.Fprintf(os.Stderr, "[do-ai] 生命线文件不存在，停止注入: %s\n", lockPath)
				}
			}
			if !lockStopped && shouldKick(sinceOut, sinceKick, kickIdle) {
				submitMode := resolvedSubmitMode()
				target := submitTargetCommand
				payload := normalizedKickPayloadForTarget(kickPayload(kickCount, calibEvery, messageMain, messageCalib), target)
				if pre := preInputPayload(); len(pre) > 0 {
					_, _ = ptmx.Write(pre)
				}
				_, _ = injectInput(ptmx.AsWriter(), payload, target, debug)
				if debug {
					fmt.Fprintf(os.Stderr, "[do-ai] 自动注入 %s\n", time.Now().Format("2006-01-02 15:04:05"))
					if kickIdle != idle {
						fmt.Fprintf(os.Stderr, "[do-ai] 注入节流: idle=%s effective=%s target=%s\n", idle, kickIdle, target)
					}
					if len(payload) > 0 {
						preview := payload
						if len(preview) > 128 {
							preview = preview[:128]
						}
						fmt.Fprintf(os.Stderr, "[do-ai] 注入摘要: bytes=%d target=%s preview=%q\n", len(payload), target, string(preview))
					}
				}
				if submit := submitPayload(); len(submit) > 0 {
					time.AfterFunc(submitDelay, func() {
						n, err := ptmx.Write(submit)
						if debug {
							fmt.Fprintf(os.Stderr, "[do-ai] 发送提交键: %d bytes, err=%v, hex=%x, mode=%s, target=%s\n", n, err, submit, submitMode, target)
						}
					})
				}
				if fallback := submitFallbackPayload(); len(fallback) > 0 {
					startAt := time.Now()
					time.AfterFunc(fallbackDelay, func() {
						if time.Since(startAt) <= fallbackWindow {
							n, err := ptmx.Write(fallback)
							if debug {
								fmt.Fprintf(os.Stderr, "[do-ai] 发送补偿键: %d bytes, err=%v, hex=%x, mode=%s, target=%s\n", n, err, fallback, submitMode, target)
							}
						}
					})
				}
				kickCount++
				atomic.StoreInt64(&lastKick, time.Now().UnixNano())
			}

			reporter.Report(relayHeartbeat{
				SessionID:    reporter.SessionID,
				SessionName:  reporter.SessionName,
				Prefix:       reporter.Prefix,
				Host:         reporter.Host,
				CWD:          reporter.CWD,
				Command:      reporter.Command,
				PID:          reporter.PID,
				State:        "running",
				StartedAt:    startedAt,
				UpdatedAt:    now.Unix(),
				LastOutputAt: lastOutAt.Unix(),
				LastKickAt:   lastKickAt.Unix(),
				IdleSeconds:  int64(sinceOut.Seconds()),
				KickCount:    kickCount,
				LastText:     valueFromAtomicString(&lastText),
			}, debug)

			commands := reporter.PullCommands(debug)
			for _, command := range commands {
				if !shouldApplyRelayCommand(command) {
					continue
				}
				if relayCommandIsTerminate(command) {
					if debug {
						fmt.Fprintf(os.Stderr, "[CRITICAL] [do-ai] relay 终止指令: session=%s source=%s\n", command.SessionID, command.Source)
					}
					if err := ptmx.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
						if debug {
							fmt.Fprintf(os.Stderr, "[CRITICAL] [do-ai] relay 终止 PTY 失败: session=%s err=%v\n", command.SessionID, err)
						}
					}
					continue
				}
				if debug {
					fmt.Fprintf(os.Stderr, "[do-ai] relay 远程指令: session=%s submit=%v input=%q\n", command.SessionID, command.Submit, command.Input)
				}
				if command.Input != "" {
					_, _ = injectInput(ptmx.AsWriter(), []byte(command.Input), submitTargetCommand, debug)
				}
				if command.Submit {
					if submit := submitPayload(); len(submit) > 0 {
						submitMode := resolvedSubmitMode()
						if delay, needDelay := relayPrimarySubmitDelayForTarget(submitTargetCommand, command.Input); needDelay && delay > 0 {
							time.Sleep(delay)
							if debug {
								fmt.Fprintf(os.Stderr, "[do-ai] relay 提交延迟: session=%s delay=%s target=%s\n", command.SessionID, delay, submitTargetCommand)
							}
						}
						n, err := ptmx.Write(submit)
						if debug {
							fmt.Fprintf(os.Stderr, "[do-ai] relay 提交写入: session=%s bytes=%d err=%v hex=%x mode=%s\n", command.SessionID, n, err, submit, submitMode)
						}
					}
					if fallback := relaySubmitFallbackPayload(command.Input); len(fallback) > 0 {
						delay := relaySubmitDelayFromEnv()
						sessionID := command.SessionID
						time.AfterFunc(delay, func() {
							n, err := ptmx.Write(fallback)
							if debug {
								fmt.Fprintf(os.Stderr, "[do-ai] relay 补偿提交: session=%s bytes=%d err=%v hex=%x delay=%s\n", sessionID, n, err, fallback, delay)
							}
						})
					}
				}
			}
		}
	}
}

func lockFilePath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cwd, defaultLockFileName)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "用法: do-ai [idle] <command> [args...]")
	fmt.Fprintln(os.Stderr, "示例: do-ai 5s codex | do-ai 5min 10s codex | do-ai 2m30s codex")
	fmt.Fprintln(os.Stderr, "服务: do-ai relay --listen :8787")
}

func isHelpArg(arg string) bool {
	switch strings.TrimSpace(strings.ToLower(arg)) {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

type appConfig struct {
	Idle         string
	MessageMain  string
	MessageCalib string
}

func loadConfig() (appConfig, bool) {
	path, ok := findConfigPath()
	if !ok {
		return appConfig{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.Getenv("DO_AI_DEBUG") == "1" {
			fmt.Fprintln(os.Stderr, "配置文件读取失败:", err)
		}
		return appConfig{}, false
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		if os.Getenv("DO_AI_DEBUG") == "1" {
			fmt.Fprintln(os.Stderr, "配置文件解析失败:", err)
		}
		return appConfig{}, false
	}
	cfg := appConfig{
		Idle:         stringFromAny(raw["idle"]),
		MessageMain:  firstNonEmpty(stringFromAny(raw["message_main"]), stringFromAny(raw["message"])),
		MessageCalib: stringFromAny(raw["message_calib"]),
	}
	return cfg, true
}

func findConfigPath() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("DO_AI_CONFIG")); p != "" {
		if fileExists(p) {
			return p, true
		}
	}
	cwd, _ := os.Getwd()
	paths := []string{
		filepath.Join(cwd, "do-ai.yaml"),
		filepath.Join(cwd, "do-ai.yml"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".config", "do-ai", "config.yaml"),
			filepath.Join(home, ".config", "do-ai", "config.yml"),
			filepath.Join(home, ".do-ai.yaml"),
			filepath.Join(home, ".do-ai.yml"),
		)
	}
	for _, p := range paths {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func stringFromAny(val any) string {
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseIdleString(val string) (time.Duration, bool) {
	tokens := strings.Fields(val)
	if len(tokens) == 0 {
		return 0, false
	}
	if d, consumed, ok := parseIdleArg(tokens); ok && consumed == len(tokens) {
		return d, true
	}
	if d, ok := parseSingleDuration(val); ok {
		return d, true
	}
	return 0, false
}

// 解析命令行 idle 参数，支持一个或两个时间片段（如 "5min 10s"）。
func parseIdleArg(args []string) (time.Duration, int, bool) {
	if len(args) == 0 {
		return 0, 0, false
	}
	first, ok := parseSingleDuration(args[0])
	if !ok {
		return 0, 0, false
	}
	if len(args) > 1 {
		if second, ok2 := parseSingleDuration(args[1]); ok2 {
			return first + second, 2, true
		}
	}
	return first, 1, true
}

// 支持 5s/2m/1h/5min/5min10s/120（秒）等格式。
func parseSingleDuration(raw string) (time.Duration, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, false
	}
	lower := strings.ToLower(text)
	if isDigits(lower) {
		val, err := strconv.Atoi(lower)
		if err != nil || val < 0 {
			return 0, false
		}
		return time.Duration(val) * time.Second, true
	}
	norm := lower
	norm = strings.ReplaceAll(norm, "minutes", "m")
	norm = strings.ReplaceAll(norm, "minute", "m")
	norm = strings.ReplaceAll(norm, "mins", "m")
	norm = strings.ReplaceAll(norm, "min", "m")
	norm = strings.ReplaceAll(norm, "seconds", "s")
	norm = strings.ReplaceAll(norm, "second", "s")
	norm = strings.ReplaceAll(norm, "secs", "s")
	norm = strings.ReplaceAll(norm, "sec", "s")
	d, err := time.ParseDuration(norm)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func shouldKick(sinceOutput, sinceKick, idle time.Duration) bool {
	if idle <= 0 {
		return false
	}
	if sinceOutput < idle {
		return false
	}
	if sinceKick < idle {
		return false
	}
	return true
}

func effectiveIdleThreshold(idle time.Duration, target string) time.Duration {
	// 全部命令使用统一的空闲阈值，不做特殊处理
	return idle
}

func kickPayload(kickCount uint64, calibEvery int, mainMsg, calibMsg string) []byte {
	msg := mainMsg
	if calibEvery > 0 && (kickCount+1)%uint64(calibEvery) == 0 {
		if calibMsg != "" {
			msg = calibMsg
		}
	}
	return []byte(msg)
}

func normalizedKickPayloadForTarget(payload []byte, target string) []byte {
	// 全部使用完整版本消息，不做简化处理
	return payload
}

// --- 注入函数 ---

// throttledWrite 按 chunkSize 分块写入，块间 sleep chunkDelay，降低 TUI wrapping 压力。
func throttledWrite(w io.Writer, data []byte, chunkSize int, chunkDelay time.Duration) (int, error) {
	if chunkSize <= 0 {
		chunkSize = 64
	}
	if len(data) <= chunkSize {
		return w.Write(data)
	}
	total := 0
	for off := 0; off < len(data); off += chunkSize {
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		n, err := w.Write(data[off:end])
		total += n
		if err != nil {
			return total, err
		}
		if end < len(data) && chunkDelay > 0 {
			time.Sleep(chunkDelay)
		}
	}
	return total, nil
}

// bracketedPastePayload 包裹 bracketed paste 前后缀，让 TUI 框架视为单次粘贴事件。
func bracketedPastePayload(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	const prefix = "\x1b[200~"
	const suffix = "\x1b[201~"
	out := make([]byte, 0, len(prefix)+len(payload)+len(suffix))
	out = append(out, prefix...)
	out = append(out, payload...)
	out = append(out, suffix...)
	return out
}

// useBracketedPasteForTarget 根据环境变量和目标命令决定是否启用 bracketed paste。
func useBracketedPasteForTarget(target string) bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DO_AI_BRACKETED_PASTE")))
	switch mode {
	case "1", "on", "true":
		return true
	case "0", "off", "false":
		return false
	default: // auto
		name := strings.ToLower(strings.TrimSpace(target))
		switch name {
		case "codex", "claude", "gemini":
			return true
		default:
			return false
		}
	}
}

func injectChunkSizeFromEnv() int {
	val := strings.TrimSpace(os.Getenv("DO_AI_INJECT_CHUNK_SIZE"))
	if val == "" {
		return 64
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return 64
	}
	return n
}

func injectChunkDelayFromEnv() time.Duration {
	val := strings.TrimSpace(os.Getenv("DO_AI_INJECT_CHUNK_DELAY"))
	if val == "" {
		return 2 * time.Millisecond
	}
	d, err := time.ParseDuration(val)
	if err != nil || d < 0 {
		return 2 * time.Millisecond
	}
	return d
}

// injectInput 综合注入函数：可选 bracketed paste + 分块节流写入。
func injectInput(w io.Writer, payload []byte, target string, debug bool) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	bracketed := useBracketedPasteForTarget(target)
	data := payload
	if bracketed {
		data = bracketedPastePayload(payload)
	}
	chunkSize := injectChunkSizeFromEnv()
	chunkDelay := injectChunkDelayFromEnv()
	n, err := throttledWrite(w, data, chunkSize, chunkDelay)
	if debug {
		fmt.Fprintf(os.Stderr, "[do-ai] 注入写入: bytes=%d bracketed=%v chunk=%d delay=%s err=%v\n", n, bracketed, chunkSize, chunkDelay, err)
	}
	return n, err
}

func resolveExecutionArgs(args []string, debug bool) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("命令为空")
	}
	return append([]string(nil), args...), nil
}

func envFlagEnabled(key string) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch val {
	case "1", "true", "yes", "on", "enabled", "force":
		return true
	default:
		return false
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return item[len(prefix):]
		}
	}
	return ""
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	next := append([]string(nil), env...)
	for i, item := range next {
		if strings.HasPrefix(item, prefix) {
			next[i] = prefix + value
			return next
		}
	}
	return append(next, prefix+value)
}

func removeEnvKey(env []string, key string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		next = append(next, item)
	}
	return next
}

func preferredTermFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("DO_AI_TERM")); v != "" {
		return v
	}
	return "xterm-256color"
}

func preferredColorTermFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("DO_AI_COLORTERM")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("COLORTERM")); v != "" {
		return v
	}
	return "truecolor"
}

func isColorSensitiveCommand(command string) bool {
	name := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	switch name {
	case "codex", "claude", "gemini":
		return true
	default:
		return false
	}
}

func normalizedRuntimeEnv(env []string, command string) []string {
	next := append([]string(nil), env...)
	desiredTerm := preferredTermFromEnv()
	desiredColor := preferredColorTermFromEnv()
	term := strings.TrimSpace(envValue(next, "TERM"))
	colorSensitive := isColorSensitiveCommand(command)

	if colorSensitive {
		next = setEnvValue(next, "TERM", desiredTerm)
	} else if term == "" || strings.EqualFold(term, "dumb") || strings.EqualFold(term, "ansi") {
		next = setEnvValue(next, "TERM", desiredTerm)
	}

	if strings.TrimSpace(envValue(next, "COLORTERM")) == "" {
		next = setEnvValue(next, "COLORTERM", desiredColor)
	}

	if colorSensitive && !envFlagEnabled("DO_AI_KEEP_NO_COLOR") {
		next = removeEnvKey(next, "NO_COLOR")
	}

	if envFlagEnabled("DO_AI_FORCE_COLOR") {
		next = setEnvValue(next, "CLICOLOR", "1")
		next = setEnvValue(next, "CLICOLOR_FORCE", "1")
		next = setEnvValue(next, "FORCE_COLOR", "1")
	}

	return next
}

func runeCount(text string) int {
	count := 0
	for range text {
		count++
	}
	return count
}

func wrapStringByRunes(text string, width int) []string {
	if width <= 0 || text == "" {
		return []string{text}
	}
	runes := []rune(text)
	out := make([]string, 0, len(runes)/width+1)
	for start := 0; start < len(runes); start += width {
		end := start + width
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}

func truncateByRunes(text string, maxRunes int) string {
	if maxRunes <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}

func asciiSafeText(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	lastSpace := false
	for _, r := range text {
		if r == '\n' || r == '\t' || (r >= 0x20 && r <= 0x7e) {
			if r == '\t' {
				r = ' '
			}
			if r == ' ' {
				if lastSpace {
					continue
				}
				lastSpace = true
			} else {
				lastSpace = false
			}
			b.WriteRune(r)
			continue
		}
		if !lastSpace {
			b.WriteRune(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func calibEveryFromEnv() int {
	val := os.Getenv("DO_AI_CALIB_EVERY")
	if val == "" {
		return 5
	}
	if val == "0" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return 5
	}
	return n
}

func idleFromEnv(defaultIdle time.Duration) time.Duration {
	val := os.Getenv("DO_AI_IDLE")
	if val == "" {
		return defaultIdle
	}
	if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if d, err := time.ParseDuration(val); err == nil && d > 0 {
		return d
	}
	return defaultIdle
}

func shouldApplyRelayCommand(command relayControlCommand) bool {
	if command.Action == relayActionTerminate {
		return true
	}
	if command.Submit {
		return true
	}
	return command.Input != ""
}

func relayCommandIsTerminate(command relayControlCommand) bool {
	action, ok := normalizeControlAction(command.Action)
	if !ok {
		action = ""
	}
	if action == relayActionTerminate {
		return true
	}
	input := strings.TrimSpace(command.Input)
	return input == relayTerminateSentinel
}

// 默认开启自动提交（Enter），可用 DO_AI_SUBMIT=0 关闭。
func submitPayload() []byte {
	if os.Getenv("DO_AI_SUBMIT") == "0" {
		return nil
	}
	mode := resolvedSubmitMode()
	switch mode {
	case "enter":
		return []byte("\r")
	case "enter-lf":
		return []byte("\r\n")
	case "lf":
		return []byte("\n")
	case "cr":
		return []byte("\r")
	case "ctrl-enter":
		return []byte("\x1b[13;5u")
	case "csi-enter":
		return []byte("\x1b[13;1u") // CSI u Enter (kitty protocol)
	case "alt-enter":
		return []byte("\x1b\r")
	case "enter+ctrl":
		return []byte("\r\x1b[13;5u")
	case "enter+alt":
		return []byte("\r\x1b\r")
	case "all":
		return []byte("\r\x1b[13;5u\x1b\r")
	default:
		return []byte("\r")
	}
}

func resolvedSubmitMode() string {
	mode := strings.TrimSpace(os.Getenv("DO_AI_SUBMIT_MODE"))
	if mode == "" {
		mode = defaultSubmitMode()
	}
	return mode
}

func defaultSubmitMode() string {
	if mode, ok := preferredSubmitModeForCommand(submitTargetCommand); ok {
		return mode
	}
	if runtime.GOOS == "windows" {
		return "enter+ctrl"
	}
	return "enter"
}

func preferredSubmitModeForCommand(command string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(command))
	switch name {
	case "codex":
		return "enter", true
	case "claude", "gemini":
		return "enter+ctrl", true
	default:
		return "", false
	}
}

func submitFallbackPayload() []byte {
	if os.Getenv("DO_AI_SUBMIT_FALLBACK") == "0" {
		return nil
	}
	repeat := submitFallbackRepeatFromEnv()
	if repeat <= 0 {
		return nil
	}
	return bytes.Repeat([]byte{'\r'}, repeat)
}

func submitFallbackRepeatFromEnv() int {
	val := strings.TrimSpace(os.Getenv("DO_AI_SUBMIT_FALLBACK_REPEAT"))
	if val != "" {
		n, err := strconv.Atoi(val)
		if err == nil {
			if n < 0 {
				return 0
			}
			if n > 12 {
				return 12
			}
			return n
		}
	}
	if runtime.GOOS == "windows" {
		return windowsSubmitRepeat
	}
	return nonWindowsSubmitRepeat
}

// relay 远程输入默认追加一次 CR 补偿，降低“只换行不执行”的概率。
// DO_AI_RELAY_SUBMIT_FALLBACK=0 可关闭；DO_AI_RELAY_SUBMIT_REPEAT 可调整次数。
// 对 codex 默认关闭该补偿（可通过 DO_AI_RELAY_SUBMIT_FALLBACK_FORCE=1 强制开启），
// 避免特定 TUI 场景下重复提交导致进程崩溃。
func relaySubmitFallbackPayload(input string) []byte {
	if input == "" {
		return nil
	}
	if os.Getenv("DO_AI_RELAY_SUBMIT_FALLBACK") == "0" {
		return nil
	}
	if shouldDisableRelaySubmitFallbackForTarget(submitTargetCommand) && os.Getenv("DO_AI_RELAY_SUBMIT_FALLBACK_FORCE") != "1" {
		return nil
	}
	repeat := relaySubmitRepeatFromEnv()
	if repeat <= 0 {
		return nil
	}
	return bytes.Repeat([]byte{'\r'}, repeat)
}

func shouldDisableRelaySubmitFallbackForTarget(command string) bool {
	name := strings.ToLower(strings.TrimSpace(command))
	switch name {
	case "codex":
		return true
	default:
		return false
	}
}

func relayPrimarySubmitDelayForTarget(command, input string) (time.Duration, bool) {
	if strings.TrimSpace(input) == "" {
		return 0, false
	}
	name := strings.ToLower(strings.TrimSpace(command))
	switch name {
	case "codex":
		return relaySubmitDelayFromEnv(), true
	default:
		return 0, false
	}
}

func relaySubmitRepeatFromEnv() int {
	val := strings.TrimSpace(os.Getenv("DO_AI_RELAY_SUBMIT_REPEAT"))
	if val == "" {
		return 1
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 1
	}
	if n < 0 {
		return 0
	}
	if n > 8 {
		return 8
	}
	return n
}

// 可选：在注入前发送清理序列，处理输入残留。
// DO_AI_PRE_INPUT 支持：ctrl-u | ctrl-a-ctrl-k | esc-2k | backspace:N | bs:N
func preInputPayload() []byte {
	val := strings.TrimSpace(os.Getenv("DO_AI_PRE_INPUT"))
	if val == "" {
		if runtime.GOOS == "windows" {
			return bytes.Repeat([]byte{0x15}, windowsPreInputRepeat)
		} else {
			return nil
		}
	}
	lower := strings.ToLower(val)
	switch lower {
	case "ctrl-u":
		return []byte{0x15}
	case "ctrl-a-ctrl-k":
		return []byte{0x01, 0x0b}
	case "esc-2k":
		return []byte("\x1b[2K")
	}
	if strings.HasPrefix(lower, "backspace:") || strings.HasPrefix(lower, "bs:") {
		parts := strings.SplitN(lower, ":", 2)
		if len(parts) != 2 {
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || n <= 0 {
			return nil
		}
		if n > 2000 {
			n = 2000
		}
		return bytes.Repeat([]byte{0x08}, n)
	}
	return nil
}

// 仅在输出包含"可见文本"时才刷新空闲计时，避免纯 ANSI 刷屏阻断无人值守。
func isMeaningfulOutput(out []byte) bool {
	for i := 0; i < len(out); {
		if out[i] == 0x1b {
			i = skipANSIEscape(out, i)
			continue
		}
		if out[i] < 0x20 || out[i] == 0x7f {
			i++
			continue
		}
		if out[i] != ' ' && out[i] != '\t' {
			return true
		}
		i++
	}
	return false
}

func skipANSIEscape(out []byte, i int) int {
	if i+1 >= len(out) {
		return i + 1
	}
	next := out[i+1]
	switch next {
	case '[': // CSI: ESC [ ... final byte 0x40-0x7e
		i += 2
		for i < len(out) && (out[i] < 0x40 || out[i] > 0x7e) {
			i++
		}
		if i < len(out) {
			i++
		}
		return i
	case ']': // OSC: ESC ] ... BEL or ESC \
		i += 2
		for i < len(out) {
			if out[i] == 0x07 {
				return i + 1
			}
			if out[i] == 0x1b && i+1 < len(out) && out[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return i
	case 'P', '^', '_': // DCS / PM / APC: ESC P ... ESC \
		i += 2
		for i < len(out) {
			if out[i] == 0x1b && i+1 < len(out) && out[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return i
	case '(', ')', '*', '+', '-', '.', '/':
		// 字符集选择序列：ESC ( B / ESC ) 0 等。
		// 若只吞 2 字节会把末字节泄露为正文（典型为孤立 B）。
		if i+2 < len(out) {
			return i + 3
		}
		return len(out)
	case '%', '#':
		// 其他常见 3 字节扩展 ESC 序列。
		if i+2 < len(out) {
			return i + 3
		}
		return len(out)
	default:
		return i + 2
	}
}

type dsrController struct {
	mu    sync.Mutex
	timer *time.Timer
	ptmx  io.Writer
}

func newDSRController(ptmx io.Writer) *dsrController {
	return &dsrController{ptmx: ptmx}
}

func (d *dsrController) Request() {
	d.mu.Lock()
	if d.timer != nil {
		d.mu.Unlock()
		return
	}
	d.timer = time.AfterFunc(dsrDelay, func() {
		d.mu.Lock()
		d.timer = nil
		d.mu.Unlock()
		_, _ = d.ptmx.Write([]byte(dsrReply))
	})
	d.mu.Unlock()
}

func (d *dsrController) Cancel() {
	d.mu.Lock()
	if d.timer != nil {
		_ = d.timer.Stop()
		d.timer = nil
	}
	d.mu.Unlock()
}

func containsDSRRequest(tail, chunk []byte) bool {
	if len(tail) == 0 {
		return bytes.Contains(chunk, []byte(dsrRequest))
	}
	combined := append(append([]byte{}, tail...), chunk...)
	return bytes.Contains(combined, []byte(dsrRequest))
}

func hasDSRReply(tail, chunk []byte) bool {
	combined := append(append([]byte{}, tail...), chunk...)
	for i := 0; i+3 < len(combined); i++ {
		if combined[i] != 0x1b || combined[i+1] != '[' {
			continue
		}
		j := i + 2
		if j >= len(combined) || combined[j] < '0' || combined[j] > '9' {
			continue
		}
		for j < len(combined) && combined[j] >= '0' && combined[j] <= '9' {
			j++
		}
		if j >= len(combined) || combined[j] != ';' {
			continue
		}
		j++
		if j >= len(combined) || combined[j] < '0' || combined[j] > '9' {
			continue
		}
		for j < len(combined) && combined[j] >= '0' && combined[j] <= '9' {
			j++
		}
		if j < len(combined) && combined[j] == 'R' {
			return true
		}
	}
	return false
}

func normalizeStdinInput(chunk []byte, prev byte, hasPrev bool) ([]byte, byte, bool, int) {
	if len(chunk) == 0 {
		return chunk, prev, hasPrev, 0
	}
	if strings.TrimSpace(os.Getenv("DO_AI_STDIN_LF_AS_CR")) == "0" {
		return chunk, chunk[len(chunk)-1], true, 0
	}

	converted := 0
	var out []byte
	for i, b := range chunk {
		if b != '\n' {
			continue
		}
		previousIsCR := false
		if i > 0 {
			previousIsCR = chunk[i-1] == '\r'
		} else if hasPrev {
			previousIsCR = prev == '\r'
		}
		if previousIsCR {
			continue
		}
		if out == nil {
			out = append([]byte{}, chunk...)
		}
		out[i] = '\r'
		converted++
	}
	if out == nil {
		out = chunk
	}
	return out, out[len(out)-1], true, converted
}

func updateTail(tail, chunk []byte, max int) []byte {
	if max <= 0 {
		return nil
	}
	if len(chunk) >= max {
		return append([]byte{}, chunk[len(chunk)-max:]...)
	}
	if len(tail)+len(chunk) <= max {
		out := make([]byte, 0, len(tail)+len(chunk))
		out = append(out, tail...)
		out = append(out, chunk...)
		return out
	}
	combined := append(append([]byte{}, tail...), chunk...)
	return append([]byte{}, combined[len(combined)-max:]...)
}

func splitOutputLines(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	clean := stripANSIAndControl(out)
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "")
	rawLines := strings.Split(clean, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 1024 {
			trimmed = trimmed[:1024]
		}
		lines = append(lines, trimmed)
	}
	return lines
}

func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return 1
	}
	return exitErr.ExitCode()
}

func submitDelayFromEnv() time.Duration {
	val := os.Getenv("DO_AI_SUBMIT_DELAY")
	if val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return 200 * time.Millisecond
}

// 仅用于补偿提交，默认值不对外暴露，避免增加心智负担。
func fallbackDelayFromEnv() time.Duration {
	return 300 * time.Millisecond
}

func fallbackWindowFromEnv() time.Duration {
	return 2 * time.Second
}

func relaySubmitDelayFromEnv() time.Duration {
	val := strings.TrimSpace(os.Getenv("DO_AI_RELAY_SUBMIT_DELAY"))
	if val != "" {
		if d, err := time.ParseDuration(val); err == nil && d >= 0 {
			return d
		}
	}
	return 120 * time.Millisecond
}
