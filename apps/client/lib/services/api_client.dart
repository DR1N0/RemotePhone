import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';

import '../models/managed_instance.dart';
import '../models/session_record.dart';
import '../models/template_info.dart';
import '../models/user.dart';

class ApiException implements Exception {
  final int statusCode;
  final String message;
  const ApiException(this.statusCode, this.message);

  @override
  String toString() => 'ApiException($statusCode): $message';
}

class ServerInfo {
  final String serverId;
  final String name;
  final String version;

  const ServerInfo({
    required this.serverId,
    required this.name,
    required this.version,
  });

  factory ServerInfo.fromJson(Map<String, dynamic> json) {
    return ServerInfo(
      serverId: json['serverId'] as String,
      name: json['name'] as String? ?? 'remotephone',
      version: json['version'] as String? ?? '',
    );
  }
}

class AccessCodeResult {
  final bool created;
  final String token;
  final User user;
  final String certSha256;
  final String serverId;
  final String serverName;

  const AccessCodeResult({
    required this.created,
    required this.token,
    required this.user,
    required this.certSha256,
    required this.serverId,
    required this.serverName,
  });
}

class AccessCode {
  final String code;
  final DateTime expiresAt;
  final String scope;

  const AccessCode({
    required this.code,
    required this.expiresAt,
    required this.scope,
  });
}

/// Talks to remotephone-controlplane's client HTTP surface. One client per
/// configured server; construct a fresh one whenever the server URL or
/// bearer token changes (see AuthService).
class ApiClient {
  final String baseUrl;
  final String? token;

  /// SHA-256 (lowercase hex) of the server certificate DER, established by
  /// PIN pairing. Null is only valid for the temporary pairing client.
  final String? pinnedCertSha256;
  final bool allowAnyCertificate;

  late final http.Client _client;
  String? _observedCertSha256;

  ApiClient({
    required this.baseUrl,
    this.token,
    this.pinnedCertSha256,
    this.allowAnyCertificate = false,
  }) {
    _client = IOClient(_httpClient());
  }

  String? get observedCertSha256 => _observedCertSha256;

  HttpClient _httpClient() {
    final client = HttpClient();
    client.badCertificateCallback = (cert, host, port) {
      final fingerprint = sha256.convert(cert.der).toString();
      _observedCertSha256 = fingerprint;
      if (allowAnyCertificate) return true;
      return pinnedCertSha256 != null && fingerprint == pinnedCertSha256;
    };
    return client;
  }

  Uri _uri(String path) => Uri.parse('$baseUrl$path');

  Map<String, String> _headers({String? overrideToken}) {
    final effectiveToken = overrideToken ?? token;
    return {
      'Content-Type': 'application/json',
      if (effectiveToken != null) 'Authorization': 'Bearer $effectiveToken',
    };
  }

  Map<String, dynamic> _decode(http.Response resp) {
    final body = resp.body.isEmpty
        ? <String, dynamic>{}
        : jsonDecode(resp.body) as Map<String, dynamic>;
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw ApiException(
        resp.statusCode,
        body['error']?.toString() ?? 'request failed (${resp.statusCode})',
      );
    }
    return body;
  }

  Future<Map<String, dynamic>> _post(
    String path,
    Map<String, dynamic> body, {
    String? overrideToken,
  }) async {
    final resp = await _client.post(
      _uri(path),
      headers: _headers(overrideToken: overrideToken),
      body: jsonEncode(body),
    );
    return _decode(resp);
  }

  Future<Map<String, dynamic>> _get(String path) async {
    final resp = await _client.get(_uri(path), headers: _headers());
    return _decode(resp);
  }

  // --- Auth ---

  /// Redeems a unified access code. A code can create a new ordinary account
  /// or add this device to an existing account; the server decides which.
  Future<AccessCodeResult> redeemAccessCode({
    required String code,
    required String username,
    required String password,
    required String deviceLabel,
  }) async {
    if (!allowAnyCertificate) {
      throw StateError('redeemAccessCode requires allowAnyCertificate');
    }
    final json = await _post('/api/v1/access-codes/redeem', {
      'code': code,
      'username': username,
      'password': password,
      'deviceLabel': deviceLabel,
    });
    final reported = json['certSha256']?.toString();
    final observed = _observedCertSha256;
    if (reported == null || observed == null || reported != observed) {
      throw const ApiException(
        0,
        'server certificate fingerprint mismatch during access-code redemption',
      );
    }
    return AccessCodeResult(
      created: json['created'] == true,
      token: json['token'] as String,
      user: User.fromJson(json['user'] as Map<String, dynamic>),
      certSha256: observed,
      serverId: json['serverId']?.toString() ?? '',
      serverName: json['serverName']?.toString() ?? '',
    );
  }

  /// Creates a self code for an ordinary user, or an unbound invitation code
  /// for an admin. The server decides from the caller's role.
  Future<AccessCode> createAccessCode() async {
    final json = await _post('/api/v1/access-codes', {});
    return AccessCode(
      code: json['code'] as String,
      expiresAt: DateTime.parse(json['expiresAt'] as String),
      scope: json['scope']?.toString() ?? 'self',
    );
  }

  Future<ServerInfo> serverInfo() async {
    final json = await _get('/api/v1/server/info');
    return ServerInfo.fromJson(json);
  }

  Future<(String, User)> login({
    required String username,
    required String password,
    required String deviceLabel,
  }) async {
    final json = await _post('/api/v1/auth/login', {
      'username': username,
      'password': password,
      'deviceLabel': deviceLabel,
    });
    return (
      json['token'] as String,
      User.fromJson(json['user'] as Map<String, dynamic>),
    );
  }

  /// Returns null if token is missing/invalid/revoked — never throws for
  /// that case, since AuthService uses this purely to decide whether a
  /// persisted token is still good.
  Future<User?> me() async {
    try {
      final json = await _get('/api/v1/auth/me');
      return User.fromJson(json['user'] as Map<String, dynamic>);
    } on ApiException {
      return null;
    }
  }

  // --- Instances ---

  Future<List<ManagedInstance>> listInstances() async {
    final json = await _get('/api/v1/instances');
    return (json['instances'] as List<dynamic>)
        .map((e) => ManagedInstance.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Lists the AVDs available on the server host, for the "new instance"
  /// template picker. The server discovers these by spawning a short-lived
  /// worker, so this call can take a second or two on a cold host.
  Future<List<TemplateInfo>> listTemplates() async {
    final json = await _get('/api/v1/templates');
    return (json['templates'] as List<dynamic>)
        .map((e) => TemplateInfo.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// resourceProfile/launchConfig are fixed MVP defaults — no UI for them
  /// yet, matching the "lightweight MVP" scope (see docs/plans in the Go
  /// backend repo for the full field set this mirrors).
  Future<ManagedInstance> createInstance({
    required String name,
    required String templateId,
  }) async {
    final json = await _post('/api/v1/instances', {
      'name': name,
      'templateId': templateId,
      'placement': {'kind': 'local-only'},
      'resourceProfile': {
        'cpuMillicores': 2000,
        'memoryMb': 4096,
        'diskMb': 8192,
        'accelerationMode': 'auto',
        'streamProfile': 'balanced',
      },
      // One worker owns one instance; the worker enforces headless mode
      // (REMOTEPHONE_HEADLESS=0 is a debug override). This value is only the
      // client's preference and is not what normally decides the launch.
      'launchConfig': {'headless': true},
    });
    return ManagedInstance.fromJson(json['instance'] as Map<String, dynamic>);
  }

  Future<ManagedInstance> startInstance(String id) async {
    final json = await _post('/api/v1/instances/$id/start', {});
    return ManagedInstance.fromJson(json['instance'] as Map<String, dynamic>);
  }

  Future<ManagedInstance> stopInstance(String id) async {
    final json = await _post('/api/v1/instances/$id/stop', {});
    return ManagedInstance.fromJson(json['instance'] as Map<String, dynamic>);
  }

  Future<void> deleteInstance(String id) async {
    final resp =
        await _client.delete(_uri('/api/v1/instances/$id'), headers: _headers());
    if (resp.statusCode != 204) {
      _decode(resp); // throws with the server's error message
    }
  }

  // --- Sessions ---

  /// attachmentId is returned explicitly rather than left for the caller to
  /// infer from session.ownerAttachment: the server may hand back either a
  /// fresh/reclaimed owner attachment or a new participant attachment (if
  /// the owner is already connected elsewhere) when joining an existing
  /// session instead of hard-failing with a 409 — see the Go server's
  /// session.Service.CreateSession.
  Future<(SessionRecord, String, String)> createSession({
    required String instanceId,
    required String ownerClientId,
  }) async {
    final json = await _post('/api/v1/sessions', {
      'instanceId': instanceId,
      'ownerClientId': ownerClientId,
    });
    return (
      SessionRecord.fromJson(json['session'] as Map<String, dynamic>),
      json['attachmentId'] as String,
      json['attachmentToken'] as String,
    );
  }

  /// Rotates and returns a fresh attachment token, marking the attachment
  /// connected again and (for the owner attachment) clearing the
  /// owner-absence deadline — lets a reconnect resume the same session
  /// instead of hitting 409 Conflict from createSession (session/service.go:
  /// disconnectAttachment only starts a grace-period timer, it doesn't
  /// close the session).
  Future<String> resumeAttachment({
    required String attachmentToken,
    required String sessionId,
    required String attachmentId,
  }) async {
    final json = await _post(
      '/api/v1/sessions/$sessionId/attachments/$attachmentId/resume',
      {},
      overrideToken: attachmentToken,
    );
    return json['attachmentToken'] as String;
  }

  /// Uses the attachment's own token — the disconnect route accepts
  /// either the owning account or the attachment itself
  /// (clientapi/sessions.go), and the stream screen only ever holds the
  /// attachment token.
  Future<void> disconnectAttachment({
    required String attachmentToken,
    required String sessionId,
    required String attachmentId,
  }) async {
    await _post(
      '/api/v1/sessions/$sessionId/attachments/$attachmentId/disconnect',
      {},
      overrideToken: attachmentToken,
    );
  }

  // --- Signaling (attachment-scoped) ---

  Future<String> signalSession({
    required String attachmentToken,
    required String sessionId,
    required String attachmentId,
    required String sdpOffer,
  }) async {
    final json = await _post(
      '/api/v1/signaling/session',
      {
        'sessionId': sessionId,
        'attachmentId': attachmentId,
        'sdpOffer': sdpOffer,
      },
      overrideToken: attachmentToken,
    );
    return json['sdpAnswer'] as String;
  }

  Future<void> signalIceCandidate({
    required String attachmentToken,
    required String sessionId,
    required String attachmentId,
    required String candidate,
  }) async {
    await _post(
      '/api/v1/signaling/ice-candidate',
      {
        'sessionId': sessionId,
        'attachmentId': attachmentId,
        'candidate': candidate,
      },
      overrideToken: attachmentToken,
    );
  }

  void close() => _client.close();
}
