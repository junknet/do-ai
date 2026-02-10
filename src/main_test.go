package main

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestShouldKick(t *testing.T) {
	idle := 3 * time.Minute

	if shouldKick(2*time.Minute, 4*time.Minute, idle) {
		t.Fatalf("未达到 idle 时间不应触发")
	}

	if shouldKick(4*time.Minute, 2*time.Minute, idle) {
		t.Fatalf("距离上次注入过短不应触发")
	}

	if !shouldKick(4*time.Minute, 4*time.Minute, idle) {
		t.Fatalf("达到 idle 时间应触发")
	}
}

func TestParseSingleDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"5s", 5 * time.Second, true},
		{"10", 10 * time.Second, true},
		{"2m", 2 * time.Minute, true},
		{"1h", time.Hour, true},
		{"5min", 5 * time.Minute, true},
		{"5min10s", 5*time.Minute + 10*time.Second, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSingleDuration(c.in)
		if ok != c.ok {
			t.Fatalf("parseSingleDuration(%q) ok=%v want=%v", c.in, ok, c.ok)
		}
		if ok && got != c.want {
			t.Fatalf("parseSingleDuration(%q)=%v want=%v", c.in, got, c.want)
		}
	}
}

func TestParseIdleString(t *testing.T) {
	d, ok := parseIdleString("5min 10s")
	if !ok || d != 5*time.Minute+10*time.Second {
		t.Fatalf("parseIdleString 失败: d=%v ok=%v", d, ok)
	}
	d, ok = parseIdleString("2m30s")
	if !ok || d != 2*time.Minute+30*time.Second {
		t.Fatalf("parseIdleString 失败: d=%v ok=%v", d, ok)
	}
	d, ok = parseIdleString("120")
	if !ok || d != 120*time.Second {
		t.Fatalf("parseIdleString 失败: d=%v ok=%v", d, ok)
	}
	_, ok = parseIdleString("")
	if ok {
		t.Fatalf("空字符串不应解析")
	}
}

func TestParseIdleArg(t *testing.T) {
	d, consumed, ok := parseIdleArg([]string{"5s", "codex"})
	if !ok || consumed != 1 || d != 5*time.Second {
		t.Fatalf("单参数解析失败: d=%v consumed=%d ok=%v", d, consumed, ok)
	}
	d, consumed, ok = parseIdleArg([]string{"5min", "10s", "codex"})
	if !ok || consumed != 2 || d != 5*time.Minute+10*time.Second {
		t.Fatalf("双参数解析失败: d=%v consumed=%d ok=%v", d, consumed, ok)
	}
	_, consumed, ok = parseIdleArg([]string{"codex"})
	if ok || consumed != 0 {
		t.Fatalf("应当不解析普通命令")
	}
}

func TestIsHelpArg(t *testing.T) {
	if !isHelpArg("-h") || !isHelpArg("--help") || !isHelpArg("help") {
		t.Fatalf("应当识别帮助参数")
	}
	if isHelpArg("codex") || isHelpArg("5s") {
		t.Fatalf("不应把普通参数当成帮助")
	}
}

func TestPreInputPayload(t *testing.T) {
	_ = os.Unsetenv("DO_AI_PRE_INPUT")
	if runtime.GOOS == "windows" {
		if got := preInputPayload(); len(got) != windowsPreInputRepeat || got[0] != 0x15 {
			t.Fatalf("Windows 默认应发送 ctrl-u x%d", windowsPreInputRepeat)
		}
	} else if len(preInputPayload()) != 0 {
		t.Fatalf("未设置时不应返回清理序列")
	}
	_ = os.Setenv("DO_AI_PRE_INPUT", "ctrl-u")
	if got := preInputPayload(); len(got) != 1 || got[0] != 0x15 {
		t.Fatalf("ctrl-u 不匹配")
	}
	_ = os.Setenv("DO_AI_PRE_INPUT", "ctrl-a-ctrl-k")
	if got := preInputPayload(); len(got) != 2 || got[0] != 0x01 || got[1] != 0x0b {
		t.Fatalf("ctrl-a-ctrl-k 不匹配")
	}
	_ = os.Setenv("DO_AI_PRE_INPUT", "esc-2k")
	if got := preInputPayload(); string(got) != "\x1b[2K" {
		t.Fatalf("esc-2k 不匹配")
	}
	_ = os.Setenv("DO_AI_PRE_INPUT", "backspace:3")
	if got := preInputPayload(); len(got) != 3 || got[0] != 0x08 {
		t.Fatalf("backspace:3 不匹配")
	}
}

func TestSubmitFallbackPayload(t *testing.T) {
	oldFallback := os.Getenv("DO_AI_SUBMIT_FALLBACK")
	oldRepeat := os.Getenv("DO_AI_SUBMIT_FALLBACK_REPEAT")
	defer func() {
		if oldFallback == "" {
			_ = os.Unsetenv("DO_AI_SUBMIT_FALLBACK")
		} else {
			_ = os.Setenv("DO_AI_SUBMIT_FALLBACK", oldFallback)
		}
		if oldRepeat == "" {
			_ = os.Unsetenv("DO_AI_SUBMIT_FALLBACK_REPEAT")
		} else {
			_ = os.Setenv("DO_AI_SUBMIT_FALLBACK_REPEAT", oldRepeat)
		}
	}()

	_ = os.Unsetenv("DO_AI_SUBMIT_FALLBACK")
	_ = os.Unsetenv("DO_AI_SUBMIT_FALLBACK_REPEAT")

	if runtime.GOOS == "windows" {
		if got := submitFallbackPayload(); len(got) != windowsSubmitRepeat || got[0] != '\r' {
			t.Fatalf("Windows 默认补偿键应为 CR x%d", windowsSubmitRepeat)
		}
	} else {
		if got := submitFallbackPayload(); len(got) != nonWindowsSubmitRepeat || got[0] != '\r' {
			t.Fatalf("非 Windows 默认补偿键应为 CR x%d", nonWindowsSubmitRepeat)
		}
	}

	_ = os.Setenv("DO_AI_SUBMIT_FALLBACK", "0")
	if got := submitFallbackPayload(); len(got) != 0 {
		t.Fatalf("关闭 submit fallback 后不应发送补偿键")
	}

	_ = os.Unsetenv("DO_AI_SUBMIT_FALLBACK")
	_ = os.Setenv("DO_AI_SUBMIT_FALLBACK_REPEAT", "2")
	if got := submitFallbackPayload(); len(got) != 2 || got[0] != '\r' {
		t.Fatalf("repeat=2 时应补偿 CR x2")
	}
}

func TestKickPayload(t *testing.T) {
	got := string(kickPayload(0, 0, autoMessageMain, autoMessageCalib))
	want := autoMessageMain
	if got != want {
		t.Fatalf("默认注入内容不匹配: got=%q want=%q", got, want)
	}

	calib := string(kickPayload(4, 5, autoMessageMain, autoMessageCalib))
	if calib != autoMessageCalib {
		t.Fatalf("校准注入内容不匹配: got=%q want=%q", calib, autoMessageCalib)
	}
}

func TestContainsDSRRequest(t *testing.T) {
	req := []byte(dsrRequest)
	if !containsDSRRequest(nil, req) {
		t.Fatalf("应当识别 DSR 请求")
	}
	if containsDSRRequest(nil, []byte("nope")) {
		t.Fatalf("不应误判 DSR 请求")
	}
	// 跨分片
	tail := []byte("\x1b[")
	chunk := []byte("6n")
	if !containsDSRRequest(tail, chunk) {
		t.Fatalf("应当识别跨分片 DSR 请求")
	}
}

func TestHasDSRReply(t *testing.T) {
	if !hasDSRReply(nil, []byte("\x1b[12;34R")) {
		t.Fatalf("应当识别 DSR 回复")
	}
	if hasDSRReply(nil, []byte("\x1b[A")) {
		t.Fatalf("不应把方向键当成 DSR 回复")
	}
	// 跨分片
	tail := []byte("\x1b[12;")
	chunk := []byte("34R")
	if !hasDSRReply(tail, chunk) {
		t.Fatalf("应当识别跨分片 DSR 回复")
	}
}

func TestNormalizeStdinInput(t *testing.T) {
	old := os.Getenv("DO_AI_STDIN_LF_AS_CR")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_STDIN_LF_AS_CR")
		} else {
			_ = os.Setenv("DO_AI_STDIN_LF_AS_CR", old)
		}
	}()

	_ = os.Unsetenv("DO_AI_STDIN_LF_AS_CR")

	out, prev, hasPrev, converted := normalizeStdinInput([]byte("A\n"), 0, false)
	if string(out) != "A\r" || converted != 1 || prev != '\r' || !hasPrev {
		t.Fatalf("单分片 LF 归一失败: out=%q converted=%d prev=%q hasPrev=%v", string(out), converted, prev, hasPrev)
	}

	out, prev, hasPrev, converted = normalizeStdinInput([]byte("\r\n"), 0, false)
	if string(out) != "\r\n" || converted != 0 || prev != '\n' || !hasPrev {
		t.Fatalf("CRLF 不应被二次归一: out=%q converted=%d prev=%q hasPrev=%v", string(out), converted, prev, hasPrev)
	}

	out, prev, hasPrev, converted = normalizeStdinInput([]byte("\n"), '\r', true)
	if string(out) != "\n" || converted != 0 || prev != '\n' || !hasPrev {
		t.Fatalf("跨分片 CR+LF 不应归一: out=%q converted=%d prev=%q hasPrev=%v", string(out), converted, prev, hasPrev)
	}

	out, prev, hasPrev, converted = normalizeStdinInput([]byte("\n\n"), 0, false)
	if string(out) != "\r\r" || converted != 2 || prev != '\r' || !hasPrev {
		t.Fatalf("连续 LF 归一失败: out=%q converted=%d prev=%q hasPrev=%v", string(out), converted, prev, hasPrev)
	}

	_ = os.Setenv("DO_AI_STDIN_LF_AS_CR", "0")
	out, prev, hasPrev, converted = normalizeStdinInput([]byte("B\n"), 0, false)
	if string(out) != "B\n" || converted != 0 || prev != '\n' || !hasPrev {
		t.Fatalf("关闭归一后不应改写 LF: out=%q converted=%d prev=%q hasPrev=%v", string(out), converted, prev, hasPrev)
	}
}

func TestIsMeaningfulOutput(t *testing.T) {
	if isMeaningfulOutput([]byte("\x1b[2J\x1b[H")) {
		t.Fatalf("纯 ANSI 不应被视为有效输出")
	}
	if isMeaningfulOutput([]byte("   \t")) {
		t.Fatalf("纯空白不应被视为有效输出")
	}
	if isMeaningfulOutput([]byte("\x1b(B")) {
		t.Fatalf("字符集切换序列不应被视为有效输出")
	}
	if !isMeaningfulOutput([]byte("Press enter to continue")) {
		t.Fatalf("应当识别可见文本")
	}
}

func TestSplitOutputLinesSkipsCharsetDesignateEscSeq(t *testing.T) {
	in := []byte("alpha\x1b(Bbeta\n")
	got := splitOutputLines(in)
	want := []string{"alphabeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitOutputLines 未正确吞并 ESC(B: got=%#v want=%#v", got, want)
	}

	in = []byte("left\x1b)0right\n")
	got = splitOutputLines(in)
	want = []string{"leftright"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitOutputLines 未正确吞并 ESC)0: got=%#v want=%#v", got, want)
	}
}

func TestSplitOutputLinesCarriageReturnCompaction(t *testing.T) {
	in := []byte("ab\rcd\r\nef\n")
	got := splitOutputLines(in)
	want := []string{"abcd", "ef"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitOutputLines carriage-return 压缩失败: got=%#v want=%#v", got, want)
	}
}

func TestSubmitPayload(t *testing.T) {
	oldTarget := submitTargetCommand
	submitTargetCommand = ""
	defer func() { submitTargetCommand = oldTarget }()

	old := os.Getenv("DO_AI_SUBMIT")
	_ = os.Setenv("DO_AI_SUBMIT", "0")
	if len(submitPayload()) != 0 {
		t.Fatalf("DO_AI_SUBMIT=0 时不应发送提交键")
	}
	if old == "" {
		_ = os.Unsetenv("DO_AI_SUBMIT")
	} else {
		_ = os.Setenv("DO_AI_SUBMIT", old)
	}

	_ = os.Unsetenv("DO_AI_SUBMIT_MODE")
	if runtime.GOOS == "windows" {
		if string(submitPayload()) != "\r\x1b[13;5u" {
			t.Fatalf("Windows 默认应发送 CR+Ctrl-Enter")
		}
	} else if string(submitPayload()) != "\r" {
		t.Fatalf("默认应发送 CR 提交")
	}

	_ = os.Setenv("DO_AI_SUBMIT_MODE", "enter")
	if string(submitPayload()) != "\r" {
		t.Fatalf("enter 模式不匹配")
	}

	_ = os.Setenv("DO_AI_SUBMIT_MODE", "enter-lf")
	if string(submitPayload()) != "\r\n" {
		t.Fatalf("enter-lf 模式不匹配")
	}

	_ = os.Setenv("DO_AI_SUBMIT_MODE", "lf")
	if string(submitPayload()) != "\n" {
		t.Fatalf("lf 模式不匹配")
	}

	_ = os.Setenv("DO_AI_SUBMIT_MODE", "cr")
	if string(submitPayload()) != "\r" {
		t.Fatalf("cr 模式不匹配")
	}
}

func TestShouldApplyRelayCommandTerminateAware(t *testing.T) {
	if !shouldApplyRelayCommand(relayControlCommand{Action: relayActionTerminate}) {
		t.Fatalf("terminate action 应被执行")
	}
	if !shouldApplyRelayCommand(relayControlCommand{Submit: true}) {
		t.Fatalf("submit=true 应被执行")
	}
	if !shouldApplyRelayCommand(relayControlCommand{Input: "ls"}) {
		t.Fatalf("input 非空应被执行")
	}
	if shouldApplyRelayCommand(relayControlCommand{}) {
		t.Fatalf("空命令不应被执行")
	}
}

func TestRelayCommandIsTerminate(t *testing.T) {
	if !relayCommandIsTerminate(relayControlCommand{Action: relayActionTerminate}) {
		t.Fatalf("terminate 应识别")
	}
	if !relayCommandIsTerminate(relayControlCommand{Action: "  STOP  "}) {
		t.Fatalf("大小写/空格归一后应识别")
	}
	if !relayCommandIsTerminate(relayControlCommand{Input: relayTerminateSentinel}) {
		t.Fatalf("terminate sentinel 输入应识别")
	}
	if relayCommandIsTerminate(relayControlCommand{Action: "input"}) {
		t.Fatalf("非 terminate action 不应识别")
	}
}

func TestPreferredSubmitModeForCommand(t *testing.T) {
	cases := []struct {
		cmd      string
		wantMode string
		wantOK   bool
	}{
		{cmd: "codex", wantMode: "enter", wantOK: true},
		{cmd: "claude", wantMode: "enter+ctrl", wantOK: true},
		{cmd: "gemini", wantMode: "enter+ctrl", wantOK: true},
		{cmd: "bash", wantMode: "", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			mode, ok := preferredSubmitModeForCommand(tc.cmd)
			if ok != tc.wantOK || mode != tc.wantMode {
				t.Fatalf("preferredSubmitModeForCommand(%q)=(%q,%v) want=(%q,%v)", tc.cmd, mode, ok, tc.wantMode, tc.wantOK)
			}
		})
	}
}

func TestSubmitPayloadPreferredByCommand(t *testing.T) {
	oldTarget := submitTargetCommand
	oldMode := os.Getenv("DO_AI_SUBMIT_MODE")
	oldSubmit := os.Getenv("DO_AI_SUBMIT")
	defer func() {
		submitTargetCommand = oldTarget
		if oldMode == "" {
			_ = os.Unsetenv("DO_AI_SUBMIT_MODE")
		} else {
			_ = os.Setenv("DO_AI_SUBMIT_MODE", oldMode)
		}
		if oldSubmit == "" {
			_ = os.Unsetenv("DO_AI_SUBMIT")
		} else {
			_ = os.Setenv("DO_AI_SUBMIT", oldSubmit)
		}
	}()

	_ = os.Unsetenv("DO_AI_SUBMIT")
	_ = os.Unsetenv("DO_AI_SUBMIT_MODE")
	submitTargetCommand = "codex"

	want := "\r"
	if string(submitPayload()) != want {
		t.Fatalf("codex 默认应使用 enter, got=%q want=%q", string(submitPayload()), want)
	}
}

func TestResolvedSubmitMode(t *testing.T) {
	oldMode := os.Getenv("DO_AI_SUBMIT_MODE")
	oldTarget := submitTargetCommand
	defer func() {
		if oldMode == "" {
			_ = os.Unsetenv("DO_AI_SUBMIT_MODE")
		} else {
			_ = os.Setenv("DO_AI_SUBMIT_MODE", oldMode)
		}
		submitTargetCommand = oldTarget
	}()

	_ = os.Setenv("DO_AI_SUBMIT_MODE", "lf")
	submitTargetCommand = "codex"
	if got := resolvedSubmitMode(); got != "lf" {
		t.Fatalf("显式 submit mode 应优先: got=%q", got)
	}

	_ = os.Unsetenv("DO_AI_SUBMIT_MODE")
	submitTargetCommand = "codex"
	if got := resolvedSubmitMode(); got != "enter" {
		t.Fatalf("codex 默认 submit mode 不匹配: got=%q", got)
	}
}

func TestNormalizedKickPayloadForTarget(t *testing.T) {
	// 现在所有命令都使用完整版本，直接返回原始 payload
	payload := []byte("hello world")
	if got := normalizedKickPayloadForTarget(payload, "codex"); string(got) != string(payload) {
		t.Fatalf("应直接返回原始 payload: got=%q, want=%q", got, payload)
	}

	payload2 := []byte("继续执行，别停")
	if got := normalizedKickPayloadForTarget(payload2, "bash"); string(got) != string(payload2) {
		t.Fatalf("应直接返回原始 payload: got=%q, want=%q", got, payload2)
	}
}

func TestEffectiveIdleThreshold(t *testing.T) {
	// 现在所有命令使用统一的空闲阈值
	if got := effectiveIdleThreshold(2*time.Second, "bash"); got != 2*time.Second {
		t.Fatalf("应直接返回原始 idle: got=%v", got)
	}
	if got := effectiveIdleThreshold(2*time.Second, "codex"); got != 2*time.Second {
		t.Fatalf("应直接返回原始 idle: got=%v", got)
	}
	if got := effectiveIdleThreshold(2*time.Minute, "gemini"); got != 2*time.Minute {
		t.Fatalf("应直接返回原始 idle: got=%v", got)
	}
}

func TestAsciiSafeText(t *testing.T) {
	if got := asciiSafeText("abc\tdef"); got != "abc def" {
		t.Fatalf("asciiSafeText tab 归一失败: got=%q", got)
	}
	if got := asciiSafeText("中文ABC"); got != "ABC" {
		t.Fatalf("asciiSafeText 中文过滤失败: got=%q", got)
	}
}

func TestShouldApplyRelayCommand(t *testing.T) {
	cases := []struct {
		name string
		in   relayControlCommand
		want bool
	}{
		{name: "empty no submit", in: relayControlCommand{Input: "", Submit: false}, want: false},
		{name: "empty with submit", in: relayControlCommand{Input: "", Submit: true}, want: true},
		{name: "space no submit", in: relayControlCommand{Input: " ", Submit: false}, want: true},
		{name: "normal input", in: relayControlCommand{Input: "pwd", Submit: false}, want: true},
		{name: "normal input submit", in: relayControlCommand{Input: "pwd", Submit: true}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldApplyRelayCommand(tc.in); got != tc.want {
				t.Fatalf("shouldApplyRelayCommand(%+v)=%v want=%v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRelaySubmitFallbackPayload(t *testing.T) {
	oldFallback := os.Getenv("DO_AI_RELAY_SUBMIT_FALLBACK")
	oldRepeat := os.Getenv("DO_AI_RELAY_SUBMIT_REPEAT")
	oldForce := os.Getenv("DO_AI_RELAY_SUBMIT_FALLBACK_FORCE")
	oldTarget := submitTargetCommand
	defer func() {
		submitTargetCommand = oldTarget
		if oldFallback == "" {
			_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_FALLBACK")
		} else {
			_ = os.Setenv("DO_AI_RELAY_SUBMIT_FALLBACK", oldFallback)
		}
		if oldRepeat == "" {
			_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_REPEAT")
		} else {
			_ = os.Setenv("DO_AI_RELAY_SUBMIT_REPEAT", oldRepeat)
		}
		if oldForce == "" {
			_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_FALLBACK_FORCE")
		} else {
			_ = os.Setenv("DO_AI_RELAY_SUBMIT_FALLBACK_FORCE", oldForce)
		}
	}()

	submitTargetCommand = "claude"
	_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_FALLBACK")
	_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_REPEAT")
	_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_FALLBACK_FORCE")

	if got := relaySubmitFallbackPayload(""); len(got) != 0 {
		t.Fatalf("空输入不应发送 relay 补偿提交")
	}
	if got := relaySubmitFallbackPayload("ls"); string(got) != "\r" {
		t.Fatalf("默认应补偿 1 次 CR, got=%q", string(got))
	}

	_ = os.Setenv("DO_AI_RELAY_SUBMIT_REPEAT", "2")
	if got := relaySubmitFallbackPayload("pwd"); string(got) != "\r\r" {
		t.Fatalf("repeat=2 应补偿 2 次 CR, got=%q", string(got))
	}

	submitTargetCommand = "codex"
	if got := relaySubmitFallbackPayload("pwd"); len(got) != 0 {
		t.Fatalf("codex 默认不应发送 relay 补偿提交")
	}

	_ = os.Setenv("DO_AI_RELAY_SUBMIT_FALLBACK_FORCE", "1")
	if got := relaySubmitFallbackPayload("pwd"); string(got) != "\r\r" {
		t.Fatalf("codex 强制开启补偿后应发送 CR, got=%q", string(got))
	}

	_ = os.Setenv("DO_AI_RELAY_SUBMIT_FALLBACK", "0")
	if got := relaySubmitFallbackPayload("pwd"); len(got) != 0 {
		t.Fatalf("关闭补偿后不应发送 relay 补偿提交")
	}
}

func TestRelayPrimarySubmitDelayForTarget(t *testing.T) {
	old := os.Getenv("DO_AI_RELAY_SUBMIT_DELAY")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_DELAY")
		} else {
			_ = os.Setenv("DO_AI_RELAY_SUBMIT_DELAY", old)
		}
	}()

	_ = os.Setenv("DO_AI_RELAY_SUBMIT_DELAY", "180ms")
	if delay, need := relayPrimarySubmitDelayForTarget("codex", "ls"); !need || delay != 180*time.Millisecond {
		t.Fatalf("codex 应应用 relay 主提交延迟, need=%v delay=%v", need, delay)
	}
	if delay, need := relayPrimarySubmitDelayForTarget("codex", ""); need || delay != 0 {
		t.Fatalf("空输入不应触发 relay 主提交延迟, need=%v delay=%v", need, delay)
	}
	if delay, need := relayPrimarySubmitDelayForTarget("claude", "ls"); need || delay != 0 {
		t.Fatalf("claude 不应触发 relay 主提交延迟, need=%v delay=%v", need, delay)
	}
}

func TestRelaySubmitDelayFromEnv(t *testing.T) {
	old := os.Getenv("DO_AI_RELAY_SUBMIT_DELAY")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_DELAY")
		} else {
			_ = os.Setenv("DO_AI_RELAY_SUBMIT_DELAY", old)
		}
	}()

	_ = os.Unsetenv("DO_AI_RELAY_SUBMIT_DELAY")
	if got := relaySubmitDelayFromEnv(); got != 120*time.Millisecond {
		t.Fatalf("默认 relay submit delay 不匹配: got=%v", got)
	}

	_ = os.Setenv("DO_AI_RELAY_SUBMIT_DELAY", "250ms")
	if got := relaySubmitDelayFromEnv(); got != 250*time.Millisecond {
		t.Fatalf("relay submit delay env 不匹配: got=%v", got)
	}

	_ = os.Setenv("DO_AI_RELAY_SUBMIT_DELAY", "bad")
	if got := relaySubmitDelayFromEnv(); got != 120*time.Millisecond {
		t.Fatalf("非法 relay submit delay 应回退默认: got=%v", got)
	}
}

func lookupEnvForTest(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, item := range env {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			return item[len(prefix):], true
		}
	}
	return "", false
}

func TestNormalizedRuntimeEnvColorProfile(t *testing.T) {
	oldTerm := os.Getenv("DO_AI_TERM")
	oldColorTerm := os.Getenv("DO_AI_COLORTERM")
	oldKeepNoColor := os.Getenv("DO_AI_KEEP_NO_COLOR")
	oldForceColor := os.Getenv("DO_AI_FORCE_COLOR")
	defer func() {
		if oldTerm == "" {
			_ = os.Unsetenv("DO_AI_TERM")
		} else {
			_ = os.Setenv("DO_AI_TERM", oldTerm)
		}
		if oldColorTerm == "" {
			_ = os.Unsetenv("DO_AI_COLORTERM")
		} else {
			_ = os.Setenv("DO_AI_COLORTERM", oldColorTerm)
		}
		if oldKeepNoColor == "" {
			_ = os.Unsetenv("DO_AI_KEEP_NO_COLOR")
		} else {
			_ = os.Setenv("DO_AI_KEEP_NO_COLOR", oldKeepNoColor)
		}
		if oldForceColor == "" {
			_ = os.Unsetenv("DO_AI_FORCE_COLOR")
		} else {
			_ = os.Setenv("DO_AI_FORCE_COLOR", oldForceColor)
		}
	}()

	_ = os.Unsetenv("DO_AI_TERM")
	_ = os.Unsetenv("DO_AI_COLORTERM")
	_ = os.Unsetenv("DO_AI_KEEP_NO_COLOR")
	_ = os.Unsetenv("DO_AI_FORCE_COLOR")

	base := []string{"TERM=dumb", "NO_COLOR=1"}
	got := normalizedRuntimeEnv(base, "claude")
	if term, ok := lookupEnvForTest(got, "TERM"); !ok || term != "xterm-256color" {
		t.Fatalf("颜色敏感命令应统一 TERM=xterm-256color, got=%q ok=%v", term, ok)
	}
	if color, ok := lookupEnvForTest(got, "COLORTERM"); !ok || color != "truecolor" {
		t.Fatalf("应补齐 COLORTERM=truecolor, got=%q ok=%v", color, ok)
	}
	if _, ok := lookupEnvForTest(got, "NO_COLOR"); ok {
		t.Fatalf("颜色会话默认应移除 NO_COLOR")
	}

	_ = os.Setenv("DO_AI_KEEP_NO_COLOR", "1")
	got = normalizedRuntimeEnv(base, "claude")
	if v, ok := lookupEnvForTest(got, "NO_COLOR"); !ok || v != "1" {
		t.Fatalf("DO_AI_KEEP_NO_COLOR=1 时应保留 NO_COLOR, got=%q ok=%v", v, ok)
	}

	_ = os.Setenv("DO_AI_FORCE_COLOR", "1")
	got = normalizedRuntimeEnv(base, "claude")
	if v, ok := lookupEnvForTest(got, "FORCE_COLOR"); !ok || v != "1" {
		t.Fatalf("DO_AI_FORCE_COLOR=1 时应注入 FORCE_COLOR=1, got=%q ok=%v", v, ok)
	}
}

func TestNormalizedRuntimeEnvCustomTerm(t *testing.T) {
	oldTerm := os.Getenv("DO_AI_TERM")
	defer func() {
		if oldTerm == "" {
			_ = os.Unsetenv("DO_AI_TERM")
		} else {
			_ = os.Setenv("DO_AI_TERM", oldTerm)
		}
	}()

	_ = os.Setenv("DO_AI_TERM", "xterm-direct")
	got := normalizedRuntimeEnv([]string{"TERM=ansi"}, "bash")
	if term, ok := lookupEnvForTest(got, "TERM"); !ok || term != "xterm-direct" {
		t.Fatalf("应使用 DO_AI_TERM 自定义终端类型, got=%q ok=%v", term, ok)
	}
}

func TestThrottledWrite(t *testing.T) {
	// 小于一个 chunk 的 payload 直接一次写入
	var buf bytes.Buffer
	n, err := throttledWrite(&buf, []byte("hi"), 64, 0)
	if err != nil || n != 2 || buf.String() != "hi" {
		t.Fatalf("小 payload 写入失败: n=%d err=%v got=%q", n, err, buf.String())
	}

	// 等于一个 chunk
	buf.Reset()
	data := bytes.Repeat([]byte("A"), 64)
	n, err = throttledWrite(&buf, data, 64, 0)
	if err != nil || n != 64 || buf.Len() != 64 {
		t.Fatalf("等 chunk 写入失败: n=%d err=%v len=%d", n, err, buf.Len())
	}

	// 大于一个 chunk，分块写入
	buf.Reset()
	data = bytes.Repeat([]byte("B"), 150)
	n, err = throttledWrite(&buf, data, 64, 0)
	if err != nil || n != 150 || buf.Len() != 150 {
		t.Fatalf("分块写入失败: n=%d err=%v len=%d", n, err, buf.Len())
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("分块写入内容不一致")
	}

	// error 中断
	errW := &errWriter{failAfter: 30}
	n, err = throttledWrite(errW, bytes.Repeat([]byte("C"), 100), 64, 0)
	if err == nil {
		t.Fatalf("error writer 应返回错误")
	}
	if n > 64 {
		t.Fatalf("error 后不应继续写入: n=%d", n)
	}
}

type errWriter struct {
	written   int
	failAfter int
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.failAfter {
		partial := w.failAfter - w.written
		if partial < 0 {
			partial = 0
		}
		w.written += partial
		return partial, errors.New("模拟写入错误")
	}
	w.written += len(p)
	return len(p), nil
}

func TestBracketedPastePayload(t *testing.T) {
	// 空 payload
	if got := bracketedPastePayload(nil); len(got) != 0 {
		t.Fatalf("空 payload 应返回空: got=%q", got)
	}
	if got := bracketedPastePayload([]byte{}); len(got) != 0 {
		t.Fatalf("空 slice 应返回空: got=%q", got)
	}

	// 正常 payload
	got := bracketedPastePayload([]byte("hello"))
	want := "\x1b[200~hello\x1b[201~"
	if string(got) != want {
		t.Fatalf("bracketedPastePayload 不匹配: got=%q want=%q", string(got), want)
	}

	// 含 ESC 的 payload
	got = bracketedPastePayload([]byte("a\x1bb"))
	want = "\x1b[200~a\x1bb\x1b[201~"
	if string(got) != want {
		t.Fatalf("含 ESC payload 不匹配: got=%q want=%q", string(got), want)
	}
}

func TestUseBracketedPasteForTarget(t *testing.T) {
	old := os.Getenv("DO_AI_BRACKETED_PASTE")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_BRACKETED_PASTE")
		} else {
			_ = os.Setenv("DO_AI_BRACKETED_PASTE", old)
		}
	}()

	// auto 模式
	_ = os.Unsetenv("DO_AI_BRACKETED_PASTE")
	if !useBracketedPasteForTarget("codex") {
		t.Fatalf("auto 模式下 codex 应启用 bracketed paste")
	}
	if !useBracketedPasteForTarget("claude") {
		t.Fatalf("auto 模式下 claude 应启用 bracketed paste")
	}
	if !useBracketedPasteForTarget("gemini") {
		t.Fatalf("auto 模式下 gemini 应启用 bracketed paste")
	}
	if useBracketedPasteForTarget("bash") {
		t.Fatalf("auto 模式下 bash 不应启用 bracketed paste")
	}

	// on 强制开启
	_ = os.Setenv("DO_AI_BRACKETED_PASTE", "on")
	if !useBracketedPasteForTarget("bash") {
		t.Fatalf("on 模式下 bash 应启用 bracketed paste")
	}

	// off 强制关闭
	_ = os.Setenv("DO_AI_BRACKETED_PASTE", "off")
	if useBracketedPasteForTarget("codex") {
		t.Fatalf("off 模式下 codex 不应启用 bracketed paste")
	}
}

func TestInjectChunkSizeFromEnv(t *testing.T) {
	old := os.Getenv("DO_AI_INJECT_CHUNK_SIZE")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_INJECT_CHUNK_SIZE")
		} else {
			_ = os.Setenv("DO_AI_INJECT_CHUNK_SIZE", old)
		}
	}()

	_ = os.Unsetenv("DO_AI_INJECT_CHUNK_SIZE")
	if got := injectChunkSizeFromEnv(); got != 64 {
		t.Fatalf("默认 chunk size 应为 64: got=%d", got)
	}

	_ = os.Setenv("DO_AI_INJECT_CHUNK_SIZE", "128")
	if got := injectChunkSizeFromEnv(); got != 128 {
		t.Fatalf("chunk size 128 不匹配: got=%d", got)
	}

	_ = os.Setenv("DO_AI_INJECT_CHUNK_SIZE", "bad")
	if got := injectChunkSizeFromEnv(); got != 64 {
		t.Fatalf("非法值应回退默认: got=%d", got)
	}

	_ = os.Setenv("DO_AI_INJECT_CHUNK_SIZE", "0")
	if got := injectChunkSizeFromEnv(); got != 64 {
		t.Fatalf("零值应回退默认: got=%d", got)
	}
}

func TestInjectChunkDelayFromEnv(t *testing.T) {
	old := os.Getenv("DO_AI_INJECT_CHUNK_DELAY")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_INJECT_CHUNK_DELAY")
		} else {
			_ = os.Setenv("DO_AI_INJECT_CHUNK_DELAY", old)
		}
	}()

	_ = os.Unsetenv("DO_AI_INJECT_CHUNK_DELAY")
	if got := injectChunkDelayFromEnv(); got != 2*time.Millisecond {
		t.Fatalf("默认 chunk delay 应为 2ms: got=%v", got)
	}

	_ = os.Setenv("DO_AI_INJECT_CHUNK_DELAY", "10ms")
	if got := injectChunkDelayFromEnv(); got != 10*time.Millisecond {
		t.Fatalf("chunk delay 10ms 不匹配: got=%v", got)
	}

	_ = os.Setenv("DO_AI_INJECT_CHUNK_DELAY", "bad")
	if got := injectChunkDelayFromEnv(); got != 2*time.Millisecond {
		t.Fatalf("非法值应回退默认: got=%v", got)
	}

	_ = os.Setenv("DO_AI_INJECT_CHUNK_DELAY", "0s")
	if got := injectChunkDelayFromEnv(); got != 0 {
		t.Fatalf("0s 应为零延迟: got=%v", got)
	}
}

func TestResolveExecutionArgsDirect(t *testing.T) {
	args, err := resolveExecutionArgs([]string{"codex", "--help"}, false)
	if err != nil {
		t.Fatalf("resolveExecutionArgs 返回错误: %v", err)
	}
	if len(args) != 2 || args[0] != "codex" || args[1] != "--help" {
		t.Fatalf("应返回原始 args: %#v", args)
	}

	_, err = resolveExecutionArgs(nil, false)
	if err == nil {
		t.Fatalf("空 args 应返回错误")
	}

	_, err = resolveExecutionArgs([]string{}, false)
	if err == nil {
		t.Fatalf("空 slice 应返回错误")
	}
}
