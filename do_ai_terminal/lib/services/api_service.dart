import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:web_socket_channel/io.dart';

import '../core/structured_log.dart';

part 'api_service.g.dart';

const String kDefaultRelayUrl = String.fromEnvironment(
  'DO_AI_RELAY_URL',
  defaultValue: 'https://relay.junknets.com',
);
const String kDefaultRelayToken = String.fromEnvironment(
  'DO_AI_RELAY_TOKEN',
  defaultValue:
      'doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff',
);
const bool kDefaultForceWss = bool.fromEnvironment(
  'DO_AI_FORCE_WSS',
  defaultValue: false,
);

class ApiRuntimeConfigStore {
  static const String _baseUrlKey = 'doai:relay:base-url:v1';
  static const String _relayTokenKey = 'doai:relay:token:v1';
  static const String _forceWssKey = 'doai:relay:force-wss:v1';

  static bool _loaded = false;
  static String _baseUrl = kDefaultRelayUrl;
  static String _relayToken = kDefaultRelayToken;
  static bool _forceWss = kDefaultForceWss;

  static String get baseUrl => _baseUrl;
  static String get relayToken => _relayToken;
  static bool get forceWss => _forceWss;

  static Future<void> ensureLoaded() async {
    if (_loaded) {
      return;
    }
    final prefs = await SharedPreferences.getInstance();
    _baseUrl = _normalizeBaseUrl(
      prefs.getString(_baseUrlKey),
      fallback: kDefaultRelayUrl,
    );
    _relayToken =
        (prefs.getString(_relayTokenKey) ?? kDefaultRelayToken).trim();
    if (_relayToken.isEmpty) {
      _relayToken = kDefaultRelayToken;
    }
    _forceWss = prefs.getBool(_forceWssKey) ?? kDefaultForceWss;
    _loaded = true;
  }

  static Future<void> save({
    required String baseUrl,
    required String relayToken,
    required bool forceWss,
  }) async {
    final prefs = await SharedPreferences.getInstance();
    _baseUrl = _normalizeBaseUrl(baseUrl, fallback: kDefaultRelayUrl);
    _relayToken = relayToken.trim();
    _forceWss = forceWss;
    await prefs.setString(_baseUrlKey, _baseUrl);
    await prefs.setString(_relayTokenKey, _relayToken);
    await prefs.setBool(_forceWssKey, _forceWss);
    _loaded = true;
  }

  static Future<void> resetToDefaults() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_baseUrlKey);
    await prefs.remove(_relayTokenKey);
    await prefs.remove(_forceWssKey);
    _baseUrl = kDefaultRelayUrl;
    _relayToken = kDefaultRelayToken;
    _forceWss = kDefaultForceWss;
    _loaded = true;
  }

  static String _normalizeBaseUrl(
    String? raw, {
    required String fallback,
  }) {
    final text = (raw ?? '').trim();
    if (text.isEmpty) {
      return fallback;
    }
    final uri = Uri.tryParse(text);
    if (uri == null || !uri.hasScheme || uri.host.trim().isEmpty) {
      return fallback;
    }
    final scheme = uri.scheme.toLowerCase();
    if (scheme != 'http' && scheme != 'https') {
      return fallback;
    }
    return uri.toString().replaceAll(RegExp(r'/$'), '');
  }
}

/// do-ai relay API 客户端
class DoAiApiService {
  final Dio _dio;
  final String baseUrl;
  final String relayToken;
  final bool forceWss;

  DoAiApiService({
    required this.baseUrl,
    this.relayToken = '',
    this.forceWss = false,
    Dio? dio,
  }) : _dio = dio ?? Dio() {
    _dio.options.baseUrl = baseUrl;
    _dio.options.connectTimeout = const Duration(seconds: 10);
    _dio.options.receiveTimeout = const Duration(seconds: 30);
    _dio.options.headers['User-Agent'] =
        'Mozilla/5.0 (Linux; Android 10) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.120 Mobile Safari/537.36';
    if (relayToken.trim().isNotEmpty) {
      _dio.options.headers['X-Relay-Token'] = relayToken.trim();
      _dio.options.headers['Authorization'] = 'Bearer ${relayToken.trim()}';
    }
  }

  /// GET /api/v1/sessions
  Future<List<SessionInfo>> getSessions() async {
    final traceId = StructuredLog.newTraceId('api_sessions');
    final stopwatch = Stopwatch()..start();
    StructuredLog.info(
      traceId: traceId,
      event: 'api.sessions.request',
      anchor: 'http_get_sessions',
      context: {
        'url': '${_dio.options.baseUrl}/api/v1/sessions',
      },
    );
    try {
      final response = await _dio.get('/api/v1/sessions');
      final payload = _asMap(response.data);
      final rawSessions = payload['sessions'];
      if (rawSessions is! List) {
        throw const FormatException('[STATE_INVALID] sessions is not a list');
      }
      final sessions = rawSessions
          .map((item) => SessionInfo.fromJson(_asMap(item)))
          .toList(growable: false);
      StructuredLog.info(
        traceId: traceId,
        event: 'api.sessions.success',
        anchor: 'http_get_sessions',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'status_code': response.statusCode,
          'count': sessions.length,
        },
      );
      return sessions;
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: traceId,
          event: '[CRITICAL] api.sessions.failed',
          anchor: 'http_get_sessions',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
        ),
      );
      rethrow;
    }
  }

  /// GET /api/v1/output/screen?session_id=...
  Future<ScreenOutput> getScreenOutput(
    String sessionId, {
    required String traceId,
    int? cols,
  }) async {
    final stopwatch = Stopwatch()..start();
    StructuredLog.info(
      traceId: traceId,
      event: 'api.screen.request',
      anchor: 'http_get_screen',
      context: {
        'session_id': sessionId,
      },
    );

    try {
      final response = await _dio.get(
        '/api/v1/output/screen',
        queryParameters: {
          'session_id': sessionId,
          'limit': 260,
          if (cols != null && cols > 0) 'cols': cols,
        },
      );
      final output = ScreenOutput.fromJson(_asMap(response.data));

      StructuredLog.info(
        traceId: traceId,
        event: 'api.screen.success',
        anchor: 'http_get_screen',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'session_id': sessionId,
          'status_code': response.statusCode,
          'cols': output.cols,
          'rows': output.rows,
          'cursor_row': output.cursorRow,
          'cursor_col': output.cursorCol,
          'content_len': output.content.length,
          'revision': output.revision,
        },
      );

      return output;
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: traceId,
          event: '[CRITICAL] api.screen.failed',
          anchor: 'http_get_screen',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
          context: {
            'session_id': sessionId,
          },
        ),
      );
      rethrow;
    }
  }

  /// WS /api/v1/output/ws?session_id=...
  IOWebSocketChannel connectOutputWs({
    required String sessionId,
    required String traceId,
  }) {
    final uri = _buildWsUri(
      path: '/api/v1/output/ws',
      queryParameters: {
        'session_id': sessionId,
        if (relayToken.trim().isNotEmpty) 'token': relayToken.trim(),
      },
    );
    final headers = relayToken.trim().isEmpty
        ? null
        : <String, dynamic>{
            'X-Relay-Token': relayToken.trim(),
            'Authorization': 'Bearer ${relayToken.trim()}',
          };
    StructuredLog.info(
      traceId: traceId,
      event: 'api.ws.connect',
      anchor: 'ws_connect',
      context: {
        'session_id': sessionId,
        'url': uri.toString(),
        'ws_scheme': uri.scheme,
        'force_wss': forceWss,
        'is_secure_ws': uri.scheme == 'wss',
      },
    );
    return IOWebSocketChannel.connect(
      uri,
      headers: headers,
      pingInterval: const Duration(seconds: 12),
    );
  }

  /// POST /api/v1/control/send
  Future<void> sendControl({
    required String sessionId,
    required String data,
    bool submit = false,
    String source = 'flutter-xterm',
    String action = '',
    int? cols,
    int? rows,
  }) async {
    final traceId = StructuredLog.newTraceId('api_control');
    final stopwatch = Stopwatch()..start();
    StructuredLog.info(
      traceId: traceId,
      event: 'api.control.request',
      anchor: 'http_post_control_send',
      context: {
        'session_id': sessionId,
        'submit': submit,
        'action': action,
        'source': source,
        'cols': cols,
        'rows': rows,
        'payload_len': data.length,
      },
    );
    try {
      final response = await _dio.post(
        '/api/v1/control/send',
        data: {
          'session_id': sessionId,
          'input': data,
          'submit': submit,
          'source': source,
          if (action.isNotEmpty) 'action': action,
          if (cols != null && cols > 0) 'cols': cols,
          if (rows != null && rows > 0) 'rows': rows,
        },
      );
      StructuredLog.info(
        traceId: traceId,
        event: 'api.control.success',
        anchor: 'http_post_control_send',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'session_id': sessionId,
          'status_code': response.statusCode,
          'submit': submit,
          'action': action,
          'source': source,
          'cols': cols,
          'rows': rows,
        },
      );
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: traceId,
          event: '[CRITICAL] api.control.failed',
          anchor: 'http_post_control_send',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
          context: {
            'session_id': sessionId,
            'submit': submit,
            'action': action,
            'source': source,
            'cols': cols,
            'rows': rows,
            'payload_len': data.length,
          },
        ),
      );
      rethrow;
    }
  }

  Future<void> sendResize({
    required String sessionId,
    required int cols,
    required int rows,
    String source = 'flutter-terminal-resize',
  }) async {
    await sendControl(
      sessionId: sessionId,
      data: '',
      submit: false,
      source: source,
      action: 'resize',
      cols: cols,
      rows: rows,
    );
  }

  /// 终止会话（复用 control/send terminate action）
  Future<void> terminateSession(String sessionId) async {
    await sendControl(
      sessionId: sessionId,
      data: '',
      submit: false,
      action: 'terminate',
      source: 'flutter-xterm',
    );
  }

  Uri _buildWsUri({
    required String path,
    required Map<String, String> queryParameters,
  }) {
    final base = Uri.parse(baseUrl);
    final scheme = forceWss ? 'wss' : (base.scheme == 'https' ? 'wss' : 'ws');
    return base.replace(
      scheme: scheme,
      path: path,
      queryParameters: queryParameters,
    );
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

String _asString(Object? value, String field) {
  if (value is String) {
    return value;
  }
  throw FormatException(
      '[STATE_INVALID] $field is not string: ${value.runtimeType}');
}

String _readString(
  Map<String, dynamic> json,
  List<String> keys, {
  String fallback = '',
}) {
  for (final key in keys) {
    final value = json[key];
    if (value is String && value.trim().isNotEmpty) {
      return value;
    }
  }
  return fallback;
}

int _readInt(
  Map<String, dynamic> json,
  List<String> keys, {
  int fallback = 0,
}) {
  for (final key in keys) {
    final value = json[key];
    if (value is int) {
      return value;
    }
    if (value is num) {
      return value.toInt();
    }
  }
  return fallback;
}

List<String> _readStringList(
  Map<String, dynamic> json,
  String key,
) {
  final value = json[key];
  if (value is! List) {
    return const [];
  }
  return value
      .whereType<Object?>()
      .map((item) => item?.toString() ?? '')
      .toList(growable: false);
}

/// 会话信息
class SessionInfo {
  final String id;
  final String name;
  final String status;
  final int pid;
  final DateTime? createdAt;

  SessionInfo({
    required this.id,
    required this.name,
    required this.status,
    required this.pid,
    this.createdAt,
  });

  factory SessionInfo.fromJson(Map<String, dynamic> json) {
    final id = _readString(json, const ['session_id', 'id']);
    final name =
        _readString(json, const ['session_name', 'name'], fallback: id);
    final status = _readString(
      json,
      const ['status', 'state'],
      fallback: 'unknown',
    );
    final createdAtRaw = _readString(
      json,
      const ['created_at'],
      fallback: '',
    );
    return SessionInfo(
      id: id,
      name: name,
      status: status,
      pid: _readInt(json, const ['pid']),
      createdAt:
          createdAtRaw.isNotEmpty ? DateTime.tryParse(createdAtRaw) : null,
    );
  }
}

/// 屏幕快照
class ScreenOutput {
  final String sessionId;
  final String content;
  final List<String> lines;
  final int rows;
  final int cols;
  final int cursorRow;
  final int cursorCol;
  final int revision;
  final bool truncated;
  final String rawReplay;
  final int byteOffset;

  bool get hasRawReplay => rawReplay.isNotEmpty;

  ScreenOutput({
    required this.sessionId,
    required this.content,
    required this.lines,
    required this.rows,
    required this.cols,
    required this.cursorRow,
    required this.cursorCol,
    required this.revision,
    required this.truncated,
    this.rawReplay = '',
    this.byteOffset = 0,
  });

  factory ScreenOutput.fromJson(Map<String, dynamic> json) {
    final lines = _readStringList(json, 'lines');
    final content =
        _readString(json, const ['content'], fallback: lines.join('\n'));
    final colsFromPayload = _readInt(json, const ['cols'], fallback: 0);
    final cols = colsFromPayload > 0 ? colsFromPayload : 80;
    final rowsFromPayload =
        _readInt(json, const ['rows', 'line_count'], fallback: 0);
    final rows = rowsFromPayload > 0 ? rowsFromPayload : 24;
    return ScreenOutput(
      sessionId: _readString(json, const ['session_id'], fallback: ''),
      content: content,
      lines: lines,
      rows: rows,
      cols: cols,
      cursorRow: _readInt(json, const ['cursor_row'], fallback: 0),
      cursorCol: _readInt(json, const ['cursor_col'], fallback: 0),
      revision: _readInt(json, const ['revision'], fallback: 0),
      truncated: json['truncated'] == true,
      rawReplay: _readString(json, const ['raw_replay'], fallback: ''),
      byteOffset: _readInt(json, const ['byte_offset'], fallback: 0),
    );
  }
}

/// WS 输出增量
class OutputWsDelta {
  final String sessionId;
  final List<String> lines;
  final List<String> rawChunks;
  final int ts;

  OutputWsDelta({
    required this.sessionId,
    required this.lines,
    required this.rawChunks,
    required this.ts,
  });

  factory OutputWsDelta.fromJson(Map<String, dynamic> json) {
    return OutputWsDelta(
      sessionId: _asString(json['session_id'], 'delta.session_id'),
      lines: _readStringList(json, 'lines'),
      rawChunks: _readStringList(json, 'raw_chunks'),
      ts: _readInt(json, const ['ts'], fallback: 0),
    );
  }
}

/// WS 消息
class OutputWsMessage {
  final String type;
  final String sessionId;
  final OutputWsDelta? delta;
  final ScreenOutput? snapshot;
  final int ts;

  OutputWsMessage({
    required this.type,
    required this.sessionId,
    required this.delta,
    required this.snapshot,
    required this.ts,
  });

  factory OutputWsMessage.fromJson(Map<String, dynamic> json) {
    final deltaRaw = json['delta'];
    final snapshotRaw = json['snapshot'];
    return OutputWsMessage(
      type: _readString(json, const ['type'], fallback: ''),
      sessionId: _readString(json, const ['session_id'], fallback: ''),
      delta: deltaRaw is Map<String, dynamic>
          ? OutputWsDelta.fromJson(deltaRaw)
          : (deltaRaw is Map ? OutputWsDelta.fromJson(_asMap(deltaRaw)) : null),
      snapshot: snapshotRaw is Map<String, dynamic>
          ? ScreenOutput.fromJson(snapshotRaw)
          : (snapshotRaw is Map
              ? ScreenOutput.fromJson(_asMap(snapshotRaw))
              : null),
      ts: _readInt(json, const ['ts'], fallback: 0),
    );
  }
}

/// API 服务提供器
@riverpod
DoAiApiService apiService(Ref ref) {
  final baseUrl = ApiRuntimeConfigStore.baseUrl;
  final relayToken = ApiRuntimeConfigStore.relayToken;
  final forceWss = ApiRuntimeConfigStore.forceWss;
  return DoAiApiService(
    baseUrl: baseUrl,
    relayToken: relayToken,
    forceWss: forceWss,
  );
}
