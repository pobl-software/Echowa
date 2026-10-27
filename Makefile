# Release builds of Echowa: one static binary per platform, with the
# frontend and the root template embedded (embed_release.go, ADR 0020).
#
#   make release   dist/echowa-linux-amd64, dist/echowa-linux-arm64, checksums
#   make build     dist/echowa for this machine, to try a release build here
#   make licenses  dist/licenses.txt, the notices every release embeds (ADR 0033)
#   make check     gofmt, go vet, go test, Biome, typecheck
#   make fmt       gofmt and Biome, writing their fixes
#   make publish TAG=vX.Y.Z   tag HEAD with release notes you edit, then publish
#   make publish   sign the tagged release and upload it to R2 (scripts/publish.sh)
#   make release-key   create the signing key, once

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# The release channel (ADR 0021): a public R2 bucket on a custom domain.
RELEASE_URL ?= https://get.echowa.dev
RELEASE_BUCKET ?= echowa-installer
MINISIGN_KEY ?= $(HOME)/.minisign/echowa.key
LDFLAGS := -s -w -X goravel/app/release.Version=$(VERSION) -X goravel/app/release.URL=$(RELEASE_URL)
BUILDFLAGS := -tags release -trimpath
PLATFORMS := linux/amd64 linux/arm64

.PHONY: release build assets licenses check fmt clean publish release-key

release: assets licenses
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "building dist/echowa-$$os-$$arch ($(VERSION))"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(BUILDFLAGS) -ldflags "$(LDFLAGS)" -o dist/echowa-$$os-$$arch . || exit 1; \
	done
	@cd dist && shasum -a 256 echowa-linux-* > SHA256SUMS && cat SHA256SUMS

build: assets licenses
	@mkdir -p dist
	CGO_ENABLED=0 go build $(BUILDFLAGS) -ldflags "$(LDFLAGS)" -o dist/echowa .

# A clean frontend build: the binary embeds whatever public/build holds at
# compile time, so never ship a stale one. public/hot (Vite's dev server) is
# never embedded, so a running `npm run dev` is left alone.
assets:
	npm ci --no-audit --no-fund
	rm -rf public/build
	npm run build

# Echowa's licence and NOTICE, then the licence files of every Go module in
# the Linux binaries and every npm package the build bundled (the list
# `npm run build` writes to dist/npm-licenses.json).
licenses:
	go run ./scripts/licenses

check:
	@unformatted=$$(git ls-files '*.go' | xargs gofmt -l); \
		if [ -n "$$unformatted" ]; then echo "gofmt would change these (make fmt):"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go test ./...
	npm run check
	npm run typecheck

fmt:
	git ls-files '*.go' | xargs gofmt -w
	npm run fix

clean:
	rm -rf dist

publish:
	RELEASE_URL=$(RELEASE_URL) RELEASE_BUCKET=$(RELEASE_BUCKET) MINISIGN_KEY=$(MINISIGN_KEY) scripts/publish.sh $(if $(TAG),--tag $(TAG))

# The release signing key: the secret half stays at MINISIGN_KEY, behind a
# passphrase (back it up offline); the public half is committed and built
# into every binary. Run once.
release-key:
	@grep -q 'no release key yet' app/release/minisign.pub || { echo "app/release/minisign.pub already holds a key; a new one would orphan every installed server"; exit 1; }
	@test ! -e $(MINISIGN_KEY) || { echo "$(MINISIGN_KEY) already exists"; exit 1; }
	@mkdir -p $(dir $(MINISIGN_KEY)) && chmod 700 $(dir $(MINISIGN_KEY))
	minisign -G -f -p app/release/minisign.pub -s $(MINISIGN_KEY) -c "echowa release key"
	@echo "Commit app/release/minisign.pub. Back up $(MINISIGN_KEY) and its passphrase offline."
