/// Mirrors only the fields the UI needs from instance.ManagedInstance
/// (apps/server/internal/controlplane/instance/types.go) — not an
/// exhaustive copy of the Go struct.
class ManagedInstance {
  final String id;
  final String name;
  final String templateId;
  final String desiredState;
  final String observedState;
  final String? lastError;

  ManagedInstance({
    required this.id,
    required this.name,
    required this.templateId,
    required this.desiredState,
    required this.observedState,
    this.lastError,
  });

  factory ManagedInstance.fromJson(Map<String, dynamic> json) {
    return ManagedInstance(
      id: json['id'] as String,
      name: json['name'] as String,
      templateId: json['templateId'] as String,
      desiredState: json['desiredState'] as String,
      observedState: json['observedState'] as String,
      lastError: json['lastError'] as String?,
    );
  }

  bool get isRunning => desiredState == 'running';
}
