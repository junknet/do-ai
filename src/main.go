package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

const (
	idleTimeout = 3 * time.Minute
	autoMessage = "自主决策，按照业务需求高roi继续推进"
	dsrRequest  = "\x1b[6n"
	dsrReply    = "\x1b[1;1R"
	dsrDelay    = 50 * time.Millisecond
	tailSize    = 32
	submitDelay = 80 * time.Millisecond
)

func main() {
	exitCode := 0
	defer func() { os.Exit(exitCode) }()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: do-ai <command> [args...]")
		exitCode = 2
		return
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "错误: 需要在真实终端中运行")
		exitCode = 2
		return
	}

	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Env = os.Environ()

	debug := os.Getenv("DO_AI_DEBUG") == "1"

	ptmx, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		exitCode = 1
		return
	}
	defer func() { _ = ptmx.Close() }()

	// 进入 Raw 模式，保证 TUI 完整透传
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法进入 raw 模式:", err)
		exitCode = 1
		return
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// 窗口大小变化同步
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	winch <- syscall.SIGWINCH

	// 输出与注入的时间戳
	lastOutput := time.Now().UnixNano()
	lastKick := int64(0)
	atomic.StoreInt64(&lastOutput, lastOutput)
	atomic.StoreInt64(&lastKick, lastKick)

	dsr := newDSRController(ptmx)

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
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-waitCh:
			exitCode = exitCodeFromErr(err)
			return
		case <-ticker.C:
			now := time.Now()
			lastOutAt := time.Unix(0, atomic.LoadInt64(&lastOutput))
			lastKickAt := time.Unix(0, atomic.LoadInt64(&lastKick))
			sinceOut := now.Sub(lastOutAt)
			sinceKick := now.Sub(lastKickAt)

			if shouldKick(sinceOut, sinceKick, idleTimeout) {
				_, _ = ptmx.Write(kickPayload())
				if debug {
					fmt.Fprintf(os.Stderr, "[do-ai] 自动注入 %s\n", time.Now().Format("2006-01-02 15:04:05"))
				}
				if submit := submitPayload(); len(submit) > 0 {
					time.AfterFunc(submitDelay, func() {
						_, _ = ptmx.Write(submit)
					})
				}
				atomic.StoreInt64(&lastKick, time.Now().UnixNano())
			}
		}
	}
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

func kickPayload() []byte {
	return []byte(autoMessage + "\n")
}

// 默认开启自动提交（Enter），可用 DO_AI_SUBMIT=0 关闭。
func submitPayload() []byte {
	if os.Getenv("DO_AI_SUBMIT") == "0" {
		return nil
	}
	mode := os.Getenv("DO_AI_SUBMIT_MODE")
	if mode == "" {
		mode = "enter"
	}
	switch mode {
	case "enter":
		return []byte("\r")
	case "ctrl-enter":
		return []byte("\x1b[13;5u")
	case "alt-enter":
		return []byte("\x1b\r")
	case "enter+ctrl":
		return []byte("\r\x1b[13;5u")
	case "enter+alt":
		return []byte("\r\x1b\r")
	case "all":
		return []byte("\r\x1b\r\x1b[13;5u")
	default:
		return []byte("\r")
	}
}

// 仅在输出包含“可见文本”时才刷新空闲计时，避免纯 ANSI 刷屏阻断无人值守。
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
	ptmx  *os.File
}

func newDSRController(ptmx *os.File) *dsrController {
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
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return 1
	}
	return status.ExitStatus()
}
