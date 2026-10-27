// Package cli names the commands the binary runs without booting Echowa:
// `echowa setup`, `echowa version`, `echowa update` and `echowa licenses`
// (main.go).
//
// Goravel refuses to start without APP_KEY, and checks as soon as config is
// first read, in the config package's init. A fresh server has no key until
// `echowa setup` writes one. So this package's init turns the check off for
// these commands only, the way Goravel does for its own key:generate. The
// config package imports it, which makes it run first.
package cli

import (
	"os"
	"slices"

	"github.com/goravel/framework/support"
)

// Standalone are the commands that don't boot the app.
var Standalone = []string{"setup", "version", "--version", "update", "licenses"}

func init() {
	if len(os.Args) >= 2 && slices.Contains(Standalone, os.Args[1]) {
		support.DontVerifyAppKey = true
	}
}
