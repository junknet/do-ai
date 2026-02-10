package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	hardcodedRelayToken  = "doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff"
	defaultRelayDomain   = "47.110.255.240"
	relayActionTerminate = "terminate"
)

var relayReachabilityChecker = defaultRelayReachabilityChecker

type relayHeartbeat struct {
	SessionID    string `json:"session_id"`
	SessionName  string `json:"session_name,omitempty"`
	Prefix       string `json:"prefix,omitempty"`
	Host         string `json:"host"`
	CWD          string `json:"cwd,omitempty"`
	Command      string `json:"command,omitempty"`
	PID          int    `json:"pid,omitempty"`
	State        string `json:"state"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	StartedAt    int64  `json:"started_at,omitempty"`
	UpdatedAt    int64  `json:"updated_at"`
	LastOutputAt int64  `json:"last_output_at,omitempty"`
	LastKickAt   int64  `json:"last_kick_at,omitempty"`
	IdleSeconds  int64  `json:"idle_seconds,omitempty"`
	KickCount    uint64 `json:"kick_count,omitempty"`
	LastText     string `json:"last_text,omitempty"`
	LockFile     string `json:"lock_file,omitempty"`
}

type relaySessionView struct {
	relayHeartbeat
	Online     bool  `json:"online"`
	AgeSeconds int64 `json:"age_seconds"`
}

type relayControlCommand struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Input     string `json:"input"`
	Submit    bool   `json:"submit"`
	Action    string `json:"action,omitempty"`
	Source    string `json:"source,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type relayControlSendRequest struct {
	SessionID string `json:"session_id"`
	Input     string `json:"input"`
	Submit    bool   `json:"submit"`
	Action    string `json:"action,omitempty"`
	Source    string `json:"source"`
}

type relayControlPullResponse struct {
	Commands []relayControlCommand `json:"commands"`
	Count    int                   `json:"count"`
	TS       int64                 `json:"ts"`
}

type relayOutputEvent struct {
	Seq       int64  `json:"seq"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	TS        int64  `json:"ts"`
}

type relayOutputPushRequest struct {
	SessionID string   `json:"session_id"`
	Lines     []string `json:"lines"`
	RawChunks []string `json:"raw_chunks,omitempty"`
	TS        int64    `json:"ts"`
}

type relayOutputListResponse struct {
	Events        []relayOutputEvent `json:"events"`
	Count         int                `json:"count"`
	Cursor        int64              `json:"cursor"`
	HasMoreBefore bool               `json:"has_more_before"`
	TS            int64              `json:"ts"`
}

type relayOutputScreenResponse struct {
	SessionID   string                  `json:"session_id"`
	Lines       []string                `json:"lines"`
	StyledLines []relayScreenStyledLine `json:"styled_lines,omitempty"`
	Content     string                  `json:"content"`
	LineCount   int                     `json:"line_count"`
	CursorRow   int                     `json:"cursor_row"`
	CursorCol   int                     `json:"cursor_col"`
	Revision    int64                   `json:"revision"`
	Truncated   bool                    `json:"truncated"`
	TS          int64                   `json:"ts"`
}

type relayScreenSegment struct {
	Text      string `json:"text"`
	FG        string `json:"fg,omitempty"`
	BG        string `json:"bg,omitempty"`
	Bold      bool   `json:"bold,omitempty"`
	Italic    bool   `json:"italic,omitempty"`
	Underline bool   `json:"underline,omitempty"`
}

type relayScreenStyledLine struct {
	Segments []relayScreenSegment `json:"segments"`
}

type relayCellStyle struct {
	FG        string
	BG        string
	Bold      bool
	Italic    bool
	Underline bool
}

type relayScreenState struct {
	rows              [][]rune
	styles            [][]relayCellStyle
	cursorRow         int
	cursorCol         int
	scrollTop         int
	scrollBottom      int
	styleState        relayCellStyle
	pending           []byte
	revision          int64
	updatedAt         int64
	altScreenActive   bool
	savedRows         [][]rune
	savedStyles       [][]relayCellStyle
	savedCursorRow    int
	savedCursorCol    int
	savedScrollTop    int
	savedScrollBottom int
	savedStyleState   relayCellStyle
}

const (
	relayScreenMaxRows         = 320
	relayScreenMaxCols         = 260
	relayScreenDefaultLimit    = 220
	relayScreenMaxLimit        = 600
	relayScreenRawChunkMaxKeep = 120
)

type relayStore struct {
	mu         sync.RWMutex
	sessions   map[string]relayHeartbeat
	notifyGate map[string]time.Time
	commandQ   map[string][]relayControlCommand
	outputQ    map[string][]relayOutputEvent
	outputSeq  int64
	screenQ    map[string]*relayScreenState
	screenSeq  int64
}

func newRelayStore() *relayStore {
	return &relayStore{
		sessions:   make(map[string]relayHeartbeat),
		notifyGate: make(map[string]time.Time),
		commandQ:   make(map[string][]relayControlCommand),
		outputQ:    make(map[string][]relayOutputEvent),
		screenQ:    make(map[string]*relayScreenState),
	}
}

func (s *relayStore) upsert(hb relayHeartbeat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[hb.SessionID] = hb
}

func (s *relayStore) list(staleSeconds int64, onlyOnline bool) []relaySessionView {
	now := time.Now().Unix()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]relaySessionView, 0, len(s.sessions))
	for _, hb := range s.sessions {
		age := now - hb.UpdatedAt
		if age < 0 {
			age = 0
		}
		online := hb.State == "running" && age <= staleSeconds
		if onlyOnline && !online {
			continue
		}
		out = append(out, relaySessionView{
			relayHeartbeat: hb,
			Online:         online,
			AgeSeconds:     age,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func (s *relayStore) allowNotify(key string, cooldown time.Duration) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.notifyGate[key]
	if ok && now.Sub(last) < cooldown {
		return false
	}
	s.notifyGate[key] = now
	return true
}

func (s *relayStore) enqueueCommand(sessionID, input string, submit bool, source, action string) relayControlCommand {
	now := time.Now().Unix()
	command := relayControlCommand{
		ID:        fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Input:     input,
		Submit:    submit,
		Action:    action,
		Source:    source,
		CreatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := append(s.commandQ[sessionID], command)
	if len(queue) > 100 {
		queue = queue[len(queue)-100:]
	}
	s.commandQ[sessionID] = queue
	return command
}

func (s *relayStore) markSessionStopping(sessionID, source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hb, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	hb.State = "stopping"
	hb.UpdatedAt = time.Now().Unix()
	if source != "" {
		hb.LastText = fmt.Sprintf("[STATE_INVALID] stop requested via %s", source)
	} else {
		hb.LastText = "[STATE_INVALID] stop requested"
	}
	s.sessions[sessionID] = hb
}

func (s *relayStore) pullCommands(sessionID string, limit int) []relayControlCommand {
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.commandQ[sessionID]
	if len(queue) == 0 {
		return nil
	}
	if limit > len(queue) {
		limit = len(queue)
	}
	out := append([]relayControlCommand(nil), queue[:limit]...)
	left := append([]relayControlCommand(nil), queue[limit:]...)
	if len(left) == 0 {
		delete(s.commandQ, sessionID)
	} else {
		s.commandQ[sessionID] = left
	}
	return out
}

func (s *relayStore) appendOutputLines(sessionID string, lines []string, ts int64) []relayOutputEvent {
	return s.appendOutput(sessionID, lines, nil, ts)
}

func (s *relayStore) appendOutputWithRaw(sessionID string, lines []string, rawChunks [][]byte, ts int64) []relayOutputEvent {
	return s.appendOutput(sessionID, lines, rawChunks, ts)
}

func (s *relayStore) appendOutput(sessionID string, lines []string, rawChunks [][]byte, ts int64) []relayOutputEvent {
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	cleanLines := normalizeOutputLines(lines)
	if len(cleanLines) == 0 && len(rawChunks) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.screenQ[sessionID]
	if state == nil {
		state = newRelayScreenState()
		s.screenQ[sessionID] = state
	}
	created := make([]relayOutputEvent, 0, len(cleanLines))
	for _, line := range cleanLines {
		s.outputSeq++
		evt := relayOutputEvent{
			Seq:       s.outputSeq,
			SessionID: sessionID,
			Text:      line,
			TS:        ts,
		}
		s.outputQ[sessionID] = append(s.outputQ[sessionID], evt)
		created = append(created, evt)
	}
	queue := s.outputQ[sessionID]
	if len(queue) > 3000 {
		queue = queue[len(queue)-3000:]
		s.outputQ[sessionID] = queue
	}

	fallbackChunks := make([][]byte, 0, len(cleanLines))
	if len(rawChunks) == 0 && len(cleanLines) > 0 {
		for _, line := range cleanLines {
			fallbackChunks = append(fallbackChunks, []byte(line+"\n"))
		}
		rawChunks = fallbackChunks
	}
	if len(rawChunks) > 0 {
		for _, chunk := range rawChunks {
			state.applyChunk(chunk, relayScreenMaxRows, relayScreenMaxCols)
		}
		s.screenSeq++
		state.revision = s.screenSeq
		state.updatedAt = ts
	}
	return created
}

func (s *relayStore) getOutputScreen(sessionID string, limit int) relayOutputScreenResponse {
	if limit <= 0 {
		limit = relayScreenDefaultLimit
	}
	if limit > relayScreenMaxLimit {
		limit = relayScreenMaxLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.screenQ[sessionID]
	if state == nil {
		return relayOutputScreenResponse{
			SessionID:   sessionID,
			Lines:       []string{},
			StyledLines: []relayScreenStyledLine{},
			LineCount:   0,
			CursorRow:   0,
			CursorCol:   0,
			Revision:    0,
			Truncated:   false,
			TS:          time.Now().Unix(),
		}
	}
	lines, styledLines, cursorRow, cursorCol, truncated := state.snapshot(limit)
	return relayOutputScreenResponse{
		SessionID:   sessionID,
		Lines:       lines,
		StyledLines: styledLines,
		Content:     strings.Join(lines, "\n"),
		LineCount:   len(lines),
		CursorRow:   cursorRow,
		CursorCol:   cursorCol,
		Revision:    state.revision,
		Truncated:   truncated,
		TS:          time.Now().Unix(),
	}
}

func newRelayScreenState() *relayScreenState {
	return &relayScreenState{
		rows:         make([][]rune, 1),
		styles:       make([][]relayCellStyle, 1),
		scrollTop:    0,
		scrollBottom: relayScreenMaxRows - 1,
		styleState:   relayCellStyle{},
	}
}

func (s *relayScreenState) ensureRow(row int) {
	if row < 0 {
		row = 0
	}
	for len(s.rows) <= row {
		s.rows = append(s.rows, nil)
	}
	for len(s.styles) <= row {
		s.styles = append(s.styles, nil)
	}
}

func (s *relayScreenState) trimRows(maxRows int) {
	if maxRows <= 0 {
		maxRows = relayScreenMaxRows
	}
	if len(s.rows) <= maxRows {
		return
	}
	drop := len(s.rows) - maxRows
	s.rows = append([][]rune(nil), s.rows[drop:]...)
	s.styles = append([][]relayCellStyle(nil), s.styles[drop:]...)
	s.cursorRow -= drop
	if s.cursorRow < 0 {
		s.cursorRow = 0
	}
}

func (s *relayScreenState) effectiveScrollRegion(maxRows int) (int, int) {
	if maxRows <= 0 {
		maxRows = relayScreenMaxRows
	}
	top := s.scrollTop
	bottom := s.scrollBottom
	if top < 0 {
		top = 0
	}
	if bottom <= 0 || bottom >= maxRows {
		bottom = maxRows - 1
	}
	if top >= bottom {
		top = 0
		bottom = maxRows - 1
	}
	return top, bottom
}

func (s *relayScreenState) setScrollRegion(top, bottom, maxRows int) {
	if maxRows <= 0 {
		maxRows = relayScreenMaxRows
	}
	if top < 0 {
		top = 0
	}
	if bottom <= 0 || bottom >= maxRows {
		bottom = maxRows - 1
	}
	if top >= bottom {
		top = 0
		bottom = maxRows - 1
	}
	s.scrollTop = top
	s.scrollBottom = bottom
}

func cloneRuneRows(rows [][]rune) [][]rune {
	if len(rows) == 0 {
		return nil
	}
	cloned := make([][]rune, len(rows))
	for i := range rows {
		cloned[i] = append([]rune(nil), rows[i]...)
	}
	return cloned
}

func cloneStyleRows(rows [][]relayCellStyle) [][]relayCellStyle {
	if len(rows) == 0 {
		return nil
	}
	cloned := make([][]relayCellStyle, len(rows))
	for i := range rows {
		cloned[i] = append([]relayCellStyle(nil), rows[i]...)
	}
	return cloned
}

func hasCSIParam(params []int, target int) bool {
	for _, p := range params {
		if p == target {
			return true
		}
	}
	return false
}

func (s *relayScreenState) resetBufferState(maxRows int) {
	s.rows = make([][]rune, 1)
	s.styles = make([][]relayCellStyle, 1)
	s.cursorRow = 0
	s.cursorCol = 0
	s.scrollTop = 0
	s.scrollBottom = maxRows - 1
	if s.scrollBottom < 0 {
		s.scrollBottom = relayScreenMaxRows - 1
	}
}

func (s *relayScreenState) enterAltScreen(maxRows int) {
	if s.altScreenActive {
		return
	}
	s.savedRows = cloneRuneRows(s.rows)
	s.savedStyles = cloneStyleRows(s.styles)
	s.savedCursorRow = s.cursorRow
	s.savedCursorCol = s.cursorCol
	s.savedScrollTop = s.scrollTop
	s.savedScrollBottom = s.scrollBottom
	s.savedStyleState = s.styleState
	s.altScreenActive = true
	s.resetBufferState(maxRows)
	s.styleState = relayCellStyle{}
}

func (s *relayScreenState) leaveAltScreen(maxRows, maxCols int) {
	if !s.altScreenActive {
		return
	}
	if len(s.savedRows) > 0 {
		s.rows = cloneRuneRows(s.savedRows)
	} else {
		s.rows = make([][]rune, 1)
	}
	if len(s.savedStyles) > 0 {
		s.styles = cloneStyleRows(s.savedStyles)
	} else {
		s.styles = make([][]relayCellStyle, len(s.rows))
	}
	if len(s.styles) < len(s.rows) {
		pad := make([][]relayCellStyle, len(s.rows)-len(s.styles))
		s.styles = append(s.styles, pad...)
	}
	s.cursorRow = s.savedCursorRow
	s.cursorCol = s.savedCursorCol
	s.scrollTop = s.savedScrollTop
	s.scrollBottom = s.savedScrollBottom
	s.styleState = s.savedStyleState
	s.altScreenActive = false
	s.savedRows = nil
	s.savedStyles = nil
	s.normalizeCursor(maxRows, maxCols)
}

func (s *relayScreenState) clearAltScreenSavedState() {
	s.altScreenActive = false
	s.savedRows = nil
	s.savedStyles = nil
	s.savedCursorRow = 0
	s.savedCursorCol = 0
	s.savedScrollTop = 0
	s.savedScrollBottom = 0
	s.savedStyleState = relayCellStyle{}
}

func (s *relayScreenState) scrollUp(count, maxRows, maxCols int) {
	if count <= 0 {
		count = 1
	}
	top, bottom := s.effectiveScrollRegion(maxRows)
	s.ensureRow(bottom)
	for step := 0; step < count; step++ {
		for row := top; row < bottom; row++ {
			s.rows[row] = append([]rune(nil), s.rows[row+1]...)
			s.styles[row] = append([]relayCellStyle(nil), s.styles[row+1]...)
		}
		s.rows[bottom] = nil
		s.styles[bottom] = nil
	}
	s.trimRows(maxRows)
}

func (s *relayScreenState) eraseChars(count, maxRows, maxCols int) {
	if count <= 0 {
		count = 1
	}
	s.normalizeCursor(maxRows, maxCols)
	row := append([]rune(nil), s.rows[s.cursorRow]...)
	styleRow := append([]relayCellStyle(nil), s.styles[s.cursorRow]...)
	for i := 0; i < count; i++ {
		idx := s.cursorCol + i
		if idx < 0 || idx >= len(row) {
			continue
		}
		row[idx] = ' '
		if idx < len(styleRow) {
			styleRow[idx] = relayCellStyle{}
		}
	}
	s.rows[s.cursorRow] = row
	s.styles[s.cursorRow] = styleRow
}

func (s *relayScreenState) normalizeCursor(maxRows, maxCols int) {
	if maxCols <= 0 {
		maxCols = relayScreenMaxCols
	}
	if s.cursorCol < 0 {
		s.cursorCol = 0
	}
	if s.cursorCol > maxCols-1 {
		s.cursorCol = maxCols - 1
	}
	if s.cursorRow < 0 {
		s.cursorRow = 0
	}
	s.ensureRow(s.cursorRow)
	s.trimRows(maxRows)
}

func (s *relayScreenState) putRune(r rune, maxRows, maxCols int) {
	if maxCols <= 0 {
		maxCols = relayScreenMaxCols
	}
	if s.cursorCol >= maxCols {
		s.cursorCol = 0
		s.lineFeed(maxRows, maxCols)
	}
	s.normalizeCursor(maxRows, maxCols)
	row := s.rows[s.cursorRow]
	styleRow := s.styles[s.cursorRow]
	for len(row) < s.cursorCol {
		row = append(row, ' ')
		styleRow = append(styleRow, relayCellStyle{})
	}
	if s.cursorCol < len(row) {
		row[s.cursorCol] = r
		if s.cursorCol < len(styleRow) {
			styleRow[s.cursorCol] = s.styleState
		}
	} else {
		row = append(row, r)
		styleRow = append(styleRow, s.styleState)
	}
	if len(row) > maxCols {
		row = row[:maxCols]
	}
	if len(styleRow) > maxCols {
		styleRow = styleRow[:maxCols]
	}
	s.rows[s.cursorRow] = row
	s.styles[s.cursorRow] = styleRow
	s.cursorCol++
}

func (s *relayScreenState) lineFeed(maxRows, maxCols int) {
	top, bottom := s.effectiveScrollRegion(maxRows)
	if s.cursorRow < top {
		s.cursorRow = top
	}
	if s.cursorRow >= bottom {
		s.scrollUp(1, maxRows, maxCols)
		s.cursorRow = bottom
	} else {
		s.cursorRow++
	}
	s.normalizeCursor(maxRows, maxCols)
}

func (s *relayScreenState) carriageReturn() {
	s.cursorCol = 0
}

func (s *relayScreenState) backspace(maxRows, maxCols int) {
	if s.cursorCol > 0 {
		s.cursorCol--
	}
	s.normalizeCursor(maxRows, maxCols)
}

func (s *relayScreenState) eraseLine(mode int, maxRows, maxCols int) {
	s.normalizeCursor(maxRows, maxCols)
	row := append([]rune(nil), s.rows[s.cursorRow]...)
	styleRow := append([]relayCellStyle(nil), s.styles[s.cursorRow]...)
	switch mode {
	case 0:
		if s.cursorCol <= len(row) {
			row = row[:s.cursorCol]
		}
		if s.cursorCol <= len(styleRow) {
			styleRow = styleRow[:s.cursorCol]
		}
	case 1:
		end := s.cursorCol
		if end >= len(row) {
			end = len(row) - 1
		}
		for i := 0; i <= end && i < len(row); i++ {
			row[i] = ' '
			if i < len(styleRow) {
				styleRow[i] = relayCellStyle{}
			}
		}
	case 2:
		row = nil
		styleRow = nil
		s.cursorCol = 0
	}
	s.rows[s.cursorRow] = row
	s.styles[s.cursorRow] = styleRow
}

func (s *relayScreenState) eraseDisplay(mode int, maxRows, maxCols int) {
	s.normalizeCursor(maxRows, maxCols)
	switch mode {
	case 0:
		s.eraseLine(0, maxRows, maxCols)
		for i := s.cursorRow + 1; i < len(s.rows); i++ {
			s.rows[i] = nil
			s.styles[i] = nil
		}
	case 1:
		for i := 0; i < s.cursorRow; i++ {
			s.rows[i] = nil
			s.styles[i] = nil
		}
		row := append([]rune(nil), s.rows[s.cursorRow]...)
		styleRow := append([]relayCellStyle(nil), s.styles[s.cursorRow]...)
		end := s.cursorCol
		if end >= len(row) {
			end = len(row) - 1
		}
		for i := 0; i <= end && i < len(row); i++ {
			row[i] = ' '
			if i < len(styleRow) {
				styleRow[i] = relayCellStyle{}
			}
		}
		s.rows[s.cursorRow] = row
		s.styles[s.cursorRow] = styleRow
	case 2:
		s.resetBufferState(maxRows)
		s.clearAltScreenSavedState()
		s.styleState = relayCellStyle{}
	}
}

func (s *relayScreenState) moveCursorBy(dr, dc int, maxRows, maxCols int) {
	s.cursorRow += dr
	s.cursorCol += dc
	s.normalizeCursor(maxRows, maxCols)
}

func (s *relayScreenState) setCursor(row, col int, maxRows, maxCols int) {
	if row < 0 {
		row = 0
	}
	if col < 0 {
		col = 0
	}
	s.cursorRow = row
	s.cursorCol = col
	s.normalizeCursor(maxRows, maxCols)
}

func clampANSIByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func rgbHex(r, g, b int) string {
	return fmt.Sprintf("#%02x%02x%02x", clampANSIByte(r), clampANSIByte(g), clampANSIByte(b))
}

func ansi256ToHex(idx int) string {
	if idx < 0 {
		idx = 0
	}
	if idx > 255 {
		idx = 255
	}
	palette16 := []string{
		"#000000", "#800000", "#008000", "#808000", "#000080", "#800080", "#008080", "#c0c0c0",
		"#808080", "#ff0000", "#00ff00", "#ffff00", "#0000ff", "#ff00ff", "#00ffff", "#ffffff",
	}
	if idx < 16 {
		return palette16[idx]
	}
	if idx <= 231 {
		value := idx - 16
		r := value / 36
		g := (value % 36) / 6
		b := value % 6
		conv := func(n int) int {
			if n == 0 {
				return 0
			}
			return 55 + n*40
		}
		return rgbHex(conv(r), conv(g), conv(b))
	}
	gray := 8 + (idx-232)*10
	return rgbHex(gray, gray, gray)
}

func setStyleFG(style relayCellStyle, color string) relayCellStyle {
	style.FG = color
	return style
}

func setStyleBG(style relayCellStyle, color string) relayCellStyle {
	style.BG = color
	return style
}

func (s *relayScreenState) applySGR(params []int) {
	if len(params) == 0 {
		s.styleState = relayCellStyle{}
		return
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			s.styleState = relayCellStyle{}
		case p == 1:
			s.styleState.Bold = true
		case p == 3:
			s.styleState.Italic = true
		case p == 4:
			s.styleState.Underline = true
		case p == 22:
			s.styleState.Bold = false
		case p == 23:
			s.styleState.Italic = false
		case p == 24:
			s.styleState.Underline = false
		case p == 39:
			s.styleState.FG = ""
		case p == 49:
			s.styleState.BG = ""
		case p >= 30 && p <= 37:
			s.styleState = setStyleFG(s.styleState, ansi256ToHex(p-30))
		case p >= 90 && p <= 97:
			s.styleState = setStyleFG(s.styleState, ansi256ToHex(p-90+8))
		case p >= 40 && p <= 47:
			s.styleState = setStyleBG(s.styleState, ansi256ToHex(p-40))
		case p >= 100 && p <= 107:
			s.styleState = setStyleBG(s.styleState, ansi256ToHex(p-100+8))
		case p == 38 || p == 48:
			isBG := p == 48
			if i+1 >= len(params) {
				continue
			}
			mode := params[i+1]
			if mode == 5 && i+2 < len(params) {
				color := ansi256ToHex(params[i+2])
				if isBG {
					s.styleState = setStyleBG(s.styleState, color)
				} else {
					s.styleState = setStyleFG(s.styleState, color)
				}
				i += 2
				continue
			}
			if mode == 2 && i+4 < len(params) {
				color := rgbHex(params[i+2], params[i+3], params[i+4])
				if isBG {
					s.styleState = setStyleBG(s.styleState, color)
				} else {
					s.styleState = setStyleFG(s.styleState, color)
				}
				i += 4
				continue
			}
		}
	}
}

func (s *relayScreenState) applyCSI(final byte, params []int, private bool, maxRows, maxCols int) {
	param := func(idx, defaultVal int) int {
		if idx >= len(params) {
			return defaultVal
		}
		if params[idx] == 0 {
			return defaultVal
		}
		return params[idx]
	}

	switch final {
	case 'A':
		s.moveCursorBy(-param(0, 1), 0, maxRows, maxCols)
	case 'B':
		s.moveCursorBy(param(0, 1), 0, maxRows, maxCols)
	case 'C':
		s.moveCursorBy(0, param(0, 1), maxRows, maxCols)
	case 'D':
		s.moveCursorBy(0, -param(0, 1), maxRows, maxCols)
	case 'E':
		s.moveCursorBy(param(0, 1), 0, maxRows, maxCols)
		s.cursorCol = 0
	case 'F':
		s.moveCursorBy(-param(0, 1), 0, maxRows, maxCols)
		s.cursorCol = 0
	case 'G':
		s.setCursor(s.cursorRow, param(0, 1)-1, maxRows, maxCols)
	case 'H', 'f':
		s.setCursor(param(0, 1)-1, param(1, 1)-1, maxRows, maxCols)
	case 'J':
		s.eraseDisplay(param(0, 0), maxRows, maxCols)
	case 'K':
		s.eraseLine(param(0, 0), maxRows, maxCols)
	case 'X':
		s.eraseChars(param(0, 1), maxRows, maxCols)
	case 'S':
		s.scrollUp(param(0, 1), maxRows, maxCols)
	case 'd':
		s.setCursor(param(0, 1)-1, s.cursorCol, maxRows, maxCols)
	case 'r':
		top := 1
		bottom := maxRows
		if len(params) >= 1 && params[0] != 0 {
			top = params[0]
		}
		if len(params) >= 2 && params[1] != 0 {
			bottom = params[1]
		}
		s.setScrollRegion(top-1, bottom-1, maxRows)
		s.setCursor(0, 0, maxRows, maxCols)
	case 'h':
		if private && (hasCSIParam(params, 1049) || hasCSIParam(params, 1047) || hasCSIParam(params, 47)) {
			s.enterAltScreen(maxRows)
		}
	case 'l':
		if private && (hasCSIParam(params, 1049) || hasCSIParam(params, 1047) || hasCSIParam(params, 47)) {
			s.leaveAltScreen(maxRows, maxCols)
		}
	case 'm':
		s.applySGR(params)
	}
}

func parseCSIParams(raw string) []int {
	raw = strings.TrimLeft(raw, "?=><!")
	if raw == "" {
		return []int{0}
	}
	parts := strings.Split(raw, ";")
	params := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			params = append(params, 0)
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			params = append(params, 0)
			continue
		}
		params = append(params, n)
	}
	if len(params) == 0 {
		params = append(params, 0)
	}
	return params
}

func parseCSISequence(data []byte) (consumed int, final byte, params []int, private bool, complete bool) {
	if len(data) < 3 || data[0] != 0x1b || data[1] != '[' {
		return 0, 0, nil, false, false
	}
	for i := 2; i < len(data); i++ {
		if data[i] >= 0x40 && data[i] <= 0x7e {
			raw := string(data[2:i])
			return i + 1, data[i], parseCSIParams(raw), strings.HasPrefix(strings.TrimSpace(raw), "?"), true
		}
	}
	return 0, 0, nil, false, false
}

func parseOscOrStSequence(data []byte, marker byte) (consumed int, complete bool) {
	if len(data) < 2 || data[0] != 0x1b || data[1] != marker {
		return 0, false
	}
	for i := 2; i < len(data); i++ {
		if marker == ']' && data[i] == 0x07 {
			return i + 1, true
		}
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
			return i + 2, true
		}
	}
	return 0, false
}

func decodeTmuxPassthrough(payload []byte) ([]byte, bool) {
	const prefix = "tmux;"
	if !bytes.HasPrefix(payload, []byte(prefix)) {
		return nil, false
	}
	body := payload[len(prefix):]
	if len(body) == 0 {
		return nil, true
	}
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] == 0x1b && i+1 < len(body) && body[i+1] == 0x1b {
			out = append(out, 0x1b)
			i++
			continue
		}
		out = append(out, body[i])
	}
	return out, true
}

func (s *relayScreenState) applyChunk(chunk []byte, maxRows, maxCols int) {
	if len(chunk) == 0 {
		return
	}
	data := append(append([]byte(nil), s.pending...), chunk...)
	s.pending = nil

	for i := 0; i < len(data); {
		b := data[i]
		if b == 0x1b {
			if i+1 >= len(data) {
				s.pending = append(s.pending, data[i:]...)
				break
			}
			next := data[i+1]
			switch next {
			case '[':
				consumed, final, params, private, ok := parseCSISequence(data[i:])
				if !ok {
					s.pending = append(s.pending, data[i:]...)
					return
				}
				s.applyCSI(final, params, private, maxRows, maxCols)
				i += consumed
				continue
			case '(', ')', '*', '+', '-', '.', '/':
				if i+2 >= len(data) {
					s.pending = append(s.pending, data[i:]...)
					return
				}
				i += 3
				continue
			case '%', '#':
				if i+2 >= len(data) {
					s.pending = append(s.pending, data[i:]...)
					return
				}
				i += 3
				continue
			case ']':
				consumed, ok := parseOscOrStSequence(data[i:], ']')
				if !ok {
					s.pending = append(s.pending, data[i:]...)
					return
				}
				i += consumed
				continue
			case 'P':
				consumed, ok := parseOscOrStSequence(data[i:], 'P')
				if !ok {
					s.pending = append(s.pending, data[i:]...)
					return
				}
				payloadStart := i + 2
				payloadEnd := i + consumed
				if payloadEnd >= payloadStart+2 && data[payloadEnd-2] == 0x1b && data[payloadEnd-1] == '\\' {
					payloadEnd -= 2
				}
				if payloadStart < payloadEnd {
					if inner, passthrough := decodeTmuxPassthrough(data[payloadStart:payloadEnd]); passthrough && len(inner) > 0 {
						s.applyChunk(inner, maxRows, maxCols)
					}
				}
				i += consumed
				continue
			case '^', '_':
				consumed, ok := parseOscOrStSequence(data[i:], next)
				if !ok {
					s.pending = append(s.pending, data[i:]...)
					return
				}
				i += consumed
				continue
			case 'c':
				s.resetBufferState(maxRows)
				s.clearAltScreenSavedState()
				s.styleState = relayCellStyle{}
				i += 2
				continue
			default:
				i += 2
				continue
			}
		}

		switch b {
		case '\r':
			s.carriageReturn()
			i++
			continue
		case '\n':
			s.lineFeed(maxRows, maxCols)
			i++
			continue
		case '\b', 0x7f:
			s.backspace(maxRows, maxCols)
			i++
			continue
		case '\t':
			spaces := 4 - (s.cursorCol % 4)
			if spaces <= 0 {
				spaces = 4
			}
			for j := 0; j < spaces; j++ {
				s.putRune(' ', maxRows, maxCols)
			}
			i++
			continue
		}

		if b < 0x20 {
			i++
			continue
		}

		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 && !utf8.FullRune(data[i:]) {
			s.pending = append(s.pending, data[i:]...)
			return
		}
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r == 'B' {
			leftBoundary := i == 0 || data[i-1] <= 0x20
			rightBoundary := i+size >= len(data) || data[i+size] <= 0x20
			if leftBoundary && rightBoundary {
				i += size
				continue
			}
		}
		s.putRune(r, maxRows, maxCols)
		i += size
	}

	s.trimRows(maxRows)
}

func styleEqual(a, b relayCellStyle) bool {
	return a.FG == b.FG && a.BG == b.BG && a.Bold == b.Bold && a.Italic == b.Italic && a.Underline == b.Underline
}

func buildStyledLineSegments(runes []rune, styles []relayCellStyle) relayScreenStyledLine {
	if len(runes) == 0 {
		return relayScreenStyledLine{Segments: []relayScreenSegment{}}
	}
	segments := make([]relayScreenSegment, 0, 4)
	start := 0
	current := relayCellStyle{}
	if len(styles) > 0 {
		current = styles[0]
	}
	for i := 1; i <= len(runes); i++ {
		var nextStyle relayCellStyle
		if i < len(runes) && i < len(styles) {
			nextStyle = styles[i]
		}
		if i == len(runes) || !styleEqual(current, nextStyle) {
			seg := relayScreenSegment{
				Text:      string(runes[start:i]),
				FG:        current.FG,
				BG:        current.BG,
				Bold:      current.Bold,
				Italic:    current.Italic,
				Underline: current.Underline,
			}
			segments = append(segments, seg)
			start = i
			current = nextStyle
		}
	}
	return relayScreenStyledLine{Segments: segments}
}

func (s *relayScreenState) snapshot(limit int) ([]string, []relayScreenStyledLine, int, int, bool) {
	if limit <= 0 {
		limit = relayScreenDefaultLimit
	}
	if len(s.rows) == 0 {
		return []string{}, []relayScreenStyledLine{}, 0, 0, false
	}

	end := s.cursorRow
	for i := len(s.rows) - 1; i >= 0; i-- {
		if strings.TrimRight(string(s.rows[i]), " ") != "" {
			if i > end {
				end = i
			}
			break
		}
	}
	if end >= len(s.rows) {
		end = len(s.rows) - 1
	}
	if end < 0 {
		end = 0
	}

	all := make([]string, 0, end+1)
	allStyled := make([]relayScreenStyledLine, 0, end+1)
	for i := 0; i <= end; i++ {
		row := s.rows[i]
		styleRow := s.styles[i]
		trimmedLen := len(row)
		for trimmedLen > 0 && row[trimmedLen-1] == ' ' {
			trimmedLen--
		}
		trimmedRunes := append([]rune(nil), row[:trimmedLen]...)
		trimmedStyles := make([]relayCellStyle, trimmedLen)
		for idx := 0; idx < trimmedLen; idx++ {
			if idx < len(styleRow) {
				trimmedStyles[idx] = styleRow[idx]
			}
		}
		all = append(all, string(trimmedRunes))
		allStyled = append(allStyled, buildStyledLineSegments(trimmedRunes, trimmedStyles))
	}

	start := 0
	truncated := false
	if len(all) > limit {
		start = len(all) - limit
		truncated = true
	}
	lines := append([]string(nil), all[start:]...)
	styledLines := append([]relayScreenStyledLine(nil), allStyled[start:]...)
	cursorRow := s.cursorRow - start
	if cursorRow < 0 {
		cursorRow = 0
	}
	if cursorRow >= len(lines) {
		cursorRow = len(lines) - 1
		if cursorRow < 0 {
			cursorRow = 0
		}
	}
	return lines, styledLines, cursorRow, s.cursorCol, truncated
}

func (s *relayStore) listOutput(sessionID string, afterSeq, beforeSeq int64, limit int, tail bool) ([]relayOutputEvent, bool) {
	if limit <= 0 {
		limit = 120
	}
	if limit > 400 {
		limit = 400
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	queue := s.outputQ[sessionID]
	if len(queue) == 0 {
		return nil, false
	}

	if tail {
		start := len(queue) - limit
		if start < 0 {
			start = 0
		}
		out := append([]relayOutputEvent(nil), queue[start:]...)
		return out, start > 0
	}

	if beforeSeq > 0 {
		idx := sort.Search(len(queue), func(i int) bool { return queue[i].Seq >= beforeSeq })
		end := idx
		if end < 0 {
			end = 0
		}
		start := end - limit
		if start < 0 {
			start = 0
		}
		if start >= end {
			return nil, start > 0
		}
		out := append([]relayOutputEvent(nil), queue[start:end]...)
		return out, start > 0
	}

	start := sort.Search(len(queue), func(i int) bool { return queue[i].Seq > afterSeq })
	if start >= len(queue) {
		return nil, start > 0
	}
	end := start + limit
	if end > len(queue) {
		end = len(queue)
	}
	out := append([]relayOutputEvent(nil), queue[start:end]...)
	return out, start > 0
}

type relayReporter struct {
	enabled              bool
	URL                  string
	PullURL              string
	OutputURL            string
	Token                string
	SessionID            string
	SessionName          string
	Prefix               string
	Host                 string
	CWD                  string
	Command              string
	PID                  int
	LockFile             string
	client               *http.Client
	lastSent             int64
	interval             time.Duration
	lastPull             int64
	pullEvery            time.Duration
	outputMu             sync.Mutex
	outputPending        []string
	outputRawPending     []string
	outputPendingTS      int64
	outputFlushScheduled bool
	outputFlushInterval  time.Duration
	outputMaxPending     int
}

func newRelayReporter(args []string, lockPath string, debug bool) *relayReporter {
	relayURL, relayFallback := resolveRelayHeartbeatURLWithMeta()
	if relayURL == "" {
		return &relayReporter{}
	}
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	prefix := strings.TrimSpace(os.Getenv("DO_AI_SESSION_PREFIX"))
	if prefix == "" {
		prefix = "do"
	}
	sessionName := strings.TrimSpace(os.Getenv("DO_AI_SESSION_NAME"))
	if sessionName == "" {
		if len(args) > 0 {
			sessionName = args[0]
		} else {
			sessionName = "unknown"
		}
	}
	sessionID := strings.TrimSpace(os.Getenv("DO_AI_SESSION_ID"))
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-%s-%d", prefix, sanitizeIDPart(host), time.Now().UnixNano())
	}
	interval := relayIntervalFromEnv()
	r := &relayReporter{
		enabled:     true,
		URL:         relayURL,
		PullURL:     deriveControlPullURL(relayURL),
		OutputURL:   deriveOutputPushURL(relayURL),
		Token:       envOr("DO_AI_RELAY_TOKEN", hardcodedRelayToken),
		SessionID:   sessionID,
		SessionName: sessionName,
		Prefix:      prefix,
		Host:        host,
		CWD:         cwd,
		Command:     strings.Join(args, " "),
		PID:         os.Getpid(),
		LockFile:    lockPath,
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		interval:            interval,
		pullEvery:           relayPullIntervalFromEnv(),
		outputFlushInterval: relayOutputFlushIntervalFromEnv(),
		outputMaxPending:    relayOutputMaxPendingFromEnv(),
	}
	if debug {
		if relayFallback {
			fmt.Fprintf(os.Stderr, "[CRITICAL] [STATE_INVALID] DO_AI_RELAY_URL=%q 本地不可达，已回退到默认 relay=%s\n", strings.TrimSpace(os.Getenv("DO_AI_RELAY_URL")), relayURL)
		}
		fmt.Fprintf(os.Stderr, "[do-ai] relay 上报已启用: %s session_id=%s\n", relayURL, sessionID)
	}
	return r
}

func (r *relayReporter) Report(hb relayHeartbeat, debug bool) {
	if !r.enabled {
		return
	}
	if r.interval > 0 {
		now := time.Now().UnixNano()
		last := atomic.LoadInt64(&r.lastSent)
		if now-last < r.interval.Nanoseconds() {
			return
		}
		if !atomic.CompareAndSwapInt64(&r.lastSent, last, now) {
			return
		}
	}
	go r.send(hb, debug)
}

func (r *relayReporter) ReportSync(hb relayHeartbeat, debug bool) {
	if !r.enabled {
		return
	}
	r.send(hb, debug)
}

func (r *relayReporter) send(hb relayHeartbeat, debug bool) {
	hb.LockFile = r.LockFile
	body, err := json.Marshal(hb)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 上报编码失败:", err)
		}
		return
	}
	req, err := http.NewRequest(http.MethodPost, r.URL, bytes.NewReader(body))
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 请求创建失败:", err)
		}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
		req.Header.Set("X-Relay-Token", r.Token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 上报失败:", err)
		}
		return
	}
	_ = resp.Body.Close()
	if debug && resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "[do-ai] relay 上报状态异常: %s\n", resp.Status)
	}
}

func (r *relayReporter) ReportOutput(lines []string, ts int64, debug bool) {
	r.reportOutput(lines, nil, ts, debug)
}

func (r *relayReporter) ReportOutputChunk(chunk []byte, ts int64, debug bool) {
	if len(chunk) == 0 {
		return
	}
	raw := append([]byte(nil), chunk...)
	r.reportOutput(splitOutputLines(chunk), raw, ts, debug)
}

func (r *relayReporter) reportOutput(lines []string, rawChunk []byte, ts int64, debug bool) {
	if !r.enabled || r.OutputURL == "" || r.SessionID == "" {
		return
	}
	cleanLines := normalizeOutputLines(lines)
	if len(cleanLines) == 0 && len(rawChunk) == 0 {
		return
	}

	r.outputMu.Lock()
	if len(cleanLines) > 0 {
		r.outputPending = append(r.outputPending, cleanLines...)
	}
	if r.outputMaxPending > 0 && len(r.outputPending) > r.outputMaxPending {
		r.outputPending = append([]string{}, r.outputPending[len(r.outputPending)-r.outputMaxPending:]...)
	}
	if len(rawChunk) > 0 {
		r.outputRawPending = append(r.outputRawPending, base64.StdEncoding.EncodeToString(rawChunk))
		if len(r.outputRawPending) > relayScreenRawChunkMaxKeep {
			r.outputRawPending = append([]string{}, r.outputRawPending[len(r.outputRawPending)-relayScreenRawChunkMaxKeep:]...)
		}
	}
	if ts > r.outputPendingTS {
		r.outputPendingTS = ts
	}
	if r.outputFlushScheduled {
		r.outputMu.Unlock()
		return
	}
	r.outputFlushScheduled = true
	delay := r.outputFlushInterval
	r.outputMu.Unlock()

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		r.flushOutput(debug)
	}()
}

func (r *relayReporter) flushOutput(debug bool) {
	r.outputMu.Lock()
	pending := append([]string(nil), r.outputPending...)
	rawPending := append([]string(nil), r.outputRawPending...)
	ts := r.outputPendingTS
	r.outputPending = nil
	r.outputRawPending = nil
	r.outputPendingTS = 0
	r.outputFlushScheduled = false
	r.outputMu.Unlock()

	if len(pending) == 0 && len(rawPending) == 0 {
		return
	}
	r.sendOutputBatch(pending, rawPending, ts, debug)
}

func (r *relayReporter) sendOutputBatch(lines, rawChunks []string, ts int64, debug bool) {
	reqBody := relayOutputPushRequest{
		SessionID: r.SessionID,
		Lines:     lines,
		RawChunks: rawChunks,
		TS:        ts,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 输出上报码失败:", err)
		}
		return
	}
	req, err := http.NewRequest(http.MethodPost, r.OutputURL, bytes.NewReader(body))
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 输出上报请求失败:", err)
		}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
		req.Header.Set("X-Relay-Token", r.Token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 输出上报失败:", err)
		}
		return
	}
	_ = resp.Body.Close()
	if debug {
		if resp.StatusCode >= 300 {
			fmt.Fprintf(os.Stderr, "[do-ai] relay 输出上报状态异常: %s\n", resp.Status)
		} else {
			fmt.Fprintf(os.Stderr, "[do-ai] relay 输出上报成功: lines=%d raw=%d status=%s\n", len(lines), len(rawChunks), resp.Status)
		}
	}
}

func (r *relayReporter) PullCommands(debug bool) []relayControlCommand {
	if !r.enabled {
		return nil
	}
	if r.pullEvery > 0 {
		now := time.Now().UnixNano()
		last := atomic.LoadInt64(&r.lastPull)
		if now-last < r.pullEvery.Nanoseconds() {
			return nil
		}
		if !atomic.CompareAndSwapInt64(&r.lastPull, last, now) {
			return nil
		}
	}
	return r.pull(debug)
}

func (r *relayReporter) pull(debug bool) []relayControlCommand {
	if r.PullURL == "" || r.SessionID == "" {
		return nil
	}
	pullURL := r.PullURL
	if strings.Contains(pullURL, "?") {
		pullURL += "&session_id=" + url.QueryEscape(r.SessionID) + "&limit=8"
	} else {
		pullURL += "?session_id=" + url.QueryEscape(r.SessionID) + "&limit=8"
	}
	req, err := http.NewRequest(http.MethodGet, pullURL, nil)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 拉取指令创建失败:", err)
		}
		return nil
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
		req.Header.Set("X-Relay-Token", r.Token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 拉取指令失败:", err)
		}
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 300 {
		if debug {
			fmt.Fprintf(os.Stderr, "[do-ai] relay 拉取指令状态异常: %s\n", resp.Status)
		}
		return nil
	}
	var out relayControlPullResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(&out); err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[do-ai] relay 拉取指令解析失败:", err)
		}
		return nil
	}
	return out.Commands
}

func summarizeOutputChunk(out []byte) string {
	if len(out) == 0 {
		return ""
	}
	clean := stripANSIAndControl(out)
	clean = strings.ReplaceAll(clean, "\r", "\n")
	lines := strings.Split(clean, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if len(line) > 240 {
			return line[:240]
		}
		return line
	}
	return ""
}

func stripANSIAndControl(in []byte) string {
	var b strings.Builder
	for i := 0; i < len(in); {
		if in[i] == 0x1b {
			i = skipANSIEscape(in, i)
			continue
		}
		ch := in[i]
		if ch == '\n' || ch == '\r' || ch == '\t' || (ch >= 0x20 && ch != 0x7f) {
			b.WriteByte(ch)
		}
		i++
	}
	return b.String()
}

func valueFromAtomicString(v *atomic.Value) string {
	if v == nil {
		return ""
	}
	raw := v.Load()
	str, ok := raw.(string)
	if !ok {
		return ""
	}
	return str
}

func sanitizeIDPart(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "host"
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteByte(ch)
		case ch >= '0' && ch <= '9':
			b.WriteByte(ch)
		case ch == '-' || ch == '_':
			b.WriteByte(ch)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeHeartbeatURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	path := strings.TrimSpace(u.Path)
	if path == "" || path == "/" {
		u.Path = "/api/v1/heartbeat"
	}
	return u.String()
}

func defaultRelayHeartbeatURL() string {
	return normalizeHeartbeatURL(fmt.Sprintf("http://%s:18787", defaultRelayDomain))
}

func relayAutoEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DO_AI_RELAY_AUTO"))) {
	case "0", "false", "off", "no", "disable", "disabled":
		return false
	default:
		return true
	}
}

func relayURLStrictMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DO_AI_RELAY_URL_STRICT"))) {
	case "1", "true", "on", "yes", "strict":
		return true
	default:
		return false
	}
}

func shouldFallbackLocalRelayURL(relayURL string) bool {
	if relayURLStrictMode() || !relayAutoEnabled() {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(relayURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	return !relayReachabilityChecker(u, 350*time.Millisecond)
}

func defaultRelayReachabilityChecker(u *url.URL, timeout time.Duration) bool {
	if u == nil {
		return false
	}
	port := strings.TrimSpace(u.Port())
	if port == "" {
		if strings.EqualFold(strings.TrimSpace(u.Scheme), "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(strings.TrimSpace(u.Hostname()), port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func resolveRelayHeartbeatURLWithMeta() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("DO_AI_RELAY_URL")); v != "" {
		normalized := normalizeHeartbeatURL(v)
		if shouldFallbackLocalRelayURL(normalized) {
			return defaultRelayHeartbeatURL(), true
		}
		return normalized, false
	}
	if !relayAutoEnabled() {
		return "", false
	}
	return defaultRelayHeartbeatURL(), false
}

func resolveRelayHeartbeatURL() string {
	resolved, _ := resolveRelayHeartbeatURLWithMeta()
	return resolved
}

func relayIntervalFromEnv() time.Duration {
	if val := strings.TrimSpace(os.Getenv("DO_AI_RELAY_INTERVAL")); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d >= 0 {
			return d
		}
	}
	return 3 * time.Second
}

func relayPullIntervalFromEnv() time.Duration {
	if val := strings.TrimSpace(os.Getenv("DO_AI_RELAY_PULL_INTERVAL")); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d >= 0 {
			return d
		}
	}
	return 2 * time.Second
}

func relayOutputFlushIntervalFromEnv() time.Duration {
	if val := strings.TrimSpace(os.Getenv("DO_AI_RELAY_OUTPUT_FLUSH_INTERVAL")); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			if d < 0 {
				return 0
			}
			return d
		}
	}
	return 220 * time.Millisecond
}

func relayOutputMaxPendingFromEnv() int {
	if val := strings.TrimSpace(os.Getenv("DO_AI_RELAY_OUTPUT_MAX_PENDING")); val != "" {
		n, err := strconv.Atoi(val)
		if err == nil {
			if n < 20 {
				return 20
			}
			if n > 4000 {
				return 4000
			}
			return n
		}
	}
	return 240
}

func normalizeOutputLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(stripANSIAndControl([]byte(line)))
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 1024 {
			trimmed = trimmed[:1024]
		}
		out = append(out, trimmed)
	}
	return out
}

func validControlSendRequest(input string, submit bool, action string) bool {
	if action == relayActionTerminate {
		return true
	}
	if submit {
		return true
	}
	return input != ""
}

func normalizeControlAction(raw string) (string, bool) {
	action := strings.ToLower(strings.TrimSpace(raw))
	switch action {
	case "":
		return "", true
	case relayActionTerminate, "stop", "close", "kill":
		return relayActionTerminate, true
	default:
		return "", false
	}
}

func deriveOutputPushURL(heartbeatURL string) string {
	u, err := url.Parse(strings.TrimSpace(heartbeatURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Path = "/api/v1/output/push"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func deriveControlPullURL(heartbeatURL string) string {
	u, err := url.Parse(strings.TrimSpace(heartbeatURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Path = "/api/v1/control/pull"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

type relayNotifier struct {
	client             *http.Client
	webhooks           []string
	telegramBotToken   string
	telegramChatID     string
	telegramParseMode  string
	notificationPrefix string
}

func newRelayNotifier(webhookCSV, botToken, chatID string) *relayNotifier {
	return &relayNotifier{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		webhooks:           splitCSV(webhookCSV),
		telegramBotToken:   strings.TrimSpace(botToken),
		telegramChatID:     strings.TrimSpace(chatID),
		telegramParseMode:  "",
		notificationPrefix: "[do-ai]",
	}
}

func (n *relayNotifier) Notify(title, message string, debug bool) {
	if n == nil {
		return
	}
	text := strings.TrimSpace(fmt.Sprintf("%s %s\n%s", n.notificationPrefix, title, message))
	for _, endpoint := range n.webhooks {
		payload := map[string]string{"title": title, "message": message, "text": text}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			if debug {
				fmt.Fprintln(os.Stderr, "[relay] webhook 请求构建失败:", err)
			}
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := n.client.Do(req)
		if err != nil {
			if debug {
				fmt.Fprintln(os.Stderr, "[relay] webhook 推送失败:", err)
			}
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if debug && resp.StatusCode >= 300 {
			fmt.Fprintf(os.Stderr, "[relay] webhook 返回状态异常: %s\n", resp.Status)
		}
	}
	if n.telegramBotToken != "" && n.telegramChatID != "" {
		n.sendTelegram(text, debug)
	}
}

func (n *relayNotifier) sendTelegram(text string, debug bool) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.telegramBotToken)
	payload := map[string]string{
		"chat_id": n.telegramChatID,
		"text":    text,
	}
	if n.telegramParseMode != "" {
		payload["parse_mode"] = n.telegramParseMode
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[relay] Telegram 请求构建失败:", err)
		}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		if debug {
			fmt.Fprintln(os.Stderr, "[relay] Telegram 推送失败:", err)
		}
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if debug && resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "[relay] Telegram 返回状态异常: %s\n", resp.Status)
	}
}

type relayAlert struct {
	key   string
	title string
	body  string
}

func runRelayServer(args []string) int {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", envOr("DO_AI_RELAY_LISTEN", ":8787"), "监听地址")
	token := fs.String("token", envOr("DO_AI_RELAY_TOKEN", hardcodedRelayToken), "上报鉴权 token")
	staleSeconds := fs.Int64("stale-seconds", envInt64("DO_AI_RELAY_STALE_SECONDS", 20), "离线判定秒数")
	idleAlertSeconds := fs.Int64("idle-alert", envInt64("DO_AI_ALERT_IDLE_SECS", 180), "空闲告警阈值（秒）")
	alertKeywordsRaw := fs.String("keywords", envOr("DO_AI_ALERT_KEYWORDS", "panic,error,exception,confirm,请选择,是否继续"), "关键词告警")
	alertCooldown := fs.Duration("alert-cooldown", envDuration("DO_AI_ALERT_COOLDOWN", 3*time.Minute), "告警冷却时间")
	notifyWebhook := fs.String("notify-webhook", strings.TrimSpace(os.Getenv("DO_AI_NOTIFY_WEBHOOK")), "Webhook 地址，逗号分隔")
	telegramToken := fs.String("telegram-token", strings.TrimSpace(os.Getenv("DO_AI_TELEGRAM_BOT_TOKEN")), "Telegram bot token")
	telegramChatID := fs.String("telegram-chat-id", strings.TrimSpace(os.Getenv("DO_AI_TELEGRAM_CHAT_ID")), "Telegram chat id")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	keywords := splitCSV(*alertKeywordsRaw)
	store := newRelayStore()
	notifier := newRelayNotifier(*notifyWebhook, *telegramToken, *telegramChatID)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	mux.HandleFunc("/api/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizedRelayRequest(r, *token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		var hb relayHeartbeat
		if err := dec.Decode(&hb); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		hb.SessionID = strings.TrimSpace(hb.SessionID)
		if hb.SessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		if hb.State == "" {
			hb.State = "running"
		}
		if hb.Host == "" {
			hb.Host = hostFromRemoteAddr(r.RemoteAddr)
		}
		if hb.UpdatedAt <= 0 {
			hb.UpdatedAt = time.Now().Unix()
		}
		store.upsert(hb)

		alerts := buildAlerts(hb, *idleAlertSeconds, keywords)
		for _, alert := range alerts {
			if !store.allowNotify(alert.key, *alertCooldown) {
				continue
			}
			go notifier.Notify(alert.title, alert.body, os.Getenv("DO_AI_DEBUG") == "1")
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		showAll := r.URL.Query().Get("all") == "1"
		onlyOnline := !showAll
		items := store.list(*staleSeconds, onlyOnline)
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{"sessions": items, "count": len(items), "ts": time.Now().Unix(), "online_only": onlyOnline})
	})

	mux.HandleFunc("/api/v1/control/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizedRelayRequest(r, *token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req relayControlSendRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		req.SessionID = strings.TrimSpace(req.SessionID)
		if req.SessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
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
		if len(req.Input) > 4096 {
			req.Input = req.Input[:4096]
		}
		req.Source = strings.TrimSpace(req.Source)
		if req.Source == "" {
			req.Source = "api"
		}
		cmd := store.enqueueCommand(req.SessionID, req.Input, req.Submit, req.Source, req.Action)
		if req.Action == relayActionTerminate {
			store.markSessionStopping(req.SessionID, req.Source)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "command": cmd})
	})

	mux.HandleFunc("/api/v1/control/pull", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizedRelayRequest(r, *token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		limit := 8
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			if n, err := strconv.Atoi(rawLimit); err == nil {
				limit = n
			}
		}
		commands := store.pullCommands(sessionID, limit)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(relayControlPullResponse{
			Commands: commands,
			Count:    len(commands),
			TS:       time.Now().Unix(),
		})
	})

	mux.HandleFunc("/api/v1/output/push", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizedRelayRequest(r, *token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var req relayOutputPushRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		req.SessionID = strings.TrimSpace(req.SessionID)
		if req.SessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
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
		created := store.appendOutputWithRaw(req.SessionID, req.Lines, rawChunks, req.TS)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "count": len(created), "ts": time.Now().Unix()})
	})

	mux.HandleFunc("/api/v1/output/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizedRelayRequest(r, *token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		limit := 200
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			if n, err := strconv.Atoi(rawLimit); err == nil {
				limit = n
			}
		}
		afterSeq := int64(0)
		if rawAfter := strings.TrimSpace(r.URL.Query().Get("after")); rawAfter != "" {
			if n, err := strconv.ParseInt(rawAfter, 10, 64); err == nil {
				afterSeq = n
			}
		}
		beforeSeq := int64(0)
		if rawBefore := strings.TrimSpace(r.URL.Query().Get("before")); rawBefore != "" {
			if n, err := strconv.ParseInt(rawBefore, 10, 64); err == nil {
				beforeSeq = n
			}
		}
		tail := r.URL.Query().Get("tail") == "1"
		events, hasMoreBefore := store.listOutput(sessionID, afterSeq, beforeSeq, limit, tail)
		cursor := int64(0)
		if len(events) > 0 {
			cursor = events[len(events)-1].Seq
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(relayOutputListResponse{
			Events:        events,
			Count:         len(events),
			Cursor:        cursor,
			HasMoreBefore: hasMoreBefore,
			TS:            time.Now().Unix(),
		})
	})

	mux.HandleFunc("/api/v1/output/screen", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizedRelayRequest(r, *token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sessionID == "" {
			http.Error(w, "missing session_id", http.StatusBadRequest)
			return
		}
		limit := relayScreenDefaultLimit
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			if n, err := strconv.Atoi(rawLimit); err == nil {
				limit = n
			}
		}
		if limit < 10 {
			limit = 10
		}
		if limit > relayScreenMaxLimit {
			limit = relayScreenMaxLimit
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.getOutputScreen(sessionID, limit))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(relayIndexHTML))
	})

	server := &http.Server{
		Addr:              *listen,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "[relay] listening on %s\n", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "[relay] 启动失败:", err)
		return 1
	}
	return 0
}

func buildAlerts(hb relayHeartbeat, idleThreshold int64, keywords []string) []relayAlert {
	alerts := make([]relayAlert, 0, 2)
	name := hb.SessionID
	if hb.SessionName != "" {
		name = hb.SessionName
	}
	if hb.State == "running" && idleThreshold > 0 && hb.IdleSeconds >= idleThreshold {
		alerts = append(alerts, relayAlert{
			key:   fmt.Sprintf("%s:idle", hb.SessionID),
			title: fmt.Sprintf("会话长时间无输出: %s", name),
			body:  fmt.Sprintf("host=%s idle=%ds cwd=%s", hb.Host, hb.IdleSeconds, hb.CWD),
		})
	}
	if keyword := firstMatchedKeyword(hb.LastText, keywords); keyword != "" {
		alerts = append(alerts, relayAlert{
			key:   fmt.Sprintf("%s:kw:%s", hb.SessionID, keyword),
			title: fmt.Sprintf("会话触发关键词[%s]: %s", keyword, name),
			body:  fmt.Sprintf("host=%s line=%s", hb.Host, hb.LastText),
		})
	}
	if hb.State == "exited" && hb.ExitCode != nil && *hb.ExitCode != 0 {
		alerts = append(alerts, relayAlert{
			key:   fmt.Sprintf("%s:exit:%d", hb.SessionID, *hb.ExitCode),
			title: fmt.Sprintf("会话异常退出: %s", name),
			body:  fmt.Sprintf("host=%s exit_code=%d cmd=%s", hb.Host, *hb.ExitCode, hb.Command),
		})
	}
	return alerts
}

func firstMatchedKeyword(text string, keywords []string) string {
	clean := strings.ToLower(strings.TrimSpace(text))
	if clean == "" {
		return ""
	}
	for _, keyword := range keywords {
		k := strings.ToLower(strings.TrimSpace(keyword))
		if k == "" {
			continue
		}
		if strings.Contains(clean, k) {
			return keyword
		}
	}
	return ""
}

func authorizedRelayRequest(r *http.Request, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return true
	}
	if got := strings.TrimSpace(r.Header.Get("X-Relay-Token")); got == token {
		return true
	}
	if got := strings.TrimSpace(r.URL.Query().Get("token")); got == token {
		return true
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		if strings.TrimSpace(authz[7:]) == token {
			return true
		}
	}
	return false
}

func hostFromRemoteAddr(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr)); err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(remoteAddr), "[]")
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func envOr(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

func envInt64(key string, def int64) int64 {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return def
	}
	return d
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Relay-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const relayIndexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>do-ai relay</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif; margin: 0; background: #0f172a; color: #e2e8f0; }
    .wrap { max-width: 980px; margin: 0 auto; padding: 14px; }
    .head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; }
    .badge { font-size: 12px; background: #1e293b; padding: 4px 8px; border-radius: 999px; }
    .grid { display: grid; gap: 10px; }
    .card { background: #111827; border: 1px solid #334155; border-radius: 10px; padding: 10px; }
    .line { margin: 6px 0; font-size: 13px; word-break: break-all; }
    .name { font-weight: 700; font-size: 14px; }
    .ok { color: #22c55e; }
    .bad { color: #f87171; }
    .warn { color: #f59e0b; }
    .mono { font-family: ui-monospace, Menlo, Monaco, monospace; font-size: 12px; }
    input { width: 100%; border-radius: 8px; border: 1px solid #334155; background: #020617; color: #e2e8f0; padding: 8px; }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="head">
      <h2 style="margin:0">do-ai 在线会话看板</h2>
      <span id="meta" class="badge">loading...</span>
    </div>
    <input id="filter" placeholder="过滤：session / host / cwd / 命令" />
    <div id="list" class="grid" style="margin-top:10px"></div>
  </div>
  <script>
    const listEl = document.getElementById('list');
    const metaEl = document.getElementById('meta');
    const filterEl = document.getElementById('filter');
    let sessions = [];

    function esc(s){ return (s||'').replaceAll('<','&lt;').replaceAll('>','&gt;') }
    function fmtTime(ts){ if(!ts) return '-'; return new Date(ts * 1000).toLocaleString(); }
    function render(){
      const kw = (filterEl.value || '').trim().toLowerCase();
      const filtered = sessions.filter(s => {
        if (!kw) return true;
        const hay = [s.session_id,s.session_name,s.host,s.cwd,s.command,s.last_text].join(' ').toLowerCase();
        return hay.includes(kw);
      });
      metaEl.textContent = 'sessions=' + filtered.length + '/' + sessions.length + '  ' + new Date().toLocaleTimeString();
      listEl.innerHTML = filtered.map(s => {
        const stateText = s.online ? '<span class="ok">在线</span>' : '<span class="bad">离线</span>';
        const idleCls = s.idle_seconds >= 180 ? 'warn' : 'ok';
        return '<div class="card">'
          + '<div class="name">' + esc(s.session_name || s.session_id) + ' · ' + stateText + '</div>'
          + '<div class="line">host: <span class="mono">' + esc(s.host || '-') + '</span></div>'
          + '<div class="line">cwd: <span class="mono">' + esc(s.cwd || '-') + '</span></div>'
          + '<div class="line">cmd: <span class="mono">' + esc(s.command || '-') + '</span></div>'
          + '<div class="line">idle: <span class="mono ' + idleCls + '">' + (s.idle_seconds || 0) + 's</span> · kicks: <span class="mono">' + (s.kick_count || 0) + '</span></div>'
          + '<div class="line">last output: <span class="mono">' + fmtTime(s.last_output_at) + '</span></div>'
          + '<div class="line">last text: <span class="mono">' + esc(s.last_text || '-') + '</span></div>'
        + '</div>';
      }).join('');
      if (!filtered.length) {
        listEl.innerHTML = '<div class="card">暂无在线会话</div>';
      }
    }

    async function pull(){
      try {
        const resp = await fetch('/api/v1/sessions', { cache: 'no-store' });
        const data = await resp.json();
        sessions = Array.isArray(data.sessions) ? data.sessions : [];
        render();
      } catch (e) {
        metaEl.textContent = '拉取失败';
      }
    }

    filterEl.addEventListener('input', render);
    pull();
    setInterval(pull, 3000);
  </script>
</body>
</html>`
