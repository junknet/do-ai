package main

import (
	"bytes"
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
	defaultIdleTimeout    = 3 * time.Minute
	autoMessageMain       = "继续按当前计划推进，按照功能ROI高低去做；如计划缺失，先快速补计划再执行；不新增范围，不重复提问。更改的代码，推进的功能需要严格的真实场景数据的e2e测试，边界测试，禁止mock数据。如果可以交付就明确告知,然后暂停"
	autoMessageCalib      = "先输出当前计划(3-7条)和已完成清单，再继续执行下一条。"
	dsrRequest            = "\x1b[6n"
	dsrReply              = "\x1b[1;1R"
	dsrDelay              = 50 * time.Millisecond
	tailSize              = 32
	windowsPreInputRepeat = 5
	windowsSubmitRepeat   = 5
)

func main() {
	exitCode := 0
	defer func() { os.Exit(exitCode) }()

	args := os.Args[1:]
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

	if !isTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "错误: 需要在真实终端中运行")
		exitCode = 2
		return
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()

	debug := os.Getenv("DO_AI_DEBUG") == "1"

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
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in := buf[:n]
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
			exitCode = code
			return
		case <-ticker.C:
			now := time.Now()
			lastOutAt := time.Unix(0, atomic.LoadInt64(&lastOutput))
			lastKickAt := time.Unix(0, atomic.LoadInt64(&lastKick))
			sinceOut := now.Sub(lastOutAt)
			sinceKick := now.Sub(lastKickAt)

			if shouldKick(sinceOut, sinceKick, idle) {
				if pre := preInputPayload(); len(pre) > 0 {
					_, _ = ptmx.Write(pre)
				}
				_, _ = ptmx.Write(kickPayload(kickCount, calibEvery, messageMain, messageCalib))
				if debug {
					fmt.Fprintf(os.Stderr, "[do-ai] 自动注入 %s\n", time.Now().Format("2006-01-02 15:04:05"))
				}
				if submit := submitPayload(); len(submit) > 0 {
					time.AfterFunc(submitDelay, func() {
						n, err := ptmx.Write(submit)
						if debug {
							fmt.Fprintf(os.Stderr, "[do-ai] 发送提交键: %d bytes, err=%v, hex=%x\n", n, err, submit)
						}
					})
				}
				if fallback := submitFallbackPayload(); len(fallback) > 0 {
					startAt := time.Now()
					time.AfterFunc(fallbackDelay, func() {
						if time.Since(startAt) <= fallbackWindow {
							n, err := ptmx.Write(fallback)
							if debug {
								fmt.Fprintf(os.Stderr, "[do-ai] 发送补偿键: %d bytes, err=%v, hex=%x\n", n, err, fallback)
							}
						}
					})
				}
				kickCount++
				atomic.StoreInt64(&lastKick, time.Now().UnixNano())
			}
		}
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "用法: do-ai [idle] <command> [args...]")
	fmt.Fprintln(os.Stderr, "示例: do-ai 5s codex | do-ai 5min 10s codex | do-ai 2m30s codex")
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

func kickPayload(kickCount uint64, calibEvery int, mainMsg, calibMsg string) []byte {
	msg := mainMsg
	if calibEvery > 0 && (kickCount+1)%uint64(calibEvery) == 0 {
		if calibMsg != "" {
			msg = calibMsg
		}
	}
	return []byte(msg)
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

// 默认开启自动提交（Enter），可用 DO_AI_SUBMIT=0 关闭。
func submitPayload() []byte {
	if os.Getenv("DO_AI_SUBMIT") == "0" {
		return nil
	}
	mode := os.Getenv("DO_AI_SUBMIT_MODE")
	if mode == "" {
		mode = defaultSubmitMode()
	}
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

func defaultSubmitMode() string {
	if runtime.GOOS == "windows" {
		return "enter+ctrl"
	}
	return "enter"
}

func submitFallbackPayload() []byte {
	if runtime.GOOS == "windows" {
		return bytes.Repeat([]byte{'\r'}, windowsSubmitRepeat)
	}
	return nil
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
	if val == "" {
		return nil
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
	switch out[i+1] {
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
