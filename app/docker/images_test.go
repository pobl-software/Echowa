package docker

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	shaA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestDigestRef(t *testing.T) {
	tests := []struct {
		name        string
		ref         string
		repoDigests []string
		want        string
		wantErr     bool
	}{
		{
			name:        "GHCR tag",
			ref:         "ghcr.io/owner/app:v1",
			repoDigests: []string{"ghcr.io/owner/app@" + shaA},
			want:        "ghcr.io/owner/app@" + shaA,
		},
		{
			name:        "Docker Hub short name",
			ref:         "traefik/whoami",
			repoDigests: []string{"traefik/whoami@" + shaA},
			want:        "traefik/whoami@" + shaA,
		},
		{
			name:        "image pulled from two repositories",
			ref:         "ghcr.io/owner/app:v1",
			repoDigests: []string{"docker.io/owner/app@" + shaB, "ghcr.io/owner/app@" + shaA},
			want:        "ghcr.io/owner/app@" + shaA,
		},
		{
			name:    "built locally, no digest",
			ref:     "app:dev",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			named, err := normalizeRef(tt.ref)
			require.NoError(t, err)

			got, err := digestRef(named, tt.repoDigests)
			if tt.wantErr {
				assert.ErrorContains(t, err, "no registry digest")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeRefAddsLatest(t *testing.T) {
	named, err := normalizeRef("traefik/whoami")
	require.NoError(t, err)
	assert.Equal(t, "docker.io/traefik/whoami:latest", named.String())

	_, err = normalizeRef("Not A Ref")
	assert.ErrorContains(t, err, "invalid image reference")
}

func TestPruneCandidates(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	old := now.Add(-100 * time.Hour).Unix()
	recent := now.Add(-time.Hour).Unix()
	img := func(id string, created int64, tags, digests []string) image.Summary {
		return image.Summary{ID: id, Created: created, RepoTags: tags, RepoDigests: digests, Containers: -1}
	}

	images := []image.Summary{
		img("old-ours", old, []string{"ghcr.io/owner/app:v1"}, []string{"ghcr.io/owner/app@" + shaA}),
		img("kept", old, nil, []string{"ghcr.io/owner/app@" + shaB}),
		img("recent", recent, []string{"ghcr.io/owner/app:v3"}, nil),
		img("not-ours", old, []string{"postgres:18"}, nil),
		img("shared", old, []string{"ghcr.io/owner/app:v0", "mine/other:v0"}, nil),
		img("dangling", old, []string{"<none>:<none>"}, nil),
		// A tag moved on to a newer pull: the old image keeps only its digest.
		img("untagged-ours", old, []string{"<none>:<none>"}, []string{"ghcr.io/owner/app@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}),
		{ID: "in-use", Created: old, RepoTags: []string{"ghcr.io/owner/app:v2"}, Containers: 1},
		img("hub-short-name", old, []string{"traefik/whoami:v1.11.0"}, nil),
	}

	got := pruneCandidates(images, PruneOptions{
		Repositories: []string{"ghcr.io/owner/app", "docker.io/traefik/whoami"},
		OlderThan:    72 * time.Hour,
		Keep:         []string{"ghcr.io/owner/app@" + shaB},
	}, now)

	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []string{"old-ours", "untagged-ours", "hub-short-name"}, ids)
}

func TestSameRepository(t *testing.T) {
	assert.True(t, SameRepository("ghcr.io/owner/app@"+shaA, "ghcr.io/owner/app"))
	assert.True(t, SameRepository("traefik/whoami@"+shaA, "docker.io/traefik/whoami"))
	assert.False(t, SameRepository("ghcr.io/owner/old@"+shaA, "ghcr.io/owner/app"))
	assert.False(t, SameRepository("Not A Ref", "ghcr.io/owner/app"))
}

func TestSameRegistry(t *testing.T) {
	assert.True(t, SameRegistry("ghcr.io", "ghcr.io/owner/app:v1"))
	assert.True(t, SameRegistry("GHCR.io", "ghcr.io/owner/app@"+shaA))
	assert.True(t, SameRegistry("docker.io", "nginx"))
	assert.True(t, SameRegistry("index.docker.io", "owner/app"))
	assert.True(t, SameRegistry("registry.example.com:5000", "registry.example.com:5000/app"))
	assert.False(t, SameRegistry("ghcr.io", "nginx"), "a GHCR token must not go to Docker Hub")
	assert.False(t, SameRegistry("ghcr.io", "evil.example/owner/app"))
	assert.False(t, SameRegistry("registry.example.com", "registry.example.com:5000/app"))
	assert.False(t, SameRegistry("ghcr.io", "Not A Ref"))
}
