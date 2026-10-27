package docker

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/image"
)

// normalizeRef parses an image reference, adding ":latest" when it has
// neither a tag nor a digest.
func normalizeRef(ref string) (reference.Named, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference %q: %w", ref, err)
	}

	return reference.TagNameOnly(named), nil
}

// digestRef picks the entry of repoDigests that belongs to named's
// repository, e.g. "ghcr.io/owner/app@sha256:...". An image pulled from
// several repositories has one entry per repository.
func digestRef(named reference.Named, repoDigests []string) (string, error) {
	for _, rd := range repoDigests {
		parsed, err := reference.ParseNormalizedNamed(rd)
		if err != nil {
			continue
		}
		if _, ok := parsed.(reference.Canonical); ok && parsed.Name() == named.Name() {
			return reference.FamiliarString(parsed), nil
		}
	}

	return "", fmt.Errorf("no registry digest for %s (images built locally have none)", reference.FamiliarString(named))
}

// pruneCandidates returns the images PruneImages may remove: every
// reference is in one of opts.Repositories, none is in opts.Keep, no
// container is known to use it, and it was created before now-OlderThan.
// Images with no references at all aren't Echowa's to judge.
func pruneCandidates(images []image.Summary, opts PruneOptions, now time.Time) []image.Summary {
	repos := map[string]bool{}
	for _, repo := range opts.Repositories {
		if named, err := reference.ParseNormalizedNamed(repo); err == nil {
			repos[named.Name()] = true
		}
	}
	keep := map[string]bool{}
	for _, ref := range opts.Keep {
		if named, err := reference.ParseNormalizedNamed(ref); err == nil {
			keep[named.String()] = true
		}
	}
	cutoff := now.Add(-opts.OlderThan)

	var candidates []image.Summary
	for _, img := range images {
		refs := imageRefs(img)
		if len(refs) == 0 || img.Containers > 0 || !time.Unix(img.Created, 0).Before(cutoff) {
			continue
		}
		ours := !slices.ContainsFunc(refs, func(ref reference.Named) bool { return !repos[ref.Name()] })
		kept := slices.ContainsFunc(refs, func(ref reference.Named) bool { return keep[ref.String()] })
		if ours && !kept {
			candidates = append(candidates, img)
		}
	}

	return candidates
}

func imageRefs(img image.Summary) []reference.Named {
	var refs []reference.Named
	for _, raw := range slices.Concat(img.RepoTags, img.RepoDigests) {
		if named, err := reference.ParseNormalizedNamed(raw); err == nil {
			refs = append(refs, named)
		}
	}

	return refs
}

// imageLabel names an image for the prune report.
func imageLabel(img image.Summary) string {
	if refs := imageRefs(img); len(refs) > 0 {
		return reference.FamiliarString(refs[0])
	}

	return img.ID
}

// SameRepository reports whether ref (e.g. "traefik/whoami@sha256:...")
// names an image in repository (e.g. "docker.io/traefik/whoami"), however
// either is spelled.
func SameRepository(ref, repository string) bool {
	a, errA := reference.ParseNormalizedNamed(ref)
	b, errB := reference.ParseNormalizedNamed(repository)

	return errA == nil && errB == nil && a.Name() == b.Name()
}

// SameRegistry reports whether host (a registry login's server, e.g.
// "ghcr.io") is the registry ref pulls from. Docker Hub goes by several
// names.
func SameRegistry(host, ref string) bool {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return false
	}

	return canonicalRegistry(host) == canonicalRegistry(reference.Domain(named))
}

func canonicalRegistry(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return "docker.io"
	}

	return host
}
