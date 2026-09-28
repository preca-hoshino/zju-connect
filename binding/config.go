// Package binding exposes the zju-connect core as an embeddable library.
//
// It is deliberately free of cgo so it can be unit tested and reused by any Go
// host (including the mobile/ package). The C ABI consumed by Dart lives in the
// binding/capi sub-package.
//
// Two data planes are supported:
//
//   - Packet mode: the host owns a TUN device (Android VpnService, iOS
//     NEPacketTunnelProvider, a desktop tun) and pumps raw IPv4 packets through
//     ReadPacket/WritePacket.
//   - Proxy mode: the library runs a userspace gVisor stack plus local SOCKS5
//     and HTTP proxies, which needs no special privileges and works everywhere.
package binding

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/configs"
)

// Config is the library-facing configuration. It mirrors the CLI config file
// but is decoded from a single JSON string so it can cross the C ABI.
//
// Fields left at their JSON zero value fall back to the same defaults the CLI
// uses. ServerAddress and Username are required.
type Config struct {
	// Protocol selects the VPN implementation: "easyconnect" (Sangfor
	// EasyConnect) or "atrust" (Sangfor aTrust). Defaults to "easyconnect".
	Protocol string `json:"protocol"`

	ServerAddress string `json:"server_address"`
	ServerPort    int    `json:"server_port"`

	Username   string `json:"username"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret"`

	// CertFile / CertPassword configure certificate login for easyconnect.
	// CertFile is a PKCS#12 (.p12/.pfx) path.
	CertFile     string `json:"cert_file"`
	CertPassword string `json:"cert_password"`

	// SessionID resumes a previous easyconnect session (the "TwfID").
	// Retrieve the value of a successful login with Client.SessionID.
	SessionID string `json:"session_id"`

	// aTrust session resume material. SID/DeviceID/SignKey come from a previous
	// successful Setup; ClientData and ResourceData are opaque JSON blobs the
	// host should persist and feed back on the next launch.
	SID          string `json:"sid"`
	DeviceID     string `json:"device_id"`
	SignKey      string `json:"sign_key"`
	ClientData   string `json:"client_data"`
	ResourceData string `json:"resource_data"`

	// Legacy file-based variants, kept for parity with the CLI.
	ClientDataFile string `json:"client_data_file"`
	ResourceFile   string `json:"resource_file"`

	// aTrust authentication options.
	AuthType      string `json:"auth_type"`
	Phone         string `json:"phone"`
	LoginDomain   string `json:"login_domain"`
	CASTicket     string `json:"cas_ticket"`
	OAuth2Code    string `json:"oauth2_code"`
	GraphCodeFile string `json:"graph_code_file"`

	// Resource handling.
	DisableServerConfig bool `json:"disable_server_config"`
	SkipDomainResource  bool `json:"skip_domain_resource"`
	DisableMultiLine    bool `json:"disable_multi_line"`

	// EnableZJUCompat turns on the ZJU-specific shim: it force-routes
	// 10.0.0.0/8 and zju.edu.cn regardless of what the server advertises.
	// It is off by default because it is meaningless (and harmful) outside ZJU.
	// easyconnect only.
	EnableZJUCompat bool `json:"enable_zju_compat"`

	// CustomProxyDomain forces the listed domains through the VPN.
	CustomProxyDomain []string `json:"custom_proxy_domain"`

	// Networking.
	BindInterface       string `json:"bind_interface"`
	AutoDetectInterface *bool  `json:"auto_detect_interface"`
	LocalDNSServer      string `json:"local_dns_server"`
	DialDirectProxy     string `json:"dial_direct_proxy"`
	ProxyAll            bool   `json:"proxy_all"`

	// DNS.
	DisableRemoteDNS   bool        `json:"disable_remote_dns"`
	RemoteDNSServer    string      `json:"remote_dns_server"`
	SecondaryDNSServer string      `json:"secondary_dns_server"`
	DNSTTL             uint64      `json:"dns_ttl"`
	CustomDNS          []CustomDNS `json:"custom_dns"`

	// Proxy mode listeners. A port of 0 asks the OS for a free port; a negative
	// port disables that listener.
	SocksBind   string `json:"socks_bind"` // ignored by StartProxy, kept for parity
	SocksUser   string `json:"socks_user"`
	SocksPasswd string `json:"socks_passwd"`

	// Diagnostics.
	DebugDump       bool   `json:"debug_dump"`
	DebugPCAPFile   string `json:"debug_pcap_file"`
	DebugTLSLogFile string `json:"debug_tls_log_file"`

	// aTrust background refresh intervals.
	UpdateBestNodesInterval int `json:"update_best_nodes_interval"`
	SessionRefreshInterval  int `json:"session_refresh_interval"`
}

// CustomDNS is a single permanent host-name to IP mapping.
type CustomDNS struct {
	HostName string `json:"host_name"`
	IP       string `json:"ip"`
}

// ParseConfig decodes a JSON configuration and applies CLI-compatible defaults.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	if len(data) > 0 {
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config JSON: %w", err)
		}
	}
	if err := cfg.applyDefaults(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() error {
	if c.Protocol == "" {
		c.Protocol = "easyconnect"
	}
	switch c.Protocol {
	case "easyconnect", "atrust":
	default:
		return fmt.Errorf("unsupported protocol %q (want easyconnect or atrust)", c.Protocol)
	}

	if c.ServerAddress == "" {
		return fmt.Errorf("server_address is required")
	}
	if c.ServerPort == 0 {
		c.ServerPort = 443
	}
	if c.DNSTTL == 0 {
		c.DNSTTL = 3600
	}
	if c.RemoteDNSServer == "" {
		c.RemoteDNSServer = "auto"
	}
	if c.SecondaryDNSServer == "" {
		c.SecondaryDNSServer = "auto"
	}
	if c.LoginDomain == "" {
		c.LoginDomain = "Radius"
	}
	if c.UpdateBestNodesInterval == 0 {
		c.UpdateBestNodesInterval = 300
	}
	if c.SessionRefreshInterval == 0 {
		c.SessionRefreshInterval = 1800
	}
	return nil
}

// autoDetectInterface reports whether the underlay interface should be picked
// automatically. It defaults to true when the setting is absent.
func (c Config) autoDetectInterface() bool {
	if c.AutoDetectInterface == nil {
		return true
	}
	return *c.AutoDetectInterface
}

// toConfigs converts the library configuration into the internal representation
// used by the rest of the code base.
func (c Config) toConfigs() configs.Config {
	cfg := configs.Default()
	cfg.Protocol = c.Protocol
	cfg.ServerAddress = c.ServerAddress
	cfg.ServerPort = c.ServerPort
	cfg.Username = c.Username
	cfg.Password = c.Password
	cfg.TOTPSecret = c.TOTPSecret
	cfg.CertFile = c.CertFile
	cfg.CertPassword = c.CertPassword
	cfg.TwfID = c.SessionID
	cfg.SID = c.SID
	cfg.DeviceID = c.DeviceID
	cfg.SignKey = c.SignKey
	cfg.ClientDataFile = c.ClientDataFile
	cfg.ResourceFile = c.ResourceFile
	cfg.AuthType = c.AuthType
	cfg.Phone = c.Phone
	cfg.LoginDomain = c.LoginDomain
	cfg.CasTicket = c.CASTicket
	cfg.OAuth2Code = c.OAuth2Code
	cfg.GraphCodeFile = c.GraphCodeFile
	cfg.DisableServerConfig = c.DisableServerConfig
	cfg.SkipDomainResource = c.SkipDomainResource
	cfg.DisableMultiLine = c.DisableMultiLine
	cfg.DisableZJUConfig = !c.EnableZJUCompat
	cfg.CustomProxyDomain = append([]string(nil), c.CustomProxyDomain...)
	cfg.BindInterface = c.BindInterface
	cfg.AutoDetectInterface = c.autoDetectInterface()
	cfg.LocalDNSServer = c.LocalDNSServer
	cfg.DialDirectProxy = c.DialDirectProxy
	cfg.ProxyAll = c.ProxyAll
	cfg.DisableRemoteDNS = c.DisableRemoteDNS
	cfg.RemoteDNSServer = c.RemoteDNSServer
	cfg.SecondaryDNSServer = c.SecondaryDNSServer
	cfg.DNSTTL = c.DNSTTL
	cfg.SocksUser = c.SocksUser
	cfg.SocksPasswd = c.SocksPasswd
	cfg.DebugDump = c.DebugDump
	cfg.DebugPCAPFile = c.DebugPCAPFile
	cfg.DebugTLSLogFile = c.DebugTLSLogFile
	cfg.UpdateBestNodesInterval = c.UpdateBestNodesInterval
	cfg.SessionRefreshInterval = c.SessionRefreshInterval

	cfg.CustomDNSList = make([]configs.SingleCustomDNS, 0, len(c.CustomDNS))
	for _, item := range c.CustomDNS {
		cfg.CustomDNSList = append(cfg.CustomDNSList, configs.SingleCustomDNS{
			HostName: item.HostName,
			IP:       item.IP,
		})
	}
	return cfg
}

// bestNodesInterval returns the aTrust best-node refresh interval.
func (c Config) bestNodesInterval() time.Duration {
	return time.Duration(c.UpdateBestNodesInterval) * time.Second
}

// sessionRefreshInterval returns the aTrust session refresh interval.
func (c Config) sessionRefreshInterval() time.Duration {
	return time.Duration(c.SessionRefreshInterval) * time.Second
}

// loginMethodOptions builds the aTrust login method configuration.
func (c Config) loginMethodOptions() auth.LoginMethodOptions {
	return auth.LoginMethodOptions{
		AuthType:      c.AuthType,
		Username:      c.Username,
		Password:      c.Password,
		Phone:         c.Phone,
		Domain:        c.LoginDomain,
		GraphCodeFile: c.GraphCodeFile,
		CASTicket:     c.CASTicket,
		OAuth2Code:    c.OAuth2Code,
	}
}
