package main

import (
	"os"
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

func TestKickPayload(t *testing.T) {
	got := string(kickPayload(0, 0))
	want := autoMessageMain + "\n"
	if got != want {
		t.Fatalf("默认注入内容不匹配: got=%q want=%q", got, want)
	}

	calib := string(kickPayload(4, 5))
	if calib != autoMessageCalib+"\n" {
		t.Fatalf("校准注入内容不匹配: got=%q want=%q", calib, autoMessageCalib+"\n")
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
	if string(submitPayload()) != "\x1b[13;5u" {
		t.Fatalf("默认应发送 ctrl-enter 提交")
	}

	_ = os.Setenv("DO_AI_SUBMIT_MODE", "enter")
	if string(submitPayload()) != "\r" {
		t.Fatalf("enter 模式不匹配")
	}
}
