import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../models/user.dart';
import 'api_client.dart';

enum AuthStatus { unknown, authenticated, unauthenticated }

/// Holds the current server/credential state for the whole app. Exactly
/// one ApiClient exists at a time, rebuilt whenever the server URL or
/// token changes — screens read it via `context.read<AuthService>().client`.
class AuthService extends ChangeNotifier {
  // usesDataProtectionKeychain: false — the modern "data protection"
  // Keychain API macOS defaults to requires a keychain-access-groups
  // entitlement under App Sandbox, which in turn requires signing with a
  // real Apple Developer Team (errSecMissingEntitlement / -34018 without
  // one). This is an unsigned local dev/MVP build with App Sandbox off
  // (see macos/Runner/DebugProfile.entitlements), so the legacy Keychain
  // API is used instead — it has no such requirement.
  static const _storage = FlutterSecureStorage(
    mOptions: MacOsOptions(usesDataProtectionKeychain: false),
  );
  static const _keyBaseUrl = 'mako_base_url';
  static const _keyToken = 'mako_token';

  String? baseUrl;
  User? currentUser;
  AuthStatus status = AuthStatus.unknown;
  ApiClient? _client;

  ApiClient get client {
    final c = _client;
    if (c == null) {
      throw StateError('AuthService.client read before a session exists');
    }
    return c;
  }

  /// Loads a persisted server URL + token (if any) and validates it against
  /// GET /auth/me before deciding whether the app should open straight to
  /// the instance list or show the login screen.
  Future<void> restore() async {
    final storedUrl = await _storage.read(key: _keyBaseUrl);
    final storedToken = await _storage.read(key: _keyToken);
    if (storedUrl == null || storedToken == null) {
      status = AuthStatus.unauthenticated;
      notifyListeners();
      return;
    }
    final client = ApiClient(baseUrl: storedUrl, token: storedToken);
    final user = await client.me();
    if (user == null) {
      client.close();
      await logout();
      return;
    }
    baseUrl = storedUrl;
    _client = client;
    currentUser = user;
    status = AuthStatus.authenticated;
    notifyListeners();
  }

  Future<void> login({
    required String baseUrl,
    required String username,
    required String password,
  }) async {
    final temp = ApiClient(baseUrl: baseUrl);
    final (token, user) = await temp.login(
      username: username,
      password: password,
      deviceLabel: _deviceLabel(),
    );
    temp.close();
    await _activate(baseUrl: baseUrl, token: token, user: user);
  }

  Future<void> register({
    required String baseUrl,
    required String inviteToken,
    required String username,
    required String password,
  }) async {
    final temp = ApiClient(baseUrl: baseUrl);
    final (token, user) = await temp.register(
      inviteToken: inviteToken,
      username: username,
      password: password,
      deviceLabel: _deviceLabel(),
    );
    temp.close();
    await _activate(baseUrl: baseUrl, token: token, user: user);
  }

  Future<void> _activate({
    required String baseUrl,
    required String token,
    required User user,
  }) async {
    await _storage.write(key: _keyBaseUrl, value: baseUrl);
    await _storage.write(key: _keyToken, value: token);
    _client?.close();
    this.baseUrl = baseUrl;
    _client = ApiClient(baseUrl: baseUrl, token: token);
    currentUser = user;
    status = AuthStatus.authenticated;
    notifyListeners();
  }

  Future<void> logout() async {
    await _storage.delete(key: _keyBaseUrl);
    await _storage.delete(key: _keyToken);
    _client?.close();
    _client = null;
    baseUrl = null;
    currentUser = null;
    status = AuthStatus.unauthenticated;
    notifyListeners();
  }

  String _deviceLabel() => 'mako-client (${Platform.operatingSystem})';
}
