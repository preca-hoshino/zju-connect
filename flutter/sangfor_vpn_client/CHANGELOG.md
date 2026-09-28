## 0.1.0

Initial implementation.

- `SangforVpnClient.connect` performs an EasyConnect or aTrust login and returns
  a live session, with `onLog` / `onError` / `onChallenge` callbacks that fire
  while the login is still running.
- Proxy data plane: `startProxy` runs local SOCKS5 and HTTP proxies over a
  userspace stack, with no privileges required.
- Packet data plane: `packets` and `writePacket` move raw IPv4 packets for a
  platform tun device, with reads on a dedicated isolate.
- Two-phase authentication for SMS/TOTP codes, text and click captchas and
  CAS/OAuth2 login pages.
- `hook/build.dart` locates the prebuilt Go library per target instead of
  compiling C, and reports a clear error when none is available.
- ffigen bindings generated from `binding/capi/include/zju_connect.h`.
