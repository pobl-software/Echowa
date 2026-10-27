package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("auth", map[string]any{
		"defaults": map[string]any{
			// Browser pages use the session guard. facades.Auth(ctx) uses it
			// unless you pick another with .Guard("api").
			"guard": "web",
		},

		"guards": map[string]any{
			"web": map[string]any{
				"driver":   "session",
				"provider": "user",
			},
			// For token-authenticated JSON APIs outside the web middleware
			// group. Needs JWT_SECRET (./artisan jwt:secret).
			"api": map[string]any{
				"driver":   "jwt",
				"provider": "user",
			},
		},

		"providers": map[string]any{
			"user": map[string]any{
				"driver": "orm",
			},
		},

		// "Remember me" cookie (see app/services/auth.go).
		"remember": map[string]any{
			"cookie": "remember_web",
			"days":   config.Env("AUTH_REMEMBER_DAYS", 30),
		},
	})
}
