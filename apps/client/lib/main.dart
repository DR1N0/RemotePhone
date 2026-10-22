import 'package:flutter/material.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:provider/provider.dart';

import 'services/auth_service.dart';
import 'screens/server_list_screen.dart';
import 'theme/app_theme.dart';

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
      debugShowCheckedModeBanner: false,
      theme: buildAppTheme(),
      darkTheme: buildAppTheme(),
      themeMode: ThemeMode.dark,
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
    // The server picker is always home, including before the first pairing:
    // that is where mDNS Nearby results and the Add server action live.
    return const ServerListScreen();
  }
}
