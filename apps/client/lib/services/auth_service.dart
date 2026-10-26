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

  const TrustedServer({
    required this.baseUrl,
    required this.trusted,
    this.certSha256,
    this.serverId,
    this.name,
  });
}

/// Holds the paired servers remembered by this client, plus the currently
/// selected server's API client. A client can pair with several control
/// planes; each profile keeps its own certificate pin and bearer token.
class AuthService extends ChangeNotifier {
  static const _storage = FlutterSecureStorage();
  static const _keyServers = 'mako_servers';
  static const _keyPinPrefix = 'mako_pin_';
  static const _keyServerPinPrefix = 'mako_server_pin_';

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

  String _pinKey(String baseUrl) => '$_keyPinPrefix$baseUrl';
  String _serverPinKey(String serverId) => '$_keyServerPinPrefix$serverId';

  Future<String?> pinnedFingerprint(String baseUrl) =>
      _storage.read(key: _pinKey(baseUrl));

  Future<String?> serverPinnedFingerprint(String serverId) =>
      _storage.read(key: _serverPinKey(serverId));

  Future<void> savePinnedFingerprint(String baseUrl, String fingerprint) =>
      _storage.write(key: _pinKey(baseUrl), value: fingerprint);

  Future<void> saveServerPinnedFingerprint(
    String serverId,
    String fingerprint,
  ) async {
    if (serverId.isEmpty) return;
    await _storage.write(key: _serverPinKey(serverId), value: fingerprint);
  }

  Future<void> clearPinnedFingerprint(String baseUrl) =>
      _storage.delete(key: _pinKey(baseUrl));

  /// Resolves whether [baseUrl] is already trusted. An exact URL pin is the
  /// fast path. Otherwise the server is identified by its unauthenticated
  /// server ID, but trust only succeeds if the certificate observed on THIS
  /// connection matches a pin stored for that server ID. An attacker can
  /// spoof the server ID, but cannot present the pinned certificate.
  Future<TrustedServer> detectTrust(String baseUrl) async {
    final exact = await pinnedFingerprint(baseUrl);
    if (exact != null) {
      return TrustedServer(baseUrl: baseUrl, trusted: true, certSha256: exact);
    }
    final temp = ApiClient(baseUrl: baseUrl, allowAnyCertificate: true);
    try {
      final info = await temp.serverInfo();
      final observed = temp.observedCertSha256;
      if (observed != null) {
        final stored = await serverPinnedFingerprint(info.serverId);
        final known = servers
            .where((s) => s.serverId == info.serverId)
            .toList();
        final match = stored ?? (known.isEmpty ? null : known.first.certSha256);
        if (match != null && match == observed) {
          await savePinnedFingerprint(baseUrl, observed);
          await saveServerPinnedFingerprint(info.serverId, observed);
          if (known.isNotEmpty) {
            await updateDiscoveredServer(info.serverId, baseUrl, info.name);
          }
          return TrustedServer(
            baseUrl: baseUrl,
            trusted: true,
            certSha256: observed,
            serverId: info.serverId,
            name: info.name,
          );
        }
      }
      return TrustedServer(
        baseUrl: baseUrl,
        trusted: false,
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

  /// Loads remembered servers. The app deliberately lands on the server
  /// picker instead of silently reconnecting to the last-used server.
  Future<void> restore() async {
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
    required String username,
    required String password,
  }) async {
    final pin = await pinnedFingerprint(baseUrl);
    if (pin == null) {
      throw StateError('pair this server before logging in');
    }
    final temp = ApiClient(baseUrl: baseUrl, pinnedCertSha256: pin);
    try {
      final (token, user) = await temp.login(
        username: username,
        password: password,
        deviceLabel: _deviceLabel(),
      );
      final info = await _readServerInfo(temp, baseUrl);
      await saveServerPinnedFingerprint(info.serverId, pin);
      final profile = ServerProfile(
        serverId: info.serverId,
        name: info.name,
        baseUrl: baseUrl,
        certSha256: pin,
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
      await savePinnedFingerprint(baseUrl, result.certSha256);
      var serverId = result.serverId;
      var serverName = result.serverName;
      if (serverId.isEmpty) {
        final info = await _readServerInfo(temp, baseUrl);
        serverId = info.serverId;
        serverName = info.name;
      }
      await saveServerPinnedFingerprint(serverId, result.certSha256);
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
    await saveServerPinnedFingerprint(
      refreshed.serverId,
      refreshed.certSha256,
    );
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

  /// Forgets every server and session, but deliberately keeps certificate
  /// pins: forgetting an account is not the same as distrusting the host.
  /// Re-adding the same URL therefore only needs credentials, not a new
  /// pairing code. Clearing all trust is an explicit app-data reset.
  Future<void> forgetAllServers() async {
    for (final profile in servers) {
      await saveServerPinnedFingerprint(profile.serverId, profile.certSha256);
    }
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

  /// Forgets a server and every account saved for it. Certificate pins are
  /// retained, so re-adding the same URL does not require another pairing
  /// code; a changed certificate will still be rejected by the pin.
  Future<void> removeServerGroup(String serverId) async {
    for (final profile in servers.where((s) => s.serverId == serverId)) {
      await saveServerPinnedFingerprint(profile.serverId, profile.certSha256);
    }
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
    await saveServerPinnedFingerprint(profile.serverId, profile.certSha256);
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
