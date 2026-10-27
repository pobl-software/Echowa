//go:build release

package main

import (
	"embed"

	"goravel/app/assets"
)

// The release build (make release) is one file: the built frontend, the
// root template and the licence notices are compiled in. Run `npm run build`
// and `make licenses` first.
//
//go:embed public/build resources/views dist/licenses.txt
var files embed.FS

func init() {
	assets.Embed(files)
}
