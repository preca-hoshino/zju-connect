package binding

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/log"
	"github.com/things-go/go-socks5"
)

const (
	httpProxyMaxIdleConns        = 100
	httpProxyMaxIdleConnsPerHost = 10
	httpProxyMaxConnsPerHost     = 50
	httpProxyIdleConnTimeout     = 90 * time.Second
	httpProxyResponseTimeout     = 30 * time.Second
	httpProxyReadHeaderTimeout   = 10 * time.Second
	httpProxyServerIdleTimeout   = 90 * time.Second
)

// ProxyAddrs reports the local endpoints the proxy data plane is listening on.
type ProxyAddrs struct {
	Socks string `json:"socks"`
	HTTP  string `json:"http"`
}

// ProxyConfig configures the local proxies. Bind addresses of the form
// "host:port" are accepted; a port of 0 asks the OS for a free port and a
// negative port disables the listener. An empty string uses the defaults
// "127.0.0.1:0" for SOCKS5 and "" (disabled) for HTTP.
type ProxyConfig struct {
	SocksBind   string `json:"socks_bind"`
	HTTPBind    string `json:"http_bind"`
	SocksUser   string `json:"socks_user"`
	SocksPasswd string `json:"socks_passwd"`
}

// proxyServers owns the proxy data plane for one session.
type proxyServers struct {
	client *Client

	socksListener net.Listener
	socksServer   *socks5.Server
	httpListener  net.Listener
	httpServer    *http.Server
	httpProxy     *httpProxy

	mu   sync.Mutex
	done bool
}

// StartProxy launches the local SOCKS5 and/or HTTP proxies backed by this
// session, and returns the addresses they listen on.
//
// The proxies run until Close is called on the client.
func (c *Client) StartProxy(cfg ProxyConfig) (ProxyAddrs, error) {
	c.mu.Lock()
	if c.vpnClient == nil || c.dialer == nil {
		c.mu.Unlock()
		return ProxyAddrs{}, ErrNotConnected
	}
	if c.proxy != nil {
		c.mu.Unlock()
		return ProxyAddrs{}, fmt.Errorf("proxy already started")
	}
	proxy := &proxyServers{client: c}
	c.proxy = proxy
	dialer := c.dialer
	resolver := c.resolver
	c.mu.Unlock()

	socksBind := cfg.SocksBind
	if socksBind == "" {
		socksBind = "127.0.0.1:0"
	}
	httpBind := cfg.HTTPBind

	if strings.HasPrefix(socksBind, "-") {
		socksBind = ""
	}
	if strings.HasPrefix(httpBind, "-") {
		httpBind = ""
	}

	var addrs ProxyAddrs

	if socksBind != "" {
		listener, err := net.Listen("tcp", socksBind)
		if err != nil {
			proxy.Close()
			return ProxyAddrs{}, fmt.Errorf("SOCKS5 listen on %s: %w", socksBind, err)
		}
		authMethods := []socks5.Authenticator{socks5.NoAuthAuthenticator{}}
		if cfg.SocksUser != "" && cfg.SocksPasswd != "" {
			authMethods = []socks5.Authenticator{socks5.UserPassAuthenticator{
				Credentials: socks5.StaticCredentials{cfg.SocksUser: cfg.SocksPasswd},
			}}
		}
		proxy.socksListener = listener
		proxy.socksServer = socks5.NewServer(
			socks5.WithAuthMethods(authMethods),
			socks5.WithResolver(resolver),
			socks5.WithDial(dialer.DialIPPort),
			socks5.WithLogger(socks5.NewLogger(log.NewLogger("[SOCKS5] "))),
		)
		addrs.Socks = listener.Addr().String()
		go func() {
			if err := proxy.socksServer.Serve(listener); err != nil && !proxy.isDone() {
				c.logf("SOCKS5 server stopped: %v", err)
			}
		}()
	}

	if httpBind != "" {
		listener, err := net.Listen("tcp", httpBind)
		if err != nil {
			proxy.Close()
			return ProxyAddrs{}, fmt.Errorf("HTTP listen on %s: %w", httpBind, err)
		}
		proxy.httpProxy = newHTTPProxy(dialer.Dial)
		proxy.httpListener = listener
		proxy.httpServer = &http.Server{
			Handler:           proxy.httpProxy,
			ReadHeaderTimeout: httpProxyReadHeaderTimeout,
			IdleTimeout:       httpProxyServerIdleTimeout,
		}
		addrs.HTTP = listener.Addr().String()
		go func() {
			if err := proxy.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed && !proxy.isDone() {
				c.logf("HTTP proxy server stopped: %v", err)
			}
		}()
	}

	if addrs.Socks == "" && addrs.HTTP == "" {
		return ProxyAddrs{}, fmt.Errorf("no proxy listener enabled")
	}
	return addrs, nil
}

func (p *proxyServers) isDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// Close stops every listener and tears down in-flight tunnels.
func (p *proxyServers) Close() {
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		return
	}
	p.done = true
	socksListener := p.socksListener
	httpListener := p.httpListener
	httpServer := p.httpServer
	httpProxy := p.httpProxy
	p.mu.Unlock()

	if socksListener != nil {
		_ = socksListener.Close()
	}
	if httpProxy != nil {
		httpProxy.close()
	}
	if httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}
	if httpListener != nil {
		_ = httpListener.Close()
	}
}

// The HTTP forward proxy below is derived from zju-connect's service/http.go,
// which in turn carries code from Ian Denhardt's "go-http-proxy" (MIT).

type httpTunnel struct {
	client net.Conn
	target net.Conn
}

type httpProxy struct {
	dialContext func(context.Context, string, string) (net.Conn, error)
	client      *http.Client
	tunnelsMu   sync.Mutex
	tunnels     map[*httpTunnel]struct{}
	closeOnce   sync.Once
}

func newHTTPProxy(dialContext func(context.Context, string, string) (net.Conn, error)) *httpProxy {
	proxy := &httpProxy{
		dialContext: dialContext,
		tunnels:     make(map[*httpTunnel]struct{}),
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialContext
	transport.MaxIdleConns = httpProxyMaxIdleConns
	transport.MaxIdleConnsPerHost = httpProxyMaxIdleConnsPerHost
	transport.MaxConnsPerHost = httpProxyMaxConnsPerHost
	transport.IdleConnTimeout = httpProxyIdleConnTimeout
	transport.ResponseHeaderTimeout = httpProxyResponseTimeout
	proxy.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return proxy
}

func (p *httpProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		p.handleConnect(w, req)
		return
	}

	req.RequestURI = ""
	resp, err := p.client.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(err.Error() + "\n"))
		return
	}
	defer resp.Body.Close()

	hdr := w.Header()
	for k, v := range resp.Header {
		hdr[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *httpProxy) handleConnect(w http.ResponseWriter, req *http.Request) {
	targetConn, err := p.dialContext(req.Context(), "tcp", req.Host)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(err.Error() + "\n"))
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = targetConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("failed to hijack connection\n"))
		return
	}

	clientConn, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = targetConn.Close()
		return
	}

	tunnel := &httpTunnel{client: clientConn, target: targetConn}
	p.registerTunnel(tunnel)
	defer p.unregisterTunnel(tunnel)
	defer clientConn.Close()
	defer targetConn.Close()

	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}

	relayDone := make(chan struct{}, 2)
	go relayHTTPConnect(targetConn, buffered, relayDone)
	go relayHTTPConnect(clientConn, targetConn, relayDone)
	<-relayDone
	_ = clientConn.Close()
	_ = targetConn.Close()
	<-relayDone
}

func relayHTTPConnect(dst net.Conn, src io.Reader, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	if conn, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = conn.CloseWrite()
	}
	if conn, ok := src.(interface{ CloseRead() error }); ok {
		_ = conn.CloseRead()
	}
	done <- struct{}{}
}

func (p *httpProxy) registerTunnel(tunnel *httpTunnel) {
	p.tunnelsMu.Lock()
	p.tunnels[tunnel] = struct{}{}
	p.tunnelsMu.Unlock()
}

func (p *httpProxy) unregisterTunnel(tunnel *httpTunnel) {
	p.tunnelsMu.Lock()
	delete(p.tunnels, tunnel)
	p.tunnelsMu.Unlock()
}

func (p *httpProxy) close() {
	p.closeOnce.Do(func() {
		p.tunnelsMu.Lock()
		tunnels := make([]*httpTunnel, 0, len(p.tunnels))
		for tunnel := range p.tunnels {
			tunnels = append(tunnels, tunnel)
		}
		p.tunnelsMu.Unlock()

		for _, tunnel := range tunnels {
			_ = tunnel.client.Close()
			_ = tunnel.target.Close()
		}
		if p.client != nil {
			p.client.CloseIdleConnections()
		}
	})
}
