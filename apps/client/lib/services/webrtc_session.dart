import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'api_client.dart';
import 'session_cache.dart';

enum SessionConnectionState { idle, connecting, connected, failed, disconnected }

/// Owns one streaming session's full lifecycle: create the session record,
/// negotiate the WebRTC peer connection against mako-worker (via the
/// control plane's signaling relay), render the incoming video track, and
/// carry outgoing touch/key input over the "input" data channel. One
/// instance per stream screen — not reused across sessions.
class WebrtcSession extends ChangeNotifier {
  final ApiClient api;
  final String instanceId;

  // Keep one renderer for the app's lifetime. Recreating/disposing the
  // renderer for every reconnect intermittently produced a decoded-but-black
  // texture on Android; the third fresh renderer would work again. A shared
  // texture is reused and only its srcObject changes.
  static final RTCVideoRenderer _sharedRenderer = RTCVideoRenderer();
  static bool _sharedRendererInitialized = false;
  RTCVideoRenderer get remoteRenderer => _sharedRenderer;

  SessionConnectionState state = SessionConnectionState.idle;
  String? errorMessage;

  RTCPeerConnection? _pc;
  RTCDataChannel? _inputChannel;
  String? _sessionId;
  String? _attachmentId;
  String? _attachmentToken;
  Timer? _statsTimer;

  WebrtcSession({required this.api, required this.instanceId});

  Future<void> _closePeerConnection() async {
    _statsTimer?.cancel();
    _statsTimer = null;
    final channel = _inputChannel;
    _inputChannel = null;
    if (channel != null) {
      try {
        await channel.close();
      } catch (_) {}
    }
    final pc = _pc;
    _pc = null;
    if (pc != null) {
      try {
        await pc.close();
      } catch (_) {}
    }
  }

  /// Resumes the cached session for this instance if one exists (a prior
  /// disconnect only starts the server's owner-absence grace period, it
  /// doesn't close the session — see session_cache.dart), falling back to
  /// creating a fresh session if there's nothing cached or the cached one
  /// has already expired server-side.
  Future<(String sessionId, String attachmentId, String attachmentToken)>
      _openSession() async {
    final cached = SessionCache.instance.get(instanceId);
    if (cached != null) {
      debugPrint('[webrtc] resuming session=${cached.sessionId} attachment=${cached.attachmentId}');
      try {
        final token = await api.resumeAttachment(
          attachmentToken: cached.attachmentToken,
          sessionId: cached.sessionId,
          attachmentId: cached.attachmentId,
        );
        debugPrint('[webrtc] resume succeeded');
        return (cached.sessionId, cached.attachmentId, token);
      } on ApiException catch (e) {
        debugPrint('[webrtc] resume failed: $e — falling back to createSession');
        SessionCache.instance.clear(instanceId);
      }
    }
    debugPrint('[webrtc] creating new session for instance=$instanceId');
    final ownerClientId =
        'mako-client-${DateTime.now().millisecondsSinceEpoch}';
    // attachmentId comes straight from the server, not session.ownerAttachment
    // — createSession can now hand back a reclaimed owner attachment or a
    // brand new participant attachment (if the owner is already connected
    // elsewhere) instead of 409ing, so "mine" isn't always "the owner one".
    final (session, attachmentId, attachmentToken) = await api.createSession(
      instanceId: instanceId,
      ownerClientId: ownerClientId,
    );
    debugPrint('[webrtc] createSession succeeded: session=${session.id} attachment=$attachmentId');
    return (session.id, attachmentId, attachmentToken);
  }

  Future<void> connect() async {
    state = SessionConnectionState.connecting;
    notifyListeners();
    try {
      debugPrint('[webrtc] connect() start, instance=$instanceId');
      // A Retry happens on this same object: tear down the failed peer
      // connection and data channel first, otherwise the old PC's callbacks
      // and the new PC race over state and the renderer.
      await _closePeerConnection();
      if (!_sharedRendererInitialized) {
        await _sharedRenderer.initialize();
        _sharedRendererInitialized = true;
      }
      _sharedRenderer.srcObject = null;
      debugPrint('[webrtc] renderer initialized');

      final (sessionId, attachmentId, attachmentToken) = await _openSession();
      _sessionId = sessionId;
      _attachmentId = attachmentId;
      _attachmentToken = attachmentToken;
      SessionCache.instance.put(
        instanceId,
        CachedSession(
          sessionId: sessionId,
          attachmentId: attachmentId,
          attachmentToken: attachmentToken,
        ),
      );

      debugPrint('[webrtc] creating peer connection');
      final pc = await createPeerConnection({'iceServers': []});
      _pc = pc;
      debugPrint('[webrtc] peer connection created');

      // Without these, createOffer() has no m-lines for the media the worker
      // sends (the client never adds local tracks), so the answer can't carry
      // video/audio back — silent black screen / no sound, no error anywhere
      // in the handshake. Audio is required for the worker to negotiate its
      // PCMU track; the Flutter WebRTC plugin plays the remote audio track
      // automatically (no renderer needed).
      await pc.addTransceiver(
        kind: RTCRtpMediaType.RTCRtpMediaTypeVideo,
        init: RTCRtpTransceiverInit(direction: TransceiverDirection.RecvOnly),
      );
      await pc.addTransceiver(
        kind: RTCRtpMediaType.RTCRtpMediaTypeAudio,
        init: RTCRtpTransceiverInit(direction: TransceiverDirection.RecvOnly),
      );
      debugPrint('[webrtc] video+audio transceivers added');

      pc.onTrack = (event) {
        debugPrint('[webrtc] onTrack: kind=${event.track.kind} streams=${event.streams.length}');
        if (event.track.kind == 'video' && event.streams.isNotEmpty) {
          remoteRenderer.srcObject = event.streams.first;
          notifyListeners();
        }
      };

      pc.onIceCandidate = (candidate) {
        final c = candidate.candidate;
        if (c == null || c.isEmpty) return;
        // Best-effort trickle: a dropped candidate just means one fewer
        // ICE path to try, not a fatal error for the whole connection —
        // the server waits for full gathering and answers once regardless
        // (internal/worker/transport/webrtc/adapter.go's HandleOffer).
        unawaited(api
            .signalIceCandidate(
              attachmentToken: attachmentToken,
              sessionId: sessionId,
              attachmentId: attachmentId,
              candidate: c,
            )
            .catchError((_) {}));
      };

      pc.onConnectionState = (s) {
        debugPrint('[webrtc] onConnectionState: $s');
        switch (s) {
          case RTCPeerConnectionState.RTCPeerConnectionStateConnected:
            state = SessionConnectionState.connected;
            _startStatsPolling(pc);
          case RTCPeerConnectionState.RTCPeerConnectionStateFailed:
            state = SessionConnectionState.failed;
            errorMessage = 'Connection failed';
            _statsTimer?.cancel();
          case RTCPeerConnectionState.RTCPeerConnectionStateClosed:
          case RTCPeerConnectionState.RTCPeerConnectionStateDisconnected:
            if (state != SessionConnectionState.disconnected) {
              state = SessionConnectionState.disconnected;
            }
            _statsTimer?.cancel();
          default:
            break;
        }
        notifyListeners();
      };

      // Must be created BEFORE createOffer so it's part of the offer's
      // SDP — the worker only ever listens via OnDataChannel, it never
      // creates one itself (adapter.go:104, "Chrome creates the input
      // data channel as part of its offer").
      _inputChannel = await pc.createDataChannel(
        'input',
        RTCDataChannelInit()..ordered = true,
      );
      debugPrint('[webrtc] data channel created');

      final offer = await pc.createOffer();
      debugPrint('[webrtc] offer created');
      await pc.setLocalDescription(offer);
      debugPrint('[webrtc] local description set, calling signalSession');

      final sdpAnswer = await api.signalSession(
        attachmentToken: attachmentToken,
        sessionId: sessionId,
        attachmentId: attachmentId,
        sdpOffer: offer.sdp!,
      );
      debugPrint('[webrtc] signalSession returned an answer, setting remote description');
      await pc.setRemoteDescription(
        RTCSessionDescription(sdpAnswer, 'answer'),
      );
      debugPrint('[webrtc] remote description set, connect() done');
    } catch (e) {
      debugPrint('[webrtc] connect() failed: $e');
      state = SessionConnectionState.failed;
      errorMessage = e.toString();
      notifyListeners();
      rethrow;
    }
  }

  /// Sends one normalized input event — wire format fixed by the worker
  /// (transport.InputEvent, internal/worker/transport/adapter.go): plain
  /// JSON text, x/y normalized 0-1 against the video's own dimensions.
  /// pointerId keeps each finger distinct so the worker can inject a real
  /// multi-touch stream via the emulator's gRPC sendTouch.
  void sendTouch({
    required String action,
    required double x,
    required double y,
    int pointerId = 0,
  }) {
    _send({
      'type': 'touch',
      'action': action,
      'x': x,
      'y': y,
      'keycode': 0,
      'pointerId': pointerId,
    });
  }

  void sendKey({required String action, required int keycode}) {
    _send({'type': 'key', 'action': action, 'x': 0, 'y': 0, 'keycode': keycode});
  }

  void _send(Map<String, dynamic> event) {
    final channel = _inputChannel;
    if (channel == null) return;
    channel.send(RTCDataChannelMessage(jsonEncode(event)));
  }

  /// Logs inbound video RTP stats every 2s — the diagnostic that actually
  /// distinguishes "frames never arrive" (packetsReceived stays 0),
  /// "frames arrive but the decoder rejects them" (packetsReceived grows,
  /// framesDecoded doesn't), and "frames decode fine, the renderer just
  /// isn't painting them" (framesDecoded grows) — three completely
  /// different bugs that all look identical as "black screen" from the UI.
  void _startStatsPolling(RTCPeerConnection pc) {
    _statsTimer?.cancel();
    _statsTimer = Timer.periodic(const Duration(seconds: 2), (_) async {
      final reports = await pc.getStats();
      final byId = <String, dynamic>{ for (final r in reports) r.id: r };
      for (final report in reports) {
        if (report.type != 'candidate-pair') continue;
        final selected = report.values['selected'] == true ||
            report.values['nominated'] == true;
        if (!selected) continue;
        final local = byId[report.values['localCandidateId']];
        final remote = byId[report.values['remoteCandidateId']];
        debugPrint('[webrtc] candidate pair: '
            '${local?.values['candidateType']} ${local?.values['address']}:${local?.values['port']} '
            '-> ${remote?.values['candidateType']} ${remote?.values['address']}:${remote?.values['port']} '
            'rtt=${report.values['currentRoundTripTime']}');
      }
      for (final report in reports) {
        if (report.type != 'inbound-rtp') continue;
        final kind = report.values['kind'] ?? report.values['mediaType'];
        if (kind == 'audio') {
          // Same black-box diagnosis as video: packetsReceived growing but
          // no sound means a decode/route problem, not an empty stream.
          debugPrint('[webrtc] audio stats: packetsReceived=${report.values['packetsReceived']} '
              'bytesReceived=${report.values['bytesReceived']} '
              'audioLevel=${report.values['audioLevel']} '
              'jitter=${report.values['jitter']}');
          continue;
        }
        if (kind != 'video') continue;
        debugPrint('[webrtc] video stats: packetsReceived=${report.values['packetsReceived']} '
            'bytesReceived=${report.values['bytesReceived']} '
            'framesReceived=${report.values['framesReceived']} '
            'framesDecoded=${report.values['framesDecoded']} '
            'frameWidth=${report.values['frameWidth']} '
            'frameHeight=${report.values['frameHeight']} '
            'framesDropped=${report.values['framesDropped']}');
      }
    });
  }

  Future<void> disconnect() async {
    debugPrint('[webrtc] disconnect() start, session=$_sessionId attachment=$_attachmentId');
    _statsTimer?.cancel();
    final sessionId = _sessionId;
    final attachmentId = _attachmentId;
    final token = _attachmentToken;
    // Deliberately leaves the SessionCache entry in place: the server
    // treats this as a graceful owner-absence window, not a close, so the
    // next connect() for this instance can resume it (see _openSession).
    if (sessionId != null && attachmentId != null && token != null) {
      try {
        await api.disconnectAttachment(
          attachmentToken: token,
          sessionId: sessionId,
          attachmentId: attachmentId,
        );
        debugPrint('[webrtc] disconnectAttachment succeeded');
      } catch (e) {
        debugPrint('[webrtc] disconnectAttachment failed (ignored): $e');
        // Best-effort: tearing down the local peer connection below
        // matters more than this call succeeding.
      }
    }
    await _closePeerConnection();
    _sharedRenderer.srcObject = null;
    state = SessionConnectionState.disconnected;
    notifyListeners();
    debugPrint('[webrtc] disconnect() done');
  }
}
