package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("queue", map[string]any{
		// Default Queue Connection Name
		// Deploys run on the database queue, worked by Goravel's queue runner
		// inside the server process (ADR 0017). Tests and artisan commands
		// don't start the worker.
		"default": config.Env("QUEUE_CONNECTION", "database"),

		// Queue Connections
		//
		// Here you may configure the connection information for each server that is used by your application.
		// Drivers: "sync", "database", "custom"
		"connections": map[string]any{
			"sync": map[string]any{
				"driver": "sync",
			},
			"database": map[string]any{
				"driver":     "database",
				"connection": "sqlite",
				"queue":      "default",
				// Workers, so different apps deploy in parallel. One app's
				// deployments still run one at a time (app/deploy).
				"concurrent": 4,
			},
		},

		// Failed Queue Jobs
		//
		// These options configure the behavior of failed queue job logging so you
		// can control how and where failed jobs are stored.
		"failed": map[string]any{
			"database": "sqlite",
			"table":    "failed_jobs",
		},
	})
}
