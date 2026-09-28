import 'dart:async';
import 'dart:convert';
import 'dart:ffi';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:ffi/ffi.dart';

import '../zju_connect_bindings_generated.dart' as native;
import 'types.dart';

/// A connected (or connecting) VPN session.
///
/// Create one with [SangforVpnClient.connect], which performs the login. The
/// session is then usable through either data plane:
///
///  * [startProxy] runs local SOCKS5/HTTP proxies. This needs no privileges and
///    works on every platform, which makes it the quickest way to a working
///    build.
///  * [packets] streams raw IPv4 packets for a platform tun device
///    (Android `VpnService`, iOS `NEPacketTunnelProvider`), and [writePacket]
///    sends packets back.
///
/// Call [close] when finished.
class SangforVpnClient {
  /// The callables and event port must outlive the constructor; they are
  /// released in [close].
  SangforVpnClient._(
    this._handle,
    this._events,
    this._eventCallable,
    this._challengeCallable,
  );

  int _handle;
  // ignore: unused_field
  final ReceivePort _events;
  // ignore: unused_field
  final NativeCallable<native.zc_event_fnFunction> _eventCallable;
  // ignore: unused_field
  final NativeCallable<native.zc_challenge_fnFunction> _challengeCallable;

  final _logController = StreamController<String>.broadcast();
  final _errorController = StreamController<String>.broadcast();
  final _stateController = StreamController<VpnState>.broadcast();
  final _challengeController = StreamController<VpnChallenge>.broadcast();

  /// Bridge for [packets]; the pump isolate forwards packets here.
  final _packetController = StreamController<Uint8List>();

  /// Diagnostics emitted by the native library.
  Stream<String> get logs => _logController.stream;

  /// Terminal errors. The session is unusable afterwards.
  Stream<String> get errors => _errorController.stream;

  /// Lifecycle transitions.
  Stream<VpnState> get states => _stateController.stream;

  /// Interactive authentication challenges that must be answered with
  /// [respondToChallenge].
  Stream<VpnChallenge> get challenges => _challengeController.stream;

  bool _closed = false;
  Isolate? _pumpIsolate;
  SendPort? _pumpControl;

  /// Performs a full VPN login and returns a ready session.
  ///
  /// [config] is validated by the native library; invalid values throw
  /// [VpnException]. The call blocks until login completes and may take several
  /// seconds.
  ///
  /// While this future is pending the session reports progress through
  /// [onLog] and [onError], and asks for credentials through [onChallenge].
  /// Those callbacks are optional, but supplying them is the only way to see
  /// what happens before the returned session exists — the streams on the
  /// session itself start empty. Both the callbacks and the corresponding
  /// streams stay active for the whole session.
  static Future<SangforVpnClient> connect(
    VpnConfig config, {
    void Function(String message)? onLog,
    void Function(String message)? onError,
    void Function(VpnChallenge challenge)? onChallenge,
  }) async {
    final errOut = calloc<Pointer<Char>>();
    final configJson = jsonEncode(config.toJson()).toNativeUtf8();

    // The callbacks post to this port. NativeCallable.listener wraps a
    // RawReceivePort, so the Go side may invoke them from any goroutine.
    final events = ReceivePort();

    var handle = 0;
    var clientOwnsResources = false;
    NativeCallable<native.zc_event_fnFunction>? eventCallable;
    NativeCallable<native.zc_challenge_fnFunction>? challengeCallable;

    try {
      eventCallable = NativeCallable<native.zc_event_fnFunction>.listener((
        Pointer<Void> _,
        int type,
        Pointer<Char> message,
      ) {
        // The native side transfers ownership of the string; see the note on
        // zcEventSink.event in binding/capi/capi.go.
        try {
          final text = message == nullptr
              ? ''
              : message.cast<Utf8>().toDartString();
          events.sendPort.send(_EventMessage(type, text));
        } finally {
          if (message != nullptr) native.zcFreeString(message);
        }
      });
      challengeCallable =
          NativeCallable<native.zc_challenge_fnFunction>.listener((
            Pointer<Void> _,
            int challengeId,
            Pointer<Char> payload,
          ) {
            try {
              final text = payload == nullptr
                  ? '{}'
                  : payload.cast<Utf8>().toDartString();
              events.sendPort.send(_ChallengeMessage(challengeId, text));
            } finally {
              if (payload != nullptr) native.zcFreeString(payload);
            }
          });

      handle = native.zcNew(
        configJson.cast<Char>(),
        nullptr,
        eventCallable.nativeFunction,
        challengeCallable.nativeFunction,
        errOut,
      );

      if (handle == 0) {
        final message = errOut.value == nullptr
            ? 'failed to create VPN client'
            : errOut.value.cast<Utf8>().toDartString();
        throw VpnException(message);
      }

      final client = SangforVpnClient._(
        handle,
        events,
        eventCallable,
        challengeCallable,
      );
      client._onLog = onLog;
      client._onError = onError;
      client._onChallenge = onChallenge;

      // From here the client owns the handle, the port and the callables. The
      // failure path below must not release them a second time.
      clientOwnsResources = true;

      // Wire the callbacks into the Dart streams before Setup so no early log
      // line or challenge is lost.
      events.listen(client._onNativeMessage);

      // zcSetup blocks for the whole login, and the callbacks arrive through a
      // ReceivePort. Running Setup on this isolate would starve the event loop,
      // so the queued log lines would only be delivered after the login was
      // already over. Running it on a worker isolate frees this one to service
      // the port while the native code works.
      final result = await _runSetup(handle);
      if (result != native.ZC_OK) {
        client.close();
        throw VpnException(
          'VPN login failed (native code $result). See errors and logs for details.',
        );
      }

      return client;
    } catch (_) {
      // Only clean up what was not handed over to a client.
      if (!clientOwnsResources) {
        if (handle != 0) {
          native.zcClose(handle);
          native.zcFree(handle);
        } else {
          events.close();
          eventCallable?.close();
          challengeCallable?.close();
        }
      }
      rethrow;
    } finally {
      calloc.free(configJson);
      calloc.free(errOut);
    }
  }

  /// Optional callbacks supplied to [connect]. They fire alongside the
  /// streams so callers can observe the login before the session exists.
  void Function(String message)? _onLog;
  void Function(String message)? _onError;
  void Function(VpnChallenge challenge)? _onChallenge;

  void _onNativeMessage(Object? message) {
    if (_closed) return;
    if (message is _EventMessage) {
      switch (message.type) {
        case native.ZC_EVENT_LOG:
          _logController.add(message.text);
          _onLog?.call(message.text);
        case native.ZC_EVENT_STATE:
          _stateController.add(VpnState.fromWireName(message.text));
        case native.ZC_EVENT_ERROR:
          _errorController.add(message.text);
          _onError?.call(message.text);
      }
    } else if (message is _ChallengeMessage) {
      try {
        final payload = jsonDecode(message.payload) as Map<String, Object?>;
        final challenge = VpnChallenge.fromNative(
          message.id,
          _kindOf(payload),
          payload,
        );
        _challengeController.add(challenge);
        _onChallenge?.call(challenge);
      } catch (error) {
        // A malformed payload must not kill the session; fail the challenge so
        // the login reports a useful error instead of hanging.
        respondToChallenge(message.id, {
          'error': 'unparsable challenge: $error',
        });
      }
    }
  }

  /// The native payload does not carry a top-level challenge kind, so infer it
  /// from the fields present.
  static String _kindOf(Map<String, Object?> payload) {
    if (payload.containsKey('login_url')) return 'external_login';
    if (payload.containsKey('image_base64')) {
      // Both captcha kinds carry an image; the click variant expects
      // coordinates back and the native side marks it by an empty output path.
      final outputPath = payload['output_path'];
      return outputPath == null || outputPath == ''
          ? 'click_captcha'
          : 'text_captcha';
    }
    return 'code';
  }

  /// Answers a challenge from [challenges].
  ///
  /// Prefer the typed helpers ([respondToCode], [respondToCaptcha],
  /// [respondToClickCaptcha], [respondToExternalLogin]). Returns true when the
  /// challenge was still pending.
  bool respondToChallenge(int id, Map<String, Object?> response) {
    if (_closed) return false;
    final json = jsonEncode(response).toNativeUtf8();
    try {
      return native.zcRespondChallenge(_handle, id, json.cast<Char>()) ==
          native.ZC_OK;
    } finally {
      calloc.free(json);
    }
  }

  /// Answers a [CodeChallenge].
  bool respondToCode(int id, String code, {bool skipSecondaryAuth = false}) =>
      respondToChallenge(id, {
        'code': code,
        'skip_secondary_auth': skipSecondaryAuth,
      });

  /// Answers a [TextCaptchaChallenge] with typed characters.
  bool respondToCaptcha(int id, String code) =>
      respondToChallenge(id, {'code': code});

  /// Answers a [ClickCaptchaChallenge] with tapped points.
  bool respondToClickCaptcha(
    int id,
    List<({int x, int y})> points, {
    required int width,
    required int height,
  }) => respondToChallenge(id, {
    'points': [
      for (final point in points) {'x': point.x, 'y': point.y},
    ],
    'width': width,
    'height': height,
  });

  /// Answers an [ExternalLoginChallenge] with the callback URL the web view
  /// ended on.
  bool respondToExternalLogin(int id, String callbackUrl) =>
      respondToChallenge(id, {'callback_url': callbackUrl});

  /// Cancels a pending challenge, aborting the login.
  bool cancelChallenge(int id, String reason) =>
      respondToChallenge(id, {'error': reason});

  /// Returns the current session status.
  VpnStatus status() {
    if (_closed) return _emptyStatus;
    final pointer = native.zcStatJSON(_handle);
    if (pointer == nullptr) return _emptyStatus;
    try {
      final json = jsonDecode(
        pointer.cast<Utf8>().toDartString(),
      ) as Map<String, Object?>;
      return VpnStatus.fromJson(json);
    } finally {
      native.zcFreeString(pointer);
    }
  }

  static const _emptyStatus = VpnStatus(
    state: VpnState.disconnected,
    virtualIp: '',
    sessionId: '',
    clientData: '',
  );

  /// Starts the local SOCKS5/HTTP proxies and returns their addresses.
  ///
  /// The proxies route through the VPN and live until [close].
  Future<ProxyAddrs> startProxy([
    ProxyOptions options = const ProxyOptions(),
  ]) async {
    if (_closed) throw VpnException('session is closed');
    final json = jsonEncode(options.toJson()).toNativeUtf8();
    try {
      final pointer = native.zcStartProxy(_handle, json.cast<Char>());
      if (pointer == nullptr) {
        throw VpnException('failed to start local proxies');
      }
      try {
        final decoded = jsonDecode(
          pointer.cast<Utf8>().toDartString(),
        ) as Map<String, Object?>;
        return ProxyAddrs.fromJson(decoded);
      } finally {
        native.zcFreeString(pointer);
      }
    } finally {
      calloc.free(json);
    }
  }

  /// Streams IPv4 packets from the VPN server, to be written into a platform tun
  /// device.
  ///
  /// Reading blocks, so it runs on a dedicated isolate. The pump starts on the
  /// first call and stops when [close] is called.
  Stream<Uint8List> packets({int bufferSize = 65536}) async* {
    if (_closed) throw VpnException('session is closed');
    await _ensurePacketPump(bufferSize);
    yield* _packetController.stream;
  }

  Future<void> _ensurePacketPump(int bufferSize) async {
    if (_pumpIsolate != null) return;

    final ready = ReceivePort();
    _pumpIsolate = await Isolate.spawn(
      _packetPumpMain,
      _PumpConfig(_handle, bufferSize, _packetController, ready.sendPort),
    );
    // Wait for the isolate to report its control port, so stop can be routed.
    _pumpControl = await ready.first as SendPort;
    ready.close();
  }

  /// Sends one IPv4 packet to the VPN server.
  void writePacket(Uint8List packet) {
    if (_closed) throw VpnException('session is closed');
    final buffer = calloc<Uint8>(packet.length);
    try {
      buffer.asTypedList(packet.length).setAll(0, packet);
      final result = native.zcWritePacket(_handle, buffer, packet.length);
      if (result != native.ZC_OK) {
        throw VpnException('failed to write packet (native code $result)');
      }
    } finally {
      calloc.free(buffer);
    }
  }

  /// Tears the session down. Safe to call more than once.
  void close() {
    if (_closed) return;
    _closed = true;

    _pumpControl?.send(const _PumpStop());
    _pumpIsolate?.kill(priority: Isolate.immediate);
    _pumpIsolate = null;
    _pumpControl = null;
    unawaited(_packetController.close());

    if (_handle != 0) {
      native.zcClose(_handle);
      native.zcFree(_handle);
      _handle = 0;
    }

    _events.close();
    _eventCallable.close();
    _challengeCallable.close();

    unawaited(_logController.close());
    unawaited(_errorController.close());
    unawaited(_stateController.close());
    unawaited(_challengeController.close());
  }
}

/// Thrown when the native library reports a failure.
class VpnException implements Exception {
  VpnException(this.message);

  final String message;

  @override
  String toString() => 'VpnException: $message';
}

/// Runs the blocking native login on a worker isolate.
///
/// The native library is already loaded in this process, so the isolate only
/// needs the handle. Doing this off the main isolate keeps the event loop free
/// to deliver log lines and challenges while the login is in flight.
Future<int> _runSetup(int handle) async {
  final result = ReceivePort();
  await Isolate.spawn((SendPort reply) {
    var code = native.ZC_ERR_INTERNAL;
    try {
      code = native.zcSetup(handle);
    } finally {
      reply.send(code);
    }
  }, result.sendPort);
  final code = await result.first as int;
  result.close();
  return code;
}

/// Entry point of the packet-pump isolate.
///
/// It exists so the blocking native read never runs on the UI isolate. The
/// handle is a plain integer, so it crosses the isolate boundary unchanged.
void _packetPumpMain(_PumpConfig config) {
  final buffer = calloc<Uint8>(config.bufferSize);
  try {
    config.ready.send(Isolate.current.controlPort);
    while (true) {
      // A timeout keeps the loop responsive to the stop sentinel.
      final n = native.zcReadPacket(
        config.handle,
        buffer,
        config.bufferSize,
        250,
      );
      if (n > 0) {
        config.sink.add(Uint8List.fromList(buffer.asTypedList(n)));
        continue;
      }
      if (n == native.ZC_ERR_TIMEOUT) continue;
      if (n == native.ZC_ERR_CLOSED) return;
      // Any other negative code is unexpected; report and stop rather than spin.
      config.sink.addError(VpnException('packet read failed (native code $n)'));
      return;
    }
  } finally {
    calloc.free(buffer);
  }
}

class _EventMessage {
  const _EventMessage(this.type, this.text);
  final int type;
  final String text;
}

class _ChallengeMessage {
  const _ChallengeMessage(this.id, this.payload);
  final int id;
  final String payload;
}

/// Configuration handed to the packet-pump isolate.
class _PumpConfig {
  const _PumpConfig(this.handle, this.bufferSize, this.sink, this.ready);
  final int handle;
  final int bufferSize;
  final StreamSink<Uint8List> sink;
  final SendPort ready;
}

class _PumpStop {
  const _PumpStop();
}
