package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/goravel/framework/support/env"

	"goravel/app/assets"
	"goravel/app/facades"
	"goravel/app/release"
	"goravel/app/services"
	"goravel/app/setup"
	"goravel/bootstrap"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Println("echowa", release.Version)
			return
		case "update":
			os.Exit(setup.UpdateMain(os.Args[2:], os.Stdout))
		case "setup":
			os.Exit(setup.Main(os.Args[2:], os.Stdout))
		case "licenses":
			text, err := fs.ReadFile(assets.FS(), assets.Licenses)
			if err != nil {
				fmt.Fprintln(os.Stderr, "no licence notices in this build (make licenses writes them):", err)
				os.Exit(1)
			}
			_, _ = os.Stdout.Write(text)
			return
		case "backup":
			// Shorthand for `echowa artisan backup <dest>`.
			os.Args = append([]string{os.Args[0], "artisan"}, os.Args[1:]...)
		}
	}

	app := bootstrap.Boot()

	// The server brings its database up to date before it serves anything,
	// so an empty data directory or a new release needs no extra step.
	// Artisan commands leave it alone.
	if !env.IsArtisan() {
		if err := facades.Artisan().Call("migrate"); err != nil {
			fmt.Fprintln(os.Stderr, "migrate:", err)
			os.Exit(1)
		}
		// Opens or recreates the metrics file now, so a problem with it is
		// logged at boot. Echowa runs without it.
		_, _ = services.MetricsDB()
	}

	app.Start()
}
