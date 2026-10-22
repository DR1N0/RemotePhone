import 'dart:convert';

/// One paired mako control plane remembered by this client. The server ID is
/// stable across restarts; the name is configurable and may change, and the
/// address may change when DHCP moves the host. The certificate fingerprint
/// is what actually authenticates the server.
class ServerProfile {
  final String serverId;
  final String name;
  final String baseUrl;
  final String certSha256;
  final String token;
  final String username;

  const ServerProfile({
    required this.serverId,
    required this.name,
    required this.baseUrl,
    required this.certSha256,
    required this.token,
    required this.username,
  });

  ServerProfile copyWith({
    String? serverId,
    String? name,
    String? baseUrl,
    String? certSha256,
    String? token,
    String? username,
  }) {
    return ServerProfile(
      serverId: serverId ?? this.serverId,
      name: name ?? this.name,
      baseUrl: baseUrl ?? this.baseUrl,
      certSha256: certSha256 ?? this.certSha256,
      token: token ?? this.token,
      username: username ?? this.username,
    );
  }

  factory ServerProfile.fromJson(Map<String, dynamic> json) {
    return ServerProfile(
      serverId: json['serverId'] as String,
      name: json['name'] as String? ?? 'mako',
      baseUrl: json['baseUrl'] as String,
      certSha256: json['certSha256'] as String,
      token: json['token'] as String,
      username: json['username'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'serverId': serverId,
        'name': name,
        'baseUrl': baseUrl,
        'certSha256': certSha256,
        'token': token,
        'username': username,
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
