package docker

import (
	"fmt"
	"strconv"

	"goravel/app/rules"
)

// Traefik entrypoints defined in app/setup/files/traefik.yml.
const (
	EntrypointHTTP  = "web"
	EntrypointHTTPS = "websecure"
)

// TraefikLabels returns the service labels that route route.Domain to the
// service's route.Port through Traefik, under the router and service name
// router. TLS routes use the websecure entrypoint and get certificates from
// certResolver. Plain routes use web. deploy/README.md explains each label.
func TraefikLabels(router string, route Route, certResolver string) (map[string]string, error) {
	if router == "" {
		return nil, fmt.Errorf("traefik labels: empty router name")
	}
	// Also keeps backticks and parentheses out of Host(), where they could
	// add rules of their own.
	if !rules.IsDomain(route.Domain) {
		return nil, fmt.Errorf("traefik labels: invalid domain %q", route.Domain)
	}
	if route.Port < 1 || route.Port > 65535 {
		return nil, fmt.Errorf("traefik labels: invalid port %d", route.Port)
	}
	if route.TLS && certResolver == "" {
		return nil, fmt.Errorf("traefik labels: TLS needs a certificate resolver")
	}

	r := "traefik.http.routers." + router
	labels := map[string]string{
		"traefik.enable": "true",
		// The Swarm VIP drops stopped tasks at once, so start-first
		// updates don't send requests to a task that's going away.
		"traefik.swarm.lbswarm": "true",
		r + ".rule":             "Host(`" + route.Domain + "`)",
		r + ".entrypoints":      EntrypointHTTP,
		r + ".service":          router,
		"traefik.http.services." + router + ".loadbalancer.server.port": strconv.Itoa(route.Port),
	}
	if route.TLS {
		labels[r+".entrypoints"] = EntrypointHTTPS
		labels[r+".tls.certresolver"] = certResolver
	}

	return labels, nil
}
