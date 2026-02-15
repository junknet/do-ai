import 'package:do_ai_terminal/core/terminal_sanitize.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('parseMobileRenderMode', () {
    test('支持 clean/raw/auto 并在非法值回退', () {
      expect(
        parseMobileRenderMode('clean'),
        MobileRenderMode.clean,
      );
      expect(
        parseMobileRenderMode('raw'),
        MobileRenderMode.raw,
      );
      expect(
        parseMobileRenderMode('auto'),
        MobileRenderMode.auto,
      );
      expect(
        parseMobileRenderMode(
          'invalid',
          fallback: MobileRenderMode.raw,
        ),
        MobileRenderMode.raw,
      );
    });
  });

  test('sanitizeTerminalInputProbe 会过滤 xterm 探测序列', () {
    const raw = '\u001b[?1;2chello\u001b[>0;95;0c\u001b[8;24;80t';
    expect(sanitizeTerminalInputProbe(raw), 'hello');
  });

  test('filterNoiseLines 在 gemini profile 会过滤状态噪声', () {
    final filtered = filterNoiseLines(
      const [
        'Skill conflict detected: something',
        'Type your message or @path/to/file',
        '正常输出 A',
        'YOLO mode (ctrl + y to toggle)',
      ],
      profile: TerminalNoiseProfile.gemini,
    );
    expect(filtered, const ['正常输出 A']);
  });

  test('filterNoiseLines 在 default profile 保留普通行', () {
    final filtered = filterNoiseLines(
      const [
        'line-1',
        'line-2',
      ],
      profile: TerminalNoiseProfile.defaultProfile,
    );
    expect(filtered, const ['line-1', 'line-2']);
  });

  test('shouldAutoSwitchToCleanMode 命中 Gemini 标记时返回 true', () {
    final shouldSwitch = shouldAutoSwitchToCleanMode(
      const [
        'Logged in with Google: test@example.com',
        'Type your message or @path/to/file',
      ],
      profile: TerminalNoiseProfile.gemini,
    );
    expect(shouldSwitch, isTrue);
  });
}
