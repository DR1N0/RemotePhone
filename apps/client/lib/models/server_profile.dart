import 'dart:convert';

enum ServerAddressKind {
  /// A stable, user-entered URL (normally a DNS name). The address is treated
  /// as authoritative and is never rewritten by discovery.
  pinnedUrl,

  /// A LAN endpoint whose host/IP may move (DHCP, mDNS). The server ID is the
  /// identity; the stored address is only a cache and may be updated.
  lan,
}

/// One paired remotephone control plane remembered by this client. The server ID is
/// stable across restarts; the name is configurable and may change, and the
/// address may change when DHCP moves the host. The certificate fingerprint
/// is what actually authenticates the server.
class ServerProfile {
  final String serverId;
  final String name;
  final String baseUrl;
  final String certSha256;
  final String token;
  final String userId;
  final String username;
  final ServerAddressKind kind;

  const ServerProfile({
    required this.serverId,
    required this.name,
    required this.baseUrl,
    required this.certSha256,
    required this.token,
    required this.userId,
    required this.username,
    required this.kind,
  });

  /// One server can be used by several accounts; the connection identity is
  /// (serverId, userId), falling back to username for old profiles.
  String get accountKey => userId.isNotEmpty ? userId : username;

  static ServerAddressKind kindForUrl(String baseUrl) {
    final host = Uri.tryParse(baseUrl)?.host ?? '';
    if (host.endsWith('.local')) return ServerAddressKind.lan;
    // IPv4 literal or bracketed/plain IPv6: an IP is not stable identity, so
    // treat it as a LAN address that discovery/editing may replace.
    final ipv4 = RegExp(r'^\d{1,3}(\.\d{1,3}){3}$');
    if (ipv4.hasMatch(host) || host.contains(':')) {
      return ServerAddressKind.lan;
    }
    return ServerAddressKind.pinnedUrl;
  }

  ServerProfile copyWith({
    String? serverId,
    String? name,
    String? baseUrl,
    String? certSha256,
    String? token,
    String? userId,
    String? username,
    ServerAddressKind? kind,
  }) {
    return ServerProfile(
      serverId: serverId ?? this.serverId,
      name: name ?? this.name,
      baseUrl: baseUrl ?? this.baseUrl,
      certSha256: certSha256 ?? this.certSha256,
      token: token ?? this.token,
      userId: userId ?? this.userId,
      username: username ?? this.username,
      kind: kind ?? this.kind,
    );
  }

  factory ServerProfile.fromJson(Map<String, dynamic> json) {
    final baseUrl = json['baseUrl'] as String;
    final username = json['username'] as String? ?? '';
    return ServerProfile(
      serverId: json['serverId'] as String,
      name: json['name'] as String? ?? 'remotephone',
      baseUrl: baseUrl,
      certSha256: json['certSha256'] as String,
      token: json['token'] as String,
      userId: json['userId'] as String? ?? '',
      username: username,
      kind: switch (json['kind']) {
        'lan' => ServerAddressKind.lan,
        'url' => ServerAddressKind.pinnedUrl,
        _ => ServerProfile.kindForUrl(baseUrl),
      },
    );
  }

  Map<String, dynamic> toJson() => {
        'serverId': serverId,
        'name': name,
        'baseUrl': baseUrl,
        'certSha256': certSha256,
        'token': token,
        'userId': userId,
        'username': username,
        'kind': kind == ServerAddressKind.lan ? 'lan' : 'url',
      };

  static List<ServerProfile> listFromJson(String raw) {
    final decoded = jsonDecode(raw);
    if (decoded is! List) return const [];
    return decoded
        .whereType<Map<String, dynamic>>()
        .map(ServerProfile.fromJson)
        .toList();
  }

  static String listToJson(List<ServerProfile> servers) =>
      jsonEncode(servers.map((s) => s.toJson()).toList());
}
