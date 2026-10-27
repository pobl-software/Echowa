package services

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func headers(values map[string]string) func(string, ...string) string {
	return func(key string, _ ...string) string { return values[key] }
}

func TestClientIP(t *testing.T) {
	cf := headers(map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "198.51.100.1"})
	tunnel := proxyConfig{header: "CF-Connecting-IP"}
	_, bridge, _ := net.ParseCIDR("172.18.0.0/16")
	traefik := proxyConfig{header: "X-Real-IP", nets: []*net.IPNet{bridge}}

	tests := []struct {
		name       string
		cfg        proxyConfig
		remoteAddr string
		header     func(string, ...string) string
		want       string
	}{
		{"cloudflared on loopback", tunnel, "127.0.0.1:51234", cf, "203.0.113.7"},
		{"cloudflared on IPv6 loopback", tunnel, "[::1]:51234", cf, "203.0.113.7"},
		{"a remote peer can't claim an address", tunnel, "192.0.2.10:443", cf, "192.0.2.10"},
		{"X-Forwarded-For is never trusted by default", tunnel, "127.0.0.1:51234", headers(map[string]string{"X-Forwarded-For": "198.51.100.1"}), "127.0.0.1"},
		{"no header configured", proxyConfig{}, "127.0.0.1:51234", cf, "127.0.0.1"},
		{"garbage in the header", tunnel, "127.0.0.1:51234", headers(map[string]string{"CF-Connecting-IP": "admin"}), "127.0.0.1"},
		{"Traefik on the Docker bridge", traefik, "172.18.0.3:40000", headers(map[string]string{"X-Real-IP": "203.0.113.7"}), "203.0.113.7"},
		{"a direct client, HTTP access", traefik, "203.0.113.9:40000", headers(map[string]string{"X-Real-IP": "10.0.0.1"}), "203.0.113.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.clientIP(tt.remoteAddr, tt.header))
		})
	}
}

func TestIsLocal(t *testing.T) {
	none := headers(nil)
	host := net.ParseIP("192.0.2.5")
	cfg := proxyConfig{header: "CF-Connecting-IP", own: func(ip net.IP) bool { return ip.Equal(host) }}

	assert.True(t, cfg.isLocal("127.0.0.1:51234", none), "curl on the server")
	assert.True(t, cfg.isLocal("[::1]:51234", none))
	assert.True(t, cfg.isLocal("192.0.2.5:51234", none), "the host, masqueraded by Docker on its way to the bridge")
	assert.False(t, cfg.isLocal("192.0.2.10:443", none))
	assert.False(t, cfg.isLocal("172.18.0.3:51234", none), "a container on the bridge")
	assert.False(t, cfg.isLocal("127.0.0.1:51234", headers(map[string]string{"CF-Connecting-IP": "203.0.113.7"})), "through cloudflared")
	assert.False(t, cfg.isLocal("127.0.0.1:51234", headers(map[string]string{"X-Forwarded-For": "198.51.100.1"})), "through another proxy")
	assert.False(t, cfg.isLocal("192.0.2.5:51234", headers(map[string]string{"X-Real-IP": "198.51.100.1"})))
}

func TestOwnAddress(t *testing.T) {
	assert.True(t, ownAddress(net.ParseIP("127.0.0.1")))
	assert.False(t, ownAddress(net.ParseIP("192.0.2.200")))
}
