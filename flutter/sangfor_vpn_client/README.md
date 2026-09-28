# sangfor_vpn_client

Dart/Flutter bindings for the [zju-connect](https://github.com/preca-hoshino/zju-connect)
VPN core, which speaks Sangfor **EasyConnect** and **aTrust**.

The native code is Go, compiled to a shared library. Two data planes are
available on one session:

| Plane | API | Requirements |
| --- | --- | --- |
| Proxy | `SangforVpnClient.startProxy()` | None. Runs a userspace TCP/IP stack with local SOCKS5/HTTP proxies. Works on every platform and is the quickest integration path. |
| Packet | `SangforVpnClient.packets` / `writePacket` | A platform tun device: Android `VpnService`, iOS `NEPacketTunnelProvider`. This is what a normal VPN app does. |

## Quick start

```dart
import 'package:sangfor_vpn_client/sangfor_vpn_client.dart';

final client = await SangforVpnClient.connect(
  const VpnConfig(
    serverAddress: 'vpn.example.edu',
    username: 'student',
    password: 'secret',
  ),
  onLog: (line) => debugPrint(line),
  onChallenge: (challenge) {
    // Ask the user, then answer. See "Authentication" below.
  },
);

final proxy = await client.startProxy();
print('SOCKS5 proxy on ${proxy.socks}');

// ... use the proxy ...

client.close();
```

`startProxy` returns addresses like `127.0.0.1:41234`. Point your HTTP client at
`ProxyAddrs.http`, or a SOCKS5-capable client at `ProxyAddrs.socks`.

### Packet mode

```dart
final client = await SangforVpnClient.connect(config);

// Feed the platform VpnService/NEPacketTunnelProvider:
client.packets().listen((packet) => tun.write(packet));

// And write what the tun device reads back into the tunnel:
client.writePacket(packetFromTun);
```

`packets()` reads on a dedicated isolate, so it never blocks the UI isolate.

## Authentication

Interactive login (SMS codes, TOTP, captcha, SSO) is a two-phase flow, because
the VPN server's challenge arrives while `connect` is still running:

1. The challenge is delivered to `onChallenge` and the `challenges` stream.
2. Your app answers it, from any thread, with a typed helper on the client.

```dart
final client = await SangforVpnClient.connect(
  config,
  onChallenge: (challenge) async {
    switch (challenge) {
      case CodeChallenge():
        final code = await promptUser(challenge.message);
        client.respondToCode(challenge.id, code);
      case TextCaptchaChallenge():
        final text = await showCaptcha(challenge.image);
        client.respondToCaptcha(challenge.id, text);
      case ClickCaptchaChallenge():
        final taps = await showClickCaptcha(challenge.image);
        client.respondToClickCaptcha(
          challenge.id,
          taps,
          width: challenge.width,
          height: challenge.height,
        );
      case ExternalLoginChallenge():
        final callback = await openWebView(challenge.loginUrl);
        client.respondToExternalLogin(challenge.id, callback);
    }
  },
);
```

> The `onChallenge` callback fires **before** `connect` returns, so it is the
> only way to answer a challenge for the initial login. The `challenges` stream
> covers anything the server asks for later.

To abort a login, call `client.cancelChallenge(id, 'user cancelled')`.

## Session resumption

Persist the resume material so the next launch skips authentication:

```dart
final status = client.status();
await storage.write('session', status.sessionId);   // EasyConnect
await storage.write('client_data', status.clientData); // aTrust
```

Feed it back through `VpnConfig(sessionId: ...)` or
`VpnConfig(sid: ..., deviceId: ..., clientData: ..., resourceData: ...)`.

## Platform notes

**Android.** Add the `INTERNET` permission. For packet mode the app needs a
`VpnService` and its `android.permission.BIND_VPN_SERVICE` declaration; proxy
mode needs neither.

**iOS.** The Go code is built as a static archive and linked into the app, so
`packets()` must be driven by a `NEPacketTunnelProvider` extension. Proxy mode
works in the main app.

**Desktop.** Both modes work, but packet mode needs a tun device and therefore
administrator/root privileges. Proxy mode does not.

## Getting the native library

The `hook/build.dart` hook locates a prebuilt library; it does not compile Go.
Build one from the repository root first:

```sh
binding/build.sh host      # current platform
binding/build.sh android   # all Android ABIs
binding/build.sh ios       # iOS static archives
```

The hook looks for the artifact, in order:

1. `hooks.user_defines.sangfor_vpn_client.library.<os>-<arch>` in the consuming
   app's `pubspec.yaml`.
2. The `ZJU_CONNECT_LIBRARY_DIR` environment variable.
3. `native/<os>-<arch>/` inside this package (for a checked-in or pre-downloaded
   artifact).
4. The repository's `build/` directory, for a developer host build.

If none is found, the build fails with instructions rather than at runtime with
a missing symbol.

## Regenerating the FFI bindings

The bindings in `lib/zju_connect_bindings_generated.dart` are generated from the
C ABI header by `ffigen`, which needs `libclang`:

```sh
dart run ffigen --config ffigen.yaml
```

`ffigen.yaml` points at `../../binding/capi/include/zju_connect.h`, which is the
single source of truth shared with the Go side. Set `llvm-path` in that file (or
install LLVM to its default location) if `libclang` is not on the search path.

## Tests

```sh
dart test                       # everything
dart test --exclude-tags native # skip tests that load the shared library
```

The `native` tests exercise the real FFI boundary but do not require a VPN
account: they cover argument validation, error propagation and event delivery.
