import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../core/structured_log.dart';

part 'api_service.g.dart';

/// do-ai relay API 客户端
class DoAiApiService {
  final Dio _dio;
  final String baseUrl;

  DoAiApiService({
    required this.baseUrl,
    Dio? dio,
  }) : _dio = dio ?? Dio() {
    _dio.options.baseUrl = baseUrl;
    _dio.options.connectTimeout = const Duration(seconds: 10);
    _dio.options.receiveTimeout = const Duration(seconds: 30);
  }

  /// 获取所有会话列表
  /// GET /api/v1/sessions
  Future<List<SessionInfo>> getSessions() async {
    final traceId = StructuredLog.newTraceId('api_sessions');
    final stopwatch = Stopwatch()..start();
    StructuredLog.info(
      traceId: traceId,
      event: 'api.sessions.request',
      anchor: 'http_get_sessions',
      context: {
        'url': '$_dio.options.baseUrl/api/v1/sessions',
      },
    );
    try {
      final response = await _dio.get('/api/v1/sessions');
      final data = _asMap(response.data)['sessions'];
      if (data is! List) {
        throw const FormatException('[STATE_INVALID] sessions is not a list');
      }
      final sessions = data
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

  /// 获取指定会话的屏幕输出
  /// GET /api/v1/output/screen?session=name
  Future<ScreenOutput> getScreenOutput(
    String sessionName, {
    required String traceId,
  }) async {
    final stopwatch = Stopwatch()..start();
    StructuredLog.info(
      traceId: traceId,
      event: 'api.screen.request',
      anchor: 'http_get_screen',
      context: {
        'session': sessionName,
      },
    );

    try {
      final response = await _dio.get(
        '/api/v1/output/screen',
        queryParameters: {'session': sessionName},
      );
      final output = ScreenOutput.fromJson(_asMap(response.data));

      StructuredLog.info(
        traceId: traceId,
        event: 'api.screen.success',
        anchor: 'http_get_screen',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'session': sessionName,
          'status_code': response.statusCode,
          'cols': output.cols,
          'rows': output.rows,
          'cursor_row': output.cursorRow,
          'cursor_col': output.cursorCol,
          'content_len': output.content.length,
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
            'session': sessionName,
          },
        ),
      );
      rethrow;
    }
  }

  /// 发送控制指令到会话
  /// POST /api/v1/control/send
  /// Body: {"session": "name", "data": "text"}
  Future<void> sendControl({
    required String sessionName,
    required String data,
  }) async {
    await _dio.post(
      '/api/v1/control/send',
      data: {
        'session': sessionName,
        'data': data,
      },
    );
  }

  /// 终止会话
  /// POST /api/v1/control/terminate
  /// Body: {"session": "name"}
  Future<void> terminateSession(String sessionName) async {
    await _dio.post(
      '/api/v1/control/terminate',
      data: {'session': sessionName},
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
  throw FormatException('[STATE_INVALID] expected map, got ${value.runtimeType}');
}

String _asString(Object? value, String field) {
  if (value is String) {
    return value;
  }
  throw FormatException('[STATE_INVALID] $field is not string: ${value.runtimeType}');
}

int _asInt(Object? value, String field) {
  if (value is int) {
    return value;
  }
  if (value is num) {
    return value.toInt();
  }
  throw FormatException('[STATE_INVALID] $field is not int: ${value.runtimeType}');
}

/// 会话信息
class SessionInfo {
  final String name;
  final String status;
  final int pid;
  final DateTime? createdAt;

  SessionInfo({
    required this.name,
    required this.status,
    required this.pid,
    this.createdAt,
  });

  factory SessionInfo.fromJson(Map<String, dynamic> json) {
    return SessionInfo(
      name: _asString(json['name'], 'name'),
      status: _asString(json['status'], 'status'),
      pid: _asInt(json['pid'], 'pid'),
      createdAt: json['created_at'] != null
          ? DateTime.parse(_asString(json['created_at'], 'created_at'))
          : null,
    );
  }
}

/// 屏幕输出
class ScreenOutput {
  final String content;
  final int rows;
  final int cols;
  final int cursorRow;
  final int cursorCol;

  ScreenOutput({
    required this.content,
    required this.rows,
    required this.cols,
    required this.cursorRow,
    required this.cursorCol,
  });

  factory ScreenOutput.fromJson(Map<String, dynamic> json) {
    return ScreenOutput(
      content: _asString(json['content'], 'content'),
      rows: _asInt(json['rows'], 'rows'),
      cols: _asInt(json['cols'], 'cols'),
      cursorRow: _asInt(json['cursor_row'], 'cursor_row'),
      cursorCol: _asInt(json['cursor_col'], 'cursor_col'),
    );
  }
}

/// API 服务提供器
@riverpod
DoAiApiService apiService(Ref ref) {
  // 使用 localhost (通过 adb reverse 端口转发)
  const baseUrl = 'http://localhost:18787';
  return DoAiApiService(baseUrl: baseUrl);
}
