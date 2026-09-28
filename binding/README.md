# binding

`binding` exposes the zju-connect core as an embeddable library. It is the
foundation for the Flutter/Dart package, but it is plain Go and can be used from
any host.

It contains three layers:

| Layer | Path | Purpose |
| --- | --- | --- |
| Core | `binding/*.go` | Pure Go, cgo-free. Session lifecycle, packet and proxy data planes, challenge bridging. |
| C ABI | `binding/capi` | `main` package compiled with `-buildmode=c-shared`. Thin `//export` wrappers around the core. |
| Header | `binding/capi/include/zju_connect.h` | Stable C declarations for `ffigen`. |

## Why two data planes

The same session can drive traffic in two ways:

- **Packet mode** (`ReadPacket` / `WritePacket`): the host owns a TUN device
  (Android `VpnService`, iOS `NEPacketTunnelProvider`, a desktop tun) and pumps
  raw IPv4 packets. This is what a normal "VPN" app does.
- **Proxy mode** (`StartProxy`): the library runs a userspace gVisor TCP/IP
  stack plus local SOCKS5/HTTP proxies. No tun device, no root, works on every
  platform. This is what the CLI does by default and is the easiest way to get
  a working build before the platform VPN service is written.

Only packet mode and proxy mode need the network to reach the VPN server; they
share the same connected session.

## Authentication challenges

Interactive authentication (SMS codes, TOTP, captcha, OAuth2) is a **two-phase**
flow, because a Dart `NativeCallable.listener` cannot return a value
synchronously:

1. The library calls the host's challenge callback with a `challenge_id` and a
   JSON payload.
2. The host shows its UI and later calls `zcRespondChallenge(id, json)` from any
   thread.

The payload/response schema per kind is documented on
`binding.Client.RespondChallenge`.

## Building

```sh
# Host platform shared library -> build/
binding/build.sh host

# Android (all ABIs), needs ANDROID_NDK_HOME
binding/build.sh android

# iOS static archive, must run on macOS
binding/build.sh ios
```

Requirements:

- `CGO_ENABLED=1` and a C compiler for the target.
- Android: NDK r26+ (16 KB page alignment is applied automatically).
- iOS: macOS with Xcode, since there is no `-buildmode=c-shared` on iOS.

### Output layout

Every artifact goes to `build/native/<os>-<arch>/`, the layout the Dart build
hook looks for, plus a convenience copy at `build/native/host/` for the smoke
test. A header is copied alongside each library.

```
build/native/windows-x64/zju_connect.dll
build/native/linux-x64/libzju_connect.so
build/native/linux-arm64/libzju_connect.so
build/native/macos-x64/libzju_connect.dylib
build/native/macos-arm64/libzju_connect.dylib
build/native/android-arm64/libzju_connect.so
build/native/android-arm/libzju_connect.so
build/native/android-ia32/libzju_connect.so
build/native/ios-arm64/libzju_connect.a      # static archive
build/native/ios-x64/libzju_connect.a        # simulator
```

Override the root with `OUT_DIR`.

A single `host` build takes about a minute; a full `all` build needs both an
Android NDK and macOS, which is why CI runs the targets on separate runners
(`.github/workflows/build-native.yml`).

## Smoke test

The C ABI has a dependency-free smoke test that exercises argument validation
and handle lifecycle (no live server required):

```sh
binding/build.sh host
cc -I build/native/host -o build/smoke binding/capi/test/smoke.c \
   -L build/native/host -lzju_connect -Wl,-rpath,build/native/host
./build/smoke
```

On Windows the library is `zju_connect.dll`; put `build/native/host` on `PATH`.

## Notes for host authors

- Persist `zcStatJSON`'s `session_id` (easyconnect) and `client_data` (aTrust)
  and feed them back through the config to resume without a full login.
- `zcSetup`, `zcReadPacket` and `zcStartProxy` block; call them off the UI
  thread.
- Strings passed to the event and challenge callbacks transfer ownership to the
  host, which must release them with `zcFreeString`. This is required because a
  Dart `NativeCallable.listener` decodes the string asynchronously.
- `zcClose` tears down the session; `zcFree` releases the handle. Call both.
