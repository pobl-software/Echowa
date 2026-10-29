package models

import (
	"github.com/goravel/framework/database/orm"
)

// ServicePrefix is prepended to an app's name to get its Swarm service name.
const ServicePrefix = "echowa-"

// App is one image deployed as one Swarm service.
type App struct {
	orm.Model
	// Name is a DNS-safe slug, unique, and can't change after creation: it
	// names the Swarm service and the Traefik router.
	Name string `json:"name"`
	// Repository is the image without a tag, e.g. "ghcr.io/owner/app".
	Repository string `json:"repository"`
	// DefaultTag is deployed when no tag or digest is given.
	DefaultTag string `json:"default_tag"`
	// RegistryID is nil for public images.
	RegistryID *uint     `json:"registry_id"`
	Registry   *Registry `json:"registry,omitempty"`
	// Domain is nil for an app that isn't routed through Traefik.
	Domain *string `json:"domain"`
	// Port is the port the app listens on inside the container.
	Port     int `json:"port"`
	Replicas int `json:"replicas"`
	// HealthcheckCmd overrides the image's HEALTHCHECK. Nil keeps it.
	HealthcheckCmd *string `json:"healthcheck_cmd"`
	TLS            bool    `gorm:"column:tls" json:"tls"`
	// Template is the slug of the template the app was made from (ADR
	// 0025). Nil for a custom app.
	Template *string `json:"template"`
	// Args replace the image's CMD. Nil keeps it. Secrets never go here: a
	// template that needs one in an argument reads its env var through sh.
	Args    []string    `gorm:"serializer:json" json:"args"`
	Volumes []AppVolume `json:"volumes,omitempty"`
	// ExternalPort publishes Port on the host, for clients outside the
	// server (ADR 0026). Nil keeps the app on the overlay network only.
	ExternalPort *int `json:"external_port"`
	// SlowQueries has a template database that needs a flag for it collect
	// its slow queries (ADR 0027).
	SlowQueries bool `json:"slow_queries"`
	// Pooling runs a connection pooler beside a PostgreSQL app, as its own
	// Swarm service (ADR 0035). It always runs in transaction mode.
	Pooling bool `json:"pooling"`
	// CPUMillicores caps each task's CPU, 1000 to a core. Nil uses
	// Echowa's default, half the server (services.AppLimits).
	CPUMillicores *int `gorm:"column:cpu_millicores" json:"cpu_millicores"`
	// MemoryMiB caps each task's memory. Nil uses the default, half the
	// server's.
	MemoryMiB *int `gorm:"column:memory_mib" json:"memory_mib"`
	// SwapMiB is the swap each task may use past its memory limit, to slow
	// down rather than be killed. 0, the default, is none.
	SwapMiB int `gorm:"column:swap_mib" json:"swap_mib"`
	// Never serialised. Signs webhook requests (HMAC-SHA256).
	WebhookSecret       Secret `json:"-"`
	CurrentDeploymentID *uint  `json:"current_deployment_id"`
}

// ServiceName is the app's Swarm service name.
func (a *App) ServiceName() string {
	return ServicePrefix + a.Name
}

// PoolerSuffix ends the name of an app's connection pooler's service (ADR
// 0035), so an app called <name>-pooler has the service name <name>'s
// pooler would.
const PoolerSuffix = "-pooler"

// PoolerServiceName is the Swarm service name of the app's connection
// pooler.
func (a *App) PoolerServiceName() string {
	return a.ServiceName() + PoolerSuffix
}
