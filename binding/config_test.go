package binding

import (
	"strings"
	"testing"
)

func TestParseConfigAppliesDefaults(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"server_address":"vpn.example.com","username":"u","password":"p"}`))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if cfg.Protocol != "easyconnect" {
		t.Errorf("Protocol = %q, want easyconnect", cfg.Protocol)
	}
	if cfg.ServerPort != 443 {
		t.Errorf("ServerPort = %d, want 443", cfg.ServerPort)
	}
	if cfg.DNSTTL != 3600 {
		t.Errorf("DNSTTL = %d, want 3600", cfg.DNSTTL)
	}
	if cfg.RemoteDNSServer != "auto" {
		t.Errorf("RemoteDNSServer = %q, want auto", cfg.RemoteDNSServer)
	}
	if !cfg.autoDetectInterface() {
		t.Error("autoDetectInterface() = false, want true by default")
	}
}

func TestParseConfigRequiresServerAddress(t *testing.T) {
	_, err := ParseConfig([]byte(`{"username":"u"}`))
	if err == nil || !strings.Contains(err.Error(), "server_address") {
		t.Fatalf("ParseConfig() error = %v, want server_address requirement", err)
	}
}

func TestParseConfigRejectsUnknownProtocol(t *testing.T) {
	_, err := ParseConfig([]byte(`{"protocol":"wireguard","server_address":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("ParseConfig() error = %v, want unsupported protocol", err)
	}
}

func TestParseConfigRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseConfig([]byte(`{"server_address":`)); err == nil {
		t.Fatal("ParseConfig() error = nil, want JSON decode error")
	}
}

func TestParseConfigEmptyUsesDefaultsThenFailsOnServer(t *testing.T) {
	_, err := ParseConfig(nil)
	if err == nil || !strings.Contains(err.Error(), "server_address") {
		t.Fatalf("ParseConfig(nil) error = %v, want server_address requirement", err)
	}
}

func TestConfigToConfigsMapsZJUCompat(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"server_address":"vpn","username":"u"}`))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	// ZJU compatibility is opt-in, so the internal "disable" flag must be set.
	if !cfg.toConfigs().DisableZJUConfig {
		t.Error("toConfigs().DisableZJUConfig = false, want true without EnableZJUCompat")
	}

	cfg.EnableZJUCompat = true
	if cfg.toConfigs().DisableZJUConfig {
		t.Error("toConfigs().DisableZJUConfig = true, want false with EnableZJUCompat")
	}
}

func TestConfigCustomDNSIsMapped(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"server_address":"vpn","custom_dns":[{"host_name":"a.example","ip":"10.0.0.1"}]}`))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	list := cfg.toConfigs().CustomDNSList
	if len(list) != 1 || list[0].HostName != "a.example" || list[0].IP != "10.0.0.1" {
		t.Fatalf("CustomDNSList = %+v, want one a.example entry", list)
	}
}
