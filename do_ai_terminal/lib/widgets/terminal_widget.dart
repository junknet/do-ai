import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:xterm/xterm.dart';
import '../core/structured_log.dart';
import '../services/api_service.dart';

/// xterm 终端组件 - 支持 ANSI 转义序列和 TUI 应用
class TerminalWidget extends ConsumerStatefulWidget {
  final String sessionName;

  const TerminalWidget({
    super.key,
    required this.sessionName,
  });

  @override
  ConsumerState<TerminalWidget> createState() => _TerminalWidgetState();
}

class _TerminalWidgetState extends ConsumerState<TerminalWidget> {
  late Terminal _terminal;
  Timer? _refreshTimer;
  int _screenCols = 80;
  int _screenRows = 24;
  String _latestSnapshotContent = '';
  final String _traceId = StructuredLog.newTraceId('terminal_widget');

  @override
  void initState() {
    super.initState();
    _initTerminal();
  }

  void _initTerminal() {
    _terminal = _newTerminal();
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.init',
      anchor: 'terminal_widget_init',
      context: {
        'session': widget.sessionName,
      },
    );

    // 首次加载内容
    _fetchScreenOutput();

    // 定时刷新（真实场景下会获取增量更新）
    _refreshTimer = Timer.periodic(
      const Duration(milliseconds: 1000),
      (_) => _fetchScreenOutput(),
    );
  }

  Terminal _newTerminal() {
    return Terminal(
      maxLines: 10000,
      reflowEnabled: false,
    );
  }

  Future<void> _fetchScreenOutput() async {
    final stopwatch = Stopwatch()..start();
    try {
      final apiService = ref.read(apiServiceProvider);
      final output = await apiService.getScreenOutput(
        widget.sessionName,
        traceId: _traceId,
      );

      if (output.cols <= 0 || output.rows <= 0) {
        throw StateError(
          '[STATE_INVALID] invalid screen size cols=${output.cols}, rows=${output.rows}',
        );
      }

      final snapshotUnchanged = output.cols == _screenCols &&
          output.rows == _screenRows &&
          output.content == _latestSnapshotContent;

      if (snapshotUnchanged) {
        StructuredLog.info(
          traceId: _traceId,
          event: 'terminal.snapshot.skip_unchanged',
          anchor: 'snapshot_compare',
          elapsedMs: stopwatch.elapsedMilliseconds,
          context: {
            'session': widget.sessionName,
            'cols': output.cols,
            'rows': output.rows,
          },
        );
        return;
      }

      final nextTerminal = _newTerminal();
      nextTerminal.resize(output.cols, output.rows);
      nextTerminal.write(output.content);

      if (!mounted) {
        return;
      }

      setState(() {
        _terminal = nextTerminal;
        _screenCols = output.cols;
        _screenRows = output.rows;
        _latestSnapshotContent = output.content;
      });

      StructuredLog.info(
        traceId: _traceId,
        event: 'terminal.snapshot.applied',
        anchor: 'snapshot_apply',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'session': widget.sessionName,
          'cols': output.cols,
          'rows': output.rows,
          'content_len': output.content.length,
        },
      );
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] terminal.snapshot.fetch_failed',
          anchor: 'snapshot_fetch',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
          context: {
            'session': widget.sessionName,
          },
        ),
      );
    }
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    StructuredLog.info(
      traceId: _traceId,
      event: 'terminal.dispose',
      anchor: 'terminal_widget_dispose',
      context: {
        'session': widget.sessionName,
      },
    );
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      color: Colors.black,
      child: TerminalView(
        _terminal,
        autoResize: false,
        textStyle: const TerminalStyle(
          fontFamily: 'monospace',
          fontFamilyFallback: [
            'Droid Sans Mono',
            'Noto Sans Mono CJK SC',
            'Noto Sans Mono CJK TC',
            'Noto Sans Mono CJK KR',
            'Noto Sans Mono CJK JP',
            'Noto Sans Mono',
            'monospace',
          ],
          fontSize: 13,
          height: 1.2,
        ),
        padding: const EdgeInsets.all(8),
      ),
    );
  }
}
