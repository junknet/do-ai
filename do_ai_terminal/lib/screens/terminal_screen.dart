import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../core/terminal_sanitize.dart';
import '../core/structured_log.dart';
import '../services/api_service.dart';
import '../widgets/terminal_widget.dart';

/// 终端页面（对齐 RN 版本关键交互：Tabs + 工具栏 + 字号 + 沉浸模式）
class TerminalScreen extends ConsumerStatefulWidget {
  final String sessionId;
  final String title;

  const TerminalScreen({
    super.key,
    required this.sessionId,
    required this.title,
  });

  @override
  ConsumerState<TerminalScreen> createState() => _TerminalScreenState();
}

class _TerminalScreenState extends ConsumerState<TerminalScreen> {
  static const double _fontSizeMin = 10;
  static const double _fontSizeMax = 22;
  static const double _fontSizeDefault = 12;
  static const String _fontSizeStorageKey = 'doai:mobile:terminal-font-size:v1';
  static const String _renderModeStorageKey =
      'doai:mobile:terminal-render-mode:v1';
  static const String _defaultRenderModeRaw = String.fromEnvironment(
    'DO_AI_MOBILE_RENDER_MODE',
    defaultValue: 'clean',
  );
  static const String _defaultNoiseProfileRaw = String.fromEnvironment(
    'DO_AI_MOBILE_NOISE_PROFILE',
    defaultValue: 'gemini',
  );
  static const Duration _sessionRefreshInterval = Duration(seconds: 3);

  final String _traceId = StructuredLog.newTraceId('terminal_screen');
  final TextEditingController _commandController = TextEditingController();
  final FocusNode _commandFocusNode = FocusNode();

  Timer? _sessionRefreshTimer;
  List<SessionInfo> _sessions = const [];
  String _activeSessionId = '';
  int _terminalEpoch = 0;
  double _fontSize = _fontSizeDefault;
  late MobileRenderMode _renderMode;
  late final TerminalNoiseProfile _noiseProfile;
  bool _immersive = false;
  bool _loadingSessions = false;
  bool _sendingInput = false;
  bool _wsLive = false;
  String _sessionError = '';
  String _terminalError = '';

  @override
  void initState() {
    super.initState();
    _activeSessionId = widget.sessionId;
    _renderMode = parseMobileRenderMode(
      _defaultRenderModeRaw,
      fallback: MobileRenderMode.clean,
    );
    _noiseProfile = parseTerminalNoiseProfile(
      _defaultNoiseProfileRaw,
      fallback: TerminalNoiseProfile.gemini,
    );
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.render.mode_set',
      anchor: 'render_mode_switch',
      context: {
        'session_id': _activeSessionId,
        'reason': 'init',
        'render_mode': mobileRenderModeValue(_renderMode),
        'noise_profile': terminalNoiseProfileValue(_noiseProfile),
      },
    );
    _loadFontSize();
    _loadRenderMode();
    unawaited(_refreshSessions(silent: false));
    _sessionRefreshTimer = Timer.periodic(
      _sessionRefreshInterval,
      (_) => unawaited(_refreshSessions(silent: true)),
    );
  }

  Future<void> _loadFontSize() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getDouble(_fontSizeStorageKey);
      if (!mounted || raw == null) {
        return;
      }
      setState(() {
        _fontSize = raw.clamp(_fontSizeMin, _fontSizeMax).toDouble();
      });
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[STATE_INVALID] terminal.font.restore_failed',
          anchor: 'font_size_restore',
          error: e,
          stackTrace: stackTrace,
        ),
      );
    }
  }

  Future<void> _persistFontSize() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setDouble(_fontSizeStorageKey, _fontSize);
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[STATE_INVALID] terminal.font.persist_failed',
          anchor: 'font_size_persist',
          error: e,
          stackTrace: stackTrace,
          context: {
            'font_size': _fontSize,
          },
        ),
      );
    }
  }

  Future<void> _loadRenderMode() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_renderModeStorageKey);
      if (!mounted || raw == null || raw.trim().isEmpty) {
        return;
      }
      final next = parseMobileRenderMode(
        raw,
        fallback: _renderMode,
      );
      if (next == _renderMode) {
        return;
      }
      setState(() {
        _renderMode = next;
        _terminalEpoch += 1;
      });
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.render.mode_set',
        anchor: 'render_mode_switch',
        context: {
          'session_id': _activeSessionId,
          'reason': 'restore',
          'render_mode': mobileRenderModeValue(_renderMode),
          'noise_profile': terminalNoiseProfileValue(_noiseProfile),
        },
      );
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[STATE_INVALID] terminal.render_mode.restore_failed',
          anchor: 'render_mode_restore',
          error: e,
          stackTrace: stackTrace,
        ),
      );
    }
  }

  Future<void> _persistRenderMode() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(
        _renderModeStorageKey,
        mobileRenderModeValue(_renderMode),
      );
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[STATE_INVALID] terminal.render_mode.persist_failed',
          anchor: 'render_mode_persist',
          error: e,
          stackTrace: stackTrace,
          context: {
            'session_id': _activeSessionId,
            'render_mode': mobileRenderModeValue(_renderMode),
          },
        ),
      );
    }
  }

  Future<void> _refreshSessions({required bool silent}) async {
    if (!silent && mounted) {
      setState(() {
        _loadingSessions = true;
      });
    }
    try {
      final sessions = await ref.read(apiServiceProvider).getSessions();
      if (!mounted) {
        return;
      }
      final online = sessions
          .where((item) => item.status.toLowerCase() != 'stopped')
          .toList(growable: false);
      final normalized = online.isNotEmpty ? online : sessions;
      final hasActive =
          normalized.any((session) => session.id == _activeSessionId);
      setState(() {
        _sessions = normalized;
        _loadingSessions = false;
        _sessionError = '';
        if (!hasActive && normalized.isNotEmpty) {
          _activeSessionId = normalized.first.id;
          _terminalEpoch += 1;
        }
      });
    } catch (e, stackTrace) {
      if (!mounted) {
        return;
      }
      setState(() {
        _loadingSessions = false;
        _sessionError = '会话列表刷新失败，请检查 relay 状态';
      });
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.sessions.refresh_failed',
          anchor: 'sessions_refresh',
          error: e,
          stackTrace: stackTrace,
        ),
      );
    }
  }

  void _switchSession(String sessionId) {
    if (sessionId.isEmpty || sessionId == _activeSessionId) {
      return;
    }
    setState(() {
      _activeSessionId = sessionId;
      _terminalError = '';
      _terminalEpoch += 1;
    });
  }

  String _offlineInputMessage() {
    final relayBase = ref.read(apiServiceProvider).baseUrl;
    return '连接未就绪，请先重连 Relay($relayBase)';
  }

  void _markOfflineInputBlocked({
    required String action,
    required int payloadLen,
    required bool submit,
  }) {
    final message = _offlineInputMessage();
    if (mounted) {
      setState(() {
        _terminalError = message;
      });
    }
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.control.blocked_offline',
      anchor: 'control_send_guard',
      status: 'blocked',
      context: {
        'session_id': _activeSessionId,
        'submit': submit,
        'ui_action': action,
        'payload_len': payloadLen,
      },
    );
  }

  Future<void> _sendControl({
    required String input,
    bool submit = false,
    String action = '',
  }) async {
    final relayAction = _normalizeRelayAction(action);
    final hasEffectivePayload =
        input.isNotEmpty || submit || relayAction.isNotEmpty;
    if (_activeSessionId.isEmpty || _sendingInput || !hasEffectivePayload) {
      return;
    }
    if (!_wsLive) {
      _markOfflineInputBlocked(
        action: action,
        payloadLen: input.length,
        submit: submit,
      );
      return;
    }
    setState(() {
      _sendingInput = true;
    });
    final stopwatch = Stopwatch()..start();
    try {
      await ref.read(apiServiceProvider).sendControl(
            sessionId: _activeSessionId,
            data: input,
            submit: submit,
            source: 'flutter-terminal-toolbar',
            action: relayAction,
          );
      if (mounted && _terminalError.isNotEmpty) {
        setState(() {
          _terminalError = '';
        });
      }
      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.control.send_ok',
        anchor: 'control_send',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'session_id': _activeSessionId,
          'submit': submit,
          'ui_action': action,
          'relay_action': relayAction,
          'payload_len': input.length,
        },
      );
    } catch (e, stackTrace) {
      final relayBase = ref.read(apiServiceProvider).baseUrl;
      if (mounted) {
        setState(() {
          _terminalError = _wsLive
              ? '输入发送失败，请检查 Relay($relayBase) 与会话状态'
              : '连接已断开，请检查 Relay($relayBase) 并重试';
        });
      }
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.control.send_failed',
          anchor: 'control_send',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
          context: {
            'session_id': _activeSessionId,
            'submit': submit,
            'ui_action': action,
            'relay_action': relayAction,
            'payload_len': input.length,
          },
        ),
      );
    } finally {
      if (mounted) {
        setState(() {
          _sendingInput = false;
        });
      }
    }
  }

  String _normalizeRelayAction(String action) {
    final normalized = action.trim().toLowerCase();
    switch (normalized) {
      case 'terminate':
      case 'resize':
        return normalized;
      default:
        return '';
    }
  }

  Future<void> _submitCommand() async {
    final raw = _commandController.text;
    final text = raw.trimRight();
    if (text.isEmpty) {
      return;
    }
    if (!_wsLive) {
      _markOfflineInputBlocked(
        action: 'submit',
        payloadLen: text.length,
        submit: true,
      );
      return;
    }
    _commandController.clear();
    await _sendControl(
      input: text,
      submit: true,
    );
  }

  Future<void> _reconnectTerminal() async {
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.reconnect.requested',
      anchor: 'reconnect_button',
      status: 'pending',
      context: {
        'session_id': _activeSessionId,
      },
    );
    if (mounted) {
      setState(() {
        _terminalError = '';
        _sessionError = '';
        _terminalEpoch += 1;
      });
    }
    await _refreshSessions(silent: false);
  }

  Future<void> _terminateSessionFromTab(SessionInfo session) async {
    final confirmed = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('关闭会话'),
            content: Text('确认关闭会话 ${session.name} 吗？'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('关闭'),
              ),
            ],
          ),
        ) ??
        false;
    if (!confirmed || !mounted) {
      return;
    }
    try {
      await ref.read(apiServiceProvider).terminateSession(session.id);
      if (!mounted) {
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已发送关闭指令: ${session.name}')),
      );
      await _refreshSessions(silent: false);
    } catch (e) {
      if (!mounted) {
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('关闭失败: $e')),
      );
    }
  }

  void _changeFont(double delta) {
    final next = (_fontSize + delta).clamp(_fontSizeMin, _fontSizeMax);
    if (next == _fontSize) {
      return;
    }
    setState(() {
      _fontSize = next;
      _terminalEpoch += 1;
    });
    unawaited(_persistFontSize());
  }

  void _setRenderMode(MobileRenderMode mode) {
    if (mode == _renderMode) {
      return;
    }
    final previous = _renderMode;
    setState(() {
      _renderMode = mode;
      _terminalEpoch += 1;
    });
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.render.mode_set',
      anchor: 'render_mode_switch',
      context: {
        'session_id': _activeSessionId,
        'reason': 'user',
        'prev_mode': mobileRenderModeValue(previous),
        'render_mode': mobileRenderModeValue(mode),
        'noise_profile': terminalNoiseProfileValue(_noiseProfile),
      },
    );
    unawaited(_persistRenderMode());
  }

  String _activeTitle() {
    for (final session in _sessions) {
      if (session.id == _activeSessionId) {
        return session.name;
      }
    }
    if (widget.title.trim().isNotEmpty) {
      return widget.title.trim();
    }
    return _activeSessionId;
  }

  bool _isSecureWs(DoAiApiService apiService) {
    if (apiService.forceWss) {
      return true;
    }
    final uri = Uri.tryParse(apiService.baseUrl);
    return uri?.scheme == 'https';
  }

  @override
  void dispose() {
    _sessionRefreshTimer?.cancel();
    _sessionRefreshTimer = null;
    _commandController.dispose();
    _commandFocusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final apiService = ref.read(apiServiceProvider);
    final secureWs = _isSecureWs(apiService);
    final sessions = _sessions.isNotEmpty
        ? _sessions
        : <SessionInfo>[
            SessionInfo(
              id: widget.sessionId,
              name: widget.title,
              status: 'running',
              pid: 0,
              createdAt: null,
            ),
          ];
    final activeSessionId =
        _activeSessionId.isNotEmpty ? _activeSessionId : widget.sessionId;
    final bannerText =
        _terminalError.isNotEmpty ? _terminalError : _sessionError;

    return Scaffold(
      backgroundColor: const Color(0xFF05091F),
      body: SafeArea(
        child: Column(
          children: [
            _TerminalHeader(
              title: _activeTitle(),
              sessions: sessions,
              activeSessionId: activeSessionId,
              onBack: () => Navigator.pop(context),
              onSwitchSession: _switchSession,
              onLongPressSession: (session) {
                unawaited(_terminateSessionFromTab(session));
              },
              onDecreaseFont: () => _changeFont(-1),
              onIncreaseFont: () => _changeFont(1),
              onSwitchRenderMode: _setRenderMode,
              onReconnect: () {
                unawaited(_reconnectTerminal());
              },
              onToggleImmersive: () {
                setState(() {
                  _immersive = !_immersive;
                });
              },
              immersive: _immersive,
              wsLive: _wsLive,
              secureWs: secureWs,
              renderMode: _renderMode,
            ),
            if (bannerText.isNotEmpty) _ErrorBanner(text: bannerText),
            Expanded(
              child: Stack(
                children: [
                  Positioned.fill(
                    child: TerminalWidget(
                      key: ValueKey(
                          'terminal-$activeSessionId-$_terminalEpoch-${_fontSize.toStringAsFixed(2)}'),
                      sessionId: activeSessionId,
                      fontSize: _fontSize,
                      renderMode: _renderMode,
                      noiseProfile: _noiseProfile,
                      onWsStateChanged: (live) {
                        if (!mounted || _wsLive == live) {
                          return;
                        }
                        setState(() {
                          _wsLive = live;
                          if (live && _terminalError.startsWith('连接未就绪，请先重连')) {
                            _terminalError = '';
                          }
                        });
                      },
                      onErrorChanged: (message) {
                        if (!mounted || _terminalError == message) {
                          return;
                        }
                        setState(() {
                          _terminalError = message;
                        });
                      },
                    ),
                  ),
                  if (_loadingSessions)
                    const Positioned(
                      top: 0,
                      left: 0,
                      right: 0,
                      child: LinearProgressIndicator(minHeight: 2),
                    ),
                ],
              ),
            ),
            if (!_immersive)
              _CommandComposer(
                controller: _commandController,
                focusNode: _commandFocusNode,
                enabled: _wsLive,
                sending: _sendingInput,
                onSubmit: _submitCommand,
              ),
            if (!_immersive)
              _QuickKeyBar(
                enabled: _wsLive,
                sending: _sendingInput,
                onTapQuickKey: (spec) {
                  if (spec.focusComposer) {
                    _commandFocusNode.requestFocus();
                    return;
                  }
                  if (spec.forceSyncTerminal) {
                    setState(() {
                      _terminalEpoch += 1;
                    });
                    return;
                  }
                  unawaited(
                    _sendControl(
                      input: spec.payload,
                      submit: false,
                      action: spec.action,
                    ),
                  );
                },
              ),
          ],
        ),
      ),
    );
  }
}

class _TerminalHeader extends StatelessWidget {
  final String title;
  final List<SessionInfo> sessions;
  final String activeSessionId;
  final VoidCallback onBack;
  final ValueChanged<String> onSwitchSession;
  final ValueChanged<SessionInfo> onLongPressSession;
  final VoidCallback onDecreaseFont;
  final VoidCallback onIncreaseFont;
  final ValueChanged<MobileRenderMode> onSwitchRenderMode;
  final VoidCallback onReconnect;
  final VoidCallback onToggleImmersive;
  final bool immersive;
  final bool wsLive;
  final bool secureWs;
  final MobileRenderMode renderMode;

  const _TerminalHeader({
    required this.title,
    required this.sessions,
    required this.activeSessionId,
    required this.onBack,
    required this.onSwitchSession,
    required this.onLongPressSession,
    required this.onDecreaseFont,
    required this.onIncreaseFont,
    required this.onSwitchRenderMode,
    required this.onReconnect,
    required this.onToggleImmersive,
    required this.immersive,
    required this.wsLive,
    required this.secureWs,
    required this.renderMode,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      color: const Color(0xFF070D23),
      padding: const EdgeInsets.fromLTRB(6, 6, 6, 8),
      child: Column(
        children: [
          Row(
            children: [
              _HeaderIconButton(
                icon: Icons.arrow_back_ios_new,
                onPressed: onBack,
              ),
              Expanded(
                child: Text(
                  title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: Color(0xFFEAF2FC),
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              _HeaderActionButton(
                label: 'A-',
                onTap: onDecreaseFont,
              ),
              const SizedBox(width: 4),
              _HeaderActionButton(
                label: 'A+',
                onTap: onIncreaseFont,
              ),
              const SizedBox(width: 4),
              _RenderModeButton(
                mode: renderMode,
                onSelected: onSwitchRenderMode,
              ),
              const SizedBox(width: 4),
              _HeaderActionButton(
                label: '重连',
                onTap: onReconnect,
              ),
              const SizedBox(width: 4),
              _HeaderIconButton(
                icon: immersive ? Icons.fullscreen_exit : Icons.fullscreen,
                onPressed: onToggleImmersive,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              _TransportChip(
                wsLive: wsLive,
                secureWs: secureWs,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: Row(
                    children: sessions.map((session) {
                      final active = session.id == activeSessionId;
                      return Padding(
                        padding: const EdgeInsets.only(right: 6),
                        child: Semantics(
                          label: 'session-tab-${session.name}',
                          button: true,
                          selected: active,
                          child: InkWell(
                            borderRadius: BorderRadius.circular(10),
                            onTap: () => onSwitchSession(session.id),
                            onLongPress: () => onLongPressSession(session),
                            child: Container(
                              height: 28,
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 12),
                              decoration: BoxDecoration(
                                color: active
                                    ? const Color(0xFF153D34)
                                    : const Color(0xFF131B34),
                                borderRadius: BorderRadius.circular(10),
                                border: Border.all(
                                  color: active
                                      ? const Color(0xFF22B87F)
                                      : const Color(0xFF202B50),
                                ),
                              ),
                              child: Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  Container(
                                    width: 6,
                                    height: 6,
                                    margin: const EdgeInsets.only(right: 7),
                                    decoration: BoxDecoration(
                                      color: active
                                          ? const Color(0xFF31D684)
                                          : const Color(0xFF5F6D8B),
                                      shape: BoxShape.circle,
                                    ),
                                  ),
                                  Text(
                                    session.name,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(
                                      color: active
                                          ? const Color(0xFF5CF2B0)
                                          : const Color(0xFF96A4C8),
                                      fontSize: 12,
                                      fontWeight: FontWeight.w700,
                                    ),
                                  ),
                                  if (active) ...[
                                    const SizedBox(width: 6),
                                    GestureDetector(
                                      behavior: HitTestBehavior.opaque,
                                      onTap: () => onLongPressSession(session),
                                      child: const Icon(
                                        Icons.close_rounded,
                                        size: 14,
                                        color: Color(0xFF7BECC0),
                                      ),
                                    ),
                                  ],
                                ],
                              ),
                            ),
                          ),
                        ),
                      );
                    }).toList(growable: false),
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _HeaderIconButton extends StatelessWidget {
  final IconData icon;
  final VoidCallback onPressed;

  const _HeaderIconButton({
    required this.icon,
    required this.onPressed,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 30,
      height: 30,
      child: Material(
        color: const Color(0xFF141C34),
        borderRadius: BorderRadius.circular(9),
        child: InkWell(
          borderRadius: BorderRadius.circular(9),
          onTap: onPressed,
          child: Icon(
            icon,
            size: 16,
            color: const Color(0xFF9FAACC),
          ),
        ),
      ),
    );
  }
}

class _HeaderActionButton extends StatelessWidget {
  final String label;
  final VoidCallback onTap;

  const _HeaderActionButton({
    required this.label,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 30,
      child: Material(
        color: const Color(0xFF141C34),
        borderRadius: BorderRadius.circular(9),
        child: InkWell(
          borderRadius: BorderRadius.circular(9),
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 9),
            child: Center(
              child: Text(
                label,
                style: const TextStyle(
                  color: Color(0xFF8F9AB9),
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _RenderModeButton extends StatelessWidget {
  final MobileRenderMode mode;
  final ValueChanged<MobileRenderMode> onSelected;

  const _RenderModeButton({
    required this.mode,
    required this.onSelected,
  });

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<MobileRenderMode>(
      tooltip: '终端渲染模式',
      initialValue: mode,
      onSelected: onSelected,
      itemBuilder: (context) {
        return MobileRenderMode.values
            .map(
              (item) => PopupMenuItem<MobileRenderMode>(
                value: item,
                child: Text(mobileRenderModeLabel(item)),
              ),
            )
            .toList(growable: false);
      },
      child: Container(
        height: 30,
        padding: const EdgeInsets.symmetric(horizontal: 9),
        decoration: BoxDecoration(
          color: const Color(0xFF141C34),
          borderRadius: BorderRadius.circular(9),
        ),
        child: Center(
          child: Text(
            mobileRenderModeLabel(mode),
            style: const TextStyle(
              color: Color(0xFF8F9AB9),
              fontSize: 11,
              fontWeight: FontWeight.w700,
            ),
          ),
        ),
      ),
    );
  }
}

class _TransportChip extends StatelessWidget {
  final bool wsLive;
  final bool secureWs;

  const _TransportChip({
    required this.wsLive,
    required this.secureWs,
  });

  @override
  Widget build(BuildContext context) {
    final text = secureWs ? 'WSS' : 'WS';
    final bgColor =
        secureWs ? const Color(0x1422B87F) : const Color(0x33D38A00);
    final borderColor =
        secureWs ? const Color(0xFF22B87F) : const Color(0xFFD38A00);
    final dotColor = wsLive ? const Color(0xFF31D684) : const Color(0xFFFF4D6A);

    return Container(
      height: 28,
      padding: const EdgeInsets.symmetric(horizontal: 8),
      decoration: BoxDecoration(
        color: bgColor,
        borderRadius: BorderRadius.circular(9),
        border: Border.all(color: borderColor),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 6,
            height: 6,
            margin: const EdgeInsets.only(right: 6),
            decoration: BoxDecoration(
              color: dotColor,
              shape: BoxShape.circle,
            ),
          ),
          Text(
            text,
            style: TextStyle(
              color:
                  secureWs ? const Color(0xFF5CF2B0) : const Color(0xFFFFC266),
              fontSize: 11,
              fontWeight: FontWeight.w700,
            ),
          ),
        ],
      ),
    );
  }
}

class _CommandComposer extends StatelessWidget {
  final TextEditingController controller;
  final FocusNode focusNode;
  final bool enabled;
  final bool sending;
  final Future<void> Function() onSubmit;

  const _CommandComposer({
    required this.controller,
    required this.focusNode,
    required this.enabled,
    required this.sending,
    required this.onSubmit,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      color: const Color(0xFFE8ECF2),
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 6),
      child: Row(
        children: [
          const Text(
            r'$',
            style: TextStyle(
              color: Color(0xFF2F3C5D),
              fontSize: 15,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: TextField(
              controller: controller,
              focusNode: focusNode,
              style: const TextStyle(
                color: Color(0xFF1D2740),
                fontSize: 14,
              ),
              onSubmitted: (_) {
                if (!enabled || sending) {
                  return;
                }
                unawaited(onSubmit());
              },
              decoration: const InputDecoration(
                isDense: true,
                hintText: '输入命令后回车发送',
                border: InputBorder.none,
              ),
            ),
          ),
          SizedBox(
            width: 38,
            height: 38,
            child: Material(
              color:
                  enabled ? const Color(0xFF2F5DFF) : const Color(0xFF95A2BF),
              borderRadius: BorderRadius.circular(19),
              child: InkWell(
                borderRadius: BorderRadius.circular(19),
                onTap:
                    (!enabled || sending) ? null : () => unawaited(onSubmit()),
                child: Center(
                  child: sending
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : const Icon(
                          Icons.send_rounded,
                          size: 18,
                          color: Colors.white,
                        ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _QuickKeyBar extends StatelessWidget {
  final bool enabled;
  final bool sending;
  final ValueChanged<_QuickKeySpec> onTapQuickKey;

  const _QuickKeyBar({
    required this.enabled,
    required this.sending,
    required this.onTapQuickKey,
  });

  static const List<_QuickKeySpec> _keys = [
    _QuickKeySpec(label: 'ESC', payload: '\u001b', action: 'quick_esc'),
    _QuickKeySpec(label: 'TAB', payload: '\t', action: 'quick_tab'),
    _QuickKeySpec(label: 'Ctrl+C', payload: '\u0003', action: 'quick_ctrlc'),
    _QuickKeySpec(label: '↑', payload: '\u001b[A', action: 'quick_up'),
    _QuickKeySpec(label: '↓', payload: '\u001b[B', action: 'quick_down'),
    _QuickKeySpec(label: '←', payload: '\u001b[D', action: 'quick_left'),
    _QuickKeySpec(label: '→', payload: '\u001b[C', action: 'quick_right'),
    _QuickKeySpec(
        label: 'Shift+Tab', payload: '\u001b[Z', action: 'quick_shifttab'),
    _QuickKeySpec(
        label: 'Sync',
        payload: '',
        action: 'force_sync',
        forceSyncTerminal: true),
    _QuickKeySpec(
        label: '⌨', payload: '', action: 'focus_composer', focusComposer: true),
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 46,
      decoration: const BoxDecoration(
        color: Color(0xFFF4F6F9),
        border: Border(
          top: BorderSide(color: Color(0xFFD7DDE7)),
        ),
      ),
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
        itemBuilder: (context, index) {
          final spec = _keys[index];
          final interactive = !sending &&
              (enabled || spec.forceSyncTerminal || spec.focusComposer);
          final fillColor =
              interactive ? Colors.white : const Color(0xFFE9EDF4);
          final borderColor =
              interactive ? const Color(0xFFD8DFEA) : const Color(0xFFDCE3EE);
          final textColor =
              interactive ? const Color(0xFF2F3A55) : const Color(0xFF98A2BA);
          return SizedBox(
            height: 34,
            child: Semantics(
              label: 'quick-key-${spec.action}',
              button: true,
              child: Material(
                color: fillColor,
                borderRadius: BorderRadius.circular(7),
                child: InkWell(
                  borderRadius: BorderRadius.circular(7),
                  onTap: interactive ? () => onTapQuickKey(spec) : null,
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10),
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(7),
                      border: Border.all(color: borderColor),
                    ),
                    child: Center(
                      child: Text(
                        spec.label,
                        style: TextStyle(
                          color: textColor,
                          fontSize: 11,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          );
        },
        separatorBuilder: (_, __) => const SizedBox(width: 6),
        itemCount: _keys.length,
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  final String text;

  const _ErrorBanner({required this.text});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(8, 6, 8, 6),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: const Color(0x29FF4D6A),
        borderRadius: BorderRadius.circular(7),
        border: Border.all(color: const Color(0x66FF4D6A)),
      ),
      child: Text(
        text,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(
          color: Color(0xFFFFB5C2),
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _QuickKeySpec {
  final String label;
  final String payload;
  final String action;
  final bool focusComposer;
  final bool forceSyncTerminal;

  const _QuickKeySpec({
    required this.label,
    required this.payload,
    required this.action,
    this.focusComposer = false,
    this.forceSyncTerminal = false,
  });
}
