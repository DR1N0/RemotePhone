import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:provider/provider.dart';

import '../models/managed_instance.dart';
import '../services/auth_service.dart';
import '../services/webrtc_session.dart';

/// Full-screen remote display. The guest Android device uses three-button
/// navigation (configured by the worker when a session starts), but games can
/// request immersive fullscreen and hide that nav bar. A small top-left exit
/// affordance therefore stays available on the phone; the phone's own Back
/// gesture also leaves the stream.
class StreamScreen extends StatefulWidget {
  final ManagedInstance instance;

  const StreamScreen({super.key, required this.instance});

  @override
  State<StreamScreen> createState() => _StreamScreenState();
}

class _StreamScreenState extends State<StreamScreen> {
  late final WebrtcSession _session;
  final GlobalKey _videoKey = GlobalKey();
  bool _disconnected = false;
  bool _allowPop = false;
  bool _leaving = false;

  static const _windowChannel = MethodChannel('mako/window');

  @override
  void initState() {
    super.initState();
    _session = WebrtcSession(
      api: context.read<AuthService>().client,
      instanceId: widget.instance.id,
    );
    _enterFullscreen();
    _session.connect();
  }

  @override
  void dispose() {
    _setFullscreen(false);
    _disconnectOnce();
    super.dispose();
  }

  void _enterFullscreen() {
    _setFullscreen(true);
  }

  Future<void> _setFullscreen(bool enabled) async {
    try {
      await _windowChannel.invokeMethod<void>(
        enabled ? 'enterFullscreen' : 'exitFullscreen',
      );
    } on MissingPluginException {
      await SystemChrome.setEnabledSystemUIMode(
        enabled ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
      );
    } on PlatformException {
      await SystemChrome.setEnabledSystemUIMode(
        enabled ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
      );
    }
  }

  void _disconnectOnce() {
    if (_disconnected) return;
    _disconnected = true;
    _session.disconnect();
  }

  Future<void> _leave() async {
    if (_leaving) return;
    _leaving = true;
    _disconnected = true;
    // Let PopScope's canPop become true before the programmatic pop below.
    if (mounted) setState(() => _allowPop = true);
    await _session.disconnect();
    if (!mounted) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.of(context).pop();
    });
  }

  Offset? _normalizedPosition(Offset globalPosition) {
    final box = _videoKey.currentContext?.findRenderObject() as RenderBox?;
    if (box == null || !box.hasSize || box.size.isEmpty) return null;
    final local = box.globalToLocal(globalPosition);
    final dx = (local.dx / box.size.width).clamp(0.0, 1.0);
    final dy = (local.dy / box.size.height).clamp(0.0, 1.0);
    return Offset(dx, dy);
  }

  void _handlePointer(PointerEvent event, String action) {
    final normalized = _normalizedPosition(event.position);
    if (normalized == null) return;
    _session.sendTouch(
      action: action,
      x: normalized.dx,
      y: normalized.dy,
      pointerId: event.pointer,
    );
  }

  @override
  Widget build(BuildContext context) {
    return PopScope(
      canPop: _allowPop,
      // Phone Back is the only way out of the stream: it disconnects the
      // attachment and returns to the instance list. The guest's own
      // three-button nav handles Back/Home/Recents inside the video.
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _leave();
      },
      child: Scaffold(
        backgroundColor: Colors.black,
        body: ListenableBuilder(
          listenable: _session,
          builder: (context, _) => _buildBody(_session.state),
        ),
      ),
    );
  }

  Widget _buildBody(SessionConnectionState state) {
    if (state == SessionConnectionState.failed) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, color: Colors.redAccent, size: 48),
            const SizedBox(height: 12),
            Text(
              _session.errorMessage ?? 'Connection failed',
              style: const TextStyle(color: Colors.white),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 12),
            FilledButton(
              onPressed: () {
                _disconnected = false;
                _session.connect();
              },
              child: const Text('Retry'),
            ),
            const SizedBox(height: 8),
            TextButton(
              onPressed: _leave,
              child: const Text('Exit'),
            ),
          ],
        ),
      );
    }
    if (state == SessionConnectionState.idle ||
        state == SessionConnectionState.connecting) {
      return const Center(
        child: CircularProgressIndicator(color: Colors.white),
      );
    }
    // RTCVideoViewObjectFitContain preserves the video's own aspect ratio
    // (letterboxing as needed); the emulator's resolution varies per template.
    return Stack(
      children: [
        Positioned.fill(
          child: Listener(
            onPointerDown: (e) => _handlePointer(e, 'down'),
            onPointerMove: (e) => _handlePointer(e, 'move'),
            onPointerUp: (e) => _handlePointer(e, 'up'),
            onPointerCancel: (e) => _handlePointer(e, 'up'),
            child: SizedBox.expand(
              key: _videoKey,
              child: RTCVideoView(
                _session.remoteRenderer,
                objectFit: RTCVideoViewObjectFit.RTCVideoViewObjectFitContain,
              ),
            ),
          ),
        ),
        Positioned(
          top: 8,
          left: 8,
          child: SafeArea(
            child: IconButton(
              tooltip: 'Exit stream',
              onPressed: _leave,
              icon: const Icon(Icons.arrow_back, size: 20),
              style: IconButton.styleFrom(
                foregroundColor: Colors.white,
                backgroundColor: Colors.black54,
                minimumSize: const Size(40, 40),
                padding: EdgeInsets.zero,
              ),
            ),
          ),
        ),
      ],
    );
  }
}
