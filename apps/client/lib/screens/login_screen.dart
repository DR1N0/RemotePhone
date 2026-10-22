import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/auth_service.dart';
import 'register_screen.dart';

// 10.0.2.2 is the Android emulator's alias for the host machine. On a real
// phone the user must replace this with the host's LAN IP.
const _defaultBaseUrl = 'https://10.0.2.2:3000';

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
      // routing (see app.dart) — no explicit navigation needed here.
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
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

/// Mako's client surface has no PIN-pairing/certificate-pinning yet
/// (docs/architecture/security-model.md) — this app trusts whatever
/// certificate the configured server presents. Surfaced here, not just in
/// a code comment, so a user connecting over an untrusted network knows.
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
              'Connects without verifying the server certificate '
              '(no pairing yet) — only use this on a trusted network.',
              style: TextStyle(
                  color: Theme.of(context).colorScheme.onErrorContainer),
            ),
          ),
        ],
      ),
    );
  }
}
