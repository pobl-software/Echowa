package services

import (
	"net"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
)

// proxyConfig says which peers are proxies in front of Echowa (ADR 0022):
// loopback (cloudflared, an SSH tunnel) always, plus the networks in
// ECHOWA_TRUSTED_PROXIES (Traefik, with HTTPS access). Only they may name
// the client in the header ECHOWA_CLIENT_IP_HEADER.
type proxyConfig struct {
	header string
	nets   []*net.IPNet
	// own reports whether ip is one of this machine's addresses. Behind
	// Traefik, Echowa listens on the Docker bridge, and Docker masquerades
	// the host's own connections to it: they arrive from the host's main
	// address, not loopback.
	own func(ip net.IP) bool
}

func proxies() proxyConfig {
	cfg := proxyConfig{header: facades.Config().GetString("echowa.client_ip_header"), own: ownAddress}
	for _, cidr := range strings.Split(facades.Config().GetString("echowa.trusted_proxies"), ",") {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(cidr)); err == nil {
			cfg.nets = append(cfg.nets, n)
		}
	}

	return cfg
}

func ownAddress(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if n, ok := addr.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}

	return false
}

func (c proxyConfig) trusts(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	for _, n := range c.nets {
		if n.Contains(ip) {
			return true
		}
	}

	return false
}

func peerIP(remoteAddr string) (string, net.IP) {
	peer, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		peer = remoteAddr
	}

	return peer, net.ParseIP(peer)
}

// ClientIP is the address a request came from, for rate limits and logs.
//
// Behind cloudflared or Traefik, the connection comes from the proxy, and
// the proxy names the client in a header. That header is believed only from
// a trusted proxy. Gin's own ClientIP isn't used: it trusts X-Forwarded-For
// from anyone, so a client could pick its own address and dodge the login
// limit.
func ClientIP(ctx http.Context) string {
	return proxies().clientIP(ctx.Request().Origin().RemoteAddr, ctx.Request().Header)
}

func (c proxyConfig) clientIP(remoteAddr string, header func(key string, defaultValue ...string) string) string {
	peer, ip := peerIP(remoteAddr)
	if c.header != "" && ip != nil && c.trusts(ip) {
		if forwarded := strings.TrimSpace(header(c.header)); net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}

	return peer
}

// IsLocalRequest reports whether a request comes from this machine itself,
// not through a tunnel or proxy on it: from loopback or another of its own
// addresses, with no header a proxy adds.
func IsLocalRequest(ctx http.Context) bool {
	return proxies().isLocal(ctx.Request().Origin().RemoteAddr, ctx.Request().Header)
}

func (c proxyConfig) isLocal(remoteAddr string, header func(key string, defaultValue ...string) string) bool {
	_, ip := peerIP(remoteAddr)
	if ip == nil || !(ip.IsLoopback() || c.own != nil && c.own(ip)) {
		return false
	}
	for _, name := range []string{c.header, "CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if name != "" && header(name) != "" {
			return false
		}
	}

	return true
}
