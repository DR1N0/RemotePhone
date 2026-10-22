import 'package:flutter/material.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:provider/provider.dart';

import '../models/managed_instance.dart';
import '../services/auth_service.dart';
import '../services/webrtc_session.dart';

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

  @override
  void initState() {
    super.initState();
    _session = WebrtcSession(
      api: context.read<AuthService>().client,
      instanceId: widget.instance.id,
    );
    _session.connect();
  }

  @override
  void dispose() {
    _disconnectOnce();
    super.dispose();
  }

  void _disconnectOnce() {
    if (_disconnected) return;
    _disconnected = true;
    _session.disconnect();
  }

  // Normalizes against the full video widget box, not the actual
  // letterboxed video content rect — if the emulator's aspect ratio
  // doesn't match the screen's, a tap in the letterbox bars maps to the
  // nearest video edge instead of being rejected. Acceptable for this
  // MVP; a precise version would compare against
  // remoteRenderer.videoWidth/videoHeight.
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
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: Text(widget.instance.name),
        actions: [
          ListenableBuilder(
            listenable: _session,
            builder: (context, _) => Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12),
              child: Center(child: Text(_statusLabel(_session.state))),
            ),
          ),
        ],
      ),
      body: ListenableBuilder(
        listenable: _session,
        builder: (context, _) => _buildBody(_session.state),
      ),
    );
  }

  String _statusLabel(SessionConnectionState state) => switch (state) {
        SessionConnectionState.idle => 'Idle',
        SessionConnectionState.connecting => 'Connecting…',
        SessionConnectionState.connected => 'Connected',
        SessionConnectionState.failed => 'Failed',
        SessionConnectionState.disconnected => 'Disconnected',
      };

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
    // connected or disconnected: keep showing the last video frame rather
    // than flashing to a blank screen on a transient state change.
    // RTCVideoViewObjectFitContain preserves the video's own aspect ratio
    // (letterboxing as needed) within whatever box it's given, so this
    // just fills the available body area rather than guessing a fixed
    // ratio — the emulator's actual resolution varies per template.
    return Listener(
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
    );
  }
}
