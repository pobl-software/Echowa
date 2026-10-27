package services

import (
	"context"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
)

// Limit units, as the app and the UI keep them.
const (
	nanoCPUsPerMillicore = 1_000_000
	bytesPerMiB          = 1 << 20
)

// ResourceLimits caps each of an app's tasks: CPU in millicores, 1000 to a
// core, and memory in MiB. Zero leaves a resource uncapped. SwapMiB is the
// swap a task may use past its memory limit, 0 for none.
type ResourceLimits struct {
	CPUMillicores int `json:"cpu_millicores"`
	MemoryMiB     int `json:"memory_mib"`
	SwapMiB       int `json:"swap_mib"`
}

// Docker is the limits as a service spec takes them.
func (l ResourceLimits) Docker() docker.Limits {
	return docker.Limits{
		NanoCPUs:    int64(l.CPUMillicores) * nanoCPUsPerMillicore,
		MemoryBytes: int64(l.MemoryMiB) * bytesPerMiB,
		SwapBytes:   int64(l.SwapMiB) * bytesPerMiB,
	}
}

func limitsFromDocker(l docker.Limits) ResourceLimits {
	return ResourceLimits{
		CPUMillicores: int(l.NanoCPUs / nanoCPUsPerMillicore),
		MemoryMiB:     int(l.MemoryBytes / bytesPerMiB),
		SwapMiB:       int(l.SwapBytes / bytesPerMiB),
	}
}

// HostLimits is the most a limit can be: the whole server.
func HostLimits(host docker.Host) ResourceLimits {
	return ResourceLimits{CPUMillicores: host.CPUs * 1000, MemoryMiB: int(host.MemoryBytes / bytesPerMiB)}
}

// HostSwapMiB is the server's swap, from the newest metrics reading. It's
// unknown before the first one.
func HostSwapMiB() (int, bool) {
	reading := LatestMetrics()
	if reading == nil || reading.Host.SwapTotal == nil {
		return 0, false
	}

	return int(*reading.Host.SwapTotal / bytesPerMiB), true
}

// DefaultLimits are half the server, so no one app can take all of it,
// however many there are. No app gets swap unless it asks.
func DefaultLimits(host docker.Host) ResourceLimits {
	whole := HostLimits(host)

	return ResourceLimits{CPUMillicores: whole.CPUMillicores / 2, MemoryMiB: whole.MemoryMiB / 2}
}

// AppLimits are the limits the app's tasks run with: its own where it set
// them, the defaults where it didn't.
func AppLimits(app *models.App, host docker.Host) ResourceLimits {
	limits := DefaultLimits(host)
	if app.CPUMillicores != nil {
		limits.CPUMillicores = *app.CPUMillicores
	}
	if app.MemoryMiB != nil {
		limits.MemoryMiB = *app.MemoryMiB
	}
	limits.SwapMiB = app.SwapMiB

	return limits
}

// ResourcesInput is the Resource limits form. Nil goes back to the
// default, which for swap is none.
type ResourcesInput struct {
	CPUMillicores *int `form:"cpu_millicores" json:"cpu_millicores"`
	MemoryMiB     *int `form:"memory_mib" json:"memory_mib"`
	SwapMiB       *int `form:"swap_mib" json:"swap_mib"`
}

// UpdateResources saves the app's limits. They apply on its next deploy.
func UpdateResources(ctx context.Context, app *models.App, in ResourcesInput) error {
	app.CPUMillicores, app.MemoryMiB = in.CPUMillicores, in.MemoryMiB
	app.SwapMiB = 0
	if in.SwapMiB != nil {
		app.SwapMiB = *in.SwapMiB
	}

	return facades.Orm().WithContext(ctx).Query().Save(app)
}
