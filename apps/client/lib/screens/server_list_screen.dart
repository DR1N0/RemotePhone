import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/server_profile.dart';
import '../services/api_client.dart';
import '../services/auth_service.dart';
import 'instance_list_screen.dart';
import 'login_screen.dart';
import 'settings_screen.dart';

/// The client's home screen. Servers are grouped by how their address behaves:
/// fixed URLs (normally public DNS) versus LAN endpoints whose IP may move.
/// Instance lists are pushed on top of this screen, so Back returns here.
class ServerListScreen extends StatefulWidget {
  const ServerListScreen({super.key});

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  String? _busyId;

  Future<void> _open(ServerProfile profile) async {
    final auth = context.read<AuthService>();
    setState(() => _busyId = profile.serverId);
    try {
      await auth.selectServer(profile);
      if (!mounted) return;
      await Navigator.of(context).push(
        MaterialPageRoute(builder: (_) => const InstanceListScreen()),
      );
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Cannot connect: $e')));
      }
    } finally {
      if (mounted) setState(() => _busyId = null);
    }
  }

  Future<void> _addServer() async {
    await Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const LoginScreen()),
    );
  }

  Future<void> _editAddress(ServerProfile profile) async {
    final address = await showDialog<String>(
      context: context,
      builder: (_) => _EditAddressDialog(initial: profile.baseUrl),
    );
    if (address == null || address.isEmpty || !mounted) return;
    await context.read<AuthService>().updateServerAddress(profile, address);
  }

  Future<void> _remove(ServerProfile profile) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Forget server?'),
        content: Text(
          'Remove ${profile.name} and its saved session from this phone?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Forget'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await context.read<AuthService>().removeServer(profile);
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    final pinned = auth.servers
        .where((s) => s.kind == ServerAddressKind.pinnedUrl)
        .toList();
    final lan = auth.servers
        .where((s) => s.kind == ServerAddressKind.lan)
        .toList();

    return Scaffold(
      appBar: AppBar(
        title: const Text('Servers'),
        actions: [
          IconButton(
            tooltip: 'Add server',
            icon: const Icon(Icons.add),
            onPressed: _addServer,
          ),
          IconButton(
            tooltip: 'Settings',
            icon: const Icon(Icons.settings_outlined),
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const SettingsScreen()),
            ),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          // mDNS refresh will live here; remembered profiles are always shown.
        },
        child: ListView(
          padding: const EdgeInsets.only(bottom: 24),
          children: [
            if (pinned.isNotEmpty)
              _ServerSection(
                title: 'Pinned URL',
                subtitle: 'Fixed address / public DNS',
                icon: Icons.link,
                profiles: pinned,
                busyId: _busyId,
                onOpen: _open,
                onEditAddress: _editAddress,
                onRemove: _remove,
              ),
            if (lan.isNotEmpty)
              _ServerSection(
                title: 'LAN',
                subtitle: 'IP may change (mDNS planned)',
                icon: Icons.wifi,
                profiles: lan,
                busyId: _busyId,
                onOpen: _open,
                onEditAddress: _editAddress,
                onRemove: _remove,
              ),
            if (pinned.isEmpty && lan.isEmpty)
              const Padding(
                padding: EdgeInsets.all(32),
                child: Text(
                  'No servers yet. Tap + to pair one.',
                  textAlign: TextAlign.center,
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _EditAddressDialog extends StatefulWidget {
  final String initial;

  const _EditAddressDialog({required this.initial});

  @override
  State<_EditAddressDialog> createState() => _EditAddressDialogState();
}

class _EditAddressDialogState extends State<_EditAddressDialog> {
  late final TextEditingController _controller =
      TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Edit address'),
      content: TextField(
        controller: _controller,
        autofocus: true,
        decoration: const InputDecoration(
          labelText: 'Server URL',
          hintText: 'https://192.168.1.10:3000',
        ),
        onSubmitted: (value) => Navigator.of(context).pop(value.trim()),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () =>
              Navigator.of(context).pop(_controller.text.trim()),
          child: const Text('Save'),
        ),
      ],
    );
  }
}

class _ServerSection extends StatelessWidget {
  final String title;
  final String subtitle;
  final IconData icon;
  final List<ServerProfile> profiles;
  final String? busyId;
  final Future<void> Function(ServerProfile) onOpen;
  final Future<void> Function(ServerProfile) onEditAddress;
  final Future<void> Function(ServerProfile) onRemove;

  const _ServerSection({
    required this.title,
    required this.subtitle,
    required this.icon,
    required this.profiles,
    required this.busyId,
    required this.onOpen,
    required this.onEditAddress,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 20, 16, 8),
          child: Row(
            children: [
              Icon(icon, size: 18),
              const SizedBox(width: 8),
              Text(title, style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  subtitle,
                  style: Theme.of(context).textTheme.bodySmall,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
        ),
        for (final profile in profiles)
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
            child: ListTile(
              title: Text(profile.name),
              subtitle: Text(
                '${profile.baseUrl}\n${profile.username} · ID '
                '${profile.serverId.length > 8 ? profile.serverId.substring(0, 8) : profile.serverId}',
              ),
              isThreeLine: true,
              trailing: busyId == profile.serverId
                  ? const SizedBox(
                      width: 24,
                      height: 24,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : PopupMenuButton<String>(
                      onSelected: (value) {
                        switch (value) {
                          case 'edit':
                            onEditAddress(profile);
                          case 'forget':
                            onRemove(profile);
                        }
                      },
                      itemBuilder: (_) => const [
                        PopupMenuItem(
                          value: 'edit',
                          child: Text('Edit address'),
                        ),
                        PopupMenuItem(
                          value: 'forget',
                          child: Text('Forget server'),
                        ),
                      ],
                    ),
              onTap: busyId == null ? () => onOpen(profile) : null,
            ),
          ),
      ],
    );
  }
}
