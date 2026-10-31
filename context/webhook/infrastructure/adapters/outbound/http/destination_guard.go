package http

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// DestinationGuard impide que un webhook salga hacia la red interna (SSRF).
//
// El worker hace el POST desde dentro de la plataforma y el tenant ve los
// primeros 500 bytes de la respuesta: sin este filtro, un endpoint apuntado a
// localhost, a la red de docker (postgres, nats) o a la metadata del cloud
// (169.254.169.254) leía servicios internos.
//
// Hay dos controles:
//   - Check, al registrar: rechaza localhost, IPs internas literales y hosts
//     que hoy resuelven a una IP interna. Es un aviso temprano (400).
//   - control, al despachar: el net.Dialer valida la IP ya resuelta de cada
//     conexión. Es el control efectivo: cubre DNS rebinding y hosts que
//     cambian de IP después del alta.
//
// allowPrivate desactiva ambos (WEBHOOK_ALLOW_PRIVATE_TARGETS, solo desarrollo).
type DestinationGuard struct {
	allowPrivate bool
	lookup       func(ctx context.Context, host string) ([]netip.Addr, error)
}

func NewDestinationGuard(allowPrivate bool) *DestinationGuard {
	return &DestinationGuard{
		allowPrivate: allowPrivate,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
	}
}

// Check implementa application.DestinationGuard.
func (g *DestinationGuard) Check(ctx context.Context, rawURL string) error {
	if g.allowPrivate {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil // el formato lo valida el dominio
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: %s", domain.ErrDestinationNotAllowed, host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if isInternal(ip) {
			return fmt.Errorf("%w: %s", domain.ErrDestinationNotAllowed, ip)
		}
		return nil
	}
	addrs, err := g.lookup(ctx, host)
	if err != nil {
		// Un host que hoy no resuelve no es interno; si después resuelve a
		// una IP interna, el control del dialer lo frena.
		return nil
	}
	for _, ip := range addrs {
		if isInternal(ip) {
			return fmt.Errorf("%w: %s resolves to %s", domain.ErrDestinationNotAllowed, host, ip)
		}
	}
	return nil
}

// control es el net.Dialer.Control del dispatcher: address es la IP ya
// resuelta de la conexión que se está por abrir.
func (g *DestinationGuard) control(_, address string, _ syscall.RawConn) error {
	if g.allowPrivate {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparseable address %q", domain.ErrDestinationNotAllowed, address)
	}
	if isInternal(ap.Addr()) {
		return fmt.Errorf("%w: %s", domain.ErrDestinationNotAllowed, ap.Addr())
	}
	return nil
}

var (
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	sharedCGNAT = netip.MustParsePrefix("100.64.0.0/10")
)

// isInternal indica si la IP no es una dirección pública de Internet:
// loopback, privadas (RFC 1918 / ULA), link-local (incluye la metadata del
// cloud), CGNAT, multicast y no especificadas.
func isInternal(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		thisNetwork.Contains(ip) || sharedCGNAT.Contains(ip)
}
