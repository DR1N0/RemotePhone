import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../models/server_profile.dart';
import '../models/user.dart';
import 'api_client.dart';

enum AuthStatus { unknown, unauthenticated, serverSelection, authenticated }

class TrustedServer {
  final String baseUrl;
  final String? certSha256;
  final String? serverId;
  final String? name;
  final bool trusted;
  final bool certMismatch;

  const TrustedServer({
    required this.baseUrl,
    required this.trusted,
    this.certSha256,
    this.serverId,
    this.name,
    this.certMismatch = false,
  });
}

/// Holds the paired servers remembered by this client, plus the currently
/// selected server's API client. The certificate pin lives in each
/// ServerProfile, keyed by the server's stable UUID; there is no separate
/// URL pin or server-pin store.
class AuthService extends ChangeNotifier {
  static const _storage = FlutterSecureStorage();
  static const _keyServers = 'mako_servers';

  List<ServerProfile> servers = [];
  ServerProfile? activeProfile;
  String? baseUrl;
  User? currentUser;
  AuthStatus status = AuthStatus.unknown;
  ApiClient? _client;

  ApiClient get client {
    final c = _client;
    if (c == null) {
      throw StateError('AuthService.client read before a server is selected');
    }
    return c;
  }

  /// Resolves whether [baseUrl] is already trusted. The URL is only an
  /// address. The client fetches the unauthenticated server ID over the
  /// connection, then looks up the profiles for that ID and trusts the
  /// server only if the certificate observed on THIS connection equals the
  /// certificate pinned in those profiles. A spoofed server ID cannot bypass
  /// the certificate comparison.
  Future<TrustedServer> detectTrust(String baseUrl) async {
    final temp = ApiClient(baseUrl: baseUrl, allowAnyCertificate: true);
    try {
      final info = await temp.serverInfo();
      final observed = temp.observedCertSha256;
      if (observed == null) {
        return TrustedServer(baseUrl: baseUrl, trusted: false);
      }
      final known = servers
          .where((s) => s.serverId == info.serverId)
          .toList();
      if (known.isEmpty) {
        return TrustedServer(
          baseUrl: baseUrl,
          trusted: false,
          certSha256: observed,
          serverId: info.serverId,
          name: info.name,
        );
      }
      final matches = known.every((s) => s.certSha256 == observed);
      return TrustedServer(
        baseUrl: baseUrl,
        trusted: matches,
        certMismatch: !matches,
        certSha256: observed,
        serverId: info.serverId,
        name: info.name,
      );
    } on ApiException {
      return TrustedServer(baseUrl: baseUrl, trusted: false);
    } catch (_) {
      return TrustedServer(baseUrl: baseUrl, trusted: false);
    } finally {
      temp.close();
    }
  }

  /// The pre-release URL-pin/server-pin stores are gone. Remove leftovers so
  /// they cannot be mistaken for a second source of trust later.
  Future<void> _removeLegacyPinStores() async {
    try {
      final all = await _storage.readAll();
      for (final key in all.keys) {
        if (key.startsWith('mako_pin_') ||
            key.startsWith('mako_server_pin_')) {
          await _storage.delete(key: key);
        }
      }
    } catch (_) {
      // Best-effort cleanup; trust no longer reads these keys.
    }
  }

  /// Loads remembered servers. The app deliberately lands on the server
  /// picker instead of silently reconnecting to the last-used server.
  Future<void> restore() async {
    await _removeLegacyPinStores();
    final raw = await _storage.read(key: _keyServers);
    if (raw != null && raw.isNotEmpty) {
      try {
        servers = ServerProfile.listFromJson(raw);
      } catch (_) {
        servers = [];
      }
    }
    status =
        servers.isEmpty ? AuthStatus.unauthenticated : AuthStatus.serverSelection;
    notifyListeners();
  }

  Future<void> login({
    required String baseUrl,
    required String certSha256,
    required String username,
    required String password,
  }) async {
    final temp = ApiClient(baseUrl: baseUrl, pinnedCertSha256: certSha256);
    try {
      final (token, user) = await temp.login(
        username: username,
        password: password,
        deviceLabel: _deviceLabel(),
      );
      final info = await _readServerInfo(temp, baseUrl);
      final profile = ServerProfile(
        serverId: info.serverId,
        name: info.name,
        baseUrl: baseUrl,
        certSha256: certSha256,
        token: token,
        userId: user.id,
        username: username,
        kind: ServerProfile.kindForUrl(baseUrl),
      );
      await _upsert(profile);
      await _activate(profile, user);
    } finally {
      temp.close();
    }
  }

  /// Redeems an access code. The server decides whether this creates a new
  /// ordinary account or pairs this device to an existing account.
  Future<bool> redeemAccessCode({
    required String baseUrl,
    required String code,
    required String username,
    required String password,
  }) async {
    final temp = ApiClient(baseUrl: baseUrl, allowAnyCertificate: true);
    try {
      final result = await temp.redeemAccessCode(
        code: code,
        username: username,
        password: password,
        deviceLabel: _deviceLabel(),
      );
      var serverId = result.serverId;
      var serverName = result.serverName;
      if (serverId.isEmpty) {
        final info = await _readServerInfo(temp, baseUrl);
        serverId = info.serverId;
        serverName = info.name;
      }
      final existing = servers
          .where((s) => s.serverId == serverId)
          .toList();
      if (existing.any((s) => s.certSha256 != result.certSha256)) {
        throw const ApiException(
          0,
          'This server certificate changed. Forget the server first, then '
          're-add it with a new access code.',
        );
      }
      final profile = ServerProfile(
        serverId: serverId,
        name: serverName.isEmpty ? Uri.parse(baseUrl).host : serverName,
        baseUrl: baseUrl,
        certSha256: result.certSha256,
        token: result.token,
        userId: result.user.id,
        username: username,
        kind: ServerProfile.kindForUrl(baseUrl),
      );
      await _upsert(profile);
      await _activate(profile, result.user);
      return result.created;
    } finally {
      temp.close();
    }
  }

  Future<AccessCode> createAccessCode() => client.createAccessCode();

  Future<AccessCode> createAccessCodeFor(ServerProfile profile) async {
    final temp = ApiClient(
      baseUrl: profile.baseUrl,
      token: profile.token,
      pinnedCertSha256: profile.certSha256,
    );
    try {
      return await temp.createAccessCode();
    } finally {
      temp.close();
    }
  }

  /// Selects a remembered server, validates its token, and refreshes the
  /// display name from the server's identity endpoint.
  Future<void> selectServer(ServerProfile profile) async {
    final client = ApiClient(
      baseUrl: profile.baseUrl,
      token: profile.token,
      pinnedCertSha256: profile.certSha256,
    );
    final user = await client.me();
    if (user == null) {
      client.close();
      throw const ApiException(401, 'server rejected the saved session');
    }
    var refreshed = profile;
    try {
      final info = await client.serverInfo();
      refreshed = profile.copyWith(
        serverId: info.serverId,
        name: info.name,
      );
      await _upsert(refreshed);
    } on ApiException {
      // Older server without /server/info: keep the stored profile.
    }
    _client?.close();
    _client = client;
    activeProfile = refreshed;
    baseUrl = refreshed.baseUrl;
    currentUser = user;
    status = AuthStatus.authenticated;
    notifyListeners();
  }

  Future<void> _activate(ServerProfile profile, User user) async {
    _client?.close();
    _client = ApiClient(
      baseUrl: profile.baseUrl,
      token: profile.token,
      pinnedCertSha256: profile.certSha256,
    );
    activeProfile = profile;
    baseUrl = profile.baseUrl;
    currentUser = user;
    status = AuthStatus.authenticated;
    notifyListeners();
  }

  Future<ServerInfo> _readServerInfo(ApiClient client, String baseUrl) async {
    try {
      return await client.serverInfo();
    } on ApiException {
      // Compatibility with a server predating /server/info.
      return ServerInfo(
        serverId: baseUrl,
        name: Uri.parse(baseUrl).host,
        version: '',
      );
    }
  }

  Future<void> _upsert(ServerProfile profile) async {
    final index = servers.indexWhere(
      (s) =>
          s.serverId == profile.serverId &&
          (s.accountKey == profile.accountKey ||
              s.username == profile.username),
    );
    if (index >= 0) {
      servers[index] = profile;
    } else {
      servers.add(profile);
    }
    await _saveServers();
  }

  Future<void> _saveServers() =>
      _storage.write(key: _keyServers, value: ServerProfile.listToJson(servers));

  /// Updates a LAN profile's cached address and display name after mDNS
  /// discovery, without changing tokens or certificate pins. All accounts for
  /// the same server move together.
  Future<void> updateDiscoveredServer(
    String serverId,
    String newBaseUrl,
    String newName,
  ) async {
    var changed = false;
    for (var i = 0; i < servers.length; i++) {
      if (servers[i].serverId != serverId) continue;
      final updated = servers[i].copyWith(baseUrl: newBaseUrl, name: newName);
      if (updated.baseUrl != servers[i].baseUrl ||
          updated.name != servers[i].name) {
        servers[i] = updated;
        changed = true;
      }
    }
    if (!changed) return;
    await _saveServers();
    if (activeProfile?.serverId == serverId) {
      activeProfile = activeProfile!.copyWith(
        baseUrl: newBaseUrl,
        name: newName,
      );
      baseUrl = newBaseUrl;
    }
    notifyListeners();
  }

  /// Updates a LAN profile's cached address without changing its identity,
  /// token, or certificate pin. This is the manual fallback until mDNS
  /// discovery can rewrite the address by server ID.
  Future<void> updateServerAddress(ServerProfile profile, String newBaseUrl) async {
    for (var i = 0; i < servers.length; i++) {
      if (servers[i].serverId == profile.serverId) {
        servers[i] = servers[i].copyWith(baseUrl: newBaseUrl);
      }
    }
    await _saveServers();
    if (activeProfile?.serverId == profile.serverId) {
      activeProfile = activeProfile!.copyWith(baseUrl: newBaseUrl);
      baseUrl = newBaseUrl;
      notifyListeners();
    }
  }

  /// Forgets every server and session. The certificate pin lives only in the
  /// profile, so this removes the pin too; re-adding any server needs a fresh
  /// access code.
  Future<void> forgetAllServers() async {
    servers = [];
    await _storage.delete(key: _keyServers);
    await logout();
  }

  /// Leaves the selected server but keeps it (and its token) in the picker.
  Future<void> logout() async {
    _client?.close();
    _client = null;
    activeProfile = null;
    baseUrl = null;
    currentUser = null;
    status =
        servers.isEmpty ? AuthStatus.unauthenticated : AuthStatus.serverSelection;
    notifyListeners();
  }

  /// Forgets a server and every account saved for it. The certificate pin is
  /// stored only in those profiles, so re-adding needs a fresh access code.
  Future<void> removeServerGroup(String serverId) async {
    servers.removeWhere((s) => s.serverId == serverId);
    await _saveServers();
    if (activeProfile?.serverId == serverId) {
      await logout();
    } else {
      notifyListeners();
    }
  }

  /// Forgets one account while keeping any other accounts for the same server.
  Future<void> removeServer(ServerProfile profile) async {
    servers.removeWhere(
      (s) =>
          s.serverId == profile.serverId &&
          s.accountKey == profile.accountKey,
    );
    await _saveServers();
    if (activeProfile?.serverId == profile.serverId &&
        activeProfile?.accountKey == profile.accountKey) {
      await logout();
    } else {
      notifyListeners();
    }
  }

  String _deviceLabel() => 'mako-client-android';
}
