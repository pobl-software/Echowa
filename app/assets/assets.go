// Package assets is where Echowa reads its built frontend (public/build) and
// its root template (resources/views). A release build embeds both
// (embed_release.go, package main), so the binary is a single file.
// Otherwise they're read from the project directory, where Vite's hot file
// and each new `npm run build` show up at once.
package assets

import (
	"io/fs"
	nethttp "net/http"
	"os"

	"github.com/goravel/framework/support/path"
)

// Paths inside FS.
const (
	RootTemplate = "resources/views/app.html"
	BuildDir     = "public/build"
	Manifest     = "public/build/manifest.json"
	HotFile      = "public/hot"
	// Echowa's licence and every third-party notice, from `make licenses`.
	Licenses = "dist/licenses.txt"
)

var embedded fs.FS

// Embed makes FS serve fsys. The release build calls it before boot.
func Embed(fsys fs.FS) {
	embedded = fsys
}

// Embedded reports whether this is a release build.
func Embedded() bool {
	return embedded != nil
}

// FS holds the files, with paths relative to the project root.
func FS() fs.FS {
	if embedded != nil {
		return embedded
	}

	return os.DirFS(path.Base())
}

// Build serves public/build without directory listings. It looks up FS on
// every request, so it always serves what FS holds now.
func Build() nethttp.FileSystem {
	return buildFiles{}
}

type buildFiles struct{}

func (buildFiles) Open(name string) (nethttp.File, error) {
	sub, err := fs.Sub(FS(), BuildDir)
	if err != nil {
		return nil, err
	}
	file, err := nethttp.FS(sub).Open(name)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err != nil || info.IsDir() {
		_ = file.Close()
		return nil, fs.ErrNotExist
	}

	return file, nil
}
