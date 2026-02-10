import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';

typedef JsonMap = Map<String, Object?>;

/// 结构化日志工具（JSON + TraceID + 耗时 + 状态锚点）
final class StructuredLog {
  StructuredLog._();

  static const String _criticalLogFileName = 'do_ai_terminal_critical.log';

  static String newTraceId(String scene) {
    final micros = DateTime.now().microsecondsSinceEpoch;
    return '$scene-$micros';
  }

  static void info({
    required String traceId,
    required String event,
    required String anchor,
    String status = 'ok',
    int? elapsedMs,
    JsonMap context = const <String, Object?>{},
  }) {
    _emit(
      level: 'INFO',
      traceId: traceId,
      event: event,
      anchor: anchor,
      status: status,
      elapsedMs: elapsedMs,
      context: context,
    );
  }

  static Future<void> critical({
    required String traceId,
    required String event,
    required String anchor,
    required Object error,
    StackTrace? stackTrace,
    int? elapsedMs,
    JsonMap context = const <String, Object?>{},
  }) async {
    final payload = _buildPayload(
      level: 'CRITICAL',
      traceId: traceId,
      event: event,
      anchor: anchor,
      status: 'failed',
      elapsedMs: elapsedMs,
      context: <String, Object?>{
        ...context,
        'error': error.toString(),
        if (stackTrace != null) 'stack_trace': stackTrace.toString(),
      },
    );

    _print(payload);
    await _appendCriticalLog(payload);
  }

  static void _emit({
    required String level,
    required String traceId,
    required String event,
    required String anchor,
    required String status,
    int? elapsedMs,
    JsonMap context = const <String, Object?>{},
  }) {
    final payload = _buildPayload(
      level: level,
      traceId: traceId,
      event: event,
      anchor: anchor,
      status: status,
      elapsedMs: elapsedMs,
      context: context,
    );
    _print(payload);
  }

  static JsonMap _buildPayload({
    required String level,
    required String traceId,
    required String event,
    required String anchor,
    required String status,
    int? elapsedMs,
    JsonMap context = const <String, Object?>{},
  }) {
    return <String, Object?>{
      'ts': DateTime.now().toUtc().toIso8601String(),
      'level': level,
      'trace_id': traceId,
      'event': event,
      'anchor': anchor,
      'status': status,
      if (elapsedMs != null) 'elapsed_ms': elapsedMs,
      if (context.isNotEmpty) 'context': context,
    };
  }

  static void _print(JsonMap payload) {
    debugPrint(jsonEncode(payload));
  }

  static Future<void> _appendCriticalLog(JsonMap payload) async {
    try {
      final directory = await getApplicationDocumentsDirectory();
      final file = File('${directory.path}/$_criticalLogFileName');
      await file.writeAsString('${jsonEncode(payload)}\n',
          mode: FileMode.append, flush: true);
    } catch (e) {
      final fallback = <String, Object?>{
        'ts': DateTime.now().toUtc().toIso8601String(),
        'level': 'ERROR',
        'trace_id': payload['trace_id'],
        'event': '[CRITICAL] structured_log.persist_failed',
        'anchor': 'critical_log_append',
        'status': 'failed',
        'context': <String, Object?>{
          'reason': e.toString(),
        },
      };
      _print(fallback);
    }
  }
}
