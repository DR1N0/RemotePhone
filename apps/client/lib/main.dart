import 'package:flutter/material.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:provider/provider.dart';

import 'services/auth_service.dart';
import 'screens/login_screen.dart';
import 'screens/server_list_screen.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // Treat remote audio as media, not a phone call: otherwise Android routes
  // it to the earpiece and the volume keys control STREAM_VOICE_CALL. This
  // must run before the first peer connection, because flutter_webrtc applies
  // it while creating its JavaAudioDeviceModule.
  await WebRTC.initialize(
    options: <String, dynamic>{
      'androidAudioConfiguration': AndroidAudioConfiguration.media.toMap(),
    },
  );
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
    if (auth.status == AuthStatus.unknown) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }
    // Servers are the home screen. Instance lists are pushed on top of it, so
    // Back from an instance list always returns here instead of exiting.
    if (auth.servers.isEmpty) {
      return const LoginScreen();
    }
    return const ServerListScreen();
  }
}
