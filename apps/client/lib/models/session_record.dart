/// Mirrors only the fields the UI needs from session.SessionRecord /
/// session.AttachmentRecord (apps/server/internal/controlplane/session/types.go).
class AttachmentRecord {
  final String id;
  final String sessionId;
  final String role;
  final String status;

  AttachmentRecord({
    required this.id,
    required this.sessionId,
    required this.role,
    required this.status,
  });

  factory AttachmentRecord.fromJson(Map<String, dynamic> json) {
    return AttachmentRecord(
      id: json['id'] as String,
      sessionId: json['sessionId'] as String,
      role: json['role'] as String,
      status: json['status'] as String,
    );
  }
}

class SessionRecord {
  final String id;
  final String instanceId;
  final String status;
  final List<AttachmentRecord> attachments;

  SessionRecord({
    required this.id,
    required this.instanceId,
    required this.status,
    required this.attachments,
  });

  factory SessionRecord.fromJson(Map<String, dynamic> json) {
    return SessionRecord(
      id: json['id'] as String,
      instanceId: json['instanceId'] as String,
      status: json['status'] as String,
      attachments: (json['attachments'] as List<dynamic>? ?? [])
          .map((a) => AttachmentRecord.fromJson(a as Map<String, dynamic>))
          .toList(),
    );
  }

  /// The owner attachment — the only one this MVP's client ever creates
  /// (POST /api/v1/sessions always creates the caller as the owner
  /// attachment, see session.Service.CreateSession).
  AttachmentRecord get ownerAttachment =>
      attachments.firstWhere((a) => a.role == 'owner');
}
