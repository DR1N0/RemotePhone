import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/server_profile.dart';
import '../services/api_client.dart';
import '../services/auth_service.dart';
import '../services/server_discovery.dart';
import '../theme/app_theme.dart';
import '../widgets/common.dart';
import 'instance_list_screen.dart';
import 'login_screen.dart';
import 'settings_screen.dart';

/// Home screen. Servers are grouped by stable server ID and each account on
/// that server is a row underneath, so one phone can hold several identities
/// on the same host without adding another navigation level.
class ServerListScreen extends StatefulWidget {
  const ServerListScreen({super.key});

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  String? _busyKey;
  final _discovery = ServerDiscovery();
  final _nearby = <String, DiscoveredServer>{};
  StreamSubscription<DiscoveredServer>? _foundSub;
  StreamSubscription<String>? _lostSub;

  @override
  void initState() {
    super.initState();
    _foundSub = _discovery.servers.listen(_onDiscovered);
    _lostSub = _discovery.lost.listen((service) {
      if (!mounted) return;
      setState(() {
        _nearby.removeWhere((_, server) => server.serviceName == service);
      });
    });
    _discovery.start();
  }

  @override
  void dispose() {
    _foundSub?.cancel();
    _lostSub?.cancel();
    unawaited(_discovery.dispose());
    super.dispose();
  }

  Future<void> _onDiscovered(DiscoveredServer discovered) async {
    debugPrint('[discovery] ${discovered.name} ${discovered.serverId} '
        '${discovered.baseUrl}');
    final auth = context.read<AuthService>();
    final known = auth.servers
        .where((p) => p.serverId == discovered.serverId)
        .toList();
    if (known.isEmpty) {
      if (mounted) {
        setState(() => _nearby[discovered.serverId] = discovered);
      }
      return;
    }
    if (known.first.baseUrl == discovered.baseUrl &&
        known.first.name == discovered.name) {
      if (mounted) setState(() => _nearby.remove(discovered.serverId));
      return;
    }
    // Never trust mDNS alone: verify the server ID and pinned certificate
    // before moving a saved profile to the advertised address.
    final client = ApiClient(
      baseUrl: discovered.baseUrl,
      token: known.first.token,
      pinnedCertSha256: known.first.certSha256,
    );
    try {
      final info = await client.serverInfo();
      if (info.serverId != discovered.serverId) return;
      await auth.updateDiscoveredServer(
        discovered.serverId,
        discovered.baseUrl,
        info.name,
      );
      if (mounted) setState(() => _nearby.remove(discovered.serverId));
    } on ApiException {
      // Keep the old address; discovery is only a hint.
    } finally {
      client.close();
    }
  }

  Future<void> _open(ServerProfile profile) async {
    final auth = context.read<AuthService>();
    setState(() => _busyKey = '${profile.serverId}:${profile.accountKey}');
    try {
      await auth.selectServer(profile);
      if (!mounted) return;
      await Navigator.of(context).push(
        MaterialPageRoute(builder: (_) => const InstanceListScreen()),
      );
    } on ApiException catch (e) {
      _show(e.message);
    } catch (e) {
      _show('Cannot connect: $e');
    } finally {
      if (mounted) setState(() => _busyKey = null);
    }
  }

  void _show(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _addServer() async {
    await Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const LoginScreen()),
    );
  }

  Future<void> _pairDiscovered(DiscoveredServer server) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => LoginScreen(
          initialBaseUrl: server.baseUrl,
          title: 'Pair nearby server',
        ),
      ),
    );
    if (mounted) setState(() => _nearby.remove(server.serverId));
  }

  Future<void> _addAccount(ServerProfile profile) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => LoginScreen(
          initialBaseUrl: profile.baseUrl,
          title: 'Add account',
        ),
      ),
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

  Future<void> _removeServer(ServerProfile profile) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Forget server?'),
        content: Text(
          'Remove ${profile.name} and every account saved for it from this '
          'phone?',
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
    await context.read<AuthService>().removeServerGroup(profile.serverId);
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    final groups = <String, List<ServerProfile>>{};
    for (final profile in auth.servers) {
      groups.putIfAbsent(profile.serverId, () => []).add(profile);
    }
    final pinned = groups.values
        .where((g) => g.first.kind == ServerAddressKind.pinnedUrl)
        .toList();
    final lan = groups.values
        .where((g) => g.first.kind == ServerAddressKind.lan)
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
      body: ListView(
        padding: const EdgeInsets.fromLTRB(12, 4, 12, 24),
        children: [
          if (_nearby.isNotEmpty)
            _NearbySection(
              servers: _nearby.values.toList(),
              onPair: _pairDiscovered,
            ),
          if (pinned.isNotEmpty)
            _groupSection(
              context,
              title: 'Pinned URL',
              subtitle: 'Fixed address / public DNS',
              icon: Icons.link,
              groups: pinned,
            ),
          if (lan.isNotEmpty)
            _groupSection(
              context,
              title: 'LAN',
              subtitle: 'IP may change (mDNS planned)',
              icon: Icons.wifi,
              groups: lan,
            ),
          if (groups.isEmpty)
            const EmptyState(
              icon: Icons.dns_outlined,
              title: 'No servers yet',
              message: 'Tap + to pair a mako server.',
            ),
        ],
      ),
    );
  }

  Widget _groupSection(
    BuildContext context, {
    required String title,
    required String subtitle,
    required IconData icon,
    required List<List<ServerProfile>> groups,
  }) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SectionHeader(title: title, subtitle: subtitle, icon: icon),
        for (final accounts in groups)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: _ServerGroup(
              accounts: accounts,
              busyKey: _busyKey,
              onOpen: _open,
              onAddAccount: _addAccount,
              onEditAddress: _editAddress,
              onRemoveServer: _removeServer,
            ),
          ),
      ],
    );
  }
}

class _NearbySection extends StatelessWidget {
  final List<DiscoveredServer> servers;
  final Future<void> Function(DiscoveredServer) onPair;

  const _NearbySection({required this.servers, required this.onPair});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SectionHeader(
          title: 'Nearby',
          subtitle: 'Discovered on this LAN',
          icon: Icons.radar,
        ),
        for (final server in servers)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: Card(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
                child: Row(
                  children: [
                    const Icon(Icons.wifi_tethering, size: 20),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            server.name,
                            style: Theme.of(context).textTheme.titleMedium,
                            overflow: TextOverflow.ellipsis,
                          ),
                          const SizedBox(height: 2),
                          Text(
                            server.baseUrl,
                            style: Theme.of(context).textTheme.bodySmall,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ],
                      ),
                    ),
                    OutlinedButton(
                      onPressed: () => onPair(server),
                      style: OutlinedButton.styleFrom(
                        minimumSize: const Size(72, 36),
                        padding: const EdgeInsets.symmetric(horizontal: 12),
                      ),
                      child: const Text('Pair'),
                    ),
                  ],
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class _ServerGroup extends StatelessWidget {
  final List<ServerProfile> accounts;
  final String? busyKey;
  final Future<void> Function(ServerProfile) onOpen;
  final Future<void> Function(ServerProfile) onAddAccount;
  final Future<void> Function(ServerProfile) onEditAddress;
  final Future<void> Function(ServerProfile) onRemoveServer;

  const _ServerGroup({
    required this.accounts,
    required this.busyKey,
    required this.onOpen,
    required this.onAddAccount,
    required this.onEditAddress,
    required this.onRemoveServer,
  });

  @override
  Widget build(BuildContext context) {
    final server = accounts.first;
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 14, 6, 10),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        server.name,
                        style: Theme.of(context).textTheme.titleMedium,
                        overflow: TextOverflow.ellipsis,
                      ),
                      const SizedBox(height: 2),
                      Text(
                        server.baseUrl,
                        style: Theme.of(context).textTheme.bodySmall,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ),
                ),
                PopupMenuButton<String>(
                  icon: const Icon(Icons.more_vert),
                  onSelected: (value) {
                    switch (value) {
                      case 'account':
                        onAddAccount(server);
                      case 'edit':
                        onEditAddress(server);
                      case 'forget':
                        onRemoveServer(server);
                    }
                  },
                  itemBuilder: (_) => const [
                    PopupMenuItem(
                      value: 'account',
                      child: Text('Add account'),
                    ),
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
              ],
            ),
          ),
          const Divider(),
          for (var i = 0; i < accounts.length; i++) ...[
            if (i > 0) const Divider(),
            _AccountRow(
              account: accounts[i],
              busy: busyKey ==
                  '${accounts[i].serverId}:${accounts[i].accountKey}',
              onTap: () => onOpen(accounts[i]),
            ),
          ],
        ],
      ),
    );
  }
}

class _AccountRow extends StatelessWidget {
  final ServerProfile account;
  final bool busy;
  final VoidCallback onTap;

  const _AccountRow({
    required this.account,
    required this.busy,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: busy ? null : onTap,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
        child: Row(
          children: [
            const Icon(Icons.person_outline, size: 18),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                account.username,
                style: Theme.of(context).textTheme.bodyMedium,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (busy)
              const SizedBox(
                width: 16,
                height: 16,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            else
              const Icon(
                Icons.chevron_right,
                size: 20,
                color: AppColors.textMuted,
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
