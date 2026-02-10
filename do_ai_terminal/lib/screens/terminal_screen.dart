import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../widgets/terminal_widget.dart';

/// 终端页面
class TerminalScreen extends ConsumerWidget {
  final String sessionName;

  const TerminalScreen({
    super.key,
    required this.sessionName,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      appBar: AppBar(
        title: Text(sessionName),
        backgroundColor: Colors.black87,
        foregroundColor: Colors.white,
        elevation: 0,
      ),
      body: TerminalWidget(sessionName: sessionName),
    );
  }
}
