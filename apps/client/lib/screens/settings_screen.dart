import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/auth_service.dart';
import '../theme/app_theme.dart';
import '../widgets/common.dart';

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
    if (context.mounted) {
      Navigator.of(context).popUntil((route) => route.isFirst);
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(12, 4, 12, 24),
        children: [
          const SectionHeader(
            title: 'Discovery',
            icon: Icons.radar,
            subtitle: 'How this phone finds servers',
          ),
          Card(
            child: SwitchListTile(
              title: const Text('LAN discovery (mDNS)'),
              subtitle: const Text(
                'Planned. Lists nearby servers automatically while keeping '
                'PIN pairing and certificate pinning.',
              ),
              value: false,
              onChanged: null,
            ),
          ),
          const SectionHeader(
            title: 'Servers',
            icon: Icons.dns_outlined,
            subtitle: 'Paired control planes',
          ),
          Card(
            child: Column(
              children: [
                ListTile(
                  leading: const Icon(Icons.dns_outlined),
                  title: const Text('Paired servers'),
                  trailing: Text(
                    '${auth.servers.length}',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                ),
                const Divider(),
                ListTile(
                  leading: const Icon(
                    Icons.delete_forever_outlined,
                    color: AppColors.danger,
                  ),
                  title: const Text('Forget all servers'),
                  subtitle: const Text(
                    'Remove every server, session, and certificate pin',
                  ),
                  onTap: () => _forgetAll(context),
                ),
              ],
            ),
          ),
          const SectionHeader(
            title: 'About',
            icon: Icons.info_outline,
          ),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const BrandLockup(size: 40),
                  const SizedBox(height: 14),
                  Text(
                    'mako 1.0.0',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 6),
                  Text(
                    'Servers are identified by a stable server ID and '
                    'authenticated by a pinned certificate after PIN pairing.',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
