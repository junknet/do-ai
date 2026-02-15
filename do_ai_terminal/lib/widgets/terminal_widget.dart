import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/io.dart';
import 'package:xterm/xterm.dart';

import '../core/terminal_sanitize.dart';
import '../core/structured_log.dart';
import '../services/api_service.dart';

/// xterm 终端组件 - WebSocket 增量流 + 断线快照兜底
class TerminalWidget extends ConsumerStatefulWidget {
  final String sessionId;
  final double fontSize;
  final MobileRenderMode renderMode;
  final TerminalNoiseProfile noiseProfile;
  final ValueChanged<bool>? onWsStateChanged;
  final ValueChanged<String>? onErrorChanged;

  const TerminalWidget({
    super.key,
    required this.sessionId,
    this.fontSize = 11,
    this.renderMode = MobileRenderMode.clean,
    this.noiseProfile = TerminalNoiseProfile.gemini,
    this.onWsStateChanged,
    this.onErrorChanged,
  });

  @override
  ConsumerState<TerminalWidget> createState() => _TerminalWidgetState();
}

class _TerminalWidgetState extends ConsumerState<TerminalWidget> {
  static const TerminalTheme _xterm256Theme = TerminalThemes.defaultTheme;

  late Terminal _terminal;
  IOWebSocketChannel? _wsChannel;
  StreamSubscription<dynamic>? _wsSubscription;
  Timer? _reconnectTimer;
  Timer? _resizeControlTimer;
  Timer? _inputFlushTimer;
  Timer? _cleanReplayTimer;
  final String _traceId = StructuredLog.newTraceId('terminal_widget');
  final List<String> _rawUtf8Fragments = <String>[];
  late ByteConversionSink _rawUtf8Sink;
  String _rawSanitizeCarry = '';
  bool _syncUpdateActive = false;
  final StringBuffer _syncUpdateBuffer = StringBuffer();
  final DateTime _startedAt = DateTime.now();

  int _viewCols = 0;
  int _viewRows = 0;
  int _pendingResizeCols = 0;
  int _pendingResizeRows = 0;
  int _sentResizeCols = 0;
  int _sentResizeRows = 0;
  bool _disposed = false;
  bool _wsLive = false;
  int _reconnectAttempt = 0;
  bool _sendingInput = false;
  bool _snapshotFetching = false;
  bool _startupAutoCleanSent = false;
  late MobileRenderMode _renderMode;
  String _lastCleanDeltaLine = '';
  String _lastUiError = '';
  final StringBuffer _pendingInput = StringBuffer();
  static const Duration _resizeControlDebounce = Duration(milliseconds: 140);
  static const int _defaultSnapshotCols = 80;
  static const int _defaultSnapshotRows = 24;
  static const String _syncUpdateStart = '\u001b[?2026h';
  static const String _syncUpdateEnd = '\u001b[?2026l';
  static const String _tmuxDcsStart = '\u001bPtmux;';
  static const String _autoCleanPayload = '\f';
  static const Duration _cleanReplayInterval = Duration(milliseconds: 900);

  @override
  void initState() {
    super.initState();
    _resetRawDecoder();
    _renderMode = widget.renderMode;
    _terminal = _newTerminal();
    _emitRenderModeSet(
      reason: 'init',
      previousMode: null,
      nextMode: _renderMode,
    );
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.init',
      anchor: 'terminal_widget_init',
      context: {
        'session_id': widget.sessionId,
        'render_mode': mobileRenderModeValue(_renderMode),
        'noise_profile': terminalNoiseProfileValue(widget.noiseProfile),
      },
    );
    _bootstrapRealtime();
  }

  @override
  void didUpdateWidget(covariant TerminalWidget oldWidget) {
    super.didUpdateWidget(oldWidget);
    final sessionChanged = oldWidget.sessionId != widget.sessionId;
    if (sessionChanged) {
      _wsLive = false;
      _pendingInput.clear();
      _reconnectTimer?.cancel();
      _reconnectTimer = null;
      _resizeControlTimer?.cancel();
      _resizeControlTimer = null;
      _cleanReplayTimer?.cancel();
      _cleanReplayTimer = null;
      _wsSubscription?.cancel();
      _wsSubscription = null;
      _wsChannel?.sink.close();
      _wsChannel = null;
      _notifyWsState(false);
      _updateUiError('');
      _terminal = _newTerminal();
      _rawUtf8Sink.close();
      _resetRawDecoder();
      _rawSanitizeCarry = '';
      _syncUpdateActive = false;
      _syncUpdateBuffer.clear();
      _pendingResizeCols = 0;
      _pendingResizeRows = 0;
      _sentResizeCols = 0;
      _sentResizeRows = 0;
      _renderMode = widget.renderMode;
      _lastCleanDeltaLine = '';
      _emitRenderModeSet(
        reason: 'session_changed',
        previousMode: null,
        nextMode: _renderMode,
      );
      unawaited(_bootstrapRealtime());
      return;
    }

    if (oldWidget.renderMode != widget.renderMode) {
      final previous = _renderMode;
      _renderMode = widget.renderMode;
      _lastCleanDeltaLine = '';
      if (_renderMode != MobileRenderMode.clean) {
        _cleanReplayTimer?.cancel();
        _cleanReplayTimer = null;
      }
      _emitRenderModeSet(
        reason: 'ui_switch',
        previousMode: previous,
        nextMode: _renderMode,
      );
      unawaited(_fetchReplay(reason: 'render_mode_switch', force: true));
      return;
    }

    if (oldWidget.noiseProfile != widget.noiseProfile) {
      _emitRenderModeSet(
        reason: 'noise_profile_switch',
        previousMode: _renderMode,
        nextMode: _renderMode,
      );
      unawaited(_fetchReplay(reason: 'noise_profile_switch', force: true));
    }
  }

  Terminal _newTerminal() {
    return Terminal(
      maxLines: 10000,
      reflowEnabled: true,
      onOutput: _onTerminalOutput,
      onResize: (int cols, int rows, int _, int __) {
        _onTerminalResize(cols, rows);
      },
    );
  }

  Future<void> _bootstrapRealtime() async {
    await _fetchReplay(reason: 'bootstrap', force: true);
    _connectWs();
  }

  void _connectWs() {
    if (_disposed) {
      return;
    }
    _wsSubscription?.cancel();
    _wsSubscription = null;
    _wsChannel?.sink.close();
    _wsChannel = null;

    final apiService = ref.read(apiServiceProvider);
    final channel = apiService.connectOutputWs(
      sessionId: widget.sessionId,
      traceId: _traceId,
    );
    _wsChannel = channel;
    _wsSubscription = channel.stream.listen(
      _onWsData,
      onDone: _onWsDisconnected,
      onError: (Object error, StackTrace stackTrace) {
        unawaited(
          StructuredLog.critical(
            traceId: _traceId,
            event: '[CRITICAL] terminal.ws.error',
            anchor: 'ws_stream',
            error: error,
            stackTrace: stackTrace,
            context: {
              'session_id': widget.sessionId,
            },
          ),
        );
        _onWsDisconnected();
      },
      cancelOnError: true,
    );
  }

  void _onWsData(dynamic payload) {
    if (_disposed) {
      return;
    }
    final message = _decodeWsMessage(payload);
    if (message == null) {
      return;
    }

    if (!_wsLive) {
      _wsLive = true;
      _reconnectAttempt = 0;
      _notifyWsState(true);
      _updateUiError('');
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.ws.live',
        anchor: 'ws_stream',
        context: {
          'session_id': widget.sessionId,
        },
      );
    }

    if (message.type == 'snapshot' && message.snapshot != null) {
      _applyReplay(message.snapshot!, reason: 'ws_snapshot');
      return;
    }

    if (message.type == 'delta' && message.delta != null) {
      _applyDelta(message.delta!);
      return;
    }
  }

  void _onWsDisconnected() {
    if (_disposed) {
      return;
    }
    _wsLive = false;
    _notifyWsState(false);
    _updateUiError('WebSocket 已断开，正在重连');
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_disposed) {
      return;
    }
    if (_reconnectTimer?.isActive == true) {
      return;
    }
    final seconds = math.min(8, 1 << _reconnectAttempt);
    _reconnectAttempt = math.min(_reconnectAttempt + 1, 4);
    _reconnectTimer = Timer(Duration(seconds: seconds), () {
      if (_disposed) {
        return;
      }
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.ws.reconnect',
        anchor: 'ws_reconnect',
        context: {
          'session_id': widget.sessionId,
          'attempt': _reconnectAttempt,
          'delay_seconds': seconds,
        },
      );
      _connectWs();
    });
  }

  Future<void> _fetchReplay({
    required String reason,
    bool force = false,
  }) async {
    if (_disposed) {
      return;
    }
    if (_snapshotFetching && !force) {
      return;
    }
    _snapshotFetching = true;
    final stopwatch = Stopwatch()..start();
    try {
      final apiService = ref.read(apiServiceProvider);
      final output = await apiService.getScreenOutput(
        widget.sessionId,
        traceId: _traceId,
        cols: _viewCols > 0 ? _viewCols : null,
      );
      _applyReplay(output, reason: reason);
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.replay.fetch_ok',
        anchor: 'replay_fetch',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'session_id': widget.sessionId,
          'reason': reason,
          'revision': output.revision,
          'has_raw_replay': output.hasRawReplay,
          'byte_offset': output.byteOffset,
        },
      );
    } catch (e, stackTrace) {
      _updateUiError('终端同步失败，请检查 relay 连通性');
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.replay.fetch_failed',
          anchor: 'replay_fetch',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
          context: {
            'session_id': widget.sessionId,
            'reason': reason,
          },
        ),
      );
    } finally {
      _snapshotFetching = false;
    }
  }

  void _applyReplay(ScreenOutput output, {required String reason}) {
    if (_disposed) {
      return;
    }

    if (_renderMode == MobileRenderMode.auto &&
        shouldAutoSwitchToCleanMode(
          output.lines,
          profile: widget.noiseProfile,
        )) {
      _setRenderMode(
        MobileRenderMode.clean,
        reason: 'auto_detected_noise',
      );
    }

    if (_renderMode == MobileRenderMode.clean) {
      _ensureCleanReplayLoop();
      _applyCleanReplay(output, reason: reason);
      return;
    }
    _cleanReplayTimer?.cancel();
    _cleanReplayTimer = null;

    if (!output.hasRawReplay) {
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.replay.no_raw_replay',
        anchor: 'replay_apply',
        context: {
          'session_id': widget.sessionId,
          'reason': reason,
        },
      );
      return;
    }

    final nextTerminal = _newTerminal();
    final cols = _viewCols > 0 ? _viewCols : _defaultSnapshotCols;
    final rows = _viewRows > 0 ? _viewRows : _defaultSnapshotRows;
    nextTerminal.resize(cols, rows);

    try {
      final bytes = base64.decode(output.rawReplay);
      final chunk = _decodeRawChunk(bytes);
      if (chunk.isNotEmpty) {
        nextTerminal.write(chunk);
      }
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.replay.decode_failed',
          anchor: 'replay_apply',
          error: e,
          stackTrace: stackTrace,
          context: {
            'session_id': widget.sessionId,
            'reason': reason,
            'raw_replay_len': output.rawReplay.length,
          },
        ),
      );
      return;
    }

    if (!mounted || _disposed) {
      return;
    }

    setState(() {
      _terminal = nextTerminal;
    });

    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.replay.applied',
      anchor: 'replay_apply',
      context: {
        'session_id': widget.sessionId,
        'reason': reason,
        'cols': cols,
        'rows': rows,
        'revision': output.revision,
        'byte_offset': output.byteOffset,
        'render_mode': mobileRenderModeValue(_renderMode),
      },
    );

    unawaited(_maybeAutoCleanStartupNoise(output, reason: reason));
  }

  void _setRenderMode(
    MobileRenderMode nextMode, {
    required String reason,
  }) {
    if (_renderMode == nextMode) {
      return;
    }
    final previous = _renderMode;
    _renderMode = nextMode;
    _lastCleanDeltaLine = '';
    if (_renderMode != MobileRenderMode.clean) {
      _cleanReplayTimer?.cancel();
      _cleanReplayTimer = null;
    }
    _emitRenderModeSet(
      reason: reason,
      previousMode: previous,
      nextMode: nextMode,
    );
  }

  void _emitRenderModeSet({
    required String reason,
    required MobileRenderMode? previousMode,
    required MobileRenderMode nextMode,
  }) {
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.render.mode_set',
      anchor: 'render_mode_switch',
      context: {
        'session_id': widget.sessionId,
        'reason': reason,
        'prev_mode':
            previousMode == null ? '' : mobileRenderModeValue(previousMode),
        'render_mode': mobileRenderModeValue(nextMode),
        'noise_profile': terminalNoiseProfileValue(widget.noiseProfile),
      },
    );
  }

  Future<void> _maybeAutoCleanStartupNoise(
    ScreenOutput output, {
    required String reason,
  }) async {
    if (_disposed || _startupAutoCleanSent) {
      return;
    }
    final age = DateTime.now().difference(_startedAt);
    if (age > const Duration(seconds: 45)) {
      return;
    }
    if (!_hasStartupBlockNoise(output.lines)) {
      return;
    }
    _startupAutoCleanSent = true;
    try {
      await ref.read(apiServiceProvider).sendControl(
            sessionId: widget.sessionId,
            data: _autoCleanPayload,
            submit: false,
            source: 'flutter-terminal-autoclean',
          );
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.autoclean.sent',
        anchor: 'startup_noise_cleanup',
        context: {
          'session_id': widget.sessionId,
          'reason': reason,
        },
      );
      await Future<void>.delayed(const Duration(milliseconds: 280));
      if (!_disposed) {
        await _fetchReplay(reason: 'autoclean_refresh', force: true);
      }
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.autoclean.failed',
          anchor: 'startup_noise_cleanup',
          error: e,
          stackTrace: stackTrace,
          context: {
            'session_id': widget.sessionId,
            'reason': reason,
          },
        ),
      );
    }
  }

  bool _hasStartupBlockNoise(List<String> lines) {
    return shouldAutoSwitchToCleanMode(
      lines,
      profile: widget.noiseProfile,
    );
  }

  void _applyDelta(OutputWsDelta delta) {
    if (_disposed) {
      return;
    }

    if (_renderMode == MobileRenderMode.clean) {
      if (delta.lines.isNotEmpty) {
        _applyCleanDeltaLines(delta.lines);
      }
      return;
    }

    if (delta.rawChunks.isNotEmpty) {
      for (final encoded in delta.rawChunks) {
        try {
          final bytes = base64.decode(encoded);
          final chunk = _decodeRawChunk(bytes);
          _applyRealtimeChunk(chunk);
        } catch (e, stackTrace) {
          unawaited(
            StructuredLog.critical(
              traceId: _traceId,
              event: '[CRITICAL] terminal.delta.decode_failed',
              anchor: 'ws_delta_decode',
              error: e,
              stackTrace: stackTrace,
              context: {
                'session_id': widget.sessionId,
                'raw_b64_len': encoded.length,
              },
            ),
          );
        }
      }
      return;
    }

    if (delta.lines.isNotEmpty) {
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.delta.line_only',
        anchor: 'ws_line_only_delta',
        context: {
          'session_id': widget.sessionId,
          'line_count': delta.lines.length,
        },
      );
    }
  }

  void _ensureCleanReplayLoop() {
    if (_cleanReplayTimer?.isActive == true) {
      return;
    }
    _cleanReplayTimer = Timer.periodic(_cleanReplayInterval, (_) {
      if (_disposed || _renderMode != MobileRenderMode.clean) {
        return;
      }
      unawaited(_fetchReplay(reason: 'clean_mode_poll'));
    });
  }

  void _applyCleanReplay(ScreenOutput output, {required String reason}) {
    final nextTerminal = _newTerminal();
    final cols = _viewCols > 0 ? _viewCols : _defaultSnapshotCols;
    final rows = _viewRows > 0 ? _viewRows : _defaultSnapshotRows;
    nextTerminal.resize(cols, rows);

    final source =
        output.lines.isNotEmpty ? output.lines : output.content.split('\n');
    final filtered = filterNoiseLines(
      source,
      profile: widget.noiseProfile,
    );
    final dropped = source.length - filtered.length;
    for (final line in filtered) {
      nextTerminal.write(line);
      nextTerminal.write('\r\n');
    }
    if (!mounted || _disposed) {
      return;
    }
    setState(() {
      _terminal = nextTerminal;
    });
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.clean_replay.applied',
      anchor: 'clean_replay_apply',
      context: {
        'session_id': widget.sessionId,
        'reason': reason,
        'cols': cols,
        'rows': rows,
        'revision': output.revision,
        'line_count': filtered.length,
        'noise_drop_count': dropped,
        'render_mode': mobileRenderModeValue(_renderMode),
        'noise_profile': terminalNoiseProfileValue(widget.noiseProfile),
      },
    );
    if (dropped > 0) {
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.clean.filter_stats',
        anchor: 'clean_replay_apply',
        context: {
          'session_id': widget.sessionId,
          'reason': reason,
          'source_count': source.length,
          'filtered_count': filtered.length,
          'noise_drop_count': dropped,
        },
      );
    }
  }

  void _applyCleanDeltaLines(List<String> lines) {
    final filtered = filterNoiseLines(
      lines,
      profile: widget.noiseProfile,
    );
    final dropped = lines.length - filtered.length;
    for (final line in filtered) {
      if (line == _lastCleanDeltaLine) {
        continue;
      }
      _terminal.write(line);
      _terminal.write('\r\n');
      _lastCleanDeltaLine = line;
    }
    if (dropped > 0) {
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.clean.filter_stats',
        anchor: 'ws_clean_delta',
        context: {
          'session_id': widget.sessionId,
          'source_count': lines.length,
          'filtered_count': filtered.length,
          'noise_drop_count': dropped,
        },
      );
    }
  }

  void _resetRawDecoder() {
    _rawUtf8Fragments.clear();
    _rawUtf8Sink = const Utf8Decoder(allowMalformed: false)
        .startChunkedConversion(_StreamingStringSink(_rawUtf8Fragments.add));
  }

  String _decodeRawChunk(List<int> bytes) {
    if (bytes.isEmpty) {
      return '';
    }
    try {
      _rawUtf8Sink.add(bytes);
      if (_rawUtf8Fragments.isEmpty) {
        return '';
      }
      final text = _rawUtf8Fragments.join();
      _rawUtf8Fragments.clear();
      return text;
    } on FormatException {
      _rawUtf8Fragments.clear();
      _rawUtf8Sink.close();
      _resetRawDecoder();
      return latin1.decode(bytes, allowInvalid: true);
    }
  }

  String _sanitizeRawChunk(String chunk) {
    if (chunk.isEmpty && _rawSanitizeCarry.isEmpty) {
      return '';
    }
    final source = '$_rawSanitizeCarry$chunk';
    final output = StringBuffer();
    var cursor = 0;
    _rawSanitizeCarry = '';
    while (true) {
      final start = source.indexOf(_tmuxDcsStart, cursor);
      if (start < 0) {
        output.write(source.substring(cursor));
        break;
      }
      output.write(source.substring(cursor, start));
      final bodyStart = start + _tmuxDcsStart.length;
      final end = source.indexOf('\u001b\\', bodyStart);
      if (end < 0) {
        _rawSanitizeCarry = source.substring(start);
        break;
      }
      final body =
          source.substring(bodyStart, end).replaceAll('\u001b\u001b', '\u001b');
      output.write(body);
      cursor = end + 2;
    }
    if (_rawSanitizeCarry.isEmpty) {
      final tailKeep = _findLongestTmuxStartPrefixSuffix(source);
      if (tailKeep > 0 && source.length >= tailKeep) {
        _rawSanitizeCarry = source.substring(source.length - tailKeep);
        final normalized = output.toString();
        if (normalized.endsWith(_rawSanitizeCarry)) {
          return normalized.substring(0, normalized.length - tailKeep);
        }
      }
    }
    return output.toString();
  }

  void _applyRealtimeChunk(String chunk) {
    if (chunk.isEmpty) {
      return;
    }
    var remaining = _sanitizeRawChunk(chunk);
    while (remaining.isNotEmpty) {
      if (!_syncUpdateActive) {
        final start = remaining.indexOf(_syncUpdateStart);
        if (start < 0) {
          _terminal.write(remaining);
          return;
        }
        if (start > 0) {
          _terminal.write(remaining.substring(0, start));
        }
        _syncUpdateActive = true;
        remaining = remaining.substring(start + _syncUpdateStart.length);
        continue;
      }

      final end = remaining.indexOf(_syncUpdateEnd);
      if (end < 0) {
        _syncUpdateBuffer.write(remaining);
        return;
      }
      if (end > 0) {
        _syncUpdateBuffer.write(remaining.substring(0, end));
      }
      if (_syncUpdateBuffer.length > 0) {
        _terminal.write(_syncUpdateBuffer.toString());
        _syncUpdateBuffer.clear();
      }
      _syncUpdateActive = false;
      remaining = remaining.substring(end + _syncUpdateEnd.length);
    }
  }

  int _findLongestTmuxStartPrefixSuffix(String text) {
    final max = math.min(_tmuxDcsStart.length - 1, text.length);
    for (var n = max; n > 0; n--) {
      if (text.endsWith(_tmuxDcsStart.substring(0, n))) {
        return n;
      }
    }
    return 0;
  }

  void _onTerminalResize(int cols, int rows) {
    if (_disposed) {
      return;
    }
    if (cols <= 0 || rows <= 0) {
      return;
    }
    if (_viewCols == cols && _viewRows == rows) {
      return;
    }
    _viewCols = cols;
    _viewRows = rows;
    _scheduleResizeControl(cols, rows);
  }

  void _scheduleResizeControl(int cols, int rows) {
    if (_disposed || cols <= 0 || rows <= 0) {
      return;
    }
    _pendingResizeCols = cols;
    _pendingResizeRows = rows;
    _resizeControlTimer?.cancel();
    _resizeControlTimer = Timer(
      _resizeControlDebounce,
      () => unawaited(_flushResizeControl()),
    );
  }

  Future<void> _flushResizeControl() async {
    _resizeControlTimer?.cancel();
    _resizeControlTimer = null;
    if (_disposed) {
      return;
    }
    final cols = _pendingResizeCols;
    final rows = _pendingResizeRows;
    if (cols <= 0 || rows <= 0) {
      return;
    }
    if (_sentResizeCols == cols && _sentResizeRows == rows) {
      return;
    }
    _sentResizeCols = cols;
    _sentResizeRows = rows;
    try {
      await ref.read(apiServiceProvider).sendResize(
            sessionId: widget.sessionId,
            cols: cols,
            rows: rows,
          );
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.resize.send_ok',
        anchor: 'terminal_resize',
        context: {
          'session_id': widget.sessionId,
          'cols': cols,
          'rows': rows,
        },
      );
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.resize.send_failed',
          anchor: 'terminal_resize',
          error: e,
          stackTrace: stackTrace,
          context: {
            'session_id': widget.sessionId,
            'cols': cols,
            'rows': rows,
          },
        ),
      );
    }
  }

  OutputWsMessage? _decodeWsMessage(dynamic payload) {
    try {
      if (payload is String) {
        return OutputWsMessage.fromJson(_asMap(jsonDecode(payload)));
      }
      if (payload is List<int>) {
        final decoded = utf8.decode(payload, allowMalformed: true);
        return OutputWsMessage.fromJson(_asMap(jsonDecode(decoded)));
      }
      throw StateError(
          '[STATE_INVALID] unsupported ws payload type: ${payload.runtimeType}');
    } catch (e, stackTrace) {
      _updateUiError('WS 消息解析失败');
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.ws.parse_failed',
          anchor: 'ws_parse',
          error: e,
          stackTrace: stackTrace,
          context: {
            'session_id': widget.sessionId,
          },
        ),
      );
      return null;
    }
  }

  void _onTerminalOutput(String data) {
    if (_disposed || data.isEmpty) {
      return;
    }
    final sanitized = _sanitizeTerminalOutput(data);
    if (sanitized.isEmpty) {
      return;
    }
    _pendingInput.write(sanitized);
    _inputFlushTimer ??= Timer(
      const Duration(milliseconds: 25),
      _flushPendingInput,
    );
  }

  String _sanitizeTerminalOutput(String data) {
    return sanitizeTerminalInputProbe(data);
  }

  Future<void> _flushPendingInput() async {
    _inputFlushTimer?.cancel();
    _inputFlushTimer = null;
    if (_sendingInput || _disposed) {
      return;
    }
    final payload = _pendingInput.toString();
    if (payload.isEmpty) {
      return;
    }
    _pendingInput.clear();
    _sendingInput = true;
    try {
      await ref.read(apiServiceProvider).sendControl(
            sessionId: widget.sessionId,
            data: payload,
            submit: false,
          );
    } catch (e, stackTrace) {
      _pendingInput.write(payload);
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.input.send_failed',
          anchor: 'control_send',
          error: e,
          stackTrace: stackTrace,
          context: {
            'session_id': widget.sessionId,
            'payload_len': payload.length,
          },
        ),
      );
    } finally {
      _sendingInput = false;
      if (_pendingInput.isNotEmpty && !_disposed) {
        _inputFlushTimer ??= Timer(
          const Duration(milliseconds: 25),
          _flushPendingInput,
        );
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _rawUtf8Sink.close();
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _resizeControlTimer?.cancel();
    _resizeControlTimer = null;
    _cleanReplayTimer?.cancel();
    _cleanReplayTimer = null;
    _inputFlushTimer?.cancel();
    _inputFlushTimer = null;
    _wsSubscription?.cancel();
    _wsSubscription = null;
    _wsChannel?.sink.close();
    _wsChannel = null;
    _notifyWsState(false);
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.dispose',
      anchor: 'terminal_widget_dispose',
      context: {
        'session_id': widget.sessionId,
      },
    );
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final fontSize = widget.fontSize.clamp(9, 22).toDouble();
    return Container(
      color: _xterm256Theme.background,
      child: TerminalView(
        _terminal,
        autoResize: true,
        theme: _xterm256Theme,
        textStyle: TerminalStyle(
          fontFamily: 'monospace',
          fontSize: fontSize,
          height: 1.14,
        ),
        padding: EdgeInsets.zero,
      ),
    );
  }

  void _notifyWsState(bool live) {
    widget.onWsStateChanged?.call(live);
  }

  void _updateUiError(String message) {
    if (widget.onErrorChanged == null) {
      return;
    }
    if (_lastUiError == message) {
      return;
    }
    _lastUiError = message;
    widget.onErrorChanged!.call(message);
  }
}

Map<String, dynamic> _asMap(Object? value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return value.map(
      (key, dynamic item) => MapEntry(key.toString(), item),
    );
  }
  throw FormatException(
      '[STATE_INVALID] expected map, got ${value.runtimeType}');
}

class _StreamingStringSink extends StringConversionSinkBase {
  final void Function(String value) onChunk;

  _StreamingStringSink(this.onChunk);

  @override
  void addSlice(String str, int start, int end, bool isLast) {
    if (start == end) {
      return;
    }
    onChunk(str.substring(start, end));
  }

  @override
  void close() {}
}
