package bootstrap

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/queue"

	"goravel/app/deploy"
)

func Jobs() []queue.Job {
	return []queue.Job{
		&deploy.Job{},
	}
}

// Runners start with the server, next to Goravel's own (HTTP, queue
// worker, scheduler).
func Runners() []foundation.Runner {
	return []foundation.Runner{
		deploy.NewRecoveryRunner(),
	}
}
