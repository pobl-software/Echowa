package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraefikLabels(t *testing.T) {
	tests := []struct {
		name     string
		router   string
		route    Route
		resolver string
		want     map[string]string
		wantErr  string
	}{
		{
			name:     "TLS route",
			router:   "echowa-blog",
			route:    Route{Domain: "blog.example.com", Port: 8080, TLS: true},
			resolver: "le",
			want: map[string]string{
				"traefik.enable":                                             "true",
				"traefik.swarm.lbswarm":                                      "true",
				"traefik.http.routers.echowa-blog.rule":                      "Host(`blog.example.com`)",
				"traefik.http.routers.echowa-blog.entrypoints":               "websecure",
				"traefik.http.routers.echowa-blog.service":                   "echowa-blog",
				"traefik.http.routers.echowa-blog.tls.certresolver":          "le",
				"traefik.http.services.echowa-blog.loadbalancer.server.port": "8080",
			},
		},
		{
			name:     "plain HTTP route has no certresolver",
			router:   "echowa-whoami",
			route:    Route{Domain: "whoami.localhost", Port: 80},
			resolver: "le",
			want: map[string]string{
				"traefik.enable":                                               "true",
				"traefik.swarm.lbswarm":                                        "true",
				"traefik.http.routers.echowa-whoami.rule":                      "Host(`whoami.localhost`)",
				"traefik.http.routers.echowa-whoami.entrypoints":               "web",
				"traefik.http.routers.echowa-whoami.service":                   "echowa-whoami",
				"traefik.http.services.echowa-whoami.loadbalancer.server.port": "80",
			},
		},
		{
			name:   "single-label host",
			router: "echowa-x",
			route:  Route{Domain: "localhost", Port: 3000},
			want: map[string]string{
				"traefik.enable":                                          "true",
				"traefik.swarm.lbswarm":                                   "true",
				"traefik.http.routers.echowa-x.rule":                      "Host(`localhost`)",
				"traefik.http.routers.echowa-x.entrypoints":               "web",
				"traefik.http.routers.echowa-x.service":                   "echowa-x",
				"traefik.http.services.echowa-x.loadbalancer.server.port": "3000",
			},
		},
		{name: "rule injection", router: "r", route: Route{Domain: "a.com`) || PathPrefix(`/", Port: 80}, wantErr: "invalid domain"},
		{name: "uppercase", router: "r", route: Route{Domain: "Blog.example.com", Port: 80}, wantErr: "invalid domain"},
		{name: "empty domain", router: "r", route: Route{Port: 80}, wantErr: "invalid domain"},
		{name: "leading hyphen", router: "r", route: Route{Domain: "-a.example.com", Port: 80}, wantErr: "invalid domain"},
		{name: "trailing dot", router: "r", route: Route{Domain: "a.example.com.", Port: 80}, wantErr: "invalid domain"},
		{name: "wildcard", router: "r", route: Route{Domain: "*.example.com", Port: 80}, wantErr: "invalid domain"},
		{name: "port zero", router: "r", route: Route{Domain: "a.com", Port: 0}, wantErr: "invalid port"},
		{name: "port too high", router: "r", route: Route{Domain: "a.com", Port: 65536}, wantErr: "invalid port"},
		{name: "TLS without resolver", router: "r", route: Route{Domain: "a.com", Port: 80, TLS: true}, wantErr: "certificate resolver"},
		{name: "no router name", route: Route{Domain: "a.com", Port: 80}, wantErr: "empty router"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TraefikLabels(tt.router, tt.route, tt.resolver)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
