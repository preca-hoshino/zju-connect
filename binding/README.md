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
- The Android artifacts go to `build/android/<abi>/libzju_connect.so`; iOS to
  `build/ios/<arch>/libzju_connect.a`.

## Smoke test

The C ABI has a dependency-free smoke test that exercises argument validation
and handle lifecycle (no live server required):

```sh
go build -buildmode=c-shared -o build/libzju_connect.so ./binding/capi
cc -I binding/capi/include -o build/smoke binding/capi/test/smoke.c \
   -L build -lzju_connect -Wl,-rpath,build
./build/smoke
```

On Windows use `libzju_connect.dll` and put `build/` on `PATH`.

## Notes for host authors

- Persist `zcStatJSON`'s `session_id` (easyconnect) and `client_data` (aTrust)
  and feed them back through the config to resume without a full login.
- `zcSetup`, `zcReadPacket` and `zcStartProxy` block; call them off the UI
  thread.
- `zcClose` tears down the session; `zcFree` releases the handle. Call both.
