import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:do_ai_terminal/main.dart';
import 'package:do_ai_terminal/services/api_service.dart';

class _FakeApiService extends DoAiApiService {
  _FakeApiService() : super(baseUrl: 'http://127.0.0.1:18787');

  @override
  Future<List<SessionInfo>> getSessions() async {
    return const [];
  }
}

void main() {
  testWidgets('App launches successfully', (WidgetTester tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          apiServiceProvider.overrideWith((ref) => _FakeApiService()),
        ],
        child: DoAiApp(),
      ),
    );

    // 验证应用启动
    expect(find.byType(MaterialApp), findsOneWidget);
  });
}
