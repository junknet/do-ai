package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeHeartbeatURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://relay.example.com", "https://relay.example.com/api/v1/heartbeat"},
		{"https://relay.example.com/", "https://relay.example.com/api/v1/heartbeat"},
		{"https://relay.example.com/custom", "https://relay.example.com/custom"},
		{"http://127.0.0.1:8787", "http://127.0.0.1:8787/api/v1/heartbeat"},
	}
	for _, c := range cases {
		got := normalizeHeartbeatURL(c.in)
		if got != c.want {
			t.Fatalf("normalizeHeartbeatURL(%q)=%q want=%q", c.in, got, c.want)
		}
	}
}

func TestResolveRelayHeartbeatURL(t *testing.T) {
	oldURL := os.Getenv("DO_AI_RELAY_URL")
	oldAuto := os.Getenv("DO_AI_RELAY_AUTO")
	oldStrict := os.Getenv("DO_AI_RELAY_URL_STRICT")
	defer func() {
		if oldURL == "" {
			_ = os.Unsetenv("DO_AI_RELAY_URL")
		} else {
			_ = os.Setenv("DO_AI_RELAY_URL", oldURL)
		}
		if oldAuto == "" {
			_ = os.Unsetenv("DO_AI_RELAY_AUTO")
		} else {
			_ = os.Setenv("DO_AI_RELAY_AUTO", oldAuto)
		}
		if oldStrict == "" {
			_ = os.Unsetenv("DO_AI_RELAY_URL_STRICT")
		} else {
			_ = os.Setenv("DO_AI_RELAY_URL_STRICT", oldStrict)
		}
	}()

	_ = os.Setenv("DO_AI_RELAY_URL", "http://127.0.0.1:18888")
	_ = os.Setenv("DO_AI_RELAY_URL_STRICT", "1")
	_ = os.Unsetenv("DO_AI_RELAY_AUTO")
	if got := resolveRelayHeartbeatURL(); got != "http://127.0.0.1:18888/api/v1/heartbeat" {
		t.Fatalf("显式 DO_AI_RELAY_URL 未生效, got=%q", got)
	}

	_ = os.Unsetenv("DO_AI_RELAY_URL_STRICT")
	_ = os.Unsetenv("DO_AI_RELAY_URL")
	_ = os.Setenv("DO_AI_RELAY_AUTO", "1")
	wantDefault := fmt.Sprintf("http://%s:18787/api/v1/heartbeat", defaultRelayDomain)
	if got := resolveRelayHeartbeatURL(); got != wantDefault {
		t.Fatalf("默认 relay URL 不匹配, got=%q want=%q", got, wantDefault)
	}

	_ = os.Setenv("DO_AI_RELAY_AUTO", "0")
	if got := resolveRelayHeartbeatURL(); got != "" {
		t.Fatalf("关闭 auto relay 后应为空, got=%q", got)
	}
}

func TestResolveRelayHeartbeatURLWithMetaFallback(t *testing.T) {
	oldURL := os.Getenv("DO_AI_RELAY_URL")
	oldAuto := os.Getenv("DO_AI_RELAY_AUTO")
	oldStrict := os.Getenv("DO_AI_RELAY_URL_STRICT")
	oldChecker := relayReachabilityChecker
	defer func() {
		relayReachabilityChecker = oldChecker
		if oldURL == "" {
			_ = os.Unsetenv("DO_AI_RELAY_URL")
		} else {
			_ = os.Setenv("DO_AI_RELAY_URL", oldURL)
		}
		if oldAuto == "" {
			_ = os.Unsetenv("DO_AI_RELAY_AUTO")
		} else {
			_ = os.Setenv("DO_AI_RELAY_AUTO", oldAuto)
		}
		if oldStrict == "" {
			_ = os.Unsetenv("DO_AI_RELAY_URL_STRICT")
		} else {
			_ = os.Setenv("DO_AI_RELAY_URL_STRICT", oldStrict)
		}
	}()

	_ = os.Setenv("DO_AI_RELAY_AUTO", "1")
	_ = os.Unsetenv("DO_AI_RELAY_URL_STRICT")
	_ = os.Setenv("DO_AI_RELAY_URL", "http://127.0.0.1:18888")
	relayReachabilityChecker = func(_ *url.URL, _ time.Duration) bool { return false }

	wantDefault := fmt.Sprintf("http://%s:18787/api/v1/heartbeat", defaultRelayDomain)
	resolved, fallback := resolveRelayHeartbeatURLWithMeta()
	if !fallback {
		t.Fatalf("本地 relay 不可达时应触发回退")
	}
	if resolved != wantDefault {
		t.Fatalf("回退 relay URL 不匹配, got=%q want=%q", resolved, wantDefault)
	}

	_ = os.Setenv("DO_AI_RELAY_URL_STRICT", "1")
	resolved, fallback = resolveRelayHeartbeatURLWithMeta()
	if fallback {
		t.Fatalf("strict 模式不应触发回退")
	}
	if resolved != "http://127.0.0.1:18888/api/v1/heartbeat" {
		t.Fatalf("strict 模式应保持显式 URL, got=%q", resolved)
	}

	_ = os.Unsetenv("DO_AI_RELAY_URL_STRICT")
	relayReachabilityChecker = func(_ *url.URL, _ time.Duration) bool { return true }
	resolved, fallback = resolveRelayHeartbeatURLWithMeta()
	if fallback {
		t.Fatalf("本地 relay 可达时不应回退")
	}
	if resolved != "http://127.0.0.1:18888/api/v1/heartbeat" {
		t.Fatalf("本地 relay 可达时应保持显式 URL, got=%q", resolved)
	}
}

func TestRelayAutoEnabled(t *testing.T) {
	oldAuto := os.Getenv("DO_AI_RELAY_AUTO")
	defer func() {
		if oldAuto == "" {
			_ = os.Unsetenv("DO_AI_RELAY_AUTO")
		} else {
			_ = os.Setenv("DO_AI_RELAY_AUTO", oldAuto)
		}
	}()

	_ = os.Unsetenv("DO_AI_RELAY_AUTO")
	if !relayAutoEnabled() {
		t.Fatalf("默认应开启 auto relay")
	}

	for _, v := range []string{"0", "false", "off", "no", "disable", "disabled"} {
		_ = os.Setenv("DO_AI_RELAY_AUTO", v)
		if relayAutoEnabled() {
			t.Fatalf("%q 应关闭 auto relay", v)
		}
	}
}

func TestSummarizeOutputChunk(t *testing.T) {
	chunk := []byte("\x1b[2J\x1b[Hhello\r\nworld\r\n")
	got := summarizeOutputChunk(chunk)
	if got != "world" {
		t.Fatalf("summarizeOutputChunk=%q want=world", got)
	}

	empty := summarizeOutputChunk([]byte("\x1b[2J\x1b[H\r\n\t"))
	if empty != "" {
		t.Fatalf("纯ANSI/空白应返回空字符串, got=%q", empty)
	}
}

func TestFirstMatchedKeyword(t *testing.T) {
	if got := firstMatchedKeyword("Need CONFIRM now", []string{"confirm", "panic"}); got != "confirm" {
		t.Fatalf("匹配失败 got=%q", got)
	}
	if got := firstMatchedKeyword("all good", []string{"panic", "error"}); got != "" {
		t.Fatalf("不应匹配 got=%q", got)
	}
}

func TestBuildAlerts(t *testing.T) {
	exitCode := 1
	hb := relayHeartbeat{
		SessionID:   "do-test",
		SessionName: "codex",
		Host:        "test-host",
		CWD:         "/tmp/work",
		Command:     "codex",
		State:       "running",
		IdleSeconds: 190,
		LastText:    "请确认是否继续",
	}
	alerts := buildAlerts(hb, 180, []string{"确认", "panic"})
	if len(alerts) != 2 {
		t.Fatalf("应触发2个告警 got=%d", len(alerts))
	}

	hb.State = "exited"
	hb.ExitCode = &exitCode
	alerts = buildAlerts(hb, 180, []string{"确认", "panic"})
	if len(alerts) != 2 {
		t.Fatalf("退出场景应触发关键词+退出告警 got=%d", len(alerts))
	}
}

func TestRelayStoreAllowNotify(t *testing.T) {
	store := newRelayStore()
	if !store.allowNotify("k", 2*time.Second) {
		t.Fatalf("首次应允许")
	}
	if store.allowNotify("k", 2*time.Second) {
		t.Fatalf("冷却期内不应允许")
	}
}

func TestHostFromRemoteAddr(t *testing.T) {
	if got := hostFromRemoteAddr("127.0.0.1:12345"); got != "127.0.0.1" {
		t.Fatalf("IPv4 解析失败 got=%q", got)
	}
	if got := hostFromRemoteAddr("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatalf("IPv6 解析失败 got=%q", got)
	}
}

func TestRelayIntervalFromEnv(t *testing.T) {
	old := os.Getenv("DO_AI_RELAY_INTERVAL")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_RELAY_INTERVAL")
		} else {
			_ = os.Setenv("DO_AI_RELAY_INTERVAL", old)
		}
	}()

	_ = os.Setenv("DO_AI_RELAY_INTERVAL", "1s")
	if got := relayIntervalFromEnv(); got != time.Second {
		t.Fatalf("relayIntervalFromEnv=%v want=1s", got)
	}

	_ = os.Setenv("DO_AI_RELAY_INTERVAL", "invalid")
	if got := relayIntervalFromEnv(); got != 3*time.Second {
		t.Fatalf("非法值应回退默认, got=%v", got)
	}
}

func TestRelayOutputFlushIntervalFromEnv(t *testing.T) {
	old := os.Getenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL")
	defer func() {
		if old == "" {
			_ = os.Unsetenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL")
		} else {
			_ = os.Setenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL", old)
		}
	}()

	_ = os.Unsetenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL")
	if got := relayOutputFlushIntervalFromEnv(); got != 220*time.Millisecond {
		t.Fatalf("默认 flush 间隔不匹配, got=%v", got)
	}

	_ = os.Setenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL", "50ms")
	if got := relayOutputFlushIntervalFromEnv(); got != 50*time.Millisecond {
		t.Fatalf("flush 间隔 env 不匹配, got=%v", got)
	}

	_ = os.Setenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL", "bad")
	if got := relayOutputFlushIntervalFromEnv(); got != 220*time.Millisecond {
		t.Fatalf("非法 flush 间隔应回退默认, got=%v", got)
	}
}

func TestRelayReporterReportOutputBatching(t *testing.T) {
	oldURL := os.Getenv("DO_AI_RELAY_URL")
	oldToken := os.Getenv("DO_AI_RELAY_TOKEN")
	oldFlush := os.Getenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL")
	defer func() {
		if oldURL == "" {
			_ = os.Unsetenv("DO_AI_RELAY_URL")
		} else {
			_ = os.Setenv("DO_AI_RELAY_URL", oldURL)
		}
		if oldToken == "" {
			_ = os.Unsetenv("DO_AI_RELAY_TOKEN")
		} else {
			_ = os.Setenv("DO_AI_RELAY_TOKEN", oldToken)
		}
		if oldFlush == "" {
			_ = os.Unsetenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL")
		} else {
			_ = os.Setenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL", oldFlush)
		}
	}()

	var mu sync.Mutex
	batches := make([][]string, 0, 2)
	rawCounts := make([]int, 0, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/output/push", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedRelayRequest(r, hardcodedRelayToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req relayOutputPushRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		mu.Lock()
		batches = append(batches, append([]string(nil), req.Lines...))
		rawCounts = append(rawCounts, len(req.RawChunks))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	_ = os.Setenv("DO_AI_RELAY_URL", ts.URL)
	_ = os.Setenv("DO_AI_RELAY_TOKEN", hardcodedRelayToken)
	_ = os.Setenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL", "60ms")

	r := newRelayReporter([]string{"sh", "-lc", "cat"}, "/tmp/.do-ai.lock", false)
	if !r.enabled {
		t.Fatalf("reporter 应启用")
	}
	r.ReportOutputChunk([]byte("line-a\n"), time.Now().Unix(), false)
	r.ReportOutputChunk([]byte("line-b\n"), time.Now().Unix(), false)

	deadline := time.Now().Add(1200 * time.Millisecond)
	for {
		mu.Lock()
		n := len(batches)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("应合并为 1 次输出上报, got=%d", len(batches))
	}
	joined := strings.Join(batches[0], "|")
	if !strings.Contains(joined, "line-a") || !strings.Contains(joined, "line-b") {
		t.Fatalf("批量上报内容不完整: %q", joined)
	}
	if len(rawCounts) != 1 || rawCounts[0] <= 0 {
		t.Fatalf("应包含 raw chunk 批量上报: %#v", rawCounts)
	}
}

func TestRelayStoreOutputQueue(t *testing.T) {
	store := newRelayStore()
	store.appendOutputLines("s1", []string{"line1", "line2"}, 100)
	store.appendOutputLines("s1", []string{"line3"}, 101)

	events, hasMore := store.listOutput("s1", 0, 0, 2, true)
	if len(events) != 2 {
		t.Fatalf("tail 应返回2条 got=%d", len(events))
	}
	if events[0].Text != "line2" || events[1].Text != "line3" {
		t.Fatalf("tail 顺序不匹配: %#v", events)
	}
	if !hasMore {
		t.Fatalf("应存在更早记录")
	}

	before := events[0].Seq
	older, _ := store.listOutput("s1", 0, before, 10, false)
	if len(older) != 1 || older[0].Text != "line1" {
		t.Fatalf("向前翻页失败: %#v", older)
	}
}

func TestOutputPushAndListHandlers(t *testing.T) {
	store := newRelayStore()
	token := hardcodedRelayToken
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/output/push", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedRelayRequest(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req relayOutputPushRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		rawChunks := make([][]byte, 0, len(req.RawChunks))
		for _, encoded := range req.RawChunks {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
			if err != nil || len(decoded) == 0 {
				continue
			}
			rawChunks = append(rawChunks, decoded)
		}
		store.appendOutputWithRaw(req.SessionID, req.Lines, rawChunks, req.TS)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/v1/output/list", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedRelayRequest(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		events, _ := store.listOutput(r.URL.Query().Get("session_id"), 0, 0, 50, false)
		_ = json.NewEncoder(w).Encode(relayOutputListResponse{Events: events, Count: len(events), TS: time.Now().Unix()})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{"session_id":"s2","lines":["hello","world"],"ts":100}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/output/push", strings.NewReader(body))
	req.Header.Set("X-Relay-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("push 请求失败: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("push 状态码异常: %d", resp.StatusCode)
	}

	listReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/output/list?session_id=s2", nil)
	listReq.Header.Set("X-Relay-Token", token)
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("list 请求失败: %v", err)
	}
	defer func() { _ = listResp.Body.Close() }()
	var out relayOutputListResponse
	if err := json.NewDecoder(listResp.Body).Decode(&out); err != nil {
		t.Fatalf("list 解析失败: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("list 数量异常: %d", out.Count)
	}
}

func TestControlSendTerminateMarksStoppingAndPullsAction(t *testing.T) {
	store := newRelayStore()
	store.upsert(relayHeartbeat{
		SessionID: "s-terminate",
		State:     "running",
		UpdatedAt: time.Now().Unix(),
	})
	token := hardcodedRelayToken
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/control/send", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedRelayRequest(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req relayControlSendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		action, ok := normalizeControlAction(req.Action)
		if !ok {
			http.Error(w, "invalid action", http.StatusBadRequest)
			return
		}
		req.Action = action
		if !validControlSendRequest(req.Input, req.Submit, req.Action) {
			http.Error(w, "missing input_or_submit", http.StatusBadRequest)
			return
		}
		cmd := store.enqueueCommand(req.SessionID, req.Input, req.Submit, req.Source, req.Action)
		if req.Action == relayActionTerminate {
			store.markSessionStopping(req.SessionID, req.Source)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "command": cmd})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	body := `{"session_id":"s-terminate","input":"","submit":false,"action":"terminate","source":"unit-test"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/control/send", strings.NewReader(body))
	req.Header.Set("X-Relay-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("control send terminate 请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("control send terminate 状态码异常: %d", resp.StatusCode)
	}

	commands := store.pullCommands("s-terminate", 8)
	if len(commands) != 1 {
		t.Fatalf("terminate 指令数量异常: %d", len(commands))
	}
	if commands[0].Action != relayActionTerminate {
		t.Fatalf("terminate action 不匹配: %#v", commands[0])
	}

	items := store.list(9999, false)
	if len(items) != 1 || items[0].State != "stopping" {
		t.Fatalf("session 状态应为 stopping: %#v", items)
	}
}

func TestControlSendTerminateSentinelBackwardCompatible(t *testing.T) {
	store := newRelayStore()
	store.upsert(relayHeartbeat{
		SessionID: "s-terminate-legacy",
		State:     "running",
		UpdatedAt: time.Now().Unix(),
	})
	token := hardcodedRelayToken
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/control/send", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedRelayRequest(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req relayControlSendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if !validControlSendRequest(req.Input, req.Submit, "") {
			http.Error(w, "missing input_or_submit", http.StatusBadRequest)
			return
		}
		cmd := store.enqueueCommand(req.SessionID, req.Input, req.Submit, req.Source, "")
		if strings.TrimSpace(req.Input) == relayTerminateSentinel {
			store.markSessionStopping(req.SessionID, req.Source)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "command": cmd})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	body := fmt.Sprintf(`{"session_id":"s-terminate-legacy","input":"%s","submit":false,"source":"legacy-client"}`, relayTerminateSentinel)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/control/send", strings.NewReader(body))
	req.Header.Set("X-Relay-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("legacy terminate 请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy terminate 状态码异常: %d", resp.StatusCode)
	}
	items := store.list(9999, false)
	if len(items) != 1 || items[0].State != "stopping" {
		t.Fatalf("legacy terminate 后 state 应为 stopping: %#v", items)
	}
}

func TestRelayStoreScreenSnapshotSupportsCarriageReturnRewrite(t *testing.T) {
	store := newRelayStore()
	store.appendOutputWithRaw("s-cr", []string{"prompt"}, [][]byte{[]byte("abc\rxy\n")}, 100)
	screen := store.getOutputScreen("s-cr", 20)
	if len(screen.Lines) == 0 {
		t.Fatalf("screen 快照应有内容")
	}
	found := false
	for _, line := range screen.Lines {
		if line == "xyc" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("CR 覆盖语义不正确: lines=%#v", screen.Lines)
	}
}

func TestRelayOutputScreenHandler(t *testing.T) {
	store := newRelayStore()
	token := hardcodedRelayToken
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/output/screen", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedRelayRequest(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sid := strings.TrimSpace(r.URL.Query().Get("session_id"))
		_ = json.NewEncoder(w).Encode(store.getOutputScreen(sid, 100))
	})

	store.appendOutputWithRaw("s-screen", nil, [][]byte{[]byte("line1\nline2\n")}, 101)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/output/screen?session_id=s-screen", nil)
	req.Header.Set("X-Relay-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("screen 请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("screen 状态码异常: %d", resp.StatusCode)
	}

	var out relayOutputScreenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("screen 解析失败: %v", err)
	}
	if out.SessionID != "s-screen" {
		t.Fatalf("session_id 不匹配: %q", out.SessionID)
	}
	if out.LineCount < 2 || len(out.Lines) < 2 {
		t.Fatalf("screen lines 不足: %#v", out)
	}
	if len(out.StyledLines) < 2 {
		t.Fatalf("screen styled_lines 不足: %#v", out)
	}
}

func TestValidControlSendRequest(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		submit bool
		action string
		want   bool
	}{
		{name: "empty without submit", input: "", submit: false, action: "", want: false},
		{name: "empty with submit", input: "", submit: true, action: "", want: true},
		{name: "space without submit", input: " ", submit: false, action: "", want: true},
		{name: "normal input", input: "ls", submit: false, action: "", want: true},
		{name: "terminate action", input: "", submit: false, action: relayActionTerminate, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validControlSendRequest(tc.input, tc.submit, tc.action); got != tc.want {
				t.Fatalf("validControlSendRequest(%q,%v,%q)=%v want=%v", tc.input, tc.submit, tc.action, got, tc.want)
			}
		})
	}
}

func TestNormalizeControlAction(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{in: "", want: "", wantOK: true},
		{in: "terminate", want: relayActionTerminate, wantOK: true},
		{in: "STOP", want: relayActionTerminate, wantOK: true},
		{in: "close", want: relayActionTerminate, wantOK: true},
		{in: "kill", want: relayActionTerminate, wantOK: true},
		{in: "weird", want: "", wantOK: false},
	}

	for _, tc := range cases {
		got, ok := normalizeControlAction(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Fatalf("normalizeControlAction(%q)=(%q,%v) want=(%q,%v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestRelayStoreMarkSessionStopping(t *testing.T) {
	store := newRelayStore()
	store.upsert(relayHeartbeat{
		SessionID: "s-stop",
		State:     "running",
		UpdatedAt: time.Now().Unix(),
	})
	store.markSessionStopping("s-stop", "android-rn")

	sessions := store.list(9999, false)
	if len(sessions) != 1 {
		t.Fatalf("session 数量异常: %d", len(sessions))
	}
	s := sessions[0]
	if s.State != "stopping" {
		t.Fatalf("state 应为 stopping, got=%q", s.State)
	}
	if !strings.Contains(strings.ToLower(s.LastText), "stop requested") {
		t.Fatalf("last_text 应包含 stop 提示, got=%q", s.LastText)
	}
}

func TestRelayStoreTracksBellTimestampFromRawChunks(t *testing.T) {
	store := newRelayStore()
	store.upsert(relayHeartbeat{
		SessionID: "s-bell",
		State:     "running",
		UpdatedAt: 100,
	})

	store.appendOutputWithRaw("s-bell", nil, [][]byte{[]byte("ready\a\n")}, 123)

	sessions := store.list(9999, false)
	if len(sessions) != 1 {
		t.Fatalf("session 数量异常: %d", len(sessions))
	}
	if sessions[0].LastBellAt != 123 {
		t.Fatalf("last_bell_at 应为 123, got=%d", sessions[0].LastBellAt)
	}

	// 心跳不带 last_bell_at 时，应保留历史 bell 时间戳。
	store.upsert(relayHeartbeat{
		SessionID: "s-bell",
		State:     "running",
		UpdatedAt: 124,
	})
	sessions = store.list(9999, false)
	if len(sessions) != 1 {
		t.Fatalf("session 数量异常: %d", len(sessions))
	}
	if sessions[0].LastBellAt != 123 {
		t.Fatalf("心跳覆盖后应保留 last_bell_at=123, got=%d", sessions[0].LastBellAt)
	}
}

func TestDecodeTmuxPassthrough(t *testing.T) {
	payload := []byte("tmux;\x1b\x1b[2Jhello")
	decoded, ok := decodeTmuxPassthrough(payload)
	if !ok {
		t.Fatalf("tmux passthrough 应识别成功")
	}
	if string(decoded) != "\x1b[2Jhello" {
		t.Fatalf("tmux passthrough 解包错误: got=%q", string(decoded))
	}

	if _, ok := decodeTmuxPassthrough([]byte("not-tmux")); ok {
		t.Fatalf("非 tmux payload 不应被识别")
	}
}

func TestRelayStoreScreenSnapshotSupportsTmuxDCS(t *testing.T) {
	store := newRelayStore()
	raw := []byte("\x1bPtmux;\x1b\x1b[2J\x1b\x1b[Hhello\nworld\x1b\\")
	store.appendOutputWithRaw("s-tmux-dcs", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-tmux-dcs", 20)
	if screen.LineCount < 2 || len(screen.Lines) < 2 {
		t.Fatalf("tmux dcs 快照行数不足: %#v", screen)
	}
	if screen.Lines[0] != "hello" || strings.TrimSpace(screen.Lines[1]) != "world" {
		t.Fatalf("tmux dcs 渲染不正确: lines=%#v", screen.Lines)
	}
}

func TestRelayStoreScreenSnapshotStyledLinesSupportsANSIColors(t *testing.T) {
	store := newRelayStore()
	raw := []byte("\x1b[31mRED\x1b[0m plain \x1b[38;5;196mIDX\x1b[0m \x1b[38;2;255;0;0mRGB\x1b[0m\n")
	store.appendOutputWithRaw("s-style-color", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-style-color", 20)
	if len(screen.StyledLines) < 1 {
		t.Fatalf("styled_lines 为空: %#v", screen)
	}
	segments := screen.StyledLines[0].Segments
	if len(segments) != 5 {
		t.Fatalf("styled 段数量不匹配: got=%d segments=%#v", len(segments), segments)
	}

	if segments[0].Text != "RED" || segments[0].FG != ansi256ToHex(1) {
		t.Fatalf("基础色段错误: %#v", segments[0])
	}
	if segments[1].Text != " plain " || segments[1].FG != "" || segments[1].BG != "" {
		t.Fatalf("reset 后默认段错误: %#v", segments[1])
	}
	if segments[2].Text != "IDX" || segments[2].FG != "#ff0000" {
		t.Fatalf("256 色段错误: %#v", segments[2])
	}
	if segments[3].Text != " " || segments[3].FG != "" || segments[3].BG != "" {
		t.Fatalf("二次 reset 后默认段错误: %#v", segments[3])
	}
	if segments[4].Text != "RGB" || segments[4].FG != "#ff0000" {
		t.Fatalf("24bit 色段错误: %#v", segments[4])
	}
}

func TestRelayStoreScreenSnapshotStyledLinesSupportsStyleReset(t *testing.T) {
	store := newRelayStore()
	raw := []byte("\x1b[1;3;4;32;48;5;25mAB\x1b[22;23;24;39;49mCD\n")
	store.appendOutputWithRaw("s-style-reset", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-style-reset", 20)
	if len(screen.StyledLines) < 1 {
		t.Fatalf("styled_lines 为空: %#v", screen)
	}
	segments := screen.StyledLines[0].Segments
	if len(segments) != 2 {
		t.Fatalf("styled 段数量不匹配: got=%d segments=%#v", len(segments), segments)
	}

	if segments[0].Text != "AB" || !segments[0].Bold || !segments[0].Italic || !segments[0].Underline {
		t.Fatalf("样式段属性错误: %#v", segments[0])
	}
	if segments[0].FG != ansi256ToHex(2) {
		t.Fatalf("前景色错误: %#v", segments[0])
	}
	if segments[0].BG != "#005faf" {
		t.Fatalf("背景色错误: %#v", segments[0])
	}

	if segments[1].Text != "CD" {
		t.Fatalf("reset 后文本段错误: %#v", segments[1])
	}
	if segments[1].Bold || segments[1].Italic || segments[1].Underline || segments[1].FG != "" || segments[1].BG != "" {
		t.Fatalf("reset 后样式应清空: %#v", segments[1])
	}
}

func TestRelayStoreScreenSnapshotSupportsEraseCharsCSIX(t *testing.T) {
	store := newRelayStore()
	raw := []byte("abcdefghij\rabc\x1b[7X\n")
	store.appendOutputWithRaw("s-csix", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-csix", 20)
	if len(screen.Lines) == 0 {
		t.Fatalf("screen 快照应有内容")
	}
	if screen.Lines[0] != "abc" {
		t.Fatalf("CSI X 擦除语义错误: lines=%#v", screen.Lines)
	}
}

func TestRelayStoreScreenSnapshotSupportsScrollUpCSIS(t *testing.T) {
	store := newRelayStore()
	raw := []byte("a\nb\nc\n\x1b[H\x1b[1S")
	store.appendOutputWithRaw("s-csis", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-csis", 20)
	if len(screen.Lines) < 1 {
		t.Fatalf("screen 快照应有内容: %#v", screen)
	}
	hasB := false
	for _, line := range screen.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "a" {
			t.Fatalf("CSI S 后顶部旧行应被滚出: lines=%#v", screen.Lines)
		}
		if trimmed == "b" {
			hasB = true
		}
	}
	if !hasB {
		t.Fatalf("CSI S 后应保留后续行: lines=%#v", screen.Lines)
	}
}

func TestRelayStoreScreenSnapshotSupportsVerticalAbsoluteCSID(t *testing.T) {
	store := newRelayStore()
	raw := []byte("row1\nrow2\nrow3\n\x1b[2d\x1b[1GX\n")
	store.appendOutputWithRaw("s-csid", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-csid", 20)
	found := false
	for _, line := range screen.Lines {
		if strings.HasPrefix(line, "X") && strings.Contains(line, "row2") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("CSI d 行定位语义错误: lines=%#v", screen.Lines)
	}
}

func TestRelayStoreScreenSnapshotLineFeedKeepsColumn(t *testing.T) {
	store := newRelayStore()
	raw := []byte("\x1b[21GX\n\nY\n")
	store.appendOutputWithRaw("s-lf-col", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-lf-col", 20)
	if len(screen.Lines) < 3 {
		t.Fatalf("line feed 快照行数不足: %#v", screen.Lines)
	}
	if !strings.HasSuffix(screen.Lines[2], "Y") {
		t.Fatalf("第三行应以 Y 结尾: %#v", screen.Lines)
	}
	if len(screen.Lines[2]) < 10 {
		t.Fatalf("LF 不应重置到列 0，第三行应有前导空格: %#v", screen.Lines[2])
	}
}

func TestRelayStoreScreenSnapshotSupportsAltScreenPrivateMode(t *testing.T) {
	store := newRelayStore()
	raw := []byte("main\n\x1b[?1049halt\n\x1b[?1049lrest\n")
	store.appendOutputWithRaw("s-alt-screen", nil, [][]byte{raw}, time.Now().Unix())

	screen := store.getOutputScreen("s-alt-screen", 20)
	joined := strings.Join(screen.Lines, "\n")
	if !strings.Contains(joined, "main") {
		t.Fatalf("离开 alt-screen 后应恢复主屏内容: lines=%#v", screen.Lines)
	}
	if strings.Contains(joined, "alt") {
		t.Fatalf("离开 alt-screen 后不应保留 alt 缓冲内容: lines=%#v", screen.Lines)
	}
	if !strings.Contains(joined, "rest") {
		t.Fatalf("离开 alt-screen 后后续输出应可见: lines=%#v", screen.Lines)
	}
}
