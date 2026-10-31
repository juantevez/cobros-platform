package http

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

func TestIsInternal(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":            true,
		"10.1.2.3":             true,
		"172.18.0.5":           true, // red de docker
		"192.168.1.10":         true,
		"169.254.169.254":      true, // metadata del cloud
		"100.64.0.1":           true, // CGNAT
		"0.0.0.0":              true,
		"0.1.2.3":              true,
		"224.0.0.1":            true,
		"::1":                  true,
		"::":                   true,
		"fd00::1":              true,
		"fe80::1":              true,
		"::ffff:127.0.0.1":     true, // IPv4 mapeada
		"::ffff:10.0.0.1":      true,
		"93.184.216.34":        false,
		"8.8.8.8":              false,
		"2606:4700::1111":      false,
		"::ffff:93.184.216.34": false,
	}
	for s, want := range cases {
		if got := isInternal(netip.MustParseAddr(s)); got != want {
			t.Errorf("isInternal(%s) = %v, want %v", s, got, want)
		}
	}
}

func guardResolving(addrs map[string][]string) *DestinationGuard {
	g := NewDestinationGuard(false)
	g.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		ips, ok := addrs[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		out := make([]netip.Addr, len(ips))
		for i, ip := range ips {
			out[i] = netip.MustParseAddr(ip)
		}
		return out, nil
	}
	return g
}

func TestDestinationGuard_Check(t *testing.T) {
	g := guardResolving(map[string][]string{
		"merchant.example.com": {"93.184.216.34"},
		"rebind.example.com":   {"93.184.216.34", "10.0.0.7"},
		"internal.example.com": {"127.0.0.1"},
	})

	cases := []struct {
		url     string
		blocked bool
	}{
		{"https://merchant.example.com/hooks", false},
		{"https://93.184.216.34/hooks", false},
		{"https://unresolvable.example.com/hooks", false}, // lo frena el dialer si después resuelve interno
		{"http://localhost:8080/hooks", true},
		{"http://LOCALHOST./hooks", true},
		{"http://api.localhost/hooks", true},
		{"http://127.0.0.1/hooks", true},
		{"http://[::1]:8080/hooks", true},
		{"http://169.254.169.254/latest/meta-data", true},
		{"http://10.0.0.1/hooks", true},
		{"http://internal.example.com/hooks", true},
		{"http://rebind.example.com/hooks", true}, // alcanza con una IP interna
	}
	for _, tc := range cases {
		err := g.Check(context.Background(), tc.url)
		if tc.blocked != errors.Is(err, domain.ErrDestinationNotAllowed) {
			t.Errorf("Check(%s) = %v, want blocked=%v", tc.url, err, tc.blocked)
		}
		if !tc.blocked && err != nil {
			t.Errorf("Check(%s) unexpected error: %v", tc.url, err)
		}
	}
}

func TestDestinationGuard_AllowPrivate(t *testing.T) {
	g := NewDestinationGuard(true)
	for _, u := range []string{"http://localhost:8080/hooks", "http://10.0.0.1/hooks", "http://169.254.169.254/"} {
		if err := g.Check(context.Background(), u); err != nil {
			t.Errorf("Check(%s) = %v, want allowed", u, err)
		}
	}
	if err := g.control("tcp4", "127.0.0.1:80", nil); err != nil {
		t.Errorf("control = %v, want allowed", err)
	}
}

func TestDestinationGuard_Control(t *testing.T) {
	g := NewDestinationGuard(false)
	for addr, blocked := range map[string]bool{
		"127.0.0.1:8080":        true,
		"[::1]:443":             true,
		"169.254.169.254:80":    true,
		"172.18.0.3:5432":       true,
		"93.184.216.34:443":     false,
		"[2606:4700::1111]:443": false,
		"garbage":               true,
	} {
		err := g.control("tcp", addr, nil)
		if blocked != errors.Is(err, domain.ErrDestinationNotAllowed) {
			t.Errorf("control(%s) = %v, want blocked=%v", addr, err, blocked)
		}
	}
}
