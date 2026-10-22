import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/auth_service.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  Future<void> _forgetAll(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Forget all servers?'),
        content: const Text(
          'This removes every paired server, saved session, and certificate '
          'pin from this phone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Forget all'),
          ),
        ],
      ),
    );
    if (confirmed != true || !context.mounted) return;
    await context.read<AuthService>().forgetAllServers();
    if (context.mounted) Navigator.of(context).popUntil((route) => route.isFirst);
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          const _SectionHeader('Discovery'),
          SwitchListTile(
            title: const Text('LAN discovery (mDNS)'),
            subtitle: const Text(
              'Planned. Will list nearby servers automatically while keeping '
              'PIN pairing and certificate pinning.',
            ),
            value: false,
            onChanged: null,
          ),
          const Divider(),
          const _SectionHeader('Servers'),
          ListTile(
            leading: const Icon(Icons.dns_outlined),
            title: const Text('Paired servers'),
            subtitle: Text('${auth.servers.length} remembered'),
          ),
          ListTile(
            leading: const Icon(Icons.delete_forever_outlined),
            title: const Text('Forget all servers'),
            subtitle: const Text('Remove every server, session, and pin'),
            onTap: () => _forgetAll(context),
          ),
          const Divider(),
          const _SectionHeader('About'),
          const ListTile(
            leading: Icon(Icons.info_outline),
            title: Text('mako'),
            subtitle: Text(
              'Version 1.0.0\n'
              'Servers are identified by a stable server ID and authenticated '
              'by a pinned certificate after PIN pairing.',
            ),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String text;

  const _SectionHeader(this.text);

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 8),
      child: Text(
        text,
        style: Theme.of(context).textTheme.titleSmall,
      ),
    );
  }
}
