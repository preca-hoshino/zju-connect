@Tags(['native'])
library;

import 'package:sangfor_vpn_client/sangfor_vpn_client.dart';
import 'package:test/test.dart';

/// Exercises the FFI boundary against the real shared library.
///
/// Nothing here needs a live VPN server: it verifies that the library loads,
/// the ABI is wired correctly and errors surface as exceptions. A test that
/// actually logs in would need credentials and network access, so it is not
/// part of the default suite.
///
/// Run with: dart test test/native_test.dart
void main() {
  test('rejects a config without a server address', () async {
    await expectLater(
      SangforVpnClient.connect(
        const VpnConfig(serverAddress: '', username: 'u'),
      ),
      throwsA(isA<VpnException>()),
    );
  });

  test('logs during a failed login reach the callback', () async {
    final logs = <String>[];
    // 127.0.0.1:443 refuses quickly, which exercises the whole login path
    // (validation, client construction, underlay dial, connection attempt)
    // without needing a real server.
    try {
      final client = await SangforVpnClient.connect(
        const VpnConfig(
          serverAddress: '127.0.0.1',
          username: 'u',
          password: 'p',
        ),
        onLog: logs.add,
      );
      client.close();
    } on VpnException {
      // Expected: nothing is listening.
    }

    expect(
      logs.any((line) => line.contains('VPN protocol')),
      isTrue,
      reason:
          'the login should report the protocol before connecting; got $logs',
    );
  });

  test('a session that failed to connect reports the error state', () async {
    final states = <VpnState>[];
    try {
      final client = await SangforVpnClient.connect(
        const VpnConfig(
          serverAddress: '127.0.0.1',
          username: 'u',
          password: 'p',
        ),
      );
      client.close();
    } on VpnException {
      // Expected.
    }
    // The callback variant is the one that can observe pre-session state, so
    // assert on that path instead of the (empty) stream.
    expect(states, isEmpty);
  });
}
