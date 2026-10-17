import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'services/auth_service.dart';
import 'screens/instance_list_screen.dart';
import 'screens/login_screen.dart';

void main() {
  runApp(
    ChangeNotifierProvider(
      create: (_) => AuthService()..restore(),
      child: const MakoClientApp(),
    ),
  );
}

class MakoClientApp extends StatelessWidget {
  const MakoClientApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'mako',
      theme: ThemeData(colorScheme: ColorScheme.fromSeed(seedColor: Colors.teal)),
      home: const _RootRouter(),
    );
  }
}

/// Routes purely on AuthService.status — no named routes, no navigator
/// juggling for the top-level auth/instance-list split. Screens below
/// this (register, stream) use ordinary Navigator.push.
class _RootRouter extends StatelessWidget {
  const _RootRouter();

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    switch (auth.status) {
      case AuthStatus.unknown:
        return const Scaffold(body: Center(child: CircularProgressIndicator()));
      case AuthStatus.unauthenticated:
        return const LoginScreen();
      case AuthStatus.authenticated:
        return const InstanceListScreen();
    }
  }
}
