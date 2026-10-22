/// Mirrors instance.TemplateInfo (apps/server/internal/controlplane/instance/
/// types.go): the client-safe subset of a host-discovered runtime template.
/// Metadata (AVD paths, emulator binary path) deliberately never reaches the
/// client.
class TemplateInfo {
  final String templateId;
  final String displayName;
  final String runtimeClass;

  TemplateInfo({
    required this.templateId,
    required this.displayName,
    required this.runtimeClass,
  });

  factory TemplateInfo.fromJson(Map<String, dynamic> json) {
    return TemplateInfo(
      templateId: json['templateId'] as String,
      displayName: (json['displayName'] as String?) ?? '',
      runtimeClass: (json['runtimeClass'] as String?) ?? '',
    );
  }

  String get label => displayName.isEmpty ? templateId : displayName;
}
