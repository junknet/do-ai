import { StatusBar } from 'expo-status-bar';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import * as Haptics from 'expo-haptics';
import Constants from 'expo-constants';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { JetBrainsMono_400Regular, JetBrainsMono_500Medium, useFonts } from '@expo-google-fonts/jetbrains-mono';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Animated,
  BackHandler,
  Dimensions,
  FlatList,
  GestureResponderEvent,
  Keyboard,
  LogBox,
  Modal,
  PanResponder,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';

// --- Types ---

type Session = {
  session_id: string;
  session_name?: string;
  host: string;
  cwd?: string;
  command?: string;
  last_text?: string;
  idle_seconds?: number;
  kick_count?: number;
  last_bell_at?: number;
  online?: boolean;
  state?: string;
};

type SessionsResponse = {
  sessions: Session[];
  count: number;
  ts: number;
  online_only: boolean;
};

type StyledSegment = {
  text: string;
  fg?: string;
  bg?: string;
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
};

type StyledLine = {
  segments: StyledSegment[];
};

type OutputEvent = {
  seq: number;
  session_id: string;
  text: string;
  ts: number;
  segments?: StyledSegment[];
};

type OutputListResponse = {
  events: OutputEvent[];
  count: number;
  cursor: number;
  has_more_before: boolean;
  ts: number;
};

type OutputScreenResponse = {
  session_id: string;
  lines: string[];
  styled_lines?: StyledLine[];
  line_count: number;
  cursor_row: number;
  cursor_col: number;
  revision: number;
  truncated: boolean;
  ts: number;
};

// --- Config ---

function relayExtraString(key: string): string {
  const expo = Constants as unknown as {
    expoConfig?: {
      extra?: Record<string, unknown>;
    };
  };
  const raw = expo.expoConfig?.extra?.[key];
  if (typeof raw !== 'string') return '';
  return raw.trim();
}

const RELAY_URL_PRIMARY = relayExtraString('relayUrlPrimary');
const RELAY_URL_LOCAL = relayExtraString('relayUrlLocal') || 'http://127.0.0.1:18787';
const RELAY_URL_CANDIDATES = [RELAY_URL_LOCAL, RELAY_URL_PRIMARY].filter(
  (url, index, all) => url.length > 0 && all.indexOf(url) === index,
);
const RELAY_TOKEN = relayExtraString('relayToken');
const RELAY_REQUEST_TIMEOUT_MS = 2200;

const SESSION_REFRESH_MS = 2500;
const TERMINAL_REFRESH_MS = 150;
const ALERT_KEYWORDS = ['confirm', '是否继续', '请选择', 'panic', 'error', 'exception'];
const TERMINATE_SENTINEL = '__DO_AI_TERMINATE__';
const CLOSE_SESSION_HIDE_MS = 15000;
const TERMINATE_UNDO_WINDOW_MS = 2000;
const TERMINAL_FONT_SIZE_BASE = 11;
const TERMINAL_FONT_SIZE_MIN = 9;
const TERMINAL_FONT_SIZE_MAX = 22;
const TERMINAL_LINE_HEIGHT_RATIO = 15 / 11;
const TERMINAL_FONT_SIZE_STORAGE_KEY = 'doai:mobile:terminal-font-size:v1';
const TERMINAL_BELL_CHAR = '\u0007';
const TERMINAL_BELL_COOLDOWN_MS = 2500;
const TERMINAL_XTERM_DEFAULT_FG = '#c5c8c6';
const TERMINAL_XTERM_DEFAULT_BG = '#1e1e1e';

if (__DEV__) {
  LogBox.ignoreLogs(['Open debugger to view warnings.']);
}

// --- Notifications ---

Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowAlert: true,
    shouldPlaySound: false,
    shouldSetBadge: false,
    shouldShowBanner: true,
    shouldShowList: true,
  }),
});

// --- Utils ---

function nowTraceId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
}

function clampNumber(value: number, min: number, max: number): number {
  if (value <= min) return min;
  if (value >= max) return max;
  return value;
}

function getPinchDistance(event: GestureResponderEvent): number | null {
  const touches = event.nativeEvent.touches;
  if (!touches || touches.length < 2) return null;
  const [first, second] = touches;
  return Math.hypot(second.pageX - first.pageX, second.pageY - first.pageY);
}

function logState(level: 'info' | 'error', data: Record<string, unknown>) {
  const line = JSON.stringify({
    level,
    ts: new Date().toISOString(),
    ...data,
  });
  if (level === 'error') {
    console.error(line);
    return;
  }
  console.log(line);
}

function pathLeaf(rawPath?: string): string {
  if (!rawPath) return '';
  const normalized = rawPath.trim().replace(/\\/g, '/').replace(/\/+/g, '/').replace(/\/+$/g, '');
  if (!normalized) return '';
  const parts = normalized.split('/');
  const leaf = parts[parts.length-1]?.trim();
  return leaf || '';
}

function sessionTitle(session: Session): string {
  const cwdName = pathLeaf(session.cwd);
  if (cwdName) {
    return cwdName;
  }
  const text = session.session_name?.trim();
  return text && text.length > 0 ? text : session.session_id;
}

function titleInitial(text: string): string {
  if (!text) return '?';
  const first = text.trim().charAt(0);
  return first ? first.toUpperCase() : '?';
}

function idleLabel(idleSeconds?: number): string {
  if (!Number.isFinite(idleSeconds)) return '--';
  const value = Math.max(0, Math.floor(idleSeconds ?? 0));
  if (value < 60) return `${value}s`;
  if (value < 3600) return `${Math.floor(value / 60)}m`;
  return `${Math.floor(value / 3600)}h`;
}

function containsAlertKeyword(text: string): string {
  const lower = text.toLowerCase();
  for (const keyword of ALERT_KEYWORDS) {
    if (lower.includes(keyword.toLowerCase())) return keyword;
  }
  return '';
}

function containsBellSignal(text: string): boolean {
  return text.includes(TERMINAL_BELL_CHAR);
}

function sessionSnapshot(session: Session): string {
  return [
    session.session_id,
    session.session_name || '',
    session.host || '',
    session.cwd || '',
    session.command || '',
    session.last_text || '',
    Number.isFinite(session.idle_seconds) ? String(Math.floor(session.idle_seconds ?? 0)) : '',
    Number.isFinite(session.kick_count) ? String(Math.floor(session.kick_count ?? 0)) : '',
    session.online === false ? '0' : '1',
  ].join('|');
}

function areSessionListsEquivalent(previous: Session[], next: Session[]): boolean {
  if (previous === next) return true;
  if (previous.length !== next.length) return false;
  const prevKeys = previous.map(sessionSnapshot).sort();
  const nextKeys = next.map(sessionSnapshot).sort();
  for (let i = 0; i < prevKeys.length; i++) {
    if (prevKeys[i] !== nextKeys[i]) return false;
  }
  return true;
}

function areStyledSegmentsEqual(previous?: StyledSegment[], next?: StyledSegment[]): boolean {
  if (previous === next) return true;
  const left = previous ?? [];
  const right = next ?? [];
  if (left.length !== right.length) return false;
  for (let i = 0; i < left.length; i++) {
    const segLeft = left[i];
    const segRight = right[i];
    if (
      segLeft.text !== segRight.text ||
      segLeft.fg !== segRight.fg ||
      segLeft.bg !== segRight.bg ||
      !!segLeft.bold !== !!segRight.bold ||
      !!segLeft.italic !== !!segRight.italic ||
      !!segLeft.underline !== !!segRight.underline
    ) {
      return false;
    }
  }
  return true;
}

function areOutputEventsEqual(previous: OutputEvent[], next: OutputEvent[]): boolean {
  if (previous === next) return true;
  if (previous.length !== next.length) return false;
  for (let i = 0; i < previous.length; i++) {
    const left = previous[i];
    const right = next[i];
    if (
      left.seq !== right.seq ||
      left.ts !== right.ts ||
      left.session_id !== right.session_id ||
      left.text !== right.text ||
      !areStyledSegmentsEqual(left.segments, right.segments)
    ) {
      return false;
    }
  }
  return true;
}

const TMUX_STATUS_PREVIEW_DEFAULT = '等待正文输出…';

function normalizeTerminalText(raw: string): string {
  if (!raw) return '';
  return raw
    .replace(/\u001b\[[0-9;?]*[ -/]*[@-~]/g, '')
    .replace(/\u001b\][^\u0007]*(?:\u0007|\u001b\\)/g, '')
    .replace(/[\u0000-\u0008\u000b-\u001f\u007f]/g, '')
    .replace(/\s+$/g, '');
}

function compactWhitespace(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}

function isTmuxStatusLine(line: string): boolean {
  const text = compactWhitespace(line);
  if (!text) return false;
  if (text === 'B') return true;
  if (/\[[^\]]+@[^ ]+ [^\]]+\]\$/.test(text)) return false;

  const explicitDoAiStatus = /^do_ai_[a-z0-9_]+\s+\d+:[^\s]+\s+do-ai(?:\s+B)?\s+do-ai\s+\d{1,2}:\d{2}(?:\s+B)?$/i;
  if (explicitDoAiStatus.test(text)) return true;

  const hasPane = /\b\d+:[^\s]+\b/.test(text);
  const hasClock = /\b\d{1,2}:\d{2}\b/.test(text);
  const hasDoAiMark = /\bdo-ai\b/.test(text) || /\bdo_ai_[a-z0-9_]+\b/i.test(text);
  return hasPane && hasClock && hasDoAiMark;
}

function sanitizeCharsetLeakArtifacts(line: string): string {
  if (!line) return '';
  let text = line;

  // 典型泄露形态：B + 大量框线字符。
  if (/^B[─━═-]{8,}/u.test(text)) {
    return '';
  }

  // 去除夹在空白/CJK 边界上的孤立 B（常见于 ESC(B 泄露）。
  text = text.replace(/(^|\s)B(?=(\s|[一-鿿]))/gu, '$1');
  text = text.replace(/([一-鿿）\)])B(?=(\s|$))/gu, '$1');

  // 去除 "onB (" 一类夹在英文与括号之间的泄露 B。
  text = text.replace(/([a-z])B(?=\s*[\(（])/gi, '$1');

  // 去除替换字符，减少“方块乱码”视觉噪点。
  text = text.replace(/\uFFFD+/g, '');

  // Claude/Codex TUI 中某些图标在安卓字体缺字，保留正文、移除前缀乱码。
  if (/\bbypass permissions on\b/i.test(text)) {
    text = text.replace(/^[^a-zA-Z0-9]+/, '');
  }

  text = text.replace(/\s{2,}/g, ' ').trim();
  return text;
}

function isTransientTerminalHintLine(text: string): boolean {
  const normalized = compactWhitespace(text);
  if (!normalized) return true;
  const lower = normalized.toLowerCase();

  if (lower === 'chatgpt.com/codex') return true;
  if (/^tab to queue message(?:\s*\d+% context left)?$/i.test(normalized)) return true;
  if (/^\d+% context left$/i.test(normalized)) return true;
  if (lower.includes('context left') && (lower.includes('esc to') || lower.includes('for shortcuts'))) {
    return true;
  }
  if (lower.includes('for shortcuts') && lower.includes('esc to')) return true;
  if (/^working\s*\(.*esc to interrupt\)$/i.test(normalized)) return true;
  if (/\(\d+[smh].*esc to/i.test(lower) && lower.includes('interrupt')) return true;
  if (/^starting mcp servers(?:\s*\(.*\))?$/i.test(normalized)) return true;

  return false;
}

function sanitizeTerminalLine(raw: string): string {
  const cleaned = normalizeTerminalText(raw);
  if (!cleaned) return '';
  if (isTmuxStatusLine(cleaned)) return '';
  if (isTransientTerminalHintLine(cleaned)) return '';
  const normalized = sanitizeCharsetLeakArtifacts(cleaned);
  if (!normalized) return '';
  if (isTmuxStatusLine(normalized)) return '';
  if (isTransientTerminalHintLine(normalized)) return '';
  return normalized;
}

function sanitizeOutputEvents(events: OutputEvent[]): OutputEvent[] {
  return events
    .map((event) => ({
      ...event,
      text: sanitizeTerminalLine(event.text),
    }))
    .filter((event) => event.text.length > 0);
}

function sanitizeScreenLines(lines: string[]): string[] {
  return lines.map((line) => sanitizeTerminalLine(line)).filter((line) => line.length > 0);
}

function comparableTerminalText(raw: string): string {
  return compactWhitespace(sanitizeTerminalLine(raw)).toLowerCase();
}

function isHighSignalTailLine(text: string): boolean {
  if (!text) return false;
  const normalized = compactWhitespace(text);
  if (!normalized) return false;
  if (normalized.startsWith('> ')) return true;
  if (/\[(critical|state_invalid|panic|error)\]/i.test(normalized)) return true;
  if (/(错误|异常|崩溃|失败|超时|拒绝|未找到|权限|回滚|告警|警告)/u.test(normalized)) return true;
  if (/\b(error|errors|exception|panic|fatal|failed|failure|traceback|denied|refused|timeout|timed out|not found|invalid|unauthorized|forbidden|rollback|warn|warning)\b/i.test(normalized)) {
    return true;
  }
  return false;
}

function isLikelyTransientTailNoise(text: string): boolean {
  if (!text) return true;
  const normalized = compactWhitespace(text);
  if (!normalized) return true;
  if (isTransientTerminalHintLine(normalized)) return true;

  if (/^[-•◦·*]\s*[a-z0-9]{1,8}$/iu.test(normalized)) {
    return true;
  }
  if (/^[a-z0-9]{1,6}$/iu.test(normalized) && !/^\d+$/u.test(normalized)) {
    return true;
  }
  if (!/[\u4E00-\u9FFF]/u.test(normalized) && normalized.length <= 4 && !normalized.includes(' ')) {
    return true;
  }

  return false;
}

function hasRichScreenContent(events: OutputEvent[]): boolean {
  if (events.length < 10) return false;
  const visible = events
    .map((event) => compactWhitespace(sanitizeTerminalLine(event.text)))
    .filter((text) => text.length > 0);
  if (visible.length < 10) return false;
  const longLines = visible.filter((text) => text.length >= 18).length;
  return longLines >= 4;
}

function mergeScreenWithTailBuffer(
  screenEvents: OutputEvent[],
  tailEvents: OutputEvent[],
  sessionId: string,
  ts: number,
  options?: {
    allowGeneralTailMerge?: boolean;
  },
): OutputEvent[] {
  if (!screenEvents.length || !tailEvents.length) {
    return screenEvents;
  }

  const allowGeneralTailMerge =
    options?.allowGeneralTailMerge ?? !hasRichScreenContent(screenEvents);

  const seen = new Set<string>();
  for (const event of screenEvents) {
    const key = comparableTerminalText(event.text);
    if (key) {
      seen.add(key);
    }
  }

  const extras: OutputEvent[] = [];
  for (const event of tailEvents) {
    const text = sanitizeTerminalLine(event.text);
    if (!text) {
      continue;
    }
    const highSignal = isHighSignalTailLine(text);
    if (!highSignal) {
      if (!allowGeneralTailMerge) {
        continue;
      }
      if (isLikelyTransientTailNoise(text)) {
        continue;
      }
    }
    const key = comparableTerminalText(text);
    if (key && seen.has(key)) {
      continue;
    }
    if (key) {
      seen.add(key);
    }
    extras.push({
      ...event,
      text,
    });
  }

  if (!extras.length) {
    return screenEvents;
  }

  const mergedTail = extras.slice(-24);
  let seq = screenEvents.reduce((maxSeq, event) => Math.max(maxSeq, event.seq), 0) + 1;
  const output: OutputEvent[] = [...screenEvents];

  for (const event of mergedTail) {
    output.push({
      seq,
      session_id: sessionId,
      text: event.text,
      ts: event.ts || ts,
    });
    seq += 1;
  }

  return output;
}

function normalizeHexColor(raw?: string): string | undefined {
  if (!raw) return undefined;
  const color = raw.trim();
  if (!/^#[0-9a-fA-F]{6}$/.test(color)) return undefined;
  return color.toLowerCase();
}

function sanitizeStyledSegmentText(raw: string): string {
  if (!raw) return '';
  return raw.replace(/[\u0000-\u0008\u000b-\u001f\u007f]/g, '');
}

function sanitizeStyledSegments(segments: StyledSegment[] | undefined): StyledSegment[] {
  if (!Array.isArray(segments) || segments.length === 0) {
    return [];
  }
  const output: StyledSegment[] = [];
  for (const segment of segments) {
    const rawText = typeof segment?.text === 'string' ? segment.text : '';
    const text = sanitizeStyledSegmentText(rawText);
    if (!text) continue;

    const next: StyledSegment = { text };
    const fg = normalizeHexColor(segment.fg);
    const bg = normalizeHexColor(segment.bg);
    if (fg) next.fg = fg;
    if (bg) next.bg = bg;
    if (segment.bold) next.bold = true;
    if (segment.italic) next.italic = true;
    if (segment.underline) next.underline = true;
    output.push(next);
  }
  return output;
}

function buildScreenStyledEvents(
  sessionId: string,
  rawLines: string[],
  styledLines: StyledLine[],
  baseSeq: number,
  ts: number,
): OutputEvent[] {
  const total = Math.max(rawLines.length, styledLines.length);
  if (total === 0) {
    return [];
  }

  const events: OutputEvent[] = [];
  for (let index = 0; index < total; index++) {
    const rawText = typeof rawLines[index] === 'string' ? rawLines[index] : '';
    const segments = sanitizeStyledSegments(styledLines[index]?.segments);
    const lineText = segments.length > 0 ? segments.map((segment) => segment.text).join('') : rawText;
    const sanitizedLineText = sanitizeTerminalLine(lineText);
    if (!sanitizedLineText) {
      continue;
    }
    const normalizedHint = normalizeScreenHintLine(lineText);
    if (normalizedHint && (isTmuxStatusLine(normalizedHint) || normalizedHint === 'B')) {
      continue;
    }

    const next: OutputEvent = {
      seq: baseSeq + events.length,
      session_id: sessionId,
      text: sanitizedLineText,
      ts,
    };
    if (segments.length > 0 && sanitizedLineText === lineText) {
      next.segments = segments;
    }
    events.push(next);
  }
  return events;
}

function terminalSegmentInlineStyle(segment: StyledSegment) {
  const style: {
    color?: string;
    backgroundColor?: string;
    fontWeight?: '700';
    fontStyle?: 'italic';
    textDecorationLine?: 'underline';
  } = {};
  if (segment.fg) {
    style.color = segment.fg;
  }
  if (segment.bg) {
    style.backgroundColor = segment.bg;
  }
  if (segment.bold) {
    style.fontWeight = '700';
  }
  if (segment.italic) {
    style.fontStyle = 'italic';
  }
  if (segment.underline) {
    style.textDecorationLine = 'underline';
  }
  return style;
}

function normalizeScreenHintLine(raw: string): string {
  return compactWhitespace(normalizeTerminalText(raw));
}

function isTmuxStatusOnlyScreen(lines: string[]): boolean {
  if (!lines.length) return false;
  let hasVisible = false;
  for (const raw of lines) {
    const normalized = normalizeScreenHintLine(raw);
    if (!normalized) continue;
    hasVisible = true;
    if (!isTmuxStatusLine(normalized) && normalized !== 'B') {
      return false;
    }
  }
  return hasVisible;
}

function isWeakScreenMarker(line: string): boolean {
  const text = compactWhitespace(line).toLowerCase();
  if (!text) return true;
  const weakTokens = new Set(['updates', 'update', 'loading', '...', '…', '●', 'b']);
  if (weakTokens.has(text)) return true;
  if (/^updates?(\s+\d+)?$/i.test(text)) return true;
  if (/^[.·•…-]+$/u.test(text)) return true;
  return false;
}

function isSparseStatusLikeScreen(lines: string[]): boolean {
  if (!lines.length) return false;
  const visible = lines
    .map((raw) => normalizeScreenHintLine(raw))
    .filter((text) => text.length > 0 && text !== 'B');
  if (visible.length === 0) {
    return lines.length >= 12;
  }

  const statusCount = visible.filter((text) => isTmuxStatusLine(text)).length;
  const nonStatus = visible.filter((text) => !isTmuxStatusLine(text));
  if (lines.length >= 12 && statusCount >= 1 && nonStatus.length > 0 && nonStatus.every((text) => isWeakScreenMarker(text))) {
    return true;
  }

  if (lines.length >= 12 && visible.length <= 2 && nonStatus.length > 0 && nonStatus.every((text) => isWeakScreenMarker(text))) {
    return true;
  }

  return false;
}

function inferPreviewFallback(session: Session): string {
  const signature = `${session.session_name || ''} ${session.command || ''}`.toLowerCase();
  if (signature.includes('claude') || signature.includes('codex') || signature.includes('gemini')) {
    return '等待 TUI 刷新…';
  }
  if (
    signature.includes('bash') ||
    signature.includes('zsh') ||
    signature.includes('fish') ||
    signature.includes(' powershell') ||
    signature.includes(' cmd') ||
    signature.endsWith('sh')
  ) {
    return '等待命令输出…';
  }
  if (signature.includes('python') || signature.includes('node') || signature.includes('npm')) {
    return '等待程序输出…';
  }
  return TMUX_STATUS_PREVIEW_DEFAULT;
}

function sessionPreviewText(session: Session): string {
  const raw = session.last_text;
  if (!raw || !raw.trim()) return 'No output received';
  const sanitized = sanitizeTerminalLine(raw);
  return sanitized || inferPreviewFallback(session);
}

function fetchRelayWithTimeout(baseURL: string, path: string, init?: RequestInit, timeoutMs = RELAY_REQUEST_TIMEOUT_MS): Promise<Response> {
  return new Promise((resolve, reject) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    fetch(`${baseURL}${path}`, {
      ...init,
      signal: controller.signal,
    })
      .then(resolve)
      .catch(reject)
      .finally(() => clearTimeout(timer));
  });
}

async function fetchRelayFirstAvailable(path: string, init?: RequestInit): Promise<{ response: Response; baseURL: string }> {
  let lastError: unknown = null;
  for (const baseURL of RELAY_URL_CANDIDATES) {
    try {
      const response = await fetchRelayWithTimeout(baseURL, path, init);
      return { response, baseURL };
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError instanceof Error ? lastError : new Error('relay unavailable');
}

// --- API ---

async function fetchRelayJson<T>(path: string, init?: RequestInit): Promise<T> {
  const traceId = nowTraceId('relay');
  const startedAt = Date.now();
  
  // logState('info', { trace_id: traceId, event: 'relay_req', path });

  const { response, baseURL } = await fetchRelayFirstAvailable(path, {
    ...init,
    headers: {
      ...(init?.headers || {}),
      ...(RELAY_TOKEN ? { 'X-Relay-Token': RELAY_TOKEN } : {}),
    },
  });

  if (!response.ok) {
    logState('error', {
      trace_id: traceId,
      event: '[CRITICAL] req_fail',
      path,
      relay: baseURL,
      status: response.status,
    });
    throw new Error(`${path} ${response.status}`);
  }

  logState('info', {
    trace_id: traceId,
    event: 'relay_req_ok',
    relay: baseURL,
    path,
    cost_ms: Date.now() - startedAt,
  });

  return (await response.json()) as T;
}

function useRelaySessions() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [lastUpdated, setLastUpdated] = useState(0);

  const fetchSessions = useCallback(async () => {
    try {
      const data = await fetchRelayJson<SessionsResponse>('/api/v1/sessions');
      const incoming = Array.isArray(data.sessions) ? data.sessions : [];
      setSessions((previous) => (areSessionListsEquivalent(previous, incoming) ? previous : incoming));
      setError('');
      setLastUpdated(Date.now());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'network error');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchSessions();
    const timer = setInterval(() => void fetchSessions(), SESSION_REFRESH_MS);
    return () => clearInterval(timer);
  }, [fetchSessions]);

  return { sessions, loading, error, lastUpdated, refetch: fetchSessions };
}

async function sendControl(sessionId: string, input: string, submit: boolean) {
  await fetchRelayJson('/api/v1/control/send', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      session_id: sessionId,
      input,
      submit,
      source: 'doai-mobile',
    }),
  });
}

async function requestTerminateSession(sessionId: string) {
  const payloads = [
    {
      session_id: sessionId,
      input: '',
      submit: false,
      action: 'terminate',
      source: 'doai-mobile-longpress',
    },
    {
      session_id: sessionId,
      input: TERMINATE_SENTINEL,
      submit: false,
      source: 'doai-mobile-longpress-fallback',
    },
  ];

  let lastError: unknown = null;
  for (const body of payloads) {
    try {
      await fetchRelayJson('/api/v1/control/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      return;
    } catch (err) {
      lastError = err;
    }
  }

  throw lastError instanceof Error ? lastError : new Error('terminate request failed');
}

async function fetchOutputTail(sessionId: string): Promise<OutputListResponse> {
  return fetchRelayJson<OutputListResponse>(
    `/api/v1/output/list?session_id=${encodeURIComponent(sessionId)}&tail=1&limit=260`,
  );
}

async function fetchOutputScreen(sessionId: string): Promise<OutputScreenResponse | null> {
  const path = `/api/v1/output/screen?session_id=${encodeURIComponent(sessionId)}&limit=260`;
  const { response } = await fetchRelayFirstAvailable(path, {
    headers: {
      ...(RELAY_TOKEN ? { 'X-Relay-Token': RELAY_TOKEN } : {}),
    },
  });

  if (response.status === 404) {
    return null;
  }

  if (!response.ok) {
    logState('error', {
      trace_id: nowTraceId('relay'),
      event: '[CRITICAL] req_fail',
      path,
      status: response.status,
    });
    throw new Error(`${path} ${response.status}`);
  }

  return (await response.json()) as OutputScreenResponse;
}

async function fetchOutputBefore(sessionId: string, beforeSeq: number): Promise<OutputListResponse> {
  return fetchRelayJson<OutputListResponse>(
    `/api/v1/output/list?session_id=${encodeURIComponent(sessionId)}&before=${beforeSeq}&limit=220`,
  );
}

async function ensureNotificationPermission(): Promise<boolean> {
  if (!Device.isDevice) return false;
  const settings = await Notifications.getPermissionsAsync();
  let granted = settings.granted || settings.ios?.status === Notifications.IosAuthorizationStatus.PROVISIONAL;
  if (!granted) {
    const requested = await Notifications.requestPermissionsAsync();
    granted = requested.granted || requested.ios?.status === Notifications.IosAuthorizationStatus.PROVISIONAL;
  }
  return granted;
}

// --- Components ---

export default function App() {
  const [fontsLoaded] = useFonts({
    JetBrainsMono_400Regular,
    JetBrainsMono_500Medium,
  });

  const { sessions, loading, error, lastUpdated, refetch } = useRelaySessions();

  const [activeSessionId, setActiveSessionId] = useState('');
  const [terminalVisible, setTerminalVisible] = useState(false);

  const [terminalLines, setTerminalLines] = useState<OutputEvent[]>([]);
  const [cursor, setCursor] = useState(0);
  const [hasMoreBefore, setHasMoreBefore] = useState(false);
  const [screenMode, setScreenMode] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [terminalError, setTerminalError] = useState('');

  const [composerVisible, setComposerVisible] = useState(false);
  const [composerCommand, setComposerCommand] = useState('');
  const [terminatingSessionId, setTerminatingSessionId] = useState('');
  const [pendingTerminateSessionId, setPendingTerminateSessionId] = useState('');
  const [closingSessionMap, setClosingSessionMap] = useState<Record<string, number>>({});
  const [toast, setToast] = useState('');

  const [notifyEnabled, setNotifyEnabled] = useState(false);
  const [followTail, setFollowTail] = useState(true);
  const [terminalFontSize, setTerminalFontSize] = useState(TERMINAL_FONT_SIZE_BASE);
  const [tabLayouts, setTabLayouts] = useState<Record<string, { x: number; width: number }>>({});
  const [sessionOrder, setSessionOrder] = useState<string[]>([]);
  const [sortMode, setSortMode] = useState(false);
  const [immersiveMode, setImmersiveMode] = useState(false);
  const [keyboardInset, setKeyboardInset] = useState(0);

  const terminalScrollRef = useRef<ScrollView>(null);
  const terminalTabScrollRef = useRef<ScrollView>(null);
  const composerInputRef = useRef<TextInput>(null);
  const terminalFontSizeHydratedRef = useRef(false);
  const terminalFontSizeRef = useRef(TERMINAL_FONT_SIZE_BASE);
  const pinchStartDistanceRef = useRef(0);
  const pinchStartFontSizeRef = useRef(TERMINAL_FONT_SIZE_BASE);
  const pinchActiveRef = useRef(false);
  const notifiedLineRef = useRef<Record<string, string>>({});
  const bellAlertAtRef = useRef<Record<string, number>>({});
  const bellSeenAtRef = useRef<Record<string, number>>({});
  const bellObserveStartAtRef = useRef(Math.floor(Date.now() / 1000));
  const topLoadLockRef = useRef(false);
  const historyLoadingRef = useRef(false);
  const lastTabFocusSessionRef = useRef('');
  const suppressCardPressSessionRef = useRef('');
  const terminateTimerRef = useRef<Record<string, ReturnType<typeof setTimeout>>>({});
  const statusOnlyKickRef = useRef<Record<string, number>>({});
  const enforcedSessionRef = useRef<string>('');
  const pulse = useRef(new Animated.Value(0.4)).current;
  const tabIndicatorX = useRef(new Animated.Value(0)).current;
  const tabIndicatorWidth = useRef(new Animated.Value(44)).current;

  const onlineSource = useMemo(
    () => {
      const now = Date.now();
      return sessions.filter((item) => {
        if (item.online === false) return false;
        const hiddenAt = closingSessionMap[item.session_id];
        if (!hiddenAt) return true;
        return now-hiddenAt > CLOSE_SESSION_HIDE_MS;
      });
    },
    [sessions, closingSessionMap],
  );

  const onlineSessions = useMemo(() => {
    if (!onlineSource.length) return [];

    const byId = new Map(onlineSource.map((item) => [item.session_id, item]));
    const ordered: Session[] = [];

    for (const sessionId of sessionOrder) {
      const target = byId.get(sessionId);
      if (target) {
        ordered.push(target);
      }
    }

    for (const session of onlineSource) {
      if (!sessionOrder.includes(session.session_id)) {
        ordered.push(session);
      }
    }

    return ordered;
  }, [onlineSource, sessionOrder]);

  const activeSession = useMemo(
    () => onlineSessions.find((item) => item.session_id === activeSessionId),
    [onlineSessions, activeSessionId],
  );

  const sessionDisplayNameMap = useMemo(() => {
    const grouped = new Map<string, Session[]>();
    for (const session of onlineSessions) {
      const base = sessionTitle(session);
      const group = grouped.get(base) ?? [];
      group.push(session);
      grouped.set(base, group);
    }

    const mapped: Record<string, string> = {};
    for (const [base, group] of grouped.entries()) {
      if (group.length <= 1) {
        if (group[0]) {
          mapped[group[0].session_id] = base;
        }
        continue;
      }

      group.forEach((session, index) => {
        mapped[session.session_id] = `${base}(${index + 1})`;
      });
    }
    return mapped;
  }, [onlineSessions]);

  const displaySessionTitle = useCallback(
    (session: Session) => sessionDisplayNameMap[session.session_id] ?? sessionTitle(session),
    [sessionDisplayNameMap],
  );

  const pendingTerminateSession = useMemo(
    () => sessions.find((item) => item.session_id === pendingTerminateSessionId),
    [sessions, pendingTerminateSessionId],
  );

  const activeSessionIndex = useMemo(
    () => onlineSessions.findIndex((item) => item.session_id === activeSessionId),
    [onlineSessions, activeSessionId],
  );

  const isAiTuiSession = useMemo(() => {
    if (!activeSession) return false;
    const signature = `${activeSession.session_name ?? ''} ${activeSession.command ?? ''}`.toLowerCase();
    return signature.includes('claude') || signature.includes('codex') || signature.includes('gemini');
  }, [activeSession]);

  const terminalLineHeight = useMemo(
    () => Math.round(terminalFontSize * TERMINAL_LINE_HEIGHT_RATIO),
    [terminalFontSize],
  );

  const keyboardLift = useMemo(() => {
    if (keyboardInset <= 0) return 0;
    return Math.max(0, Math.floor(keyboardInset));
  }, [keyboardInset]);

  const navBarInset = useMemo(() => {
    if (Platform.OS !== 'android') return 0;
    const screenHeight = Dimensions.get('screen').height;
    const windowHeight = Dimensions.get('window').height;
    const dynamicInset = Math.max(0, Math.round(screenHeight - windowHeight));
    return Math.max(dynamicInset, 48);
  }, []);

  const effectiveBottomInset = keyboardLift > 0 ? 0 : navBarInset;

  const terminalDockBottom = useMemo(() => keyboardLift + effectiveBottomInset, [keyboardLift, effectiveBottomInset]);

  const jumpButtonBottom = useMemo(() => keyboardLift + effectiveBottomInset + 56, [keyboardLift, effectiveBottomInset]);

  const terminalScrollPaddingBottom = useMemo(() => {
    const base = 64;
    return base + keyboardLift + effectiveBottomInset;
  }, [keyboardLift, effectiveBottomInset]);

  const monoFont = Platform.OS === 'android' ? 'monospace' : (fontsLoaded ? 'JetBrainsMono_400Regular' : 'monospace');

  // --- Effects ---

  useEffect(() => {
    Animated.loop(
      Animated.sequence([
        Animated.timing(pulse, { toValue: 1, duration: 1200, useNativeDriver: true }),
        Animated.timing(pulse, { toValue: 0.4, duration: 1200, useNativeDriver: true }),
      ]),
    ).start();
  }, [pulse]);

  useEffect(() => {
    let alive = true;

    const loadTerminalFontSize = async () => {
      const traceId = nowTraceId('font');
      try {
        const raw = await AsyncStorage.getItem(TERMINAL_FONT_SIZE_STORAGE_KEY);
        if (!alive) return;
        if (!raw) {
          terminalFontSizeHydratedRef.current = true;
          return;
        }

        const parsed = Number.parseInt(raw, 10);
        if (!Number.isFinite(parsed)) {
          terminalFontSizeHydratedRef.current = true;
          return;
        }

        const restored = clampNumber(parsed, TERMINAL_FONT_SIZE_MIN, TERMINAL_FONT_SIZE_MAX);
        const restoredZoom = Math.round((restored / TERMINAL_FONT_SIZE_BASE) * 100);
        terminalFontSizeHydratedRef.current = true;
        setTerminalFontSize(restored);
        if (restored !== TERMINAL_FONT_SIZE_BASE) {
          setToast(`字体 ${restoredZoom}%`);
        }
        logState('info', {
          trace_id: traceId,
          event: 'font_size_restored',
          value: restored,
          zoom_pct: restoredZoom,
        });
      } catch (err) {
        terminalFontSizeHydratedRef.current = true;
        logState('error', {
          trace_id: traceId,
          event: '[STATE_INVALID] font_size_restore_fail',
          error: err instanceof Error ? err.message : String(err),
        });
      }
    };

    void loadTerminalFontSize();
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    terminalFontSizeRef.current = terminalFontSize;
    if (!terminalFontSizeHydratedRef.current) {
      return;
    }

    const traceId = nowTraceId('font');
    void AsyncStorage.setItem(TERMINAL_FONT_SIZE_STORAGE_KEY, String(terminalFontSize)).catch((err) => {
      logState('error', {
        trace_id: traceId,
        event: '[STATE_INVALID] font_size_persist_fail',
        value: terminalFontSize,
        error: err instanceof Error ? err.message : String(err),
      });
    });
  }, [terminalFontSize]);

  useEffect(() => {
    const now = Date.now();
    const sessionSet = new Set(sessions.map((item) => item.session_id));
    setClosingSessionMap((previous) => {
      if (!Object.keys(previous).length) {
        return previous;
      }

      let changed = false;
      const next: Record<string, number> = {};
      for (const [sessionId, hiddenAt] of Object.entries(previous)) {
        if (!sessionSet.has(sessionId)) {
          changed = true;
          continue;
        }
        if (now-hiddenAt > CLOSE_SESSION_HIDE_MS) {
          changed = true;
          continue;
        }
        next[sessionId] = hiddenAt;
      }
      return changed ? next : previous;
    });
  }, [sessions]);

  useEffect(() => {
    const onlineIds = onlineSource.map((item) => item.session_id);
    setSessionOrder((previous) => {
      if (!onlineIds.length) {
        if (!previous.length) return previous;
        return [];
      }

      const onlineIdSet = new Set(onlineIds);
      const kept = previous.filter((sessionId) => onlineIdSet.has(sessionId));
      const previousSet = new Set(previous);
      const appended = onlineIds.filter((sessionId) => !previousSet.has(sessionId));
      const next = [...kept, ...appended];

      if (next.length === previous.length && next.every((id, idx) => id === previous[idx])) {
        return previous;
      }

      return next;
    });
  }, [onlineSource]);

  useEffect(() => {
    if (!activeSessionId && onlineSessions.length > 0) {
      setActiveSessionId(onlineSessions[0].session_id);
    } else if (activeSessionId && !onlineSessions.some((item) => item.session_id === activeSessionId)) {
      setActiveSessionId(onlineSessions[0]?.session_id ?? '');
    }
  }, [activeSessionId, onlineSessions]);

  useEffect(() => {
    const showEvent = Platform.OS === 'ios' ? 'keyboardWillShow' : 'keyboardDidShow';
    const hideEvent = Platform.OS === 'ios' ? 'keyboardWillHide' : 'keyboardDidHide';

    const showSub = Keyboard.addListener(showEvent, (event) => {
      const height = event.endCoordinates?.height ?? 0;
      setKeyboardInset(height > 0 ? height : 0);
    });
    const hideSub = Keyboard.addListener(hideEvent, () => {
      setKeyboardInset(0);
      setComposerVisible(false);
      setComposerCommand('');
    });

    return () => {
      showSub.remove();
      hideSub.remove();
    };
  }, []);

  useEffect(() => {
    setFollowTail(true);
    historyLoadingRef.current = false;
    setComposerVisible(false);
    setComposerCommand('');
    setKeyboardInset(0);
  }, [activeSessionId]);

  useEffect(() => {
    if (onlineSessions.length < 2 && sortMode) {
      setSortMode(false);
    }
  }, [onlineSessions.length, sortMode]);

  useEffect(() => {
    if (!terminalVisible) {
      setSortMode(false);
      setImmersiveMode(false);
      setComposerVisible(false);
      setComposerCommand('');
      setKeyboardInset(0);
    }
  }, [terminalVisible]);

  useEffect(() => {
    setTabLayouts((previous) => {
      const sessionSet = new Set(onlineSessions.map((item) => item.session_id));
      let changed = false;
      const next: Record<string, { x: number; width: number }> = {};
      for (const [sessionId, layout] of Object.entries(previous)) {
        if (sessionSet.has(sessionId)) {
          next[sessionId] = layout;
        } else {
          changed = true;
        }
      }
      return changed ? next : previous;
    });
  }, [onlineSessions]);

  useEffect(() => {
    if (!terminalVisible) return;
    const layout = tabLayouts[activeSessionId];
    if (!layout) return;

    const shouldAnimate = lastTabFocusSessionRef.current !== activeSessionId;
    lastTabFocusSessionRef.current = activeSessionId;

    if (shouldAnimate) {
      Animated.parallel([
        Animated.spring(tabIndicatorX, {
          toValue: layout.x,
          tension: 170,
          friction: 19,
          useNativeDriver: false,
        }),
        Animated.spring(tabIndicatorWidth, {
          toValue: Math.max(layout.width, 42),
          tension: 170,
          friction: 19,
          useNativeDriver: false,
        }),
      ]).start();
    } else {
      tabIndicatorX.setValue(layout.x);
      tabIndicatorWidth.setValue(Math.max(layout.width, 42));
    }

    terminalTabScrollRef.current?.scrollTo({
      x: Math.max(layout.x - 42, 0),
      animated: shouldAnimate,
    });
  }, [activeSessionId, tabLayouts, tabIndicatorX, tabIndicatorWidth, terminalVisible]);

  useEffect(() => {
    ensureNotificationPermission().then(setNotifyEnabled);
  }, []);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(''), 1600);
    return () => clearTimeout(t);
  }, [toast]);

  useEffect(() => {
    return () => {
      for (const timer of Object.values(terminateTimerRef.current)) {
        clearTimeout(timer);
      }
      terminateTimerRef.current = {};
    };
  }, []);

  useEffect(() => {
    const sub = Notifications.addNotificationResponseReceivedListener(res => {
      const sid = res.notification.request.content.data?.sessionId;
      if (typeof sid === 'string' && sid) {
        setActiveSessionId(sid);
        setTerminalVisible(true);
      }
    });
    return () => sub.remove();
  }, []);

  useEffect(() => {
    if (!notifyEnabled) return;
    (async () => {
      for (const s of onlineSessions) {
        const text = s.last_text || '';
        if (!text) continue;
        const kw = containsAlertKeyword(text);
        if (kw && notifiedLineRef.current[s.session_id] !== text) {
          notifiedLineRef.current[s.session_id] = text;
          await Notifications.scheduleNotificationAsync({
            content: {
              title: `⚡ ${sessionTitle(s)} Alert`,
              body: `${kw}: ${text.slice(0, 60)}`,
              data: { sessionId: s.session_id },
            },
            trigger: null,
          });
        }
      }
    })();
  }, [notifyEnabled, onlineSessions]);

  const emitBellAlert = useCallback(
    async (sessionId: string, rawText: string) => {
      if (!notifyEnabled) return;

      const now = Date.now();
      const lastAt = bellAlertAtRef.current[sessionId] || 0;
      if (now - lastAt < TERMINAL_BELL_COOLDOWN_MS) {
        return;
      }
      bellAlertAtRef.current[sessionId] = now;

      const traceId = nowTraceId('bell');
      const matchedSession = onlineSessions.find((item) => item.session_id === sessionId);
      const title = matchedSession ? sessionTitle(matchedSession) : sessionId;

      try {
        await Notifications.scheduleNotificationAsync({
          content: {
            title: `🔔 ${title}`,
            body: '终端响铃，请处理当前会话。',
            data: { sessionId, reason: 'terminal-bell' },
          },
          trigger: null,
        });
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Warning).catch(() => undefined);
        logState('info', {
          trace_id: traceId,
          event: 'terminal_bell_alert',
          session_id: sessionId,
          preview: normalizeTerminalText(rawText).slice(0, 64),
        });
      } catch (err) {
        logState('error', {
          trace_id: traceId,
          event: '[STATE_INVALID] terminal_bell_alert_fail',
          session_id: sessionId,
          error: err instanceof Error ? err.message : String(err),
        });
      }
    },
    [notifyEnabled, onlineSessions],
  );

  useEffect(() => {
    if (!notifyEnabled) return;
    void (async () => {
      const onlineSet = new Set(onlineSessions.map((item) => item.session_id));
      for (const sid of Object.keys(bellSeenAtRef.current)) {
        if (!onlineSet.has(sid)) {
          delete bellSeenAtRef.current[sid];
        }
      }

      for (const session of onlineSessions) {
        const sid = session.session_id;
        const text = session.last_text || '';
        const bellAtRaw = session.last_bell_at;
        const bellAt = Number.isFinite(bellAtRaw) ? Math.floor(bellAtRaw ?? 0) : 0;
        const seenAt = bellSeenAtRef.current[sid] || 0;

        if (bellAt > 0) {
          if (seenAt === 0) {
            bellSeenAtRef.current[sid] = bellAt;
            if (bellAt >= bellObserveStartAtRef.current-1) {
              await emitBellAlert(sid, text);
            }
            continue;
          }
          if (bellAt > seenAt) {
            bellSeenAtRef.current[sid] = bellAt;
            await emitBellAlert(sid, text);
          }
          continue;
        }

        if (text && containsBellSignal(text) && seenAt === 0) {
          bellSeenAtRef.current[sid] = Math.floor(Date.now() / 1000);
          await emitBellAlert(sid, text);
        }
      }
    })();
  }, [emitBellAlert, notifyEnabled, onlineSessions]);

  // --- Logic ---

  const reloadTerminalTail = useCallback(async () => {
    if (!activeSessionId) {
      setTerminalLines([]);
      return;
    }

    try {
      const screen = await fetchOutputScreen(activeSessionId);
      if (screen) {
        const rawScreenLines = Array.isArray(screen.lines) ? screen.lines : [];
        const rawStyledLines = Array.isArray(screen.styled_lines) ? screen.styled_lines : [];
        const bellScreenLine = rawScreenLines.find((line) => containsBellSignal(String(line ?? '')));
        if (bellScreenLine) {
          void emitBellAlert(activeSessionId, String(bellScreenLine));
        }
        const statusOnlyScreen = isTmuxStatusOnlyScreen(rawScreenLines);
        const sparseStatusLikeScreen = isSparseStatusLikeScreen(rawScreenLines);
        let screenLines = sanitizeScreenLines(rawScreenLines);

        const needsStatusHint = statusOnlyScreen || sparseStatusLikeScreen;
        if (needsStatusHint) {
          screenLines = [
            statusOnlyScreen
              ? '⚠ 仅收到 tmux 状态栏；等待 TUI 重绘（可发送 Ctrl+L）'
              : '⚠ 当前会话仅状态区输出（可能为旧会话）；建议切换最新会话或发送 Ctrl+L',
          ];
          const now = Date.now();
          const lastKick = statusOnlyKickRef.current[activeSessionId] || 0;
          if (now - lastKick > 12000) {
            statusOnlyKickRef.current[activeSessionId] = now;
            void sendControl(activeSessionId, '\n', false)
              .then(() => {
                logState('info', {
                  trace_id: nowTraceId('tui'),
                  event: statusOnlyScreen ? 'status_only_redraw_kick' : 'sparse_screen_redraw_kick',
                  session_id: activeSessionId,
                });
              })
              .catch((kickErr) => {
                logState('error', {
                  trace_id: nowTraceId('tui'),
                  event: '[STATE_INVALID] status_like_redraw_kick_fail',
                  session_id: activeSessionId,
                  err: kickErr instanceof Error ? kickErr.message : String(kickErr),
                });
              });
          }
        } else if (screenLines.length === 0 && rawScreenLines.length > 0) {
          const looseLines = rawScreenLines
            .map((line) => normalizeScreenHintLine(line))
            .filter((line) => line.length > 0 && line !== 'B');
          if (looseLines.length > 0) {
            screenLines = looseLines;
          }
        }

        const screenTS = screen.ts || Math.floor(Date.now() / 1000);
        const baseSeq = Math.max(1, screen.revision || 0) * 1000;
        const styledEvents = !statusOnlyScreen && !sparseStatusLikeScreen && rawStyledLines.length > 0
          ? buildScreenStyledEvents(activeSessionId, rawScreenLines, rawStyledLines, baseSeq, screenTS)
          : [];
        let nextEvents = styledEvents.length > 0
          ? styledEvents
          : screenLines.map((text, index) => ({
            seq: baseSeq + index,
            session_id: activeSessionId,
            text,
            ts: screenTS,
          }));

        const sparseStyledFallback = !needsStatusHint && styledEvents.length > 0 && isSparseStatusLikeScreen(
          styledEvents.map((event) => event.text),
        );
        if (sparseStyledFallback) {
          nextEvents = [{
            seq: baseSeq,
            session_id: activeSessionId,
            text: '⚠ 当前会话仅状态区输出（可能为旧会话）；建议切换最新会话或发送 Ctrl+L',
            ts: screenTS,
          }];
        }

        if (nextEvents.length === 0) {
          const fallbackText = activeSession ? inferPreviewFallback(activeSession) : '等待终端输出…';
          nextEvents = [{
            seq: baseSeq,
            session_id: activeSessionId,
            text: fallbackText,
            ts: screenTS,
          }];
        }

        try {
          const tailData = await fetchOutputTail(activeSessionId);
          const rawTailEvents = Array.isArray(tailData.events) ? tailData.events : [];
          const bellTailEvent = rawTailEvents.find((event) => containsBellSignal(String(event?.text ?? '')));
          if (bellTailEvent) {
            void emitBellAlert(activeSessionId, String(bellTailEvent.text ?? ''));
          }
          const tailEvents = sanitizeOutputEvents(rawTailEvents);
          const activeSignature = `${activeSession?.session_name || ''} ${activeSession?.command || ''}`.toLowerCase();
          const interactiveAgentSession =
            activeSignature.includes('codex') ||
            activeSignature.includes('claude') ||
            activeSignature.includes('gemini') ||
            activeSignature.includes('chatgpt.com/codex');
          const allowGeneralTailMerge =
            needsStatusHint ||
            sparseStyledFallback ||
            (!interactiveAgentSession && nextEvents.length <= 18);

          nextEvents = mergeScreenWithTailBuffer(nextEvents, tailEvents, activeSessionId, screenTS, {
            allowGeneralTailMerge,
          });
          setCursor(Math.max(screen.revision || 0, tailData.cursor || 0));
          setHasMoreBefore(!!screen.truncated || !!tailData.has_more_before);
        } catch (tailErr) {
          logState('error', {
            trace_id: nowTraceId('tail'),
            event: '[STATE_INVALID] tail_merge_fail',
            session_id: activeSessionId,
            error: tailErr instanceof Error ? tailErr.message : String(tailErr),
          });
          setCursor(screen.revision || 0);
          setHasMoreBefore(!!screen.truncated);
        }

        setTerminalLines((previous) => (areOutputEventsEqual(previous, nextEvents) ? previous : nextEvents));
        setScreenMode(true);
        setTerminalError('');
        return;
      }

      setScreenMode(false);
      const data = await fetchOutputTail(activeSessionId);
      const rawTailEvents = Array.isArray(data.events) ? data.events : [];
      const bellTailEvent = rawTailEvents.find((event) => containsBellSignal(String(event?.text ?? '')));
      if (bellTailEvent) {
        void emitBellAlert(activeSessionId, String(bellTailEvent.text ?? ''));
      }
      const nextEvents = sanitizeOutputEvents(rawTailEvents);
      setTerminalLines((previous) => (areOutputEventsEqual(previous, nextEvents) ? previous : nextEvents));
      setCursor(data.cursor || 0);
      setHasMoreBefore(!!data.has_more_before);
      setTerminalError('');
    } catch (err) {
      setTerminalError(err instanceof Error ? err.message : 'fetch failed');
    }
  }, [activeSessionId, activeSession, emitBellAlert]);

  const loadOlder = useCallback(async () => {
    if (screenMode) return;
    if (!activeSessionId || !hasMoreBefore || loadingMore || !terminalLines.length) return;
    setLoadingMore(true);
    historyLoadingRef.current = true;
    try {
      const data = await fetchOutputBefore(activeSessionId, terminalLines[0].seq);
      if (data.events?.length) {
        const olderEvents = sanitizeOutputEvents(data.events);
        if (olderEvents.length > 0) {
          setTerminalLines((previous) => [...olderEvents, ...previous]);
        }
      }
      setHasMoreBefore(!!data.has_more_before);
    } catch (err) {
      setTerminalError(err instanceof Error ? err.message : 'load older failed');
    } finally {
      setLoadingMore(false);
      setTimeout(() => {
        historyLoadingRef.current = false;
      }, 220);
    }
  }, [activeSessionId, hasMoreBefore, loadingMore, screenMode, terminalLines]);

  useEffect(() => {
    if (terminalVisible && activeSessionId) {
      void reloadTerminalTail();
      const t = setInterval(() => {
        void reloadTerminalTail();
      }, TERMINAL_REFRESH_MS);
      return () => clearInterval(t);
    }
  }, [activeSessionId, terminalVisible, reloadTerminalTail]);

  useEffect(() => {
    if (!terminalVisible) {
      enforcedSessionRef.current = '';
      return;
    }
    const enforceKey = `${activeSessionId}:open`;
    if (enforcedSessionRef.current === enforceKey) return;
    if (!activeSession) return;

    setImmersiveMode(false);
    enforcedSessionRef.current = enforceKey;
  }, [terminalVisible, activeSessionId, activeSession]);

  const sendInput = useCallback(
    async (text: string, submit: boolean) => {
      if (!activeSessionId) return;
      if (!text && !submit) return;

      const traceId = nowTraceId('ctrl');
      const startedAt = Date.now();
      const normalizedInput = sanitizeTerminalLine(text);
      const shouldEchoInput = normalizedInput.length > 0 && !/[\u0000-\u001f\u007f]/.test(text);

      if (shouldEchoInput) {
        const nowTs = Math.floor(Date.now() / 1000);
        const optimisticText = normalizedInput.length > 240 ? normalizedInput.slice(0, 240) : normalizedInput;
        setTerminalLines((previous) => {
          const baseSeq = previous.length > 0 ? previous[previous.length - 1].seq + 1 : nowTs * 1000;
          const optimisticEvent: OutputEvent = {
            seq: baseSeq,
            session_id: activeSessionId,
            text: `> ${optimisticText}`,
            ts: nowTs,
          };
          const next = [...previous, optimisticEvent].slice(-360);
          return areOutputEventsEqual(previous, next) ? previous : next;
        });
        setFollowTail(true);
      }

      try {
        await sendControl(activeSessionId, text, submit);

        [80, 220, 480, 900].forEach((delay) => {
          setTimeout(() => {
            void reloadTerminalTail();
          }, delay);
        });

        logState('info', {
          trace_id: traceId,
          event: 'control_ok',
          session_id: activeSessionId,
          submit,
          bytes: text.length,
          preview: text.slice(0, 48),
          cost_ms: Date.now() - startedAt,
        });
      } catch (err) {
        logState('error', {
          trace_id: traceId,
          event: '[CRITICAL] control_fail',
          session_id: activeSessionId,
          cost_ms: Date.now() - startedAt,
          error: err instanceof Error ? err.message : String(err),
        });
        setToast('发送失败');
      }
    },
    [activeSessionId, reloadTerminalTail],
  );

  const sendQuickKey = useCallback(async (key: string) => {
    const map: Record<string, string> = {
      tab: '\t', up: '\u001b[A', down: '\u001b[B', left: '\u001b[D', right: '\u001b[C',
      esc: '\u001b', pgup: '\u001b[5~', pgdn: '\u001b[6~', home: '\u001b[H', end: '\u001b[F',
      shifttab: '\u001b[Z', ctrlc: '\u0003'
    };
    if (map[key]) await sendInput(map[key], false);
  }, [sendInput]);

  const hitLight = useCallback(() => {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => undefined);
  }, []);

  const hitMedium = useCallback(() => {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium).catch(() => undefined);
  }, []);

  const openSession = useCallback((sessionId: string) => {
    if (suppressCardPressSessionRef.current === sessionId) {
      suppressCardPressSessionRef.current = '';
      return;
    }
    hitMedium();
    setActiveSessionId(sessionId);
    setTerminalVisible(true);
  }, [hitMedium]);

  const terminateSession = useCallback((session: Session) => {
    if (!session?.session_id) return;
    const sid = session.session_id;
    const title = displaySessionTitle(session);

    const removeClosingTag = () => {
      setClosingSessionMap((previous) => {
        if (!previous[sid]) return previous;
        const next = { ...previous };
        delete next[sid];
        return next;
      });
    };

    const scheduleTerminate = () => {
      const traceId = nowTraceId('terminate');
      setTerminatingSessionId(sid);
      setPendingTerminateSessionId(sid);
      setClosingSessionMap((previous) => ({
        ...previous,
        [sid]: Date.now(),
      }));
      setToast('2秒内可撤销');

      if (activeSessionId === sid) {
        setTerminalVisible(false);
      }

      logState('info', {
        trace_id: traceId,
        event: 'terminate_scheduled',
        session_id: sid,
        undo_window_ms: TERMINATE_UNDO_WINDOW_MS,
      });

      const exist = terminateTimerRef.current[sid];
      if (exist) {
        clearTimeout(exist);
      }

      terminateTimerRef.current[sid] = setTimeout(() => {
        delete terminateTimerRef.current[sid];
        setPendingTerminateSessionId((previous) => (previous === sid ? '' : previous));

        void requestTerminateSession(sid)
          .then(() => {
            logState('info', {
              trace_id: traceId,
              event: 'terminate_sent',
              session_id: sid,
              source: 'doai-mobile-longpress',
            });
            setToast('已发送关闭指令');
            [120, 650, 1800].forEach((delay) => {
              setTimeout(() => {
                void refetch();
              }, delay);
            });
          })
          .catch((err) => {
            logState('error', {
              trace_id: traceId,
              event: '[CRITICAL] terminate_fail',
              session_id: sid,
              error: err instanceof Error ? err.message : String(err),
            });
            removeClosingTag();
            setToast('关闭失败');
          })
          .finally(() => {
            setTerminatingSessionId((previous) => (previous === sid ? '' : previous));
          });
      }, TERMINATE_UNDO_WINDOW_MS);
    };

    Alert.alert(
      '关闭会话',
      `确定关闭 ${title} 吗？这会终止远端 do-ai 进程。`,
      [
        { text: '取消', style: 'cancel' },
        {
          text: '关闭',
          style: 'destructive',
          onPress: scheduleTerminate,
        },
      ],
      { cancelable: true },
    );
  }, [activeSessionId, displaySessionTitle, refetch]);

  const undoTerminate = useCallback(() => {
    const sid = pendingTerminateSessionId;
    if (!sid) return;
    const timer = terminateTimerRef.current[sid];
    if (timer) {
      clearTimeout(timer);
      delete terminateTimerRef.current[sid];
    }
    setPendingTerminateSessionId('');
    setTerminatingSessionId((previous) => (previous === sid ? '' : previous));
    setClosingSessionMap((previous) => {
      if (!previous[sid]) return previous;
      const next = { ...previous };
      delete next[sid];
      return next;
    });
    logState('info', {
      trace_id: nowTraceId('terminate'),
      event: 'terminate_undo',
      session_id: sid,
    });
    setToast('已撤销关闭');
    void refetch();
  }, [pendingTerminateSessionId, refetch]);

  const switchTerminalTab = useCallback((sessionId: string) => {
    hitLight();
    setActiveSessionId(sessionId);
  }, [hitLight]);

  const switchTerminalTabByOffset = useCallback((offset: -1 | 1) => {
    if (activeSessionIndex < 0 || onlineSessions.length === 0) return;
    const nextIndex = Math.max(0, Math.min(onlineSessions.length - 1, activeSessionIndex + offset));
    if (nextIndex === activeSessionIndex) {
      hitLight();
      return;
    }
    setActiveSessionId(onlineSessions[nextIndex].session_id);
    hitMedium();
  }, [activeSessionIndex, hitLight, hitMedium, onlineSessions]);

  const moveActiveSession = useCallback((offset: -1 | 1) => {
    if (activeSessionIndex < 0 || onlineSessions.length < 2) {
      hitLight();
      return;
    }

    const targetIndex = Math.max(0, Math.min(onlineSessions.length - 1, activeSessionIndex + offset));
    if (targetIndex === activeSessionIndex) {
      hitLight();
      return;
    }

    const nextIds = onlineSessions.map((item) => item.session_id);
    const [movingId] = nextIds.splice(activeSessionIndex, 1);
    nextIds.splice(targetIndex, 0, movingId);

    setSessionOrder(nextIds);
    setActiveSessionId(nextIds[targetIndex]);
    setToast(offset > 0 ? '会话已右移' : '会话已左移');
    hitMedium();
  }, [activeSessionIndex, hitLight, hitMedium, onlineSessions]);

  const toggleSortMode = useCallback(() => {
    hitLight();
    setSortMode((previous) => {
      const next = !previous;
      setToast(next ? '排序模式开启' : '排序模式关闭');
      return next;
    });
  }, [hitLight]);

  const registerTabLayout = useCallback((sessionId: string, x: number, width: number) => {
    setTabLayouts((previous) => {
      const current = previous[sessionId];
      if (current && Math.abs(current.x - x) < 1 && Math.abs(current.width - width) < 1) {
        return previous;
      }
      return {
        ...previous,
        [sessionId]: { x, width },
      };
    });
  }, []);

  const resetPinchState = useCallback(() => {
    pinchActiveRef.current = false;
    pinchStartDistanceRef.current = 0;
  }, []);

  const syncPinchZoom = useCallback((event: GestureResponderEvent) => {
    const pinchDistance = getPinchDistance(event);
    if (pinchDistance === null) {
      if (pinchActiveRef.current) {
        resetPinchState();
      }
      return;
    }

    if (!pinchActiveRef.current || pinchStartDistanceRef.current <= 0) {
      pinchActiveRef.current = true;
      pinchStartDistanceRef.current = pinchDistance;
      pinchStartFontSizeRef.current = terminalFontSizeRef.current;
      return;
    }

    const scale = pinchDistance / pinchStartDistanceRef.current;
    const nextSize = clampNumber(
      Math.round(pinchStartFontSizeRef.current * scale),
      TERMINAL_FONT_SIZE_MIN,
      TERMINAL_FONT_SIZE_MAX,
    );
    setTerminalFontSize((previous) => (previous === nextSize ? previous : nextSize));
  }, [resetPinchState]);

  const handleTerminalTouchStart = useCallback((event: GestureResponderEvent) => {
    syncPinchZoom(event);
  }, [syncPinchZoom]);

  const handleTerminalTouchMove = useCallback((event: GestureResponderEvent) => {
    syncPinchZoom(event);
  }, [syncPinchZoom]);

  const handleTerminalTouchEnd = useCallback((event: GestureResponderEvent) => {
    const remainTouches = event.nativeEvent.touches?.length ?? 0;
    if (remainTouches < 2) {
      resetPinchState();
    }
  }, [resetPinchState]);

  const handleTerminalTouchCancel = useCallback(() => {
    resetPinchState();
  }, [resetPinchState]);

  const terminalSwipeResponder = useMemo(
    () =>
      PanResponder.create({
        onStartShouldSetPanResponderCapture: () => false,
        onMoveShouldSetPanResponderCapture: () => false,
        onMoveShouldSetPanResponder: (_event, gesture) => {
          const horizontal = Math.abs(gesture.dx);
          const vertical = Math.abs(gesture.dy);
          return horizontal > 16 && horizontal > vertical * 1.25;
        },
        onPanResponderRelease: (_event, gesture) => {
          if (gesture.dx <= -42 || gesture.vx <= -0.36) {
            switchTerminalTabByOffset(1);
            return;
          }
          if (gesture.dx >= 42 || gesture.vx >= 0.36) {
            switchTerminalTabByOffset(-1);
          }
        },
        onPanResponderTerminate: () => undefined,
        onPanResponderTerminationRequest: () => true,
      }),
    [switchTerminalTabByOffset],
  );

  const openActiveTerminal = useCallback(() => {
    if (!activeSessionId) return;
    hitMedium();
    setTerminalVisible(true);
  }, [activeSessionId, hitMedium]);

  const closeTerminal = useCallback(() => {
    hitLight();
    setComposerVisible(false);
    setComposerCommand('');
    setKeyboardInset(0);
    Keyboard.dismiss();
    setTerminalVisible(false);
  }, [hitLight]);

  useEffect(() => {
    if (Platform.OS !== 'android') return;
    const sub = BackHandler.addEventListener('hardwareBackPress', () => {
      if (!terminalVisible) {
        return false;
      }
      closeTerminal();
      return true;
    });
    return () => sub.remove();
  }, [closeTerminal, terminalVisible]);

  const refreshTerminal = useCallback(() => {
    hitLight();
    void reloadTerminalTail();
  }, [hitLight, reloadTerminalTail]);

  const triggerQuickKey = useCallback((key: string) => {
    hitLight();
    void sendQuickKey(key);
  }, [hitLight, sendQuickKey]);

  const submitComposerCommand = useCallback(async () => {
    if (!activeSessionId) {
      setToast('无可用会话');
      return;
    }

    const payload = composerCommand.replace(/[\r\n]/g, '');
    hitLight();
    await sendInput(payload, true);
    setComposerCommand('');
    setComposerVisible(false);
    setKeyboardInset(0);
    Keyboard.dismiss();
  }, [activeSessionId, composerCommand, hitLight, sendInput]);

  const closeComposer = useCallback(() => {
    setComposerVisible(false);
    setComposerCommand('');
    setKeyboardInset(0);
    Keyboard.dismiss();
  }, []);

  const increaseFont = useCallback(() => {
    hitLight();
    setTerminalFontSize((previous) => clampNumber(previous + 1, TERMINAL_FONT_SIZE_MIN, TERMINAL_FONT_SIZE_MAX));
  }, [hitLight]);

  const decreaseFont = useCallback(() => {
    hitLight();
    setTerminalFontSize((previous) => clampNumber(previous - 1, TERMINAL_FONT_SIZE_MIN, TERMINAL_FONT_SIZE_MAX));
  }, [hitLight]);

  const toggleImmersiveMode = useCallback(() => {
    hitLight();
    setImmersiveMode((previous) => !previous);
  }, [hitLight]);

  const focusCommandInput = useCallback(() => {
    hitLight();
    if (composerVisible) {
      closeComposer();
      return;
    }
    setComposerVisible(true);
    setTimeout(() => {
      composerInputRef.current?.focus();
    }, 60);
    setTimeout(() => {
      composerInputRef.current?.focus();
    }, 220);
  }, [closeComposer, composerVisible, hitLight]);

  useEffect(() => {
    if (!composerVisible) {
      return;
    }

    const firstFocus = setTimeout(() => {
      composerInputRef.current?.focus();
    }, 32);
    const secondFocus = setTimeout(() => {
      composerInputRef.current?.focus();
    }, 168);

    return () => {
      clearTimeout(firstFocus);
      clearTimeout(secondFocus);
    };
  }, [composerVisible]);

  useEffect(() => {
    if (!terminalVisible || keyboardLift <= 0) {
      return;
    }
    const timer = setTimeout(() => {
      terminalScrollRef.current?.scrollToEnd({ animated: true });
    }, 60);
    return () => clearTimeout(timer);
  }, [keyboardLift, terminalVisible]);

  // --- Render ---

  return (
    <View style={styles.appRoot}>
      <StatusBar style="light" backgroundColor="#050913" />

      {/* --- HEADER --- */}
      <View style={styles.mainHeader}>
        <View>
          <Text style={styles.headerTitle}>Terminals</Text>
          <View style={styles.headerSubtitleRow}>
            <View style={[styles.statusDot, error ? styles.bgRed : styles.bgGreen]} />
            <Text style={styles.headerSubtitle}>{loading ? 'Connecting...' : `${onlineSessions.length} active`}</Text>
          </View>
        </View>
        <Pressable style={({ pressed }) => [styles.iconButton, pressed && styles.pressDown]} onPress={() => refetch()}>
          <Text style={styles.iconText}>↻</Text>
        </Pressable>
      </View>

      {pendingTerminateSessionId ? (
        <View style={styles.undoBanner}>
          <Text style={styles.undoBannerText} numberOfLines={1}>
            正在关闭 {displaySessionTitle(pendingTerminateSession || { session_id: pendingTerminateSessionId, host: '', online: true })}（2秒内可撤销）
          </Text>
          <Pressable style={({ pressed }) => [styles.undoBannerButton, pressed && styles.pressDown]} onPress={undoTerminate}>
            <Text style={styles.undoBannerButtonText}>撤销关闭</Text>
          </Pressable>
        </View>
      ) : null}

      {/* --- CONTENT --- */}
      {loading ? (
        <View style={styles.centerView}>
          <ActivityIndicator color="#4D9CFF" />
        </View>
      ) : (
        <FlatList
          data={onlineSessions}
          keyExtractor={i => i.session_id}
          contentContainerStyle={styles.listContent}
          ListHeaderComponent={() => (
            <View style={styles.listSectionHeader}>
              <Text style={styles.sectionTitle}>SSH Sessions</Text>
              <Pressable style={({ pressed }) => [pressed && styles.pressDown]} onPress={() => setNotifyEnabled(!notifyEnabled)}>
                 <Text style={[styles.sectionAction, notifyEnabled && styles.textBlue]}>
                   {notifyEnabled ? 'Alerts On ⚡' : 'Alerts Off'}
                 </Text>
              </Pressable>
            </View>
          )}
          renderItem={({ item }) => {
            const active = item.session_id === activeSessionId;
            return (
              <Pressable
                style={({ pressed }) => [styles.card, active && styles.cardActive, pressed && styles.cardPressed]}
                onPress={() => openSession(item.session_id)}
                onLongPress={() => {
                  suppressCardPressSessionRef.current = item.session_id;
                  terminateSession(item);
                }}
                delayLongPress={360}
              >
                <View style={styles.cardLeft}>
                  <Text style={styles.hostInitial}>{titleInitial(displaySessionTitle(item))}</Text>
                </View>
                <View style={styles.cardBody}>
                  <View style={styles.cardRow}>
                    <Text style={styles.hostName} numberOfLines={1}>{displaySessionTitle(item)}</Text>
                    {item.online && <Animated.View style={[styles.liveIndicator, { opacity: pulse }]} />}
                    {terminatingSessionId === item.session_id && <ActivityIndicator size="small" color="#4D9CFF" style={styles.terminatingIndicator} />}
                  </View>
                  <Text style={styles.hostMeta} numberOfLines={1}>
                    {item.host} {item.cwd ? `· ${item.cwd}` : ''}
                  </Text>
                  <View style={styles.previewRow}>
                    <Text style={[styles.previewText, { fontFamily: monoFont }]} numberOfLines={1}>
                      {sessionPreviewText(item)}
                    </Text>
                  </View>
                </View>
                <View style={styles.cardRight}>
                  <Text style={styles.idleTime}>{idleLabel(item.idle_seconds)}</Text>
                  <Text style={styles.arrowIcon}>›</Text>
                </View>
              </Pressable>
            );
          }}
          ListEmptyComponent={
            <View style={styles.emptyView}>
              <Text style={styles.emptyTitle}>No Active Sessions</Text>
              <Text style={[styles.emptySub, { fontFamily: monoFont }]}>Run `do-ai 60s codex ...` and sessions appear automatically.</Text>
            </View>
          }
        />
      )}

      {/* --- BOTTOM NAV --- */}
      <View
        style={[
          styles.bottomNav,
          {
            paddingBottom: Platform.OS === 'android' ? Math.max(navBarInset, 12) : 24,
          },
        ]}
      >
        <Pressable style={({ pressed }) => [styles.navItem, pressed && styles.pressDown]} onPress={() => refetch()}>
          <Text style={styles.navIcon}>≡</Text>
          <Text style={styles.navLabel}>Hosts</Text>
        </Pressable>
        <Pressable style={({ pressed }) => [styles.navItem, pressed && styles.pressDown]} onPress={openActiveTerminal}>
          <Text style={[styles.navIcon, styles.textBlue]}>&gt;_</Text>
          <Text style={[styles.navLabel, styles.textBlue]}>Terminal</Text>
        </Pressable>
        <Pressable style={({ pressed }) => [styles.navItem, pressed && styles.pressDown]}>
          <Text style={styles.navIcon}>⚙</Text>
          <Text style={styles.navLabel}>Settings</Text>
        </Pressable>
      </View>

      {/* --- FULL SCREEN TERMINAL MODAL --- */}
      <Modal visible={terminalVisible} animationType="slide" presentationStyle="fullScreen" onRequestClose={closeTerminal}>
        <View style={styles.termContainer}>
          <StatusBar style="light" backgroundColor="#12163A" />
          
          {/* Term Header */}
          <View style={[styles.termHeader, immersiveMode && styles.termHeaderImmersive]}>
            <Pressable style={({ pressed }) => [styles.termBackBtn, pressed && styles.pressDown]} onPress={closeTerminal}>
              <Text style={styles.termBackText}>‹</Text>
            </Pressable>

            <ScrollView
              ref={terminalTabScrollRef}
              horizontal
              showsHorizontalScrollIndicator={false}
              style={[styles.termTabScroll, immersiveMode && styles.termTabScrollImmersive]}
            >
              <View style={styles.termTabRail}>
                {onlineSessions.map((s) => (
                  <Pressable
                    key={s.session_id}
                    onLayout={(event) => {
                      const { x, width } = event.nativeEvent.layout;
                      registerTabLayout(s.session_id, x, width);
                    }}
                    delayLongPress={220}
                    onLongPress={() => {
                      if (!sortMode) {
                        hitMedium();
                        setSortMode(true);
                        setActiveSessionId(s.session_id);
                        setToast('排序模式：点击左右箭头调整');
                      }
                    }}
                    style={({ pressed }) => [
                      styles.termTab,
                      s.session_id === activeSessionId && styles.termTabActive,
                      sortMode && s.session_id === activeSessionId && styles.termTabSorting,
                      pressed && styles.termTabPressed,
                    ]}
                    onPress={() => switchTerminalTab(s.session_id)}
                  >
                    <View style={[styles.termTabDot, s.session_id === activeSessionId ? styles.bgGreen : styles.bgGrey]} />
                    <Text style={[styles.termTabText, s.session_id === activeSessionId && styles.termTabTextActive]}>
                      {displaySessionTitle(s)}
                    </Text>
                  </Pressable>
                ))}
                <Animated.View
                  pointerEvents="none"
                  style={[
                    styles.termTabIndicator,
                    {
                      transform: [{ translateX: tabIndicatorX }],
                      width: tabIndicatorWidth,
                    },
                  ]}
                />
              </View>
            </ScrollView>

            <View style={[styles.termActionGroup, immersiveMode && styles.termActionGroupHidden]}>
              <Pressable style={({ pressed }) => [styles.termActionBtn, pressed && styles.pressDown]} onPress={decreaseFont}>
                <Text style={styles.termActionText}>A-</Text>
              </Pressable>
              <Pressable style={({ pressed }) => [styles.termActionBtn, pressed && styles.pressDown]} onPress={increaseFont}>
                <Text style={styles.termActionText}>A+</Text>
              </Pressable>
            </View>
          </View>
          {!immersiveMode && sortMode && (
            <View style={styles.sortBar}>
              <Pressable style={({ pressed }) => [styles.sortChip, pressed && styles.pressDown]} onPress={() => moveActiveSession(-1)}>
                <Text style={styles.sortChipText}>←</Text>
              </Pressable>
              <Text style={styles.sortBarText}>排序模式</Text>
              <Pressable style={({ pressed }) => [styles.sortChip, pressed && styles.pressDown]} onPress={() => moveActiveSession(1)}>
                <Text style={styles.sortChipText}>→</Text>
              </Pressable>
              <Pressable style={({ pressed }) => [styles.sortDoneChip, pressed && styles.pressDown]} onPress={toggleSortMode}>
                <Text style={styles.sortDoneText}>完成</Text>
              </Pressable>
            </View>
          )}

          {/* Term Output */}
          <View
            style={[styles.termViewport, immersiveMode && styles.termViewportImmersive]}
            onTouchStart={handleTerminalTouchStart}
            onTouchMove={handleTerminalTouchMove}
            onTouchEnd={handleTerminalTouchEnd}
            onTouchCancel={handleTerminalTouchCancel}
            {...terminalSwipeResponder.panHandlers}
          >
             <ScrollView
              ref={terminalScrollRef}
              style={styles.termScroll}
              contentContainerStyle={[
                styles.termScrollContent,
                immersiveMode && styles.termScrollContentImmersive,
                composerVisible && styles.termScrollContentComposer,
                immersiveMode && composerVisible && styles.termScrollContentComposerImmersive,
                { paddingBottom: terminalScrollPaddingBottom },
              ]}
              onContentSizeChange={() => {
                if (followTail && !historyLoadingRef.current) {
                  terminalScrollRef.current?.scrollToEnd({ animated: false });
                }
              }}
              onScroll={e => {
                const { contentOffset, layoutMeasurement, contentSize } = e.nativeEvent;
                const nearTop = contentOffset.y <= 10;
                const nearBottom = contentOffset.y + layoutMeasurement.height >= contentSize.height - 24;
                if (nearBottom !== followTail) {
                  setFollowTail(nearBottom);
                }
                if (nearTop && !topLoadLockRef.current) {
                  topLoadLockRef.current = true;
                  setFollowTail(false);
                  loadOlder().then(() =>
                    setTimeout(() => {
                      topLoadLockRef.current = false;
                    }, 500),
                  );
                }
              }}
              scrollEventThrottle={16}
            >
              {loadingMore && <ActivityIndicator size="small" color="#444" style={{marginVertical: 10}} />}
              {terminalLines.map((line) => (
                <Text
                  key={line.seq}
                  style={[
                    styles.termLine,
                    {
                      fontFamily: monoFont,
                      fontSize: terminalFontSize,
                      lineHeight: terminalLineHeight,
                    },
                  ]}
                  allowFontScaling={false}
                  maxFontSizeMultiplier={1}
                >
                  {line.segments && line.segments.length > 0
                    ? line.segments.map((segment, segmentIndex) => (
                      <Text key={`${line.seq}-${segmentIndex}`} style={terminalSegmentInlineStyle(segment)}>
                        {segment.text.length > 0 ? segment.text : ' '}
                      </Text>
                    ))
                    : (line.text.length > 0 ? line.text : ' ')}
                </Text>
              ))}
            </ScrollView>
            {!!terminalError && (
              <View style={styles.termErrorBadge}>
                <Text style={styles.termErrorText}>{terminalError}</Text>
              </View>
            )}
            {!followTail && (
              <Pressable
                style={({ pressed }) => [styles.termJumpBtn, { bottom: jumpButtonBottom }, pressed && styles.pressDown]}
                onPress={() => {
                  hitLight();
                  setFollowTail(true);
                  terminalScrollRef.current?.scrollToEnd({ animated: true });
                }}
              >
                <Text style={styles.termJumpText}>Latest ↓</Text>
              </Pressable>
            )}

            <View
              style={[
                styles.termDock,
                immersiveMode && styles.termDockImmersive,
                { bottom: terminalDockBottom },
              ]}
            >
              <Pressable style={({ pressed }) => [styles.termAccessoryKey, pressed && styles.termAccessoryKeyPressed]} onPress={() => triggerQuickKey('ctrlc')}>
                <Text style={[styles.termAccessoryText, styles.termAccessoryCtrlText]}>Ctrl+C</Text>
              </Pressable>

              <Pressable style={({ pressed }) => [styles.termAccessoryKey, pressed && styles.termAccessoryKeyPressed]} onPress={() => { void sendInput('***', false); }}>
                <Text style={styles.termAccessoryText}>***</Text>
              </Pressable>

              <Pressable style={({ pressed }) => [styles.termAccessoryKey, styles.termAccessoryShiftKey, pressed && styles.termAccessoryKeyPressed]} onPress={() => triggerQuickKey('shifttab')}>
                <Text style={[styles.termAccessoryText, styles.termAccessoryShiftText]}>{'shift\ntab'}</Text>
              </Pressable>

              <Pressable
                style={({ pressed }) => [
                  styles.termAccessoryKey,
                  styles.termAccessoryKeyboardKey,
                  composerVisible && styles.dockKeyboardBtnActive,
                  pressed && styles.termAccessoryKeyPressed,
                ]}
                onPress={focusCommandInput}
              >
                <Text style={styles.termAccessoryText}>⌨</Text>
              </Pressable>
            </View>

            {composerVisible ? (
              <TextInput
                ref={composerInputRef}
                style={styles.hiddenCommandInput}
                value={composerCommand}
                onChangeText={setComposerCommand}
                autoCapitalize="none"
                autoCorrect={false}
                spellCheck={false}
                blurOnSubmit={false}
                keyboardAppearance="dark"
                returnKeyType="send"
                onSubmitEditing={() => {
                  void submitComposerCommand();
                }}
              />
            ) : null}

          </View>
          
        </View>
      </Modal>
    </View>
  );
}

// --- Styles (The V9 Theme) ---

const theme = {
  bg: '#0B1220',
  card: '#182334',
  cardBorder: '#2A3B52',
  primary: '#4D9CFF',
  text: '#EAF2FC',
  textDim: '#A8B6CA',
  accent: '#31D684',
  danger: '#FF4D6A',
  termBg: '#0A0E14',
};

const styles = StyleSheet.create({
  // Global
  appRoot: {
    flex: 1,
    backgroundColor: theme.bg,
    paddingTop: Platform.OS === 'android' ? Constants.statusBarHeight : 0,
  },
  textWhite: { color: theme.text },
  textBlue: { color: theme.primary },
  pressDown: { opacity: 0.8, transform: [{ scale: 0.98 }] },
  bgGreen: { backgroundColor: theme.accent },
  bgRed: { backgroundColor: theme.danger },
  bgGrey: { backgroundColor: '#444' },

  // Header
  mainHeader: {
    paddingHorizontal: 20,
    paddingTop: 16,
    paddingBottom: 16,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    borderBottomWidth: 1,
    borderBottomColor: '#2A3A4D',
  },
  headerTitle: {
    color: theme.text,
    fontSize: 26,
    fontWeight: '800',
    fontFamily: Platform.OS === 'ios' ? 'System' : 'sans-serif-medium',
    letterSpacing: 0.5,
  },
  headerSubtitleRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: 4,
  },
  statusDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    marginRight: 6,
  },
  headerSubtitle: {
    color: theme.textDim,
    fontSize: 13,
    fontWeight: '600',
  },
  undoBanner: {
    marginHorizontal: 16,
    marginTop: 10,
    marginBottom: 2,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(77,156,255,0.46)',
    backgroundColor: 'rgba(15,35,58,0.82)',
    paddingHorizontal: 12,
    paddingVertical: 8,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 10,
  },
  undoBannerText: {
    flex: 1,
    color: '#CFE3FF',
    fontSize: 12,
    fontWeight: '700',
  },
  undoBannerButton: {
    height: 28,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(104, 182, 255, 0.8)',
    backgroundColor: 'rgba(31, 71, 112, 0.9)',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 10,
  },
  undoBannerButtonText: {
    color: '#E2F1FF',
    fontSize: 11,
    fontWeight: '800',
    letterSpacing: 0.3,
  },
  iconButton: {
    padding: 10,
    backgroundColor: '#161F2C',
    borderRadius: 12,
  },
  iconText: {
    color: theme.text,
    fontSize: 18,
    fontWeight: '700',
  },

  // List
  listContent: {
    paddingHorizontal: 16,
    paddingBottom: 20,
  },
  listSectionHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginTop: 20,
    marginBottom: 12,
  },
  sectionTitle: {
    color: '#8FA4BD',
    fontSize: 12,
    fontWeight: '800',
    textTransform: 'uppercase',
    letterSpacing: 1,
  },
  sectionAction: {
    color: '#9DB2CC',
    fontSize: 12,
    fontWeight: '700',
  },

  // Card
  card: {
    flexDirection: 'row',
    backgroundColor: theme.card,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: theme.cardBorder,
    marginBottom: 10,
    padding: 14,
    alignItems: 'center',
  },
  cardActive: {
    borderColor: '#304B6E',
    backgroundColor: '#141E2E',
  },
  cardPressed: {
    backgroundColor: '#19263A',
  },
  cardLeft: {
    width: 44,
    height: 44,
    borderRadius: 12,
    backgroundColor: '#1E2938',
    alignItems: 'center',
    justifyContent: 'center',
    marginRight: 14,
  },
  hostInitial: {
    color: '#D0D9E5',
    fontSize: 18,
    fontWeight: '800',
  },
  cardBody: {
    flex: 1,
  },
  cardRow: {
    flexDirection: 'row',
    alignItems: 'center',
  },
  hostName: {
    color: theme.text,
    fontSize: 16,
    fontWeight: '700',
    marginRight: 8,
  },
  liveIndicator: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: theme.accent,
  },
  terminatingIndicator: {
    marginLeft: 8,
  },
  hostMeta: {
    color: theme.textDim,
    fontSize: 12,
    marginTop: 2,
  },
  previewRow: {
    marginTop: 6,
    backgroundColor: '#132033',
    padding: 6,
    borderRadius: 6,
  },
  previewText: {
    color: '#B4C6DB',
    fontSize: 11,
  },
  cardRight: {
    marginLeft: 8,
    alignItems: 'flex-end',
    justifyContent: 'center',
  },
  idleTime: {
    color: '#9BB2CC',
    fontSize: 11,
    fontWeight: '600',
  },
  arrowIcon: {
    color: '#7E9ABB',
    fontSize: 18,
    marginTop: 4,
  },

  // Empty
  centerView: { flex: 1, justifyContent: 'center', alignItems: 'center' },
  emptyView: { alignItems: 'center', paddingVertical: 60, opacity: 0.95 },
  emptyTitle: { color: theme.text, fontSize: 18, fontWeight: '700', marginBottom: 8 },
  emptySub: { color: theme.textDim, fontSize: 13 },

  // Bottom Nav
  bottomNav: {
    flexDirection: 'row',
    backgroundColor: '#111A28',
    borderTopWidth: 1,
    borderTopColor: '#1F2A3A',
    paddingBottom: Platform.OS === 'ios' ? 24 : 12,
    paddingTop: 12,
  },
  navItem: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
  },
  navIcon: {
    color: '#97B0CC',
    fontSize: 20,
    marginBottom: 4,
    fontWeight: '700',
  },
  navLabel: {
    color: '#97B0CC',
    fontSize: 10,
    fontWeight: '600',
  },

  // Terminal (Full Screen)
  termContainer: {
    flex: 1,
    backgroundColor: '#05091F',
  },
  termHeader: {
    height: 42 + (Platform.OS === 'android' ? Constants.statusBarHeight : 0),
    paddingTop: Platform.OS === 'android' ? Constants.statusBarHeight : 0,
    paddingHorizontal: 6,
    flexDirection: 'row',
    alignItems: 'center',
    borderBottomWidth: 1,
    borderBottomColor: '#16213F',
    backgroundColor: '#070D23',
    position: 'relative',
    elevation: 0,
  },
  termHeaderImmersive: {
    height: 38 + (Platform.OS === 'android' ? Constants.statusBarHeight : 0),
    backgroundColor: '#060C20',
  },
  termBackBtn: {
    width: 30,
    height: 30,
    marginRight: 6,
    borderRadius: 9,
    backgroundColor: '#141C34',
    alignItems: 'center',
    justifyContent: 'center',
  },
  termBackText: { color: '#9FAACC', fontSize: 17, fontWeight: '700' },
  termActionGroup: {
    flexDirection: 'row',
    alignItems: 'center',
    marginLeft: 4,
  },
  termActionGroupHidden: { width: 0, opacity: 0, overflow: 'hidden' },
  termActionBtn: {
    minWidth: 36,
    height: 30,
    marginRight: 4,
    borderRadius: 9,
    paddingHorizontal: 6,
    backgroundColor: '#141C34',
    alignItems: 'center',
    justifyContent: 'center',
  },
  termActionText: { color: '#8F9AB9', fontSize: 11, fontWeight: '700' },
  sortBar: {
    height: 36,
    borderBottomWidth: 1,
    borderBottomColor: '#222',
    backgroundColor: '#121212',
    paddingHorizontal: 12,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  sortBarText: {
    flex: 1,
    color: '#666',
    fontSize: 11,
    fontWeight: '600',
    textAlign: 'center',
  },
  sortChip: {
    minWidth: 32,
    height: 26,
    borderRadius: 6,
    backgroundColor: '#222',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 8,
  },
  sortChipText: {
    color: '#AAA',
    fontSize: 14,
    fontWeight: '600',
  },
  sortDoneChip: {
    height: 26,
    borderRadius: 6,
    backgroundColor: theme.primary,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 12,
  },
  sortDoneText: {
    color: '#FFF',
    fontSize: 11,
    fontWeight: '700',
  },
  termTabScroll: {
    flex: 1,
  },
  termTabScrollImmersive: {
    marginRight: 4,
  },
  termTabRail: {
    flexDirection: 'row',
    alignItems: 'center',
    position: 'relative',
  },
  termTab: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 13,
    height: 28,
    marginVertical: 6,
    marginRight: 6,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: '#202B50',
    backgroundColor: '#131B34',
  },
  termTabActive: {
    backgroundColor: '#153D34',
    borderWidth: 1,
    borderColor: '#22B87F',
  },
  termTabSorting: {
    backgroundColor: '#333',
    borderColor: theme.primary,
  },
  termTabPressed: {
    opacity: 0.8,
  },
  termTabDot: { width: 7, height: 7, borderRadius: 3.5, marginRight: 7 },
  termTabText: { color: '#96A4C8', fontSize: 13, fontWeight: '700' },
  termTabTextActive: { color: '#5CF2B0' },
  termTabIndicator: {
    position: 'absolute',
    left: 0,
    bottom: 8,
    height: 2,
    borderRadius: 1,
    backgroundColor: '#6EA4FF',
    opacity: 0,
  },

  termViewport: {
    flex: 1,
    flexShrink: 1,
    minHeight: 0,
    backgroundColor: TERMINAL_XTERM_DEFAULT_BG,
    position: 'relative',
  },
  termViewportImmersive: {
    backgroundColor: '#17191d',
  },
  termScroll: { flex: 1, minHeight: 0 },
  termScrollContent: { padding: 8, paddingBottom: 142 },
  termScrollContentCompact: { paddingBottom: 144 },
  termScrollContentImmersive: { paddingBottom: 108 },
  termScrollContentComposer: { paddingBottom: Platform.OS === 'android' ? 188 : 152 },
  termScrollContentComposerImmersive: { paddingBottom: Platform.OS === 'android' ? 214 : 156 },
  termLine: {
    color: TERMINAL_XTERM_DEFAULT_FG,
    fontSize: 11,
    lineHeight: 15,
    includeFontPadding: false,
    letterSpacing: 0,
    flexWrap: 'wrap',
    flexShrink: 1,
  },
  termFloatStats: {
    position: 'absolute',
    top: 8,
    right: 8,
    backgroundColor: 'rgba(24, 43, 92, 0.92)',
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  termFloatStatsImmersive: {
    top: 6,
    right: 6,
    backgroundColor: 'rgba(18, 34, 72, 0.86)',
  },
  termStatsText: { color: '#8BF5C2', fontSize: 9, fontWeight: '700' },
  termErrorBadge: {
    position: 'absolute',
    top: 8,
    left: 8,
    right: 80,
    paddingVertical: 4,
    paddingHorizontal: 10,
    borderRadius: 6,
    backgroundColor: 'rgba(255, 77, 106, 0.1)',
    borderWidth: 1,
    borderColor: 'rgba(255, 77, 106, 0.2)',
  },
  termErrorText: {
    color: '#FF4D6A',
    fontSize: 11,
    fontWeight: '600',
  },
  termJumpBtn: {
    position: 'absolute',
    right: 12,
    bottom: Platform.OS === 'android' ? 132 : 70,
    backgroundColor: '#1D2D59',
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderWidth: 1,
    borderColor: '#4763A2',
    elevation: 4,
  },
  termJumpText: {
    color: '#B6C5EC',
    fontSize: 11,
    fontWeight: '600',
  },
  termDock: {
    position: 'absolute',
    left: 0,
    right: 0,
    bottom: 0,
    height: 44,
    borderRadius: 0,
    backgroundColor: '#F4F6F9',
    borderTopWidth: 1,
    borderTopColor: '#D7DDE7',
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 4,
    gap: 4,
    elevation: 8,
    zIndex: 30,
    shadowColor: '#000',
    shadowOpacity: 0.16,
    shadowRadius: 6,
    shadowOffset: { width: 0, height: -2 },
  },

  termDockExpanded: {
    bottom: 0,
  },
  termDockCollapsed: {
    bottom: 0,
  },
  termDockImmersive: {
    backgroundColor: '#EEF2F8',
    borderTopColor: '#CFD6E2',
    bottom: 0,
  },
  dockBtn: {
    flex: 1,
    height: 32,
    borderRadius: 16,
    backgroundColor: 'rgba(110, 130, 180, 0.22)',
    borderWidth: 1,
    borderColor: '#5E73AA',
    alignItems: 'center',
    justifyContent: 'center',
  },
  dockBtnActive: {
    backgroundColor: 'rgba(89, 132, 230, 0.40)',
    borderColor: '#7C9EF2',
  },
  dockBtnText: {
    color: '#D7E3FF',
    fontSize: 11,
    fontWeight: '700',
  },
  dockBtnTextActive: {
    color: '#F0F6FF',
  },
  dockDivider: {
    width: 1,
    height: 20,
    backgroundColor: '#4A5F93',
  },
  dockIconBtn: {
    minWidth: 34,
    height: 34,
    borderRadius: 17,
    paddingHorizontal: 8,
    backgroundColor: '#243761',
    borderWidth: 1,
    borderColor: '#4F6EA8',
    alignItems: 'center',
    justifyContent: 'center',
  },
  dockIconBtnActive: {
    backgroundColor: '#2F5495',
    borderColor: '#7AA0F1',
  },
  dockIconText: {
    color: '#E0EBFF',
    fontSize: 11,
    fontWeight: '700',
  },
  dockIconTextActive: {
    color: '#C8DBFF',
  },
  dockKeyboardBtn: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: '#4B6FFF',
    borderWidth: 1,
    borderColor: '#AFC2FF',
    alignItems: 'center',
    justifyContent: 'center',
    elevation: 5,
  },
  dockKeyboardBtnActive: {
    backgroundColor: '#D8E7FF',
    borderColor: '#7B97CF',
  },
  dockKeyboardText: {
    color: '#FFFFFF',
    fontSize: 16,
    fontWeight: '700',
  },
  termAccessoryKey: {
    flex: 1,
    height: 34,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: '#D8DFEA',
    backgroundColor: '#FFFFFF',
    alignItems: 'center',
    justifyContent: 'center',
  },
  termAccessoryShiftKey: {
    flex: 1.2,
  },
  termAccessoryKeyboardKey: {
    flex: 0.9,
  },
  termAccessoryKeyPressed: {
    backgroundColor: '#EAF0F8',
  },
  termAccessoryText: {
    color: '#2F3A55',
    fontSize: 12,
    fontWeight: '700',
  },
  termAccessoryCtrlText: {
    fontSize: 10,
    lineHeight: 11,
    letterSpacing: 0.1,
  },
  termAccessoryShiftText: {
    fontSize: 10,
    lineHeight: 11,
    textAlign: 'center',
  },
  hiddenCommandInput: {
    position: 'absolute',
    width: 1,
    height: 1,
    opacity: 0,
    left: -200,
    bottom: 0,
  },

  commandComposerWrap: {
    position: 'absolute',
    left: 6,
    right: 6,
    bottom: 48,
    zIndex: 32,
  },
  commandComposerWrapImmersive: {
    bottom: 48,
  },
  commandComposer: {
    minHeight: 42,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: '#25365F',
    backgroundColor: 'rgba(9, 15, 34, 0.98)',
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 10,
    gap: 8,
    elevation: 8,
    shadowColor: '#000',
    shadowOpacity: 0.24,
    shadowRadius: 6,
    shadowOffset: { width: 0, height: 3 },
  },
  commandComposerPrompt: {
    color: '#58E8A7',
    fontSize: 15,
    fontWeight: '700',
  },
  commandComposerInput: {
    flex: 1,
    minHeight: 34,
    color: '#E7EFFC',
    fontSize: 13,
    paddingVertical: 0,
    includeFontPadding: false,
  },
  commandComposerSend: {
    minWidth: 40,
    height: 28,
    borderRadius: 8,
    backgroundColor: '#1B5FD7',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 8,
  },
  commandComposerSendText: {
    color: '#F4F8FF',
    fontSize: 11,
    fontWeight: '700',
  },
  commandComposerClose: {
    minWidth: 38,
    height: 28,
    borderRadius: 8,
    backgroundColor: '#1A2646',
    borderWidth: 1,
    borderColor: '#344A76',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 7,
  },
  commandComposerCloseText: {
    color: '#A9BADA',
    fontSize: 10,
    fontWeight: '600',
  },
  toastOverlay: {
    position: 'absolute',
    top: 8,
    left: 8,
    right: 8,
    zIndex: 10,
    backgroundColor: '#1A1A1A',
    borderWidth: 1,
    borderColor: '#333',
    borderRadius: 8,
    paddingVertical: 8,
    paddingHorizontal: 12,
    elevation: 6,
  },
  toastText: {
    color: '#EEE',
    fontSize: 12,
    fontWeight: '600',
    textAlign: 'center',
  },
  // Accessory Bar
  accessoryBar: {
    height: 44,
    flexShrink: 0,
    backgroundColor: '#E8ECF2',
    borderTopWidth: 1,
    borderTopColor: '#D2D7E2',
  },
  accessoryContent: {
    alignItems: 'center',
    paddingHorizontal: 8,
  },
  accKey: {
    minWidth: 44,
    paddingHorizontal: 10,
    height: 32,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: '#EFF2F8',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: '#CDD4E1',
    marginRight: 8,
  },
  accKeyPressed: {
    backgroundColor: '#DDE3EE',
  },
  accText: {
    color: '#2F3C5D',
    fontSize: 13,
    fontWeight: '700',
  },
  accTextCompact: {
    fontSize: 10,
    lineHeight: 11,
    textAlign: 'center',
    letterSpacing: 0,
  },
  accDiv: {
    width: 1,
    height: 18,
    backgroundColor: '#C4CBD8',
    marginRight: 8,
  },

  historyPanel: {
    maxHeight: 180,
    marginHorizontal: 12,
    marginBottom: 8,
    borderRadius: 12,
    backgroundColor: '#121212',
    borderWidth: 1,
    borderColor: '#2B3545',
    overflow: 'hidden',
    elevation: 8,
  },
  historyHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 12,
    paddingVertical: 10,
    backgroundColor: '#1A1A1A',
  },
  historyTitle: {
    color: '#888',
    fontSize: 11,
    fontWeight: '700',
    textTransform: 'uppercase',
  },
  historyClose: {
    color: theme.primary,
    fontSize: 11,
    fontWeight: '700',
  },
  historyBody: {
    maxHeight: 130,
  },
  historyBodyContent: {
    padding: 8,
  },
  historyItem: {
    height: 36,
    borderRadius: 8,
    backgroundColor: '#1A1A1A',
    justifyContent: 'center',
    paddingHorizontal: 12,
    marginBottom: 6,
  },
  historyItemPressed: {
    backgroundColor: '#222',
  },
  historyItemText: {
    color: '#BBB',
    fontSize: 13,
  },
  historyEmpty: {
    color: '#8FA0B5',
    fontSize: 12,
    textAlign: 'center',
    paddingVertical: 20,
  },

  // Input Area
  inputSafeWrap: {
    flexShrink: 0,
    paddingHorizontal: 12,
    paddingTop: 6,
    paddingBottom: Platform.OS === 'android' ? 16 : 8,
    backgroundColor: '#E8ECF2',
  },
  inputArea: {
    height: 52,
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#FFFFFF',
    borderRadius: 26,
    borderWidth: 1,
    borderColor: '#CFD6E3',
    paddingHorizontal: 16,
  },
  promptLabel: {
    color: '#2F3C5D',
    fontSize: 16,
    fontWeight: '700',
    marginRight: 10,
  },
  cmdInput: {
    flex: 1,
    color: '#1D2740',
    fontSize: 15,
    height: '100%',
    paddingVertical: 0,
  },
  historyBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: '#EEF2F8',
    alignItems: 'center',
    justifyContent: 'center',
    marginLeft: 8,
  },
  historyBtnActive: {
    backgroundColor: '#DCE3EE',
  },
  historyBtnText: {
    color: '#2F3C5D',
    fontSize: 14,
  },
  sendBtn: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: '#2F5DFF',
    alignItems: 'center',
    justifyContent: 'center',
    marginLeft: 10,
  },
  btnDisabled: { opacity: 0.3 },
  sendBtnText: { color: '#FFF', fontSize: 18, fontWeight: 'bold' },
});
