import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/structured_log.dart';
import '../services/api_service.dart';

class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  final String _traceId = StructuredLog.newTraceId('settings_screen');
  final TextEditingController _baseUrlController = TextEditingController();
  final TextEditingController _tokenController = TextEditingController();

  bool _forceWss = kDefaultForceWss;
  bool _loading = true;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    unawaited(_loadConfig());
  }

  Future<void> _loadConfig() async {
    try {
      await ApiRuntimeConfigStore.ensureLoaded();
      if (!mounted) {
        return;
      }
      setState(() {
        _baseUrlController.text = ApiRuntimeConfigStore.baseUrl;
        _tokenController.text = ApiRuntimeConfigStore.relayToken;
        _forceWss = ApiRuntimeConfigStore.forceWss;
        _loading = false;
      });
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] settings.config.load_failed',
          anchor: 'settings_load',
          error: e,
          stackTrace: stackTrace,
        ),
      );
      if (!mounted) {
        return;
      }
      setState(() {
        _loading = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('读取配置失败，请重试')),
      );
    }
  }

  Future<void> _saveConfig() async {
    if (_saving) {
      return;
    }
    final baseUrl = _baseUrlController.text.trim();
    final token = _tokenController.text.trim();
    if (baseUrl.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Relay 地址不能为空')),
      );
      return;
    }
    setState(() {
      _saving = true;
    });
    final stopwatch = Stopwatch()..start();
    try {
      await ApiRuntimeConfigStore.save(
        baseUrl: baseUrl,
        relayToken: token,
        forceWss: _forceWss,
      );
      ref.invalidate(apiServiceProvider);
      if (!mounted) {
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('已保存，终端页将使用新配置')),
      );
      StructuredLog.info(
        traceId: _traceId,
        event: 'settings.config.save_ok',
        anchor: 'settings_save',
        elapsedMs: stopwatch.elapsedMilliseconds,
        context: {
          'base_url': ApiRuntimeConfigStore.baseUrl,
          'force_wss': _forceWss,
          'token_len': token.length,
        },
      );
    } catch (e, stackTrace) {
      unawaited(
        StructuredLog.critical(
          traceId: _traceId,
          event: '[CRITICAL] settings.config.save_failed',
          anchor: 'settings_save',
          error: e,
          stackTrace: stackTrace,
          elapsedMs: stopwatch.elapsedMilliseconds,
        ),
      );
      if (!mounted) {
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('保存失败，请检查输入格式')),
      );
    } finally {
      if (mounted) {
        setState(() {
          _saving = false;
        });
      }
    }
  }

  Future<void> _applyLocalRelayPreset() async {
    setState(() {
      _baseUrlController.text = 'http://127.0.0.1:19797';
      _forceWss = false;
    });
  }

  Future<void> _resetDefaults() async {
    await ApiRuntimeConfigStore.resetToDefaults();
    ref.invalidate(apiServiceProvider);
    if (!mounted) {
      return;
    }
    setState(() {
      _baseUrlController.text = ApiRuntimeConfigStore.baseUrl;
      _tokenController.text = ApiRuntimeConfigStore.relayToken;
      _forceWss = ApiRuntimeConfigStore.forceWss;
    });
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('已恢复默认配置')),
    );
  }

  @override
  void dispose() {
    _baseUrlController.dispose();
    _tokenController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }

    return Column(
      children: [
        AppBar(
          title: const Text('设置'),
          elevation: 0,
        ),
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              TextField(
                controller: _baseUrlController,
                decoration: const InputDecoration(
                  labelText: 'Relay 地址',
                  hintText: 'http://127.0.0.1:19797',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _tokenController,
                decoration: const InputDecoration(
                  labelText: 'Relay Token',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              SwitchListTile(
                value: _forceWss,
                onChanged: (value) {
                  setState(() {
                    _forceWss = value;
                  });
                },
                title: const Text('强制 WSS'),
                subtitle: const Text('启用后将优先使用 wss:// 连接'),
              ),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  FilledButton.icon(
                    onPressed: _saving ? null : _saveConfig,
                    icon: const Icon(Icons.save_outlined),
                    label: Text(_saving ? '保存中...' : '保存配置'),
                  ),
                  OutlinedButton.icon(
                    onPressed: _saving ? null : _applyLocalRelayPreset,
                    icon: const Icon(Icons.usb),
                    label: const Text('本地 Relay 19797'),
                  ),
                  OutlinedButton.icon(
                    onPressed: _saving ? null : _resetDefaults,
                    icon: const Icon(Icons.restart_alt),
                    label: const Text('恢复默认'),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              const Text(
                '说明：修改配置后，无需重启 App。返回终端页会自动使用新地址连接。',
              ),
            ],
          ),
        ),
      ],
    );
  }
}
