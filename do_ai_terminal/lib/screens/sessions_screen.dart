import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../core/structured_log.dart';
import '../services/api_service.dart';
import '../widgets/session_card.dart';

/// 会话列表页面
class SessionsScreen extends ConsumerWidget {
  const SessionsScreen({super.key});

  static final String _traceId = StructuredLog.newTraceId('sessions_screen');

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    StructuredLog.info(
      traceId: _traceId,
      event: 'sessions_screen.build',
      anchor: 'ui_build',
    );
    return Column(
      children: [
        AppBar(
          title: const Text('do-ai 终端'),
          elevation: 0,
        ),
        Expanded(
          child: FutureBuilder<List<SessionInfo>>(
            future: ref.read(apiServiceProvider).getSessions(),
            builder: (context, snapshot) {
              StructuredLog.info(
                traceId: _traceId,
                event: 'sessions_screen.future_state',
                anchor: 'future_builder',
                context: {
                  'connection_state': snapshot.connectionState.name,
                  'has_error': snapshot.hasError,
                },
              );

              if (snapshot.connectionState == ConnectionState.waiting) {
                return const Center(child: CircularProgressIndicator());
              }

              if (snapshot.hasError) {
                unawaited(
                  StructuredLog.critical(
                    traceId: _traceId,
                    event: '[CRITICAL] sessions_screen.load_failed',
                    anchor: 'future_builder',
                    error: snapshot.error ?? 'unknown_error',
                    context: {
                      'connection_state': snapshot.connectionState.name,
                    },
                  ),
                );
                return Center(
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      const Icon(Icons.error_outline, size: 64, color: Colors.red),
                      const SizedBox(height: 16),
                      Text('连接失败: ${snapshot.error}'),
                      const SizedBox(height: 16),
                      ElevatedButton(
                        onPressed: () {
                          (context as Element).markNeedsBuild();
                        },
                        child: const Text('重试'),
                      ),
                    ],
                  ),
                );
              }

              final sessions = snapshot.data ?? [];

              if (sessions.isEmpty) {
                return const Center(
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      Icon(Icons.inbox_outlined, size: 64),
                      SizedBox(height: 16),
                      Text('暂无活动会话'),
                    ],
                  ),
                );
              }

              return ListView.builder(
                padding: const EdgeInsets.all(16),
                itemCount: sessions.length,
                itemBuilder: (context, index) {
                  return SessionCard(session: sessions[index]);
                },
              );
            },
          ),
        ),
      ],
    );
  }
}
