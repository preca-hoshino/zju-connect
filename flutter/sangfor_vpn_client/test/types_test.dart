import 'dart:convert';

import 'package:sangfor_vpn_client/sangfor_vpn_client.dart';
import 'package:test/test.dart';

void main() {
  group('VpnConfig', () {
    test('serialises to the native schema', () {
      final json = VpnConfig(
        serverAddress: 'vpn.example.edu',
        username: 'student',
        password: 'secret',
      ).toJson();

      expect(json['protocol'], 'easyconnect');
      expect(json['server_address'], 'vpn.example.edu');
      expect(json['server_port'], 443);
      expect(json['username'], 'student');
      expect(json['password'], 'secret');
      // The ZJU routing shim must stay opt-in.
      expect(json['enable_zju_compat'], false);
    });

    test('uses the aTrust wire name', () {
      final json = VpnConfig(
        serverAddress: 'vpn.example.edu',
        username: 'u',
        protocol: VpnProtocol.aTrust,
      ).toJson();
      expect(json['protocol'], 'atrust');
    });

    test('serialises custom DNS entries', () {
      final json = VpnConfig(
        serverAddress: 'vpn',
        username: 'u',
        customDns: const [
          CustomDnsEntry(hostName: 'a.example', ip: '10.0.0.1'),
        ],
      ).toJson();
      expect(json['custom_dns'], [
        {'host_name': 'a.example', 'ip': '10.0.0.1'},
      ]);
    });
  });

  group('ProxyOptions', () {
    test('defaults to an OS-assigned SOCKS5 port and no HTTP proxy', () {
      final json = const ProxyOptions().toJson();
      expect(json['socks_bind'], '127.0.0.1:0');
      expect(json['http_bind'], '');
    });
  });

  group('VpnState', () {
    test('maps native names', () {
      expect(VpnState.fromWireName('connected'), VpnState.connected);
      expect(VpnState.fromWireName('connecting'), VpnState.connecting);
      expect(VpnState.fromWireName('disconnected'), VpnState.disconnected);
      expect(VpnState.fromWireName('error'), VpnState.error);
    });

    test('falls back to disconnected for unknown names', () {
      expect(VpnState.fromWireName('nonsense'), VpnState.disconnected);
      expect(VpnState.fromWireName(''), VpnState.disconnected);
    });
  });

  group('VpnStatus', () {
    test('parses the native JSON shape', () {
      final status = VpnStatus.fromJson({
        'state': 'connected',
        'virtual_ip': '10.1.2.3',
        'session_id': 'abc',
        'client_data': '{"a":1}',
      });
      expect(status.state, VpnState.connected);
      expect(status.virtualIp, '10.1.2.3');
      expect(status.sessionId, 'abc');
      expect(status.clientData, '{"a":1}');
    });

    test('tolerates missing fields', () {
      final status = VpnStatus.fromJson(const {});
      expect(status.state, VpnState.disconnected);
      expect(status.virtualIp, '');
    });
  });

  group('VpnChallenge', () {
    test('parses a code challenge', () {
      final challenge = VpnChallenge.fromNative(7, 'code', {
        'kind': 'sms',
        'message': 'Enter the code',
        'can_skip_secondary_auth': true,
      }) as CodeChallenge;

      expect(challenge.id, 7);
      expect(challenge.kind, 'sms');
      expect(challenge.message, 'Enter the code');
      expect(challenge.canSkipSecondaryAuth, isTrue);
    });

    test('parses a text captcha and decodes the image', () {
      final challenge = VpnChallenge.fromNative(3, 'text_captcha', {
        'message': 'Type the characters',
        'image_base64': base64Encode([1, 2, 3]),
      }) as TextCaptchaChallenge;

      expect(challenge.id, 3);
      expect(challenge.image, [1, 2, 3]);
    });

    test('parses a click captcha', () {
      final challenge = VpnChallenge.fromNative(4, 'click_captcha', {
        'message': 'Tap the characters',
        'image_base64': base64Encode([9]),
      });
      expect(challenge, isA<ClickCaptchaChallenge>());
    });

    test('parses an external login challenge', () {
      final challenge = VpnChallenge.fromNative(5, 'external_login', {
        'kind': 'oauth2',
        'login_url': 'https://idp.example/login',
        'message': 'Sign in',
      }) as ExternalLoginChallenge;

      expect(challenge.kind, 'oauth2');
      expect(challenge.loginUrl, 'https://idp.example/login');
    });

    test('rejects an unknown kind', () {
      expect(
        () => VpnChallenge.fromNative(1, 'telepathy', const {}),
        throwsA(isA<FormatException>()),
      );
    });
  });

  group('ProxyAddrs', () {
    test('parses the native JSON shape', () {
      final addrs = ProxyAddrs.fromJson(const {
        'socks': '127.0.0.1:1234',
        'http': '',
      });
      expect(addrs.socks, '127.0.0.1:1234');
      expect(addrs.http, '');
    });
  });
}
