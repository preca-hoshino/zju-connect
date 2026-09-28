import 'dart:convert';
import 'dart:typed_data';

/// Which VPN implementation to use.
enum VpnProtocol {
  /// Sangfor EasyConnect (the classic `rvpn` protocol).
  easyConnect('easyconnect'),

  /// Sangfor aTrust.
  aTrust('atrust');

  const VpnProtocol(this.wireName);

  /// The value understood by the native library.
  final String wireName;
}

/// Lifecycle state of a [SangforVpnClient].
enum VpnState {
  /// No session has been established yet.
  connecting('connecting'),

  /// The session is up and traffic can flow.
  connected('connected'),

  /// The session was closed.
  disconnected('disconnected'),

  /// The session ended because of an error; see the error stream.
  error('error');

  const VpnState(this.wireName);

  /// The value understood by the native library.
  final String wireName;

  /// Parses a state reported by the native library.
  static VpnState fromWireName(String value) {
    for (final state in VpnState.values) {
      if (state.wireName == value) return state;
    }
    return VpnState.disconnected;
  }
}

/// A custom DNS entry mapping a host name to a fixed IP.
class CustomDnsEntry {
  const CustomDnsEntry({required this.hostName, required this.ip});

  final String hostName;
  final String ip;

  Map<String, Object?> toJson() => {'host_name': hostName, 'ip': ip};
}

/// Configuration for a VPN session.
///
/// Only [serverAddress] and [username] are required; every other field falls
/// back to the same default the CLI uses.
class VpnConfig {
  const VpnConfig({
    required this.serverAddress,
    required this.username,
    this.protocol = VpnProtocol.easyConnect,
    this.serverPort = 443,
    this.password = '',
    this.totpSecret = '',
    this.certFile = '',
    this.certPassword = '',
    this.sessionId = '',
    this.sid = '',
    this.deviceId = '',
    this.signKey = '',
    this.clientData = '',
    this.resourceData = '',
    this.clientDataFile = '',
    this.resourceFile = '',
    this.authType = '',
    this.phone = '',
    this.loginDomain = '',
    this.casTicket = '',
    this.oauth2Code = '',
    this.graphCodeFile = '',
    this.disableServerConfig = false,
    this.skipDomainResource = false,
    this.disableMultiLine = false,
    this.enableZjuCompat = false,
    this.customProxyDomain = const [],
    this.bindInterface = '',
    this.autoDetectInterface = true,
    this.localDnsServer = '',
    this.dialDirectProxy = '',
    this.proxyAll = false,
    this.disableRemoteDns = false,
    this.remoteDnsServer = '',
    this.secondaryDnsServer = '',
    this.dnsTtl = 3600,
    this.customDns = const [],
    this.debugDump = false,
    this.debugPcapFile = '',
    this.debugTlsLogFile = '',
    this.updateBestNodesIntervalSeconds = 300,
    this.sessionRefreshIntervalSeconds = 1800,
  });

  /// EasyConnect or aTrust server host, without a scheme or port.
  final String serverAddress;

  /// VPN account name.
  final String username;

  final VpnProtocol protocol;
  final int serverPort;
  final String password;
  final String totpSecret;

  /// PKCS#12 (.p12/.pfx) certificate path for EasyConnect certificate login.
  final String certFile;
  final String certPassword;

  /// EasyConnect session token (TwfID) captured from a previous login, used to
  /// resume without authenticating again.
  final String sessionId;

  /// aTrust session resume material. [clientData] comes from
  /// [VpnStatus.clientData] of a previous session.
  final String sid;
  final String deviceId;
  final String signKey;
  final String clientData;

  /// The aTrust resource blob from a previous session. Persist it alongside
  /// [clientData] to skip fetching resources again.
  final String resourceData;

  /// File-based variants of [clientData] and [resourceData], for parity with
  /// the CLI. Prefer the in-memory fields on mobile.
  final String clientDataFile;
  final String resourceFile;

  /// aTrust authentication type, e.g. `auth/psw`, `auth/cas`,
  /// `auth/httpsOauth2`, `auth/smsCheckCode`. Empty auto-detects.
  final String authType;
  final String phone;
  final String loginDomain;
  final String casTicket;
  final String oauth2Code;
  final String graphCodeFile;

  final bool disableServerConfig;
  final bool skipDomainResource;
  final bool disableMultiLine;

  /// EasyConnect only. Force-routes 10.0.0.0/8 and zju.edu.cn regardless of
  /// what the server advertises. Off by default.
  final bool enableZjuCompat;

  /// Domains that must always go through the VPN.
  final List<String> customProxyDomain;

  /// Underlay interface settings for reaching the VPN server.
  final String bindInterface;
  final bool autoDetectInterface;
  final String localDnsServer;

  /// Optional proxy used for connections that do not match VPN rules, in
  /// `http://host:port` or `socks://host:port` form.
  final String dialDirectProxy;

  /// Send all traffic through the VPN (debugging only).
  final bool proxyAll;

  final bool disableRemoteDns;
  final String remoteDnsServer;
  final String secondaryDnsServer;
  final int dnsTtl;
  final List<CustomDnsEntry> customDns;

  final bool debugDump;
  final String debugPcapFile;
  final String debugTlsLogFile;

  /// How often aTrust refreshes its best-node list. Zero disables it.
  final int updateBestNodesIntervalSeconds;

  /// How often aTrust refreshes the session. Zero disables it.
  final int sessionRefreshIntervalSeconds;

  /// Encodes this configuration for the native library.
  Map<String, Object?> toJson() => {
    'protocol': protocol.wireName,
    'server_address': serverAddress,
    'server_port': serverPort,
    'username': username,
    'password': password,
    'totp_secret': totpSecret,
    'cert_file': certFile,
    'cert_password': certPassword,
    'session_id': sessionId,
    'sid': sid,
    'device_id': deviceId,
    'sign_key': signKey,
    'client_data': clientData,
    'resource_data': resourceData,
    'client_data_file': clientDataFile,
    'resource_file': resourceFile,
    'auth_type': authType,
    'phone': phone,
    'login_domain': loginDomain,
    'cas_ticket': casTicket,
    'oauth2_code': oauth2Code,
    'graph_code_file': graphCodeFile,
    'disable_server_config': disableServerConfig,
    'skip_domain_resource': skipDomainResource,
    'disable_multi_line': disableMultiLine,
    'enable_zju_compat': enableZjuCompat,
    'custom_proxy_domain': customProxyDomain,
    'bind_interface': bindInterface,
    'auto_detect_interface': autoDetectInterface,
    'local_dns_server': localDnsServer,
    'dial_direct_proxy': dialDirectProxy,
    'proxy_all': proxyAll,
    'disable_remote_dns': disableRemoteDns,
    'remote_dns_server': remoteDnsServer,
    'secondary_dns_server': secondaryDnsServer,
    'dns_ttl': dnsTtl,
    'custom_dns': customDns.map((entry) => entry.toJson()).toList(),
    'debug_dump': debugDump,
    'debug_pcap_file': debugPcapFile,
    'debug_tls_log_file': debugTlsLogFile,
    'update_best_nodes_interval': updateBestNodesIntervalSeconds,
    'session_refresh_interval': sessionRefreshIntervalSeconds,
  };
}

/// A snapshot of a session's state.
class VpnStatus {
  const VpnStatus({
    required this.state,
    required this.virtualIp,
    required this.sessionId,
    required this.clientData,
  });

  final VpnState state;

  /// The IPv4 address the server assigned, if connected.
  final String virtualIp;

  /// EasyConnect session token, to be persisted and reused through
  /// [VpnConfig.sessionId].
  final String sessionId;

  /// aTrust client-data blob, to be persisted and reused through
  /// [VpnConfig.clientData].
  final String clientData;

  factory VpnStatus.fromJson(Map<String, Object?> json) => VpnStatus(
    state: VpnState.fromWireName(json['state'] as String? ?? ''),
    virtualIp: json['virtual_ip'] as String? ?? '',
    sessionId: json['session_id'] as String? ?? '',
    clientData: json['client_data'] as String? ?? '',
  );
}

/// The kind of an interactive authentication challenge.
enum VpnChallengeKind {
  /// A one-time code (SMS, TOTP, RADIUS).
  code,

  /// A text captcha: the app shows [CaptchaChallenge.image] and asks the user
  /// to type the characters.
  textCaptcha,

  /// A click captcha: the app shows the image and collects taps.
  clickCaptcha,

  /// An external login page (CAS or OAuth2): the app opens
  /// [ExternalLoginChallenge.loginUrl], lets the user sign in and reports the
  /// resulting callback URL.
  externalLogin,
}

/// A challenge the app must answer through
/// [SangforVpnClient.respondToChallenge].
///
/// Every challenge carries an [id]; the answer must use the same id.
sealed class VpnChallenge {
  const VpnChallenge({required this.id});

  /// Identifier to pass back to [SangforVpnClient.respondToChallenge].
  final int id;

  /// Parses a challenge from the native library's payload.
  static VpnChallenge fromNative(
    int id,
    String kind,
    Map<String, Object?> payload,
  ) {
    switch (kind) {
      case 'code':
        return CodeChallenge(
          id: id,
          kind: payload['kind'] as String? ?? '',
          message: payload['message'] as String? ?? '',
          canSkipSecondaryAuth:
              payload['can_skip_secondary_auth'] as bool? ?? false,
        );
      case 'text_captcha':
        return TextCaptchaChallenge(
          id: id,
          message: payload['message'] as String? ?? '',
          image: _decodeImage(payload['image_base64']),
        );
      case 'click_captcha':
        return ClickCaptchaChallenge(
          id: id,
          message: payload['message'] as String? ?? '',
          image: _decodeImage(payload['image_base64']),
        );
      case 'external_login':
        return ExternalLoginChallenge(
          id: id,
          kind: payload['kind'] as String? ?? '',
          loginUrl: payload['login_url'] as String? ?? '',
          message: payload['message'] as String? ?? '',
        );
      default:
        throw FormatException('Unknown challenge kind: $kind');
    }
  }

  static Uint8List _decodeImage(Object? value) {
    if (value is! String || value.isEmpty) return Uint8List(0);
    return base64Decode(value);
  }
}

/// A request for a one-time code (SMS, TOTP or RADIUS).
class CodeChallenge extends VpnChallenge {
  const CodeChallenge({
    required super.id,
    required this.kind,
    required this.message,
    required this.canSkipSecondaryAuth,
  });

  /// `sms`, `totp` or `radius`.
  final String kind;

  /// Prompt to show the user.
  final String message;

  /// When true the user may skip the secondary authentication step.
  final bool canSkipSecondaryAuth;
}

/// A captcha the user reads and types.
class TextCaptchaChallenge extends VpnChallenge {
  const TextCaptchaChallenge({
    required super.id,
    required this.message,
    required this.image,
  });

  final String message;

  /// PNG/JPEG bytes to render.
  final Uint8List image;
}

/// A captcha the user clicks.
class ClickCaptchaChallenge extends VpnChallenge {
  const ClickCaptchaChallenge({
    required super.id,
    required this.message,
    required this.image,
  });

  final String message;
  final Uint8List image;
}

/// An external (CAS/OAuth2) login page.
class ExternalLoginChallenge extends VpnChallenge {
  const ExternalLoginChallenge({
    required super.id,
    required this.kind,
    required this.loginUrl,
    required this.message,
  });

  /// `cas` or `oauth2`.
  final String kind;

  /// The page the app should open in a web view.
  final String loginUrl;

  final String message;
}

/// Where the local proxies are listening, as returned by
/// [SangforVpnClient.startProxy].
class ProxyAddrs {
  const ProxyAddrs({required this.socks, required this.http});

  /// SOCKS5 address, e.g. `127.0.0.1:1234`. Empty when disabled.
  final String socks;

  /// HTTP/HTTPS proxy address. Empty when disabled.
  final String http;

  factory ProxyAddrs.fromJson(Map<String, Object?> json) => ProxyAddrs(
    socks: json['socks'] as String? ?? '',
    http: json['http'] as String? ?? '',
  );
}

/// Options for [SangforVpnClient.startProxy].
///
/// Addresses are `host:port`. A port of `0` asks the OS for a free port; an
/// empty address uses the default, and a negative port disables that listener.
class ProxyOptions {
  const ProxyOptions({
    this.socksBind = '127.0.0.1:0',
    this.httpBind = '',
    this.socksUser = '',
    this.socksPassword = '',
  });

  /// SOCKS5 listener. Enabled by default on an OS-assigned port.
  final String socksBind;

  /// HTTP proxy listener. Disabled by default.
  final String httpBind;

  final String socksUser;
  final String socksPassword;

  Map<String, Object?> toJson() => {
    'socks_bind': socksBind,
    'http_bind': httpBind,
    'socks_user': socksUser,
    'socks_passwd': socksPassword,
  };
}
