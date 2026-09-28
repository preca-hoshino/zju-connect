/// Dart/Flutter bindings for the zju-connect VPN core.
///
/// The package wraps a Go library that speaks Sangfor EasyConnect and aTrust.
/// Two data planes are available on the same session:
///
///  * **Proxy mode** — [SangforVpnClient.startProxy] brings up local SOCKS5 and
///    HTTP proxies. It needs no privileges and works everywhere, so it is the
///    quickest integration path.
///  * **Packet mode** — [SangforVpnClient.packets] and
///    [SangforVpnClient.writePacket] move raw IPv4 packets, to be wired into a
///    platform tun device (Android `VpnService`, iOS `NEPacketTunnelProvider`).
///
/// ```dart
/// final client = await SangforVpnClient.connect(
///   VpnConfig(
///     serverAddress: 'vpn.example.edu',
///     username: 'student',
///     password: 'secret',
///   ),
/// );
///
/// client.challenges.listen((challenge) {
///   if (challenge is CodeChallenge) {
///     client.respondToCode(challenge.id, '123456');
///   }
/// });
///
/// final proxy = await client.startProxy();
/// print('SOCKS5 on ${proxy.socks}');
/// ```
library;

export 'src/client.dart' show SangforVpnClient, VpnException;
export 'src/types.dart';
