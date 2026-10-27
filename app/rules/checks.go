// Package rules is the one place that defines what valid input looks like
// for registries, apps, env vars and deploy refs. The Is* functions are the
// checks themselves. The Goravel rules in rules.go wrap them for
// ctx.Request().Validate. forms.go has a rule map per form for controllers.
package rules

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/distribution/reference"

	"goravel/app/tasks"
)

// MaxSlugLength keeps "echowa-<slug>" well inside Swarm's 63-character
// limit on service names.
const MaxSlugLength = 40

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// A lowercase DNS name: dot-separated labels of letters, digits and
	// inner hyphens. It also keeps backticks and parentheses out of
	// Traefik's Host() rule.
	domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tagPattern    = regexp.MustCompile(`^` + reference.TagRegexp.String() + `$`)
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// IsSlug: an app name. Lowercase letters, digits and inner hyphens, at most
// MaxSlugLength characters. It must also work as a DNS label and as part of
// a Swarm service name.
func IsSlug(s string) bool {
	return len(s) <= MaxSlugLength && slugPattern.MatchString(s)
}

// IsDomain: a lowercase hostname such as "blog.example.com". No scheme,
// port, path, wildcard or trailing dot.
func IsDomain(s string) bool {
	return len(s) <= 253 && domainPattern.MatchString(s)
}

// IsOtherHost: domain isn't rawURL's host. Cookies ignore the port, so an
// app on the host Echowa's UI is on (APP_URL) would be sent the admin's
// session (ADR 0022).
func IsOtherHost(domain, rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err != nil || !strings.EqualFold(u.Hostname(), domain)
}

// IsEnvKey: a POSIX-style environment variable name.
func IsEnvKey(s string) bool {
	return len(s) <= 255 && envKeyPattern.MatchString(s)
}

// IsPort: a TCP port.
func IsPort(n int) bool {
	return n >= 1 && n <= 65535
}

// IsRepository: an image repository without a tag or digest, e.g.
// "ghcr.io/owner/app" or "nginx".
func IsRepository(s string) bool {
	named, err := reference.ParseNormalizedNamed(s)
	return err == nil && reference.IsNameOnly(named)
}

// IsTag: an image tag such as "latest" or "v1.4.2".
func IsTag(s string) bool {
	return tagPattern.MatchString(s)
}

// IsDigest: "sha256:" and 64 lowercase hex digits.
func IsDigest(s string) bool {
	return digestPattern.MatchString(s)
}

// IsDeployRef: what a deploy may ask for, a tag or a digest.
func IsDeployRef(s string) bool {
	return IsTag(s) || IsDigest(s)
}

// IsRegistryHost: a registry address, a hostname or IP with an optional
// port, e.g. "ghcr.io" or "registry.example.com:5000".
func IsRegistryHost(s string) bool {
	host := s
	if h, port, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(port)
		if err != nil || !IsPort(n) {
			return false
		}
		host = h
	} else if strings.Contains(s, ":") {
		return false
	}

	return IsDomain(host) || net.ParseIP(host) != nil
}

// IsCron: a five-field cron expression such as "*/5 * * * *", one that
// runs at some point (ADR 0032).
func IsCron(s string) bool {
	_, err := tasks.Parse(s, "UTC")
	return err == nil
}

// IsTimezone: an IANA zone such as "Europe/London" or "UTC".
func IsTimezone(s string) bool {
	_, err := tasks.LoadZone(s)
	return err == nil
}
