package services

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"goravel/app/docker"
	"goravel/app/models"
)

func TestDefaultLimitsAreHalfTheServer(t *testing.T) {
	assert.Equal(t, ResourceLimits{CPUMillicores: 2000, MemoryMiB: 4096}, DefaultLimits(docker.Host{CPUs: 4, MemoryBytes: 8 << 30}))
	assert.Equal(t, ResourceLimits{CPUMillicores: 500, MemoryMiB: 957}, DefaultLimits(docker.Host{CPUs: 1, MemoryBytes: 1915 << 20}),
		"a one-core server's apps still get half a core each")
}

func TestAppLimitsUseTheDefaultForWhatTheAppLeavesUnset(t *testing.T) {
	host := docker.Host{CPUs: 4, MemoryBytes: 8 << 30}
	cpu, memory := 250, 512

	assert.Equal(t, DefaultLimits(host), AppLimits(&models.App{}, host))
	assert.Equal(t, ResourceLimits{CPUMillicores: 250, MemoryMiB: 4096}, AppLimits(&models.App{CPUMillicores: &cpu}, host))
	assert.Equal(t, ResourceLimits{CPUMillicores: 2000, MemoryMiB: 512}, AppLimits(&models.App{MemoryMiB: &memory}, host))
}

func TestResourceLimitsConvertToDockersUnitsAndBack(t *testing.T) {
	limits := ResourceLimits{CPUMillicores: 1500, MemoryMiB: 768}

	assert.Equal(t, docker.Limits{NanoCPUs: 1_500_000_000, MemoryBytes: 768 << 20}, limits.Docker())
	assert.Equal(t, limits, limitsFromDocker(limits.Docker()))
	withSwap := ResourceLimits{CPUMillicores: 1500, MemoryMiB: 768, SwapMiB: 256}
	assert.EqualValues(t, 256<<20, withSwap.Docker().SwapBytes)
	assert.Equal(t, withSwap, limitsFromDocker(withSwap.Docker()))
	assert.Equal(t, docker.Limits{}, ResourceLimits{}.Docker(), "zero stays uncapped")
}
