package main

import (
	"os"
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
	if runtime.GOOS == "windows" {
		if got := submitFallbackPayload(); len(got) != windowsSubmitRepeat || got[0] != '\r' {
			t.Fatalf("Windows 补偿键应为 CR x%d", windowsSubmitRepeat)
		}
	} else if len(submitFallbackPayload()) != 0 {
		t.Fatalf("非 Windows 不应发送补偿键")
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

func TestIsMeaningfulOutput(t *testing.T) {
	if isMeaningfulOutput([]byte("\x1b[2J\x1b[H")) {
		t.Fatalf("纯 ANSI 不应被视为有效输出")
	}
	if isMeaningfulOutput([]byte("   \t")) {
		t.Fatalf("纯空白不应被视为有效输出")
	}
	if !isMeaningfulOutput([]byte("Press enter to continue")) {
		t.Fatalf("应当识别可见文本")
	}
}

func TestSubmitPayload(t *testing.T) {
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
