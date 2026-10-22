import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../models/user.dart';
import 'api_client.dart';

enum AuthStatus { unknown, authenticated, unauthenticated }

/// Holds the current server/credential state for the whole app. Exactly
/// one ApiClient exists at a time, rebuilt whenever the server URL or
/// token changes — screens read it via `context.read<AuthService>().client`.
class AuthService extends ChangeNotifier {
  static const _storage = FlutterSecureStorage();
  static const _keyBaseUrl = 'mako_base_url';
  static const _keyToken = 'mako_token';
  static const _keyPinPrefix = 'mako_pin_';

  String _pinKey(String baseUrl) => '$_keyPinPrefix$baseUrl';

  Future<String?> pinnedFingerprint(String baseUrl) =>
      _storage.read(key: _pinKey(baseUrl));

  Future<void> savePinnedFingerprint(String baseUrl, String fingerprint) =>
      _storage.write(key: _pinKey(baseUrl), value: fingerprint);

  Future<void> clearPinnedFingerprint(String baseUrl) =>
      _storage.delete(key: _pinKey(baseUrl));

  /// PIN-pairs [baseUrl] and persists the certificate fingerprint. The
  /// temporary client trusts the self-signed certificate only for this call;
  /// the returned fingerprint is what all later clients pin.
  Future<String> pair({
    required String baseUrl,
    required String pin,
  }) async {
    final temp = ApiClient(baseUrl: baseUrl, allowAnyCertificate: true);
    try {
      final fingerprint = await temp.verifyPairing(pin);
      await savePinnedFingerprint(baseUrl, fingerprint);
      return fingerprint;
    } finally {
      temp.close();
    }
  }

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
    final pin = await pinnedFingerprint(storedUrl);
    if (pin == null) {
      status = AuthStatus.unauthenticated;
      notifyListeners();
      return;
    }
    final client = ApiClient(
      baseUrl: storedUrl,
      token: storedToken,
      pinnedCertSha256: pin,
    );
    try {
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
    } catch (_) {
      client.close();
      await logout();
    }
  }

  Future<void> login({
    required String baseUrl,
    required String username,
    required String password,
  }) async {
    final pin = await pinnedFingerprint(baseUrl);
    if (pin == null) {
      throw StateError('pair this server before logging in');
    }
    final temp = ApiClient(baseUrl: baseUrl, pinnedCertSha256: pin);
    final (token, user) = await temp.login(
      username: username,
      password: password,
      deviceLabel: _deviceLabel(),
    );
    temp.close();
    await _activate(baseUrl: baseUrl, token: token, user: user, pin: pin);
  }

  Future<void> register({
    required String baseUrl,
    required String inviteToken,
    required String username,
    required String password,
  }) async {
    final pin = await pinnedFingerprint(baseUrl);
    if (pin == null) {
      throw StateError('pair this server before registering');
    }
    final temp = ApiClient(baseUrl: baseUrl, pinnedCertSha256: pin);
    final (token, user) = await temp.register(
      inviteToken: inviteToken,
      username: username,
      password: password,
      deviceLabel: _deviceLabel(),
    );
    temp.close();
    await _activate(baseUrl: baseUrl, token: token, user: user, pin: pin);
  }

  Future<void> _activate({
    required String baseUrl,
    required String token,
    required User user,
    required String pin,
  }) async {
    await _storage.write(key: _keyBaseUrl, value: baseUrl);
    await _storage.write(key: _keyToken, value: token);
    _client?.close();
    this.baseUrl = baseUrl;
    _client = ApiClient(
      baseUrl: baseUrl,
      token: token,
      pinnedCertSha256: pin,
    );
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

  String _deviceLabel() => 'mako-client-android';
}
