import 'dart:math' as math;

enum MobileRenderMode {
  clean,
  raw,
  auto,
}

enum TerminalNoiseProfile {
  defaultProfile,
  gemini,
}

MobileRenderMode parseMobileRenderMode(
  String? raw, {
  MobileRenderMode fallback = MobileRenderMode.clean,
}) {
  final normalized = (raw ?? '').trim().toLowerCase();
  switch (normalized) {
    case 'clean':
      return MobileRenderMode.clean;
    case 'raw':
      return MobileRenderMode.raw;
    case 'auto':
      return MobileRenderMode.auto;
    default:
      return fallback;
  }
}

String mobileRenderModeValue(MobileRenderMode mode) {
  switch (mode) {
    case MobileRenderMode.clean:
      return 'clean';
    case MobileRenderMode.raw:
      return 'raw';
    case MobileRenderMode.auto:
      return 'auto';
  }
}

String mobileRenderModeLabel(MobileRenderMode mode) {
  switch (mode) {
    case MobileRenderMode.clean:
      return 'Clean';
    case MobileRenderMode.raw:
      return 'Raw';
    case MobileRenderMode.auto:
      return 'Auto';
  }
}

TerminalNoiseProfile parseTerminalNoiseProfile(
  String? raw, {
  TerminalNoiseProfile fallback = TerminalNoiseProfile.gemini,
}) {
  final normalized = (raw ?? '').trim().toLowerCase();
  switch (normalized) {
    case 'default':
      return TerminalNoiseProfile.defaultProfile;
    case 'gemini':
      return TerminalNoiseProfile.gemini;
    default:
      return fallback;
  }
}

String terminalNoiseProfileValue(TerminalNoiseProfile profile) {
  switch (profile) {
    case TerminalNoiseProfile.defaultProfile:
      return 'default';
    case TerminalNoiseProfile.gemini:
      return 'gemini';
  }
}

String sanitizeTerminalInputProbe(String data) {
  var out = data;
  // 过滤 xterm 自动探测应答，避免污染远端命令输入（例如 "1;2c0;0;0c"）。
  out = out.replaceAll(RegExp('\u001b\\[\\?1;2c'), '');
  out = out.replaceAll(RegExp('\u001b\\[>[0-9;]*c'), '');
  out = out.replaceAll(RegExp('\u001b\\[8;[0-9]+;[0-9]+t'), '');
  return out;
}

String stripAnsi(String text) {
  var out = text;
  out = out.replaceAll(RegExp('\u001b\\][^\\u0007]*\\u0007'), '');
  out = out.replaceAll(RegExp('\u001b\\[[0-9;?]*[ -/]*[@-~]'), '');
  out = out.replaceAll(RegExp('\u001b[@-_]'), '');
  return out;
}

List<String> filterNoiseLines(
  List<String> lines, {
  TerminalNoiseProfile profile = TerminalNoiseProfile.gemini,
}) {
  final out = <String>[];
  for (final raw in lines) {
    var line = stripAnsi(raw);
    line = line.replaceAll('\u0000', '');
    final trimmed = line.trim();
    if (trimmed.isEmpty) {
      continue;
    }
    if (isNoiseLine(trimmed, profile: profile)) {
      continue;
    }
    out.add(line);
  }
  return out;
}

bool isNoiseLine(
  String line, {
  TerminalNoiseProfile profile = TerminalNoiseProfile.gemini,
}) {
  if (RegExp(r'^(?:\d+;)+\d+[a-zA-Z]$').hasMatch(line)) {
    return true;
  }
  if (line.startsWith('> ') &&
      line.contains('(esc to cancel') &&
      line.contains('s)')) {
    return true;
  }
  if (profile == TerminalNoiseProfile.defaultProfile) {
    return false;
  }

  if (line.contains('Skill conflict detected') ||
      line.contains('overriding the same skill') ||
      line.contains('/.agents/skills') ||
      line.contains('/.gemini/skills') ||
      line.contains('Type your message or @path/to/file') ||
      line.contains('YOLO mode (ctrl + y to toggle)') ||
      line.contains('GEMINI.md files') ||
      line.contains('MCP servers')) {
    return true;
  }
  if ((line.startsWith('- ') || line.startsWith(' - ')) &&
      line.toLowerCase().contains('skills')) {
    return true;
  }
  if (line.contains('Auto (Gemini 3) /model') ||
      line.contains('no sandbox') ||
      line.contains('/model (100%)') ||
      RegExp(r'\|\s*\d+(\.\d+)?\s*MB$').hasMatch(line)) {
    return true;
  }
  return false;
}

bool shouldAutoSwitchToCleanMode(
  List<String> lines, {
  TerminalNoiseProfile profile = TerminalNoiseProfile.gemini,
}) {
  if (lines.isEmpty) {
    return false;
  }
  final sampleCount = math.min(lines.length, 14);
  var noisy = 0;
  for (var i = 0; i < sampleCount; i++) {
    if (_isDenseBlockLine(lines[i])) {
      noisy++;
    }
  }
  if (noisy >= 1) {
    return true;
  }
  if (profile != TerminalNoiseProfile.gemini) {
    return false;
  }
  for (final line in lines) {
    if (line.contains('YOLO mode') ||
        line.contains('Type your message or @path/to/file') ||
        line.contains('Logged in with Google') ||
        line.contains('Skill conflict detected') ||
        line.contains('Gemini 3')) {
      return true;
    }
  }
  return false;
}

bool _isDenseBlockLine(String line) {
  final text = line.trim();
  if (text.length < 8) {
    return false;
  }
  const blockChars = '█▓▒░▀▄▌▐▉▊▋▍▎▏';
  var count = 0;
  for (final rune in text.runes) {
    if (blockChars.contains(String.fromCharCode(rune))) {
      count++;
    }
  }
  if (count < 6) {
    return false;
  }
  final ratio = count / text.runes.length;
  return ratio >= 0.35;
}
