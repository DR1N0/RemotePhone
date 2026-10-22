import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/managed_instance.dart';
import '../models/template_info.dart';
import '../services/api_client.dart';
import '../services/auth_service.dart';
import '../theme/app_theme.dart';
import '../widgets/common.dart';
import 'stream_screen.dart';

class InstanceListScreen extends StatefulWidget {
  const InstanceListScreen({super.key});

  @override
  State<InstanceListScreen> createState() => _InstanceListScreenState();
}

class _InstanceListScreenState extends State<InstanceListScreen> {
  List<ManagedInstance> _instances = [];
  bool _loading = true;
  String? _error;
  final Set<String> _busyIds = {};

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  ApiClient get _api => context.read<AuthService>().client;

  Future<void> _refresh() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final instances = await _api.listInstances();
      if (!mounted) return;
      setState(() => _instances = instances);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _withBusy(String id, Future<void> Function() action) async {
    setState(() => _busyIds.add(id));
    try {
      await action();
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    } finally {
      if (mounted) setState(() => _busyIds.remove(id));
      await _refresh();
    }
  }

  Future<void> _createInstance() async {
    List<TemplateInfo> templates;
    try {
      templates = await _api.listTemplates();
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
      return;
    }
    if (!mounted) return;
    if (templates.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text('No Android AVDs found on the server host. Create one '
            'in Android Studio (or avdmanager), then retry.'),
      ));
      return;
    }

    final nameController = TextEditingController();
    var selectedTemplate = templates.first.templateId;
    final created = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('New instance'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameController,
                decoration: InputDecoration(
                  labelText: 'Name',
                  hintText: templates.first.label,
                ),
                autofocus: true,
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: selectedTemplate,
                decoration: const InputDecoration(labelText: 'Template'),
                items: templates
                    .map((t) => DropdownMenuItem(
                          value: t.templateId,
                          child: Text(t.label),
                        ))
                    .toList(),
                onChanged: (value) {
                  if (value != null) {
                    setDialogState(() => selectedTemplate = value);
                  }
                },
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: const Text('Create'),
            ),
          ],
        ),
      ),
    );
    if (created != true) return;
    final selected = templates.firstWhere(
      (t) => t.templateId == selectedTemplate,
      orElse: () => templates.first,
    );
    final name = nameController.text.trim().isEmpty
        ? selected.label
        : nameController.text.trim();
    try {
      await _api.createInstance(name: name, templateId: selectedTemplate);
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    } finally {
      await _refresh();
    }
  }

  void _connect(ManagedInstance instance) {
    Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => StreamScreen(instance: instance),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    return Scaffold(
      appBar: AppBar(
        title: Text(auth.activeProfile?.name ??
            auth.currentUser?.displayName ??
            'Instances'),
        actions: [
          IconButton(
            tooltip: 'New instance',
            icon: const Icon(Icons.add),
            onPressed: _createInstance,
          ),
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _refresh,
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading && _instances.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return EmptyState(
        icon: Icons.cloud_off,
        title: 'Could not load instances',
        message: _error!,
        action: OutlinedButton.icon(
          onPressed: _refresh,
          icon: const Icon(Icons.refresh),
          label: const Text('Retry'),
        ),
      );
    }
    if (_instances.isEmpty) {
      return EmptyState(
        icon: Icons.smartphone,
        title: 'No instances yet',
        message: 'Create an instance from one of the server templates to '
            'start a remote Android device.',
        action: FilledButton.icon(
          onPressed: _createInstance,
          icon: const Icon(Icons.add),
          label: const Text('New instance'),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
        itemCount: _instances.length,
        separatorBuilder: (_, _) => const SizedBox(height: 10),
        itemBuilder: (context, index) {
          final instance = _instances[index];
          final busy = _busyIds.contains(instance.id);
          final templateLabel = instance.templateId.split(':').last;
          final running = instance.isRunning;
          return Card(
            clipBehavior: Clip.antiAlias,
            child: InkWell(
              onTap: running ? () => _connect(instance) : null,
              child: Padding(
                padding: const EdgeInsets.fromLTRB(14, 14, 6, 10),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Container(
                          width: 46,
                          height: 46,
                          decoration: BoxDecoration(
                            color: running
                                ? AppColors.accent.withValues(alpha: 0.12)
                                : AppColors.surfaceHigh,
                            borderRadius: BorderRadius.circular(14),
                          ),
                          child: Icon(
                            Icons.smartphone,
                            color: running
                                ? AppColors.accent
                                : AppColors.textMuted,
                            size: 24,
                          ),
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                instance.name,
                                style:
                                    Theme.of(context).textTheme.titleMedium,
                                overflow: TextOverflow.ellipsis,
                              ),
                              const SizedBox(height: 2),
                              Text(
                                templateLabel,
                                style: Theme.of(context).textTheme.bodySmall,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ],
                          ),
                        ),
                        if (busy)
                          const Padding(
                            padding: EdgeInsets.all(12),
                            child: SizedBox(
                              width: 18,
                              height: 18,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            ),
                          )
                        else
                          StatusPill(
                            label: running ? 'Running' : 'Stopped',
                            color: running
                                ? AppColors.success
                                : AppColors.textMuted,
                          ),
                      ],
                    ),
                    if (instance.lastError != null) ...[
                      const SizedBox(height: 8),
                      Text(
                        instance.lastError!,
                        style: const TextStyle(
                          color: AppColors.warning,
                          fontSize: 12,
                        ),
                      ),
                    ],
                    const SizedBox(height: 6),
                    Row(
                      mainAxisAlignment: MainAxisAlignment.end,
                      children: [
                        if (!running)
                          IconButton(
                            icon: const Icon(Icons.play_arrow),
                            tooltip: 'Start',
                            onPressed: () => _withBusy(instance.id,
                                () => _api.startInstance(instance.id)),
                          ),
                        if (running) ...[
                          IconButton(
                            icon: const Icon(Icons.cast_connected),
                            tooltip: 'Connect',
                            onPressed: () => _connect(instance),
                          ),
                          IconButton(
                            icon: const Icon(Icons.stop),
                            tooltip: 'Stop',
                            onPressed: () => _withBusy(instance.id,
                                () => _api.stopInstance(instance.id)),
                          ),
                        ],
                        IconButton(
                          icon: const Icon(Icons.delete_outline),
                          tooltip: 'Delete',
                          onPressed: () => _withBusy(instance.id,
                              () => _api.deleteInstance(instance.id)),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}
