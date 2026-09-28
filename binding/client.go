package binding

import (
	"crypto"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/mythologyli/zju-connect/client"
	atrustclient "github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	easyconnectclient "github.com/mythologyli/zju-connect/client/easyconnect"
	"github.com/mythologyli/zju-connect/dial"
	"github.com/mythologyli/zju-connect/internal/hook_func"
	"github.com/mythologyli/zju-connect/internal/keylog"
	"github.com/mythologyli/zju-connect/internal/zcdns"
	"github.com/mythologyli/zju-connect/log"
	"github.com/mythologyli/zju-connect/resolve"
	"github.com/mythologyli/zju-connect/service"
	"github.com/mythologyli/zju-connect/stack"
	"github.com/mythologyli/zju-connect/stack/gvisor"
	"github.com/mythologyli/zju-connect/underlay"
	"golang.org/x/crypto/pkcs12"
	"inet.af/netaddr"
)

// EventType classifies an asynchronous event emitted to the host.
type EventType int

const (
	// EventLog carries a line of diagnostic output.
	EventLog EventType = iota
	// EventState announces a lifecycle transition. Message is one of the
	// StateConnected / StateDisconnected / StateError constants.
	EventState
	// EventError carries a terminal error message.
	EventError
)

// Lifecycle states reported through EventState.
const (
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateDisconnected = "disconnected"
	StateError        = "error"
)

// Event is delivered to the host's event callback.
type Event struct {
	Type    EventType
	Message string
}

// EventHandler receives asynchronous events. It may be called from any
// goroutine and must not block for long.
type EventHandler func(Event)

// Client owns one VPN session. Create it with NewClient, start it with Setup,
// then use either the packet or the proxy data plane. Close it when done.
//
// A Client is safe for concurrent use.
type Client struct {
	cfg Config

	eventHandler EventHandler
	challenges   *challengeHandler

	// Everything below is populated by Setup and guarded by mu.
	mu        sync.Mutex
	vpnClient client.Client
	underlay  *underlay.Dialer
	tlsKeyLog io.WriteCloser
	vpnStack  stack.Stack
	resolver  *resolve.Resolver
	dialer    *dial.Dialer

	// Packet data plane.
	l3Conn  *packetStream
	writeMu sync.Mutex

	// Proxy data plane.
	proxy *proxyServers

	// localResolver serves DNS for the proxy data plane's hijack path.
	localResolver zcdns.LocalServer

	// aTrust client-data blob captured during setup.
	clientDataMu sync.Mutex
	clientData   string

	state     atomic.Value // string
	closed    chan struct{}
	closeOnce sync.Once
}

// NewClient creates a client from the given configuration. Setup performs the
// actual login.
//
// The challenge handler is invoked from a background goroutine whenever the VPN
// server requires interactive input (SMS code, captcha, OAuth2 login, ...).
// A nil handler makes such challenges fail with an "unsupported" error.
//
// The event handler receives log lines and state transitions. A nil handler
// discards them.
func NewClient(cfg Config, eventHandler EventHandler, challengeHandler ChallengeResponder) (*Client, error) {
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	if challengeHandler == nil {
		challengeHandler = func(ch Challenge) {}
	}

	c := &Client{
		cfg:          cfg,
		eventHandler: eventHandler,
		challenges:   newChallengeHandler(challengeHandler),
		closed:       make(chan struct{}),
	}
	c.setState(StateDisconnected)
	return c, nil
}

func (c *Client) emit(eventType EventType, message string) {
	if c.eventHandler == nil {
		return
	}
	c.eventHandler(Event{Type: eventType, Message: message})
}

func (c *Client) logf(format string, args ...any) {
	c.emit(EventLog, fmt.Sprintf(format, args...))
}

func (c *Client) setState(state string) {
	c.state.Store(state)
	c.emit(EventState, state)
}

// State reports the current lifecycle state.
func (c *Client) State() string {
	if state, ok := c.state.Load().(string); ok {
		return state
	}
	return StateDisconnected
}

// logWriter forwards log-package output to the event callback.
type logWriter struct {
	client *Client
}

func (w logWriter) Write(p []byte) (int, error) {
	line := string(p)
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	w.client.emit(EventLog, line)
	return len(p), nil
}

// Setup logs in and starts the underlying stack. On success the client is ready
// for either ReadPacket/WritePacket or StartProxy.
func (c *Client) Setup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setState(StateConnecting)

	// Route all log output (and fatal errors) to the host.
	log.SetOutput(logWriter{client: c})
	log.SetFatalHandler(func(err error) {
		c.emit(EventError, err.Error())
		c.setState(StateError)
	})
	// The hook registry is process-global; reset it so a second session in the
	// same process starts from a clean slate.
	hook_func.Reset()
	hook_func.ResetInitial()

	if c.cfg.DebugDump {
		log.EnableDebug()
	}

	newUnderlay, err := underlay.New(underlay.Options{
		InterfaceName:  c.cfg.BindInterface,
		AutoDetect:     c.cfg.autoDetectInterface(),
		DebugPCAPFile:  c.cfg.DebugPCAPFile,
		LocalDNSServer: c.cfg.LocalDNSServer,
	})
	if err != nil {
		return c.fail(fmt.Errorf("create underlay dialer: %w", err))
	}

	tlsKeyLog, err := keylog.Open(c.cfg.DebugTLSLogFile)
	if err != nil {
		_ = newUnderlay.Close()
		return c.fail(fmt.Errorf("create TLS key log: %w", err))
	}

	vpnClient, err := c.createVPNClient(newUnderlay, tlsKeyLog)
	if err != nil {
		_ = newUnderlay.Close()
		if tlsKeyLog != nil {
			_ = tlsKeyLog.Close()
		}
		return c.fail(err)
	}

	ipResources, ipSet, domainResources, dnsResource, err := c.collectResources(vpnClient)
	if err != nil {
		closeVPNClient(vpnClient)
		_ = newUnderlay.Close()
		if tlsKeyLog != nil {
			_ = tlsKeyLog.Close()
		}
		return c.fail(err)
	}
	// The gVisor stack programs its own routes; the IP set is only needed by
	// the (unused here) TUN stack, so discard it explicitly.
	_ = ipSet

	vpnStack, err := gvisor.NewStack(vpnClient)
	if err != nil {
		closeVPNClient(vpnClient)
		_ = newUnderlay.Close()
		if tlsKeyLog != nil {
			_ = tlsKeyLog.Close()
		}
		return c.fail(fmt.Errorf("gVisor stack setup error: %w", err))
	}

	useRemoteDNS := !c.cfg.DisableRemoteDNS
	remoteDNSServer := c.cfg.RemoteDNSServer
	policyDNSServers, _ := vpnClient.DNSServers()
	if useRemoteDNS && remoteDNSServer == "auto" {
		remoteDNSServer, err = vpnClient.DNSServer()
		if err != nil {
			useRemoteDNS = false
			remoteDNSServer = "10.10.0.21"
			c.logf("No DNS server provided by server, disabling remote DNS")
		} else {
			c.logf("Using DNS server %s provided by server", remoteDNSServer)
		}
	}
	secondaryDNSServer := c.cfg.SecondaryDNSServer
	if secondaryDNSServer == "auto" {
		secondaryDNSServer = "114.114.114.114"
		if len(policyDNSServers) > 1 {
			secondaryDNSServer = policyDNSServers[1]
			c.logf("Using secondary DNS server %s provided by server", secondaryDNSServer)
		}
	}

	vpnResolver, err := resolve.NewResolver(
		vpnStack,
		remoteDNSServer,
		secondaryDNSServer,
		c.cfg.DNSTTL,
		domainResources,
		dnsResource,
		useRemoteDNS,
	)
	if err != nil {
		closeVPNClient(vpnClient)
		_ = newUnderlay.Close()
		if tlsKeyLog != nil {
			_ = tlsKeyLog.Close()
		}
		return c.fail(fmt.Errorf("create resolver: %w", err))
	}

	for _, custom := range c.cfg.CustomDNS {
		ipAddr := net.ParseIP(custom.IP)
		if ipAddr == nil {
			c.logf("Custom DNS for host %s is invalid, skipping", custom.HostName)
			continue
		}
		vpnResolver.SetPermanentDNS(custom.HostName, ipAddr)
	}

	localResolver := service.NewDnsServer(vpnResolver, []string{remoteDNSServer, c.cfg.SecondaryDNSServer})
	vpnStack.SetupResolve(localResolver)
	vpnStack.SetupIPPool(vpnResolver.IPPool)

	go vpnStack.Run()

	c.vpnClient = vpnClient
	c.underlay = newUnderlay
	c.tlsKeyLog = tlsKeyLog
	c.vpnStack = vpnStack
	c.resolver = vpnResolver
	c.dialer = dial.NewDialer(vpnStack, vpnResolver, ipResources, c.cfg.ProxyAll, c.cfg.DialDirectProxy)
	c.localResolver = localResolver

	c.setState(StateConnected)
	return nil
}

// fail records a terminal error and returns it for the caller to propagate.
func (c *Client) fail(err error) error {
	c.emit(EventError, err.Error())
	c.setState(StateError)
	return err
}

func (c *Client) createVPNClient(underlayDialer client.UnderlayDialer, tlsKeyLog io.WriteCloser) (client.Client, error) {
	switch c.cfg.Protocol {
	case "easyconnect":
		tlsCert, err := loadClientCertificate(c.cfg.CertFile, c.cfg.CertPassword)
		if err != nil {
			return nil, err
		}

		vpnClient := easyconnectclient.NewClient(easyconnectclient.Options{
			Server: net.JoinHostPort(c.cfg.ServerAddress, strconv.Itoa(c.cfg.ServerPort)),
			Auth: easyconnectclient.AuthOptions{
				Username:      c.cfg.Username,
				Password:      c.cfg.Password,
				TOTPSecret:    c.cfg.TOTPSecret,
				Certificate:   tlsCert,
				GraphCodeFile: c.cfg.GraphCodeFile,
			},
			SessionID:     c.cfg.SessionID,
			TestMultiLine: !c.cfg.DisableMultiLine,
			Resources: easyconnectclient.ResourceOptions{
				Fetch:          !c.cfg.DisableServerConfig,
				IncludeDomains: !c.cfg.SkipDomainResource,
			},
			UnderlayDialer:   underlayDialer,
			TLSKeyLogWriter:  tlsKeyLog,
			ChallengeHandler: c.challenges,
		})

		c.logf("VPN protocol: easyconnect")
		if err := vpnClient.Setup(); err != nil {
			vpnClient.Close()
			return nil, fmt.Errorf("VPN client setup error: %w", err)
		}
		return vpnClient, nil

	case "atrust":
		var resourceData []byte
		var err error
		if c.cfg.ResourceFile != "" {
			resourceData, err = os.ReadFile(c.cfg.ResourceFile)
			if err != nil {
				return nil, fmt.Errorf("read resource file: %w", err)
			}
		} else if c.cfg.ResourceData != "" {
			resourceData = []byte(c.cfg.ResourceData)
		}

		var clientData []byte
		if c.cfg.ClientDataFile != "" {
			clientData, err = os.ReadFile(c.cfg.ClientDataFile)
			if err != nil {
				c.logf("Read client data file error: %s", err)
			}
		} else if c.cfg.ClientData != "" {
			clientData = []byte(c.cfg.ClientData)
		}

		var loginMethod auth.LoginMethod
		if c.cfg.SID == "" || c.cfg.DeviceID == "" || resourceData == nil {
			loginMethod, err = auth.NewLoginMethod(c.cfg.loginMethodOptions())
			if err != nil {
				return nil, fmt.Errorf("configure aTrust login: %w", err)
			}
		}

		vpnClient := atrustclient.NewClient(atrustclient.ClientOptions{
			Session: atrustclient.SessionOptions{
				Username: c.cfg.Username,
				SID:      c.cfg.SID,
				DeviceID: c.cfg.DeviceID,
				SignKey:  c.cfg.SignKey,
			},
			UnderlayDialer:  underlayDialer,
			TLSKeyLogWriter: tlsKeyLog,
		})

		var saveClientData func([]byte) error
		if c.cfg.ClientDataFile != "" {
			path := c.cfg.ClientDataFile
			saveClientData = func(data []byte) error {
				return os.WriteFile(path, data, 0o600)
			}
		} else {
			saveClientData = func(data []byte) error {
				c.clientDataMu.Lock()
				c.clientData = string(data)
				c.clientDataMu.Unlock()
				return nil
			}
		}

		c.logf("VPN protocol: atrust")
		if _, err := vpnClient.Setup(atrustclient.SetupOptions{
			ServerAddress:            c.cfg.ServerAddress,
			ServerPort:               c.cfg.ServerPort,
			LoginMethod:              loginMethod,
			TOTPSecret:               c.cfg.TOTPSecret,
			ClientData:               clientData,
			ResourceData:             resourceData,
			BestNodesRefreshInterval: c.cfg.bestNodesInterval(),
			SessionRefreshInterval:   c.cfg.sessionRefreshInterval(),
			ChallengeHandler:         c.challenges,
			SaveClientData:           saveClientData,
		}); err != nil {
			vpnClient.Close()
			return nil, fmt.Errorf("VPN client setup error: %w", err)
		}
		return vpnClient, nil
	}

	return nil, fmt.Errorf("unsupported protocol %q", c.cfg.Protocol)
}

// collectResources gathers the server-issued resources, applying the optional
// ZJU compatibility shim.
func (c *Client) collectResources(vpnClient client.Client) ([]client.IPResource, *netaddr.IPSet, client.DomainResources, map[string][]net.IP, error) {
	ipResources, err := vpnClient.IPResources()
	if err != nil && !c.cfg.DisableServerConfig {
		c.logf("No IP resources")
	}
	ipSet, err := vpnClient.IPSet()
	if err != nil && !c.cfg.DisableServerConfig {
		c.logf("No IP set")
	}
	domainResources, err := vpnClient.DomainResources()
	if err != nil && !c.cfg.DisableServerConfig {
		c.logf("No domain resources")
	}
	dnsResource, err := vpnClient.DNSResource()
	if err != nil && !c.cfg.DisableServerConfig {
		c.logf("No DNS resource")
	}

	if c.cfg.Protocol == "easyconnect" && c.cfg.EnableZJUCompat {
		if domainResources == nil {
			domainResources = make(client.DomainResources)
		}
		domainResources["zju.edu.cn"] = []client.DomainResource{{
			PortMin:  1,
			PortMax:  65535,
			Protocol: "all",
		}}

		if ipResources == nil {
			ipResources = []client.IPResource{}
		}
		ipResources = append([]client.IPResource{{
			IPMin:    net.ParseIP("10.0.0.0"),
			IPMax:    net.ParseIP("10.255.255.255"),
			PortMin:  1,
			PortMax:  65535,
			Protocol: "all",
		}}, ipResources...)

		ipSetBuilder := netaddr.IPSetBuilder{}
		if ipSet != nil {
			ipSetBuilder.AddSet(ipSet)
		}
		ipSetBuilder.AddPrefix(netaddr.MustParseIPPrefix("10.0.0.0/8"))
		ipSet, _ = ipSetBuilder.IPSet()
	}

	for _, customProxyDomain := range c.cfg.CustomProxyDomain {
		if domainResources == nil {
			domainResources = make(client.DomainResources)
		}
		domainResources[customProxyDomain] = append(domainResources[customProxyDomain], client.DomainResource{
			PortMin:  1,
			PortMax:  65535,
			Protocol: "all",
		})
	}

	return ipResources, ipSet, domainResources, dnsResource, nil
}

// loadClientCertificate decodes an optional PKCS#12 client certificate.
func loadClientCertificate(path, password string) (tls.Certificate, error) {
	if path == "" {
		return tls.Certificate{}, nil
	}
	p12Data, err := os.ReadFile(path)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read certificate file: %w", err)
	}
	key, cert, err := pkcs12.Decode(p12Data, password)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("decode certificate file: %w", err)
	}
	privateKey, ok := key.(crypto.PrivateKey)
	if !ok {
		return tls.Certificate{}, fmt.Errorf("certificate key is not a crypto.PrivateKey")
	}
	return tls.Certificate{
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  privateKey,
		Leaf:        cert,
	}, nil
}

// SessionID returns the easyconnect TwfID captured during login, so the host
// can resume the session later through Config.SessionID. Empty for aTrust.
func (c *Client) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ec, ok := c.vpnClient.(*easyconnectclient.Client); ok {
		return ec.TwfID()
	}
	return ""
}

// ClientData returns the aTrust client-data blob produced by the last login.
// Persist it and feed it back through Config.ClientData to resume the session.
func (c *Client) ClientData() string {
	c.clientDataMu.Lock()
	defer c.clientDataMu.Unlock()
	return c.clientData
}

// VirtualIP returns the IPv4 address assigned by the VPN server.
func (c *Client) VirtualIP() string {
	c.mu.Lock()
	vpnClient := c.vpnClient
	c.mu.Unlock()
	if vpnClient == nil {
		return ""
	}
	ip, err := vpnClient.IP()
	if err != nil {
		return ""
	}
	return ip.String()
}

// RespondChallenge answers a challenge previously delivered to the
// ChallengeResponder. It returns false if the id is unknown or already
// answered.
//
// responseJSON is a JSON object whose schema depends on the challenge kind:
//
//	code:           {"code":"123456","skip_secondary_auth":false}
//	text_captcha:   {"code":"AB12"}
//	click_captcha:  {"points":[{"x":1,"y":2}],"width":100,"height":40}
//	external_login: {"callback_url":"..."}
//
// Every payload may instead carry {"error":"..."} to abort the login.
func (c *Client) RespondChallenge(id int64, responseJSON string) bool {
	return c.challenges.Respond(id, responseJSON)
}

// Close tears the session down. It is safe to call multiple times.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.challenges.failAll()

		c.mu.Lock()
		if c.proxy != nil {
			c.proxy.Close()
			c.proxy = nil
		}
		if c.l3Conn != nil {
			c.l3Conn.Close()
			c.l3Conn = nil
		}
		if c.resolver != nil {
			c.resolver.Close()
			c.resolver = nil
		}
		if c.vpnClient != nil {
			closeVPNClient(c.vpnClient)
			c.vpnClient = nil
		}
		if c.underlay != nil {
			_ = c.underlay.Close()
			c.underlay = nil
		}
		if c.tlsKeyLog != nil {
			_ = c.tlsKeyLog.Close()
			c.tlsKeyLog = nil
		}
		c.mu.Unlock()

		// Restore the global log state so a subsequent session in the same
		// process starts clean.
		log.SetFatalHandler(nil)
		log.SetOutput(nil)
		log.DisableDebug()

		c.setState(StateDisconnected)
	})
}

// closeVPNClient closes a client if it exposes Close.
func closeVPNClient(vpnClient client.Client) {
	if closer, ok := vpnClient.(interface{ Close() }); ok {
		closer.Close()
	}
}
