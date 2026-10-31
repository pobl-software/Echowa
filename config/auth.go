package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("auth", map[string]any{
		"defaults": map[string]any{
			"guard": "web",
		},

		"guards": map[string]any{
			"web": map[string]any{
				"driver":   "session",
				"provider": "user",
			},
		},

		"providers": map[string]any{
			"user": map[string]any{
				"driver": "orm",
			},
		},
	})
}
