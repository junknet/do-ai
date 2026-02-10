import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'screens/home_screen.dart';

void main() {
  runApp(
    const ProviderScope(
      child: DoAiApp(),
    ),
  );
}

class DoAiApp extends StatelessWidget {
  const DoAiApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'do-ai 终端',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF4D9CFF),
          brightness: Brightness.light,
        ),
        useMaterial3: true,
        fontFamily: 'Roboto',
      ),
      darkTheme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF4D9CFF),
          brightness: Brightness.dark,
        ),
        useMaterial3: true,
        fontFamily: 'Roboto',
      ),
      themeMode: ThemeMode.light, // 强制浅色主题用于调试
      home: const HomeScreen(),
    );
  }
}
