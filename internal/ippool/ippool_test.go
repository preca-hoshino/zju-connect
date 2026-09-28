package ippool

import (
	"net"
	"testing"
)

func newTestPool(t *testing.T) *IPPool[string] {
	t.Helper()
	pool, err := NewIPPool[string]("198.18.0.0/30")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	return pool
}

func TestGenerateIPAllocatesAndReuses(t *testing.T) {
	pool := newTestPool(t)

	first, err := pool.GenerateIP("a.example", "res-a")
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	if first == nil {
		t.Fatal("first allocation returned a nil IP")
	}

	// A repeated lookup must return the same address rather than consuming the
	// range again.
	again, err := pool.GenerateIP("a.example", "res-a")
	if err != nil {
		t.Fatalf("repeat allocation: %v", err)
	}
	if !again.Equal(first) {
		t.Fatalf("repeat allocation = %s, want %s", again, first)
	}

	if domain, res, ok := pool.GetDomain(first); !ok || domain != "a.example" || res != "res-a" {
		t.Fatalf("GetDomain = %q, %q, %v", domain, res, ok)
	}
}

// The pool used to panic on exhaustion, which would abort an embedding host.
// It now reports an error so callers can fall back to a real DNS lookup.
func TestGenerateIPReportsExhaustionInsteadOfPanicking(t *testing.T) {
	pool := newTestPool(t)

	// 198.18.0.0/30 yields four addresses and allocation starts at .2, so two
	// distinct domains consume the range.
	if _, err := pool.GenerateIP("a.example", "res"); err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	if _, err := pool.GenerateIP("b.example", "res"); err != nil {
		t.Fatalf("second allocation: %v", err)
	}

	_, err := pool.GenerateIP("c.example", "res")
	if err == nil {
		t.Fatal("exhausted pool did not report an error")
	}
	if err.Error() != "fake IP range exhausted" {
		t.Fatalf("error = %q, want the exhaustion diagnostic", err.Error())
	}
}

func TestGenerateIPRejectsInvalidCIDR(t *testing.T) {
	if _, err := NewIPPool[string]("not-a-cidr"); err == nil {
		t.Fatal("NewIPPool accepted an invalid CIDR")
	}
}

func TestSetIPDomainRejectsNonIPv4(t *testing.T) {
	pool := newTestPool(t)
	if err := pool.SetIPDomain(net.ParseIP("2001:db8::1"), "v6.example", "res"); err == nil {
		t.Fatal("SetIPDomain accepted an IPv6 address")
	}
}
