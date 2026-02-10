import { StatusBar } from 'expo-status-bar';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import * as Haptics from 'expo-haptics';
import Constants from 'expo-constants';
import { JetBrainsMono_400Regular, JetBrainsMono_500Medium, useFonts } from '@expo-google-fonts/jetbrains-mono';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  FlatList,
  GestureResponderEvent,
  Keyboard,
  LogBox,
  Modal,
  Alert,
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

const RELAY_URL = 'http://47.110.255.240:18787';
const RELAY_TOKEN =
  'doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff';

const SESSION_REFRESH_MS = 2500;
const TERMINAL_REFRESH_MS = 1400;
const ALERT_KEYWORDS = ['confirm', '是否继续', '请选择', 'panic', 'error', 'exception'];
const TERMINATE_SENTINEL = '__DO_AI_TERMINATE__';
const CLOSE_SESSION_HIDE_MS = 15000;
const TERMINATE_UNDO_WINDOW_MS = 2000;
const TERMINAL_FONT_SIZE_BASE = 13;
const TERMINAL_FONT_SIZE_MIN = 10;
const TERMINAL_FONT_SIZE_MAX = 24;
const TERMINAL_LINE_HEIGHT_RATIO = 18 / 13;

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

function sessionTitle(session: Session): string {
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

  text = text.replace(/\s{2,}/g, ' ').trim();
  return text;
}

function sanitizeTerminalLine(raw: string): string {
  const cleaned = normalizeTerminalText(raw);
  if (!cleaned) return '';
  if (isTmuxStatusLine(cleaned)) return '';
  const normalized = sanitizeCharsetLeakArtifacts(cleaned);
  if (!normalized) return '';
  if (isTmuxStatusLine(normalized)) return '';
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
    const normalizedHint = normalizeScreenHintLine(lineText);
    if (normalizedHint && (isTmuxStatusLine(normalizedHint) || normalizedHint === 'B')) {
      continue;
    }

    const next: OutputEvent = {
      seq: baseSeq + events.length,
      session_id: sessionId,
      text: lineText,
      ts,
    };
    if (segments.length > 0) {
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

// --- API ---

async function fetchRelayJson<T>(path: string, init?: RequestInit): Promise<T> {
  const traceId = nowTraceId('relay');
  const startedAt = Date.now();
  
  // logState('info', { trace_id: traceId, event: 'relay_req', path });

  const response = await fetch(`${RELAY_URL}${path}`, {
    ...init,
    headers: {
      ...(init?.headers || {}),
      'X-Relay-Token': RELAY_TOKEN,
    },
  });

  if (!response.ok) {
    logState('error', {
      trace_id: traceId,
      event: '[CRITICAL] req_fail',
      path,
      status: response.status,
    });
    throw new Error(`${path} ${response.status}`);
  }

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
      source: 'android-rn',
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
      source: 'android-rn-longpress',
    },
    {
      session_id: sessionId,
      input: TERMINATE_SENTINEL,
      submit: false,
      source: 'android-rn-longpress-fallback',
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
  const response = await fetch(`${RELAY_URL}${path}`, {
    headers: {
      'X-Relay-Token': RELAY_TOKEN,
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

  const [commandInput, setCommandInput] = useState('');
  const [sending, setSending] = useState(false);
  const [terminatingSessionId, setTerminatingSessionId] = useState('');
  const [pendingTerminateSessionId, setPendingTerminateSessionId] = useState('');
  const [closingSessionMap, setClosingSessionMap] = useState<Record<string, number>>({});
  const [toast, setToast] = useState('');
  const [autoSubmit, setAutoSubmit] = useState(true);

  const [notifyEnabled, setNotifyEnabled] = useState(false);
  const [followTail, setFollowTail] = useState(true);
  const [refreshRunning, setRefreshRunning] = useState(false);
  const [terminalFontSize, setTerminalFontSize] = useState(TERMINAL_FONT_SIZE_BASE);
  const [tabLayouts, setTabLayouts] = useState<Record<string, { x: number; width: number }>>({});
  const [sessionOrder, setSessionOrder] = useState<string[]>([]);
  const [sortMode, setSortMode] = useState(false);
  const [commandHistoryBySession, setCommandHistoryBySession] = useState<Record<string, string[]>>({});
  const [historyVisible, setHistoryVisible] = useState(false);
  const [controlPanelVisible, setControlPanelVisible] = useState(true);
  const [immersiveMode, setImmersiveMode] = useState(false);

  const terminalScrollRef = useRef<ScrollView>(null);
  const terminalTabScrollRef = useRef<ScrollView>(null);
  const commandInputRef = useRef<TextInput>(null);
  const terminalFontSizeRef = useRef(TERMINAL_FONT_SIZE_BASE);
  const pinchStartDistanceRef = useRef(0);
  const pinchStartFontSizeRef = useRef(TERMINAL_FONT_SIZE_BASE);
  const pinchActiveRef = useRef(false);
  const notifiedLineRef = useRef<Record<string, string>>({});
  const topLoadLockRef = useRef(false);
  const historyLoadingRef = useRef(false);
  const lastTabFocusSessionRef = useRef('');
  const suppressCardPressSessionRef = useRef('');
  const terminateTimerRef = useRef<Record<string, ReturnType<typeof setTimeout>>>({});
  const statusOnlyKickRef = useRef<Record<string, number>>({});
  const pulse = useRef(new Animated.Value(0.4)).current;
  const tabIndicatorX = useRef(new Animated.Value(0)).current;
  const tabIndicatorWidth = useRef(new Animated.Value(44)).current;
  const refreshProgress = useRef(new Animated.Value(0)).current;

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

  const pendingTerminateSession = useMemo(
    () => sessions.find((item) => item.session_id === pendingTerminateSessionId),
    [sessions, pendingTerminateSessionId],
  );

  const activeSessionIndex = useMemo(
    () => onlineSessions.findIndex((item) => item.session_id === activeSessionId),
    [onlineSessions, activeSessionId],
  );

  const activeCommandHistory = useMemo(() => {
    if (!activeSessionId) return [];
    return commandHistoryBySession[activeSessionId] ?? [];
  }, [activeSessionId, commandHistoryBySession]);

  const isAiTuiSession = useMemo(() => {
    if (!activeSession) return false;
    const signature = `${activeSession.session_name ?? ''} ${activeSession.command ?? ''}`.toLowerCase();
    return signature.includes('claude') || signature.includes('codex') || signature.includes('gemini');
  }, [activeSession]);

  const canSend = useMemo(
    () => !sending && (autoSubmit || commandInput.trim().length > 0),
    [sending, autoSubmit, commandInput],
  );

  const refreshProgressWidth = useMemo(
    () =>
      refreshProgress.interpolate({
        inputRange: [0, 1],
        outputRange: ['0%', '100%'],
      }),
    [refreshProgress],
  );

  const terminalLineHeight = useMemo(
    () => Math.round(terminalFontSize * TERMINAL_LINE_HEIGHT_RATIO),
    [terminalFontSize],
  );

  const terminalZoomLabel = useMemo(
    () => `${Math.round((terminalFontSize / TERMINAL_FONT_SIZE_BASE) * 100)}%`,
    [terminalFontSize],
  );

  const monoFont = Platform.OS === 'android' ? 'monospace' : (fontsLoaded ? 'JetBrainsMono_400Regular' : 'monospace');
  const monoFontStrong = Platform.OS === 'android' ? 'monospace' : (fontsLoaded ? 'JetBrainsMono_500Medium' : 'monospace');

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
    terminalFontSizeRef.current = terminalFontSize;
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
    setFollowTail(true);
    setHistoryVisible(false);
    historyLoadingRef.current = false;
  }, [activeSessionId]);

  useEffect(() => {
    if (onlineSessions.length < 2 && sortMode) {
      setSortMode(false);
    }
  }, [onlineSessions.length, sortMode]);

  useEffect(() => {
    if (!terminalVisible) {
      setHistoryVisible(false);
      setSortMode(false);
      setImmersiveMode(false);
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

  // --- Logic ---

  const reloadTerminalTail = useCallback(async (options?: { showProgress?: boolean }) => {
    if (!activeSessionId) {
      setTerminalLines([]);
      return;
    }

    const showProgress = options?.showProgress === true;
    if (showProgress) {
      setRefreshRunning(true);
      refreshProgress.stopAnimation();
      refreshProgress.setValue(0);
      Animated.timing(refreshProgress, {
        toValue: 0.86,
        duration: 420,
        useNativeDriver: false,
      }).start();
    }

    try {
      const screen = await fetchOutputScreen(activeSessionId);
      if (screen) {
        const rawScreenLines = Array.isArray(screen.lines) ? screen.lines : [];
        const rawStyledLines = Array.isArray(screen.styled_lines) ? screen.styled_lines : [];
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
            void sendControl(activeSessionId, '', false)
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

        setTerminalLines((previous) => (areOutputEventsEqual(previous, nextEvents) ? previous : nextEvents));
        setCursor(screen.revision || 0);
        setHasMoreBefore(!!screen.truncated);
        setScreenMode(true);
        setTerminalError('');
        return;
      }

      setScreenMode(false);
      const data = await fetchOutputTail(activeSessionId);
      const nextEvents = sanitizeOutputEvents(data.events || []);
      setTerminalLines((previous) => (areOutputEventsEqual(previous, nextEvents) ? previous : nextEvents));
      setCursor(data.cursor || 0);
      setHasMoreBefore(!!data.has_more_before);
      setTerminalError('');
    } catch (err) {
      setTerminalError(err instanceof Error ? err.message : 'fetch failed');
    } finally {
      if (!showProgress) {
        setRefreshRunning(false);
        return;
      }
      Animated.timing(refreshProgress, {
        toValue: 1,
        duration: 180,
        useNativeDriver: false,
      }).start(() => {
        refreshProgress.setValue(0);
        setRefreshRunning(false);
      });
    }
  }, [activeSessionId, activeSession, refreshProgress]);

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
      void reloadTerminalTail({ showProgress: true });
      const t = setInterval(() => {
        void reloadTerminalTail();
      }, TERMINAL_REFRESH_MS);
      return () => clearInterval(t);
    }
  }, [activeSessionId, terminalVisible, reloadTerminalTail]);

  useEffect(() => {
    if (!terminalVisible) return;
    if (isAiTuiSession) {
      setControlPanelVisible(false);
      setHistoryVisible(false);
      return;
    }
    setControlPanelVisible(true);
  }, [terminalVisible, activeSessionId, isAiTuiSession]);

  useEffect(() => {
    if (!terminalVisible || !immersiveMode) return;
    setControlPanelVisible(false);
    setHistoryVisible(false);
  }, [terminalVisible, immersiveMode]);

  const sendInput = useCallback(
    async (text: string, submit: boolean, recordHistory = false) => {
      if (!activeSessionId) return;
      if (!text && !submit) return;

      const traceId = nowTraceId('ctrl');
      const startedAt = Date.now();
      setSending(true);

      try {
        await sendControl(activeSessionId, text, submit);

        if (recordHistory && text.trim()) {
          const normalized = text.trim();
          setCommandHistoryBySession((previous) => {
            const current = previous[activeSessionId] ?? [];
            const merged = [normalized, ...current.filter((entry) => entry !== normalized)].slice(0, 30);
            return {
              ...previous,
              [activeSessionId]: merged,
            };
          });
        }

        if (text) setCommandInput('');
        setTimeout(() => {
          void reloadTerminalTail();
        }, 150);

        logState('info', {
          trace_id: traceId,
          event: 'control_ok',
          session_id: activeSessionId,
          submit,
          bytes: text.length,
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
      } finally {
        setSending(false);
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
    const title = sessionTitle(session);

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
              source: 'android-rn-longpress',
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
  }, [activeSessionId, refetch]);

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

  const toggleHistoryPanel = useCallback(() => {
    hitLight();
    setHistoryVisible((previous) => !previous);
  }, [hitLight]);

  const applyHistoryCommand = useCallback((command: string) => {
    hitLight();
    setCommandInput(command);
    setHistoryVisible(false);
  }, [hitLight]);

  const terminalSwipeResponder = useMemo(
    () =>
      PanResponder.create({
        onMoveShouldSetPanResponder: (event, gesture) => {
          const pinchDistance = getPinchDistance(event);
          if (pinchDistance !== null) {
            return true;
          }
          const horizontal = Math.abs(gesture.dx);
          const vertical = Math.abs(gesture.dy);
          return horizontal > 16 && horizontal > vertical * 1.25;
        },
        onPanResponderGrant: (event) => {
          const pinchDistance = getPinchDistance(event);
          if (pinchDistance === null) {
            pinchActiveRef.current = false;
            pinchStartDistanceRef.current = 0;
            return;
          }
          pinchActiveRef.current = true;
          pinchStartDistanceRef.current = pinchDistance;
          pinchStartFontSizeRef.current = terminalFontSizeRef.current;
        },
        onPanResponderMove: (event) => {
          if (!pinchActiveRef.current || pinchStartDistanceRef.current <= 0) {
            return;
          }
          const pinchDistance = getPinchDistance(event);
          if (pinchDistance === null) {
            return;
          }
          const scale = pinchDistance / pinchStartDistanceRef.current;
          const nextSize = clampNumber(
            Math.round(pinchStartFontSizeRef.current * scale),
            TERMINAL_FONT_SIZE_MIN,
            TERMINAL_FONT_SIZE_MAX,
          );
          setTerminalFontSize((previous) => (previous === nextSize ? previous : nextSize));
        },
        onPanResponderRelease: (_event, gesture) => {
          if (pinchActiveRef.current) {
            pinchActiveRef.current = false;
            pinchStartDistanceRef.current = 0;
            return;
          }
          if (gesture.dx <= -42 || gesture.vx <= -0.36) {
            switchTerminalTabByOffset(1);
            return;
          }
          if (gesture.dx >= 42 || gesture.vx >= 0.36) {
            switchTerminalTabByOffset(-1);
          }
        },
        onPanResponderTerminate: () => {
          pinchActiveRef.current = false;
          pinchStartDistanceRef.current = 0;
        },
        onPanResponderTerminationRequest: () => !pinchActiveRef.current,
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
    setTerminalVisible(false);
  }, [hitLight]);

  const refreshTerminal = useCallback(() => {
    hitLight();
    void reloadTerminalTail();
  }, [hitLight, reloadTerminalTail]);

  const triggerQuickKey = useCallback((key: string) => {
    hitLight();
    void sendQuickKey(key);
  }, [hitLight, sendQuickKey]);

  const triggerQuickInput = useCallback((textValue: string) => {
    hitLight();
    void sendInput(textValue, false, false);
  }, [hitLight, sendInput]);

  const triggerQuickSubmit = useCallback(() => {
    hitMedium();
    void sendInput('', true, false);
  }, [hitMedium, sendInput]);

  const triggerSend = useCallback(() => {
    hitMedium();
    void sendInput(commandInput, autoSubmit, true);
  }, [hitMedium, sendInput, commandInput, autoSubmit]);

  const increaseFont = useCallback(() => {
    hitLight();
    setTerminalFontSize((previous) => clampNumber(previous + 1, TERMINAL_FONT_SIZE_MIN, TERMINAL_FONT_SIZE_MAX));
  }, [hitLight]);

  const decreaseFont = useCallback(() => {
    hitLight();
    setTerminalFontSize((previous) => clampNumber(previous - 1, TERMINAL_FONT_SIZE_MIN, TERMINAL_FONT_SIZE_MAX));
  }, [hitLight]);

  const toggleControlPanel = useCallback(() => {
    hitLight();
    setControlPanelVisible((previous) => {
      const next = !previous;
      if (!next) {
        setHistoryVisible(false);
      }
      return next;
    });
  }, [hitLight]);

  const toggleImmersiveMode = useCallback(() => {
    hitLight();
    setImmersiveMode((previous) => !previous);
  }, [hitLight]);

  const focusCommandInput = useCallback(() => {
    hitLight();
    if (!controlPanelVisible) {
      setControlPanelVisible(true);
    }
    setTimeout(() => {
      commandInputRef.current?.focus();
    }, 60);
  }, [hitLight, controlPanelVisible]);

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
            正在关闭 {sessionTitle(pendingTerminateSession || { session_id: pendingTerminateSessionId, host: '', online: true })}（2秒内可撤销）
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
                  <Text style={styles.hostInitial}>{titleInitial(sessionTitle(item))}</Text>
                </View>
                <View style={styles.cardBody}>
                  <View style={styles.cardRow}>
                    <Text style={styles.hostName} numberOfLines={1}>{sessionTitle(item)}</Text>
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
      <View style={styles.bottomNav}>
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
      <Modal visible={terminalVisible} animationType="slide" presentationStyle="fullScreen">
        <View style={styles.termContainer}>
          <StatusBar style="light" backgroundColor="#12163A" />
          
          {/* Term Header */}
          <View style={styles.termHeader}>
            <Pressable style={({ pressed }) => [styles.termBackBtn, pressed && styles.pressDown]} onPress={closeTerminal}>
              <Text style={styles.termBackText}>‹</Text>
            </Pressable>

            <ScrollView
              ref={terminalTabScrollRef}
              horizontal
              showsHorizontalScrollIndicator={false}
              style={styles.termTabScroll}
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
                      {sessionTitle(s)}
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

            <View style={styles.termActionGroup}>
              <Pressable
                style={({ pressed }) => [styles.termActionBtn, pressed && styles.pressDown]}
                onPress={toggleControlPanel}
              >
                <Text style={styles.termActionText}>{controlPanelVisible ? '⌄' : '⌃'}</Text>
              </Pressable>
              <Pressable style={({ pressed }) => [styles.termActionBtn, pressed && styles.pressDown]} onPress={toggleSortMode}>
                <Text style={[styles.termActionText, sortMode && styles.textBlue]}>{sortMode ? '✓' : '+'}</Text>
              </Pressable>
              <Pressable style={({ pressed }) => [styles.termActionBtn, pressed && styles.pressDown]} onPress={refreshTerminal}>
                <Text style={styles.termActionText}>↻</Text>
              </Pressable>
            </View>
          </View>
          <View style={styles.termProgressTrack}>
            <Animated.View
              style={[
                styles.termProgressBar,
                {
                  width: refreshProgressWidth,
                  opacity: refreshRunning ? 1 : 0.2,
                },
              ]}
            />
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

          {!immersiveMode && !!toast && (
            <View style={styles.toastOverlay}>
              <Text style={styles.toastText}>{toast}</Text>
            </View>
          )}

          {/* Term Output */}
          <View style={styles.termViewport} {...terminalSwipeResponder.panHandlers}>
             <ScrollView
              ref={terminalScrollRef}
              style={styles.termScroll}
              contentContainerStyle={styles.termScrollContent}
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
                  numberOfLines={1}
                  ellipsizeMode="clip"
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
            <View style={[styles.termFloatStats, immersiveMode && styles.termFloatStatsImmersive]}>
              <Text style={styles.termStatsText}>
                {(screenMode ? `R${cursor} • ${terminalLines.length}` : `${cursor}x${terminalLines.length}`) + ` • Z${terminalZoomLabel}`}
              </Text>
            </View>
            {!followTail && (
              <Pressable
                style={({ pressed }) => [styles.termJumpBtn, pressed && styles.pressDown]}
                onPress={() => {
                  hitLight();
                  setFollowTail(true);
                  terminalScrollRef.current?.scrollToEnd({ animated: true });
                }}
              >
                <Text style={styles.termJumpText}>Latest ↓</Text>
              </Pressable>
            )}

            <View style={[styles.termDock, immersiveMode && styles.termDockImmersive]}>
              <Pressable
                style={({ pressed }) => [styles.dockBtn, controlPanelVisible && styles.dockBtnActive, pressed && styles.pressDown]}
                onPress={toggleControlPanel}
              >
                <Text style={[styles.dockBtnText, controlPanelVisible && styles.dockBtnTextActive]}>{controlPanelVisible ? '工具收起' : '工具展开'}</Text>
              </Pressable>

              <Pressable style={({ pressed }) => [styles.dockIconBtn, pressed && styles.pressDown]} onPress={decreaseFont}>
                <Text style={styles.dockIconText}>A-</Text>
              </Pressable>

              <Pressable style={({ pressed }) => [styles.dockIconBtn, pressed && styles.pressDown]} onPress={increaseFont}>
                <Text style={styles.dockIconText}>A+</Text>
              </Pressable>

              <Pressable
                style={({ pressed }) => [styles.dockIconBtn, immersiveMode && styles.dockIconBtnActive, pressed && styles.pressDown]}
                onPress={toggleImmersiveMode}
              >
                <Text style={[styles.dockIconText, immersiveMode && styles.dockIconTextActive]}>{immersiveMode ? '全' : '沉'}</Text>
              </Pressable>

              <Pressable style={({ pressed }) => [styles.dockKeyboardBtn, pressed && styles.pressDown]} onPress={focusCommandInput}>
                <Text style={styles.dockKeyboardText}>⌨</Text>
              </Pressable>
            </View>
          </View>

          {!immersiveMode && controlPanelVisible && (
            <>
              {/* Accessory Bar */}
              <View style={styles.accessoryBar}>
                <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.accessoryContent}>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={() => triggerQuickKey('ctrlc')}><Text style={styles.accText}>☎</Text></Pressable>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={() => triggerQuickKey('esc')}><Text style={styles.accText}>✶</Text></Pressable>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={() => triggerQuickInput('***')}><Text style={styles.accText}>***</Text></Pressable>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={() => triggerQuickInput('{}')}><Text style={styles.accText}>{'{}'}</Text></Pressable>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={toggleHistoryPanel}><Text style={styles.accText}>📁</Text></Pressable>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={() => triggerQuickKey('shifttab')}><Text style={[styles.accText, styles.accTextCompact]}>{'shift\ntab'}</Text></Pressable>
                  <Pressable style={({ pressed }) => [styles.accKey, pressed && styles.accKeyPressed]} onPress={triggerQuickSubmit}><Text style={styles.accText}>⌨</Text></Pressable>
                </ScrollView>
              </View>

              {historyVisible && (
                <View style={styles.historyPanel}>
                  <View style={styles.historyHeader}>
                    <Text style={styles.historyTitle}>Recent Commands</Text>
                    <Pressable style={({ pressed }) => [pressed && styles.pressDown]} onPress={toggleHistoryPanel}>
                      <Text style={styles.historyClose}>收起</Text>
                    </Pressable>
                  </View>
                  <ScrollView style={styles.historyBody} contentContainerStyle={styles.historyBodyContent}>
                    {activeCommandHistory.length ? (
                      activeCommandHistory.map((command, idx) => (
                        <Pressable
                          key={`${command}-${idx}`}
                          style={({ pressed }) => [styles.historyItem, pressed && styles.historyItemPressed]}
                          onPress={() => applyHistoryCommand(command)}
                        >
                          <Text style={[styles.historyItemText, { fontFamily: monoFont }]} numberOfLines={1}>
                            {command}
                          </Text>
                        </Pressable>
                      ))
                    ) : (
                      <Text style={styles.historyEmpty}>当前会话暂无历史命令</Text>
                    )}
                  </ScrollView>
                </View>
              )}

              {/* Input Area */}
              <View style={styles.inputSafeWrap}>
                <View style={styles.inputArea}>
                  <Text style={[styles.promptLabel, { fontFamily: monoFontStrong }]}>$</Text>
                  <TextInput
                    ref={commandInputRef}
                    style={[styles.cmdInput, { fontFamily: monoFont }]}
                    value={commandInput}
                    onChangeText={setCommandInput}
                    placeholder="Enter command..."
                    placeholderTextColor="#7F93C4"
                    autoCapitalize="none"
                    autoCorrect={false}
                    onSubmitEditing={triggerSend}
                    blurOnSubmit={false}
                  />
                  <Pressable
                    style={({ pressed }) => [
                      styles.historyBtn,
                      historyVisible && styles.historyBtnActive,
                      pressed && styles.pressDown,
                    ]}
                    onPress={toggleHistoryPanel}
                  >
                    <Text style={styles.historyBtnText}>⌛</Text>
                  </Pressable>
                  <Pressable
                    style={({ pressed }) => [
                      styles.sendBtn,
                      (!canSend || sending) && styles.btnDisabled,
                      pressed && styles.pressDown,
                    ]}
                    onPress={triggerSend}
                    disabled={!canSend}
                  >
                    {sending ? <ActivityIndicator color="#FFFFFF" size="small" /> : <Text style={styles.sendBtnText}>↵</Text>}
                  </Pressable>
                </View>
              </View>
            </>
          )}
          
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
    backgroundColor: '#0A0E14',
  },
  termHeader: {
    height: 56 + (Platform.OS === 'android' ? Constants.statusBarHeight : 0),
    paddingTop: Platform.OS === 'android' ? Constants.statusBarHeight : 0,
    flexDirection: 'row',
    alignItems: 'center',
    borderBottomWidth: 1,
    borderBottomColor: '#2B3569',
    backgroundColor: '#12163A',
    position: 'relative',
    elevation: 4,
  },
  termBackBtn: {
    width: 44,
    marginLeft: 8,
    marginRight: 8,
    marginVertical: 8,
    borderRadius: 14,
    backgroundColor: 'rgba(255,255,255,0.06)',
    height: '100%',
    alignItems: 'center',
    justifyContent: 'center',
  },
  termBackText: { color: '#AFC2F7', fontSize: 22, fontWeight: '600' },
  termActionGroup: {
    flexDirection: 'row',
    height: '100%',
  },
  termActionBtn: {
    width: 42,
    marginVertical: 8,
    marginRight: 8,
    borderRadius: 14,
    backgroundColor: 'rgba(255,255,255,0.06)',
    height: '100%',
    alignItems: 'center',
    justifyContent: 'center',
  },
  termActionText: { color: '#AFC2F7', fontSize: 19, fontWeight: '700' },
  termProgressTrack: {
    height: 2,
    backgroundColor: '#000',
    overflow: 'hidden',
  },
  termProgressBar: {
    height: '100%',
    backgroundColor: theme.primary,
  },
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
  termTabRail: {
    flexDirection: 'row',
    alignItems: 'center',
    position: 'relative',
  },
  termTab: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 14,
    height: 36,
    marginVertical: 10,
    marginRight: 8,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: '#2A3156',
    backgroundColor: '#1A2045',
  },
  termTabActive: {
    backgroundColor: '#133B3A',
    borderWidth: 1,
    borderColor: '#1F8C68',
  },
  termTabSorting: {
    backgroundColor: '#333',
    borderColor: theme.primary,
  },
  termTabPressed: {
    opacity: 0.8,
  },
  termTabDot: { width: 6, height: 6, borderRadius: 3, marginRight: 6 },
  termTabText: { color: '#A7B3D8', fontSize: 13, fontWeight: '700' },
  termTabTextActive: { color: '#31D684' },
  termTabIndicator: {
    position: 'absolute',
    left: 0,
    bottom: 8,
    height: 2,
    borderRadius: 1,
    backgroundColor: theme.primary,
  },

  termViewport: {
    flex: 1,
    backgroundColor: '#0B1230',
    position: 'relative',
  },
  termScroll: { flex: 1 },
  termScrollContent: { padding: 8, paddingBottom: 20 },
  termLine: {
    color: '#34D779',
    fontSize: 13,
    lineHeight: 18,
    includeFontPadding: false,
    letterSpacing: 0,
    flexWrap: 'nowrap',
  },
  termFloatStats: {
    position: 'absolute',
    top: 8,
    right: 8,
    backgroundColor: 'rgba(15, 32, 78, 0.88)',
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderRadius: 4,
  },
  termStatsText: { color: '#7EE2AE', fontSize: 9, fontWeight: '700' },
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
    bottom: 12,
    backgroundColor: '#141D41',
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderWidth: 1,
    borderColor: '#2C3A74',
    elevation: 4,
  },
  termJumpText: {
    color: '#B6C5EC',
    fontSize: 11,
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
