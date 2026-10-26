import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/auth_service.dart';
import '../theme/app_theme.dart';
import '../widgets/common.dart';

const _defaultBaseUrl = String.fromEnvironment(
  'MAKO_DEFAULT_SERVER',
  defaultValue: 'https://10.0.2.2:3000',
);

enum _TrustState { checking, trusted, needsCode, certMismatch }

/// One client-available flow:
///
/// URL -> (access code only when the certificate is not already trusted) ->
/// username/password. A code may create a new ordinary account or pair this
/// device to an existing account; the server decides. When the server is
/// already known by server ID and the pinned certificate matches, the code is
/// optional and can be revealed only when the user has one.
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
  final _codeController = TextEditingController();
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();

  bool _submitting = false;
  bool _showCodeWhenTrusted = false;
  _TrustState _trust = _TrustState.checking;
  TrustedServer? _detected;
  Timer? _trustCheck;

  @override
  void initState() {
    super.initState();
    _baseUrlController =
        TextEditingController(text: widget.initialBaseUrl ?? _defaultBaseUrl);
    _refreshTrust();
  }

  @override
  void dispose() {
    _trustCheck?.cancel();
    _baseUrlController.dispose();
    _codeController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  void _onUrlChanged(String _) {
    _trustCheck?.cancel();
    setState(() {
      _trust = _TrustState.checking;
      _detected = null;
      _showCodeWhenTrusted = false;
    });
    _trustCheck = Timer(const Duration(milliseconds: 350), _refreshTrust);
  }

  Future<void> _refreshTrust() async {
    final url = _baseUrlController.text.trim();
    if (url.isEmpty) {
      if (mounted) setState(() => _trust = _TrustState.needsCode);
      return;
    }
    if (mounted) setState(() => _trust = _TrustState.checking);
    try {
      final detected = await context.read<AuthService>().detectTrust(url);
      if (!mounted || _baseUrlController.text.trim() != url) return;
      setState(() {
        _detected = detected;
        _trust = detected.certMismatch
            ? _TrustState.certMismatch
            : detected.trusted
                ? _TrustState.trusted
                : _TrustState.needsCode;
      });
    } catch (_) {
      if (!mounted || _baseUrlController.text.trim() != url) return;
      setState(() => _trust = _TrustState.needsCode);
    }
  }

  bool get _codeRequired => _trust == _TrustState.needsCode;
  bool get _showCode =>
      _trust != _TrustState.certMismatch &&
      (_codeRequired || _showCodeWhenTrusted);

  Future<void> _forgetChangedServer() async {
    final serverId = _detected?.serverId;
    if (serverId == null) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Forget changed server?'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text(
              'The certificate on this server no longer matches the one this '
              'phone trusted. Remove the saved server and pair again with a '
              'new access code.',
            ),
            const SizedBox(height: 24),
            Row(
              children: [
                Expanded(
                  child: OutlinedButton(
                    onPressed: () => Navigator.of(context).pop(false),
                    child: const Text('Cancel'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: FilledButton(
                    onPressed: () => Navigator.of(context).pop(true),
                    style: FilledButton.styleFrom(
                      backgroundColor: AppColors.danger,
                      foregroundColor: Colors.black,
                    ),
                    child: const Text('Forget'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
    if (confirmed != true || !mounted) return;
    await context.read<AuthService>().removeServerGroup(serverId);
    if (mounted) await _refreshTrust();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    final url = _baseUrlController.text.trim();
    final auth = context.read<AuthService>();
    final code = _codeController.text.trim();

    setState(() => _submitting = true);
    try {
      if (code.isEmpty) {
        if (_trust == _TrustState.certMismatch) {
          throw const ApiException(
            0,
            'This server certificate changed. Forget the saved server and '
            'pair again with a new access code.',
          );
        }
        if (_trust != _TrustState.trusted) {
          throw const ApiException(
            0,
            'This server is not trusted yet. Enter an access code from its admin.',
          );
        }
        final cert = _detected?.certSha256;
        if (cert == null) {
          throw const ApiException(0, 'Could not verify the server certificate');
        }
        await auth.login(
          baseUrl: url,
          certSha256: cert,
          username: _usernameController.text.trim(),
          password: _passwordController.text,
        );
      } else {
        // One code path for both "create account" and "pair existing account".
        await auth.redeemAccessCode(
          baseUrl: url,
          code: code,
          username: _usernameController.text.trim(),
          password: _passwordController.text,
        );
      }
      if (mounted) {
        Navigator.of(context).popUntil((route) => route.isFirst);
      }
    } on HandshakeException {
      if (mounted) setState(() => _trust = _TrustState.needsCode);
      _show('Server certificate changed. Enter a new access code from its admin.');
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
                  _TrustRow(state: _trust, detected: _detected),
                  if (_trust == _TrustState.certMismatch)
                    Align(
                      alignment: Alignment.centerLeft,
                      child: TextButton(
                        onPressed: _submitting ? null : _forgetChangedServer,
                        child: const Text('Forget changed server'),
                      ),
                    ),
                  if (_showCode) ...[
                    const SizedBox(height: 12),
                    TextFormField(
                      controller: _codeController,
                      keyboardType: TextInputType.text,
                      autocorrect: false,
                      textCapitalization: TextCapitalization.characters,
                      maxLength: 24,
                      onChanged: (_) => setState(() {}),
                      decoration: InputDecoration(
                        labelText: 'Access code',
                        helperText: _codeRequired
                            ? 'Ask the server admin for a one-time access code.'
                            : 'Optional: use a code to create another account.',
                        prefixIcon: const Icon(Icons.key_outlined),
                        counterText: '',
                      ),
                      validator: (v) {
                        if (_codeRequired && (v == null || v.trim().isEmpty)) {
                          return 'Access code is required for a new server';
                        }
                        return null;
                      },
                    ),
                  ] else
                    Align(
                      alignment: Alignment.centerLeft,
                      child: TextButton(
                        onPressed: _submitting
                            ? null
                            : () => setState(() => _showCodeWhenTrusted = true),
                        child: const Text('Have an access code?'),
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
                    onPressed: _submitting ||
                            _trust == _TrustState.checking ||
                            _trust == _TrustState.certMismatch
                        ? null
                        : _submit,
                    child: _submitting
                        ? const SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(strokeWidth: 2))
                        : Text(_codeController.text.trim().isEmpty
                            ? 'Log in'
                            : 'Continue'),
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

class _TrustRow extends StatelessWidget {
  final _TrustState state;
  final TrustedServer? detected;

  const _TrustRow({required this.state, this.detected});

  @override
  Widget build(BuildContext context) {
    if (state == _TrustState.checking) {
      return const Row(
        children: [
          SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          SizedBox(width: 8),
          Text('Checking certificate...', style: TextStyle(fontSize: 12)),
        ],
      );
    }
    if (state == _TrustState.trusted) {
      final name = detected?.name;
      return Row(
        children: [
          const Icon(Icons.verified_outlined, size: 18, color: AppColors.accent),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              name == null || name.isEmpty
                  ? 'Certificate pinned'
                  : 'Known server: $name',
              style: const TextStyle(color: AppColors.accent, fontSize: 12),
            ),
          ),
        ],
      );
    }
    if (state == _TrustState.certMismatch) {
      return const Row(
        children: [
          Icon(Icons.gpp_bad_outlined, size: 18, color: AppColors.danger),
          SizedBox(width: 8),
          Expanded(
            child: Text(
              'This server certificate changed. Forget the saved server and '
              'pair again with a new access code.',
              style: TextStyle(color: AppColors.danger, fontSize: 12),
            ),
          ),
        ],
      );
    }
    return const Row(
      children: [
        Icon(Icons.key_outlined, size: 18, color: AppColors.textMuted),
        SizedBox(width: 8),
        Expanded(
          child: Text(
            'This server is not trusted yet; an access code is required.',
            style: TextStyle(fontSize: 12),
          ),
        ),
      ],
    );
  }
}

