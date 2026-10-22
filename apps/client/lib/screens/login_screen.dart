import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/auth_service.dart';
import 'register_screen.dart';

// 10.0.2.2 is the Android emulator's alias for the host machine. A real
// phone must replace this with the host's LAN/Tailscale IP — or the build can
// bake in a default for testing/deployments:
//   flutter build apk --dart-define=MAKO_DEFAULT_SERVER=https://192.168.1.7:3000
const _defaultBaseUrl = String.fromEnvironment(
  'MAKO_DEFAULT_SERVER',
  defaultValue: 'https://10.0.2.2:3000',
);

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _baseUrlController = TextEditingController(text: _defaultBaseUrl);
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  bool _submitting = false;

  @override
  void dispose() {
    _baseUrlController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _pair() async {
    final pin = await showDialog<String>(
      context: context,
      builder: (_) => const _PairDialog(),
    );
    if (pin == null || pin.trim().isEmpty) return;
    if (!mounted) return;

    setState(() => _submitting = true);
    try {
      final fingerprint = await context.read<AuthService>().pair(
            baseUrl: _baseUrlController.text.trim(),
            pin: pin.trim(),
          );
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              'Paired. Certificate SHA-256: ${fingerprint.substring(0, 16)}…',
            ),
          ),
        );
      }
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Pairing failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() => _submitting = true);
    try {
      await context.read<AuthService>().login(
            baseUrl: _baseUrlController.text.trim(),
            username: _usernameController.text.trim(),
            password: _passwordController.text,
          );
      // AuthService.notifyListeners() drives the app's own auth-state
      // routing (see main.dart). When this screen was pushed from the server
      // picker, also unwind back to the (now rebuilt) root.
      if (mounted) {
        Navigator.of(context).popUntil((route) => route.isFirst);
      }
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    } on StateError catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Login failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('mako')),
      body: Padding(
        padding: const EdgeInsets.all(24),
        child: Form(
          key: _formKey,
          child: ListView(
            children: [
              const _InsecureTlsBanner(),
              const SizedBox(height: 16),
              TextFormField(
                controller: _baseUrlController,
                decoration: const InputDecoration(
                  labelText: 'Server URL',
                  hintText: 'https://192.168.1.10:3000',
                ),
                validator: (v) =>
                    (v == null || v.isEmpty) ? 'Server URL is required' : null,
              ),
              const SizedBox(height: 8),
              Align(
                alignment: Alignment.centerLeft,
                child: OutlinedButton.icon(
                  onPressed: _submitting ? null : _pair,
                  icon: const Icon(Icons.link),
                  label: const Text('Pair server'),
                ),
              ),
              const SizedBox(height: 16),
              TextFormField(
                controller: _usernameController,
                decoration: const InputDecoration(labelText: 'Username'),
                validator: (v) =>
                    (v == null || v.isEmpty) ? 'Username is required' : null,
              ),
              const SizedBox(height: 16),
              TextFormField(
                controller: _passwordController,
                decoration: const InputDecoration(labelText: 'Password'),
                obscureText: true,
                onFieldSubmitted: (_) => _submit(),
                validator: (v) =>
                    (v == null || v.isEmpty) ? 'Password is required' : null,
              ),
              const SizedBox(height: 24),
              FilledButton(
                onPressed: _submitting ? null : _submit,
                child: _submitting
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(strokeWidth: 2))
                    : const Text('Log in'),
              ),
              const SizedBox(height: 8),
              TextButton(
                onPressed: _submitting
                    ? null
                    : () => Navigator.of(context).push(MaterialPageRoute(
                          builder: (_) => RegisterScreen(
                            initialBaseUrl: _baseUrlController.text.trim(),
                          ),
                        )),
                child: const Text('Have an invite token? Register'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _PairDialog extends StatefulWidget {
  const _PairDialog();

  @override
  State<_PairDialog> createState() => _PairDialogState();
}

class _PairDialogState extends State<_PairDialog> {
  final _pinController = TextEditingController();

  @override
  void dispose() {
    _pinController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Pair this server'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Text(
            'Enter the PIN printed by mako-controlplane on the host. '
            'This also pins the server certificate for later connections.',
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _pinController,
            autofocus: true,
            keyboardType: TextInputType.number,
            decoration: const InputDecoration(labelText: 'Pairing PIN'),
            onSubmitted: (value) => Navigator.of(context).pop(value),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(_pinController.text),
          child: const Text('Pair'),
        ),
      ],
    );
  }
}

/// The client surface uses a self-signed certificate, so a new device must
/// PIN-pair before it can trust the server. Surfaced here, not just in a code
/// comment, so a user connecting over an untrusted network knows.
class _InsecureTlsBanner extends StatelessWidget {
  const _InsecureTlsBanner();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.errorContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Icon(Icons.warning_amber_rounded,
              color: Theme.of(context).colorScheme.onErrorContainer),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Pair this server first using the PIN shown by '
              'mako-controlplane. The certificate is pinned after pairing.',
              style: TextStyle(
                  color: Theme.of(context).colorScheme.onErrorContainer),
            ),
          ),
        ],
      ),
    );
  }
}
