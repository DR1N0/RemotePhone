import 'dart:async';

import 'package:flutter/services.dart';

class DiscoveredServer {
  final String serverId;
  final String name;
  final String host;
  final int port;
  final String serviceName;

  const DiscoveredServer({
    required this.serverId,
    required this.name,
    required this.host,
    required this.port,
    required this.serviceName,
  });

  String get baseUrl {
    final formattedHost = host.contains(':') ? '[$host]' : host;
    return 'https://$formattedHost:$port';
  }
}

class ServerDiscovery {
  static const _events = EventChannel('remotephone/discovery/events');

  final _controller = StreamController<DiscoveredServer>.broadcast();
  final _lostController = StreamController<String>.broadcast();
  StreamSubscription<dynamic>? _subscription;

  Stream<DiscoveredServer> get servers => _controller.stream;
  Stream<String> get lost => _lostController.stream;

  bool get running => _subscription != null;

  void start() {
    if (_subscription != null) return;
    _subscription = _events.receiveBroadcastStream().listen((event) {
      if (event is! Map) return;
      final map = Map<String, dynamic>.from(event);
      switch (map['type']) {
        case 'found':
          final id = map['id']?.toString();
          final host = map['host']?.toString();
          final port = map['port'];
          if (id == null || host == null || port is! int) return;
          _controller.add(DiscoveredServer(
            serverId: id,
            name: map['name']?.toString() ?? host,
            host: host,
            port: port,
            serviceName: map['service']?.toString() ?? id,
          ));
        case 'lost':
          final service = map['service']?.toString();
          if (service != null) _lostController.add(service);
      }
    }, onError: (Object error) {
      // Discovery is best-effort; manual entry remains available.
    });
  }

  Future<void> stop() async {
    final subscription = _subscription;
    _subscription = null;
    await subscription?.cancel();
  }

  Future<void> dispose() async {
    await stop();
    await _controller.close();
    await _lostController.close();
  }
}
