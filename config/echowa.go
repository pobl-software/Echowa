package config

import (
	"goravel/app/facades"
)

// Echowa's own settings (docs/mvp.md, "Configuration").
func init() {
	config := facades.Config()
	config.Add("echowa", map[string]any{
		"docker": map[string]any{
			// Docker API endpoint. Empty uses DOCKER_HOST or the default socket.
			"host": config.Env("ECHOWA_DOCKER_HOST", ""),
			// Overlay network shared by Traefik and the apps.
			"network": config.Env("ECHOWA_NETWORK", "echowa-public"),
			// Traefik certificate resolver for apps with TLS.
			"cert_resolver": config.Env("ECHOWA_CERT_RESOLVER", "le"),
			// How long Swarm watches a new task before accepting an update.
			"update_monitor": config.Env("ECHOWA_UPDATE_MONITOR", "30s"),
			// How long a deploy may take before Echowa gives up waiting.
			"deploy_timeout": config.Env("ECHOWA_DEPLOY_TIMEOUT", "10m"),
		},
		// Header that names the real client when a request comes through a
		// proxy on loopback (cloudflared sets CF-Connecting-IP). Used for the
		// login rate limit. Empty trusts no header.
		"client_ip_header": config.Env("ECHOWA_CLIENT_IP_HEADER", "CF-Connecting-IP"),
		// Networks (CIDR, comma-separated) whose requests may name the
		// client in that header, besides loopback: Traefik's, when it
		// serves the UI over HTTPS.
		"trusted_proxies": config.Env("ECHOWA_TRUSTED_PROXIES", ""),
		// Where Traefik serves HTTPS on this machine. The Domains tab
		// reads each domain's certificate from here, not over DNS.
		"traefik_https": config.Env("ECHOWA_TRAEFIK_HTTPS", "127.0.0.1:443"),
		"prune": map[string]any{
			// Images of each app's last N successful deployments are never
			// pruned, so rolling back to them needs no pull.
			"keep": config.Env("ECHOWA_PRUNE_KEEP", 5),
			// Only images created longer ago than this are pruned.
			"min_age": config.Env("ECHOWA_PRUNE_MIN_AGE", "72h"),
		},
	})
}
