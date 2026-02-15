import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/structured_log.dart';
import '../services/api_service.dart';
import '../widgets/session_card.dart';

/// 会话列表页面
class SessionsScreen extends ConsumerStatefulWidget {
  const SessionsScreen({super.key});

  @override
  ConsumerState<SessionsScreen> createState() => _SessionsScreenState();
}

class _SessionsScreenState extends ConsumerState<SessionsScreen> {
  static final String _traceId = StructuredLog.newTraceId('sessions_screen');
  static const Duration _refreshInterval = Duration(seconds: 3);

  late Future<List<SessionInfo>> _sessionsFuture;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    _sessionsFuture = _loadSessions();
    _refreshTimer = Timer.periodic(_refreshInterval, (_) {
      if (!mounted) {
        return;
      }
      setState(() {
        _sessionsFuture = _loadSessions();
      });
    });
  }

  Future<List<SessionInfo>> _loadSessions() async {
    StructuredLog.info(
      traceId: _traceId,
      event: 'sessions_screen.fetch',
      anchor: 'sessions_fetch',
    );
    return ref.read(apiServiceProvider).getSessions();
  }

  Future<void> _refreshNow() async {
    final future = _loadSessions();
    if (mounted) {
      setState(() {
        _sessionsFuture = future;
      });
    }
    await future;
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _refreshTimer = null;
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
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
            future: _sessionsFuture,
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
                      const Icon(Icons.error_outline,
                          size: 64, color: Colors.red),
                      const SizedBox(height: 16),
                      Text(
                        '连接失败 (${ApiRuntimeConfigStore.baseUrl}): ${snapshot.error}',
                      ),
                      const SizedBox(height: 16),
                      ElevatedButton(
                        onPressed: () => unawaited(_refreshNow()),
                        child: const Text('重试'),
                      ),
                    ],
                  ),
                );
              }

              final sessions = snapshot.data ?? [];

              if (sessions.isEmpty) {
                return RefreshIndicator(
                  onRefresh: _refreshNow,
                  child: ListView(
                    children: const [
                      SizedBox(height: 140),
                      Icon(Icons.inbox_outlined, size: 64),
                      SizedBox(height: 16),
                      Center(child: Text('暂无活动会话')),
                    ],
                  ),
                );
              }

              return RefreshIndicator(
                onRefresh: _refreshNow,
                child: ListView.builder(
                  padding: const EdgeInsets.all(16),
                  itemCount: sessions.length,
                  itemBuilder: (context, index) {
                    return SessionCard(
                      session: sessions[index],
                      onSessionChanged: () => unawaited(_refreshNow()),
                    );
                  },
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}
