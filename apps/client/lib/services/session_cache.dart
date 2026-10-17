class CachedSession {
  final String sessionId;
  final String attachmentId;
  final String attachmentToken;

  const CachedSession({
    required this.sessionId,
    required this.attachmentId,
    required this.attachmentToken,
  });
}

/// Remembers the most recent owner session per instance for this app run.
/// Disconnecting an attachment only starts the server's owner-absence grace
/// period (session/service.go) rather than closing the session outright, so
/// reconnecting to the same instance should resume that session instead of
/// calling createSession again, which would 409.
class SessionCache {
  SessionCache._();
  static final instance = SessionCache._();

  final Map<String, CachedSession> _byInstance = {};

  CachedSession? get(String instanceId) => _byInstance[instanceId];

  void put(String instanceId, CachedSession session) {
    _byInstance[instanceId] = session;
  }

  void clear(String instanceId) => _byInstance.remove(instanceId);
}
