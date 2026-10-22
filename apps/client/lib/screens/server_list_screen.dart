import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/server_profile.dart';
import '../services/auth_service.dart';
import '../services/api_client.dart';
import 'login_screen.dart';

/// First screen after startup once at least one server has been paired.
/// Servers are keyed by the stable server ID, so renaming or re-addressing the
/// host does not create duplicates.
class ServerListScreen extends StatefulWidget {
  const ServerListScreen({super.key});

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  String? _busyId;

  Future<void> _open(ServerProfile profile) async {
    setState(() => _busyId = profile.serverId);
    try {
      await context.read<AuthService>().selectServer(profile);
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
    return Scaffold(
      appBar: AppBar(
        title: const Text('Servers'),
        actions: [
          IconButton(
            tooltip: 'Add server',
            icon: const Icon(Icons.add),
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const LoginScreen()),
            ),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          // Re-selecting is what validates tokens; there is no separate list
          // fetch yet. This will also be where mDNS refresh lands.
        },
        child: ListView.builder(
          itemCount: auth.servers.length,
          itemBuilder: (context, index) {
            final profile = auth.servers[index];
            final busy = _busyId == profile.serverId;
            return ListTile(
              leading: const Icon(Icons.dns_outlined),
              title: Text(profile.name),
              subtitle: Text('${profile.baseUrl}\n${profile.username}'),
              isThreeLine: true,
              trailing: busy
                  ? const SizedBox(
                      width: 24,
                      height: 24,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : IconButton(
                      tooltip: 'Forget server',
                      icon: const Icon(Icons.delete_outline),
                      onPressed: () => _remove(profile),
                    ),
              onTap: busy ? null : () => _open(profile),
            );
          },
        ),
      ),
    );
  }
}
