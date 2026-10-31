package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"goravel/app/services"
)

// HostMaintenance prints what the server needs doing as JSON on stdout,
// as the Server page reads it, and how long that took on stderr. It's for
// checking each read under echowa.service's sandbox (docs/verification.md,
// "Server maintenance"):
//
//	deploy/dev-vm.sh artisan host:maintenance
//	sudo echowa-artisan host:maintenance
//
// Unlike the page, it waits for the apt simulation to finish.
type HostMaintenance struct{}

func (r *HostMaintenance) Signature() string {
	return "host:maintenance"
}

func (r *HostMaintenance) Description() string {
	return "Read what the server needs doing, and print it"
}

func (r *HostMaintenance) Extend() command.Extend {
	return command.Extend{Category: "host"}
}

func (r *HostMaintenance) Handle(console.Context) error {
	services.HostReadLimit = time.Minute
	start := time.Now()
	maintenance := services.HostMaintenance(context.Background())
	fmt.Fprintf(os.Stderr, "read in %s; needs attention: %t\n", time.Since(start).Round(time.Millisecond), services.HostAttention())

	out, err := json.MarshalIndent(maintenance, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))

	return nil
}
