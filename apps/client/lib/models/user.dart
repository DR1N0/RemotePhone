class User {
  final String id;
  final String type;
  final String? username;
  final String displayName;
  final String status;

  User({
    required this.id,
    required this.type,
    required this.username,
    required this.displayName,
    required this.status,
  });

  factory User.fromJson(Map<String, dynamic> json) {
    return User(
      id: json['id'] as String,
      type: json['type'] as String,
      username: json['username'] as String?,
      displayName: json['displayName'] as String,
      status: json['status'] as String,
    );
  }
}
