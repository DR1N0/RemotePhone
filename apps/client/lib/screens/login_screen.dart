import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/auth_service.dart';
import '../widgets/common.dart';

const _defaultBaseUrl = String.fromEnvironment(
  'MAKO_DEFAULT_SERVER',
  defaultValue: 'https://10.0.2.2:3000',
);

/// One flow for "add a server/account":
///
/// URL -> (PIN only on the first visit) -> sign in or create an account.
/// Pairing is not a separate button or page; it happens inline before the
/// first login and is skipped afterward because the certificate is pinned.
class LoginScreen extends StatefulWidget {
  final String? initialBaseUrl;
  final String? title;

  const LoginScreen({super.key, this.initialBaseUrl, this.title});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  late final TextEditingController _baseUrlController;
  final _pinController = TextEditingController();
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();

  bool _submitting = false;
  bool? _paired;
  Timer? _pairCheck;

  @override
  void initState() {
    super.initState();
    _baseUrlController =
        TextEditingController(text: widget.initialBaseUrl ?? _defaultBaseUrl);
    _refreshPairingState();
  }

  @override
  void dispose() {
    _pairCheck?.cancel();
    _baseUrlController.dispose();
    _pinController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  void _onUrlChanged(String _) {
    _pairCheck?.cancel();
    setState(() => _paired = null);
    _pairCheck = Timer(const Duration(milliseconds: 350), _refreshPairingState);
  }

  Future<void> _refreshPairingState() async {
    final url = _baseUrlController.text.trim();
    if (url.isEmpty) {
      if (mounted) setState(() => _paired = false);
      return;
    }
    final pin = await context.read<AuthService>().pinnedFingerprint(url);
    if (!mounted || _baseUrlController.text.trim() != url) return;
    setState(() => _paired = pin != null);
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    final url = _baseUrlController.text.trim();
    final auth = context.read<AuthService>();

    setState(() => _submitting = true);
    try {
      var paired = await auth.pinnedFingerprint(url) != null;
      if (!paired) {
        final pin = _pinController.text.trim();
        if (pin.isEmpty) {
          throw const ApiException(0, 'Enter the PIN shown on the server');
        }
        await auth.pair(baseUrl: url, pin: pin);
        paired = true;
        if (mounted) setState(() => _paired = true);
      }

      await auth.login(
        baseUrl: url,
        username: _usernameController.text.trim(),
        password: _passwordController.text,
      );      if (mounted) {
        Navigator.of(context).popUntil((route) => route.isFirst);
      }
    } on ApiException catch (e) {
      _show(e.message);
    } on StateError catch (e) {
      _show(e.message);
    } catch (e) {
      _show('Could not continue: $e');
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  void _show(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    final canPop = Navigator.of(context).canPop();
    final needsPin = _paired != true;
    return Scaffold(
      appBar: canPop
          ? AppBar(title: Text(widget.title ?? 'Add server'))
          : null,
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 460),
            child: Form(
              key: _formKey,
              child: ListView(
                padding: const EdgeInsets.fromLTRB(24, 12, 24, 32),
                children: [
                  if (!canPop) ...[
                    const SizedBox(height: 28),
                    const BrandLockup(size: 54),
                    const SizedBox(height: 20),
                    Text(
                      'Android, wherever you are.',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'Add a server once, then pick one of its instances.',
                      style: Theme.of(context).textTheme.bodySmall,
                    ),
                    const SizedBox(height: 28),
                  ],
                  TextFormField(
                    controller: _baseUrlController,
                    keyboardType: TextInputType.url,
                    autocorrect: false,
                    onChanged: _onUrlChanged,
                    decoration: const InputDecoration(
                      labelText: 'Server URL',
                      hintText: 'https://192.168.1.10:3000',
                      prefixIcon: Icon(Icons.dns_outlined),
                    ),
                    validator: (v) => (v == null || v.trim().isEmpty)
                        ? 'Server URL is required'
                        : null,
                  ),
                  const SizedBox(height: 12),
                  if (needsPin)
                    TextFormField(
                      controller: _pinController,
                      keyboardType: TextInputType.number,
                      maxLength: 12,
                      decoration: const InputDecoration(
                        labelText: 'Pairing code',
                        helperText: 'Ask the server admin for a one-time code.',
                        prefixIcon: Icon(Icons.lock_outline),
                        counterText: '',
                      ),
                      validator: (v) {
                        if (!needsPin) return null;
                        if (v == null || v.trim().isEmpty) {
                          return 'Pairing code is required';
                        }
                        return null;
                      },
                    )
                  else
                    const Padding(
                      padding: EdgeInsets.symmetric(vertical: 4),
                      child: Row(
                        children: [
                          Icon(Icons.verified_outlined,
                              size: 18, color: Color(0xFFD9B45B)),
                          SizedBox(width: 8),
                          Text(
                            'Certificate pinned',
                            style: TextStyle(
                              color: Color(0xFFD9B45B),
                              fontSize: 12,
                            ),
                          ),
                        ],
                      ),
                    ),
                  const SizedBox(height: 14),
                  TextFormField(
                    controller: _usernameController,
                    textInputAction: TextInputAction.next,
                    decoration: const InputDecoration(
                      labelText: 'Username',
                      prefixIcon: Icon(Icons.person_outline),
                    ),
                    validator: (v) => (v == null || v.trim().isEmpty)
                        ? 'Username is required'
                        : null,
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    controller: _passwordController,
                    obscureText: true,
                    decoration: const InputDecoration(
                      labelText: 'Password',
                      prefixIcon: Icon(Icons.lock_outline),
                    ),
                    onFieldSubmitted: (_) => _submit(),
                    validator: (v) {
                      if (v == null || v.isEmpty) return 'Password is required';
                      return null;
                    },
                  ),
                  const SizedBox(height: 22),
                  FilledButton(
                    onPressed: _submitting ? null : _submit,
                    child: _submitting
                        ? const SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(strokeWidth: 2))
                        : const Text('Log in'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
