package rules

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChecks(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)

	tests := []struct {
		name  string
		check func(string) bool
		valid []string
		bad   []string
	}{
		{
			name:  "IsSlug",
			check: IsSlug,
			valid: []string{"blog", "my-app", "a", "app2", "0day", strings.Repeat("a", 40)},
			bad:   []string{"", "Blog", "my_app", "-app", "app-", "my.app", "my app", "é", strings.Repeat("a", 41)},
		},
		{
			name:  "IsDomain",
			check: IsDomain,
			valid: []string{"example.com", "blog.example.com", "whoami.localhost", "localhost", "a-b.c-d.io", "xn--bcher-kva.example"},
			bad: []string{"", "Example.com", "https://example.com", "example.com/path", "example.com:443", "*.example.com",
				"example.com.", "-a.example.com", "a..com", "a.com`) || PathPrefix(`/", "a b.com", strings.Repeat("a", 64) + ".com"},
		},
		{
			name:  "IsEnvKey",
			check: IsEnvKey,
			valid: []string{"PORT", "DATABASE_URL", "_PRIVATE", "lower_case", "A1"},
			bad:   []string{"", "1ABC", "MY-VAR", "MY VAR", "A=B", "ÉTÉ", strings.Repeat("A", 256)},
		},
		{
			name:  "IsRepository",
			check: IsRepository,
			valid: []string{"nginx", "traefik/whoami", "ghcr.io/owner/app", "registry.example.com:5000/team/app"},
			bad:   []string{"", "nginx:latest", "ghcr.io/owner/app:v1", "ghcr.io/owner/app@" + digest, "GHCR.io/Owner/App", "ghcr.io/owner/app/"},
		},
		{
			name:  "IsTag",
			check: IsTag,
			valid: []string{"latest", "v1.4.2", "main_2026-10-23", "1"},
			bad:   []string{"", ".hidden", "-x", "v1:2", "a/b", strings.Repeat("a", 129)},
		},
		{
			name:  "IsDigest",
			check: IsDigest,
			valid: []string{digest},
			bad:   []string{"", "sha256:abc", "sha256:" + strings.Repeat("AB", 32), "sha512:" + strings.Repeat("ab", 64)},
		},
		{
			name:  "IsDeployRef",
			check: IsDeployRef,
			valid: []string{"latest", "v2.0.0", digest},
			bad:   []string{"", "ghcr.io/owner/app:v1", "sha256:abc"},
		},
		{
			name:  "IsRegistryHost",
			check: IsRegistryHost,
			valid: []string{"ghcr.io", "registry.example.com:5000", "localhost:5000", "10.0.0.5", "10.0.0.5:5000", "[::1]:5000"},
			bad:   []string{"", "https://ghcr.io", "ghcr.io/owner", "ghcr.io:", "ghcr.io:0", "ghcr.io:99999", "GHCR.io", "ghcr.io:abc"},
		},
		{
			name:  "IsCron",
			check: IsCron,
			valid: []string{"* * * * *", "0 0 1 * *", "*/5 9-17 * * 1-5", "0 0 * * SUN", " 0  * * * * "},
			bad:   []string{"", "@hourly", "@every 5m", "* * * *", "0 * * * * *", "60 * * * *", "0 0 30 2 *", "CRON_TZ=UTC 0 * * *"},
		},
		{
			name:  "IsTimezone",
			check: IsTimezone,
			valid: []string{"UTC", "Europe/London", "America/New_York", "Asia/Kathmandu"},
			bad:   []string{"", "Local", "Mars/Olympus", "Europe/Nowhere", "../etc/passwd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, s := range tt.valid {
				assert.True(t, tt.check(s), "%q should be valid", s)
			}
			for _, s := range tt.bad {
				assert.False(t, tt.check(s), "%q should be invalid", s)
			}
		})
	}
}

func TestIsOtherHost(t *testing.T) {
	ui := "https://echowa.example.com:8192"
	for _, domain := range []string{"blog.example.com", "example.com", "app.echowa.example.com"} {
		assert.True(t, IsOtherHost(domain, ui), domain)
	}
	assert.False(t, IsOtherHost("echowa.example.com", ui), "the port doesn't separate cookies")
	assert.False(t, IsOtherHost("localhost", "http://localhost:3000"))
}

func TestIsPort(t *testing.T) {
	for _, n := range []int{1, 80, 3000, 65535} {
		assert.True(t, IsPort(n), n)
	}
	for _, n := range []int{-1, 0, 65536} {
		assert.False(t, IsPort(n), n)
	}
}
